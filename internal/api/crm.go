package api

// Handlers para a aba WhatsApp no CRM do Bitrix24.
//
// Fluxo:
//   1. placement.bind registra CRM_CONTACT_DETAIL_TAB (e LEAD, DEAL) apontando para GET /bitrix/crm/tab
//   2. Bitrix abre a URL em iframe quando o operador abre um Contato/Lead/Deal
//   3. A página usa BX24.placement.info() para descobrir entityType e entityId
//   4. Busca o telefone do contato via /bitrix/crm/entity e exibe sessões WA disponíveis
//   5. Operador clica "Iniciar conversa" → POST /bitrix/crm/send que abre a sessão e envia

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/bitrix"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/queue"
	"go.uber.org/zap"
)

// GET|POST /bitrix/crm/tab — página iframe exibida na aba WhatsApp do CRM
func (h *handlers) bitrixCRMTab(c *fiber.Ctx) error {
	return c.Type("html").SendString(crmTabHTML)
}

// GET /bitrix/crm/entity?domain=...&entity_type=contact|lead|deal&entity_id=...
// Retorna { phone, name } do contato/lead/deal para o iframe popular o formulário.
func (h *handlers) bitrixCRMEntity(c *fiber.Ctx) error {
	domain := c.Query("domain")
	entityType := strings.ToLower(c.Query("entity_type", "contact"))
	entityID := c.Query("entity_id")

	if domain == "" || entityID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "domain e entity_id são obrigatórios"})
	}

	portal, err := h.repo.GetBitrixPortalByDomain(c.Context(), normalizePortalDomain(domain))
	if err != nil || portal == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "portal não encontrado: " + domain})
	}
	creds := h.portalToCreds(portal)

	var raw json.RawMessage
	switch entityType {
	case "lead":
		raw, err = h.bitrixClient.GetLead(c.Context(), creds, entityID)
	case "deal":
		raw, err = h.bitrixClient.GetDeal(c.Context(), creds, entityID)
	default:
		raw, err = h.bitrixClient.GetContact(c.Context(), creds, entityID)
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	// Extrai nome e telefone do objeto retornado
	var obj map[string]json.RawMessage
	if e := json.Unmarshal(raw, &obj); e != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "parse error"})
	}

	name := jsonStr(obj, "NAME") + " " + jsonStr(obj, "LAST_NAME")
	name = strings.TrimSpace(name)
	if name == "" {
		name = jsonStr(obj, "TITLE")
	}

	// PHONE é array de objetos [{VALUE: "55...", VALUE_TYPE: "WORK"}, ...]
	phone := extractPhone(obj)

	// Sessões WA disponíveis
	sessions := h.waManager.ListSessions()

	return c.JSON(fiber.Map{
		"name":     name,
		"phone":    phone,
		"sessions": sessions,
	})
}

