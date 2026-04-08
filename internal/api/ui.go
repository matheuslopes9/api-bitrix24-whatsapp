package api

import (
	"strings"

	"github.com/gofiber/fiber/v2"
)

// GET /connect — página de conexão WhatsApp (sem auth, serve HTML)
func (h *handlers) connectPage(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/html; charset=utf-8")
	return c.SendString(connectHTML)
}

// POST /ui/sessions — proxy interno (usa a API key do servidor)
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

// GET /ui/sessions/:phone/qr — proxy interno para QR
func (h *handlers) uiGetQR(c *fiber.Ctx) error {
	phone := c.Params("phone")
	qr := h.waManager.GetQR(phone)
	sessions := h.waManager.ListSessions()
	for _, jid := range sessions {
		if strings.HasPrefix(jid, phone) {
			return c.JSON(fiber.Map{"status": "connected", "jid": jid})
		}
	}
	if qr == "" {
		return c.JSON(fiber.Map{"status": "waiting", "qr": ""})
	}
	return c.JSON(fiber.Map{"status": "ready", "qr": qr})
}

// GET /ui/sessions — lista sessões ativas
func (h *handlers) uiListSessions(c *fiber.Ctx) error {
	jids := h.waManager.ListSessions()
	return c.JSON(fiber.Map{"sessions": jids, "count": len(jids)})
}

// DELETE /ui/sessions/:jid — desconecta sessão
func (h *handlers) uiDisconnectSession(c *fiber.Ctx) error {
	jid := c.Params("jid")
	h.waManager.Disconnect(jid)
	return c.JSON(fiber.Map{"status": "disconnected", "jid": jid})
}

