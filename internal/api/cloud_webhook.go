package api

// Webhooks da WhatsApp Cloud API (Meta).
// Não interfere no fluxo whatsmeow — apenas injeta InboundJob na fila igual
// ao buildMessageHandler faz para QR Code, mantendo processor.ProcessInbound
// agnóstico de origem.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/queue"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/whatsapp"
	"go.uber.org/zap"
)

// GET /webhook/cloud/:session_id — verificação inicial do webhook.
// Meta envia hub.mode=subscribe, hub.verify_token, hub.challenge.
// Retornamos hub.challenge se o token bater com o cadastrado na sessão.
func (h *handlers) cloudWebhookVerify(c *fiber.Ctx) error {
	sessionIDStr := c.Params("session_id")
	id, err := uuid.Parse(sessionIDStr)
	if err != nil {
		return c.Status(400).SendString("invalid session_id")
	}
	sess, err := h.repo.GetSessionByID(c.Context(), id)
	if err != nil || sess == nil || sess.Type != db.SessionTypeCloudAPI {
		return c.Status(404).SendString("session not found")
	}

	mode := c.Query("hub.mode")
	token := c.Query("hub.verify_token")
	challenge := c.Query("hub.challenge")

	if mode != "subscribe" {
		return c.Status(400).SendString("invalid mode")
	}
	if sess.CloudVerifyToken == "" || token != sess.CloudVerifyToken {
		h.log.Warn("cloud webhook verify: token mismatch",
			zap.String("session_id", sessionIDStr))
		return c.Status(403).SendString("invalid verify_token")
	}
	h.log.Info("cloud webhook verified", zap.String("session_id", sessionIDStr))
	return c.SendString(challenge)
}

// POST /webhook/cloud/:session_id — recebe eventos (mensagens, statuses).
func (h *handlers) cloudWebhookReceive(c *fiber.Ctx) error {
	sessionIDStr := c.Params("session_id")
	id, err := uuid.Parse(sessionIDStr)
	if err != nil {
		return c.Status(400).SendString("invalid session_id")
	}
	sess, err := h.repo.GetSessionByID(c.Context(), id)
	if err != nil || sess == nil || sess.Type != db.SessionTypeCloudAPI {
		return c.Status(404).SendString("session not found")
	}
	body := c.Body()

	// Validação de assinatura HMAC-SHA256 (X-Hub-Signature-256: sha256=<hex>).
	if sess.CloudAppSecret != "" {
		got := c.Get("X-Hub-Signature-256")
		if !verifyMetaSignature(sess.CloudAppSecret, body, got) {
			h.log.Warn("cloud webhook: invalid signature",
				zap.String("session_id", sessionIDStr))
			return c.Status(401).SendString("invalid signature")
		}
	}

	// Parse: { object, entry: [ { id, changes: [ { value: { messages, contacts, statuses } } ] } ] }
	var payload cloudWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		h.log.Warn("cloud webhook: invalid json",
			zap.String("session_id", sessionIDStr), zap.Error(err))
		// Retornamos 200 mesmo assim — Meta retransmite se receber 4xx/5xx
		return c.SendStatus(200)
	}

	// Para cada mudança, processa cada mensagem.
	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			if change.Field != "messages" {
				continue
			}
			h.processCloudMessages(c.Context(), sess, change.Value)
		}
	}
	return c.SendStatus(200)
}

// processCloudMessages converte cada msg/status do payload em InboundJob.
func (h *handlers) processCloudMessages(ctx context.Context, sess *db.WhatsAppSession, v cloudChangeValue) {
	// Mapeia contacts por wa_id para pegar profile.name.
	nameByWAID := map[string]string{}
	for _, ct := range v.Contacts {
		if ct.Profile.Name != "" {
			nameByWAID[ct.WAID] = ct.Profile.Name
		}
	}

	for _, m := range v.Messages {
		// Dedup por wamid (Meta as vezes reenvia o mesmo evento).
		if m.ID != "" {
			first, derr := h.q.MarkProcessed(ctx, "cloud:msg:"+m.ID, 24*time.Hour)
			if derr == nil && !first {
				h.log.Info("cloud webhook: duplicate ignored", zap.String("wamid", m.ID))
				continue
			}
		}
		h.handleCloudInboundMessage(ctx, sess, m, nameByWAID[m.From])
	}

	// Statuses: poderiam atualizar messages.status — fora do escopo principal.
	for _, st := range v.Statuses {
		h.log.Debug("cloud webhook: status",
			zap.String("wamid", st.ID), zap.String("status", st.Status))
		_ = st
	}
}

