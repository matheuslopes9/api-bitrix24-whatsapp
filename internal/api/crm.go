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
	"sync"
	"time"

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

// ─── Endpoints publicos de permissoes (chamados do /dashboard interno) ─────
// Diferenca dos endpoints /admin/api/tenant/*: nao exigem cookie admin.
// Quem acessa o /dashboard ja tem acesso ao backoffice do app, entao a
// barreira aqui eh validar que o domain pedido EXISTE em bitrix_portals.

// resolveDashboardDomain extrai o domain a operar.
// Em modo "portal isolado" (?portal=x.bitrix24.com), usa esse valor;
// senao usa o unico portal cadastrado. Retorna ("",err) se nada bater.
func (h *handlers) resolveDashboardDomain(ctx context.Context, c *fiber.Ctx) (string, error) {
	domain := strings.TrimSpace(c.Query("domain"))
	if domain == "" {
		domain = strings.TrimSpace(c.Query("portal"))
	}
	if domain != "" {
		return normalizePortalDomain(domain), nil
	}
	portals, err := h.repo.ListBitrixPortals(ctx)
	if err != nil {
		return "", err
	}
	if len(portals) == 1 {
		return portals[0].Domain, nil
	}
	return "", fmt.Errorf("informe ?domain= ou ?portal=")
}

