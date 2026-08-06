package api

// HTML embarcado das páginas /admin/* — separado de admin.go para não poluir.

const adminLoginHTML = `<!doctype html>
<html lang="pt-br">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Admin — UC Talk</title>
<style>
  *{box-sizing:border-box;margin:0;padding:0}
  body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',system-ui,sans-serif;background:radial-gradient(1200px 600px at 50% -10%,#13314f 0%,#0b1220 55%);color:#e2e8f0;min-height:100vh;display:flex;align-items:center;justify-content:center;padding:20px}
  .card{background:rgba(17,25,40,.85);backdrop-filter:blur(12px);padding:38px 34px 30px;border-radius:18px;width:380px;box-shadow:0 24px 60px rgba(0,0,0,.45);border:1px solid rgba(255,255,255,.07)}
  .brand{display:flex;align-items:center;gap:11px;margin-bottom:6px}
  .brand .logo{width:38px;height:38px;border-radius:10px;background:linear-gradient(135deg,#25D366,#10b981);display:flex;align-items:center;justify-content:center;font-size:20px}
  .brand h1{font-size:1.25em;font-weight:800}
  .desc{color:#94a3b8;font-size:.88em;margin:4px 0 24px;line-height:1.5}
  label{display:block;margin-bottom:6px;font-size:.8em;color:#cbd5e1;font-weight:600}
  input{width:100%;padding:.72em .9em;background:#0b1220;border:1px solid #29374b;border-radius:9px;color:#f1f5f9;font-size:1em;font-family:inherit;transition:border-color .15s}
  input:focus{outline:0;border-color:#25D366;box-shadow:0 0 0 3px rgba(37,211,102,.12)}
  .field{margin-bottom:16px}
  button{width:100%;padding:.85em;background:linear-gradient(90deg,#25D366,#10b981);color:#052e16;border:0;border-radius:9px;font-size:1em;font-weight:800;cursor:pointer;margin-top:6px;transition:transform .1s,box-shadow .1s}
  button:hover{transform:translateY(-1px);box-shadow:0 8px 22px rgba(37,211,102,.28)}
  .err{background:rgba(127,29,29,.5);color:#fecaca;padding:.7em 1em;border-radius:9px;font-size:.85em;margin-bottom:16px;border:1px solid rgba(248,113,113,.3)}
  .foot{text-align:center;margin-top:20px;font-size:.75em;color:#475569}
</style>
</head>
<body>
<form class="card" method="post" action="/admin/login">
  <div class="brand" style="justify-content:center;margin-bottom:14px">
    <img src="/assets/logo.png" alt="UC Talk" style="height:46px;width:auto;max-width:220px;object-fit:contain">
  </div>
  <p class="desc" style="text-align:center">Painel de administração. Gerencie tenants, planos, pagamentos e a saúde do sistema.</p>
  <!--ERR-->
  <div class="field">
    <label for="user">Usuário</label>
    <input type="text" id="user" name="user" autocomplete="username" required autofocus>
  </div>
  <div class="field">
    <label for="password">Senha</label>
    <input type="password" id="password" name="password" autocomplete="current-password" required>
  </div>
  <button type="submit">Entrar</button>
  <div class="foot">Acesso restrito · UC Technology</div>
</form>
</body>
</html>`

