package api

// Endpoints de simulação interna para testar os 3 fluxos sem WhatsApp/Bitrix reais.
// Acesso: GET /sim

import (
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"go.uber.org/zap"
)

// GET /sim — página HTML de simulação
func (h *handlers) simPage(c *fiber.Ctx) error {
	return c.Type("html").SendString(simHTML)
}

// POST /sim/inbound — simula mensagem chegando do cliente WhatsApp
// Body: { "from_phone": "5519987717792", "session_phone": "5519910001772", "text": "Olá!" }
func (h *handlers) simInbound(c *fiber.Ctx) error {
	var body struct {
		FromPhone   string `json:"from_phone"`
		SessionPhone string `json:"session_phone"`
		Text        string `json:"text"`
		MsgType     string `json:"msg_type"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"ok": false, "error": err.Error()})
	}
	if body.FromPhone == "" || body.SessionPhone == "" || body.Text == "" {
		return c.Status(400).JSON(fiber.Map{"ok": false, "error": "from_phone, session_phone e text são obrigatórios"})
	}

	msgType := db.MsgTypeText
	if body.MsgType != "" {
		msgType = db.MessageType(body.MsgType)
	}

	fromJID := body.FromPhone + "@s.whatsapp.net"
	toJID := body.SessionPhone + "@s.whatsapp.net"
	waID := "SIM-IN-" + uuid.New().String()[:8]
	now := time.Now()

	msg := &db.Message{
		ID:          uuid.New(),
		WAMessageID: waID,
		FromJID:     fromJID,
		ToJID:       toJID,
		AuthorName:  "Cliente Simulado",
		Direction:   db.DirInbound,
		MessageType: msgType,
		Content:     body.Text,
		Status:      db.MsgReceived,
		SentAt:      &now,
	}

	if err := h.repo.InsertMessage(c.Context(), msg); err != nil {
		h.log.Error("sim inbound: InsertMessage failed", zap.Error(err))
		return c.Status(500).JSON(fiber.Map{"ok": false, "error": err.Error()})
	}

	h.log.Info("sim: inbound message inserted",
		zap.String("from_jid", fromJID),
		zap.String("to_jid", toJID),
		zap.String("wa_id", waID),
	)

	return c.JSON(fiber.Map{
		"ok":       true,
		"action":   "inbound_saved",
		"wa_id":    waID,
		"from_jid": fromJID,
		"to_jid":   toJID,
		"text":     body.Text,
	})
}

// POST /sim/outbound — simula operador enviando mensagem pelo CRM
// Body: { "session_phone": "5519910001772", "to_phone": "5519987717792", "text": "Olá!", "operator": "Ana" }
func (h *handlers) simOutbound(c *fiber.Ctx) error {
	var body struct {
		SessionPhone string `json:"session_phone"`
		ToPhone      string `json:"to_phone"`
		Text         string `json:"text"`
		Operator     string `json:"operator"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"ok": false, "error": err.Error()})
	}
	if body.SessionPhone == "" || body.ToPhone == "" || body.Text == "" {
		return c.Status(400).JSON(fiber.Map{"ok": false, "error": "session_phone, to_phone e text são obrigatórios"})
	}

	operator := body.Operator
	if operator == "" {
		operator = "Operador Simulado"
	}

	fromJID := body.SessionPhone + "@s.whatsapp.net"
	toJID := body.ToPhone + "@s.whatsapp.net"
	waID := "SIM-OUT-" + uuid.New().String()[:8]
	now := time.Now()

	msg := &db.Message{
		ID:          uuid.New(),
		WAMessageID: waID,
		FromJID:     fromJID,
		ToJID:       toJID,
		AuthorName:  operator,
		Direction:   db.DirOutbound,
		MessageType: db.MsgTypeText,
		Content:     body.Text,
		Status:      db.MsgDelivered,
		SentAt:      &now,
	}

	if err := h.repo.InsertMessage(c.Context(), msg); err != nil {
		h.log.Error("sim outbound: InsertMessage failed", zap.Error(err))
		return c.Status(500).JSON(fiber.Map{"ok": false, "error": err.Error()})
	}

	h.log.Info("sim: outbound message inserted",
		zap.String("from_jid", fromJID),
		zap.String("to_jid", toJID),
		zap.String("operator", operator),
		zap.String("wa_id", waID),
	)

	return c.JSON(fiber.Map{
		"ok":       true,
		"action":   "outbound_saved",
		"wa_id":    waID,
		"from_jid": fromJID,
		"to_jid":   toJID,
		"operator": operator,
		"text":     body.Text,
	})
}

