package api

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/bitrix"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/config"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/queue"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/telemetry"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/whatsapp"
	"go.uber.org/zap"
)

type handlers struct {
	cfg          *config.Config
	repo         *db.Repository
	waManager    *whatsapp.Manager
	bitrixClient *bitrix.Client
	q            *queue.Queue
	metrics      *telemetry.Metrics
	log          *zap.Logger
}

func newHandlers(
	cfg *config.Config,
	repo *db.Repository,
	waManager *whatsapp.Manager,
	bitrixClient *bitrix.Client,
	q *queue.Queue,
	metrics *telemetry.Metrics,
	log *zap.Logger,
) *handlers {
	return &handlers{cfg: cfg, repo: repo, waManager: waManager, bitrixClient: bitrixClient, q: q, metrics: metrics, log: log}
}

// GET /health
func (h *handlers) health(c *fiber.Ctx) error {
	sessions := h.waManager.ListSessions()
	in, out, dead := h.q.Lengths(c.Context())
	return c.JSON(fiber.Map{
		"status":           "ok",
		"active_sessions":  len(sessions),
		"queue_inbound":    in,
		"queue_outbound":   out,
		"queue_dead":       dead,
	})
}

// POST /wa/sessions
// Body: { "phone": "5511999999999" }
// Retorna imediatamente. QR disponível em GET /wa/sessions/:phone/qr
func (h *handlers) addSession(c *fiber.Ctx) error {
	var body struct {
		Phone string `json:"phone"`
	}
	if err := c.BodyParser(&body); err != nil || body.Phone == "" {
		return fiber.NewError(fiber.StatusBadRequest, "phone required")
	}

	if err := h.waManager.AddSession(c.Context(), body.Phone); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}

	return c.JSON(fiber.Map{
		"status": "connecting",
		"phone":  body.Phone,
		"qr_url": "/wa/sessions/" + body.Phone + "/qr",
	})
}

// GET /wa/sessions/:phone/qr — retorna o QR code atual (polling)
func (h *handlers) getSessionQR(c *fiber.Ctx) error {
	phone := c.Params("phone")
	qr := h.waManager.GetQR(phone)
	if qr == "" {
		return c.JSON(fiber.Map{"status": "waiting", "qr": ""})
	}
	return c.JSON(fiber.Map{"status": "ready", "qr": qr})
}

// GET /wa/sessions
func (h *handlers) listSessions(c *fiber.Ctx) error {
	jids := h.waManager.ListSessions()
	return c.JSON(fiber.Map{"sessions": jids, "count": len(jids)})
}

// DELETE /wa/sessions/:jid
func (h *handlers) removeSession(c *fiber.Ctx) error {
	jid := c.Params("jid")
	h.waManager.Disconnect(jid)
	return c.JSON(fiber.Map{"status": "disconnected", "jid": jid})
}

// POST /wa/send
// Body: { "session_jid": "...", "to": "5511999999999@s.whatsapp.net", "text": "Olá!" }
func (h *handlers) sendMessage(c *fiber.Ctx) error {
	var body struct {
		SessionJID string `json:"session_jid"`
		To         string `json:"to"`
		Text       string `json:"text"`
	}
	if err := c.BodyParser(&body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	if body.SessionJID == "" || body.To == "" || body.Text == "" {
		return fiber.NewError(fiber.StatusBadRequest, "session_jid, to, text required")
	}

	// Coloca na fila de saída (não bloqueia a request)
	if err := h.q.PushOutbound(c.Context(), &queue.OutboundJob{
		SessionJID: body.SessionJID,
		ToJID:      body.To,
		Text:       body.Text,
	}); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}

	return c.JSON(fiber.Map{"status": "queued"})
}