// GET /ui/permissions/list — lista usuarios liberados para o portal atual.
func (h *handlers) uiPermissionsList(c *fiber.Ctx) error {
	ctx := c.Context()
	domain, err := h.resolveDashboardDomain(ctx, c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	key := strings.ToLower(domain)
	perms, err := h.repo.ListCrmPermissionsByDomain(ctx, key)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	out := make([]fiber.Map, 0, len(perms))
	for _, p := range perms {
		out = append(out, fiber.Map{
			"user_id":    p.UserID,
			"user_name":  p.UserName,
			"granted_at": p.GrantedAt.Format("2006-01-02 15:04"),
		})
	}
	return c.JSON(fiber.Map{"users": out, "granted": len(perms), "domain": domain})
}

// Cache em memoria de usuarios listados por dominio. TTL 10min.
// Sem locks complexos — uma race condition aqui so causa 1 chamada extra ao
// Bitrix, nao corrompe dado.
type allUsersCacheEntry struct {
	users     []bitrix.BitrixUser
	cachedAt  time.Time
}

var (
	allUsersCacheMu sync.RWMutex
	allUsersCache   = map[string]allUsersCacheEntry{}
)

const allUsersCacheTTL = 10 * time.Minute

// GET /ui/permissions/all-users?refresh=1 — lista todos usuarios ativos do portal.
// Itera IDs 1..500 em batches via im.user.list.get. Cache de 10min por dominio.
func (h *handlers) uiPermissionsAllUsers(c *fiber.Ctx) error {
	ctx := c.Context()
	domain, err := h.resolveDashboardDomain(ctx, c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	refresh := c.Query("refresh") == "1"
	key := strings.ToLower(domain)

	if !refresh {
		allUsersCacheMu.RLock()
		entry, ok := allUsersCache[key]
		allUsersCacheMu.RUnlock()
		if ok && time.Since(entry.cachedAt) < allUsersCacheTTL {
			return c.JSON(buildAllUsersResponse(ctx, h, key, entry.users, true))
		}
	}

	portal, err := h.repo.GetBitrixPortalByDomain(ctx, domain)
	if err != nil || portal == nil {
		return c.Status(404).JSON(fiber.Map{"error": "portal nao encontrado: " + domain})
	}
	creds := h.portalToCreds(portal)
	users, err := h.bitrixClient.ListAllUsers(ctx, creds, 500)
	if err != nil {
		h.log.Error("ui: ListAllUsers failed", zap.String("domain", domain), zap.Error(err))
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	allUsersCacheMu.Lock()
	allUsersCache[key] = allUsersCacheEntry{users: users, cachedAt: time.Now()}
	allUsersCacheMu.Unlock()
	h.log.Info("ui: ListAllUsers ok", zap.String("domain", domain), zap.Int("count", len(users)))
	return c.JSON(buildAllUsersResponse(ctx, h, key, users, false))
}

// buildAllUsersResponse adiciona flag has_access para cada user (cruza com
// crm_user_permissions). Retorno enxuto para o frontend.
func buildAllUsersResponse(ctx context.Context, h *handlers, domainKey string, users []bitrix.BitrixUser, fromCache bool) fiber.Map {
	perms, _ := h.repo.ListCrmPermissionsByDomain(ctx, domainKey)
	granted := map[string]bool{}
	for _, p := range perms {
		granted[p.UserID] = true
	}
	out := make([]fiber.Map, 0, len(users))
	for _, u := range users {
		full := strings.TrimSpace(u.Name + " " + u.LastName)
		if full == "" {
			full = "User #" + u.ID
		}
		out = append(out, fiber.Map{
			"id":         u.ID,
			"name":       full,
			"email":      u.Email,
			"position":   u.Position,
			"active":     u.Active,
			"has_access": granted[u.ID],
		})
	}
	return fiber.Map{
		"users":      out,
		"total":      len(out),
		"granted":    len(perms),
		"from_cache": fromCache,
	}
}

// GET /ui/permissions/user-info?user_id=N — busca info de 1 user no Bitrix.
func (h *handlers) uiPermissionsUserInfo(c *fiber.Ctx) error {
	userID := strings.TrimSpace(c.Query("user_id"))
	if userID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "user_id obrigatorio"})
	}
	ctx := c.Context()
	domain, err := h.resolveDashboardDomain(ctx, c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	portal, err := h.repo.GetBitrixPortalByDomain(ctx, domain)
	if err != nil || portal == nil {
		return c.Status(404).JSON(fiber.Map{"error": "portal nao encontrado: " + domain})
	}
	creds := h.portalToCreds(portal)
	users, err := h.bitrixClient.GetUserByIDs(ctx, creds, []string{userID})
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	if len(users) == 0 {
		return c.Status(404).JSON(fiber.Map{"error": "usuario " + userID + " nao encontrado"})
	}
	u := users[0]
	full := strings.TrimSpace(u.Name + " " + u.LastName)
	if full == "" {
		full = "User #" + u.ID
	}
	return c.JSON(fiber.Map{
		"id": u.ID, "name": full, "first": u.Name, "last": u.LastName,
		"email": u.Email, "position": u.Position, "active": u.Active,
	})
}

// POST /ui/permissions/grant — Body: {"user_id":"N","user_name":"X"}
func (h *handlers) uiPermissionsGrant(c *fiber.Ctx) error {
	return h.uiPermissionsMutate(c, true)
}

// POST /ui/permissions/revoke — Body: {"user_id":"N"}
func (h *handlers) uiPermissionsRevoke(c *fiber.Ctx) error {
	return h.uiPermissionsMutate(c, false)
}

func (h *handlers) uiPermissionsMutate(c *fiber.Ctx, grant bool) error {
	ctx := c.Context()
	domain, err := h.resolveDashboardDomain(ctx, c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	var req struct {
		UserID   string `json:"user_id"`
		UserName string `json:"user_name"`
	}
	if err := c.BodyParser(&req); err != nil || req.UserID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "user_id obrigatorio"})
	}
	key := strings.ToLower(domain)
	if grant {
		if err := h.repo.GrantCrmPermission(ctx, key, req.UserID, req.UserName, "dashboard"); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		h.log.Info("ui: granted CRM access",
			zap.String("domain", key), zap.String("user_id", req.UserID))
		return c.JSON(fiber.Map{"ok": true, "action": "granted"})
	}
	removed, err := h.repo.RevokeCrmPermission(ctx, key, req.UserID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.log.Info("ui: revoked CRM access",
		zap.String("domain", key), zap.String("user_id", req.UserID))
	return c.JSON(fiber.Map{"ok": true, "action": "revoked", "removed": removed})
}