// GET /sim/history?phone=5519987717792 — lê o histórico do banco local para o número
func (h *handlers) simHistory(c *fiber.Ctx) error {
	phone := normalizeWAPhone(c.Query("phone"))
	if phone == "" {
		return c.Status(400).JSON(fiber.Map{"ok": false, "error": "phone obrigatório"})
	}

	msgs, err := h.repo.GetMessagesByPhone(c.Context(), phone, 50)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"ok": false, "error": err.Error(), "phone": phone})
	}

	type row struct {
		WAID      string `json:"wa_id"`
		Direction string `json:"direction"`
		FromJID   string `json:"from_jid"`
		ToJID     string `json:"to_jid"`
		Author    string `json:"author"`
		Text      string `json:"text"`
		CreatedAt string `json:"created_at"`
	}
	rows := make([]row, len(msgs))
	for i, m := range msgs {
		rows[i] = row{
			WAID:      m.WAMessageID,
			Direction: string(m.Direction),
			FromJID:   m.FromJID,
			ToJID:     m.ToJID,
			Author:    m.AuthorName,
			Text:      m.Content,
			CreatedAt: m.CreatedAt.Format("2006-01-02 15:04:05"),
		}
	}

	return c.JSON(fiber.Map{
		"ok":      true,
		"phone":   phone,
		"pattern": fmt.Sprintf("%s@%%", phone),
		"count":   len(rows),
		"messages": rows,
	})
}

// POST /sim/clear?phone=5519987717792 — remove mensagens simuladas do número (limpeza)
func (h *handlers) simClear(c *fiber.Ctx) error {
	phone := normalizeWAPhone(c.Query("phone"))
	if phone == "" {
		return c.Status(400).JSON(fiber.Map{"ok": false, "error": "phone obrigatório"})
	}
	pattern := phone + "@%"
	tag, err := h.repo.DeleteMessagesByJIDPattern(c.Context(), pattern)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"ok": false, "error": err.Error()})
	}
	return c.JSON(fiber.Map{"ok": true, "deleted": tag, "phone": phone})
}