// GET|POST /bitrix/callback — handler de instalação do app local Bitrix24
// O Bitrix24 chama este endpoint com event=ONAPPINSTALL e auth[access_token]
func (h *handlers) bitrixOAuthCallback(c *fiber.Ctx) error {
	h.log.Info("bitrix callback received",
		zap.String("method", c.Method()),
		zap.String("raw_body", string(c.Body())),
		zap.String("content_type", c.Get("Content-Type")),
	)

	// App local envia form-encoded
	event := c.FormValue("event")
	accessToken := c.FormValue("auth[access_token]")
	refreshToken := c.FormValue("auth[refresh_token]")
	domain := c.FormValue("auth[domain]")
	expiresIn := c.FormValue("auth[expires_in]")

	// Fallback: tenta JSON
	if accessToken == "" {
		var body struct {
			Event string `json:"event"`
			Auth  struct {
				AccessToken  string `json:"access_token"`
				RefreshToken string `json:"refresh_token"`
				Domain       string `json:"domain"`
				ExpiresIn    int    `json:"expires_in"`
			} `json:"auth"`
		}
		if err := c.BodyParser(&body); err == nil {
			event = body.Event
			accessToken = body.Auth.AccessToken
			refreshToken = body.Auth.RefreshToken
			domain = body.Auth.Domain
		}
	}

	preview := accessToken
	if len(preview) > 10 {
		preview = preview[:10]
	}
	h.log.Info("bitrix install event",
		zap.String("event", event),
		zap.String("domain", domain),
		zap.String("token_preview", preview))

	if accessToken == "" {
		return fiber.NewError(fiber.StatusBadRequest, "access_token missing")
	}

	if domain == "" {
		domain = h.cfg.Bitrix.Domain
	}

	exp := 3600
	if expiresIn != "" {
		fmt.Sscanf(expiresIn, "%d", &exp)
	}

	if err := h.bitrixClient.SaveToken(c.Context(), domain, accessToken, refreshToken, exp); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}

	// Registra conector, ativa na Open Line e vincula evento de reply (background)
	eventURL := h.cfg.Bitrix.RedirectURI // base: https://<dominio>/bitrix/callback
	// Deriva a URL do evento trocando o sufixo
	eventURL = strings.TrimSuffix(eventURL, "/bitrix/callback") + "/bitrix/connector/event"
	go func() {
		ctx := context.Background()
		handlerURL := h.cfg.Bitrix.RedirectURI
		if err := h.bitrixClient.RegisterConnector(ctx, "whatsapp_uc", "WhatsApp UC", handlerURL); err != nil {
			h.log.Warn("imconnector.register failed", zap.Error(err))
		} else {
			h.log.Info("imconnector registered")
		}
		if err := h.bitrixClient.ActivateConnector(ctx, "whatsapp_uc", h.cfg.Bitrix.OpenLineID, true); err != nil {
			h.log.Warn("imconnector.activate failed", zap.Error(err))
		} else {
			h.log.Info("imconnector activated", zap.Int("line_id", h.cfg.Bitrix.OpenLineID))
		}
		// Registra o webhook de reply do operador via event.bind
		if err := h.bitrixClient.BindEvent(ctx, "ONIMCONNECTORMESSAGEADD", eventURL); err != nil {
			h.log.Warn("event.bind ONIMCONNECTORMESSAGEADD failed", zap.Error(err))
		} else {
			h.log.Info("event.bind ONIMCONNECTORMESSAGEADD ok", zap.String("url", eventURL))
		}
	}()

	return c.SendStatus(fiber.StatusOK)
}

// GET /bitrix/oauth/start — não usado em app local, mantido por compatibilidade
func (h *handlers) bitrixOAuthStart(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"info": "App local Bitrix24 — autorização via installation handler em POST /bitrix/callback",
	})
}


