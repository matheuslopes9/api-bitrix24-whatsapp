package api

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"
)

// GET /connect
func (h *handlers) connectPage(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/html; charset=utf-8")
	return c.SendString(connectHTML)
}

// POST /ui/sessions
func (h *handlers) uiStartSession(c *fiber.Ctx) error {
	var body struct {
		Phone string `json:"phone"`
	}
	if err := c.BodyParser(&body); err != nil || body.Phone == "" {
		return c.Status(400).JSON(fiber.Map{"error": "phone required"})
	}
	if err := h.waManager.AddSession(c.Context(), body.Phone); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"status": "connecting", "phone": body.Phone})
}

// GET /ui/sessions/:phone/qr
func (h *handlers) uiGetQR(c *fiber.Ctx) error {
	phone := c.Params("phone")
	sessions := h.waManager.ListSessions()
	for _, jid := range sessions {
		if strings.HasPrefix(jid, phone) {
			return c.JSON(fiber.Map{"status": "connected", "jid": jid})
		}
	}
	qr := h.waManager.GetQR(phone)
	if qr == "" {
		return c.JSON(fiber.Map{"status": "waiting", "qr": ""})
	}
	return c.JSON(fiber.Map{"status": "ready", "qr": qr})
}

// GET /ui/sessions
// Retorna todas as sessões ativas (QR Code + Cloud API) com seus tipos.
// Mantém retrocompatibilidade: campo "sessions" continua sendo []string com JIDs.
// Adiciona "details" com objetos {jid, type, phone, label}.
func (h *handlers) uiListSessions(c *fiber.Ctx) error {
	qrJIDs := h.waManager.ListSessions()
	type sessionDetail struct {
		JID       string `json:"jid"`
		SessionID string `json:"session_id"`
		Type      string `json:"type"` // "qr" | "cloud_api"
		Phone     string `json:"phone"`
		Label     string `json:"label"`
	}
	allJIDs := make([]string, 0, len(qrJIDs))
	allJIDs = append(allJIDs, qrJIDs...)
	details := make([]sessionDetail, 0, len(qrJIDs))

	for _, jid := range qrJIDs {
		phone := jid
		if at := indexAt(phone); at != -1 {
			phone = phone[:at]
		}
		if c := indexColon(phone); c != -1 {
			phone = phone[:c]
		}
		details = append(details, sessionDetail{JID: jid, Type: "qr", Phone: phone, Label: "+" + phone})
	}

	if h.cloudMgr != nil {
		for _, jid := range h.cloudMgr.ListJIDs() {
			allJIDs = append(allJIDs, jid)
			label := jid
			phone := ""
			sessionID := ""
			if s, ok := h.cloudMgr.Get(jid); ok {
				phone = s.DisplayPhone
				if phone != "" {
					label = "+" + phone + " (Oficial)"
				} else {
					label = "Cloud " + s.PhoneNumberID
				}
			}
			// Busca o session_id (UUID) no banco
			if dbSess, err := h.repo.GetSessionByJID(c.Context(), jid); err == nil && dbSess != nil {
				sessionID = dbSess.ID.String()
			}
			details = append(details, sessionDetail{
				JID:       jid,
				SessionID: sessionID,
				Type:      "cloud_api",
				Phone:     phone,
				Label:     label,
			})
		}
	}

	h.log.Info("uiListSessions called", zap.Int("count", len(allJIDs)))
	return c.JSON(fiber.Map{
		"sessions": allJIDs,
		"details":  details,
		"count":    len(allJIDs),
	})
}

func indexAt(s string) int {
	for i, c := range s {
		if c == '@' {
			return i
		}
	}
	return -1
}
func indexColon(s string) int {
	for i, c := range s {
		if c == ':' {
			return i
		}
	}
	return -1
}

// DELETE /ui/sessions/:jid
// O JID pode conter '@' e ':' — lê também via query param ?jid= como fallback seguro.
// Despacha para o manager correto baseado no prefixo do JID.
func (h *handlers) uiDisconnectSession(c *fiber.Ctx) error {
	jid := c.Query("jid")
	if jid == "" {
		jid = c.Params("jid")
	}
	if jid == "" {
		return c.Status(400).JSON(fiber.Map{"error": "jid required"})
	}
	// Cloud API
	if isCloudJID(jid) {
		if h.cloudMgr != nil {
			if err := h.cloudMgr.RemoveSession(c.Context(), jid); err != nil {
				return c.Status(500).JSON(fiber.Map{"error": err.Error()})
			}
		}
		return c.JSON(fiber.Map{"status": "disconnected", "jid": jid, "type": "cloud_api"})
	}
	// QR (whatsmeow)
	h.waManager.Disconnect(jid)
	return c.JSON(fiber.Map{"status": "disconnected", "jid": jid, "type": "qr"})
}