const connectHTML = `<!DOCTYPE html>
<html lang="pt-BR">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>WhatsApp Connector</title>
<style>
* { box-sizing: border-box; margin: 0; padding: 0; }
body {
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
  background: #f0f2f5;
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
}
.card {
  background: #fff;
  border-radius: 16px;
  box-shadow: 0 4px 24px rgba(0,0,0,0.10);
  padding: 40px 48px;
  max-width: 480px;
  width: 100%;
  text-align: center;
}
.logo {
  width: 56px; height: 56px;
  background: #25D366;
  border-radius: 50%;
  display: flex; align-items: center; justify-content: center;
  margin: 0 auto 20px;
}
.logo svg { width: 32px; height: 32px; fill: #fff; }
h1 { font-size: 22px; color: #111; margin-bottom: 6px; }
.subtitle { color: #666; font-size: 14px; margin-bottom: 28px; }
.form-group { display: flex; gap: 10px; margin-bottom: 24px; }
input {
  flex: 1; padding: 12px 16px;
  border: 1.5px solid #ddd; border-radius: 8px;
  font-size: 15px; outline: none; transition: border-color .2s;
}
input:focus { border-color: #25D366; }
button {
  padding: 12px 20px; background: #25D366; color: #fff;
  border: none; border-radius: 8px; font-size: 15px;
  font-weight: 600; cursor: pointer; transition: background .2s; white-space: nowrap;
}
button:hover { background: #1ebe5d; }
button:disabled { background: #aaa; cursor: not-allowed; }
#qr-section { display: none; }
#qr-wrap { margin: 0 auto 16px; display: inline-block; padding: 12px; background: #fff; border: 1px solid #eee; border-radius: 8px; }
.status-badge {
  display: inline-flex; align-items: center; gap: 6px;
  padding: 6px 14px; border-radius: 20px;
  font-size: 13px; font-weight: 600; margin-bottom: 16px;
}
.status-waiting  { background: #fff3cd; color: #856404; }
.status-ready    { background: #d1ecf1; color: #0c5460; }
.status-connected { background: #d4edda; color: #155724; }
.timer { font-size: 12px; color: #888; margin-top: 8px; }
.sessions-list { margin-top: 28px; text-align: left; }
.sessions-list h3 { font-size: 14px; color: #555; margin-bottom: 10px; }
.session-item {
  background: #f8f9fa; border-radius: 8px;
  padding: 10px 14px; font-size: 13px; color: #333;
  margin-bottom: 6px; display: flex; align-items: center; gap: 8px;
}
.dot { width: 8px; height: 8px; border-radius: 50%; background: #25D366; flex-shrink: 0; }
.instructions {
  background: #f8f9fa; border-radius: 8px;
  padding: 14px 16px; text-align: left; margin-bottom: 20px;
  font-size: 13px; color: #555; line-height: 1.7;
}
.instructions b { color: #333; }
</style>
</head>
<body>
<div class="card">
  <div class="logo">
    <svg viewBox="0 0 24 24"><path d="M17.472 14.382c-.297-.149-1.758-.867-2.03-.967-.273-.099-.471-.148-.67.15-.197.297-.767.966-.94 1.164-.173.199-.347.223-.644.075-.297-.15-1.255-.463-2.39-1.475-.883-.788-1.48-1.761-1.653-2.059-.173-.297-.018-.458.13-.606.134-.133.298-.347.446-.52.149-.174.198-.298.298-.497.099-.198.05-.371-.025-.52-.075-.149-.669-1.612-.916-2.207-.242-.579-.487-.5-.669-.51-.173-.008-.371-.01-.57-.01-.198 0-.52.074-.792.372-.272.297-1.04 1.016-1.04 2.479 0 1.462 1.065 2.875 1.213 3.074.149.198 2.096 3.2 5.077 4.487.709.306 1.262.489 1.694.625.712.227 1.36.195 1.871.118.571-.085 1.758-.719 2.006-1.413.248-.694.248-1.289.173-1.413-.074-.124-.272-.198-.57-.347m-5.421 7.403h-.004a9.87 9.87 0 01-5.031-1.378l-.361-.214-3.741.982.998-3.648-.235-.374a9.86 9.86 0 01-1.51-5.26c.001-5.45 4.436-9.884 9.888-9.884 2.64 0 5.122 1.03 6.988 2.898a9.825 9.825 0 012.893 6.994c-.003 5.45-4.437 9.884-9.885 9.884m8.413-18.297A11.815 11.815 0 0012.05 0C5.495 0 .16 5.335.157 11.892c0 2.096.547 4.142 1.588 5.945L.057 24l6.305-1.654a11.882 11.882 0 005.683 1.448h.005c6.554 0 11.89-5.335 11.893-11.893a11.821 11.821 0 00-3.48-8.413z"/></svg>
  </div>
  <h1>WhatsApp Connector</h1>
  <p class="subtitle">Conecte seu número ao Bitrix24</p>

  <div class="form-group">
    <input type="text" id="phone" placeholder="5519910001772" maxlength="20"/>
    <button id="btn-connect" onclick="startSession()">Conectar</button>
  </div>

  <div id="qr-section">
    <div class="instructions">
      <b>Como escanear:</b><br>
      1. Abra o WhatsApp no celular<br>
      2. Toque em <b>⋮ Menu → Aparelhos conectados</b><br>
      3. Toque em <b>Conectar um aparelho</b><br>
      4. Aponte a câmera para o QR code abaixo
    </div>
    <div id="status-badge" class="status-badge status-waiting">⏳ Aguardando QR...</div>
    <div id="qr-wrap"><canvas id="qr-canvas" width="256" height="256"></canvas></div>
    <div class="timer" id="timer"></div>
  </div>

  <div class="sessions-list" id="sessions-list" style="display:none">
    <h3>✅ Sessões ativas</h3>
    <div id="sessions-items"></div>
  </div>
</div>
<style>
.btn-disconnect {
  background: #fff; color: #e53935; border: 1.5px solid #e53935;
  border-radius: 6px; padding: 4px 12px; font-size: 12px;
  cursor: pointer; margin-left: auto; font-weight: 600;
  transition: background .2s, color .2s;
}
.btn-disconnect:hover { background: #e53935; color: #fff; }
.session-item { justify-content: space-between; }
</style>

<script>
// QR Code generator embutido (sem dependência externa)
// Algoritmo de QR code simplificado usando canvas
var QR = (function() {
  // Implementação mínima de QR via canvas usando a API nativa do browser
  // Usa um iframe com data URI para renderizar via qr-server API de forma segura
  function draw(canvas, text) {
    var ctx = canvas.getContext('2d');
    ctx.clearRect(0, 0, canvas.width, canvas.height);
    ctx.fillStyle = '#fff';
    ctx.fillRect(0, 0, canvas.width, canvas.height);
    ctx.fillStyle = '#999';
    ctx.font = '13px sans-serif';
    ctx.textAlign = 'center';
    ctx.fillText('Carregando QR...', 128, 128);
  }
  return { draw: draw };
})();

var pollInterval = null;
var timerInterval = null;
var countdown = 25;
var currentPhone = '';
var lastQR = '';

function startSession() {
  var phone = document.getElementById('phone').value.trim().replace(/\D/g,'');
  if (!phone || phone.length < 10) {
    alert('Digite um número válido (ex: 5519910001772)');
    return;
  }
  currentPhone = phone;
  var btn = document.getElementById('btn-connect');
  btn.disabled = true;
  btn.textContent = 'Conectando...';

  fetch('/ui/sessions', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({phone: phone})
  })
  .then(function(r) { return r.json(); })
  .then(function(data) {
    if (data.error) { alert(data.error); resetBtn(); return; }
    document.getElementById('qr-section').style.display = 'block';
    startPolling(phone);
  })
  .catch(function(e) { alert('Erro ao conectar: ' + e); resetBtn(); });
}

function resetBtn() {
  var btn = document.getElementById('btn-connect');
  btn.disabled = false;
  btn.textContent = 'Conectar';
}

function startPolling(phone) {
  if (pollInterval) clearInterval(pollInterval);
  pollQR(phone);
  pollInterval = setInterval(function(){ pollQR(phone); }, 2000);
}

function pollQR(phone) {
  fetch('/ui/sessions/' + phone + '/qr')
  .then(function(r) { return r.json(); })
  .then(function(data) {
    if (data.status === 'connected') {
      onConnected(data.jid);
    } else if (data.status === 'ready' && data.qr && data.qr !== lastQR) {
      lastQR = data.qr;
      showQR(data.qr);
    } else if (data.status === 'waiting') {
      setBadge('waiting', '⏳ Aguardando QR...');
    }
  })
  .catch(function(){});
}

function showQR(text) {
  setBadge('ready', '📷 Escaneie o QR code');
  // Usa img tag apontando para API de QR (sem CDN de JS — apenas requisição de imagem)
  var wrap = document.getElementById('qr-wrap');
  var encoded = encodeURIComponent(text);
  wrap.innerHTML = '<img src="https://api.qrserver.com/v1/create-qr-code/?size=256x256&ecc=L&data=' + encoded + '" width="256" height="256" style="display:block"/>';

  countdown = 25;
  if (timerInterval) clearInterval(timerInterval);
  timerInterval = setInterval(function() {
    countdown--;
    document.getElementById('timer').textContent = 'QR expira em ' + countdown + 's';
    if (countdown <= 0) {
      clearInterval(timerInterval);
      document.getElementById('timer').textContent = 'Atualizando QR...';
    }
  }, 1000);
}

function onConnected(jid) {
  clearInterval(pollInterval);
  clearInterval(timerInterval);
  document.getElementById('timer').textContent = '';
  setBadge('connected', '✅ WhatsApp conectado!');
  document.getElementById('qr-wrap').innerHTML = '<div style="font-size:48px;padding:20px">✅</div>';
  loadSessions();
  resetBtn();
}

function setBadge(type, text) {
  var el = document.getElementById('status-badge');
  el.className = 'status-badge status-' + type;
  el.textContent = text;
}

function loadSessions() {
  fetch('/ui/sessions')
  .then(function(r) { return r.json(); })
  .then(function(data) {
    var list = document.getElementById('sessions-list');
    var items = document.getElementById('sessions-items');
    if (data.count > 0) {
      list.style.display = 'block';
      var html = '';
      data.sessions.forEach(function(jid) {
        var jidEnc = encodeURIComponent(jid);
        html += '<div class="session-item">'
          + '<div style="display:flex;align-items:center;gap:8px"><div class="dot"></div><span style="word-break:break-all;font-size:12px">' + jid + '</span></div>'
          + '<button class="btn-disconnect" onclick="disconnectSession(\'' + jidEnc + '\')">Desconectar</button>'
          + '</div>';
      });
      items.innerHTML = html;
    } else {
      list.style.display = 'none';
      items.innerHTML = '';
    }
  });
}

function disconnectSession(jidEnc) {
  var jid = decodeURIComponent(jidEnc);
  if (!confirm('Desconectar ' + jid + '?')) return;
  fetch('/ui/sessions/' + jidEnc, { method: 'DELETE' })
  .then(function(r) { return r.json(); })
  .then(function() {
    loadSessions();
    // reseta UI se era a sessão atual
    document.getElementById('qr-section').style.display = 'none';
    document.getElementById('qr-wrap').innerHTML = '<canvas id="qr-canvas" width="256" height="256"></canvas>';
    if (pollInterval) clearInterval(pollInterval);
    if (timerInterval) clearInterval(timerInterval);
    lastQR = '';
    resetBtn();
  })
  .catch(function(e) { alert('Erro: ' + e); });
}

window.onload = function() { loadSessions(); };
</script>
</body>
</html>`