func (h *handlers) handleCloudInboundMessage(ctx context.Context, sess *db.WhatsAppSession, m cloudMessage, contactName string) {
	// from = telefone E.164 sem @. Construímos JID padrão do sistema.
	fromPhone := normalizeWAPhone(m.From)
	if fromPhone == "" {
		h.log.Warn("cloud webhook: msg without from", zap.String("wamid", m.ID))
		return
	}
	fromJID := fromPhone + "@s.whatsapp.net"

	// Resolve texto + mídia conforme o tipo.
	text := ""
	msgType := db.MsgTypeText
	var mediaData []byte
	mediaMime := ""
	mediaName := ""
	mediaID := ""

	switch m.Type {
	case "text":
		if m.Text != nil {
			text = m.Text.Body
		}
	case "image":
		msgType = db.MsgTypeImage
		if m.Image != nil {
			mediaID = m.Image.ID
			mediaMime = m.Image.MimeType
			text = m.Image.Caption
			mediaName = "image" + extFromMime(mediaMime, ".jpg")
		}
	case "audio", "voice":
		msgType = db.MsgTypeAudio
		if m.Audio != nil {
			mediaID = m.Audio.ID
			mediaMime = m.Audio.MimeType
			mediaName = "audio" + extFromMime(mediaMime, ".ogg")
		}
	case "video":
		msgType = db.MsgTypeVideo
		if m.Video != nil {
			mediaID = m.Video.ID
			mediaMime = m.Video.MimeType
			text = m.Video.Caption
			mediaName = "video" + extFromMime(mediaMime, ".mp4")
		}
	case "document":
		msgType = db.MsgTypeDocument
		if m.Document != nil {
			mediaID = m.Document.ID
			mediaMime = m.Document.MimeType
			text = m.Document.Caption
			mediaName = m.Document.Filename
			if mediaName == "" {
				mediaName = "document" + extFromMime(mediaMime, "")
			}
		}
	case "sticker":
		msgType = db.MsgTypeImage
		if m.Sticker != nil {
			mediaID = m.Sticker.ID
			mediaMime = m.Sticker.MimeType
			mediaName = "sticker.webp"
		}
	case "button":
		if m.Button != nil {
			text = m.Button.Text
		}
	case "interactive":
		if m.Interactive != nil {
			if m.Interactive.ButtonReply != nil {
				text = m.Interactive.ButtonReply.Title
			} else if m.Interactive.ListReply != nil {
				text = m.Interactive.ListReply.Title
			}
		}
	case "location":
		if m.Location != nil {
			text = "[Localização: " + m.Location.Name + "]"
		}
	default:
		text = "[" + m.Type + "]"
	}

	// Baixa mídia se houver mediaID. Se falhar, segue com texto/placeholder.
	if mediaID != "" && h.cloudMgr != nil {
		data, mime, err := h.cloudMgr.DownloadMedia(ctx, sess.JID, mediaID)
		if err != nil {
			h.log.Warn("cloud webhook: media download failed",
				zap.String("media_id", mediaID), zap.Error(err))
		} else {
			mediaData = data
			if mediaMime == "" {
				mediaMime = mime
			}
		}
	}

	// Salva no banco com status "received" — mesmo padrão do whatsmeow path.
	now := time.Now()
	dbMsg := &db.Message{
		ID:          uuid.New(),
		WAMessageID: m.ID,
		SessionID:   &sess.ID,
		FromJID:     fromJID,
		ToJID:       sess.JID,
		AuthorName:  contactName,
		Direction:   db.DirInbound,
		MessageType: msgType,
		Content:     text,
		MediaMime:   mediaMime,
		Status:      db.MsgReceived,
		SentAt:      &now,
	}
	if err := h.repo.InsertMessage(ctx, dbMsg); err != nil {
		h.log.Warn("cloud webhook: insert message failed",
			zap.String("wamid", m.ID), zap.Error(err))
	}

	// Enfileira para o processor entregar no Bitrix Open Lines.
	job := &queue.InboundJob{
		SessionID:   sess.ID,
		SessionJID:  sess.JID,
		FromJID:     fromJID,
		FromPhone:   fromPhone,
		FromName:    contactName,
		MessageID:   m.ID,
		MessageType: string(msgType),
		Text:        text,
		MediaData:   mediaData,
		MediaName:   mediaName,
		MediaMime:   mediaMime,
	}
	if err := h.q.PushInbound(ctx, job); err != nil {
		h.log.Error("cloud webhook: push inbound failed",
			zap.String("wamid", m.ID), zap.Error(err))
		return
	}
	h.log.Info("cloud inbound queued",
		zap.String("from", fromPhone), zap.String("type", string(msgType)),
		zap.String("session", sess.JID))
}

