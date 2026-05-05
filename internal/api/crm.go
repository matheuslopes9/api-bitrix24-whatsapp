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
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/bitrix"
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
	if body.Domain == "" || body.Phone == "" || body.SessionJID == "" || body.Message == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "domain, phone, session_jid e message são obrigatórios",
		})
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

// GET /bitrix/crm/history?phone=5511999999999&limit=50 — histórico de mensagens com um número
func (h *handlers) bitrixCRMHistory(c *fiber.Ctx) error {
	phone := c.Query("phone")
	if phone == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "phone obrigatório"})
	}
	phone = normalizeWAPhone(phone)

	limit := 50
	if l := c.QueryInt("limit", 50); l > 0 && l <= 200 {
		limit = l
	}

	msgs, err := h.repo.GetMessagesByPhone(c.Context(), phone, limit)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	type msgDTO struct {
		ID          string `json:"id"`
		Direction   string `json:"direction"`
		MessageType string `json:"type"`
		Content     string `json:"content"`
		MediaURL    string `json:"media_url,omitempty"`
		MediaMime   string `json:"media_mime,omitempty"`
		Status      string `json:"status"`
		CreatedAt   string `json:"created_at"`
	}

	out := make([]msgDTO, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, msgDTO{
			ID:          m.WAMessageID,
			Direction:   string(m.Direction),
			MessageType: string(m.MessageType),
			Content:     m.Content,
			MediaURL:    m.MediaURL,
			MediaMime:   m.MediaMime,
			Status:      string(m.Status),
			CreatedAt:   m.CreatedAt.Format("2006-01-02T15:04:05Z"),
		})
	}

	return c.JSON(fiber.Map{"messages": out, "count": len(out)})
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
body{font-family:'Plus Jakarta Sans',sans-serif;background:#1e2736;color:#e2e8f0;font-size:13px;display:flex;flex-direction:column;height:100%}

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
.composer{background:#1e2736;border-top:1px solid #2d3a4e;padding:10px 14px;display:flex;gap:8px;align-items:flex-end;flex-shrink:0}
.composer textarea{flex:1;background:#252f3e;border:1px solid #334155;border-radius:12px;padding:9px 12px;color:#f1f5f9;font-size:13px;font-family:inherit;outline:none;resize:none;max-height:120px;min-height:38px;line-height:1.4;transition:border-color .2s}
.composer textarea:focus{border-color:#25D366}
.composer textarea::placeholder{color:#475569}
.composer textarea:disabled{opacity:.4}
.btn-send{background:#25D366;border:none;border-radius:50%;width:38px;height:38px;display:flex;align-items:center;justify-content:center;cursor:pointer;flex-shrink:0;transition:opacity .15s}
.btn-send:hover{opacity:.85}
.btn-send:disabled{opacity:.4;cursor:not-allowed}
.fila-row{padding:0 14px 8px;display:flex;align-items:center;gap:8px;flex-shrink:0;background:#1e2736}
.fila-row select{flex:1;background:#252f3e;border:1px solid #334155;border-radius:8px;padding:5px 8px;color:#f1f5f9;font-size:12px;font-family:inherit;outline:none}
.fila-row select:focus{border-color:#25D366}
.fila-label{font-size:10px;color:#64748b;font-weight:600;text-transform:uppercase;letter-spacing:.06em;white-space:nowrap}

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

    <!-- fila -->
    <div class="fila-row">
      <span class="fila-label">Fila:</span>
      <select id="sel-line"><option value="">Padrão</option></select>
    </div>

    <!-- compositor -->
    <div class="composer">
      <textarea id="msg-input" placeholder="Mensagem..." rows="1" disabled
        onkeydown="onKey(event)" oninput="autoResize(this)"></textarea>
      <button class="btn-send" id="btn-send" onclick="sendMsg()" title="Enviar" disabled>
        <svg width="18" height="18" viewBox="0 0 24 24" fill="white"><path d="M2 21l21-9L2 3v7l15 2-15 2z"/></svg>
      </button>
    </div>

  </div>
</div>

<script src="https://api.bitrix24.com/api/v1/"></script>
<script>
var _domain = '', _entityType = 'contact', _entityId = '';
var _contactName = '', _contactPhone = '', _contactInitials = '?';
var _baseUrl = window.location.origin;
var _sessions = [];       // [{jid, phone}]
var _activeSession = '';  // jid selecionado
var _pollTimer = null;
var _lastMsgId = '';
var _allConvs = [];       // [{name, phone, preview, time, unread}]

// ── Inicialização ────────────────────────────────────────────────────────
function init() {
  BX24.init(function() {
    var p = BX24.placement.info();
    _entityType = (p.options && p.options.entityTypeName) ? p.options.entityTypeName.toLowerCase() : 'contact';
    _entityId   = (p.options && p.options.id) ? String(p.options.id) : '';
    _domain     = BX24.getDomain ? BX24.getDomain() : (p.domain || '');

    // Info do operador logado via BX24.js
    BX24.callMethod('profile', {}, function(res) {
      var u = res.data() || {};
      var name  = [u.NAME, u.LAST_NAME].filter(Boolean).join(' ') || u.ID || 'Operador';
      var email = u.EMAIL || '';
      document.getElementById('op-name').textContent    = name;
      document.getElementById('op-email').textContent   = email;
      document.getElementById('op-initials').textContent = initials(name);
    });

    loadSessions();
    loadEntity();
    loadLines();
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
  if (!_entityId) { showConvOnly(); return; }
  var url = _baseUrl + '/bitrix/crm/entity?domain=' + enc(_domain) + '&entity_type=' + _entityType + '&entity_id=' + _entityId;
  fetch(url).then(r => r.json()).then(function(d) {
    _contactName   = d.name  || 'Contato';
    _contactPhone  = d.phone || '';
    _contactInitials = initials(_contactName);

    // Popula o contato atual no topo da lista (conversa ativa)
    var convs = [];
    if (_contactPhone) {
      convs.push({ name: _contactName, phone: _contactPhone, preview: '', time: '', unread: 0, current: true });
    }
    _allConvs = convs;
    renderConvList();
    if (_contactPhone) openChat(_contactName, _contactPhone);
  }).catch(function() {
    document.getElementById('op-name').textContent = 'Erro ao carregar';
  });
}

function showConvOnly() {
  // Sem entidade — mostra lista vazia
  renderConvList();
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
    var badge = c.unread ? '<span class="conv-badge">' + c.unread + '</span>' : '';
    return '<div class="conv-item' + (c.current ? ' active' : '') + '" onclick="openChat(' + JSON.stringify(c.name) + ',' + JSON.stringify(c.phone) + ')">'
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
  document.getElementById('chat-hdr-phone').textContent  = phone;
  document.getElementById('chat-hdr-avatar').textContent = initials(name);
  document.getElementById('msg-input').disabled  = false;
  document.getElementById('btn-send').disabled   = false;
  hideAlert();
  loadHistory();
  // marca conversa ativa
  document.querySelectorAll('.conv-item').forEach(function(el){ el.classList.remove('active'); });
  event && event.currentTarget && event.currentTarget.classList && event.currentTarget.classList.add('active');
}

function reloadChat() { if (_contactPhone) loadHistory(); }

// ── Histórico ─────────────────────────────────────────────────────────────
function loadHistory() {
  if (!_contactPhone) return;
  var ph = _contactPhone.replace(/\D/g,'');
  document.getElementById('chat-body').innerHTML = '<div class="chat-placeholder"><div class="spinner"></div></div>';
  fetch(_baseUrl + '/bitrix/crm/history?phone=' + ph + '&limit=80')
    .then(r => r.json())
    .then(function(d) { renderHistory(d.messages || []); })
    .catch(function() {
      document.getElementById('chat-body').innerHTML = '<div class="chat-placeholder"><p>Erro ao carregar histórico.</p></div>';
    });
  if (_pollTimer) clearInterval(_pollTimer);
  _pollTimer = setInterval(pollHistory, 6000);
}

function pollHistory() {
  if (!_contactPhone) return;
  var ph = _contactPhone.replace(/\D/g,'');
  fetch(_baseUrl + '/bitrix/crm/history?phone=' + ph + '&limit=3')
    .then(r => r.json())
    .then(function(d) {
      var msgs = d.messages || [];
      if (msgs.length && msgs[msgs.length-1].id !== _lastMsgId) loadHistory();
    }).catch(function(){});
}

function renderHistory(msgs) {
  var body = document.getElementById('chat-body');
  if (!msgs.length) {
    body.innerHTML = '<div class="chat-placeholder"><svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M21 15a2 2 0 01-2 2H7l-4 4V5a2 2 0 012-2h14a2 2 0 012 2z"/></svg><p>Nenhuma mensagem ainda</p><span class="sub">Envie a primeira mensagem abaixo</span></div>';
    return;
  }
  var html = '', lastDay = '';
  msgs.forEach(function(m) {
    var dt = new Date(m.created_at);
    var day = dt.toLocaleDateString('pt-BR',{day:'2-digit',month:'2-digit',year:'numeric'});
    if (day !== lastDay) { html += '<div class="day-div"><span>' + day + '</span></div>'; lastDay = day; }
    var isOut = m.direction === 'outbound';
    var time  = dt.toLocaleTimeString('pt-BR',{hour:'2-digit',minute:'2-digit'});
    var st = '';
    if (isOut) {
      if      (m.status==='delivered') st = '<span class="bst delivered">✓✓</span>';
      else if (m.status==='failed')    st = '<span class="bst failed">!</span>';
      else                             st = '<span class="bst sent">✓</span>';
    }
    var content = '';
    if (m.type && m.type !== 'text') {
      content = '<div class="bmedia">' + mediaIcon(m.type) + ' ' + mediaLabel(m.type) + '</div>';
      if (m.content) content += esc(m.content);
    } else {
      content = esc(m.content || '');
    }
    html += '<div class="bw ' + (isOut?'out':'in') + '"><div class="bubble ' + (isOut?'out':'in') + '">'
          + content
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
    _allConvs[idx].time    = new Date(last.created_at).toLocaleTimeString('pt-BR',{hour:'2-digit',minute:'2-digit'});
    renderConvList();
  }
}

// ── Envio ─────────────────────────────────────────────────────────────────
function sendMsg() {
  var msg    = document.getElementById('msg-input').value.trim();
  var lineID = parseInt(document.getElementById('sel-line').value) || 0;
  if (!_contactPhone) { showAlert('error','Selecione um contato.'); return; }
  if (!_activeSession){ showAlert('error','Selecione um número WhatsApp no topo.'); return; }
  if (!msg)            return;

  var btn = document.getElementById('btn-send');
  btn.disabled = true;
  fetch(_baseUrl + '/bitrix/crm/send', {
    method:'POST', headers:{'Content-Type':'application/json'},
    body: JSON.stringify({ domain:_domain, entity_type:_entityType, entity_id:_entityId,
                           phone:_contactPhone, session_jid:_activeSession, message:msg, line_id:lineID })
  })
  .then(r => r.json())
  .then(function(d) {
    btn.disabled = false;
    if (d.status === 'queued') {
      document.getElementById('msg-input').value = '';
      autoResize(document.getElementById('msg-input'));
      hideAlert();
      appendOptimistic(msg);
    } else {
      showAlert('error', d.error || 'Erro ao enviar.');
    }
  })
  .catch(function(e){ btn.disabled = false; showAlert('error','Falha: ' + e); });
}

function appendOptimistic(text) {
  var body = document.getElementById('chat-body');
  var ph = body.querySelector('.chat-placeholder');
  if (ph) ph.remove();
  var now  = new Date();
  var time = now.toLocaleTimeString('pt-BR',{hour:'2-digit',minute:'2-digit'});
  var el = document.createElement('div');
  el.className = 'bw out';
  el.innerHTML = '<div class="bubble out">' + esc(text) + '<div class="bmeta"><span class="btime">' + time + '</span><span class="bst sent">✓</span></div></div>';
  body.appendChild(el);
  body.scrollTop = body.scrollHeight;
}

// ── Open Lines ────────────────────────────────────────────────────────────
function loadLines() {
  if (!_domain) return;
  fetch(_baseUrl + '/bitrix/crm/lines?domain=' + enc(_domain)).then(r => r.json()).then(function(d) {
    var sel = document.getElementById('sel-line');
    sel.innerHTML = '<option value="">Padrão</option>';
    var lines = Array.isArray(d) ? d : (d.result || []);
    lines.forEach(function(l) {
      var id = l.ID||l.id, name = l.LINE_NAME||l.name||('Fila '+id);
      var opt = document.createElement('option');
      opt.value = id; opt.textContent = name;
      sel.appendChild(opt);
    });
  }).catch(function(){});
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