const adminHomeHTML = `<!doctype html>
<html lang="pt-br">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>UC Talk — Painel Admin</title>
<style>
  *{box-sizing:border-box;margin:0;padding:0}
  :root{
    --bg:#0a0f1a; --sidebar:#0d1421; --panel:#111b2c; --panel2:#0f1826; --hover:#16223a;
    --border:rgba(255,255,255,.06); --txt:#e6edf6; --muted:#8b9bb3; --dim:#5a6b85;
    --green:#25D366; --blue:#60a5fa; --amber:#fbbf24; --red:#f87171; --purple:#a78bfa;
    --sbw:236px;
  }
  html,body{height:100%}
  body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',system-ui,sans-serif;background:var(--bg);color:var(--txt);display:flex;min-height:100vh}
  a{color:var(--blue);text-decoration:none}

  /* ── SIDEBAR ── */
  .sidebar{width:var(--sbw);flex-shrink:0;background:var(--sidebar);border-right:1px solid var(--border);display:flex;flex-direction:column;position:fixed;top:0;left:0;bottom:0;z-index:60;transition:transform .2s}
  .sb-brand{display:flex;flex-direction:column;align-items:center;gap:8px;padding:22px 16px 18px;border-bottom:1px solid var(--border)}
  .sb-brand .logo{width:36px;height:36px;border-radius:10px;background:linear-gradient(135deg,var(--green),#10b981);display:flex;align-items:center;justify-content:center;font-size:18px;flex-shrink:0}
  .sb-brand .t1{font-size:.98em;font-weight:800;line-height:1.1}
  .sb-brand .t2{font-size:.68em;color:var(--dim);font-weight:600;margin-top:1px}
  .sb-nav{flex:1;padding:12px 10px;overflow-y:auto;scrollbar-width:thin;scrollbar-color:rgba(255,255,255,.12) transparent}
  .sb-nav::-webkit-scrollbar{width:6px}
  .sb-nav::-webkit-scrollbar-track{background:transparent}
  .sb-nav::-webkit-scrollbar-thumb{background:rgba(255,255,255,.1);border-radius:999px}
  .sb-nav::-webkit-scrollbar-thumb:hover{background:rgba(255,255,255,.2)}
  /* Scrollbar discreta global (tabelas, panes, logs) */
  *{scrollbar-width:thin;scrollbar-color:rgba(255,255,255,.12) transparent}
  ::-webkit-scrollbar{width:8px;height:8px}
  ::-webkit-scrollbar-track{background:transparent}
  ::-webkit-scrollbar-thumb{background:rgba(255,255,255,.1);border-radius:999px}
  ::-webkit-scrollbar-thumb:hover{background:rgba(255,255,255,.2)}
  .sb-group{font-size:.64em;color:var(--dim);text-transform:uppercase;letter-spacing:.08em;font-weight:700;padding:11px 12px 4px}
  .sb-item{display:flex;align-items:center;gap:11px;padding:8px 12px;border-radius:9px;color:var(--muted);font-size:.87em;font-weight:600;cursor:pointer;margin-bottom:1px;transition:background .12s,color .12s}
  .sb-item:hover{background:var(--hover);color:var(--txt)}
  .sb-item.active{background:linear-gradient(90deg,rgba(37,211,102,.16),rgba(37,211,102,.02));color:#fff}
  .sb-item.active .ic{filter:none}
  .sb-item .ic{font-size:1.05em;width:20px;text-align:center;flex-shrink:0}
  .sb-item .badge-count{margin-left:auto;font-size:.72em;font-weight:700;background:rgba(255,255,255,.08);color:var(--muted);padding:1px 7px;border-radius:999px}
  .sb-foot{padding:14px;border-top:1px solid var(--border);display:flex;flex-direction:column;gap:8px}
  .sb-foot .env{font-size:.72em;color:var(--dim);display:flex;align-items:center;gap:6px}
  .sb-foot .dot{width:7px;height:7px;border-radius:50%;background:var(--green)}
  .logout-btn{padding:.55em;border-radius:9px;font-size:.82em;font-weight:700;border:1px solid rgba(248,113,113,.28);background:rgba(127,29,29,.22);color:#fecaca;cursor:pointer;width:100%}
  .logout-btn:hover{background:rgba(127,29,29,.4)}

  /* ── MAIN ── */
  .main{flex:1;margin-left:var(--sbw);min-width:0;display:flex;flex-direction:column}
  .header{position:sticky;top:0;z-index:40;background:rgba(10,15,26,.9);backdrop-filter:blur(10px);border-bottom:1px solid var(--border);padding:16px 26px;display:flex;align-items:center;gap:14px}
  .header .burger{display:none;background:none;border:0;color:var(--txt);font-size:1.4em;cursor:pointer}
  .header .htitle{font-size:1.2em;font-weight:800}
  .header .hsub{font-size:.78em;color:var(--dim);margin-top:2px}
  .header .spacer{flex:1}
  .btn{padding:.55em .95em;border-radius:9px;font-size:.83em;font-weight:700;border:1px solid var(--border);background:rgba(255,255,255,.04);color:var(--txt);cursor:pointer;transition:background .12s}
  .btn:hover{background:rgba(255,255,255,.09)}
  .content{padding:26px;max-width:1400px;width:100%}

  .page{display:none} .page.active{display:block}

  /* KPIs */
  .kpis{display:grid;grid-template-columns:repeat(auto-fit,minmax(190px,1fr));gap:15px;margin-bottom:26px}
  .kpi{background:var(--panel);border:1px solid var(--border);border-radius:15px;padding:18px 20px;position:relative;overflow:hidden}
  .kpi::after{content:'';position:absolute;right:-20px;top:-20px;width:90px;height:90px;border-radius:50%;opacity:.07}
  .kpi.green::after{background:var(--green)} .kpi.blue::after{background:var(--blue)}
  .kpi.amber::after{background:var(--amber)} .kpi.red::after{background:var(--red)} .kpi.purple::after{background:var(--purple)}
  .kpi .label{font-size:.72em;color:var(--dim);text-transform:uppercase;letter-spacing:.06em;font-weight:700;margin-bottom:9px}
  .kpi .value{font-size:2em;font-weight:800;line-height:1}
  .kpi .foot{font-size:.76em;color:var(--muted);margin-top:7px}
  .kpi.green .value{color:var(--green)} .kpi.blue .value{color:var(--blue)}
  .kpi.amber .value{color:var(--amber)} .kpi.red .value{color:var(--red)} .kpi.purple .value{color:var(--purple)}

  .section-title{font-size:1.05em;font-weight:800;margin:8px 0 14px;display:flex;align-items:center;gap:9px}

  /* toolbar */
  .toolbar{display:flex;gap:10px;align-items:center;margin-bottom:16px;flex-wrap:wrap}
  .search{flex:1;min-width:220px;padding:.62em .95em;background:var(--panel2);border:1px solid var(--border);border-radius:11px;color:var(--txt);font-size:.9em}
  .search:focus{outline:0;border-color:var(--green)}
  select.filter{padding:.62em .85em;background:var(--panel2);border:1px solid var(--border);border-radius:11px;color:var(--txt);font-size:.85em}

  /* table */
  .tablewrap{background:var(--panel);border:1px solid var(--border);border-radius:15px;overflow:hidden}
  .tablescroll{overflow-x:auto}
  table{width:100%;border-collapse:collapse;min-width:640px}
  th{text-align:left;font-size:.71em;color:var(--dim);text-transform:uppercase;letter-spacing:.05em;font-weight:700;padding:13px 15px;border-bottom:1px solid var(--border);background:var(--panel2);white-space:nowrap}
  td{padding:13px 15px;font-size:.86em;border-bottom:1px solid rgba(255,255,255,.035);vertical-align:middle}
  tr:last-child td{border-bottom:0}
  tr:hover td{background:rgba(255,255,255,.018)}
  .domain{font-weight:600;color:var(--txt)}
  .meta{font-size:.78em;color:var(--dim)}

  /* badges */
  .badge{display:inline-flex;align-items:center;gap:5px;font-size:.72em;font-weight:700;padding:3px 9px;border-radius:999px;white-space:nowrap}
  .b-trial{background:rgba(251,191,36,.15);color:var(--amber)}
  .b-active{background:rgba(37,211,102,.15);color:var(--green)}
  .b-expired{background:rgba(248,113,113,.15);color:var(--red)}
  .b-suspended{background:rgba(148,163,184,.15);color:var(--muted)}
  .b-pro{background:rgba(167,139,250,.15);color:var(--purple)}
  .b-basic{background:rgba(96,165,250,.15);color:var(--blue)}
  .b-none{background:rgba(148,163,184,.1);color:var(--dim)}
  .tok-valid{color:var(--green)} .tok-expiring{color:var(--amber)} .tok-expired{color:var(--red)}

  /* actions dropdown */
  .actions{position:relative;display:inline-block}
  .actions>.btn{padding:.35em .7em}
  .menu{position:absolute;right:0;top:calc(100% + 4px);background:var(--panel);border:1px solid var(--border);border-radius:11px;min-width:216px;box-shadow:0 16px 44px rgba(0,0,0,.55);z-index:30;overflow:hidden;display:none}
  .menu.open{display:block}
  .menu button{display:block;width:100%;text-align:left;padding:10px 14px;background:none;border:0;color:var(--txt);font-size:.83em;cursor:pointer}
  .menu button:hover{background:var(--hover)}
  .menu .sep{height:1px;background:var(--border);margin:3px 0}
  .menu .danger{color:#fecaca}

  .empty{text-align:center;padding:44px;color:var(--dim);font-size:.9em}
  .loading{text-align:center;padding:44px;color:var(--muted)}
  .mono{font-family:ui-monospace,'SF Mono',Menlo,monospace;font-size:.9em}

  /* tools */
  .tools{display:grid;grid-template-columns:repeat(auto-fit,minmax(300px,1fr));gap:15px}
  .toolcard{background:var(--panel);border:1px solid var(--border);border-radius:15px;padding:20px}
  .toolcard h3{font-size:.98em;margin-bottom:6px}
  .toolcard p{font-size:.8em;color:var(--muted);line-height:1.5;margin-bottom:13px}
  .toolcard .row{display:flex;gap:8px;flex-wrap:wrap}
  input.dominput{padding:.58em .85em;background:var(--panel2);border:1px solid var(--border);border-radius:10px;color:var(--txt);font-size:.85em;width:100%;margin-bottom:11px}

  /* toast */
  #toast{position:fixed;bottom:24px;left:calc(50% + var(--sbw)/2);transform:translateX(-50%);background:var(--panel);border:1px solid var(--border);color:var(--txt);padding:.85em 1.4em;border-radius:12px;font-size:.88em;box-shadow:0 14px 44px rgba(0,0,0,.55);z-index:9999;display:none;max-width:80vw}
  #toast.ok{border-color:rgba(37,211,102,.4)} #toast.err{border-color:rgba(248,113,113,.4)}

  .backdrop{display:none;position:fixed;inset:0;background:rgba(0,0,0,.5);z-index:55}

  @media(max-width:900px){
    .sidebar{transform:translateX(-100%)}
    .sidebar.open{transform:translateX(0)}
    .main{margin-left:0}
    .header .burger{display:block}
    #toast{left:50%}
    .backdrop.open{display:block}
  }
</style>
</head>
<body>
<div class="backdrop" id="backdrop" onclick="fecharSidebar()"></div>

<aside class="sidebar" id="sidebar">
  <div class="sb-brand">
    <img src="/assets/logo.png" alt="UC Talk" style="width:100%;max-width:180px;height:auto;object-fit:contain;display:block">
    <div class="t2" style="letter-spacing:.1em;text-transform:uppercase">Painel Admin</div>
  </div>
  <nav class="sb-nav">
    <div class="sb-group">Principal</div>
    <div class="sb-item active" data-page="overview" onclick="irPara('overview')"><span class="ic">📊</span> Visão geral</div>
    <div class="sb-item" data-page="tenants" onclick="irPara('tenants')"><span class="ic">🏢</span> Tenants <span class="badge-count" id="cnt-tenants">0</span></div>
    <div class="sb-item" data-page="usage" onclick="irPara('usage')"><span class="ic">📈</span> Consumo</div>
    <div class="sb-group">Financeiro</div>
    <div class="sb-item" data-page="plandefs" onclick="irPara('plandefs')"><span class="ic">🧩</span> Planos</div>
    <div class="sb-item" data-page="cupons" onclick="irPara('cupons')"><span class="ic">🎟️</span> Cupons</div>
    <div class="sb-item" data-page="gateway" onclick="irPara('gateway')"><span class="ic">🔌</span> Gateway</div>
    <div class="sb-item" data-page="billing" onclick="irPara('billing')"><span class="ic">💳</span> Pagamentos <span class="badge-count" id="cnt-charges">0</span></div>
    <div class="sb-group">Monitoramento</div>
    <div class="sb-item" data-page="system" onclick="irPara('system')"><span class="ic">🖥️</span> Sistema</div>
    <div class="sb-item" data-page="logs" onclick="irPara('logs')"><span class="ic">📜</span> Logs ao vivo</div>
    <div class="sb-group">Segurança</div>
    <div class="sb-item" data-page="users" onclick="irPara('users')"><span class="ic">👥</span> Usuários admin</div>
    <div class="sb-item" data-page="ips" onclick="irPara('ips')"><span class="ic">🚫</span> IPs bloqueados</div>
    <div class="sb-item" data-page="audit" onclick="irPara('audit')"><span class="ic">📋</span> Auditoria</div>
    <div class="sb-group">Sistema</div>
    <div class="sb-item" data-page="preview" onclick="irPara('preview')"><span class="ic">👁️</span> Preview do app</div>
    <div class="sb-item" data-page="tools" onclick="irPara('tools')"><span class="ic">🔧</span> Ferramentas</div>
    <div class="sb-item" data-page="diag" onclick="irPara('diag')"><span class="ic">🩺</span> Diagnóstico</div>
  </nav>
  <div class="sb-foot">
    <div class="env"><span class="dot"></span> <span id="env-txt">online</span></div>
    <form method="post" action="/admin/logout"><button class="logout-btn" type="submit">Sair</button></form>
  </div>
</aside>

<div class="main">
  <div class="header">
    <button class="burger" onclick="abrirSidebar()">☰</button>
    <div>
      <div class="htitle" id="page-title">Visão geral</div>
      <div class="hsub" id="page-sub">Métricas e saúde do sistema</div>
    </div>
    <div class="spacer"></div>
    <button class="btn" onclick="carregarTudo()">↻ Atualizar</button>
  </div>

  <div class="content">
    <!-- OVERVIEW -->
    <div class="page active" id="page-overview">
      <div class="kpis" id="kpis"><div class="loading">Carregando métricas…</div></div>
      <div class="section-title">💳 Últimos pagamentos</div>
      <div class="tablewrap"><div class="tablescroll">
        <table>
          <thead><tr><th>Referência</th><th>Tenant</th><th>Plano</th><th>Valor</th><th>Status</th><th>Data</th></tr></thead>
          <tbody id="recent-charges"><tr><td colspan="6" class="loading">Carregando…</td></tr></tbody>
        </table>
      </div></div>
    </div>

    <!-- TENANTS -->
    <div class="page" id="page-tenants">
      <div class="toolbar">
        <input class="search" id="tenant-search" placeholder="🔎 Buscar por domínio…" oninput="renderTenants()">
        <select class="filter" id="tenant-filter" onchange="renderTenants()">
          <option value="">Todos os status</option>
          <option value="trial">Trial</option><option value="active">Ativo</option>
          <option value="expired">Expirado</option><option value="suspended">Suspenso</option>
          <option value="no_plan">Sem plano</option>
        </select>
        <select class="filter" id="tenant-plan-filter" onchange="renderTenants()">
          <option value="">Todos os planos</option>
          <option value="pro">Pro</option><option value="basic">Básico</option>
        </select>
      </div>
      <div class="tablewrap"><div class="tablescroll">
        <table>
          <thead><tr><th>Portal</th><th>Plano</th><th>Status</th><th>Conexões</th><th>Msgs 24h</th><th>Token</th><th style="text-align:right">Ações</th></tr></thead>
          <tbody id="tenants-body"><tr><td colspan="7" class="loading">Carregando tenants…</td></tr></tbody>
        </table>
      </div></div>
    </div>

    <!-- BILLING -->
    <div class="page" id="page-billing">
      <div class="tablewrap"><div class="tablescroll">
        <table>
          <thead><tr><th>Tenant</th><th>Plano</th><th>Método</th><th>Valor</th><th>Status</th><th>Referência</th><th>Criado</th><th>Boleto</th></tr></thead>
          <tbody id="billing-body"><tr><td colspan="8" class="loading">Carregando cobranças…</td></tr></tbody>
        </table>
      </div></div>
    </div>

    <!-- CUPONS -->
    <div class="page" id="page-cupons">
      <div class="toolcard" style="margin-bottom:16px">
        <h3>🎟️ Novo cupom</h3>
        <p>Crie cupons de desconto ou de extensão de teste. O cliente digita o código na aba Assinatura.</p>
        <div style="display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:10px;align-items:end">
          <div><label style="font-size:.72em;color:var(--dim)">Código</label><input class="dominput" id="cp-code" placeholder="PROMO10" style="margin:2px 0 0;text-transform:uppercase"></div>
          <div><label style="font-size:.72em;color:var(--dim)">Tipo</label>
            <select class="filter" id="cp-kind" style="width:100%" onchange="atualizarDicaCupom()">
              <option value="percent">Desconto %</option>
              <option value="amount">Desconto R$</option>
              <option value="trial_days">Dias de teste</option>
            </select></div>
          <div><label style="font-size:.72em;color:var(--dim)" id="cp-value-label">Valor (%)</label><input class="dominput" id="cp-value" type="number" min="1" value="10" style="margin:2px 0 0"></div>
          <div><label style="font-size:.72em;color:var(--dim)">Plano (vazio = todos)</label><input class="dominput" id="cp-plan" placeholder="pro" style="margin:2px 0 0"></div>
          <div><label style="font-size:.72em;color:var(--dim)">Máx. usos (0 = ilimitado)</label><input class="dominput" id="cp-max" type="number" min="0" value="0" style="margin:2px 0 0"></div>
          <div><label style="font-size:.72em;color:var(--dim)">Validade (opcional)</label><input class="dominput" id="cp-exp" type="date" style="margin:2px 0 0"></div>
        </div>
        <input class="dominput" id="cp-desc" placeholder="Descrição (aparece pro cliente)" style="margin:10px 0 0">
        <div class="row" style="margin-top:12px;align-items:center">
          <label style="display:flex;align-items:center;gap:8px;font-size:.86em;color:#cbd5e1;cursor:pointer"><input type="checkbox" id="cp-active" checked style="width:16px;height:16px"> Ativo</label>
          <button class="btn btn-primary" onclick="salvarCupom()">Criar cupom</button>
        </div>
      </div>
      <div class="tablewrap"><div class="tablescroll">
        <table>
          <thead><tr><th>Código</th><th>Tipo</th><th>Valor</th><th>Plano</th><th>Usos</th><th>Validade</th><th>Status</th><th style="text-align:right">Ações</th></tr></thead>
          <tbody id="cupons-body"><tr><td colspan="8" class="loading">Carregando…</td></tr></tbody>
        </table>
      </div></div>
    </div>

    <!-- GATEWAY (config de pagamento) -->
    <div class="page" id="page-gateway">
      <div style="max-width:640px">
        <div class="toolcard">
          <h3>🏦 Gateway de pagamento (Itaú)</h3>
          <p>PIX e Boleto são emitidos direto pelo Itaú (mTLS). As credenciais vêm das variáveis de ambiente <code>ITAU_*</code> no servidor; abaixo é só o status (somente leitura).</p>

          <div id="itau-status" style="margin:8px 0 14px"></div>

          <div style="display:flex;gap:10px">
            <div style="flex:1">
              <label style="font-size:.72em;color:var(--dim)">Client ID</label>
              <input class="dominput" id="itau-clientid" readonly style="margin:2px 0 12px;opacity:.85">
            </div>
            <div style="flex:1">
              <label style="font-size:.72em;color:var(--dim)">Chave PIX</label>
              <input class="dominput" id="itau-chavepix" readonly style="margin:2px 0 12px;opacity:.85">
            </div>
          </div>

          <div style="display:flex;gap:10px">
            <div style="flex:1">
              <label style="font-size:.72em;color:var(--dim)">Ambiente</label>
              <input class="dominput" id="itau-env" readonly style="margin:2px 0 12px;opacity:.85">
            </div>
            <div style="flex:1">
              <label style="font-size:.72em;color:var(--dim)">Certificado mTLS</label>
              <input class="dominput" id="itau-cert" readonly style="margin:2px 0 12px;opacity:.85">
            </div>
          </div>

          <div style="font-size:.74em;color:var(--dim);margin-top:2px">O período de teste (trial) é configurado na aba <b>Planos</b>. Para trocar credenciais, ajuste as envs <code>ITAU_*</code> no EasyPanel e redeploy.</div>

          <div class="row" style="margin-top:16px">
            <button class="btn" onclick="testarGateway('pix')">Testar PIX (R$1 real)</button>
            <button class="btn" onclick="testarGateway('boleto')">Testar Boleto (validação)</button>
          </div>
          <div id="gateway-test" style="margin-top:12px"></div>
        </div>

        <div class="toolcard" style="margin-top:14px">
          <h3>🔔 Webhook PIX (Itaú)</h3>
          <p>Cadastre esta URL no portal do Itaú — <b>SEM</b> o sufixo <code>/pix</code> (o banco acrescenta sozinho). É por ela que o Itaú avisa quando um PIX é pago, liberando o plano automaticamente.</p>
          <input class="dominput" id="bc-postback" readonly onclick="this.select()" style="margin:0;cursor:pointer">
        </div>
      </div>
    </div>

    <!-- PLAN DEFS (construtor de planos) -->
    <div class="page" id="page-plandefs">
      <div class="toolbar">
        <div style="flex:1;font-size:.85em;color:var(--muted)">Configure preço e features de cada plano. Alterações valem na hora para novos gates e para os cards que o cliente vê.</div>
        <button class="btn btn-primary" onclick="novoPlano()">➕ Novo plano</button>
      </div>
      <div id="plandefs-list" style="display:grid;grid-template-columns:repeat(auto-fill,minmax(320px,1fr));gap:16px">
        <div class="loading">Carregando planos…</div>
      </div>
    </div>

    <!-- USAGE -->
    <div class="page" id="page-usage">
      <div class="section-title">📈 Consumo por tenant</div>
      <div class="tablewrap"><div class="tablescroll">
        <table>
          <thead><tr><th>Tenant</th><th>Msgs 24h</th><th>Msgs 7d</th><th>Msgs 30d</th><th>Sessões</th><th>Pagamentos</th><th>Receita</th></tr></thead>
          <tbody id="usage-body"><tr><td colspan="7" class="loading">Carregando consumo…</td></tr></tbody>
        </table>
      </div></div>
    </div>

    <!-- SYSTEM -->
    <div class="page" id="page-system">
      <div class="kpis" id="sys-kpis"><div class="loading">Carregando métricas do sistema…</div></div>
      <div class="section-title">🖥️ Recursos do processo</div>
      <div class="tablewrap"><div class="tablescroll">
        <table><tbody id="sys-detail"><tr><td class="loading">Carregando…</td></tr></tbody></table>
      </div></div>
    </div>

    <!-- LOGS -->
    <div class="page" id="page-logs">
      <div class="toolbar">
        <input class="search" id="log-filter" placeholder="🔎 Filtrar linhas (ex: error, domain)…" oninput="filtrarLogs()">
        <button class="btn" id="log-pause" onclick="toggleLogPause()">⏸ Pausar</button>
        <button class="btn" onclick="document.getElementById('log-view').innerHTML=''">🗑 Limpar</button>
      </div>
      <pre id="log-view" style="background:#05080f;border:1px solid var(--border);border-radius:12px;padding:14px;font-family:ui-monospace,Menlo,monospace;font-size:.76em;line-height:1.5;color:#a7f3d0;overflow:auto;height:60vh;white-space:pre-wrap"></pre>
    </div>

    <!-- USERS -->
    <div class="page" id="page-users">
      <div class="toolcard" style="margin-bottom:16px">
        <h3>➕ Novo usuário admin</h3>
        <p>Crie logins adicionais pro painel. O login root (env) continua funcionando sempre.</p>
        <div class="row" style="gap:8px;align-items:flex-end">
          <div style="flex:1;min-width:140px"><label style="font-size:.72em;color:var(--dim)">E-mail</label><input class="dominput" id="u-email" placeholder="pessoa@uctechnology.com.br" style="margin:0"></div>
          <div style="flex:1;min-width:120px"><label style="font-size:.72em;color:var(--dim)">Nome</label><input class="dominput" id="u-name" placeholder="Nome" style="margin:0"></div>
          <div style="min-width:140px"><label style="font-size:.72em;color:var(--dim)">Senha (8+)</label><input class="dominput" id="u-pass" type="password" placeholder="senha" style="margin:0"></div>
          <div style="min-width:120px"><label style="font-size:.72em;color:var(--dim)">Papel</label>
            <select class="filter" id="u-role" style="width:100%"><option value="support">Suporte</option><option value="superadmin">Superadmin</option></select></div>
          <button class="btn btn-primary" onclick="criarUser()" style="height:38px">Criar</button>
        </div>
      </div>
      <div class="tablewrap"><div class="tablescroll">
        <table>
          <thead><tr><th>E-mail</th><th>Nome</th><th>Papel</th><th>Status</th><th>Último acesso</th><th style="text-align:right">Ações</th></tr></thead>
          <tbody id="users-body"><tr><td colspan="6" class="loading">Carregando…</td></tr></tbody>
        </table>
      </div></div>
    </div>

    <!-- IPS -->
    <div class="page" id="page-ips">
      <div class="toolcard" style="margin-bottom:16px">
        <h3>🚫 Bloquear IP manualmente</h3>
        <p>IPs com muitas tentativas de login são bloqueados automaticamente. Aqui você libera ou adiciona bloqueios manuais.</p>
        <div class="row" style="gap:8px;align-items:flex-end">
          <div style="flex:1;min-width:160px"><label style="font-size:.72em;color:var(--dim)">IP</label><input class="dominput" id="ip-addr" placeholder="203.0.113.45" style="margin:0"></div>
          <div style="flex:2;min-width:160px"><label style="font-size:.72em;color:var(--dim)">Nota</label><input class="dominput" id="ip-note" placeholder="motivo (opcional)" style="margin:0"></div>
          <button class="btn btn-danger" onclick="bloquearIP()" style="height:38px">Bloquear</button>
        </div>
      </div>
      <div class="tablewrap"><div class="tablescroll">
        <table>
          <thead><tr><th>IP</th><th>Motivo</th><th>Falhas</th><th>Status</th><th>Atualizado</th><th>Nota</th><th style="text-align:right">Ação</th></tr></thead>
          <tbody id="ips-body"><tr><td colspan="7" class="loading">Carregando…</td></tr></tbody>
        </table>
      </div></div>
    </div>

    <!-- AUDIT -->
    <div class="page" id="page-audit">
      <div class="section-title">📋 Log de auditoria</div>
      <div class="tablewrap"><div class="tablescroll">
        <table>
          <thead><tr><th>Quando</th><th>Ator</th><th>Ação</th><th>Alvo</th><th>Detalhe</th><th>IP</th></tr></thead>
          <tbody id="audit-body"><tr><td colspan="6" class="loading">Carregando…</td></tr></tbody>
        </table>
      </div></div>
    </div>

    <!-- PREVIEW -->
    <div class="page" id="page-preview">
      <div class="toolcard" style="margin-bottom:14px">
        <h3>👁️ Preview do app (como o cliente vê no Bitrix24)</h3>
        <p>Esta é a interface que o cliente enxerga dentro do Bitrix24. Você acessa via cookie de admin — no cliente real, ela abre no iframe do Marketplace. Use o seletor pra ver as telas de um tenant específico.</p>
        <div class="row" style="align-items:center">
          <input class="dominput" id="preview-domain" placeholder="domínio do tenant (opcional) — ex: crm.cliente.bitrix24.com" style="margin:0;max-width:420px">
          <button class="btn btn-primary" onclick="recarregarPreview()" style="height:38px">Carregar preview</button>
          <button class="btn" onclick="abrirPreviewNovaAba()" style="height:38px">Abrir em nova aba ↗</button>
        </div>
      </div>
      <div style="background:#0b1220;border:1px solid var(--border);border-radius:14px;overflow:hidden;height:74vh">
        <iframe id="preview-frame" src="about:blank" style="width:100%;height:100%;border:0;background:#0f172a" title="Preview UC Talk"></iframe>
      </div>
    </div>

    <!-- TOOLS -->
    <div class="page" id="page-tools">
      <div class="tools">
        <div class="toolcard">
          <h3>🔧 Ações por tenant</h3>
          <p>Informe o domínio e escolha a ação. Útil pra reparar placement, re-registrar robôs ou popular templates de teste.</p>
          <input class="dominput" id="tool-domain" placeholder="crm.cliente.bitrix24.com">
          <div class="row">
            <button class="btn" onclick="toolAction('bp-reregister','GET')">Re-registrar robôs</button>
            <button class="btn" onclick="toolAction('placements/force-unbind','GET')">Limpar placements</button>
            <button class="btn" onclick="toolAction('seed-templates','GET')">Semear templates</button>
            <button class="btn" onclick="toolAction('bp-debug-sessions','GET')">Debug sessões</button>
            <button class="btn" onclick="toolAction('portal-debug','GET')">Debug portal</button>
          </div>
        </div>
        <div class="toolcard">
          <h3>🩺 Manutenção global</h3>
          <p>Ações que afetam todos os tenants. Use com cuidado.</p>
          <div class="row">
            <button class="btn" onclick="globalAction('queue/flush','POST')">Esvaziar filas</button>
            <button class="btn" onclick="globalAction('cleanup/banned-sessions','POST')">Limpar sessões banidas</button>
            <button class="btn" onclick="globalAction('cleanup/placeholder-portals','POST')">Limpar placeholders</button>
          </div>
        </div>
      </div>
      <pre id="tool-output" style="margin-top:16px;background:var(--panel2);border:1px solid var(--border);border-radius:12px;padding:16px;font-size:.78em;color:var(--muted);overflow:auto;max-height:420px;white-space:pre-wrap;display:none"></pre>
    </div>

    <!-- DIAG -->
    <div class="page" id="page-diag">
      <div class="toolcard" style="margin-bottom:16px">
        <h3>🩺 Diagnóstico do banco</h3>
        <p>Contagens das tabelas-chave + amostras. Útil quando o painel mostra zeros e precisamos ver onde está quebrando.</p>
        <div class="row"><button class="btn" onclick="globalAction('debug','GET')">Rodar diagnóstico</button></div>
      </div>
      <pre id="diag-output" style="background:var(--panel2);border:1px solid var(--border);border-radius:12px;padding:16px;font-size:.78em;color:var(--muted);overflow:auto;max-height:520px;white-space:pre-wrap;display:none"></pre>
    </div>
  </div>
</div>

<div id="toast"></div>

<script>
var TENANTS=[];
var PAGES={
  overview:{title:'Visão geral',sub:'Métricas e saúde do sistema'},
  tenants:{title:'Tenants',sub:'Portais Bitrix24 que instalaram o app'},
  usage:{title:'Consumo',sub:'Uso de recursos por tenant'},
  plandefs:{title:'Planos',sub:'Construtor de planos — preço e features'},
  cupons:{title:'Cupons',sub:'Promoções e descontos'},
  gateway:{title:'Gateway',sub:'Credenciais do gateway e período de teste'},
  billing:{title:'Pagamentos',sub:'Cobranças e receita via maxiPago'},
  system:{title:'Sistema',sub:'Monitoramento do processo em tempo real'},
  logs:{title:'Logs ao vivo',sub:'Stream de logs direto do servidor'},
  users:{title:'Usuários admin',sub:'Gerenciar quem acessa o painel'},
  ips:{title:'IPs bloqueados',sub:'Controle de acesso por IP'},
  audit:{title:'Auditoria',sub:'Histórico de ações no painel'},
  preview:{title:'Preview do app',sub:'Como o cliente vê o UC Talk no Bitrix24'},
  tools:{title:'Ferramentas',sub:'Ações de manutenção e reparo'},
  diag:{title:'Diagnóstico',sub:'Estado interno do banco de dados'}
};

function toast(msg,ok){var t=document.getElementById('toast');t.textContent=msg;t.className=ok?'ok':'err';t.style.display='block';clearTimeout(window._tt);window._tt=setTimeout(function(){t.style.display='none';},3800);}
function irPara(p){
  document.querySelectorAll('.sb-item').forEach(function(t){t.classList.toggle('active',t.dataset.page===p);});
  document.querySelectorAll('.page').forEach(function(pg){pg.classList.remove('active');});
  document.getElementById('page-'+p).classList.add('active');
  if(PAGES[p]){document.getElementById('page-title').textContent=PAGES[p].title;document.getElementById('page-sub').textContent=PAGES[p].sub;}
  fecharSidebar();
  // Carrega dados sob demanda por secao.
  if(p==='gateway')carregarGateway();
  if(p==='cupons')carregarCupons();
  if(p==='plandefs')carregarPlanDefs();
  if(p==='usage')carregarUsage();
  if(p==='system')carregarSystem();
  if(p==='users')carregarUsers();
  if(p==='ips')carregarIPs();
  if(p==='audit')carregarAudit();
  if(p==='logs')iniciarLogs(); else pararLogs();
  if(p==='preview')abrirPreview();
  if(p==='system'){clearInterval(window._sysT);window._sysT=setInterval(carregarSystem,5000);}else{clearInterval(window._sysT);}
}
function previewURL(){
  var dom=(document.getElementById('preview-domain').value||'').trim();
  var url='/dashboard';
  if(dom)url+='?domain='+encodeURIComponent(dom);
  return url;
}
function abrirPreview(){
  var f=document.getElementById('preview-frame');
  if(f && (f.src==='about:blank' || f.src.indexOf('about:blank')>=0))f.src=previewURL();
}
function recarregarPreview(){document.getElementById('preview-frame').src=previewURL();}
function abrirPreviewNovaAba(){window.open(previewURL(),'_blank');}
function abrirSidebar(){document.getElementById('sidebar').classList.add('open');document.getElementById('backdrop').classList.add('open');}
function fecharSidebar(){document.getElementById('sidebar').classList.remove('open');document.getElementById('backdrop').classList.remove('open');}

function fmtBRL(cents){return 'R$ '+((cents||0)/100).toFixed(2).replace('.',',');}
function fmtDate(s){if(!s)return '—';try{return new Date(s).toLocaleString('pt-BR',{day:'2-digit',month:'2-digit',year:'2-digit',hour:'2-digit',minute:'2-digit'});}catch(e){return s;}}
function planBadge(plan,status){var s=status||'no_plan';var cls={trial:'b-trial',active:'b-active',expired:'b-expired',suspended:'b-suspended',no_plan:'b-none'}[s]||'b-none';var lbl={trial:'Trial',active:'Ativo',expired:'Expirado',suspended:'Suspenso',no_plan:'Sem plano'}[s]||s;return '<span class="badge '+cls+'">'+lbl+'</span>';}
// planTag usa os planos configurados (PLANDEFS) pra rotular; cai nos
// legados se ainda nao carregou a lista.
function planTag(plan){
  if(!plan||plan==='none')return '<span class="badge b-none">—</span>';
  var def=(PLANDEFS||[]).filter(function(x){return x.code===plan;})[0];
  var nome=def?def.name:(plan==='pro'?'Pro':plan==='basic'?'Básico':plan==='trial'?'Trial':plan);
  var cls=(def&&def.is_trial_default)||plan==='trial'?'b-trial':
          (def?(def.is_pro?'b-pro':'b-basic'):(plan==='pro'?'b-pro':'b-basic'));
  return '<span class="badge '+cls+'">'+nome.toUpperCase()+'</span>';
}
function chargeStatus(s){return s==='paid'?'<span class="badge b-active">pago</span>':s==='pending'?'<span class="badge b-trial">pendente</span>':'<span class="badge b-none">'+s+'</span>';}

function renderKpis(m){
  var box=document.getElementById('kpis');
  function k(cls,label,val,foot){return '<div class="kpi '+cls+'"><div class="label">'+label+'</div><div class="value">'+val+'</div><div class="foot">'+(foot||'')+'</div></div>';}
  box.innerHTML=
    k('blue','Tenants',m.tenants_total,(m.tenants_pro||0)+' Pro · '+(m.tenants_basic||0)+' Básico')+
    k('amber','Em trial',m.tenants_trial,'período de teste')+
    k('green','Ativos (pagos)',m.tenants_active,'assinatura em dia')+
    k('red','Expirados',m.tenants_expired,(m.tenants_suspended||0)+' suspensos')+
    k('green','Receita recebida',fmtBRL(m.revenue_cents_paid),(m.charges_paid||0)+' pagamentos')+
    k('purple','Sessões ativas',m.sessions_active,(m.msgs_24h||0)+' msgs 24h');
  document.getElementById('cnt-tenants').textContent=m.tenants_total||0;
  document.getElementById('cnt-charges').textContent=(m.charges_paid||0)+(m.charges_pending||0);
}
function renderRecentCharges(charges){
  var b=document.getElementById('recent-charges');
  if(!charges||!charges.length){b.innerHTML='<tr><td colspan="6" class="empty">Nenhum pagamento ainda.</td></tr>';return;}
  b.innerHTML=charges.slice(0,8).map(function(c){return '<tr><td class="mono meta">'+(c.reference_num||'').slice(0,18)+'</td><td class="domain">'+c.domain+'</td><td>'+planTag(c.plan)+'</td><td>'+fmtBRL(c.amount_cents)+'</td><td>'+chargeStatus(c.status)+'</td><td class="meta">'+fmtDate(c.created_at)+'</td></tr>';}).join('');
}
function renderTenants(){
  var q=(document.getElementById('tenant-search').value||'').toLowerCase();
  var fs=document.getElementById('tenant-filter').value, fp=document.getElementById('tenant-plan-filter').value;
  var body=document.getElementById('tenants-body');
  var list=TENANTS.filter(function(t){if(q&&t.domain.toLowerCase().indexOf(q)<0)return false;if(fs&&(t.plan_status||'no_plan')!==fs)return false;if(fp&&t.plan!==fp)return false;return true;});
  if(!list.length){body.innerHTML='<tr><td colspan="7" class="empty">Nenhum tenant encontrado.</td></tr>';return;}
  body.innerHTML=list.map(function(t){
    var conn=(t.connections_qr||0)+' QR';if(t.connections_cloud)conn+=' · '+t.connections_cloud+' Cloud';
    var tokCls={valid:'tok-valid',expiring:'tok-expiring',expired:'tok-expired'}[t.token_status]||'tok-expired';
    var tokLbl={valid:'válido',expiring:'expirando',expired:'expirado'}[t.token_status]||t.token_status;
    var trial=t.plan_status==='trial'?'<div class="meta">'+(t.trial_days_remaining||0)+'d restantes</div>':'';
    var d=encodeURIComponent(t.domain);
    return '<tr><td><div class="domain">'+t.domain+'</div><div class="meta">Linha '+(t.open_line_id||'—')+' · desde '+fmtDate(t.installed_at)+'</div></td>'+
      '<td>'+planTag(t.plan)+'</td><td>'+planBadge(t.plan,t.plan_status)+trial+'</td>'+
      '<td>'+conn+'</td><td>'+(t.msgs_24h||0)+'<div class="meta">'+(t.msgs_inbound_24h||0)+'↓ '+(t.msgs_outbound_24h||0)+'↑</div></td>'+
      '<td class="'+tokCls+'">● '+tokLbl+'</td>'+
      '<td style="text-align:right"><div class="actions"><button class="btn" onclick="toggleMenu(this)">⋯</button><div class="menu">'+
        '<button onclick="planAct(\''+d+'\',\'activate-pro\')">✅ Ativar Pro</button>'+
        '<button onclick="planActBasic(\''+d+'\')">🔵 Ativar Básico</button>'+
        '<button onclick="planAct(\''+d+'\',\'extend-trial\')">⏰ +7 dias de trial</button>'+
        '<div class="sep"></div>'+
        '<button onclick="planAct(\''+d+'\',\'reactivate\')">↻ Reativar</button>'+
        '<button class="danger" onclick="planAct(\''+d+'\',\'suspend\')">⛔ Suspender</button>'+
        '<div class="sep"></div>'+
        '<button onclick="setToolDomain(\''+d+'\')">🔧 Abrir em Ferramentas</button>'+
      '</div></div></td></tr>';
  }).join('');
}
function toggleMenu(btn){var m=btn.nextElementSibling;var was=m.classList.contains('open');document.querySelectorAll('.menu.open').forEach(function(x){x.classList.remove('open');});if(!was)m.classList.add('open');}
document.addEventListener('click',function(e){if(!e.target.closest('.actions'))document.querySelectorAll('.menu.open').forEach(function(x){x.classList.remove('open');});});

function renderBilling(charges){
  var b=document.getElementById('billing-body');
  if(!charges||!charges.length){b.innerHTML='<tr><td colspan="8" class="empty">Nenhuma cobrança registrada.</td></tr>';return;}
  b.innerHTML=charges.map(function(c){var bol=c.boleto_url?'<a href="'+c.boleto_url+'" target="_blank">abrir ↗</a>':'—';return '<tr><td class="domain">'+c.domain+'</td><td>'+planTag(c.plan)+'</td><td class="meta">'+(c.method||'—')+'</td><td>'+fmtBRL(c.amount_cents)+'</td><td>'+chargeStatus(c.status)+'</td><td class="mono meta">'+(c.reference_num||'').slice(0,20)+'</td><td class="meta">'+fmtDate(c.created_at)+'</td><td>'+bol+'</td></tr>';}).join('');
}

function planAct(domain,action){
  var dom=decodeURIComponent(domain);
  var map={'activate-pro':{url:'/admin/api/tenant/plan/activate-pro',body:{domain:dom},confirm:'Ativar plano Pro para '+dom+'?'},'extend-trial':{url:'/admin/api/tenant/plan/extend-trial',body:{domain:dom,days:7},confirm:'Adicionar 7 dias de trial?'},'suspend':{url:'/admin/api/tenant/plan/suspend',body:{domain:dom},confirm:'SUSPENDER o acesso deste tenant?'},'reactivate':{url:'/admin/api/tenant/plan/reactivate',body:{domain:dom},confirm:'Reativar acesso?'}};
  var a=map[action];if(!a||!confirm(a.confirm))return;
  fetch(a.url,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(a.body)}).then(function(r){return r.json().then(function(j){return{ok:r.ok,j:j};});}).then(function(res){res.ok?(toast('✓ '+action+' aplicado',true),carregarTudo()):toast('✗ '+(res.j.error||'falha'),false);}).catch(function(){toast('✗ erro de conexão',false);});
}
function planActBasic(domain){
  var dom=decodeURIComponent(domain);if(!confirm('Ativar plano Básico (pago) para '+dom+'?'))return;
  fetch('/admin/api/tenant/plan',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({domain:dom,plan:'basic',status:'active'})}).then(function(r){return r.json().then(function(j){return{ok:r.ok,j:j};});}).then(function(res){res.ok?(toast('✓ Básico ativado',true),carregarTudo()):toast('✗ '+(res.j.error||'falha'),false);}).catch(function(){toast('✗ erro de conexão',false);});
}

function setToolDomain(d){irPara('tools');document.getElementById('tool-domain').value=decodeURIComponent(d);}
function toolAction(path,method){var dom=document.getElementById('tool-domain').value.trim();if(!dom){toast('Informe o domínio primeiro',false);return;}runTool('/admin/api/tenant/'+path+'?domain='+encodeURIComponent(dom),method,'tool-output');}
function globalAction(path,method){var target=path==='debug'?'diag-output':'tool-output';runTool('/admin/api/'+path,method,target);}
function runTool(url,method,targetId){var out=document.getElementById(targetId);out.style.display='block';out.textContent='Executando '+method+' '+url+' …';fetch(url,{method:method}).then(function(r){return r.text();}).then(function(t){try{out.textContent=JSON.stringify(JSON.parse(t),null,2);}catch(e){out.textContent=t;}toast('✓ executado',true);}).catch(function(e){out.textContent='Erro: '+e;toast('✗ falha',false);});}

// ── CUPONS ──
function atualizarDicaCupom(){
  var k=document.getElementById('cp-kind').value;
  var lbl=document.getElementById('cp-value-label');
  lbl.textContent = k==='percent'?'Valor (%)' : k==='amount'?'Valor (R$)' : 'Dias de teste';
}
function carregarCupons(){
  fetch('/admin/api/coupons').then(function(r){return r.json();}).then(function(d){
    var b=document.getElementById('cupons-body'); var list=d.coupons||[];
    if(!list.length){b.innerHTML='<tr><td colspan="8" class="empty">Nenhum cupom criado.</td></tr>';return;}
    b.innerHTML=list.map(function(c){
      var tipo={percent:'Desconto %',amount:'Desconto R$',trial_days:'Dias de teste'}[c.kind]||c.kind;
      var val=c.kind==='percent'?(c.value+'%'):c.kind==='amount'?fmtBRL(c.value):(c.value+' dias');
      var usos=(c.used_count||0)+(c.max_uses>0?(' / '+c.max_uses):' / ∞');
      var exp=c.expires_at?fmtDate(c.expires_at):'—';
      var esgotado=c.max_uses>0&&c.used_count>=c.max_uses;
      var venceu=c.expires_at&&new Date(c.expires_at)<new Date();
      var st=!c.active?'<span class="badge b-suspended">inativo</span>':
             esgotado?'<span class="badge b-expired">esgotado</span>':
             venceu?'<span class="badge b-expired">expirado</span>':
             '<span class="badge b-active">ativo</span>';
      return '<tr><td class="mono domain">'+c.code+'</td><td><span class="badge b-basic">'+tipo+'</span></td>'+
        '<td><b>'+val+'</b></td><td class="meta">'+(c.plan_code||'todos')+'</td><td>'+usos+'</td>'+
        '<td class="meta">'+exp+'</td><td>'+st+'</td>'+
        '<td style="text-align:right"><button class="btn" onclick="toggleCupom(\''+c.code+'\','+(!c.active)+')">'+(c.active?'Desativar':'Ativar')+'</button> '+
        '<button class="btn btn-danger" onclick="excluirCupom(\''+c.code+'\')">Excluir</button></td></tr>';
    }).join('');
  }).catch(function(){document.getElementById('cupons-body').innerHTML='<tr><td colspan="8" class="empty">Falha ao carregar.</td></tr>';});
}
function salvarCupom(){
  var kind=document.getElementById('cp-kind').value;
  var raw=parseFloat(document.getElementById('cp-value').value||'0');
  // amount vem em reais na UI -> centavos no backend
  var value=kind==='amount'?Math.round(raw*100):Math.round(raw);
  var body={
    code:document.getElementById('cp-code').value.trim().toUpperCase(),
    description:document.getElementById('cp-desc').value.trim(),
    kind:kind, value:value,
    plan_code:document.getElementById('cp-plan').value.trim().toLowerCase(),
    max_uses:parseInt(document.getElementById('cp-max').value||'0',10),
    active:document.getElementById('cp-active').checked,
    expires_at:document.getElementById('cp-exp').value||''
  };
  if(!body.code){toast('Informe o código',false);return;}
  fetch('/admin/api/coupons',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)})
    .then(function(r){return r.json().then(function(j){return{ok:r.ok,j:j};});})
    .then(function(res){
      if(res.ok){toast('✓ cupom salvo',true);document.getElementById('cp-code').value='';document.getElementById('cp-desc').value='';carregarCupons();}
      else toast('✗ '+(res.j.error||'falha'),false);
    }).catch(function(){toast('✗ erro de conexão',false);});
}
function toggleCupom(code,active){
  // Recarrega o cupom e regrava com o novo status (upsert por code).
  var c=null;
  fetch('/admin/api/coupons').then(function(r){return r.json();}).then(function(d){
    (d.coupons||[]).forEach(function(x){if(x.code===code)c=x;});
    if(!c){toast('cupom não encontrado',false);return;}
    var body={code:c.code,description:c.description,kind:c.kind,value:c.value,
      plan_code:c.plan_code,max_uses:c.max_uses,active:active,
      expires_at:c.expires_at?c.expires_at.slice(0,10):''};
    return fetch('/admin/api/coupons',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});
  }).then(function(){toast('✓ atualizado',true);carregarCupons();})
    .catch(function(){toast('✗ falha',false);});
}
function excluirCupom(code){
  if(!confirm('Excluir o cupom '+code+'?'))return;
  fetch('/admin/api/coupons/delete',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({code:code})})
    .then(function(){toast('✓ excluído',true);carregarCupons();}).catch(function(){toast('✗ falha',false);});
}

// ── GATEWAY (Itaú — somente leitura + teste) ──
function carregarGateway(){
  fetch('/admin/api/itau-status').then(function(r){return r.json();}).then(function(d){
    document.getElementById('itau-clientid').value=d.client_id||'(não configurado)';
    document.getElementById('itau-chavepix').value=d.chave_pix||'(não configurada)';
    document.getElementById('itau-env').value=d.environment||'sandbox';
    var cert='';
    if(d.cert_exists&&d.key_exists){cert='✅ certificado + chave presentes';}
    else if(d.cert_exists){cert='⚠️ .crt presente, .key faltando';}
    else{cert='❌ não encontrado ('+(d.cert_path||'?')+')';}
    document.getElementById('itau-cert').value=cert;

    // badge de status agregado
    var badges=[];
    badges.push(pill(d.pix_configured,'PIX'));
    badges.push(pill(d.boleto_configured,'Boleto'));
    badges.push(pill(d.cert_exists&&d.key_exists,'Certificado'));
    document.getElementById('itau-status').innerHTML=badges.join(' ');

    // webhook URL — cadastrar SEM /pix
    var pb=document.getElementById('bc-postback');
    if(pb){pb.value=location.origin+'/billing/itau';}
  }).catch(function(){toast('Falha ao carregar status Itaú',false);});
}
function pill(ok,label){
  var c=ok?'rgba(37,211,102,.15)':'rgba(248,113,113,.12)';
  var b=ok?'rgba(37,211,102,.4)':'rgba(248,113,113,.3)';
  var t=ok?'var(--green)':'#fca5a5';
  return '<span style="display:inline-block;padding:3px 10px;border-radius:99px;font-size:.74em;font-weight:600;background:'+c+';border:1px solid '+b+';color:'+t+'">'+(ok?'✓ ':'✗ ')+label+'</span>';
}
function testarGateway(metodo){
  metodo=metodo||'pix';
  var box=document.getElementById('gateway-test');
  var acao=metodo==='boleto'?'validando boleto':'gerando cobrança de R$ 1,00';
  box.innerHTML='<div style="color:var(--muted);font-size:.85em">Testando '+metodo.toUpperCase()+' ('+acao+')…</div>';
  fetch('/admin/api/itau-test?method='+metodo,{method:'POST'})
    .then(function(r){return r.json();})
    .then(function(d){
      if(d.ok){
        box.innerHTML='<div style="color:var(--green);font-size:.85em;padding:12px;background:rgba(37,211,102,.08);border-radius:10px;border:1px solid rgba(37,211,102,.25)">'+
          '<b>✅ '+metodo.toUpperCase()+' OK</b> — o Itaú aceitou a requisição.'+
          (d.hint?'<div style="color:var(--muted);margin-top:6px;line-height:1.5">'+d.hint+'</div>':'')+
          (d.copy_paste?'<div style="color:var(--dim);margin-top:6px;font-family:monospace;word-break:break-all">'+d.copy_paste.replace(/</g,'&lt;')+'</div>':'')+
          '<div style="color:var(--dim);margin-top:6px">ambiente '+(d.environment||'?')+'</div></div>';
        return;
      }
      box.innerHTML='<div style="font-size:.82em;padding:12px;background:rgba(248,113,113,.08);border-radius:10px;border:1px solid rgba(248,113,113,.25)">'+
        '<div style="color:#fca5a5;font-weight:700">❌ '+metodo.toUpperCase()+' falhou</div>'+
        (d.message?'<div style="color:#fca5a5;margin-top:4px;font-family:monospace;word-break:break-all">'+d.message.replace(/</g,'&lt;')+'</div>':'')+
        '<div style="color:var(--muted);margin-top:8px;line-height:1.5">'+(d.hint||'')+'</div>'+
      '</div>';
    })
    .catch(function(){ box.innerHTML='<div style="color:#fca5a5;font-size:.85em">Falha de conexão no teste.</div>'; });
}

// ── PLAN DEFS (construtor de planos) ──
var PLANDEFS=[];
function carregarPlanDefs(){
  fetch('/admin/api/plan-defs').then(function(r){return r.json();}).then(function(d){
    PLANDEFS=d.plans||[]; renderPlanDefs();
  }).catch(function(){document.getElementById('plandefs-list').innerHTML='<div class="empty">Falha ao carregar.</div>';});
}
function featChip(on,label){return '<span class="badge '+(on?'b-active':'b-none')+'" style="margin:2px 3px 0 0">'+(on?'✓ ':'✕ ')+label+'</span>';}
function renderPlanDefs(){
  var box=document.getElementById('plandefs-list');
  if(!PLANDEFS.length){box.innerHTML='<div class="empty">Nenhum plano. Clique em "Novo plano".</div>';return;}
  box.innerHTML=PLANDEFS.map(function(p){
    var isTrial=p.is_trial_default;
    var st=isTrial?'<span class="badge b-trial">plano de teste</span>':
           (p.active?'<span class="badge b-active">à venda</span>':'<span class="badge b-suspended">não listado</span>');
    var preco=isTrial
      ? '<div style="font-size:1.5em;font-weight:800;color:var(--amber);margin:6px 0">'+(p.trial_days||0)+'<span style="font-size:.5em;color:var(--dim);font-weight:500"> dias grátis</span></div>'
      : '<div style="font-size:1.5em;font-weight:800;color:var(--green);margin:6px 0">'+fmtBRL(p.price_cents)+'<span style="font-size:.5em;color:var(--dim);font-weight:500"> /mês</span></div>';
    return '<div class="toolcard"'+(isTrial?' style="border-color:rgba(251,191,36,.4);background:linear-gradient(160deg,rgba(251,191,36,.05),transparent)"':'')+'>'+
      '<div style="display:flex;justify-content:space-between;align-items:flex-start;gap:8px;margin-bottom:6px">'+
        '<div><div style="font-size:1.05em;font-weight:800;color:#f1f5f9">'+(isTrial?'⏳ ':'')+p.name+'</div>'+
        '<div class="mono meta">'+p.code+'</div></div>'+st+'</div>'+
      preco+
      '<div class="meta" style="margin-bottom:10px">Até <b>'+p.max_sessions+'</b> sessão(ões) · '+(p.is_pro?'rótulo Pro':'rótulo Básico')+
        (isTrial?' · <span style="color:var(--amber)">concedido automaticamente no install</span>':'')+'</div>'+
      '<div style="margin-bottom:12px">'+featChip(p.feat_templates,'Templates')+featChip(p.feat_automations,'Automações')+featChip(p.feat_sms,'SMS')+featChip(p.feat_reports,'Relatórios')+'</div>'+
      '<div class="row"><button class="btn" onclick="editarPlano(\''+p.code+'\')">Editar</button>'+
        '<button class="btn btn-danger" onclick="excluirPlano(\''+p.code+'\')">Excluir</button></div>'+
    '</div>';
  }).join('');
}
function novoPlano(){ abrirPlanoModal(null); }
function editarPlano(code){ var p=PLANDEFS.filter(function(x){return x.code===code;})[0]; abrirPlanoModal(p); }
function abrirPlanoModal(p){
  var isNew=!p; p=p||{code:'',name:'',description:'',price_cents:0,max_sessions:1,feat_templates:false,feat_automations:false,feat_sms:false,feat_reports:false,is_pro:false,active:true,sort_order:99,trial_days:0,is_trial_default:false,accept_boleto:true,accept_pix:true};
  var ov=document.createElement('div');
  ov.id='plano-modal';
  ov.style.cssText='position:fixed;inset:0;background:rgba(2,6,23,.8);backdrop-filter:blur(6px);z-index:99999;display:flex;align-items:center;justify-content:center;padding:20px;overflow:auto';
  function chk(id,on,lbl){return '<label style="display:flex;align-items:center;gap:8px;font-size:.86em;color:#cbd5e1;padding:6px 0;cursor:pointer"><input type="checkbox" id="'+id+'" '+(on?'checked':'')+' style="width:16px;height:16px">'+lbl+'</label>';}
  ov.innerHTML='<div style="max-width:460px;width:100%;background:linear-gradient(160deg,#0f172a,#1e293b);border:1px solid var(--border);border-radius:18px;padding:26px">'+
    '<div style="font-size:1.15em;font-weight:800;margin-bottom:16px">'+(isNew?'Novo plano':'Editar plano')+'</div>'+
    '<label style="font-size:.72em;color:var(--dim)">Código (id) '+(isNew?'':'— fixo')+'</label>'+
    '<input class="dominput" id="pd-code" value="'+p.code+'" '+(isNew?'':'disabled')+' placeholder="ex: starter" style="margin:2px 0 10px">'+
    '<label style="font-size:.72em;color:var(--dim)">Nome</label>'+
    '<input class="dominput" id="pd-name" value="'+(p.name||'')+'" placeholder="ex: Starter" style="margin:2px 0 10px">'+
    '<label style="font-size:.72em;color:var(--dim)">Descrição</label>'+
    '<input class="dominput" id="pd-desc" value="'+(p.description||'')+'" placeholder="uma frase" style="margin:2px 0 10px">'+
    '<div style="display:flex;gap:10px">'+
      '<div style="flex:1"><label style="font-size:.72em;color:var(--dim)">Preço (R$)</label><input class="dominput" id="pd-price" type="number" step="0.01" value="'+((p.price_cents||0)/100).toFixed(2)+'" style="margin:2px 0 10px"></div>'+
      '<div style="flex:1"><label style="font-size:.72em;color:var(--dim)">Máx. sessões</label><input class="dominput" id="pd-max" type="number" min="1" value="'+(p.max_sessions||1)+'" style="margin:2px 0 10px"></div>'+
    '</div>'+
    '<div style="font-size:.72em;color:var(--dim);margin:6px 0 2px;text-transform:uppercase;letter-spacing:.05em">Features liberadas</div>'+
    chk('pd-tpl',p.feat_templates,'Templates + Cloud API Meta')+
    chk('pd-aut',p.feat_automations,'Automações (robôs BizProc)')+
    chk('pd-sms',p.feat_sms,'Campanhas SMS')+
    chk('pd-rep',p.feat_reports,'Relatórios + histórico longo')+
    '<div style="height:1px;background:var(--border);margin:10px 0"></div>'+
    '<div style="font-size:.72em;color:var(--dim);margin:2px 0 6px;text-transform:uppercase;letter-spacing:.05em">Período de teste</div>'+
    chk('pd-trialdef',p.is_trial_default,'Este é o plano de teste (novos clientes recebem)')+
    '<label style="font-size:.72em;color:var(--dim)">Duração do teste (dias)</label>'+
    '<input class="dominput" id="pd-trialdays" type="number" min="0" value="'+(p.trial_days||0)+'" style="margin:2px 0 4px">'+
    '<div style="font-size:.72em;color:var(--dim);margin-bottom:8px">Ao marcar acima, todo cliente novo entra neste plano por esses dias. Só um plano pode ser o de teste.</div>'+
    '<div style="height:1px;background:var(--border);margin:10px 0"></div>'+
    '<div style="font-size:.72em;color:var(--dim);margin:2px 0 6px;text-transform:uppercase;letter-spacing:.05em">Formas de pagamento</div>'+
    chk('pd-boleto',p.accept_boleto!==false,'Aceita Boleto')+
    chk('pd-pix',p.accept_pix!==false,'Aceita PIX')+
    '<div style="font-size:.72em;color:var(--dim);margin-bottom:8px">Controla quais métodos aparecem no checkout deste plano. O PIX só é oferecido se o gateway PIX estiver configurado.</div>'+
    '<div style="height:1px;background:var(--border);margin:10px 0"></div>'+
    chk('pd-pro',p.is_pro,'Marcar como plano "Pro" (rótulo)')+
    chk('pd-active',p.active,'Disponível para assinatura (aparece nos cards do cliente)')+
    '<div class="row" style="margin-top:18px;justify-content:flex-end">'+
      '<button class="btn" onclick="fecharPlanoModal()">Cancelar</button>'+
      '<button class="btn btn-primary" onclick="salvarPlano('+(isNew?'true':'false')+')">Salvar</button>'+
    '</div></div>';
  document.body.appendChild(ov);
}
function fecharPlanoModal(){var m=document.getElementById('plano-modal');if(m)m.remove();}
function salvarPlano(isNew){
  var g=function(id){return document.getElementById(id);};
  var code=g('pd-code').value.trim().toLowerCase();
  // Preserva a ORDEM: se o plano ja' existe, reusa o sort_order dele em vez de
  // forcar 99 (que embaralhava a lista a cada save). Plano novo vai pro fim.
  var existente=PLANDEFS.filter(function(x){return x.code===code;})[0];
  var ordem=(existente&&typeof existente.sort_order==='number')?existente.sort_order:99;
  var body={
    code:code,
    name:g('pd-name').value.trim(),
    description:g('pd-desc').value.trim(),
    price_cents:Math.round(parseFloat(g('pd-price').value||'0')*100),
    max_sessions:parseInt(g('pd-max').value||'1',10),
    feat_templates:g('pd-tpl').checked,
    feat_automations:g('pd-aut').checked,
    feat_sms:g('pd-sms').checked,
    feat_reports:g('pd-rep').checked,
    is_pro:g('pd-pro').checked,
    active:g('pd-active').checked,
    trial_days:parseInt(g('pd-trialdays').value||'0',10),
    is_trial_default:g('pd-trialdef').checked,
    accept_boleto:g('pd-boleto').checked,
    accept_pix:g('pd-pix').checked,
    sort_order:ordem
  };
  if(!body.code||!body.name){toast('Código e nome obrigatórios',false);return;}
  fetch('/admin/api/plan-defs',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)})
    .then(function(r){return r.json().then(function(j){return{ok:r.ok,j:j};});})
    .then(function(res){res.ok?(toast('✓ plano salvo',true),fecharPlanoModal(),carregarPlanDefs()):toast('✗ '+(res.j.error||'falha'),false);})
    .catch(function(){toast('✗ erro de conexão',false);});
}
function excluirPlano(code){
  if(!confirm('Excluir o plano "'+code+'"? (bloqueado se houver tenants usando)'))return;
  fetch('/admin/api/plan-defs/delete',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({code:code})})
    .then(function(r){return r.json().then(function(j){return{ok:r.ok,j:j};});})
    .then(function(res){res.ok?(toast('✓ excluído',true),carregarPlanDefs()):toast('✗ '+(res.j.error||'falha'),false);})
    .catch(function(){toast('✗ falha',false);});
}

// ── USAGE ──
function carregarUsage(){
  fetch('/admin/api/usage').then(function(r){return r.json();}).then(function(d){
    var b=document.getElementById('usage-body'); var list=d.usage||[];
    if(!list.length){b.innerHTML='<tr><td colspan="7" class="empty">Sem dados de consumo ainda.</td></tr>';return;}
    b.innerHTML=list.map(function(u){
      var sess=(u.sessions_qr||0)+' QR'+(u.sessions_cloud?' · '+u.sessions_cloud+' Cloud':'');
      return '<tr><td class="domain">'+u.domain+'</td><td>'+(u.msgs_24h||0)+'</td><td>'+(u.msgs_7d||0)+'</td><td><b>'+(u.msgs_30d||0)+'</b></td><td>'+sess+'</td><td>'+(u.charges_paid||0)+'</td><td>'+fmtBRL(u.revenue_cents)+'</td></tr>';
    }).join('');
  }).catch(function(){document.getElementById('usage-body').innerHTML='<tr><td colspan="7" class="empty">Falha ao carregar.</td></tr>';});
}

// ── SYSTEM ──
function fmtUptime(s){s=s||0;var d=Math.floor(s/86400),h=Math.floor(s%86400/3600),m=Math.floor(s%3600/60);return (d?d+'d ':'')+(h?h+'h ':'')+m+'m';}
function carregarSystem(){
  fetch('/admin/api/system').then(function(r){return r.json();}).then(function(s){
    function k(cls,label,val,foot){return '<div class="kpi '+cls+'"><div class="label">'+label+'</div><div class="value">'+val+'</div><div class="foot">'+(foot||'')+'</div></div>';}
    document.getElementById('sys-kpis').innerHTML=
      k('blue','Goroutines',s.num_goroutine,s.num_cpu+' CPUs')+
      k('purple','Heap (RAM)',(s.heap_alloc_mb||0).toFixed(1)+' MB','de '+(s.heap_sys_mb||0).toFixed(0)+' MB reservado')+
      k('green','Redis',s.redis_ok?s.redis_ping_ms+' ms':'offline',s.redis_ok?'conectado':'sem conexão')+
      k('amber','Filas',(s.queue_inbound||0)+(s.queue_outbound||0),'in '+(s.queue_inbound||0)+' · out '+(s.queue_outbound||0)+' · dead '+(s.queue_dead||0))+
      k('blue','Conexões DB',(s.db_conns_used||0)+'/'+(s.db_conns_max||0),(s.db_conns_idle||0)+' ociosas')+
      k('green','Uptime',fmtUptime(s.uptime_seconds),s.go_version||'');
    var rows=[['Go',s.go_version],['CPUs',s.num_cpu],['Goroutines',s.num_goroutine],
      ['Heap alocado',(s.heap_alloc_mb||0).toFixed(2)+' MB'],['Heap reservado',(s.heap_sys_mb||0).toFixed(2)+' MB'],
      ['Stack',(s.stack_sys_mb||0).toFixed(2)+' MB'],['Total alocado (acum.)',(s.total_alloc_mb||0).toFixed(0)+' MB'],
      ['Ciclos GC',s.num_gc],['Sessões WA vivas',s.wa_sessions_live],
      ['DB conns (uso/idle/max)',(s.db_conns_used||0)+' / '+(s.db_conns_idle||0)+' / '+(s.db_conns_max||0)],
      ['Redis ping',s.redis_ok?s.redis_ping_ms+' ms':'offline']];
    document.getElementById('sys-detail').innerHTML=rows.map(function(r){return '<tr><td class="meta" style="width:220px">'+r[0]+'</td><td class="mono">'+(r[1]==null?'—':r[1])+'</td></tr>';}).join('');
  }).catch(function(){document.getElementById('sys-kpis').innerHTML='<div class="empty">Falha ao carregar sistema.</div>';});
}

// ── LOGS (SSE) ──
var _logES=null,_logPaused=false,_logFilter='';
function iniciarLogs(){
  if(_logES)return;
  _logES=new EventSource('/admin/api/logs/stream');
  _logES.onmessage=function(ev){ if(_logPaused)return; appendLog(ev.data); };
  _logES.onerror=function(){ /* reconecta sozinho */ };
}
function pararLogs(){ if(_logES){_logES.close();_logES=null;} }
function appendLog(text){
  var v=document.getElementById('log-view'); if(!v)return;
  if(_logFilter && text.toLowerCase().indexOf(_logFilter)<0)return;
  var lvl='info'; try{var o=JSON.parse(text); lvl=o.level||'info'; text=(o.ts?new Date(o.ts*1000).toLocaleTimeString('pt-BR'):'')+' ['+lvl.toUpperCase()+'] '+(o.msg||'')+(o.error?' — '+o.error:'')+(o.domain?' {'+o.domain+'}':'');}catch(e){}
  var color=lvl==='error'||lvl==='fatal'?'#fca5a5':lvl==='warn'?'#fcd34d':'#a7f3d0';
  var line=document.createElement('div'); line.style.color=color; line.textContent=text;
  v.appendChild(line);
  while(v.childNodes.length>600)v.removeChild(v.firstChild);
  v.scrollTop=v.scrollHeight;
}
function toggleLogPause(){_logPaused=!_logPaused;document.getElementById('log-pause').textContent=_logPaused?'▶ Retomar':'⏸ Pausar';}
function filtrarLogs(){_logFilter=(document.getElementById('log-filter').value||'').toLowerCase();}

// ── USERS ──
function carregarUsers(){
  fetch('/admin/api/users').then(function(r){return r.json();}).then(function(d){
    var b=document.getElementById('users-body'); var list=d.users||[];
    var root='<tr><td class="domain">'+(d.root_user||'admin')+' <span class="badge b-pro">ROOT</span></td><td>—</td><td>superadmin</td><td><span class="badge b-active">env</span></td><td class="meta">—</td><td style="text-align:right" class="meta">login raiz</td></tr>';
    var rows=list.map(function(u){
      var st=u.active?'<span class="badge b-active">ativo</span>':'<span class="badge b-suspended">inativo</span>';
      var role=u.role==='superadmin'?'<span class="badge b-pro">superadmin</span>':'<span class="badge b-basic">suporte</span>';
      return '<tr><td class="domain">'+u.email+'</td><td>'+(u.name||'—')+'</td><td>'+role+'</td><td>'+st+'</td><td class="meta">'+(u.last_login_at?fmtDate(u.last_login_at):'nunca')+'</td>'+
        '<td style="text-align:right"><button class="btn" onclick="toggleUser(\''+u.id+'\','+(!u.active)+')">'+(u.active?'Desativar':'Ativar')+'</button> <button class="btn btn-danger" onclick="delUser(\''+u.id+'\',\''+u.email+'\')">Excluir</button></td></tr>';
    }).join('');
    b.innerHTML=root+(rows||'');
  }).catch(function(){document.getElementById('users-body').innerHTML='<tr><td colspan="6" class="empty">Falha ao carregar.</td></tr>';});
}
function criarUser(){
  var body={email:document.getElementById('u-email').value,name:document.getElementById('u-name').value,password:document.getElementById('u-pass').value,role:document.getElementById('u-role').value};
  fetch('/admin/api/users',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)})
    .then(function(r){return r.json().then(function(j){return{ok:r.ok,j:j};});})
    .then(function(res){if(res.ok){toast('✓ usuário criado',true);document.getElementById('u-email').value='';document.getElementById('u-name').value='';document.getElementById('u-pass').value='';carregarUsers();}else toast('✗ '+(res.j.error||'falha'),false);})
    .catch(function(){toast('✗ erro de conexão',false);});
}
function toggleUser(id,active){fetch('/admin/api/users/toggle',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({id:id,active:active})}).then(function(){toast('✓ atualizado',true);carregarUsers();}).catch(function(){toast('✗ falha',false);});}
function delUser(id,email){if(!confirm('Excluir o admin '+email+'?'))return;fetch('/admin/api/users/delete',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({id:id})}).then(function(){toast('✓ excluído',true);carregarUsers();}).catch(function(){toast('✗ falha',false);});}

// ── IPS ──
function carregarIPs(){
  fetch('/admin/api/blocked-ips').then(function(r){return r.json();}).then(function(d){
    var b=document.getElementById('ips-body'); var list=d.blocked||[];
    if(!list.length){b.innerHTML='<tr><td colspan="7" class="empty">Nenhum IP bloqueado.</td></tr>';return;}
    b.innerHTML=list.map(function(x){
      var st=x.active?'<span class="badge b-expired">bloqueado</span>':'<span class="badge b-active">liberado</span>';
      var rs=x.reason==='brute_force'?'<span class="badge b-trial">brute-force</span>':'<span class="badge b-basic">manual</span>';
      var act=x.active?'<button class="btn btn-primary" onclick="unblockIP(\''+x.ip+'\')">Liberar</button>':'<button class="btn btn-danger" onclick="reblockIP(\''+x.ip+'\')">Rebloquear</button>';
      return '<tr><td class="mono domain">'+x.ip+'</td><td>'+rs+'</td><td>'+(x.fail_count||0)+'</td><td>'+st+'</td><td class="meta">'+fmtDate(x.updated_at)+'</td><td class="meta">'+(x.note||'—')+'</td><td style="text-align:right">'+act+'</td></tr>';
    }).join('');
  }).catch(function(){document.getElementById('ips-body').innerHTML='<tr><td colspan="7" class="empty">Falha ao carregar.</td></tr>';});
}
function bloquearIP(){var ip=document.getElementById('ip-addr').value.trim();if(!ip){toast('Informe o IP',false);return;}fetch('/admin/api/blocked-ips/block',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({ip:ip,note:document.getElementById('ip-note').value})}).then(function(){toast('✓ IP bloqueado',true);document.getElementById('ip-addr').value='';document.getElementById('ip-note').value='';carregarIPs();}).catch(function(){toast('✗ falha',false);});}
function unblockIP(ip){fetch('/admin/api/blocked-ips/unblock',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({ip:ip})}).then(function(){toast('✓ IP liberado',true);carregarIPs();}).catch(function(){toast('✗ falha',false);});}
function reblockIP(ip){fetch('/admin/api/blocked-ips/block',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({ip:ip,note:'rebloqueado manualmente'})}).then(function(){toast('✓ rebloqueado',true);carregarIPs();}).catch(function(){toast('✗ falha',false);});}

// ── AUDIT ──
function carregarAudit(){
  fetch('/admin/api/audit').then(function(r){return r.json();}).then(function(d){
    var b=document.getElementById('audit-body'); var list=d.entries||[];
    if(!list.length){b.innerHTML='<tr><td colspan="6" class="empty">Sem registros ainda.</td></tr>';return;}
    b.innerHTML=list.map(function(e){return '<tr><td class="meta">'+fmtDate(e.created_at)+'</td><td class="domain">'+(e.actor||'—')+'</td><td><span class="badge b-basic">'+(e.action||'')+'</span></td><td class="meta">'+(e.target||'—')+'</td><td class="meta">'+(e.detail||'')+'</td><td class="mono meta">'+(e.ip||'')+'</td></tr>';}).join('');
  }).catch(function(){document.getElementById('audit-body').innerHTML='<tr><td colspan="6" class="empty">Falha ao carregar.</td></tr>';});
}

function carregarTudo(){
  // Planos primeiro: planTag() usa PLANDEFS pra rotular os badges em
  // todas as abas (tenants, pagamentos, consumo).
  fetch('/admin/api/plan-defs').then(function(r){return r.json();}).then(function(d){
    PLANDEFS=d.plans||[];
    if(document.getElementById('page-plandefs').classList.contains('active'))renderPlanDefs();
  }).catch(function(){});
  fetch('/admin/api/metrics').then(function(r){return r.json();}).then(renderKpis).catch(function(){document.getElementById('kpis').innerHTML='<div class="empty">Falha ao carregar métricas.</div>';});
  fetch('/admin/api/tenants').then(function(r){return r.json();}).then(function(d){TENANTS=d.tenants||[];renderTenants();}).catch(function(){document.getElementById('tenants-body').innerHTML='<tr><td colspan="7" class="empty">Falha ao carregar tenants.</td></tr>';});
  fetch('/admin/api/billing/charges').then(function(r){return r.json();}).then(function(d){renderBilling(d.charges||[]);renderRecentCharges(d.charges||[]);}).catch(function(){});
}
carregarTudo();
setInterval(carregarTudo,60000);
</script>
</body>
</html>`