func isCloudJID(jid string) bool {
	return len(jid) > 6 && jid[:6] == "cloud:"
}

const connectHTML = `<!DOCTYPE html>
<html lang="pt-BR">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>WhatsApp Connector</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;background:#f0f2f5;min-height:100vh;display:flex;align-items:center;justify-content:center;padding:20px}
.card{background:#fff;border-radius:16px;box-shadow:0 4px 24px rgba(0,0,0,.1);padding:36px 40px;max-width:500px;width:100%;text-align:center}
.logo{width:56px;height:56px;background:#25D366;border-radius:50%;display:flex;align-items:center;justify-content:center;margin:0 auto 18px}
.logo svg{width:30px;height:30px;fill:#fff}
h1{font-size:22px;color:#111;margin-bottom:6px}
.subtitle{color:#666;font-size:14px;margin-bottom:28px}
.form-row{display:flex;gap:10px;margin-bottom:20px}
input{flex:1;padding:11px 14px;border:1.5px solid #ddd;border-radius:8px;font-size:15px;outline:none;transition:border-color .2s}
input:focus{border-color:#25D366}
.btn{padding:11px 18px;background:#25D366;color:#fff;border:none;border-radius:8px;font-size:15px;font-weight:600;cursor:pointer;white-space:nowrap;transition:background .2s}
.btn:hover{background:#1ebe5d}
.btn:disabled{background:#aaa;cursor:not-allowed}
.btn-red{background:#fff;color:#e53935;border:1.5px solid #e53935;padding:7px 14px;border-radius:6px;font-size:13px;font-weight:600;cursor:pointer;transition:background .2s,color .2s}
.btn-red:hover{background:#e53935;color:#fff}
#qr-section{display:none;margin-bottom:20px}
.instructions{background:#f8f9fa;border-radius:8px;padding:12px 16px;text-align:left;margin-bottom:16px;font-size:13px;color:#555;line-height:1.7}
.instructions b{color:#333}
.badge{display:inline-flex;align-items:center;gap:6px;padding:6px 14px;border-radius:20px;font-size:13px;font-weight:600;margin-bottom:14px}
.badge-wait{background:#fff3cd;color:#856404}
.badge-ready{background:#d1ecf1;color:#0c5460}
.badge-ok{background:#d4edda;color:#155724}
.timer{font-size:12px;color:#888;margin-top:8px;min-height:18px}
#qr-wrap{display:inline-block;padding:10px;background:#fff;border:1px solid #eee;border-radius:8px;margin-bottom:4px}
#qr-wrap img{display:block}
.divider{border:none;border-top:1px solid #eee;margin:20px 0}
.sessions-title{font-size:14px;font-weight:600;color:#444;text-align:left;margin-bottom:12px}
.session-item{display:flex;align-items:center;justify-content:space-between;background:#f8f9fa;border-radius:8px;padding:10px 14px;margin-bottom:8px;gap:12px}
.session-info{display:flex;align-items:center;gap:8px;min-width:0}
.dot{width:9px;height:9px;border-radius:50%;background:#25D366;flex-shrink:0}
.jid{font-size:12px;color:#333;word-break:break-all}
.no-sessions{font-size:13px;color:#999;text-align:center;padding:12px 0}
.modal-overlay{display:none;position:fixed;inset:0;background:rgba(0,0,0,.45);z-index:50;align-items:center;justify-content:center;padding:16px}
.modal-overlay.open{display:flex}
.modal-box{background:#fff;border-radius:16px;box-shadow:0 8px 32px rgba(0,0,0,.18);padding:28px 24px;max-width:320px;width:100%;text-align:center}
.modal-icon{width:44px;height:44px;background:#fef2f2;border-radius:50%;display:flex;align-items:center;justify-content:center;margin:0 auto 14px}
.modal-title{font-size:16px;font-weight:700;color:#111;margin-bottom:6px}
.modal-sub{font-size:13px;color:#666;margin-bottom:22px;line-height:1.5}
.modal-actions{display:flex;gap:10px}
.modal-cancel{flex:1;padding:10px;background:#f3f4f6;border:none;border-radius:8px;font-size:14px;font-weight:600;cursor:pointer;color:#555}
.modal-cancel:hover{background:#e5e7eb}
.modal-confirm{flex:1;padding:10px;background:#e53935;border:none;border-radius:8px;font-size:14px;font-weight:600;cursor:pointer;color:#fff}
.modal-confirm:hover{background:#c62828}
</style>
</head>
<body>
<!-- Modal de confirmação -->
<div class="modal-overlay" id="dc-modal">
  <div class="modal-box">
    <div class="modal-icon">
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="#e53935" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16" stroke-linecap="round"/></svg>
    </div>
    <div class="modal-title">Desconectar número?</div>
    <div class="modal-sub" id="dc-modal-msg">O dispositivo será removido do WhatsApp.</div>
    <div class="modal-actions">
      <button class="modal-cancel" onclick="fecharModal()">Cancelar</button>
      <button class="modal-confirm" id="dc-modal-ok">Desconectar</button>
    </div>
  </div>
</div>

<div class="card">
  <div class="logo">
    <svg viewBox="0 0 24 24"><path d="M17.472 14.382c-.297-.149-1.758-.867-2.03-.967-.273-.099-.471-.148-.67.15-.197.297-.767.966-.94 1.164-.173.199-.347.223-.644.075-.297-.15-1.255-.463-2.39-1.475-.883-.788-1.48-1.761-1.653-2.059-.173-.297-.018-.458.13-.606.134-.133.298-.347.446-.52.149-.174.198-.298.298-.497.099-.198.05-.371-.025-.52-.075-.149-.669-1.612-.916-2.207-.242-.579-.487-.5-.669-.51-.173-.008-.371-.01-.57-.01-.198 0-.52.074-.792.372-.272.297-1.04 1.016-1.04 2.479 0 1.462 1.065 2.875 1.213 3.074.149.198 2.096 3.2 5.077 4.487.709.306 1.262.489 1.694.625.712.227 1.36.195 1.871.118.571-.085 1.758-.719 2.006-1.413.248-.694.248-1.289.173-1.413-.074-.124-.272-.198-.57-.347m-5.421 7.403h-.004a9.87 9.87 0 01-5.031-1.378l-.361-.214-3.741.982.998-3.648-.235-.374a9.86 9.86 0 01-1.51-5.26c.001-5.45 4.436-9.884 9.888-9.884 2.64 0 5.122 1.03 6.988 2.898a9.825 9.825 0 012.893 6.994c-.003 5.45-4.437 9.884-9.885 9.884m8.413-18.297A11.815 11.815 0 0012.05 0C5.495 0 .16 5.335.157 11.892c0 2.096.547 4.142 1.588 5.945L.057 24l6.305-1.654a11.882 11.882 0 005.683 1.448h.005c6.554 0 11.89-5.335 11.893-11.893a11.821 11.821 0 00-3.48-8.413z"/></svg>
  </div>
  <h1>WhatsApp Connector</h1>
  <p class="subtitle">Conecte seu número ao Bitrix24</p>

  <!-- Toggle entre QR Code e API Oficial -->
  <div class="conn-tabs" style="display:flex;gap:6px;background:#f3f4f6;border-radius:10px;padding:4px;margin-bottom:20px">
    <button class="conn-tab active" id="tab-qr" onclick="setMode('qr')" style="flex:1;padding:9px;border:none;background:#fff;border-radius:8px;font-size:13px;font-weight:600;cursor:pointer;color:#111;box-shadow:0 1px 3px rgba(0,0,0,.06)">📱 QR Code</button>
    <button class="conn-tab" id="tab-cloud" onclick="setMode('cloud')" style="flex:1;padding:9px;border:none;background:transparent;border-radius:8px;font-size:13px;font-weight:600;cursor:pointer;color:#666">☁️ API Oficial</button>
  </div>

  <!-- ─── Modo QR Code ─── -->
  <div id="mode-qr">
    <div class="form-row">
      <input type="text" id="phone" placeholder="5519910001772" maxlength="20"/>
      <button class="btn" id="btn-connect" onclick="startSession()">Conectar</button>
    </div>

    <div id="qr-section">
      <div class="instructions">
        <b>Como escanear:</b><br>
        1. Abra o WhatsApp no celular<br>
        2. Toque em <b>⋮ → Aparelhos conectados</b><br>
        3. Toque em <b>Conectar um aparelho</b><br>
        4. Aponte a câmera para o QR abaixo
      </div>
      <div id="badge" class="badge badge-wait">⏳ Aguardando QR...</div><br>
      <div id="qr-wrap"><img id="qr-img" src="" width="256" height="256" style="display:none"/></div>
      <div class="timer" id="timer"></div>
    </div>
  </div>

  <!-- ─── Modo Cloud API (Meta Oficial) ─── -->
  <div id="mode-cloud" style="display:none;text-align:left">
    <div class="instructions" style="text-align:left">
      <b>WhatsApp Business API (Meta Oficial):</b><br>
      Crie um app no <a href="https://developers.facebook.com" target="_blank">Meta for Developers</a>,
      adicione o produto <b>WhatsApp</b> e pegue:
      <ul style="margin:6px 0 0 18px;padding:0">
        <li>Phone Number ID</li>
        <li>WABA ID (opcional)</li>
        <li>Access Token (permanente recomendado)</li>
        <li>App Secret (Configurações → Básico)</li>
      </ul>
    </div>
    <label style="display:block;font-size:12px;color:#555;margin:12px 0 4px;font-weight:600">Telefone (E.164, sem +)</label>
    <input type="text" id="c-display" placeholder="5519910001772" style="margin-bottom:0">
    <label style="display:block;font-size:12px;color:#555;margin:12px 0 4px;font-weight:600">Phone Number ID *</label>
    <input type="text" id="c-pnid" placeholder="123456789012345" style="margin-bottom:0">
    <label style="display:block;font-size:12px;color:#555;margin:12px 0 4px;font-weight:600">WABA ID</label>
    <input type="text" id="c-waba" placeholder="(opcional)" style="margin-bottom:0">
    <label style="display:block;font-size:12px;color:#555;margin:12px 0 4px;font-weight:600">Access Token *</label>
    <input type="text" id="c-token" placeholder="EAAxxxxxxxxxxxx..." style="margin-bottom:0">
    <label style="display:block;font-size:12px;color:#555;margin:12px 0 4px;font-weight:600">App Secret</label>
    <input type="text" id="c-secret" placeholder="(necessário para validar webhooks)" style="margin-bottom:0">
    <button class="btn" id="btn-cloud" onclick="startCloud()" style="width:100%;margin-top:16px">Conectar via API Oficial</button>
    <div id="cloud-result" style="display:none;margin-top:18px;background:#f0fdf4;border:1px solid #86efac;border-radius:8px;padding:14px;font-size:12px">
      <b style="color:#15803d">✓ Conta cadastrada!</b><br>
      Configure no Meta (WhatsApp → Webhooks → Editar URL de retorno):
      <div style="margin-top:8px"><b>Callback URL:</b></div>
      <div id="cl-url" style="background:#fff;padding:6px 8px;border-radius:6px;border:1px solid #d1d5db;word-break:break-all;font-family:monospace;margin-top:3px;cursor:pointer" onclick="copyText(this)"></div>
      <div style="margin-top:8px"><b>Verify Token:</b></div>
      <div id="cl-token" style="background:#fff;padding:6px 8px;border-radius:6px;border:1px solid #d1d5db;word-break:break-all;font-family:monospace;margin-top:3px;cursor:pointer" onclick="copyText(this)"></div>
      <div style="margin-top:10px;color:#475569">Clique para copiar. Depois assine os campos <b>messages</b> e <b>message_status</b>.</div>
    </div>
  </div>

  <hr class="divider"/>

  <div class="sessions-title">📱 Dispositivos conectados</div>
  <div id="sessions-wrap">
    <div class="no-sessions" id="no-sessions">Nenhum dispositivo conectado</div>
  </div>
</div>

<script>
var pollQRInterval = null;
var pollSessionsInterval = null;
var timerInterval = null;
var countdown = 0;
var lastQR = '';
var currentPhone = '';

function startSession() {
  var raw = document.getElementById('phone').value.trim().replace(/\D/g,'');
  if (!raw || raw.length < 10) { alert('Digite um número válido'); return; }
  currentPhone = raw;
  var btn = document.getElementById('btn-connect');
  btn.disabled = true; btn.textContent = 'Conectando...';

  fetch('/ui/sessions', {
    method:'POST',
    headers:{'Content-Type':'application/json'},
    body: JSON.stringify({phone: raw})
  })
  .then(function(r){ return r.json(); })
  .then(function(d){
    if (d.error) { alert(d.error); resetBtn(); return; }
    document.getElementById('qr-section').style.display = 'block';
    startQRPoll(raw);
  })
  .catch(function(e){ alert('Erro: '+e); resetBtn(); });
}

function resetBtn() {
  var btn = document.getElementById('btn-connect');
  btn.disabled = false; btn.textContent = 'Conectar';
}

function startQRPoll(phone) {
  if (pollQRInterval) clearInterval(pollQRInterval);
  doQRPoll(phone);
  pollQRInterval = setInterval(function(){ doQRPoll(phone); }, 2000);
}

function doQRPoll(phone) {
  fetch('/ui/sessions/'+phone+'/qr')
  .then(function(r){ return r.json(); })
  .then(function(d){
    if (d.status === 'connected') {
      stopQRPoll();
      showConnectedState();
      loadSessions();
    } else if (d.status === 'ready' && d.qr && d.qr !== lastQR) {
      lastQR = d.qr;
      renderQR(d.qr);
    } else if (d.status === 'waiting') {
      setBadge('wait','⏳ Aguardando QR...');
    }
  }).catch(function(){});
}

function renderQR(text) {
  setBadge('ready','📷 Escaneie o QR code');
  var img = document.getElementById('qr-img');
  img.style.display = 'block';
  img.src = 'https://api.qrserver.com/v1/create-qr-code/?size=256x256&ecc=L&data=' + encodeURIComponent(text);
  countdown = 25;
  if (timerInterval) clearInterval(timerInterval);
  timerInterval = setInterval(function(){
    countdown--;
    document.getElementById('timer').textContent = 'QR expira em '+countdown+'s';
    if (countdown <= 0) { clearInterval(timerInterval); document.getElementById('timer').textContent = 'Atualizando...'; }
  }, 1000);
}

function showConnectedState() {
  setBadge('ok','✅ Conectado!');
  document.getElementById('qr-wrap').innerHTML = '<div style="font-size:52px;padding:16px">✅</div>';
  document.getElementById('timer').textContent = '';
  resetBtn();
}

function stopQRPoll() {
  if (pollQRInterval) { clearInterval(pollQRInterval); pollQRInterval = null; }
  if (timerInterval) { clearInterval(timerInterval); timerInterval = null; }
}

function setBadge(type, text) {
  var el = document.getElementById('badge');
  el.className = 'badge badge-'+type;
  el.textContent = text;
}

function loadSessions() {
  fetch('/ui/sessions')
  .then(function(r){ return r.json(); })
  .then(function(d){
    var wrap = document.getElementById('sessions-wrap');
    if (d.count === 0) {
      wrap.innerHTML = '<div class="no-sessions" id="no-sessions">Nenhum dispositivo conectado</div>';
      return;
    }
    var details = d.details || [];
    var html = '';
    if (details.length) {
      details.forEach(function(s){
        var badge = s.type === 'cloud_api'
          ? '<span style="font-size:10px;background:#dbeafe;color:#1e40af;padding:2px 7px;border-radius:10px;font-weight:700;margin-left:6px">OFICIAL</span>'
          : '<span style="font-size:10px;background:#dcfce7;color:#166534;padding:2px 7px;border-radius:10px;font-weight:700;margin-left:6px">QR</span>';
        var label = s.label || s.jid;
        html += '<div class="session-item">'
          + '<div class="session-info"><div class="dot"></div><span class="jid">'+label+badge+'</span></div>'
          + '<button class="btn-red" onclick="doDisconnect(\''+encodeURIComponent(s.jid)+'\')">Desconectar</button>'
          + '</div>';
      });
    } else {
      d.sessions.forEach(function(jid){
        html += '<div class="session-item">'
          + '<div class="session-info"><div class="dot"></div><span class="jid">'+jid+'</span></div>'
          + '<button class="btn-red" onclick="doDisconnect(\''+encodeURIComponent(jid)+'\')">Desconectar</button>'
          + '</div>';
      });
    }
    wrap.innerHTML = html;
  }).catch(function(){});
}

// ─── Toggle entre QR e Cloud API ───
function setMode(m) {
  var qr = document.getElementById('mode-qr');
  var cl = document.getElementById('mode-cloud');
  var tQR = document.getElementById('tab-qr');
  var tCL = document.getElementById('tab-cloud');
  if (m === 'cloud') {
    qr.style.display = 'none';
    cl.style.display = 'block';
    tQR.style.background = 'transparent'; tQR.style.color = '#666'; tQR.style.boxShadow = 'none';
    tCL.style.background = '#fff'; tCL.style.color = '#111'; tCL.style.boxShadow = '0 1px 3px rgba(0,0,0,.06)';
  } else {
    qr.style.display = 'block';
    cl.style.display = 'none';
    tCL.style.background = 'transparent'; tCL.style.color = '#666'; tCL.style.boxShadow = 'none';
    tQR.style.background = '#fff'; tQR.style.color = '#111'; tQR.style.boxShadow = '0 1px 3px rgba(0,0,0,.06)';
  }
}

function startCloud() {
  var pnid = document.getElementById('c-pnid').value.trim();
  var waba = document.getElementById('c-waba').value.trim();
  var token = document.getElementById('c-token').value.trim();
  var secret = document.getElementById('c-secret').value.trim();
  var disp = document.getElementById('c-display').value.replace(/\D/g,'');
  if (!pnid || !token) { alert('Phone Number ID e Access Token são obrigatórios'); return; }
  var btn = document.getElementById('btn-cloud');
  btn.disabled = true; btn.textContent = 'Validando credenciais...';
  fetch('/ui/sessions/cloud', {
    method:'POST',
    headers:{'Content-Type':'application/json'},
    body: JSON.stringify({
      phone_number_id: pnid,
      waba_id: waba,
      access_token: token,
      app_secret: secret,
      display_phone: disp
    })
  })
  .then(function(r){ return r.json(); })
  .then(function(d){
    btn.disabled = false; btn.textContent = 'Conectar via API Oficial';
    if (d.error) { alert('Erro: ' + d.error); return; }
    document.getElementById('cl-url').textContent = d.webhook_url;
    document.getElementById('cl-token').textContent = d.verify_token;
    document.getElementById('cloud-result').style.display = 'block';
    loadSessions();
  })
  .catch(function(e){
    btn.disabled = false; btn.textContent = 'Conectar via API Oficial';
    alert('Falha: ' + e);
  });
}

function copyText(el) {
  var t = el.textContent;
  navigator.clipboard.writeText(t).then(function(){
    var orig = el.style.background;
    el.style.background = '#dcfce7';
    setTimeout(function(){ el.style.background = orig; }, 600);
  }).catch(function(){
    var ta = document.createElement('textarea');
    ta.value = t; document.body.appendChild(ta); ta.select();
    try { document.execCommand('copy'); } catch(e){}
    document.body.removeChild(ta);
  });
}

var dcCallback = null;
function doDisconnect(jidEnc) {
  var jid = decodeURIComponent(jidEnc);
  var tel = '+' + jid.split(':')[0].split('@')[0];
  document.getElementById('dc-modal-msg').textContent = 'Desconectar ' + tel + '? O dispositivo será removido do WhatsApp.';
  document.getElementById('dc-modal').classList.add('open');
  dcCallback = function() {
    fetch('/ui/sessions/remove?jid='+jidEnc, {method:'DELETE'})
    .then(function(r){ return r.json(); })
    .then(function(){
      loadSessions();
      stopQRPoll();
      document.getElementById('qr-section').style.display = 'none';
      lastQR = '';
      resetBtn();
    })
    .catch(function(e){ alert('Erro: '+e); });
  };
}
function fecharModal() {
  document.getElementById('dc-modal').classList.remove('open');
  dcCallback = null;
}
document.getElementById('dc-modal-ok').addEventListener('click', function() {
  var cb = dcCallback;
  fecharModal();
  if (cb) cb();
});
document.getElementById('dc-modal').addEventListener('click', function(e) {
  if (e.target === this) fecharModal();
});

// Polling de sessões a cada 5s para manter lista atualizada
pollSessionsInterval = setInterval(loadSessions, 5000);
window.onload = loadSessions;
</script>
</body>
</html>`
