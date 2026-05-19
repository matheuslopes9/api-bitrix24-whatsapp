package api

// Tela de boas-vindas + pagina de planos. Mostradas no primeiro acesso
// apos install (welcome_shown=false em tenant_plans).
//
// Fluxo esperado:
//   1. Bitrix carrega iframe /bitrix-app
//   2. /bitrix-app faz handshake POST /bitrix/auth (seta cookie tenant +
//      cria trial 7d + auto-master)
//   3. /bitrix-app carrega iframe filho com /dashboard
//   4. /dashboard handler verifica tenant_plans.welcome_shown — se FALSE,
//      redireciona pra /welcome em vez de servir o HTML do dashboard
//   5. /welcome mostra: trial countdown, confirmacao master, features
//      Basic vs Pro, botoes "Continuar pro App" e "Ver Planos"
//   6. User clica "Continuar" -> POST /ui/welcome/dismiss -> marca
//      welcome_shown=true -> redireciona pra /dashboard
//
// /planos e' acessivel a qualquer momento, nao bloqueia.

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

// GET /planos — comparativo Basic vs Pro. Acessivel a qualquer momento
// via menu lateral ou banner. Nao precisa estar logado no tenant — pagina
// estatica, sem dados sensiveis.
func (h *handlers) planosPage(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/html; charset=utf-8")
	c.Set("Cache-Control", "no-store, no-cache, must-revalidate")
	return c.SendString(planosHTML)
}