var simHTML = `<!DOCTYPE html>
<html lang="pt-BR">
<head>
<meta charset="UTF-8">
<title>UC Talk — Simulador</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:monospace;background:#0f172a;color:#e2e8f0;padding:24px;font-size:13px}
h1{color:#25D366;margin-bottom:4px;font-size:18px}
.sub{color:#64748b;margin-bottom:24px;font-size:12px}
.grid{display:grid;grid-template-columns:1fr 1fr;gap:16px;margin-bottom:16px}
.card{background:#1e2736;border:1px solid #2d3a4e;border-radius:10px;padding:16px}
.card h2{font-size:13px;font-weight:700;color:#94a3b8;text-transform:uppercase;letter-spacing:.05em;margin-bottom:12px}
label{display:block;font-size:11px;color:#64748b;margin-bottom:3px;margin-top:8px}
input,textarea{width:100%;background:#252f3e;border:1px solid #334155;border-radius:6px;padding:7px 10px;color:#f1f5f9;font-size:12px;font-family:monospace;outline:none}
input:focus,textarea:focus{border-color:#25D366}
textarea{resize:vertical;min-height:60px}
.btn{display:inline-block;padding:8px 16px;border-radius:6px;border:none;cursor:pointer;font-size:12px;font-weight:700;font-family:monospace;margin-top:10px}
.btn-green{background:#25D366;color:#fff}.btn-green:hover{opacity:.85}
.btn-blue{background:#3b82f6;color:#fff}.btn-blue:hover{opacity:.85}
.btn-red{background:#ef4444;color:#fff}.btn-red:hover{opacity:.85}
.btn-gray{background:#334155;color:#e2e8f0}.btn-gray:hover{background:#475569}
.result{margin-top:16px;background:#0f172a;border:1px solid #2d3a4e;border-radius:6px;padding:12px;font-size:11px;white-space:pre-wrap;max-height:260px;overflow-y:auto;color:#94a3b8}
.result .ok{color:#4ade80}.result .err{color:#f87171}
.tag{display:inline-block;padding:2px 7px;border-radius:4px;font-size:10px;font-weight:700;margin-right:4px}
.tag-in{background:#1a3a2a;color:#4ade80}.tag-out{background:#1e3a5f;color:#60a5fa}
.msg-row{padding:6px 0;border-bottom:1px solid #1e2736;display:grid;grid-template-columns:60px 80px 1fr;gap:8px;align-items:start}
.msg-from{font-size:10px;color:#64748b;word-break:break-all}
.msg-text{color:#e2e8f0}
.status-bar{background:#1e2736;border:1px solid #2d3a4e;border-radius:8px;padding:10px 14px;margin-bottom:16px;display:flex;gap:16px;align-items:center;flex-wrap:wrap}
.status-item{font-size:11px;color:#64748b}
.status-item strong{color:#e2e8f0}
.full{grid-column:1/-1}
</style>
</head>
<body>
<h1>UC Talk — Simulador de Mensagens</h1>
<p class="sub">Testa os 3 fluxos diretamente no banco sem precisar de WhatsApp ou Bitrix reais</p>

<div class="status-bar" id="status-bar">
  <span class="status-item">Carregando...</span>
</div>

<div class="grid">

  <!-- FLUXO A: Cliente → Servidor (inbound) -->
  <div class="card">
    <h2>🟢 Fluxo A — Cliente envia para o servidor</h2>
    <p style="font-size:11px;color:#64748b;margin-bottom:8px">Simula mensagem chegando do WhatsApp do cliente. Deve aparecer no histórico do CRM.</p>
    <label>Telefone do CLIENTE (from_phone)</label>
    <input id="a-from" value="5519987717792" placeholder="5519987717792">
    <label>Telefone da SESSÃO WA (session_phone)</label>
    <input id="a-session" value="5519910001772" placeholder="5519910001772">
    <label>Texto da mensagem</label>
    <textarea id="a-text">Olá, preciso de ajuda!</textarea>
    <button class="btn btn-green" onclick="sendInbound()">▶ Simular INBOUND</button>
    <div class="result" id="a-result">Aguardando...</div>
  </div>

  <!-- FLUXO B: Operador → CRM → WhatsApp (outbound) -->
  <div class="card">
    <h2>🔵 Fluxo B — Operador envia pelo CRM</h2>
    <p style="font-size:11px;color:#64748b;margin-bottom:8px">Simula operador enviando msg pelo CRM tab. Deve aparecer no histórico e no Open Lines.</p>
    <label>Telefone da SESSÃO WA (session_phone)</label>
    <input id="b-session" value="5519910001772" placeholder="5519910001772">
    <label>Telefone do CLIENTE (to_phone)</label>
    <input id="b-to" value="5519987717792" placeholder="5519987717792">
    <label>Nome do operador</label>
    <input id="b-op" value="Ana Silva" placeholder="Nome do operador">
    <label>Texto da mensagem</label>
    <textarea id="b-text">Olá! Em que posso ajudar?</textarea>
    <button class="btn btn-blue" onclick="sendOutbound()">▶ Simular OUTBOUND</button>
    <div class="result" id="b-result">Aguardando...</div>
  </div>

  <!-- VALIDAÇÃO: Leitura do histórico -->
  <div class="card full">
    <h2>🔍 Validação — Histórico do banco</h2>
    <p style="font-size:11px;color:#64748b;margin-bottom:8px">Verifica se as mensagens simuladas aparecem corretamente na busca que o CRM tab usa.</p>
    <div style="display:flex;gap:8px;align-items:flex-end">
      <div style="flex:1">
        <label>Telefone para buscar</label>
        <input id="h-phone" value="5519987717792" placeholder="5519987717792">
      </div>
      <button class="btn btn-gray" onclick="fetchHistory()" style="margin-top:0">🔍 Buscar histórico</button>
      <button class="btn btn-red" onclick="clearHistory()" style="margin-top:0">🗑 Limpar</button>
    </div>
    <div class="result" id="h-result" style="max-height:400px">Aguardando...</div>
  </div>

</div>

<script>
function post(url, body) {
  return fetch(url, {method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify(body)}).then(r => r.json());
}

function sendInbound() {
  var el = document.getElementById('a-result');
  el.innerHTML = 'Enviando...';
  post('/sim/inbound', {
    from_phone:    document.getElementById('a-from').value.trim(),
    session_phone: document.getElementById('a-session').value.trim(),
    text:          document.getElementById('a-text').value.trim(),
  }).then(function(d) {
    if (d.ok) {
      el.innerHTML = '<span class="ok">✓ SUCESSO</span>\n\n'
        + 'wa_id:    ' + d.wa_id + '\n'
        + 'from_jid: ' + d.from_jid + '\n'
        + 'to_jid:   ' + d.to_jid + '\n'
        + 'text:     ' + d.text + '\n\n'
        + '→ Agora clique em "Buscar histórico" abaixo para validar.';
    } else {
      el.innerHTML = '<span class="err">✗ ERRO: ' + (d.error||JSON.stringify(d)) + '</span>';
    }
  }).catch(function(e){ el.innerHTML = '<span class="err">✗ Falha: ' + e + '</span>'; });
}

function sendOutbound() {
  var el = document.getElementById('b-result');
  el.innerHTML = 'Enviando...';
  post('/sim/outbound', {
    session_phone: document.getElementById('b-session').value.trim(),
    to_phone:      document.getElementById('b-to').value.trim(),
    operator:      document.getElementById('b-op').value.trim(),
    text:          document.getElementById('b-text').value.trim(),
  }).then(function(d) {
    if (d.ok) {
      el.innerHTML = '<span class="ok">✓ SUCESSO</span>\n\n'
        + 'wa_id:    ' + d.wa_id + '\n'
        + 'from_jid: ' + d.from_jid + '\n'
        + 'to_jid:   ' + d.to_jid + '\n'
        + 'operator: ' + d.operator + '\n'
        + 'text:     ' + d.text + '\n\n'
        + '→ Agora clique em "Buscar histórico" abaixo para validar.';
    } else {
      el.innerHTML = '<span class="err">✗ ERRO: ' + (d.error||JSON.stringify(d)) + '</span>';
    }
  }).catch(function(e){ el.innerHTML = '<span class="err">✗ Falha: ' + e + '</span>'; });
}

function fetchHistory() {
  var phone = document.getElementById('h-phone').value.trim();
  var el = document.getElementById('h-result');
  el.innerHTML = 'Buscando...';
  fetch('/sim/history?phone=' + encodeURIComponent(phone))
    .then(r => r.json())
    .then(function(d) {
      if (!d.ok) { el.innerHTML = '<span class="err">✗ ERRO: ' + d.error + '</span>'; return; }
      if (!d.count) {
        el.innerHTML = '<span class="err">✗ NENHUMA mensagem encontrada para ' + d.phone + '\n'
          + 'Pattern usado: ' + d.pattern + '\n\n'
          + 'Isso significa que o CRM tab também mostraria "Nenhuma mensagem ainda".\n'
          + 'Clique em "Simular INBOUND" ou "Simular OUTBOUND" primeiro.</span>';
        return;
      }
      var lines = '<span class="ok">✓ ' + d.count + ' mensagem(ns) encontrada(s) para ' + d.phone + '</span>\n'
        + 'Pattern usado no LIKE: ' + d.pattern + '\n\n';
      d.messages.forEach(function(m, i) {
        var tag = m.direction === 'inbound'
          ? '<span class="tag tag-in">IN </span>'
          : '<span class="tag tag-out">OUT</span>';
        lines += tag + ' [' + m.created_at + '] '
          + (m.author ? m.author + ': ' : '')
          + m.text + '\n'
          + '         from: ' + m.from_jid + '\n'
          + '         to:   ' + m.to_jid + '\n\n';
      });
      el.innerHTML = lines;
    })
    .catch(function(e){ el.innerHTML = '<span class="err">✗ Falha: ' + e + '</span>'; });
}

function clearHistory() {
  var phone = document.getElementById('h-phone').value.trim();
  if (!confirm('Apagar mensagens simuladas do número ' + phone + '?')) return;
  fetch('/sim/clear?phone=' + encodeURIComponent(phone), {method:'POST'})
    .then(r => r.json())
    .then(function(d){
      document.getElementById('h-result').innerHTML = d.ok
        ? '<span class="ok">✓ ' + d.deleted + ' mensagem(ns) removida(s)</span>'
        : '<span class="err">✗ ' + d.error + '</span>';
    });
}

// Status bar — verifica sessões e portais
function loadStatus() {
  Promise.all([
    fetch('/bitrix/crm/sessions').then(r=>r.json()).catch(()=>({sessions:[]})),
  ]).then(function(results) {
    var sessions = results[0].sessions || [];
    document.getElementById('status-bar').innerHTML =
      '<span class="status-item">Sessões WA: <strong>' + (sessions.length ? sessions.join(', ') : 'nenhuma') + '</strong></span>'
      + '<span class="status-item" style="color:#4ade80">● Simulador ativo</span>'
      + '<span class="status-item">Use os formulários acima para testar os fluxos</span>';
  });
}
loadStatus();
</script>
</body>
</html>`
