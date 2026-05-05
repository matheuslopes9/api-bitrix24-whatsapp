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
<title>WhatsApp</title>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link href="https://fonts.googleapis.com/css2?family=Plus+Jakarta+Sans:wght@400;500;600;700&display=swap" rel="stylesheet">
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:'Plus Jakarta Sans',sans-serif;background:#0f172a;color:#e2e8f0;font-size:14px;min-height:100vh;padding:16px}
.card{background:#1e293b;border-radius:16px;padding:20px;border:1px solid #334155}
.logo{display:flex;align-items:center;gap:10px;margin-bottom:18px}
.logo svg{width:32px;height:32px}
.logo span{font-size:17px;font-weight:700;color:#25D366}
h2{font-size:14px;font-weight:600;color:#94a3b8;margin-bottom:14px;text-transform:uppercase;letter-spacing:.05em}
.info-row{display:flex;flex-direction:column;gap:4px;margin-bottom:16px}
.info-label{font-size:11px;color:#64748b;font-weight:500;text-transform:uppercase;letter-spacing:.06em}
.info-value{font-size:15px;font-weight:600;color:#f1f5f9}
label{display:block;font-size:12px;color:#94a3b8;font-weight:500;margin-bottom:6px;margin-top:14px}
input,select,textarea{width:100%;background:#0f172a;border:1px solid #334155;border-radius:10px;padding:10px 12px;color:#f1f5f9;font-size:14px;font-family:inherit;outline:none;transition:border-color .2s}
input:focus,select:focus,textarea:focus{border-color:#25D366}
textarea{resize:vertical;min-height:90px}
.btn{display:block;width:100%;margin-top:18px;padding:12px;background:#25D366;color:#fff;font-size:15px;font-weight:700;border:none;border-radius:12px;cursor:pointer;transition:opacity .2s;font-family:inherit}
.btn:hover{opacity:.88}
.btn:disabled{opacity:.5;cursor:not-allowed}
.badge{display:inline-flex;align-items:center;gap:6px;background:#14532d;color:#4ade80;padding:4px 10px;border-radius:99px;font-size:12px;font-weight:600}
.badge.offline{background:#450a0a;color:#f87171}
.badge.loading{background:#1e3a5f;color:#60a5fa}
.status-dot{width:7px;height:7px;border-radius:50%;background:currentColor}
.alert{margin-top:14px;padding:12px;border-radius:10px;font-size:13px}
.alert.success{background:#14532d;color:#4ade80;border:1px solid #166534}
.alert.error{background:#450a0a;color:#f87171;border:1px solid #7f1d1d}
.loading-txt{color:#64748b;font-size:13px;text-align:center;padding:20px 0}
.sep{height:1px;background:#334155;margin:16px 0}
</style>
</head>
<body>
<div class="card">
  <div class="logo">
    <svg viewBox="0 0 32 32" fill="none"><circle cx="16" cy="16" r="16" fill="#25D366"/><path d="M16 6C10.477 6 6 10.477 6 16c0 1.822.487 3.53 1.338 5.003L6 26l5.147-1.318A9.958 9.958 0 0016 26c5.523 0 10-4.477 10-10S21.523 6 16 6zm0 18a7.964 7.964 0 01-4.062-1.113l-.29-.173-3.057.783.811-2.978-.19-.306A7.96 7.96 0 018 16c0-4.411 3.589-8 8-8s8 3.589 8 8-3.589 8-8 8zm4.39-5.995c-.24-.12-1.42-.7-1.64-.78-.22-.08-.38-.12-.54.12-.16.24-.62.78-.76.94-.14.16-.28.18-.52.06-.24-.12-1.014-.374-1.932-1.192-.714-.636-1.196-1.422-1.337-1.662-.14-.24-.015-.37.106-.49.108-.108.24-.282.36-.423.12-.14.16-.24.24-.4.08-.16.04-.3-.02-.42-.06-.12-.54-1.3-.74-1.78-.195-.468-.393-.404-.54-.412l-.46-.008c-.16 0-.42.06-.64.3-.22.24-.84.82-.84 2s.86 2.32.98 2.48c.12.16 1.7 2.595 4.12 3.64.576.248 1.025.396 1.374.507.578.183 1.103.157 1.52.095.463-.069 1.42-.58 1.62-1.14.2-.56.2-1.04.14-1.14-.06-.1-.22-.16-.46-.28z" fill="#fff"/></svg>
    <span>WhatsApp UC</span>
  </div>

  <div id="loading" class="loading-txt">Carregando informações...</div>
  <div id="main" style="display:none">
    <div class="info-row">
      <span class="info-label">Contato</span>
      <span class="info-value" id="contact-name">—</span>
    </div>
    <div class="info-row">
      <span class="info-label">Número detectado</span>
      <span class="info-value" id="contact-phone">—</span>
    </div>

    <div class="sep"></div>
    <h2>Iniciar conversa</h2>

    <label>Número WhatsApp (com DDD e código do país)</label>
    <input type="tel" id="inp-phone" placeholder="Ex: +55 11 99999-9999">

    <label>Sessão WhatsApp</label>
    <select id="inp-session"><option value="">Carregando sessões...</option></select>

    <label>Fila de atendimento</label>
    <select id="inp-line"><option value="">Padrão</option></select>

    <label>Mensagem inicial</label>
    <textarea id="inp-msg" placeholder="Olá! Entramos em contato para..."></textarea>

    <button class="btn" id="btn-send" onclick="sendMsg()">Enviar mensagem</button>
    <div id="result"></div>
  </div>
</div>

<script src="https://api.bitrix24.com/api/v1/"></script>
<script>
var _domain = '';
var _entityType = '';
var _entityId = '';
var _baseUrl = window.location.origin;

function init() {
  BX24.init(function() {
    var p = BX24.placement.info();
    _entityType = (p.options && p.options.entityTypeName) ? p.options.entityTypeName.toLowerCase() : 'contact';
    _entityId   = (p.options && p.options.id) ? String(p.options.id) : '';
    _domain     = BX24.getDomain();

    if (!_entityId) {
      showError('Não foi possível identificar o contato/lead.');
      return;
    }

    loadEntity();
    loadSessions();
    loadLines();
  });
}

function loadEntity() {
  var url = _baseUrl + '/bitrix/crm/entity?domain=' + encodeURIComponent(_domain) +
            '&entity_type=' + _entityType + '&entity_id=' + _entityId;
  fetch(url)
    .then(function(r){ return r.json(); })
    .then(function(d){
      document.getElementById('loading').style.display = 'none';
      document.getElementById('main').style.display = 'block';
      document.getElementById('contact-name').textContent = d.name || '—';
      var ph = d.phone || '';
      document.getElementById('contact-phone').textContent = ph || '—';
      document.getElementById('inp-phone').value = ph;
    })
    .catch(function(e){
      showError('Erro ao carregar contato: ' + e);
    });
}

function loadSessions() {
  fetch(_baseUrl + '/bitrix/crm/sessions')
    .then(function(r){ return r.json(); })
    .then(function(d){
      var sel = document.getElementById('inp-session');
      sel.innerHTML = '';
      var sessions = d.sessions || [];
      if (sessions.length === 0) {
        sel.innerHTML = '<option value="">Nenhuma sessão conectada</option>';
        return;
      }
      sessions.forEach(function(jid){
        var opt = document.createElement('option');
        opt.value = jid;
        opt.textContent = jid;
        sel.appendChild(opt);
      });
    });
}

function loadLines() {
  if (!_domain) return;
  fetch(_baseUrl + '/bitrix/crm/lines?domain=' + encodeURIComponent(_domain))
    .then(function(r){ return r.json(); })
    .then(function(d){
      var sel = document.getElementById('inp-line');
      sel.innerHTML = '<option value="">Padrão</option>';
      var lines = Array.isArray(d) ? d : (d.result || []);
      lines.forEach(function(l){
        var id = l.ID || l.id;
        var name = l.LINE_NAME || l.name || ('Fila ' + id);
        var opt = document.createElement('option');
        opt.value = id;
        opt.textContent = name;
        sel.appendChild(opt);
      });
    })
    .catch(function(){});
}

function sendMsg() {
  var phone   = document.getElementById('inp-phone').value.trim();
  var session = document.getElementById('inp-session').value;
  var lineID  = parseInt(document.getElementById('inp-line').value) || 0;
  var msg     = document.getElementById('inp-msg').value.trim();

  if (!phone) { showResult('error','Informe o número do WhatsApp.'); return; }
  if (!session) { showResult('error','Selecione uma sessão WhatsApp conectada.'); return; }
  if (!msg)   { showResult('error','Escreva uma mensagem.'); return; }

  var btn = document.getElementById('btn-send');
  btn.disabled = true;
  btn.textContent = 'Enviando...';

  fetch(_baseUrl + '/bitrix/crm/send', {
    method: 'POST',
    headers: {'Content-Type':'application/json'},
    body: JSON.stringify({
      domain: _domain,
      entity_type: _entityType,
      entity_id: _entityId,
      phone: phone,
      session_jid: session,
      message: msg,
      line_id: lineID
    })
  })
  .then(function(r){ return r.json(); })
  .then(function(d){
    btn.disabled = false;
    btn.textContent = 'Enviar mensagem';
    if (d.status === 'queued') {
      showResult('success', '✓ Mensagem enviada para ' + phone);
      document.getElementById('inp-msg').value = '';
    } else {
      showResult('error', d.error || 'Erro desconhecido.');
    }
  })
  .catch(function(e){
    btn.disabled = false;
    btn.textContent = 'Enviar mensagem';
    showResult('error', 'Falha na requisição: ' + e);
  });
}

function showResult(type, text) {
  var el = document.getElementById('result');
  el.className = 'alert ' + type;
  el.textContent = text;
}

function showError(msg) {
  document.getElementById('loading').textContent = msg;
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
		if err := h.bitrixClient.BindPlacement(ctx, creds, p, tabURL, "WhatsApp"); err != nil {
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