// POST /ui/welcome/dismiss — marca tenant_plans.welcome_shown=TRUE.
// Idempotente. Chamado quando user clica "Continuar pro App" no /welcome.
func (h *handlers) uiWelcomeDismiss(c *fiber.Ctx) error {
	ctx := c.Context()
	domain, err := h.resolveDashboardDomain(ctx, c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	// Garante que tem row (clientes muito antigos podem nao ter).
	_ = h.repo.EnsureTenantTrial(ctx, domain)
	if err := h.repo.SetWelcomeShown(ctx, domain); err != nil {
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
    <p class="sub">Você acabou de instalar o conector WhatsApp + Bitrix24 da UC Technology. Comece a usar agora ou explore os planos disponíveis.</p>
  </div>

  <div class="cards">
    <div class="card">
      <div class="label">Trial gratuito</div>
      <div class="big" id="trial-days">7</div>
      <div class="small">dia(s) restantes no plano <strong>Básico</strong></div>
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
    <h2>O que você pode fazer agora</h2>
    <div class="feat-grid">
      <div class="feat-col basic">
        <h3>✓ Plano Básico (atual)</h3>
        <ul>
          <li>Conectar 1 número WhatsApp (QR ou Oficial)</li>
          <li>Aba WhatsApp dentro de Contatos / Leads / Negociações</li>
          <li>Envio e recepção de mensagens inline no CRM</li>
          <li>Permissões básicas (usuário master)</li>
          <li class="off">Templates de mensagem</li>
          <li class="off">Campanhas SMS via WhatsApp</li>
          <li class="off">Automações BizProc (robots)</li>
          <li class="off">Relatórios e histórico estendido</li>
        </ul>
      </div>
      <div class="feat-col pro">
        <h3>★ Plano Pro (upgrade)</h3>
        <ul>
          <li>Até 10 números WhatsApp (QR + Cloud API Meta)</li>
          <li>Tudo do Básico, +</li>
          <li>Templates Não Oficiais e Templates Meta (HSM)</li>
          <li>Importação direta de templates da Meta Graph API</li>
          <li>Robots BizProc (Não Oficial + Oficial HSM)</li>
          <li>Campanhas SMS (Marketing &gt; SMS via WhatsApp)</li>
          <li>Relatórios completos + histórico de 1 ano</li>
          <li>Suporte prioritário UC Technology</li>
        </ul>
      </div>
    </div>
  </div>

  <div class="cta">
    <button class="btn btn-primary" id="btn-continue">Continuar para o App →</button>
    <a class="btn btn-ghost" href="/planos" target="_top">Ver Planos</a>
  </div>
</div>

<script>
(function(){
  // Carrega dados do plano + master pra preencher countdown e avatar.
  fetch('/ui/plan', {credentials:'include'})
    .then(function(r){ return r.json(); })
    .then(function(p){
      if (p && typeof p.trial_days_remaining === 'number') {
        document.getElementById('trial-days').textContent = p.trial_days_remaining;
      }
    })
    .catch(function(){});

  // Master via /ui/permissions/user-info OU /bitrix/crm/master/status.
  // Como nao temos domain aqui (cookie cuida), usamos /bitrix/crm/master/status
  // com query domain inferido pelo backend. Simplificado: chama /ui/plan
  // ja' devolve domain.
  fetch('/ui/plan', {credentials:'include'})
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

const planosHTML = `<!DOCTYPE html>
<html lang="pt-BR">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Planos UC Talk</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;
     background:linear-gradient(135deg,#0f172a 0%,#1e293b 100%);color:#e2e8f0;
     min-height:100vh;padding:40px 20px}
.wrap{max-width:1100px;margin:0 auto}
.hero{text-align:center;margin-bottom:36px}
.hero h1{font-size:30px;font-weight:800;margin-bottom:8px}
.hero p{font-size:14px;color:#94a3b8;max-width:560px;margin:0 auto;line-height:1.6}
.plans{display:grid;grid-template-columns:repeat(auto-fit,minmax(300px,1fr));gap:24px;margin-bottom:32px}
.plan{background:rgba(30,41,59,.6);border:1px solid rgba(255,255,255,.08);border-radius:18px;padding:28px;backdrop-filter:blur(10px);position:relative;display:flex;flex-direction:column}
.plan.pro{border-color:rgba(37,211,102,.4);box-shadow:0 0 30px rgba(37,211,102,.08)}
.plan .ribbon{position:absolute;top:-12px;right:20px;background:linear-gradient(90deg,#25D366,#10b981);color:#fff;font-size:11px;font-weight:700;padding:4px 12px;border-radius:8px;letter-spacing:.05em}
.plan .name{font-size:13px;font-weight:700;text-transform:uppercase;letter-spacing:.08em;margin-bottom:4px}
.plan.basic .name{color:#60a5fa}
.plan.pro .name{color:#25D366}
.plan .desc{font-size:13px;color:#94a3b8;margin-bottom:18px;line-height:1.5}
.plan .price-row{display:flex;align-items:baseline;gap:6px;margin-bottom:22px;padding-bottom:18px;border-bottom:1px solid rgba(255,255,255,.06)}
.plan .price{font-size:34px;font-weight:800;color:#f1f5f9}
.plan .period{font-size:13px;color:#64748b}
.plan ul{list-style:none;display:flex;flex-direction:column;gap:9px;margin-bottom:24px;flex:1}
.plan li{font-size:13px;color:#cbd5e1;display:flex;align-items:flex-start;gap:9px;line-height:1.5}
.plan li::before{content:'';width:5px;height:5px;border-radius:50%;flex-shrink:0;margin-top:7px}
.plan.basic li::before{background:#60a5fa}
.plan.pro li::before{background:#25D366}
.plan .cta{display:flex;flex-direction:column;gap:8px}
.btn{padding:13px 20px;border-radius:11px;font-size:14px;font-weight:700;border:0;cursor:pointer;text-align:center;text-decoration:none;display:inline-flex;align-items:center;justify-content:center;gap:8px;transition:transform .12s,box-shadow .12s}
.btn:hover{transform:translateY(-1px)}
.btn-primary{background:linear-gradient(90deg,#25D366,#10b981);color:#fff;box-shadow:0 4px 14px rgba(37,211,102,.2)}
.btn-ghost{background:rgba(255,255,255,.05);color:#cbd5e1;border:1px solid rgba(255,255,255,.1)}
.btn-ghost:hover{background:rgba(255,255,255,.08)}
.back{text-align:center;margin-top:20px}
.back a{color:#64748b;text-decoration:none;font-size:13px}
.back a:hover{color:#cbd5e1}
@media(max-width:640px){.hero h1{font-size:22px}}
</style>
</head>
<body>
<div class="wrap">
  <div class="hero">
    <h1>Escolha seu plano</h1>
    <p>Comece com o Básico gratuito por 7 dias. Faça upgrade para Pro a qualquer momento e libere todas as features avançadas.</p>
  </div>

  <div class="plans">
    <div class="plan basic">
      <div class="name">Básico</div>
      <div class="desc">Para times que só precisam conectar o WhatsApp ao Bitrix24 e atender clientes pelo CRM.</div>
      <div class="price-row">
        <span class="price">Grátis</span>
        <span class="period">trial 7 dias</span>
      </div>
      <ul>
        <li>1 número WhatsApp conectado</li>
        <li>Aba WhatsApp no Contato/Lead/Deal</li>
        <li>Envio e recepção inline no CRM</li>
        <li>Permissões básicas (master user)</li>
        <li>Histórico de 30 dias</li>
      </ul>
      <div class="cta">
        <a class="btn btn-ghost" href="/dashboard" target="_top">Continuar com Básico</a>
      </div>
    </div>

    <div class="plan pro">
      <div class="ribbon">RECOMENDADO</div>
      <div class="name">Pro</div>
      <div class="desc">Para empresas que disparam campanhas, automatizam fluxos e operam múltiplos números.</div>
      <div class="price-row">
        <span class="price">Fale</span>
        <span class="period">com o comercial</span>
      </div>
      <ul>
        <li>Até 10 números (QR + Cloud API Meta)</li>
        <li>Tudo do Básico, mais:</li>
        <li>Templates Não Oficiais e Meta HSM</li>
        <li>Importação direta da Meta Graph API</li>
        <li>Robots BizProc (Oficial + Não Oficial)</li>
        <li>Campanhas SMS via WhatsApp</li>
        <li>Relatórios + histórico de 1 ano</li>
        <li>Suporte prioritário</li>
      </ul>
      <div class="cta">
        <a class="btn btn-primary" href="https://wa.me/551932345030?text=Quero%20o%20plano%20Pro%20do%20UC%20Talk" target="_blank">Falar com comercial (WhatsApp)</a>
        <a class="btn btn-ghost" href="mailto:comercial@uctechnology.com.br?subject=UC%20Talk%20-%20Plano%20Pro" target="_blank">Enviar e-mail</a>
      </div>
    </div>
  </div>

  <div class="back"><a href="/dashboard" target="_top">← Voltar pro App</a></div>
</div>
</body>
</html>`