// POST /bitrix/crm/send
// Body: { "domain", "entity_type", "entity_id", "phone", "session_jid", "message", "line_id" }
// Abre a sessão de Open Channel e envia a primeira mensagem via WA.
func (h *handlers) bitrixCRMSend(c *fiber.Ctx) error {
	var body struct {
		Domain     string `json:"domain"`
		EntityType string `json:"entity_type"`
		EntityID   string `json:"entity_id"`
		Phone      string `json:"phone"`
		SessionJID string `json:"session_jid"`
		Message    string `json:"message"`
		LineID     int    `json:"line_id"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if body.Domain == "" || body.Phone == "" || body.SessionJID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "domain, phone e session_jid são obrigatórios",
		})
	}
	if body.Message == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "message é obrigatório"})
	}

	portal, err := h.repo.GetBitrixPortalByDomain(c.Context(), normalizePortalDomain(body.Domain))
	if err != nil || portal == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "portal não encontrado"})
	}
	creds := h.portalToCreds(portal)

	// Normaliza o telefone para formato WA (apenas dígitos)
	phone := normalizeWAPhone(body.Phone)
	toJID := phone + "@s.whatsapp.net"

	connectorID := portal.ConnectorID
	if connectorID == "" {
		connectorID = "whatsapp_uc_v2"
	}
	lineID := body.LineID
	if lineID == 0 {
		lineID = portal.OpenLineID
	}
	if lineID == 0 {
		lineID = 1
	}

	// USER_CODE: "<connector>|<lineID>|<ext_chat_id>|<ext_user_id>"
	// ext_chat_id e ext_user_id usamos o número de telefone normalizado
	userCode := fmt.Sprintf("%s|%d|%s|%s", connectorID, lineID, phone, phone)

	chatID, err := h.bitrixClient.OpenChatSessionByCode(c.Context(), creds, userCode)
	if err != nil {
		h.log.Warn("crm send: imopenlines.session.open failed", zap.String("user_code", userCode), zap.Error(err))
		// Continua mesmo se a sessão falhar — a mensagem WA ainda será enviada
	} else {
		h.log.Info("crm send: session opened", zap.String("chat_id", chatID), zap.String("user_code", userCode))
	}

	// Enfileira mensagem outbound para o WhatsApp
	job := &queue.OutboundJob{
		SessionJID:      body.SessionJID,
		ToJID:           toJID,
		Text:            body.Message,
		BitrixConnector: connectorID,
		BitrixLine:      lineID,
	}
	if err := h.q.PushOutbound(c.Context(), job); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "falha ao enfileirar mensagem: " + err.Error()})
	}

	h.log.Info("crm send: outbound queued",
		zap.String("to_jid", toJID),
		zap.String("session_jid", body.SessionJID),
		zap.String("domain", body.Domain),
	)

	return c.JSON(fiber.Map{
		"status":  "queued",
		"to_jid":  toJID,
		"chat_id": chatID,
	})
}

// POST /bitrix/crm/upload — recebe arquivo multipart e enfileira envio via WA
// Form fields: domain, phone, session_jid + file (multipart)
func (h *handlers) bitrixCRMUpload(c *fiber.Ctx) error {
	domain     := c.FormValue("domain")
	phone      := c.FormValue("phone")
	sessionJID := c.FormValue("session_jid")
	caption    := c.FormValue("caption") // texto opcional junto ao arquivo

	if domain == "" || phone == "" || sessionJID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "domain, phone e session_jid são obrigatórios"})
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "arquivo obrigatório"})
	}

	portal, err := h.repo.GetBitrixPortalByDomain(c.Context(), normalizePortalDomain(domain))
	if err != nil || portal == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "portal não encontrado"})
	}

	phone = normalizeWAPhone(phone)
	toJID := phone + "@s.whatsapp.net"

	connectorID := portal.ConnectorID
	if connectorID == "" {
		connectorID = "whatsapp_uc_v2"
	}
	lineID := portal.OpenLineID
	if lineID == 0 {
		lineID = 1
	}

	// Lê bytes do arquivo
	f, err := fileHeader.Open()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "erro ao ler arquivo"})
	}
	defer f.Close()

	// Detecta MIME pelo Content-Type do campo ou pelo nome
	mime := fileHeader.Header.Get("Content-Type")
	if mime == "" || mime == "application/octet-stream" {
		mime = guessMime(fileHeader.Filename)
	}

	job := &queue.OutboundJob{
		SessionJID:      sessionJID,
		ToJID:           toJID,
		Text:            caption,
		FileName:        fileHeader.Filename,
		FileMime:        mime,
		BitrixConnector: connectorID,
		BitrixLine:      lineID,
	}

	// Para arquivos pequenos (<= 64 MB) embute os bytes na fila via MediaURL data URI
	const maxEmbed = 64 << 20
	if fileHeader.Size <= maxEmbed {
		buf := make([]byte, fileHeader.Size)
		if _, rerr := f.Read(buf); rerr == nil {
			job.MediaURL = "data:" + mime + ";base64," + b64Encode(buf)
		}
	}

	if err := h.q.PushOutbound(c.Context(), job); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	h.log.Info("crm upload queued",
		zap.String("file", fileHeader.Filename),
		zap.String("to", toJID),
		zap.String("session", sessionJID),
	)
	return c.JSON(fiber.Map{"status": "queued", "file": fileHeader.Filename})
}

// GET /bitrix/crm/history?domain=...&entity_type=contact&entity_id=...&phone=...&limit=80
// Busca histórico do banco local (fonte primária) + fallback Bitrix API.
func (h *handlers) bitrixCRMHistory(c *fiber.Ctx) error {
	domain     := c.Query("domain")
	entityType := strings.ToLower(c.Query("entity_type", "contact"))
	entityID   := c.Query("entity_id")
	phone      := c.Query("phone") // telefone já conhecido pelo frontend

	if domain == "" || entityID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "domain e entity_id são obrigatórios"})
	}

	limit := 80
	if l := c.QueryInt("limit", 80); l > 0 && l <= 200 {
		limit = l
	}

	// ── Fonte 1: banco local ──────────────────────────────────────────────
	// Se o frontend passou o telefone, busca direto no banco
	if phone != "" {
		phoneNorm := normalizeWAPhone(phone)
		localMsgs, err := h.repo.GetMessagesByPhone(c.Context(), phoneNorm, limit)
		if err == nil && len(localMsgs) > 0 {
			h.log.Info("crm history: found in local db", zap.Int("count", len(localMsgs)), zap.String("phone", phoneNorm))
			return c.JSON(fiber.Map{
				"messages": localMsgsToCRM(localMsgs),
				"count":    len(localMsgs),
				"source":   "local",
			})
		}
	}

	// ── Fonte 2: Bitrix API ───────────────────────────────────────────────
	portal, err := h.repo.GetBitrixPortalByDomain(c.Context(), normalizePortalDomain(domain))
	if err != nil || portal == nil {
		return c.JSON(fiber.Map{"messages": []interface{}{}, "count": 0})
	}
	creds := h.portalToCreds(portal)
	bxEntityType := strings.ToUpper(entityType)

	// Tenta getLastId primeiro, depois crm.chat.get
	chatID, _ := h.bitrixClient.GetCRMChatLastID(c.Context(), creds, bxEntityType, entityID)
	if chatID == "" {
		if chatsRaw, e := h.bitrixClient.GetCRMChats(c.Context(), creds, bxEntityType, entityID); e == nil {
			chatID = extractChatID(chatsRaw)
		}
	}
	h.log.Info("crm history: bitrix chat_id", zap.String("chat_id", chatID), zap.String("entity", entityID))

	if chatID == "" {
		return c.JSON(fiber.Map{"messages": []interface{}{}, "count": 0, "source": "none"})
	}

	msgsRaw, err := h.bitrixClient.GetSessionHistory(c.Context(), creds, chatID, limit)
	if err != nil {
		msgsRaw, err = h.bitrixClient.GetChatMessages(c.Context(), creds, chatID, limit)
		if err != nil {
			return c.JSON(fiber.Map{"messages": []interface{}{}, "count": 0, "source": "error"})
		}
	}

	msgs := parseSessionHistory(msgsRaw, portal.ConnectorID)
	if len(msgs) == 0 {
		msgs = parseBitrixMessages(msgsRaw, portal.ConnectorID)
	}

	h.log.Info("crm history: from bitrix", zap.Int("count", len(msgs)))
	return c.JSON(fiber.Map{"messages": msgs, "count": len(msgs), "source": "bitrix", "chat_id": chatID})
}

// localMsgsToCRM converte []db.Message para o formato crmMessage do frontend.
func localMsgsToCRM(msgs []db.Message) []crmMessage {
	out := make([]crmMessage, 0, len(msgs))
	for _, m := range msgs {
		msgType := string(m.MessageType)
		if msgType == "" {
			msgType = "text"
		}
		out = append(out, crmMessage{
			ID:        m.WAMessageID,
			Direction: string(m.Direction),
			Type:      msgType,
			Content:   m.Content,
			MediaURL:  m.MediaURL,
			MediaMime: m.MediaMime,
			Status:    string(m.Status),
			CreatedAt: m.CreatedAt.Format("2006-01-02T15:04:05Z"),
		})
	}
	return out
}

// extractChatID tenta extrair o CHAT_ID de várias estruturas possíveis de resposta.
func extractChatID(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "false" {
		return ""
	}
	// Tenta objeto direto: {"CHAT_ID": 123}
	var obj struct {
		ChatID interface{} `json:"CHAT_ID"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.ChatID != nil {
		return fmt.Sprintf("%v", obj.ChatID)
	}
	// Tenta array: [{"CHAT_ID": 123}]
	var arr []struct {
		ChatID interface{} `json:"CHAT_ID"`
	}
	if json.Unmarshal(raw, &arr) == nil && len(arr) > 0 && arr[0].ChatID != nil {
		return fmt.Sprintf("%v", arr[0].ChatID)
	}
	// Tenta result wrapper: {"result": {"CHAT_ID": 123}}
	var wrapped struct {
		Result struct {
			ChatID interface{} `json:"CHAT_ID"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &wrapped) == nil && wrapped.Result.ChatID != nil {
		return fmt.Sprintf("%v", wrapped.Result.ChatID)
	}
	return ""
}

// extractChatIDFromRecent percorre im.recent.list procurando um chat cujo título/nome contém o telefone.
func extractChatIDFromRecent(raw json.RawMessage, phone string) string {
	var items []struct {
		ID    interface{} `json:"ID"`
		Title string      `json:"TITLE"`
		Name  string      `json:"NAME"`
		Type  string      `json:"TYPE"`
	}
	// Tenta array direto
	if json.Unmarshal(raw, &items) != nil {
		// Tenta {result: [...]}
		var w struct {
			Result []struct {
				ID    interface{} `json:"ID"`
				Title string      `json:"TITLE"`
				Name  string      `json:"NAME"`
				Type  string      `json:"TYPE"`
			} `json:"result"`
		}
		if json.Unmarshal(raw, &w) == nil {
			for _, it := range w.Result {
				if strings.Contains(it.Title, phone) || strings.Contains(it.Name, phone) {
					return fmt.Sprintf("%v", it.ID)
				}
			}
		}
		return ""
	}
	for _, it := range items {
		if strings.Contains(it.Title, phone) || strings.Contains(it.Name, phone) {
			return fmt.Sprintf("%v", it.ID)
		}
	}
	return ""
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// parseSessionHistory converte a resposta de imopenlines.session.history.get para []crmMessage.
// Resposta: {"result": {"chatId":"X","message":{"ID":{"id":"X","senderid":"X","date":"...","text":"..."}},"users":{...}}}
func parseSessionHistory(raw json.RawMessage, connectorID string) []crmMessage {
	// Desempacota result wrapper
	var wrapper struct {
		Result json.RawMessage `json:"result"`
	}
	data := raw
	if json.Unmarshal(raw, &wrapper) == nil && len(wrapper.Result) > 0 {
		data = wrapper.Result
	}

	var resp struct {
		ChatID    interface{}                `json:"chatId"`
		SessionID interface{}                `json:"sessionId"`
		Message   map[string]struct {
			ID       interface{} `json:"id"`
			SenderID interface{} `json:"senderid"`
			Date     string      `json:"date"`
			Text     string      `json:"text"`
			Params   struct {
				ConnectorMID string `json:"CONNECTOR_MID"`
			} `json:"params"`
		} `json:"message"`
		Users map[string]struct {
			Name string `json:"name"`
		} `json:"users"`
	}

	if err := json.Unmarshal(data, &resp); err != nil || len(resp.Message) == 0 {
		return nil
	}

	// Coleta e ordena por ID numérico
	type msgEntry struct {
		numID int64
		msg   crmMessage
	}
	entries := make([]msgEntry, 0, len(resp.Message))
	for _, m := range resp.Message {
		senderID := fmt.Sprintf("%v", m.SenderID)
		direction := "outbound"
		if m.Params.ConnectorMID != "" {
			direction = "inbound"
		}
		authorName := ""
		if u, ok := resp.Users[senderID]; ok {
			authorName = u.Name
		}
		numID, _ := strconv.ParseInt(fmt.Sprintf("%v", m.ID), 10, 64)
		entries = append(entries, msgEntry{
			numID: numID,
			msg: crmMessage{
				ID:         fmt.Sprintf("%v", m.ID),
				Direction:  direction,
				Type:       "text",
				Content:    m.Text,
				AuthorID:   senderID,
				AuthorName: authorName,
				Status:     "delivered",
				CreatedAt:  m.Date,
			},
		})
	}
	// Ordena por ID crescente (cronológico)
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j].numID < entries[j-1].numID; j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}
	out := make([]crmMessage, len(entries))
	for i, e := range entries {
		out[i] = e.msg
	}
	return out
}

type crmMessage struct {
	ID        string `json:"id"`
	Direction string `json:"direction"` // inbound | outbound
	Type      string `json:"type"`
	Content   string `json:"content"`
	MediaURL  string `json:"media_url,omitempty"`
	MediaMime string `json:"media_mime,omitempty"`
	AuthorID  string `json:"author_id,omitempty"`
	AuthorName string `json:"author_name,omitempty"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

// parseBitrixMessages converte a resposta do im.dialog.messages.get para []crmMessage.
// O Bitrix retorna: {"messages": [{ID, AUTHOR_ID, DATE, MESSAGE, ATTACH, ...}], "users": {...}}
func parseBitrixMessages(raw json.RawMessage, connectorID string) []crmMessage {
	var resp struct {
		Messages []struct {
			ID       interface{} `json:"ID"`
			AuthorID interface{} `json:"AUTHOR_ID"`
			Date     string      `json:"DATE"`
			Message  string      `json:"MESSAGE"`
			Attach   interface{} `json:"ATTACH"`
			IsSystem interface{} `json:"IS_SYSTEM"`
			Params   struct {
				ConnectorMID string `json:"CONNECTOR_MID"`
			} `json:"PARAMS"`
		} `json:"messages"`
		Users map[string]struct {
			Name string `json:"name"`
		} `json:"users"`
	}

	if err := json.Unmarshal(raw, &resp); err != nil {
		// Tenta via result wrapper
		var wrapper struct {
			Result json.RawMessage `json:"result"`
		}
		if json.Unmarshal(raw, &wrapper) == nil {
			json.Unmarshal(wrapper.Result, &resp)
		}
	}

	out := make([]crmMessage, 0, len(resp.Messages))
	for _, m := range resp.Messages {
		// Pula mensagens de sistema
		if fmt.Sprintf("%v", m.IsSystem) == "true" || fmt.Sprintf("%v", m.IsSystem) == "1" {
			continue
		}

		authorID := fmt.Sprintf("%v", m.AuthorID)
		// Mensagens com CONNECTOR_MID são as que vieram do WhatsApp (inbound do cliente)
		// Mensagens dos operadores (users do Bitrix) são outbound
		direction := "outbound"
		if m.Params.ConnectorMID != "" {
			direction = "inbound"
		}

		authorName := ""
		if u, ok := resp.Users[authorID]; ok {
			authorName = u.Name
		}

		// Detecta mídia no ATTACH
		mediaURL, mediaMime, msgType := "", "", "text"
		if m.Attach != nil {
			attachJSON, _ := json.Marshal(m.Attach)
			var attaches []struct {
				Type  string `json:"type"`
				Link  string `json:"link"`
				Value string `json:"value"`
			}
			if json.Unmarshal(attachJSON, &attaches) == nil {
				for _, a := range attaches {
					if a.Type == "image" || a.Type == "file" || a.Type == "video" || a.Type == "audio" {
						mediaURL = a.Link
						if a.Type == "image" { mediaMime = "image/jpeg"; msgType = "image" }
						if a.Type == "video" { mediaMime = "video/mp4";  msgType = "video" }
						if a.Type == "audio" { mediaMime = "audio/ogg";  msgType = "audio" }
						if a.Type == "file"  { msgType = "document" }
						break
					}
				}
			}
		}

		out = append(out, crmMessage{
			ID:         fmt.Sprintf("%v", m.ID),
			Direction:  direction,
			Type:       msgType,
			Content:    m.Message,
			MediaURL:   mediaURL,
			MediaMime:  mediaMime,
			AuthorID:   authorID,
			AuthorName: authorName,
			Status:     "delivered",
			CreatedAt:  m.Date,
		})
	}
	return out
}

// GET /bitrix/crm/sessions — lista sessões WA disponíveis (para o select do iframe)
func (h *handlers) bitrixCRMSessions(c *fiber.Ctx) error {
	sessions := h.waManager.ListSessions()
	return c.JSON(fiber.Map{"sessions": sessions, "count": len(sessions)})
}

// GET /bitrix/crm/lines?domain=... — lista Open Lines do portal
func (h *handlers) bitrixCRMLines(c *fiber.Ctx) error {
	domain := c.Query("domain")
	if domain == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "domain obrigatório"})
	}
	portal, err := h.repo.GetBitrixPortalByDomain(c.Context(), normalizePortalDomain(domain))
	if err != nil || portal == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "portal não encontrado"})
	}
	creds := h.portalToCreds(portal)
	raw, err := h.bitrixClient.ListOpenLines(c.Context(), creds)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Type("json").Send(raw)
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func jsonStr(obj map[string]json.RawMessage, key string) string {
	v, ok := obj[key]
	if !ok {
		return ""
	}
	var s string
	if json.Unmarshal(v, &s) == nil {
		return s
	}
	return ""
}