// ─── Helpers ───────────────────────────────────────────────────────────────

// verifyMetaSignature compara HMAC-SHA256(body, app_secret) com header "sha256=<hex>".
func verifyMetaSignature(appSecret string, body []byte, header string) bool {
	if !strings.HasPrefix(header, "sha256=") {
		return false
	}
	expected := strings.TrimPrefix(header, "sha256=")
	mac := hmac.New(sha256.New, []byte(appSecret))
	mac.Write(body)
	got := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(got))
}

func extFromMime(mime, fallback string) string {
	switch mime {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "audio/ogg", "audio/opus":
		return ".ogg"
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/mp4", "audio/m4a":
		return ".m4a"
	case "video/mp4":
		return ".mp4"
	case "application/pdf":
		return ".pdf"
	}
	return fallback
}

// ─── Estruturas do payload ─────────────────────────────────────────────────

type cloudWebhookPayload struct {
	Object string             `json:"object"`
	Entry  []cloudEntry       `json:"entry"`
}

type cloudEntry struct {
	ID      string         `json:"id"`
	Changes []cloudChange  `json:"changes"`
}

type cloudChange struct {
	Field string           `json:"field"`
	Value cloudChangeValue `json:"value"`
}

type cloudChangeValue struct {
	MessagingProduct string          `json:"messaging_product"`
	Metadata         cloudMetadata   `json:"metadata"`
	Contacts         []cloudContact  `json:"contacts"`
	Messages         []cloudMessage  `json:"messages"`
	Statuses         []cloudStatus   `json:"statuses"`
}

type cloudMetadata struct {
	DisplayPhoneNumber string `json:"display_phone_number"`
	PhoneNumberID      string `json:"phone_number_id"`
}

type cloudContact struct {
	Profile struct {
		Name string `json:"name"`
	} `json:"profile"`
	WAID string `json:"wa_id"`
}

type cloudMessage struct {
	ID          string                  `json:"id"`
	From        string                  `json:"from"`
	Timestamp   string                  `json:"timestamp"`
	Type        string                  `json:"type"`
	Text        *cloudText              `json:"text,omitempty"`
	Image       *cloudMedia             `json:"image,omitempty"`
	Audio       *cloudMedia             `json:"audio,omitempty"`
	Video       *cloudMedia             `json:"video,omitempty"`
	Document    *cloudMedia             `json:"document,omitempty"`
	Sticker     *cloudMedia             `json:"sticker,omitempty"`
	Button      *cloudButton            `json:"button,omitempty"`
	Interactive *cloudInteractive       `json:"interactive,omitempty"`
	Location    *cloudLocation          `json:"location,omitempty"`
	Context     *cloudMessageContext    `json:"context,omitempty"`
}

type cloudText struct {
	Body string `json:"body"`
}

type cloudMedia struct {
	ID       string `json:"id"`
	MimeType string `json:"mime_type"`
	SHA256   string `json:"sha256"`
	Caption  string `json:"caption,omitempty"`
	Filename string `json:"filename,omitempty"`
}

type cloudButton struct {
	Payload string `json:"payload"`
	Text    string `json:"text"`
}

type cloudInteractive struct {
	Type        string `json:"type"`
	ButtonReply *struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	} `json:"button_reply,omitempty"`
	ListReply *struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	} `json:"list_reply,omitempty"`
}

type cloudLocation struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Name      string  `json:"name"`
	Address   string  `json:"address"`
}

type cloudMessageContext struct {
	From string `json:"from"`
	ID   string `json:"id"`
}

type cloudStatus struct {
	ID          string `json:"id"`
	Status      string `json:"status"` // sent, delivered, read, failed
	Timestamp   string `json:"timestamp"`
	RecipientID string `json:"recipient_id"`
}

// Garantir uso do whatsapp para evitar import dropped por linter
var _ = whatsapp.IsCloudJID
