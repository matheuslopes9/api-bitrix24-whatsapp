package api

// Tela de boas-vindas do primeiro acesso apos o install
// (tenant_licenses.welcome_shown = false).
//
// Fluxo esperado:
//   1. Bitrix carrega iframe /bitrix-app
//   2. /bitrix-app faz handshake POST /bitrix/auth (seta cookie tenant +
//      cria a licenca minima + auto-master)
//   3. /bitrix-app carrega iframe filho com /dashboard
//   4. /dashboard verifica tenant_licenses.welcome_shown — se FALSE,
//      redireciona pra /welcome
//   5. /welcome mostra as boas-vindas e a confirmacao do usuario master
//   6. User clica "Continuar" -> POST /ui/welcome/dismiss -> marca
//      welcome_shown=true -> redireciona pra /dashboard
//
// A pagina /planos saiu junto com o modelo SaaS: nao ha' mais vitrine de
// precos nem auto-servico de assinatura. Quem contrata fala com o comercial.

import (
	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"
)

// GET /welcome — pagina servida no iframe Bitrix quando tenant ainda
// nao viu boas-vindas. Cookie tenant (setado em /bitrix/auth) e
// necessario — sem cookie redireciona pra /bitrix-connect.
func (h *handlers) welcomePage(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/html; charset=utf-8")
	c.Set("Cache-Control", "no-store, no-cache, must-revalidate")
	c.Set("Pragma", "no-cache")
	c.Set("Expires", "0")
	h.maybeSetTenantCookieFromBitrixPost(c)
	return c.SendString(welcomeHTML)
}