func extractPhone(obj map[string]json.RawMessage) string {
	v, ok := obj["PHONE"]
	if !ok {
		return ""
	}
	var phones []struct {
		Value     string `json:"VALUE"`
		ValueType string `json:"VALUE_TYPE"`
	}
	if json.Unmarshal(v, &phones) != nil || len(phones) == 0 {
		return ""
	}
	return phones[0].Value
}

func b64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

func guessMime(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	m := map[string]string{
		".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png",
		".gif": "image/gif", ".webp": "image/webp",
		".mp4": "video/mp4", ".mov": "video/quicktime", ".avi": "video/x-msvideo",
		".mp3": "audio/mpeg", ".ogg": "audio/ogg", ".wav": "audio/wav", ".m4a": "audio/mp4",
		".pdf": "application/pdf",
		".doc": "application/msword",
		".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		".xls": "application/vnd.ms-excel",
		".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		".zip": "application/zip",
	}
	if v, ok := m[ext]; ok {
		return v
	}
	return "application/octet-stream"
}

func normalizeWAPhone(phone string) string {
	var out strings.Builder
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			out.WriteRune(r)
		}
	}
	return out.String()
}

// ─── HTML da aba CRM ─────────────────────────────────────────────────────────

// portalToCreds já está definido em partner.go