// POST /bitrix/connector/event — recebe ONIMCONNECTORMESSAGEADD do Bitrix24
// O operador respondeu no Contact Center → encaminha para o WhatsApp correto
func (h *handlers) bitrixConnectorEvent(c *fiber.Ctx) error {
	h.log.Info("connector event received", zap.String("body", string(c.Body())))

	// Bitrix envia form-encoded
	connector := c.FormValue("data[CONNECTOR]")
	lineStr := c.FormValue("data[LINE]")
	chatID := c.FormValue("data[MESSAGES][0][chat][id]")   // JID que definimos (FromJID)
	text := c.FormValue("data[MESSAGES][0][message][text]")
	msgID := c.FormValue("data[MESSAGES][0][message][id]")
	userID := c.FormValue("data[MESSAGES][0][message][user_id]")

	h.log.Info("connector event parsed",
		zap.String("connector", connector),
		zap.String("line", lineStr),
		zap.String("chat_id", chatID),
		zap.String("user_id", userID),
		zap.String("text", text))

	// user_id=0 é mensagem automática do sistema (welcome message), ignora
	if userID == "0" || userID == "" {
		return c.SendStatus(fiber.StatusOK)
	}
	if text == "" || chatID == "" {
		return c.SendStatus(fiber.StatusOK)
	}

	// Remove BBCode do Bitrix24 ([b]...[/b], [br], etc.)
	cleanText := stripBBCode(text)

	// Sanitiza o JID: "127586399207476:47@lid" → "127586399207476@s.whatsapp.net"
	toJID := sanitizeJID(chatID)

	ctx := context.Background()

	// Busca o contato pelo JID original (como foi salvo no banco)
	contact, err := h.repo.GetContactByWAJID(ctx, chatID)
	if err != nil {
		h.log.Warn("connector event: contact not found", zap.String("jid", chatID), zap.Error(err))
		return c.SendStatus(fiber.StatusOK)
	}

	// Descobre qual sessão WA usar
	sessionJID := ""
	if contact.SessionID != nil {
		sess, err := h.repo.GetSessionByID(ctx, *contact.SessionID)
		if err == nil {
			sessionJID = sess.JID
		}
	}
	if sessionJID == "" {
		h.log.Warn("connector event: no session found for contact", zap.String("jid", chatID))
		return c.SendStatus(fiber.StatusOK)
	}

	// Coloca na fila de saída
	if err := h.q.PushOutbound(ctx, &queue.OutboundJob{
		SessionJID: sessionJID,
		ToJID:      toJID,
		Text:       cleanText,
	}); err != nil {
		h.log.Error("connector event: push outbound failed", zap.Error(err))
		return c.SendStatus(fiber.StatusOK)
	}

	// Confirma entrega ao Bitrix (background com contexto próprio)
	line := 218
	fmt.Sscanf(lineStr, "%d", &line)
	go func(conn string, ln int, mID string) {
		_ = h.bitrixClient.ConnectorSetDelivery(context.Background(), conn, ln, mID)
	}(connector, line, msgID)

	h.log.Info("operator reply queued",
		zap.String("to_jid", toJID),
		zap.String("session", sessionJID),
		zap.String("text", cleanText))

	return c.SendStatus(fiber.StatusOK)
}

// sanitizeJID normaliza JIDs do Bitrix para formato aceito pelo whatsmeow.
// "127586399207476:47@lid" → "127586399207476@lid" (remove device part, mantém @lid)
// "5511999@s.whatsapp.net" → mantém como está
func sanitizeJID(jid string) string {
	if idx := strings.Index(jid, ":"); idx != -1 {
		suffix := ""
		if at := strings.Index(jid, "@"); at != -1 {
			suffix = jid[at:] // "@lid" ou "@s.whatsapp.net"
		}
		return jid[:idx] + suffix
	}
	return jid
}

// stripBBCode remove tags BBCode do Bitrix24 ([b], [/b], [br], etc.).
func stripBBCode(s string) string {
	// [br] → newline
	s = strings.ReplaceAll(s, "[br]", "\n")
	// Remove todas as outras tags [tag] e [/tag]
	result := strings.Builder{}
	inTag := false
	for _, ch := range s {
		if ch == '[' {
			inTag = true
			continue
		}
		if ch == ']' {
			inTag = false
			continue
		}
		if !inTag {
			result.WriteRune(ch)
		}
	}
	return strings.TrimSpace(result.String())
}

// POST /bitrix/webhook — recebe mensagens do operador via Bitrix
func (h *handlers) bitrixWebhook(c *fiber.Ctx) error {
	var payload struct {
		Event string `json:"event"`
		Data  struct {
			SessionID string `json:"SESSION_ID"`
			Message   string `json:"MESSAGE"`
			UserPhone string `json:"USER_PHONE"`
			SessionJID string `json:"WA_SESSION_JID"`
			ToJID     string `json:"WA_TO_JID"`
		} `json:"data"`
	}
	if err := c.BodyParser(&payload); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	if payload.Data.Message == "" {
		return c.SendStatus(fiber.StatusNoContent)
	}

	if err := h.q.PushOutbound(c.Context(), &queue.OutboundJob{
		SessionJID: payload.Data.SessionJID,
		ToJID:      payload.Data.ToJID,
		Text:       payload.Data.Message,
	}); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}

	return c.SendStatus(fiber.StatusOK)
}

// GET /stats/daily?days=7
func (h *handlers) dailyStats(c *fiber.Ctx) error {
	days, _ := strconv.Atoi(c.Query("days", "7"))
	if days < 1 || days > 90 {
		days = 7
	}
	stats, err := h.repo.GetDailyStats(c.Context(), days)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	return c.JSON(stats)
}

// GET /stats/queues
func (h *handlers) queueStats(c *fiber.Ctx) error {
	in, out, dead := h.q.Lengths(c.Context())
	return c.JSON(fiber.Map{
		"inbound":  in,
		"outbound": out,
		"dead":     dead,
	})
}