// POST /ui/welcome/dismiss — marca tenant_licenses.welcome_shown=TRUE.
// Idempotente. Chamado quando user clica "Continuar pro App" no /welcome.
func (h *handlers) uiWelcomeDismiss(c *fiber.Ctx) error {
	ctx := c.Context()
	domain, err := h.resolveDashboardDomain(ctx, c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	// Garante que tem licenca (clientes muito antigos podem nao ter).
	_ = h.repo.EnsureLicense(ctx, domain)
	if err := h.repo.SetLicenseWelcomeShown(ctx, domain); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.log.Info("welcome: dismissed", zap.String("domain", domain))
	return c.JSON(fiber.Map{"ok": true})
}

const welcomeHTML = `<!DOCTYPE html>
<html lang="pt-BR">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Bem-vindo ao UC Talk</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;
     background:linear-gradient(135deg,#0f172a 0%,#1e293b 100%);color:#e2e8f0;
     min-height:100vh;padding:40px 20px}
.wrap{max-width:900px;margin:0 auto}
.hero{text-align:center;margin-bottom:40px}
.hero h1{font-size:32px;font-weight:800;margin-bottom:10px;background:linear-gradient(90deg,#25D366,#10b981);-webkit-background-clip:text;-webkit-text-fill-color:transparent;background-clip:text}
.hero .sub{font-size:15px;color:#94a3b8;line-height:1.6;max-width:560px;margin:0 auto}
.cards{display:grid;grid-template-columns:repeat(auto-fit,minmax(260px,1fr));gap:18px;margin-bottom:32px}
.card{background:rgba(30,41,59,.6);border:1px solid rgba(255,255,255,.08);border-radius:16px;padding:20px;backdrop-filter:blur(10px)}
.card .label{font-size:11px;color:#64748b;font-weight:700;text-transform:uppercase;letter-spacing:.08em;margin-bottom:10px}
.card .big{font-size:36px;font-weight:800;color:#fbbf24;line-height:1;margin-bottom:6px}
.card .small{font-size:13px;color:#cbd5e1;line-height:1.5}
.master-row{display:flex;align-items:center;gap:12px;margin-top:14px;padding:12px;background:rgba(15,23,42,.5);border-radius:10px}
.master-avatar{width:42px;height:42px;border-radius:50%;background:linear-gradient(135deg,#3b82f6,#8b5cf6);display:flex;align-items:center;justify-content:center;font-weight:700;font-size:16px;color:#fff;flex-shrink:0}
.master-info{flex:1;min-width:0}
.master-info .nm{font-size:14px;font-weight:600;color:#f1f5f9;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.master-info .role{font-size:11px;color:#64748b;margin-top:2px}
.features{background:rgba(30,41,59,.6);border:1px solid rgba(255,255,255,.08);border-radius:16px;padding:24px;margin-bottom:32px}
.features h2{font-size:18px;font-weight:700;margin-bottom:16px;color:#f1f5f9}
.feat-grid{display:grid;grid-template-columns:1fr 1fr;gap:24px}
.feat-col h3{font-size:12px;font-weight:700;text-transform:uppercase;letter-spacing:.08em;margin-bottom:12px}
.feat-col.basic h3{color:#60a5fa}
.feat-col.pro h3{color:#25D366}
.feat-col ul{list-style:none;display:flex;flex-direction:column;gap:8px}
.feat-col li{font-size:13px;color:#cbd5e1;display:flex;align-items:flex-start;gap:8px;line-height:1.5}
.feat-col li::before{content:'';width:6px;height:6px;border-radius:50%;flex-shrink:0;margin-top:7px}
.feat-col.basic li::before{background:#60a5fa}
.feat-col.pro li::before{background:#25D366}
.feat-col li.off{color:#475569;text-decoration:line-through}
.feat-col li.off::before{background:#334155}
.cta{display:flex;gap:14px;justify-content:center;flex-wrap:wrap}
.btn{padding:14px 28px;border-radius:12px;font-size:14px;font-weight:700;border:0;cursor:pointer;display:inline-flex;align-items:center;gap:8px;text-decoration:none;transition:transform .12s,box-shadow .12s}
.btn:hover{transform:translateY(-1px)}
.btn-primary{background:linear-gradient(90deg,#25D366,#10b981);color:#fff;box-shadow:0 4px 14px rgba(37,211,102,.25)}
.btn-primary:hover{box-shadow:0 6px 20px rgba(37,211,102,.35)}
.btn-ghost{background:rgba(255,255,255,.06);color:#e2e8f0;border:1px solid rgba(255,255,255,.1)}
.btn-ghost:hover{background:rgba(255,255,255,.1)}
@media(max-width:640px){.feat-grid{grid-template-columns:1fr}.hero h1{font-size:24px}}
</style>
</head>
<body>
<div class="wrap">
  <div class="hero">
    <h1>Bem-vindo ao UC Talk! 🎉</h1>
    <p class="sub">Você acabou de instalar o conector WhatsApp + Bitrix24 da UC Technology. Vamos conectar seu primeiro número.</p>
  </div>

  <div class="cards">
    <div class="card">
      <div class="label">Números do contrato</div>
      <div class="big" id="max-sessions">1</div>
      <div class="small">número(s) WhatsApp que você pode conectar</div>
    </div>
    <div class="card">
      <div class="label">Usuário Master</div>
      <div class="master-row">
        <div class="master-avatar" id="master-initial">?</div>
        <div class="master-info">
          <div class="nm" id="master-name">Carregando...</div>
          <div class="role">Você gerencia o app neste portal</div>
        </div>
      </div>
    </div>
  </div>

  <div class="features">
    <h2>O que você pode fazer</h2>
    <div class="feat-grid">
      <div class="feat-col basic">
        <h3>✓ Sempre incluso</h3>
        <ul>
          <li>Conectar número WhatsApp (QR Code)</li>
          <li>Aba WhatsApp em Contatos / Leads / Negociações</li>
          <li>Envio e recepção de mensagens inline no CRM</li>
          <li>Linhas Abertas (Contact Center) do Bitrix24</li>
          <li>Permissões por número, por operador</li>
          <li>Histórico de conversas</li>
        </ul>
      </div>
      <div class="feat-col pro">
        <h3>★ Conforme seu contrato</h3>
        <ul id="feats-contrato">
          <li>Vários números WhatsApp</li>
          <li>WhatsApp Cloud API oficial da Meta</li>
          <li>Templates de mensagem (inclusive HSM da Meta)</li>
          <li>Automações BizProc (robôs no editor de processos)</li>
          <li>Relatórios completos</li>
          <li>Suporte UC Technology</li>
        </ul>
      </div>
    </div>
  </div>

  <div class="cta">
    <button class="btn btn-primary" id="btn-continue">Continuar para o App →</button>
  </div>
</div>

<script>
(function(){
  // Licenca: quantos numeros o contrato libera.
  fetch('/ui/license', {credentials:'include'})
    .then(function(r){ return r.json(); })
    .then(function(p){
      if (p && typeof p.max_sessions === 'number') {
        document.getElementById('max-sessions').textContent = p.max_sessions;
      }
    })
    .catch(function(){});

  // Master: /ui/license ja' devolve o domain, entao encadeia a partir dele.
  fetch('/ui/license', {credentials:'include'})
    .then(function(r){ return r.json(); })
    .then(function(p){
      if (!p || !p.domain) return;
      return fetch('/bitrix/crm/master/status?domain=' + encodeURIComponent(p.domain))
        .then(function(r){ return r.json(); });
    })
    .then(function(m){
      if (!m || !m.configured) {
        document.getElementById('master-name').textContent = 'Não configurado — defina no painel';
        return;
      }
      var nm = m.master_user_name || ('Usuário #' + m.master_user_id);
      document.getElementById('master-name').textContent = nm;
      var initial = (nm.trim().charAt(0) || '?').toUpperCase();
      document.getElementById('master-initial').textContent = initial;
    })
    .catch(function(){});

  document.getElementById('btn-continue').addEventListener('click', function(){
    var btn = this;
    btn.disabled = true;
    btn.textContent = 'Carregando...';
    fetch('/ui/welcome/dismiss', {method:'POST', credentials:'include'})
      .then(function(){ window.location.href = '/dashboard'; })
      .catch(function(){ window.location.href = '/dashboard'; });
  });
})();
</script>
</body>
</html>`