var crmTabHTML = `<!DOCTYPE html>
<html lang="pt-BR">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>UC Talk</title>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link href="https://fonts.googleapis.com/css2?family=Plus+Jakarta+Sans:wght@400;500;600;700&display=swap" rel="stylesheet">
<style>
*{box-sizing:border-box;margin:0;padding:0}
html,body{height:100%;overflow:hidden}
body{font-family:'Plus Jakarta Sans',sans-serif;background:#1a2234;color:#e2e8f0;font-size:13px;display:flex;flex-direction:column;height:100%}

/* ══ TOPO: operador + seletor de número ══ */
.topbar{background:#252f3e;border-bottom:1px solid #2d3a4e;padding:8px 14px;display:flex;align-items:center;gap:10px;flex-shrink:0;min-height:52px}
.op-avatar{width:32px;height:32px;border-radius:50%;background:#334155;display:flex;align-items:center;justify-content:center;font-size:13px;font-weight:700;color:#94a3b8;flex-shrink:0;text-transform:uppercase}
.op-info{flex:1;min-width:0}
.op-name{font-size:13px;font-weight:600;color:#f1f5f9;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.op-email{font-size:10px;color:#64748b;margin-top:1px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.wa-selector{display:flex;align-items:center;gap:6px;background:#1a2436;border:1px solid #334155;border-radius:8px;padding:4px 10px;cursor:pointer;flex-shrink:0;position:relative}
.wa-selector-icon{width:18px;height:18px;background:#25D366;border-radius:50%;display:flex;align-items:center;justify-content:center;flex-shrink:0}
.wa-selector-icon svg{width:11px;height:11px}
.wa-selector-label{font-size:12px;font-weight:600;color:#e2e8f0;max-width:130px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.wa-selector-arrow{color:#64748b;font-size:10px;margin-left:2px}
.wa-dropdown{position:absolute;top:calc(100% + 6px);right:0;background:#252f3e;border:1px solid #334155;border-radius:10px;min-width:220px;box-shadow:0 8px 24px rgba(0,0,0,.4);z-index:100;overflow:hidden;display:none}
.wa-dropdown.open{display:block}
.wa-drop-item{display:flex;align-items:center;gap:10px;padding:10px 14px;cursor:pointer;transition:background .12s}
.wa-drop-item:hover{background:#334155}
.wa-drop-item.active{background:#1a3a2a}
.wa-drop-num{font-size:13px;font-weight:600;color:#f1f5f9}
.wa-drop-jid{font-size:10px;color:#64748b;margin-top:1px}
.wa-dot{width:8px;height:8px;border-radius:50%;background:#25D366;flex-shrink:0}
.logo-badge{display:flex;align-items:center;gap:5px;color:#25D366;font-weight:700;font-size:12px;margin-left:4px;flex-shrink:0}
.logo-badge svg{width:18px;height:18px}

/* ══ LAYOUT PRINCIPAL: dois painéis ══ */
.main{flex:1;display:flex;overflow:hidden}

/* ── Painel esquerdo: conversas ── */
.sidebar{width:260px;flex-shrink:0;background:#1e2736;border-right:1px solid #2d3a4e;display:flex;flex-direction:column;overflow:hidden}
.sidebar-header{padding:10px 12px;border-bottom:1px solid #2d3a4e;flex-shrink:0}
.sidebar-title{font-size:12px;font-weight:700;color:#94a3b8;text-transform:uppercase;letter-spacing:.06em;margin-bottom:8px}
.search-box{display:flex;align-items:center;gap:6px;background:#252f3e;border:1px solid #2d3a4e;border-radius:8px;padding:6px 10px}
.search-box svg{color:#64748b;flex-shrink:0}
.search-box input{background:none;border:none;outline:none;color:#f1f5f9;font-size:12px;font-family:inherit;width:100%}
.search-box input::placeholder{color:#475569}
.conv-list{flex:1;overflow-y:auto;padding:4px 0}
.conv-list::-webkit-scrollbar{width:3px}
.conv-list::-webkit-scrollbar-thumb{background:#334155;border-radius:3px}
.conv-item{display:flex;align-items:center;gap:10px;padding:9px 12px;cursor:pointer;transition:background .12s;border-left:3px solid transparent}
.conv-item:hover{background:#252f3e}
.conv-item.active{background:#1a3a2a;border-left-color:#25D366}
.conv-avatar{width:34px;height:34px;border-radius:50%;background:#334155;display:flex;align-items:center;justify-content:center;font-size:12px;font-weight:700;color:#94a3b8;flex-shrink:0;text-transform:uppercase}
.conv-body{flex:1;min-width:0}
.conv-name{font-size:13px;font-weight:600;color:#f1f5f9;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.conv-preview{font-size:11px;color:#64748b;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;margin-top:1px}
.conv-meta{display:flex;flex-direction:column;align-items:flex-end;gap:3px;flex-shrink:0}
.conv-time{font-size:10px;color:#475569}
.conv-badge{background:#25D366;color:#fff;font-size:9px;font-weight:700;border-radius:99px;padding:1px 5px;min-width:16px;text-align:center}
.conv-empty{padding:20px;text-align:center;color:#475569;font-size:12px}

/* ── Painel direito: chat ── */
.chat-panel{flex:1;display:flex;flex-direction:column;overflow:hidden;background:#111827}

/* cabeçalho do contato */
.chat-header{background:#1e2736;border-bottom:1px solid #2d3a4e;padding:10px 16px;display:flex;align-items:center;gap:10px;flex-shrink:0}
.chat-hdr-avatar{width:36px;height:36px;border-radius:50%;background:#334155;display:flex;align-items:center;justify-content:center;font-size:14px;font-weight:700;color:#94a3b8;text-transform:uppercase;flex-shrink:0}
.chat-hdr-info{flex:1;min-width:0}
.chat-hdr-name{font-size:14px;font-weight:700;color:#f1f5f9}
.chat-hdr-phone{font-size:11px;color:#64748b;margin-top:1px}
.btn-icon{background:none;border:none;cursor:pointer;color:#64748b;padding:4px;border-radius:6px;transition:color .15s,background .15s;display:flex;align-items:center}
.btn-icon:hover{color:#e2e8f0;background:#334155}

/* área de mensagens */
.chat-body{flex:1;overflow-y:auto;padding:14px 16px;display:flex;flex-direction:column;gap:4px}
.chat-body::-webkit-scrollbar{width:4px}
.chat-body::-webkit-scrollbar-thumb{background:#334155;border-radius:4px}
.chat-placeholder{flex:1;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:12px;color:#475569;text-align:center;padding:20px}
.chat-placeholder svg{opacity:.3}
.chat-placeholder p{font-size:13px}
.chat-placeholder .sub{font-size:11px;color:#334155}

.day-div{text-align:center;margin:8px 0}
.day-div span{background:#1e2736;color:#64748b;font-size:10px;font-weight:600;padding:3px 10px;border-radius:99px}

.bw{display:flex;flex-direction:column;max-width:72%}
.bw.out{align-self:flex-end;align-items:flex-end}
.bw.in{align-self:flex-start;align-items:flex-start}
.bubble{padding:8px 12px;border-radius:14px;font-size:13px;line-height:1.5;word-break:break-word}
.bubble.out{background:#005c4b;color:#e2e8f0;border-bottom-right-radius:3px}
.bubble.in{background:#1e2736;color:#e2e8f0;border-bottom-left-radius:3px;border:1px solid #2d3a4e}
.bmeta{display:flex;align-items:center;gap:3px;margin-top:2px;padding:0 2px}
.btime{font-size:10px;color:#64748b}
.bst{font-size:11px;line-height:1}
.bst.sent{color:#94a3b8}.bst.delivered{color:#94a3b8}.bst.failed{color:#f87171}
.bmedia{display:flex;align-items:center;gap:5px;color:#94a3b8;font-size:11px;font-style:italic;margin-bottom:2px}

/* compositor */
.composer{background:#1e2736;border-top:1px solid #2d3a4e;padding:8px 12px;display:flex;flex-direction:column;gap:6px;flex-shrink:0}
.composer-row{display:flex;gap:8px;align-items:flex-end}
.composer textarea{flex:1;background:#252f3e;border:1px solid #334155;border-radius:12px;padding:9px 12px;color:#f1f5f9;font-size:13px;font-family:inherit;outline:none;resize:none;max-height:120px;min-height:38px;line-height:1.4;transition:border-color .2s}
.composer textarea:focus{border-color:#25D366}
.composer textarea::placeholder{color:#475569}
.composer textarea:disabled{opacity:.4}
.btn-attach{background:none;border:1px solid #334155;border-radius:50%;width:38px;height:38px;display:flex;align-items:center;justify-content:center;cursor:pointer;flex-shrink:0;color:#64748b;transition:all .15s}
.btn-attach:hover{border-color:#25D366;color:#25D366}
.btn-attach:disabled{opacity:.3;cursor:not-allowed}
.btn-send{background:#25D366;border:none;border-radius:50%;width:38px;height:38px;display:flex;align-items:center;justify-content:center;cursor:pointer;flex-shrink:0;transition:opacity .15s}
.btn-send:hover{opacity:.85}
.btn-send:disabled{opacity:.4;cursor:not-allowed}
.file-preview{display:none;align-items:center;gap:8px;background:#252f3e;border:1px solid #334155;border-radius:10px;padding:7px 10px;font-size:12px;color:#94a3b8}
.file-preview.show{display:flex}
.file-preview-name{flex:1;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;color:#e2e8f0}
.file-preview-rm{background:none;border:none;color:#64748b;cursor:pointer;padding:0;display:flex;align-items:center}
.file-preview-rm:hover{color:#f87171}
/* spinner */
.spinner{width:22px;height:22px;border:2px solid #2d3a4e;border-top-color:#25D366;border-radius:50%;animation:spin .7s linear infinite}
@keyframes spin{to{transform:rotate(360deg)}}

/* alert inline */
.inline-alert{padding:7px 12px;border-radius:8px;font-size:11px;margin:4px 14px;flex-shrink:0;display:none}
.inline-alert.error{background:#450a0a;color:#f87171;border:1px solid #7f1d1d;display:block}
.inline-alert.success{background:#14532d;color:#4ade80;display:block}
</style>
</head>
<body>

<!-- ══ TOPO: operador + seletor de número WA ══ -->
<div class="topbar">
  <div class="op-avatar" id="op-initials">?</div>
  <div class="op-info">
    <div class="op-name" id="op-name">Carregando...</div>
    <div class="op-email" id="op-email"></div>
  </div>
  <div class="wa-selector" id="wa-selector" onclick="toggleWADropdown()">
    <div class="wa-selector-icon">
      <svg viewBox="0 0 24 24" fill="white"><path d="M12 2C6.48 2 2 6.48 2 12c0 1.82.49 3.53 1.34 5L2 22l5.15-1.32A10 10 0 1012 2z"/></svg>
    </div>
    <span class="wa-selector-label" id="wa-sel-label">Carregando...</span>
    <span class="wa-selector-arrow">▾</span>
    <div class="wa-dropdown" id="wa-dropdown"></div>
  </div>
  <div class="logo-badge">
    <svg viewBox="0 0 24 24" fill="#25D366"><path d="M12 2C6.48 2 2 6.48 2 12c0 1.82.49 3.53 1.34 5L2 22l5.15-1.32A10 10 0 1012 2z"/></svg>
    UC Talk
  </div>
</div>

<!-- ══ CORPO: sidebar + chat ══ -->
<div class="main">

  <!-- ── Sidebar: lista de conversas ── -->
  <div class="sidebar">
    <div class="sidebar-header">
      <div class="sidebar-title">Conversas</div>
      <div class="search-box">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
        <input type="text" id="conv-search" placeholder="Pesquisar..." oninput="filterConvs()">
      </div>
    </div>
    <div class="conv-list" id="conv-list">
      <div style="padding:16px;text-align:center"><div class="spinner" style="margin:auto"></div></div>
    </div>
  </div>

  <!-- ── Painel de chat ── -->
  <div class="chat-panel">

    <!-- cabeçalho do contato ativo -->
    <div class="chat-header">
      <div class="chat-hdr-avatar" id="chat-hdr-avatar">?</div>
      <div class="chat-hdr-info">
        <div class="chat-hdr-name" id="chat-hdr-name">Selecione uma conversa</div>
        <div class="chat-hdr-phone" id="chat-hdr-phone"></div>
      </div>
      <button class="btn-icon" title="Atualizar" onclick="reloadChat()">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="23 4 23 10 17 10"/><polyline points="1 20 1 14 7 14"/><path d="M3.51 9a9 9 0 0114.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0020.49 15"/></svg>
      </button>
    </div>

    <!-- mensagens -->
    <div class="chat-body" id="chat-body">
      <div class="chat-placeholder">
        <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M21 15a2 2 0 01-2 2H7l-4 4V5a2 2 0 012-2h14a2 2 0 012 2z"/></svg>
        <p>Selecione uma conversa<br>ou inicie uma nova</p>
      </div>
    </div>

    <!-- alerta inline -->
    <div class="inline-alert" id="inline-alert"></div>

    <!-- compositor -->
    <div class="composer">
      <!-- preview do arquivo selecionado -->
      <div class="file-preview" id="file-preview">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>
        <span class="file-preview-name" id="file-preview-name"></span>
        <button class="file-preview-rm" onclick="clearFile()" title="Remover arquivo">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
        </button>
      </div>
      <div class="composer-row">
        <input type="file" id="file-input" style="display:none" onchange="onFileSelected(this)">
        <button class="btn-attach" id="btn-attach" onclick="document.getElementById('file-input').click()" title="Anexar arquivo" disabled>
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21.44 11.05l-9.19 9.19a6 6 0 01-8.49-8.49l9.19-9.19a4 4 0 015.66 5.66l-9.2 9.19a2 2 0 01-2.83-2.83l8.49-8.48"/></svg>
        </button>
        <textarea id="msg-input" placeholder="Mensagem..." rows="1" disabled
          onkeydown="onKey(event)" oninput="autoResize(this)"></textarea>
        <button class="btn-send" id="btn-send" onclick="sendOrUpload()" title="Enviar" disabled>
          <svg width="18" height="18" viewBox="0 0 24 24" fill="white"><path d="M2 21l21-9L2 3v7l15 2-15 2z"/></svg>
        </button>
      </div>
    </div>

  </div>
</div>

<script src="https://api.bitrix24.com/api/v1/"></script>
<script>
var _domain = '', _entityType = 'contact', _entityId = '';
var _contactName = '', _contactPhone = '', _contactInitials = '?';
var _baseUrl = window.location.origin;
var _sessions = [];
var _activeSession = '';
var _pollTimer = null;
var _lastMsgId = '';
var _allConvs = [];
var _placementRaw = null;

// ── Inicialização ────────────────────────────────────────────────────────
function init() {
  BX24.init(function() {
    var p = BX24.placement.info();

    // Salva raw para debug
    _placementRaw = p;

    // Tenta extrair entity_id de todas as formas que o Bitrix pode enviar
    var opts = p.options || {};

    // 1. CRM_CONTACT_DETAIL_TAB → opts.ID (maiúsculo)
    // 2. CRM_CONTACT_DETAIL_TAB → opts.id (minúsculo)
    // 3. opts.entityTypeName + opts.id (formato antigo)
    // 4. p.ID direto
    _entityId = String(opts.ID || opts.id || opts.ENTITY_ID || p.ID || '').replace(/\D/g,'');

    // entityTypeName pode vir como "contact", "CONTACT", "CRM_CONTACT" etc.
    var rawType = (opts.entityTypeName || opts.ENTITY_TYPE_NAME || opts.CRM_ENTITY_TYPE || p.placement || '').toLowerCase();
    if      (rawType.indexOf('lead')    >= 0) _entityType = 'lead';
    else if (rawType.indexOf('deal')    >= 0) _entityType = 'deal';
    else if (rawType.indexOf('contact') >= 0) _entityType = 'contact';
    else _entityType = 'contact'; // default

    // Domain
    _domain = (BX24.getDomain ? BX24.getDomain() : '') || opts.domain || p.domain || '';

    // Mostra debug discreto na sidebar se entity_id estiver vazio
    if (!_entityId) {
      document.getElementById('conv-list').innerHTML =
        '<div class="conv-empty" style="font-size:10px;color:#334155;text-align:left;padding:10px 12px;word-break:break-all">'
        + 'placement: ' + (p.placement||'—') + '<br>'
        + 'options: ' + JSON.stringify(opts).slice(0,200)
        + '</div>';
    }

    // Info do operador logado via BX24.js
    BX24.callMethod('profile', {}, function(res) {
      var u = res.data() || {};
      var name  = [u.NAME, u.LAST_NAME].filter(Boolean).join(' ') || u.ID || 'Operador';
      var email = u.EMAIL || '';
      document.getElementById('op-name').textContent     = name;
      document.getElementById('op-email').textContent    = email;
      document.getElementById('op-initials').textContent = initials(name);
    });

    loadSessions();
    loadEntity();
  });
}

// ── Operador ─────────────────────────────────────────────────────────────
function initials(name) {
  var parts = name.trim().split(/\s+/);
  if (parts.length >= 2) return (parts[0][0] + parts[parts.length-1][0]).toUpperCase();
  return name.slice(0,2).toUpperCase();
}

// ── Sessões WhatsApp ──────────────────────────────────────────────────────
function loadSessions() {
  fetch(_baseUrl + '/bitrix/crm/sessions').then(r => r.json()).then(function(d) {
    _sessions = (d.sessions || []).map(function(jid) {
      var num = jid.split('@')[0];
      return {jid: jid, phone: num};
    });
    renderWADropdown();
  });
}

function renderWADropdown() {
  var dd = document.getElementById('wa-dropdown');
  var label = document.getElementById('wa-sel-label');
  dd.innerHTML = '';
  if (!_sessions.length) {
    dd.innerHTML = '<div style="padding:12px 14px;font-size:12px;color:#64748b">Nenhuma sessão conectada</div>';
    label.textContent = 'Desconectado';
    document.getElementById('wa-selector').style.borderColor = '#7f1d1d';
    return;
  }
  _sessions.forEach(function(s) {
    var el = document.createElement('div');
    el.className = 'wa-drop-item' + (s.jid === _activeSession ? ' active' : '');
    el.innerHTML = '<div class="wa-dot"></div><div><div class="wa-drop-num">+' + s.phone + '</div><div class="wa-drop-jid">' + s.jid + '</div></div>';
    el.onclick = function(e) { e.stopPropagation(); selectSession(s.jid); };
    dd.appendChild(el);
  });
  if (!_activeSession && _sessions.length) selectSession(_sessions[0].jid);
}

function selectSession(jid) {
  _activeSession = jid;
  var s = _sessions.find(function(x){ return x.jid === jid; });
  document.getElementById('wa-sel-label').textContent = s ? ('+' + s.phone) : jid;
  document.getElementById('wa-dropdown').classList.remove('open');
  // re-render checkmarks
  var items = document.querySelectorAll('.wa-drop-item');
  items.forEach(function(el, i) { el.className = 'wa-drop-item' + (_sessions[i] && _sessions[i].jid === jid ? ' active' : ''); });
}

function toggleWADropdown() {
  document.getElementById('wa-dropdown').classList.toggle('open');
}
document.addEventListener('click', function(e) {
  if (!document.getElementById('wa-selector').contains(e.target))
    document.getElementById('wa-dropdown').classList.remove('open');
});

// ── Entidade CRM (contato/lead/deal) ─────────────────────────────────────
function loadEntity() {
  // Mostra spinner na sidebar enquanto carrega
  document.getElementById('conv-list').innerHTML = '<div style="padding:16px;text-align:center"><div class="spinner" style="margin:auto"></div></div>';

  if (!_entityId) {
    document.getElementById('conv-list').innerHTML = '<div class="conv-empty">Abra um Contato,<br>Lead ou Deal para<br>ver o histórico.</div>';
    return;
  }
  var url = _baseUrl + '/bitrix/crm/entity?domain=' + enc(_domain) + '&entity_type=' + _entityType + '&entity_id=' + _entityId;
  fetch(url).then(function(r){ return r.json(); }).then(function(d) {
    _contactName     = d.name  || 'Contato';
    _contactPhone    = d.phone || '';
    _contactInitials = initials(_contactName);

    if (!_contactPhone) {
      document.getElementById('conv-list').innerHTML = '<div class="conv-empty">Nenhum telefone<br>cadastrado neste contato.</div>';
      document.getElementById('chat-hdr-name').textContent  = _contactName;
      document.getElementById('chat-hdr-avatar').textContent = _contactInitials;
      return;
    }

    // Adiciona o contato na lista e abre o chat imediatamente
    _allConvs = [{ name: _contactName, phone: _contactPhone, preview: _contactPhone, time: '', unread: 0, active: true }];
    renderConvList();
    openChat(_contactName, _contactPhone);
  }).catch(function() {
    document.getElementById('conv-list').innerHTML = '<div class="conv-empty" style="color:#f87171">Erro ao carregar contato.</div>';
  });
}

// ── Lista de conversas ────────────────────────────────────────────────────
function renderConvList() {
  var list = document.getElementById('conv-list');
  var q = (document.getElementById('conv-search').value || '').toLowerCase();
  var items = _allConvs.filter(function(c){ return !q || c.name.toLowerCase().includes(q) || c.phone.includes(q); });
  if (!items.length) {
    list.innerHTML = '<div class="conv-empty">Nenhuma conversa encontrada</div>';
    return;
  }
  list.innerHTML = items.map(function(c) {
    var av = initials(c.name);
    var isActive = c.active || c.phone === _contactPhone;
    var badge = c.unread ? '<span class="conv-badge">' + c.unread + '</span>' : '';
    return '<div class="conv-item' + (isActive ? ' active' : '') + '" data-phone="' + esc(c.phone) + '" onclick="openChat(' + JSON.stringify(c.name) + ',' + JSON.stringify(c.phone) + ')">'
      + '<div class="conv-avatar">' + av + '</div>'
      + '<div class="conv-body"><div class="conv-name">' + esc(c.name) + '</div>'
      + '<div class="conv-preview">' + esc(c.preview || c.phone) + '</div></div>'
      + '<div class="conv-meta"><span class="conv-time">' + (c.time||'') + '</span>' + badge + '</div>'
      + '</div>';
  }).join('');
}

function filterConvs() { renderConvList(); }

// ── Abrir chat ────────────────────────────────────────────────────────────
function openChat(name, phone) {
  _contactName  = name;
  _contactPhone = phone;
  document.getElementById('chat-hdr-name').textContent   = name;
  document.getElementById('chat-hdr-phone').textContent  = phone ? ('📱 ' + phone) : '';
  document.getElementById('chat-hdr-avatar').textContent = initials(name);
  document.getElementById('msg-input').disabled  = false;
  document.getElementById('btn-send').disabled   = false;
  document.getElementById('btn-attach').disabled = false;
  hideAlert();
  loadHistory();
  // marca conversa ativa na sidebar
  document.querySelectorAll('.conv-item').forEach(function(el){ el.classList.remove('active'); });
  var items = document.querySelectorAll('.conv-item');
  items.forEach(function(el){ if (el.dataset.phone === phone) el.classList.add('active'); });
}

function reloadChat() { if (_contactPhone) loadHistory(); }

// ── Histórico (banco local → fallback Bitrix) ─────────────────────────────
function loadHistory() {
  if (!_entityId || !_domain) return;
  document.getElementById('chat-body').innerHTML = '<div class="chat-placeholder"><div class="spinner"></div></div>';
  var url = _baseUrl + '/bitrix/crm/history?domain=' + enc(_domain)
          + '&entity_type=' + _entityType + '&entity_id=' + _entityId
          + '&phone=' + enc(_contactPhone)
          + '&limit=80';
  fetch(url)
    .then(function(r){ return r.json(); })
    .then(function(d) { renderHistory(d.messages || [], d.chat_id || ''); })
    .catch(function() {
      document.getElementById('chat-body').innerHTML = '<div class="chat-placeholder"><p>Erro ao carregar histórico.</p></div>';
    });
  if (_pollTimer) clearInterval(_pollTimer);
  _pollTimer = setInterval(pollHistory, 5000);
}

function pollHistory() {
  if (!_entityId || !_domain) return;
  var url = _baseUrl + '/bitrix/crm/history?domain=' + enc(_domain)
          + '&entity_type=' + _entityType + '&entity_id=' + _entityId
          + '&phone=' + enc(_contactPhone)
          + '&limit=3';
  fetch(url)
    .then(function(r){ return r.json(); })
    .then(function(d) {
      var msgs = d.messages || [];
      if (msgs.length && msgs[msgs.length-1].id !== _lastMsgId) loadHistory();
    }).catch(function(){});
}

function renderHistory(msgs, chatID) {
  var body = document.getElementById('chat-body');
  if (!msgs.length) {
    body.innerHTML = '<div class="chat-placeholder"><svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M21 15a2 2 0 01-2 2H7l-4 4V5a2 2 0 012-2h14a2 2 0 012 2z"/></svg><p>Nenhuma mensagem ainda</p><span class="sub">Envie a primeira mensagem abaixo</span></div>';
    return;
  }
  var html = '', lastDay = '';
  msgs.forEach(function(m) {
    // O Bitrix retorna datas no formato "DD.MM.YYYY HH:MM:SS" ou ISO
    var dt = parseDate(m.created_at);
    var day = dt.toLocaleDateString('pt-BR',{day:'2-digit',month:'2-digit',year:'numeric'});
    if (day !== lastDay) { html += '<div class="day-div"><span>' + day + '</span></div>'; lastDay = day; }
    var isOut = m.direction === 'outbound';
    var time  = dt.toLocaleTimeString('pt-BR',{hour:'2-digit',minute:'2-digit'});
    var st = isOut ? '<span class="bst delivered">✓✓</span>' : '';
    var content = '';
    if (m.type && m.type !== 'text') {
      var icon = mediaIcon(m.type);
      if (m.media_url) {
        content = '<a href="' + esc(m.media_url) + '" target="_blank" style="display:flex;align-items:center;gap:5px;color:#60a5fa;text-decoration:none;">' + icon + ' ' + mediaLabel(m.type) + '</a>';
      } else {
        content = '<div class="bmedia">' + icon + ' ' + mediaLabel(m.type) + '</div>';
      }
      if (m.content) content += '<div style="margin-top:3px">' + esc(m.content) + '</div>';
    } else {
      content = esc(m.content || '');
    }
    // Nome do autor apenas em mensagens outbound (operador)
    var authorLabel = (isOut && m.author_name) ? '<div style="font-size:10px;color:#4ade80;margin-bottom:2px;font-weight:600">' + esc(m.author_name) + '</div>' : '';
    html += '<div class="bw ' + (isOut?'out':'in') + '"><div class="bubble ' + (isOut?'out':'in') + '">'
          + authorLabel + content
          + '<div class="bmeta"><span class="btime">' + time + '</span>' + st + '</div>'
          + '</div></div>';
  });
  body.innerHTML = html;
  body.scrollTop = body.scrollHeight;
  if (msgs.length) _lastMsgId = msgs[msgs.length-1].id;
  // Atualiza preview na sidebar
  var last = msgs[msgs.length-1];
  var idx = _allConvs.findIndex(function(c){ return c.phone === _contactPhone; });
  if (idx >= 0) {
    _allConvs[idx].preview = last.content || mediaLabel(last.type);
    _allConvs[idx].time    = last.created_at ? parseDate(last.created_at).toLocaleTimeString('pt-BR',{hour:'2-digit',minute:'2-digit'}) : '';
    renderConvList();
  }
}

function parseDate(s) {
  if (!s) return new Date();
  // "DD.MM.YYYY HH:MM:SS"
  var m = s.match(/^(\d{2})\.(\d{2})\.(\d{4})\s+(\d{2}):(\d{2}):(\d{2})$/);
  if (m) return new Date(m[3]+'-'+m[2]+'-'+m[1]+'T'+m[4]+':'+m[5]+':'+m[6]);
  return new Date(s);
}

// ── Arquivo ───────────────────────────────────────────────────────────────
var _pendingFile = null;

function onFileSelected(input) {
  if (!input.files || !input.files[0]) return;
  _pendingFile = input.files[0];
  document.getElementById('file-preview-name').textContent = _pendingFile.name;
  document.getElementById('file-preview').classList.add('show');
  document.getElementById('msg-input').placeholder = 'Legenda (opcional)...';
}

function clearFile() {
  _pendingFile = null;
  document.getElementById('file-input').value = '';
  document.getElementById('file-preview').classList.remove('show');
  document.getElementById('msg-input').placeholder = 'Mensagem...';
}

// ── Envio (texto ou arquivo) ───────────────────────────────────────────────
function sendOrUpload() {
  if (_pendingFile) { uploadFile(); } else { sendMsg(); }
}

function sendMsg() {
  var msg = document.getElementById('msg-input').value.trim();
  if (!_contactPhone) { showAlert('error','Selecione um contato.'); return; }
  if (!_activeSession){ showAlert('error','Selecione um número WhatsApp no topo.'); return; }
  if (!msg) return;

  setBtnLoading(true);
  fetch(_baseUrl + '/bitrix/crm/send', {
    method:'POST', headers:{'Content-Type':'application/json'},
    body: JSON.stringify({ domain:_domain, entity_type:_entityType, entity_id:_entityId,
                           phone:_contactPhone, session_jid:_activeSession, message:msg })
  })
  .then(function(r){ return r.json(); })
  .then(function(d) {
    setBtnLoading(false);
    if (d.status === 'queued') {
      document.getElementById('msg-input').value = '';
      autoResize(document.getElementById('msg-input'));
      hideAlert();
      appendOptimistic(msg, null);
    } else {
      showAlert('error', d.error || 'Erro ao enviar.');
    }
  })
  .catch(function(e){ setBtnLoading(false); showAlert('error','Falha: ' + e); });
}

function uploadFile() {
  if (!_pendingFile) return;
  if (!_contactPhone) { showAlert('error','Selecione um contato.'); return; }
  if (!_activeSession){ showAlert('error','Selecione um número WhatsApp no topo.'); return; }

  var caption = document.getElementById('msg-input').value.trim();
  var fd = new FormData();
  fd.append('domain',      _domain);
  fd.append('phone',       _contactPhone);
  fd.append('session_jid', _activeSession);
  fd.append('caption',     caption);
  fd.append('file',        _pendingFile, _pendingFile.name);

  setBtnLoading(true);
  fetch(_baseUrl + '/bitrix/crm/upload', { method:'POST', body: fd })
  .then(function(r){ return r.json(); })
  .then(function(d) {
    setBtnLoading(false);
    if (d.status === 'queued') {
      appendOptimistic(caption || _pendingFile.name, _pendingFile.name);
      clearFile();
      document.getElementById('msg-input').value = '';
      autoResize(document.getElementById('msg-input'));
      hideAlert();
    } else {
      showAlert('error', d.error || 'Erro ao enviar arquivo.');
    }
  })
  .catch(function(e){ setBtnLoading(false); showAlert('error','Falha: ' + e); });
}

function setBtnLoading(on) {
  document.getElementById('btn-send').disabled   = on;
  document.getElementById('btn-attach').disabled = on;
}

function appendOptimistic(text, filename) {
  var body = document.getElementById('chat-body');
  var ph = body.querySelector('.chat-placeholder');
  if (ph) ph.remove();
  var now  = new Date();
  var time = now.toLocaleTimeString('pt-BR',{hour:'2-digit',minute:'2-digit'});
  var content = filename
    ? '<div class="bmedia">📎 ' + esc(filename) + '</div>' + (text ? '<div>' + esc(text) + '</div>' : '')
    : esc(text);
  var el = document.createElement('div');
  el.className = 'bw out';
  el.innerHTML = '<div class="bubble out">' + content + '<div class="bmeta"><span class="btime">' + time + '</span><span class="bst sent">✓</span></div></div>';
  body.appendChild(el);
  body.scrollTop = body.scrollHeight;
}

// ── Helpers ───────────────────────────────────────────────────────────────
function onKey(e) { if (e.key==='Enter' && !e.shiftKey){ e.preventDefault(); sendMsg(); } }
function autoResize(el){ el.style.height='auto'; el.style.height=Math.min(el.scrollHeight,120)+'px'; }
function enc(s){ return encodeURIComponent(s); }
function esc(s){ return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/\n/g,'<br>'); }
function showAlert(t,m){ var el=document.getElementById('inline-alert'); el.className='inline-alert '+t; el.textContent=m; el.style.display='block'; }
function hideAlert(){ var el=document.getElementById('inline-alert'); el.style.display='none'; }

function mediaIcon(t){
  var i={image:'🖼',video:'▶',audio:'🎵',document:'📄',sticker:'🙂'};
  return i[t]||'📎';
}
function mediaLabel(t){
  var l={image:'Imagem',video:'Vídeo',audio:'Áudio',document:'Documento',sticker:'Sticker'};
  return l[t]||'Arquivo';
}

init();
</script>
</body>
</html>`

// RegisterPlacementsForPortal registra placement.bind para os 3 tipos de entidade CRM.
// Chamado após instalar ou ativar o connector.
func (h *handlers) RegisterPlacementsForPortal(ctx context.Context, domain string, creds bitrix.TenantCreds) {
	base := h.cfg.App.BaseURL()
	tabURL := base + "/bitrix/crm/tab"
	placements := []string{
		"CRM_CONTACT_DETAIL_TAB",
		"CRM_LEAD_DETAIL_TAB",
		"CRM_DEAL_DETAIL_TAB",
	}
	for _, p := range placements {
		if err := h.bitrixClient.BindPlacement(ctx, creds, p, tabURL, "UC Talk"); err != nil {
			h.log.Warn("placement.bind failed",
				zap.String("placement", p),
				zap.String("domain", domain),
				zap.Error(err),
			)
		} else {
			h.log.Info("placement.bind ok", zap.String("placement", p), zap.String("domain", domain))
		}
	}
}