// GET /bitrix/crm/check-access?domain=...&user_id=...
// Retorna { allowed: bool, configured: bool }
//   - configured: tenant tem pelo menos 1 user liberado (config foi feita)
//   - allowed: este user esta na lista
// JS do CRM tab chama isso antes de carregar a interface. Se !allowed,
// mostra pagina amigavel de "Sem permissao".
func (h *handlers) bitrixCRMCheckAccess(c *fiber.Ctx) error {
	domain := strings.TrimSpace(c.Query("domain"))
	userID := strings.TrimSpace(c.Query("user_id"))
	if domain == "" || userID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain e user_id obrigatorios"})
	}
	key := normalizeDomainKey(domain)
	allowed, total, err := h.repo.HasCrmAccess(c.Context(), key, userID)
	if err != nil {
		h.log.Error("crm check access failed", zap.Error(err))
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{
		"allowed":    allowed,
		"configured": total > 0,
	})
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
// Body: { "domain", "entity_type", "entity_id", "phone", "session_jid", "message", "line_id", "operator_name" }
func (h *handlers) bitrixCRMSend(c *fiber.Ctx) error {
	var body struct {
		Domain       string `json:"domain"`
		EntityType   string `json:"entity_type"`
		EntityID     string `json:"entity_id"`
		Phone        string `json:"phone"`
		SessionJID   string `json:"session_jid"`
		Message      string `json:"message"`
		LineID       int    `json:"line_id"`
		OperatorName string `json:"operator_name"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if body.Domain == "" || body.Phone == "" || body.SessionJID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "domain, phone e session_jid são obrigatórios"})
	}
	if body.Message == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "message é obrigatório"})
	}

	portal, err := h.repo.GetBitrixPortalByDomain(c.Context(), normalizePortalDomain(body.Domain))
	if err != nil || portal == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "portal não encontrado"})
	}
	creds := h.portalToCreds(portal)

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

	operatorName := body.OperatorName
	if operatorName == "" {
		operatorName = "UC Talk"
	}

	// 1. Busca o chat_id do Open Lines vinculado a este contato/lead/deal.
	//    Necessário para registrar a mensagem no Open Lines existente (não criar paralela).
	chatID := ""
	if body.EntityID != "" {
		bxEntityType := strings.ToUpper(body.EntityType)
		chatID, _ = h.bitrixClient.GetCRMChatLastID(c.Context(), creds, bxEntityType, body.EntityID)
		if chatID == "" {
			if chatsRaw, e := h.bitrixClient.GetCRMChats(c.Context(), creds, bxEntityType, body.EntityID); e == nil {
				chatID = extractChatID(chatsRaw)
			}
		}
	}

	// 2. Se há chat no Open Lines, registra a msg lá. O Bitrix roteia para o
	//    connector (ONIMCONNECTORMESSAGEADD) que enfileira o OutboundJob via
	//    bitrixConnectorEvent — então NÃO enfileiramos direto aqui (evita duplicar).
	//    Se não há chat ainda, enfileira direto pelo WhatsApp como fallback.
	if chatID != "" {
		if _, sendErr := h.bitrixClient.SendOperatorMessage(c.Context(), creds, chatID, body.Message); sendErr != nil {
			h.log.Warn("crm send: SendOperatorMessage failed, falling back to direct WA send",
				zap.String("chat_id", chatID), zap.Error(sendErr))
			// Fallback: se falhou no Open Lines, envia direto pelo WA para não perder a msg
			textWithPrefix := fmt.Sprintf("*%s:*\n%s", operatorName, body.Message)
			job := &queue.OutboundJob{
				SessionJID: body.SessionJID, ToJID: toJID, Text: textWithPrefix,
				BitrixConnector: connectorID, BitrixLine: lineID, OperatorName: operatorName,
			}
			if err := h.q.PushOutbound(c.Context(), job); err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "falha ao enfileirar: " + err.Error()})
			}
			return c.JSON(fiber.Map{"status": "queued", "to_jid": toJID, "chat_id": chatID})
		}
		h.log.Info("crm send: registered in Open Lines (Bitrix roteia para WA via connector)",
			zap.String("chat_id", chatID), zap.String("operator", operatorName))
		return c.JSON(fiber.Map{"status": "queued", "to_jid": toJID, "chat_id": chatID, "via": "openlines"})
	}

	// 3. Sem chat ainda — primeira mensagem do contato. Envia direto pelo WhatsApp
	//    com prefixo formatado igual ao Open Lines.
	textWithPrefix := fmt.Sprintf("*%s:*\n%s", operatorName, body.Message)
	job := &queue.OutboundJob{
		SessionJID:      body.SessionJID,
		ToJID:           toJID,
		Text:            textWithPrefix,
		BitrixConnector: connectorID,
		BitrixLine:      lineID,
		OperatorName:    operatorName,
	}
	if err := h.q.PushOutbound(c.Context(), job); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "falha ao enfileirar mensagem: " + err.Error()})
	}

	h.log.Info("crm send: no chat — sent direct to WhatsApp with prefix",
		zap.String("to_jid", toJID),
		zap.String("operator", operatorName),
	)
	return c.JSON(fiber.Map{"status": "queued", "to_jid": toJID, "via": "direct"})
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
	if phone != "" {
		phoneNorm := normalizeWAPhone(phone)
		localMsgs, dbErr := h.repo.GetMessagesByPhone(c.Context(), phoneNorm, limit)
		h.log.Info("crm history: local db query",
			zap.String("phone_raw", phone),
			zap.String("phone_norm", phoneNorm),
			zap.Int("count", len(localMsgs)),
			zap.Error(dbErr),
		)
		if dbErr == nil && len(localMsgs) > 0 {
			return c.JSON(fiber.Map{
				"messages": localMsgsToCRM(localMsgs),
				"count":    len(localMsgs),
				"source":   "local",
			})
		}
		// Se houver erro no banco, loga e faz fallback para Bitrix (nunca retorna erro ao cliente)
		if dbErr != nil {
			h.log.Error("crm history: local db error, falling back to bitrix", zap.String("phone", phoneNorm), zap.Error(dbErr))
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
		h.log.Warn("crm history: no chat_id found in bitrix", zap.String("entity_id", entityID))
		return c.JSON(fiber.Map{"messages": []interface{}{}, "count": 0, "source": "none"})
	}

	msgsRaw, err := h.bitrixClient.GetSessionHistory(c.Context(), creds, chatID, limit)
	if err != nil {
		h.log.Warn("crm history: GetSessionHistory failed, trying GetChatMessages", zap.String("chat_id", chatID), zap.Error(err))
		msgsRaw, err = h.bitrixClient.GetChatMessages(c.Context(), creds, chatID, limit)
		if err != nil {
			h.log.Error("crm history: both history methods failed", zap.String("chat_id", chatID), zap.Error(err))
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
// Preenche session_jid/session_phone/kind com base no lado "nosso" da msg:
//   - outbound: from_jid (a sessao enviou)
//   - inbound:  to_jid   (a sessao recebeu)
func localMsgsToCRM(msgs []db.Message) []crmMessage {
	out := make([]crmMessage, 0, len(msgs))
	for _, m := range msgs {
		msgType := string(m.MessageType)
		if msgType == "" {
			msgType = "text"
		}
		var ourJID string
		if m.Direction == db.DirOutbound {
			ourJID = m.FromJID
		} else {
			ourJID = m.ToJID
		}
		kind, phone := classifySessionJID(ourJID)
		out = append(out, crmMessage{
			ID:           m.WAMessageID,
			Direction:    string(m.Direction),
			Type:         msgType,
			Content:      m.Content,
			MediaURL:     m.MediaURL,
			MediaMime:    m.MediaMime,
			AuthorName:   m.AuthorName,
			Status:       string(m.Status),
			CreatedAt:    m.CreatedAt.Format("2006-01-02T15:04:05Z"),
			SessionJID:   ourJID,
			SessionPhone: phone,
			Kind:         kind,
		})
	}
	return out
}

// classifySessionJID extrai (kind, phone) de um session_jid do nosso lado.
// Aceita formatos:
//   - "cloud:1160607470462388@s.whatsapp.net" -> ("cloud", "1160607470462388")
//   - "cloud@s.whatsapp.net"                  -> ("cloud", "")        (lixo historico)
//   - "5519910001772:88@s.whatsapp.net"       -> ("qr", "5519910001772")
//   - "5519910001772@s.whatsapp.net"          -> ("qr", "5519910001772")
//   - ""                                       -> ("", "")
func classifySessionJID(jid string) (kind, phone string) {
	if jid == "" {
		return "", ""
	}
	if strings.HasPrefix(jid, "cloud:") {
		// cloud:<id>@s.whatsapp.net
		rest := strings.TrimPrefix(jid, "cloud:")
		if at := strings.Index(rest, "@"); at != -1 {
			rest = rest[:at]
		}
		return "cloud", rest
	}
	if strings.HasPrefix(jid, "cloud@") {
		return "cloud", "" // lixo do bug stripDeviceSuffix antigo
	}
	// QR: pode ter device suffix ":NN"
	at := strings.Index(jid, "@")
	if at == -1 {
		return "qr", jid
	}
	base := jid[:at]
	if colon := strings.Index(base, ":"); colon != -1 {
		base = base[:colon]
	}
	return "qr", base
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
	ID         string `json:"id"`
	Direction  string `json:"direction"` // inbound | outbound
	Type       string `json:"type"`
	Content    string `json:"content"`
	MediaURL   string `json:"media_url,omitempty"`
	MediaMime  string `json:"media_mime,omitempty"`
	AuthorID   string `json:"author_id,omitempty"`
	AuthorName string `json:"author_name,omitempty"`
	Status     string `json:"status"`
	CreatedAt  string `json:"created_at"`
	// Vinculo da conexao usada — permite separar chats por sessao quando o
	// mesmo contato falou com varios numeros do mesmo portal.
	SessionJID   string `json:"session_jid,omitempty"`   // ex: "cloud:1160...@s.whatsapp.net" ou "5519910001772@s.whatsapp.net"
	SessionPhone string `json:"session_phone,omitempty"` // numero E.164 da sessao (nosso lado)
	Kind         string `json:"kind,omitempty"`          // "cloud" | "qr"
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

// GET /bitrix/crm/debug?phone=5519987717792 — diagnóstico: estado do banco e mensagens salvas
func (h *handlers) bitrixCRMDebug(c *fiber.Ctx) error {
	phone := normalizeWAPhone(c.Query("phone", ""))

	stats, err := h.repo.DebugMessageStats(c.Context(), phone)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(stats)
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

/* tabs de vinculos (quando o mesmo contato falou em varios numeros) */
.session-tabs{display:flex;gap:0;border-bottom:1px solid #334155;background:#0f172a;padding:0 12px;overflow-x:auto;flex-shrink:0}
.session-tabs::-webkit-scrollbar{height:3px}
.session-tabs::-webkit-scrollbar-thumb{background:#334155;border-radius:3px}
.session-tab{padding:8px 14px;background:transparent;border:0;border-bottom:2px solid transparent;color:#64748b;font-size:11.5px;font-weight:600;cursor:pointer;white-space:nowrap;display:flex;align-items:center;gap:6px;font-family:inherit;letter-spacing:.02em}
.session-tab:hover:not(.active){color:#cbd5e1}
.session-tab.active{color:#e2e8f0;border-bottom-color:#3b82f6}
.session-tab .badge-kind{font-size:9px;padding:1px 5px;border-radius:6px;font-weight:700;letter-spacing:.04em}
.session-tab .badge-kind.cloud{background:rgba(59,130,246,.2);color:#60a5fa}
.session-tab .badge-kind.qr{background:rgba(37,211,102,.18);color:#25D366}
.session-tab .count{font-size:10px;color:#475569;font-weight:500}

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

    <!-- tabs por vinculo (oculto quando so 1 sessao) -->
    <div class="session-tabs" id="session-tabs" style="display:none"></div>

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

    // Info do operador logado via BX24.js — depois disso, checa permissao
    BX24.callMethod('profile', {}, function(res) {
      var u = res.data() || {};
      var name  = [u.NAME, u.LAST_NAME].filter(Boolean).join(' ') || u.ID || 'Operador';
      var email = u.EMAIL || '';
      var userID = String(u.ID || '');
      document.getElementById('op-name').textContent     = name;
      document.getElementById('op-email').textContent    = email;
      document.getElementById('op-initials').textContent = initials(name);

      // ── Checagem de permissao de acesso ao CRM tab ──
      if (!userID || !_domain) {
        // Sem user_id ou dominio nao consegue checar — bloqueia por seguranca
        showAccessDenied('Não foi possível identificar o usuário');
        return;
      }
      var checkUrl = _baseUrl + '/bitrix/crm/check-access'
        + '?domain=' + encodeURIComponent(_domain)
        + '&user_id=' + encodeURIComponent(userID);
      fetch(checkUrl).then(function(r){ return r.json(); }).then(function(data) {
        if (data.allowed) {
          // Permissao OK — segue fluxo normal
          loadSessions();
          loadEntity();
        } else {
          showAccessDenied(data.configured
            ? 'Seu usuário não tem permissão para acessar o CRM. Peça a um administrador da UC Technology para liberar seu acesso.'
            : 'O acesso ao CRM ainda não foi configurado neste portal. Entre em contato com a UC Technology para configurar permissões.');
        }
      }).catch(function(err) {
        showAccessDenied('Erro ao verificar permissões: ' + err.message);
      });
    });
  });
}

// Substitui a UI inteira por pagina amigavel de acesso negado.
function showAccessDenied(msg) {
  var html = ''
    + '<div style="display:flex;flex-direction:column;align-items:center;justify-content:center;height:100vh;padding:32px;text-align:center;background:#0f172a;color:#e2e8f0;font-family:-apple-system,system-ui,sans-serif">'
    +   '<div style="width:88px;height:88px;border-radius:50%;background:rgba(239,68,68,.12);display:flex;align-items:center;justify-content:center;margin-bottom:18px">'
    +     '<svg width="44" height="44" viewBox="0 0 24 24" fill="none" stroke="#ef4444" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>'
    +   '</div>'
    +   '<h2 style="margin:0 0 12px;font-size:18px;font-weight:700">Acesso negado</h2>'
    +   '<p style="max-width:480px;color:#94a3b8;line-height:1.6;font-size:14px;margin:0 0 24px">' + msg + '</p>'
    +   '<div style="font-size:11px;color:#475569">UC Talk &middot; suporte@uctechnology.com.br</div>'
    + '</div>';
  document.body.innerHTML = html;
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
  // Reseta cache de sessoes ao trocar de contato pra nao manter aba antiga ativa
  _msgsBySession = {};
  _activeSessionKey = '';
  document.getElementById('session-tabs').style.display = 'none';
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
          + '&limit=20';
  fetch(url)
    .then(function(r){ return r.json(); })
    .then(function(d) {
      var msgs = d.messages || [];
      if (!msgs.length) return;
      var lastId = msgs[msgs.length-1].id;
      if (lastId !== _lastMsgId) loadHistory();
    }).catch(function(){});
}

// Cache: msgs do contato atual, agrupadas por session_phone (lado nosso).
// Permite trocar de tab sem refetch.
var _msgsBySession = {}; // { "554220181520": [...], "5519910001772": [...] }
var _activeSessionKey = ''; // chave de _msgsBySession atualmente exibida

function renderHistory(msgs, chatID) {
  var body = document.getElementById('chat-body');
  var tabsEl = document.getElementById('session-tabs');
  if (!msgs.length) {
    tabsEl.style.display = 'none';
    body.innerHTML = '<div class="chat-placeholder"><svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M21 15a2 2 0 01-2 2H7l-4 4V5a2 2 0 012-2h14a2 2 0 012 2z"/></svg><p>Nenhuma mensagem ainda</p><span class="sub">Envie a primeira mensagem abaixo</span></div>';
    _msgsBySession = {};
    _activeSessionKey = '';
    return;
  }

  // ── Agrupa mensagens por (kind + phone) ──
  // Chave usada: 'cloud:<phone>' ou 'qr:<phone>'. Se phone vazio, usa 'unknown'.
  var groups = {}; // key -> { kind, phone, msgs[], lastTime }
  msgs.forEach(function(m) {
    var kind = m.kind || 'qr';
    var phone = m.session_phone || '';
    var key = kind + ':' + (phone || 'unknown');
    if (!groups[key]) groups[key] = { kind: kind, phone: phone, key: key, msgs: [], lastTime: 0 };
    groups[key].msgs.push(m);
    var t = parseDate(m.created_at).getTime();
    if (t > groups[key].lastTime) groups[key].lastTime = t;
  });
  _msgsBySession = groups;

  // ── Renderiza tabs (so se houver >1 sessao) ──
  var keys = Object.keys(groups);
  keys.sort(function(a, b) { return groups[b].lastTime - groups[a].lastTime; }); // mais recente primeiro

  // mantem aba ativa se ainda existe; senao usa a mais recente
  if (!_activeSessionKey || !groups[_activeSessionKey]) {
    _activeSessionKey = keys[0];
  }

  if (keys.length > 1) {
    var tabsHtml = '';
    keys.forEach(function(k) {
      var g = groups[k];
      var isActive = (k === _activeSessionKey);
      var kindLabel = g.kind === 'cloud' ? 'OFICIAL' : 'MULTI-DEVICE';
      var phoneLabel = g.phone ? '+' + g.phone : 'sem número';
      tabsHtml += '<button class="session-tab' + (isActive ? ' active' : '') + '" onclick="switchSessionTab(\'' + esc(k) + '\')">'
                + '<span class="badge-kind ' + g.kind + '">' + kindLabel + '</span>'
                + '<span>' + esc(phoneLabel) + '</span>'
                + '<span class="count">(' + g.msgs.length + ')</span>'
                + '</button>';
    });
    tabsEl.innerHTML = tabsHtml;
    tabsEl.style.display = 'flex';
  } else {
    tabsEl.style.display = 'none';
  }

  // ── Renderiza msgs da aba ativa ──
  renderSessionMessages();

  // Atualiza preview na sidebar usando a msg mais recente GLOBAL
  if (msgs.length) _lastMsgId = msgs[msgs.length-1].id;
  var last = msgs[msgs.length-1];
  var idx = _allConvs.findIndex(function(c){ return c.phone === _contactPhone; });
  if (idx >= 0) {
    _allConvs[idx].preview = last.content || mediaLabel(last.type);
    _allConvs[idx].time    = last.created_at ? parseDate(last.created_at).toLocaleTimeString('pt-BR',{hour:'2-digit',minute:'2-digit'}) : '';
    renderConvList();
  }
}

function switchSessionTab(key) {
  if (!_msgsBySession[key]) return;
  _activeSessionKey = key;
  // marca tab ativa
  document.querySelectorAll('.session-tab').forEach(function(el) {
    el.classList.remove('active');
  });
  // re-renderiza ambos os elementos
  var tabsEl = document.getElementById('session-tabs');
  Array.prototype.forEach.call(tabsEl.children, function(btn) {
    if (btn.getAttribute('onclick') && btn.getAttribute('onclick').indexOf(key) !== -1) {
      btn.classList.add('active');
    }
  });
  renderSessionMessages();
}

function renderSessionMessages() {
  var body = document.getElementById('chat-body');
  var group = _msgsBySession[_activeSessionKey];
  if (!group || !group.msgs.length) {
    body.innerHTML = '<div class="chat-placeholder"><p>Nenhuma mensagem neste vínculo</p></div>';
    return;
  }
  var html = '', lastDay = '';
  group.msgs.forEach(function(m) {
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
    var authorLabel = (isOut && m.author_name) ? '<div style="font-size:10px;color:#4ade80;margin-bottom:2px;font-weight:600">' + esc(m.author_name) + '</div>' : '';
    html += '<div class="bw ' + (isOut?'out':'in') + '"><div class="bubble ' + (isOut?'out':'in') + '">'
          + authorLabel + content
          + '<div class="bmeta"><span class="btime">' + time + '</span>' + st + '</div>'
          + '</div></div>';
  });
  body.innerHTML = html;
  body.scrollTop = body.scrollHeight;
}

function parseDate(s) {
  if (!s) return new Date();
  // Formato Bitrix: "DD.MM.YYYY HH:MM:SS"
  var m = s.match(/^(\d{2})\.(\d{2})\.(\d{4})\s+(\d{2}):(\d{2}):(\d{2})$/);
  if (m) return new Date(m[3]+'-'+m[2]+'-'+m[1]+'T'+m[4]+':'+m[5]+':'+m[6]);
  // Formato ISO (banco local): "2024-01-15T14:30:00Z"
  var d = new Date(s);
  if (!isNaN(d.getTime()) && d.getFullYear() > 2000) return d;
  return new Date();
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
                           phone:_contactPhone, session_jid:_activeSession, message:msg,
                           operator_name: document.getElementById('op-name').textContent || 'UC Talk' })
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

// GET/POST /bitrix-app — pagina servida no LEFT_MENU placement do Bitrix24.
// Mostra UI completa (lista de conversas + chat) para usuarios liberados.
// Checa permissao via mesma rota /bitrix/crm/check-access do CRM tab.
func (h *handlers) bitrixAppMenu(c *fiber.Ctx) error {
	return c.Type("html").SendString(bitrixAppMenuHTML)
}

// HTML simples que carrega BX24, valida o user_id contra check-access e
// embute o CRM tab via iframe (com modo "lista geral", sem entity_id).
const bitrixAppMenuHTML = `<!DOCTYPE html>
<html lang="pt-br">
<head>
<meta charset="utf-8">
<title>UC Talk</title>
<style>
  *{box-sizing:border-box}
  html,body{margin:0;padding:0;height:100%;background:#0f172a;color:#e2e8f0;font-family:-apple-system,system-ui,sans-serif}
  #wrap{height:100vh;display:flex;align-items:center;justify-content:center;padding:24px;text-align:center}
  .denied{max-width:480px}
  .denied .icon{width:88px;height:88px;border-radius:50%;background:rgba(239,68,68,.12);display:flex;align-items:center;justify-content:center;margin:0 auto 18px}
  .denied h2{margin:0 0 12px;font-size:18px;font-weight:700}
  .denied p{color:#94a3b8;line-height:1.6;font-size:14px;margin:0 0 24px}
  .denied .ft{font-size:11px;color:#475569}
  iframe{width:100%;height:100vh;border:0;display:block}
</style>
</head>
<body>
<div id="wrap"><div style="color:#64748b;font-size:14px">Carregando...</div></div>
<script src="https://api.bitrix24.com/api/v1/"></script>
<script>
var _baseUrl = window.location.origin;
BX24.init(function() {
  var domain = (BX24.getDomain ? BX24.getDomain() : '') || '';
  BX24.callMethod('profile', {}, function(res) {
    var u = res.data() || {};
    var userID = String(u.ID || '');
    if (!userID || !domain) {
      showDenied('Não foi possível identificar o usuário.');
      return;
    }
    fetch(_baseUrl + '/bitrix/crm/check-access?domain=' + encodeURIComponent(domain) + '&user_id=' + encodeURIComponent(userID))
      .then(function(r){ return r.json(); })
      .then(function(data) {
        if (data.allowed) {
          // Permissao OK — carrega o CRM tab em modo "menu" (sem entity_id especifico).
          document.getElementById('wrap').outerHTML =
            '<iframe src="' + _baseUrl + '/bitrix/crm/tab?menu=1&domain=' + encodeURIComponent(domain) + '&user_id=' + encodeURIComponent(userID) + '"></iframe>';
        } else {
          showDenied(data.configured
            ? 'Seu usuário não tem permissão para acessar o UC Talk. Peça a um administrador da UC Technology para liberar seu acesso.'
            : 'O acesso ao UC Talk ainda não foi configurado neste portal. Entre em contato com a UC Technology.');
        }
      })
      .catch(function(err) { showDenied('Erro ao verificar permissões: ' + err.message); });
  });
});

function showDenied(msg) {
  document.getElementById('wrap').innerHTML =
    '<div class="denied">'
    + '<div class="icon"><svg width="44" height="44" viewBox="0 0 24 24" fill="none" stroke="#ef4444" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg></div>'
    + '<h2>Acesso negado</h2>'
    + '<p>' + msg + '</p>'
    + '<div class="ft">UC Talk &middot; suporte@uctechnology.com.br</div>'
    + '</div>';
}
</script>
</body>
</html>`

// RegisterPlacementsForPortal registra placement.bind para os 3 tipos de entidade CRM
// e tambem para o menu lateral esquerdo. Chamado após instalar ou ativar o connector.
func (h *handlers) RegisterPlacementsForPortal(ctx context.Context, domain string, creds bitrix.TenantCreds) {
	base := h.cfg.App.BaseURL()
	tabURL := base + "/bitrix/crm/tab"
	appURL := base + "/bitrix-app"

	// CRM tabs (abas dentro de contato/lead/deal)
	crmPlacements := []string{
		"CRM_CONTACT_DETAIL_TAB",
		"CRM_LEAD_DETAIL_TAB",
		"CRM_DEAL_DETAIL_TAB",
	}
	for _, p := range crmPlacements {
		if err := h.bitrixClient.BindPlacement(ctx, creds, p, tabURL, "UC Talk"); err != nil {
			h.log.Warn("placement.bind failed",
				zap.String("placement", p),
				zap.String("domain", domain),
				zap.Error(err))
		} else {
			h.log.Info("placement.bind ok", zap.String("placement", p), zap.String("domain", domain))
		}
	}

	// Menu lateral esquerdo — item "UC Talk" para usuarios liberados acessarem
	// a interface fora do contexto de contato/lead/deal.
	if err := h.bitrixClient.BindPlacement(ctx, creds, "LEFT_MENU", appURL, "UC Talk"); err != nil {
		h.log.Warn("placement.bind LEFT_MENU failed",
			zap.String("domain", domain), zap.Error(err))
	} else {
		h.log.Info("placement.bind LEFT_MENU ok", zap.String("domain", domain))
	}
}
