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
body{font-family:'Plus Jakarta Sans',sans-serif;background:#111827;color:#e2e8f0;font-size:13px;display:flex;flex-direction:column;height:100%}

/* ── Cabeçalho ── */
.header{background:#1e293b;border-bottom:1px solid #334155;padding:10px 14px;display:flex;align-items:center;gap:10px;flex-shrink:0}
.header-avatar{width:36px;height:36px;border-radius:50%;background:#25D366;display:flex;align-items:center;justify-content:center;flex-shrink:0}
.header-avatar svg{width:20px;height:20px}
.header-info{flex:1;min-width:0}
.header-name{font-size:14px;font-weight:700;color:#f1f5f9;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.header-phone{font-size:11px;color:#64748b;margin-top:1px}
.header-actions{display:flex;align-items:center;gap:6px}
.btn-icon{background:none;border:none;cursor:pointer;color:#64748b;padding:4px;border-radius:6px;transition:color .15s,background .15s;display:flex;align-items:center}
.btn-icon:hover{color:#e2e8f0;background:#334155}
.status-badge{font-size:10px;font-weight:600;padding:3px 8px;border-radius:99px;display:inline-flex;align-items:center;gap:4px}
.status-badge.online{background:#14532d;color:#4ade80}
.status-badge.offline{background:#450a0a;color:#f87171}
.status-dot{width:6px;height:6px;border-radius:50%;background:currentColor}

/* ── Config bar (sessão / fila) ── */
.config-bar{background:#1a2436;border-bottom:1px solid #1e293b;padding:6px 12px;display:flex;gap:8px;align-items:center;flex-shrink:0;flex-wrap:wrap}
.config-bar select{flex:1;min-width:120px;background:#0f172a;border:1px solid #334155;border-radius:8px;padding:5px 8px;color:#f1f5f9;font-size:12px;font-family:inherit;outline:none}
.config-bar select:focus{border-color:#25D366}
.config-label{font-size:10px;color:#64748b;font-weight:600;text-transform:uppercase;letter-spacing:.06em;white-space:nowrap}

/* ── Histórico de mensagens ── */
.chat-body{flex:1;overflow-y:auto;padding:14px 12px;display:flex;flex-direction:column;gap:6px;background:#111827}
.chat-body::-webkit-scrollbar{width:4px}
.chat-body::-webkit-scrollbar-track{background:transparent}
.chat-body::-webkit-scrollbar-thumb{background:#334155;border-radius:4px}

.chat-empty{flex:1;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:8px;color:#475569}
.chat-empty svg{opacity:.4}
.chat-empty p{font-size:13px}

.day-divider{text-align:center;margin:6px 0}
.day-divider span{background:#1e293b;color:#64748b;font-size:10px;font-weight:600;padding:3px 10px;border-radius:99px}

.bubble-wrap{display:flex;flex-direction:column;max-width:78%}
.bubble-wrap.out{align-self:flex-end;align-items:flex-end}
.bubble-wrap.in{align-self:flex-start;align-items:flex-start}

.bubble{padding:8px 11px;border-radius:14px;font-size:13px;line-height:1.45;word-break:break-word;position:relative}
.bubble.out{background:#005c4b;color:#e2e8f0;border-bottom-right-radius:4px}
.bubble.in{background:#1e293b;color:#e2e8f0;border-bottom-left-radius:4px}
.bubble-meta{display:flex;align-items:center;gap:4px;margin-top:3px}
.bubble-time{font-size:10px;color:#64748b}
.bubble-status{font-size:10px;display:flex;align-items:center}
.bubble-status .s-sent{color:#94a3b8}
.bubble-status .s-delivered{color:#94a3b8}
.bubble-status .s-read{color:#53bdeb}
.bubble-media{display:flex;align-items:center;gap:6px;color:#94a3b8;font-size:12px;font-style:italic}
.bubble-media svg{flex-shrink:0}

/* ── Área de digitação ── */
.composer{background:#1e293b;border-top:1px solid #334155;padding:10px 12px;display:flex;gap:8px;align-items:flex-end;flex-shrink:0}
.composer textarea{flex:1;background:#0f172a;border:1px solid #334155;border-radius:12px;padding:9px 12px;color:#f1f5f9;font-size:13px;font-family:inherit;outline:none;resize:none;max-height:120px;min-height:38px;line-height:1.4;transition:border-color .2s}
.composer textarea:focus{border-color:#25D366}
.composer textarea::placeholder{color:#475569}
.btn-send{background:#25D366;border:none;border-radius:50%;width:38px;height:38px;display:flex;align-items:center;justify-content:center;cursor:pointer;flex-shrink:0;transition:opacity .15s}
.btn-send:hover{opacity:.85}
.btn-send:disabled{opacity:.4;cursor:not-allowed}

/* ── Loading / error ── */
.fullscreen-msg{flex:1;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:8px;color:#64748b;font-size:13px;text-align:center;padding:20px}
.spinner{width:24px;height:24px;border:2px solid #334155;border-top-color:#25D366;border-radius:50%;animation:spin .7s linear infinite}
@keyframes spin{to{transform:rotate(360deg)}}
.alert{padding:8px 12px;border-radius:8px;font-size:12px;margin:6px 12px;flex-shrink:0}
.alert.error{background:#450a0a;color:#f87171;border:1px solid #7f1d1d}
.alert.success{background:#14532d;color:#4ade80;border:1px solid #166534}
</style>
</head>
<body>

<!-- Cabeçalho -->
<div class="header">
  <div class="header-avatar">
    <svg viewBox="0 0 24 24" fill="white"><path d="M12 2C6.477 2 2 6.477 2 12c0 1.82.487 3.53 1.338 5.003L2 22l5.147-1.318A9.96 9.96 0 0012 22c5.523 0 10-4.477 10-10S17.523 2 12 2zm3.39 13.005c-.24-.12-1.42-.7-1.64-.78-.22-.08-.38-.12-.54.12-.16.24-.62.78-.76.94-.14.16-.28.18-.52.06-.24-.12-1.014-.374-1.932-1.192-.714-.636-1.196-1.422-1.337-1.662-.14-.24-.015-.37.106-.49.108-.108.24-.282.36-.423.12-.14.16-.24.24-.4.08-.16.04-.3-.02-.42-.06-.12-.54-1.3-.74-1.78-.195-.468-.393-.404-.54-.412l-.46-.008c-.16 0-.42.06-.64.3-.22.24-.84.82-.84 2s.86 2.32.98 2.48c.12.16 1.7 2.595 4.12 3.64.576.248 1.025.396 1.374.507.578.183 1.103.157 1.52.095.463-.069 1.42-.58 1.62-1.14.2-.56.2-1.04.14-1.14-.06-.1-.22-.16-.46-.28z"/></svg>
  </div>
  <div class="header-info">
    <div class="header-name" id="hdr-name">Carregando...</div>
    <div class="header-phone" id="hdr-phone"></div>
  </div>
  <div class="header-actions">
    <span class="status-badge offline" id="wa-badge"><span class="status-dot"></span><span id="wa-badge-txt">—</span></span>
    <button class="btn-icon" title="Atualizar" onclick="refresh()">
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="23 4 23 10 17 10"/><polyline points="1 20 1 14 7 14"/><path d="M3.51 9a9 9 0 0114.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0020.49 15"/></svg>
    </button>
  </div>
</div>

<!-- Seletor de sessão / fila -->
<div class="config-bar">
  <span class="config-label">Via:</span>
  <select id="sel-session" onchange="onSessionChange()"><option value="">Carregando...</option></select>
  <span class="config-label">Fila:</span>
  <select id="sel-line"><option value="">Padrão</option></select>
</div>

<!-- Alerta de erro/sucesso global -->
<div id="global-alert" style="display:none" class="alert"></div>

<!-- Área de chat -->
<div class="chat-body" id="chat-body">
  <div class="fullscreen-msg" id="chat-loading"><div class="spinner"></div><p>Carregando histórico...</p></div>
</div>

<!-- Compositor -->
<div class="composer">
  <textarea id="msg-input" placeholder="Digite uma mensagem..." rows="1"
    onkeydown="onKey(event)" oninput="autoResize(this)"></textarea>
  <button class="btn-send" id="btn-send" onclick="sendMsg()" title="Enviar">
    <svg width="18" height="18" viewBox="0 0 24 24" fill="white"><path d="M2 21l21-9L2 3v7l15 2-15 2z"/></svg>
  </button>
</div>

<script src="https://api.bitrix24.com/api/v1/"></script>
<script>
var _domain = '', _entityType = '', _entityId = '', _phone = '', _contactName = '';
var _baseUrl = window.location.origin;
var _pollTimer = null;
var _lastMsgId = '';

function init() {
  BX24.init(function() {
    var p = BX24.placement.info();
    _entityType = (p.options && p.options.entityTypeName) ? p.options.entityTypeName.toLowerCase() : 'contact';
    _entityId   = (p.options && p.options.id) ? String(p.options.id) : '';
    _domain     = BX24.getDomain ? BX24.getDomain() : (p.domain || '');

    if (!_entityId) {
      showChat('<div class="fullscreen-msg"><p>Não foi possível identificar o contato.</p></div>');
      return;
    }
    loadAll();
  });
}

function loadAll() {
  loadEntity();
  loadSessions();
  loadLines();
}

function refresh() {
  loadEntity();
  loadHistory();
}

function loadEntity() {
  var url = _baseUrl + '/bitrix/crm/entity?domain=' + enc(_domain) + '&entity_type=' + _entityType + '&entity_id=' + _entityId;
  fetch(url).then(r => r.json()).then(function(d) {
    _contactName = d.name || '';
    _phone = d.phone || '';
    document.getElementById('hdr-name').textContent  = _contactName || 'Contato';
    document.getElementById('hdr-phone').textContent = _phone || '';
    if (_phone) loadHistory();
    else showChat('<div class="fullscreen-msg"><p>Nenhum telefone cadastrado neste contato.</p></div>');
  }).catch(function() {
    document.getElementById('hdr-name').textContent = 'Erro ao carregar';
  });
}

function loadSessions() {
  fetch(_baseUrl + '/bitrix/crm/sessions').then(r => r.json()).then(function(d) {
    var sel = document.getElementById('sel-session');
    sel.innerHTML = '';
    var sessions = d.sessions || [];
    if (!sessions.length) {
      sel.innerHTML = '<option value="">Nenhuma sessão</option>';
      setWABadge(false, 'Desconectado');
      return;
    }
    sessions.forEach(function(jid) {
      var opt = document.createElement('option');
      opt.value = jid;
      opt.textContent = jid.split('@')[0] || jid;
      sel.appendChild(opt);
    });
    setWABadge(true, sel.options[0] ? sel.options[0].textContent : 'Conectado');
  });
}

function onSessionChange() {
  var sel = document.getElementById('sel-session');
  var jid = sel.value;
  setWABadge(!!jid, jid ? (jid.split('@')[0]) : 'Desconectado');
}

function setWABadge(online, label) {
  var b = document.getElementById('wa-badge');
  b.className = 'status-badge ' + (online ? 'online' : 'offline');
  document.getElementById('wa-badge-txt').textContent = label;
}

function loadLines() {
  if (!_domain) return;
  fetch(_baseUrl + '/bitrix/crm/lines?domain=' + enc(_domain)).then(r => r.json()).then(function(d) {
    var sel = document.getElementById('sel-line');
    sel.innerHTML = '<option value="">Padrão</option>';
    var lines = Array.isArray(d) ? d : (d.result || []);
    lines.forEach(function(l) {
      var id = l.ID || l.id, name = l.LINE_NAME || l.name || ('Fila ' + id);
      var opt = document.createElement('option');
      opt.value = id; opt.textContent = name;
      sel.appendChild(opt);
    });
  }).catch(function(){});
}

function loadHistory() {
  if (!_phone) return;
  var ph = _phone.replace(/\D/g, '');
  fetch(_baseUrl + '/bitrix/crm/history?phone=' + ph + '&limit=80')
    .then(r => r.json())
    .then(function(d) {
      renderHistory(d.messages || []);
    })
    .catch(function() {
      showChat('<div class="fullscreen-msg"><p>Erro ao carregar histórico.</p></div>');
    });
  if (!_pollTimer) {
    _pollTimer = setInterval(function() { pollHistory(); }, 5000);
  }
}

function pollHistory() {
  if (!_phone) return;
  var ph = _phone.replace(/\D/g, '');
  fetch(_baseUrl + '/bitrix/crm/history?phone=' + ph + '&limit=5')
    .then(r => r.json())
    .then(function(d) {
      var msgs = d.messages || [];
      if (!msgs.length) return;
      var newest = msgs[msgs.length - 1];
      if (newest.id !== _lastMsgId) loadHistory();
    }).catch(function(){});
}

function renderHistory(msgs) {
  var body = document.getElementById('chat-body');
  if (!msgs || !msgs.length) {
    body.innerHTML = '<div class="chat-empty"><svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M21 15a2 2 0 01-2 2H7l-4 4V5a2 2 0 012-2h14a2 2 0 012 2z"/></svg><p>Nenhuma mensagem ainda</p><p style="font-size:11px;color:#334155">Envie a primeira mensagem abaixo</p></div>';
    return;
  }

  var html = '';
  var lastDay = '';
  msgs.forEach(function(m) {
    var dt = new Date(m.created_at);
    var day = dt.toLocaleDateString('pt-BR', {day:'2-digit',month:'2-digit',year:'numeric'});
    if (day !== lastDay) {
      html += '<div class="day-divider"><span>' + day + '</span></div>';
      lastDay = day;
    }
    var isOut = m.direction === 'outbound';
    var time = dt.toLocaleTimeString('pt-BR', {hour:'2-digit',minute:'2-digit'});
    var statusIcon = '';
    if (isOut) {
      if (m.status === 'delivered') statusIcon = '<span class="s-delivered">✓✓</span>';
      else if (m.status === 'failed') statusIcon = '<span style="color:#f87171">!</span>';
      else statusIcon = '<span class="s-sent">✓</span>';
    }
    var content = '';
    if (m.type !== 'text' && m.type !== '') {
      var icon = mediaIcon(m.type);
      content = '<div class="bubble-media">' + icon + ' <span>' + mediaLabel(m.type) + '</span></div>';
      if (m.content) content += '<div style="margin-top:4px">' + esc(m.content) + '</div>';
    } else {
      content = esc(m.content || '');
    }
    html += '<div class="bubble-wrap ' + (isOut ? 'out' : 'in') + '">'
           + '<div class="bubble ' + (isOut ? 'out' : 'in') + '">' + content
           + '<div class="bubble-meta"><span class="bubble-time">' + time + '</span>'
           + (isOut ? '<span class="bubble-status">' + statusIcon + '</span>' : '')
           + '</div></div></div>';
  });

  body.innerHTML = html;
  body.scrollTop = body.scrollHeight;
  if (msgs.length) _lastMsgId = msgs[msgs.length - 1].id;
}

function sendMsg() {
  var msg     = document.getElementById('msg-input').value.trim();
  var session = document.getElementById('sel-session').value;
  var lineID  = parseInt(document.getElementById('sel-line').value) || 0;
  var phone   = _phone;

  if (!phone)   { showAlert('error', 'Nenhum telefone detectado neste contato.'); return; }
  if (!session) { showAlert('error', 'Selecione uma sessão WhatsApp conectada.'); return; }
  if (!msg)     { return; }

  var btn = document.getElementById('btn-send');
  btn.disabled = true;

  fetch(_baseUrl + '/bitrix/crm/send', {
    method: 'POST',
    headers: {'Content-Type':'application/json'},
    body: JSON.stringify({ domain:_domain, entity_type:_entityType, entity_id:_entityId,
                           phone:phone, session_jid:session, message:msg, line_id:lineID })
  })
  .then(r => r.json())
  .then(function(d) {
    btn.disabled = false;
    if (d.status === 'queued') {
      document.getElementById('msg-input').value = '';
      autoResize(document.getElementById('msg-input'));
      hideAlert();
      // Adiciona a mensagem localmente enquanto o polling não chega
      appendOptimistic(msg);
    } else {
      showAlert('error', d.error || 'Erro ao enviar.');
    }
  })
  .catch(function(e) {
    btn.disabled = false;
    showAlert('error', 'Falha na conexão: ' + e);
  });
}

function appendOptimistic(text) {
  var body = document.getElementById('chat-body');
  // Remove empty state se existir
  var empty = body.querySelector('.chat-empty,.fullscreen-msg');
  if (empty) empty.remove();

  var now = new Date();
  var time = now.toLocaleTimeString('pt-BR', {hour:'2-digit',minute:'2-digit'});
  var el = document.createElement('div');
  el.className = 'bubble-wrap out';
  el.innerHTML = '<div class="bubble out">' + esc(text)
    + '<div class="bubble-meta"><span class="bubble-time">' + time + '</span>'
    + '<span class="bubble-status"><span class="s-sent">✓</span></span></div></div>';
  body.appendChild(el);
  body.scrollTop = body.scrollHeight;
}

function onKey(e) {
  if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); sendMsg(); }
}

function autoResize(el) {
  el.style.height = 'auto';
  el.style.height = Math.min(el.scrollHeight, 120) + 'px';
}

function showChat(html) {
  document.getElementById('chat-body').innerHTML = html;
}

function showAlert(type, msg) {
  var el = document.getElementById('global-alert');
  el.className = 'alert ' + type;
  el.textContent = msg;
  el.style.display = 'block';
}

function hideAlert() {
  document.getElementById('global-alert').style.display = 'none';
}

function enc(s) { return encodeURIComponent(s); }

function esc(s) {
  return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/\n/g,'<br>');
}

function mediaIcon(type) {
  var icons = {
    image: '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="8.5" cy="8.5" r="1.5"/><polyline points="21 15 16 10 5 21"/></svg>',
    video: '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polygon points="23 7 16 12 23 17 23 7"/><rect x="1" y="5" width="15" height="14" rx="2"/></svg>',
    audio: '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M9 18V5l12-2v13"/><circle cx="6" cy="18" r="3"/><circle cx="18" cy="16" r="3"/></svg>',
    document: '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>',
    sticker: '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><path d="M8 13s1.5 2 4 2 4-2 4-2"/><line x1="9" y1="9" x2="9.01" y2="9"/><line x1="15" y1="9" x2="15.01" y2="9"/></svg>'
  };
  return icons[type] || icons.document;
}

function mediaLabel(type) {
  var labels = {image:'Imagem',video:'Vídeo',audio:'Áudio',document:'Documento',sticker:'Sticker'};
  return labels[type] || 'Arquivo';
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
