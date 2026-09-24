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
  .content{padding:26px 34px;max-width:1600px;width:100%;margin:0 auto}
  @media(min-width:1800px){ .content{max-width:1760px} }

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
  /* Alerta operacional: o que exige alguem agir agora. */
  .alerta{background:rgba(239,68,68,.10);border:1px solid rgba(239,68,68,.35);color:#fca5a5;
          border-radius:11px;padding:11px 14px;margin-bottom:9px;font-size:.85em;font-weight:600}
  .dir-in{color:#34d399;font-weight:700}
  .dir-out{color:#60a5fa;font-weight:700}
  .st-ok{color:var(--muted)}
  .st-fail{color:#f87171;font-weight:700}

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
  /* position:fixed, NAO absolute. Um elemento absolute e' recortado por
     QUALQUER ancestral com overflow != visible — e este menu tem dois:
     .tablewrap (overflow:hidden) e .tablescroll (overflow-x:auto, que pelo
     spec torna o eixo Y 'auto' tambem). Por isso o menu sumia dentro da
     tabela e z-index nao adiantava: z-index ordena o empilhamento, nao
     impede recorte. Com fixed o menu sai do fluxo de recorte; as
     coordenadas sao calculadas na abertura por posicionaMenu(). */
  .menu{position:fixed;left:0;top:0;background:var(--panel);border:1px solid var(--border);border-radius:11px;min-width:216px;box-shadow:0 16px 44px rgba(0,0,0,.55);z-index:9998;overflow:hidden;display:none}
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

  /* barra de controles da preview */
  .preview-controls{display:flex;gap:14px;align-items:flex-end;flex-wrap:wrap}
  .preview-field{display:flex;flex-direction:column;gap:6px;min-width:0}
  .preview-field label{font-size:.68em;font-weight:600;color:var(--muted);text-transform:uppercase;letter-spacing:.06em}
  .preview-field .dominput{margin:0;height:40px;box-sizing:border-box}
  .preview-field select.dominput{padding:.55em .7em;background:var(--panel2);border:1px solid var(--border);border-radius:10px;color:var(--txt);font-size:.85em;width:100%;height:40px;cursor:pointer;appearance:none;-webkit-appearance:none;background-image:url("data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 24 24' fill='none' stroke='%2394a3b8' stroke-width='2'><path d='M6 9l6 6 6-6'/></svg>");background-repeat:no-repeat;background-position:right .7em center;padding-right:2em}
  .preview-actions{display:flex;gap:8px;flex-wrap:wrap}
  .preview-actions .btn{height:40px;white-space:nowrap}
  @media(max-width:720px){.preview-field{flex:1 1 100%!important}.preview-actions{width:100%}.preview-actions .btn{flex:1}}

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
    <div class="sb-item" data-page="health" onclick="irPara('health')"><span class="ic">💚</span> Saúde do cliente</div>
    <div class="sb-group">Contratos</div>
    <div class="sb-item" data-page="licencas" onclick="irPara('licencas')"><span class="ic">📄</span> Licenças <span class="badge-count" id="cnt-vencendo">0</span></div>
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
      <div id="alertas-op"></div>
      <div class="section-title">💬 Últimas mensagens
        <span class="meta" style="font-weight:400;text-transform:none;margin-left:8px">o que está passando agora, em todos os clientes</span>
      </div>
      <div class="tablewrap"><div class="tablescroll" style="max-height:360px">
        <table>
          <thead><tr><th>Quando</th><th>Cliente</th><th></th><th>Número</th><th>Mensagem</th><th>Situação</th></tr></thead>
          <tbody id="msgs-recentes"><tr><td colspan="6" class="loading">Carregando…</td></tr></tbody>
        </table>
      </div></div>
      <div class="section-title">⏰ Renovações a vencer</div>
      <div class="tablewrap"><div class="tablescroll">
        <table>
          <thead><tr><th>Cliente</th><th>Vence em</th><th>Situação</th><th style="text-align:right">Ações</th></tr></thead>
          <tbody id="renovacoes"><tr><td colspan="4" class="loading">Carregando…</td></tr></tbody>
        </table>
      </div></div>
    </div>

    <!-- TENANTS -->
    <div class="page" id="page-tenants">
      <div class="toolbar">
        <input class="search" id="tenant-search" placeholder="🔎 Buscar por domínio…" oninput="renderTenants()">
        <select class="filter" id="tenant-filter" onchange="renderTenants()">
          <option value="">Todas as situações</option>
          <option value="vencendo">Vencendo (7 dias)</option>
          <option value="vencida">Vencida</option>
          <option value="sem_licenca">Sem licença</option>
        </select>
        <span id="tenant-count" style="margin-left:auto;font-size:.8em;color:var(--dim);align-self:center"></span>
      </div>
      <div class="tablewrap"><div class="tablescroll">
        <table>
          <thead><tr><th>Cliente</th><th>Contrato</th><th>Licença</th><th>Conexões</th><th>Msgs 24h</th><th>Token</th><th style="text-align:right">Ações</th></tr></thead>
          <tbody id="tenants-body"><tr><td colspan="7" class="loading">Carregando clientes…</td></tr></tbody>
        </table>
      </div></div>
    </div>

    <!-- LICENCAS -->
    <div class="page" id="page-licencas">
      <div class="toolbar">
        <input class="search" id="lic-search" placeholder="Filtrar por domínio…" oninput="renderLicencas()">
        <select class="filter" id="lic-filter" onchange="renderLicencas()">
          <option value="">Todas</option>
          <option value="vencendo">Vencendo (7 dias)</option>
          <option value="vencida">Vencidas</option>
          <option value="sem_prazo">Sem prazo</option>
        </select>
      </div>
      <div class="tablewrap"><div class="tablescroll">
        <table>
          <thead><tr><th>Cliente</th><th>Números</th><th>Benefícios</th><th>Vigência</th><th>Situação</th><th style="text-align:right">Ações</th></tr></thead>
          <tbody id="licencas-body"><tr><td colspan="6" class="loading">Carregando licenças…</td></tr></tbody>
        </table>
      </div></div>
    </div>

    <!-- SAUDE DO CLIENTE -->
    <div class="page" id="page-health">
      <div class="toolbar">
        <select class="filter" id="health-domain" style="flex:1" onchange="carregarHealth()"><option value="">— escolha um cliente —</option></select>
        <button class="btn" onclick="carregarHealth()">↻ Atualizar</button>
      </div>
      <div id="health-out"><div class="empty">Escolha um cliente para ver o estado do app dele.</div></div>
    </div>

    <div class="page" id="page-usage">
      <div class="section-title">📈 Consumo por tenant</div>
      <div class="tablewrap"><div class="tablescroll">
        <table>
          <thead><tr><th>Cliente</th><th>24h</th><th>7 dias</th><th>30 dias</th><th style="width:130px">Volume 30d</th><th>Números</th><th>Vigência</th></tr></thead>
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
            <select class="filter" id="u-role" style="width:100%"><option value="support">Suporte</option><option value="superadmin">Administrador</option></select></div>
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
        <p>A interface que o cliente enxerga dentro do Bitrix24. Escolha um cliente para ver os dados reais dele, ou deixe em <strong>Demonstração</strong> para conferir o programa sem expor dado de ninguém — útil para mostrar o produto e para testar mudança de layout.</p>
        <div class="preview-controls">
          <div class="preview-field" style="flex:0 0 220px">
            <label for="preview-screen">Tela</label>
            <select class="dominput" id="preview-screen" onchange="recarregarPreview()">
              <option value="/dashboard">Painel do cliente</option>
              <option value="/bitrix/crm/tab">Aba no CRM (contato/negócio)</option>
            </select>
          </div>
          <div class="preview-field" style="flex:1 1 280px">
            <label for="preview-domain">Cliente</label>
            <select class="dominput" id="preview-domain" onchange="recarregarPreview()">
              <option value="">— Demonstração (sem dados de cliente) —</option>
            </select>
          </div>
          <div class="preview-actions">
            <button class="btn btn-primary" onclick="recarregarPreview()">Carregar</button>
            <button class="btn" onclick="abrirPreviewNovaAba()">Abrir em nova aba ↗</button>
          </div>
        </div>
        <p id="preview-hint" style="margin:10px 0 0;font-size:.75em;color:var(--muted)"></p>
      </div>
      <div style="background:#0b1220;border:1px solid var(--border);border-radius:14px;overflow:hidden;height:74vh">
        <iframe id="preview-frame" src="about:blank" style="width:100%;height:100%;border:0;background:#0f172a" title="Preview UC Talk"></iframe>
      </div>
    </div>

    <!-- TOOLS -->
    <div class="page" id="page-tools">
      <div class="tools">
        <div class="toolcard">
          <h3>🔧 Reparo de um cliente</h3>
          <p>Escolha o cliente e a ação. Cada uma resolve um sintoma específico — se o sintoma não for esse, não use.</p>
          <select class="dominput" id="tool-domain">
            <option value="">— escolha um cliente —</option>
          </select>
          <div class="row" style="flex-direction:column;align-items:stretch;gap:10px;margin-top:4px">
            <div>
              <button class="btn" onclick="toolAction('bp-reregister','GET')">Re-registrar automações</button>
              <div class="meta" style="margin-top:4px">Quando: os robôs do BizProc sumiram dos fluxos do cliente, ou pararam de disparar.</div>
            </div>
            <div>
              <button class="btn" onclick="confirmarFerramenta('placements/force-unbind','Reinstalar as abas do UC Talk')">Reinstalar abas do app</button>
              <div class="meta" style="margin-top:4px">Quando: a aba UC Talk não aparece no contato/negócio do cliente. Desfaz e refaz o registro das abas.</div>
            </div>
          </div>
        </div>

        <div class="toolcard">
          <h3>🧹 Faxina global</h3>
          <p>Afeta todos os clientes, mas só remove lixo — nada de atendimento é perdido.</p>
          <div class="row" style="flex-direction:column;align-items:stretch;gap:10px">
            <div>
              <button class="btn" onclick="globalAction('cleanup/banned-sessions','POST')">Limpar sessões banidas</button>
              <div class="meta" style="margin-top:4px">Remove números que o WhatsApp baniu e que ficam tentando reconectar à toa.</div>
            </div>
            <div>
              <button class="btn" onclick="globalAction('cleanup/placeholder-portals','POST')">Limpar portais fantasma</button>
              <div class="meta" style="margin-top:4px">Remove registros de instalação que nunca se completaram.</div>
            </div>
          </div>
        </div>

        <div class="toolcard" style="border-color:rgba(239,68,68,.4)">
          <h3 style="color:#f87171">⚠️ Destrutivo</h3>
          <p>Esvaziar as filas <strong>descarta mensagens de clientes que ainda não foram entregues</strong> — inclusive as que estão esperando para ser reprocessadas. Elas não voltam.</p>
          <p class="meta">Antes disso: em <strong>Saúde do cliente</strong> existe <em>Reentregar mensagens</em>, que devolve a fila presa ao fluxo em vez de jogar fora. É quase sempre o que você quer.</p>
          <button class="btn" onclick="confirmarEsvaziarFilas()" style="border-color:rgba(239,68,68,.5);color:#fca5a5">Esvaziar filas</button>
        </div>
      </div>
      <pre id="tool-output" style="margin-top:16px;background:var(--panel2);border:1px solid var(--border);border-radius:12px;padding:16px;font-size:.78em;color:var(--muted);overflow:auto;max-height:420px;white-space:pre-wrap;display:none"></pre>
    </div>

    <!-- DIAG -->
  </div>
</div>

<div id="toast"></div>

<script>
var TENANTS=[];
var PAGES={
  overview:{title:'Visão geral',sub:'Métricas e saúde do sistema'},
  tenants:{title:'Tenants',sub:'Portais Bitrix24 que instalaram o app'},
  usage:{title:'Consumo',sub:'Uso de recursos por tenant'},
  health:{title:'Saúde do cliente',sub:'Estado real do app de um cliente, num lugar só'},
  licencas:{title:'Licenças',sub:'Benefícios contratados, vigência e pagamentos'},
  system:{title:'Sistema',sub:'Monitoramento do processo em tempo real'},
  logs:{title:'Logs ao vivo',sub:'Stream de logs direto do servidor'},
  users:{title:'Usuários admin',sub:'Gerenciar quem acessa o painel'},
  ips:{title:'IPs bloqueados',sub:'Controle de acesso por IP'},
  audit:{title:'Auditoria',sub:'Histórico de ações no painel'},
  preview:{title:'Preview do app',sub:'Como o cliente vê o UC Talk no Bitrix24'},
  tools:{title:'Ferramentas',sub:'Reparo pontual — cada ação resolve um sintoma'}
};

function toast(msg,ok){var t=document.getElementById('toast');t.textContent=msg;t.className=ok?'ok':'err';t.style.display='block';clearTimeout(window._tt);window._tt=setTimeout(function(){t.style.display='none';},3800);}
function irPara(p){
  document.querySelectorAll('.sb-item').forEach(function(t){t.classList.toggle('active',t.dataset.page===p);});
  document.querySelectorAll('.page').forEach(function(pg){pg.classList.remove('active');});
  document.getElementById('page-'+p).classList.add('active');
  if(PAGES[p]){document.getElementById('page-title').textContent=PAGES[p].title;document.getElementById('page-sub').textContent=PAGES[p].sub;}
  fecharSidebar();
  // Carrega dados sob demanda por secao.
  if(p==='licencas')carregarLicencas();
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
  var scrEl=document.getElementById('preview-screen');
  var scr=(scrEl&&scrEl.value)||'/dashboard';
  var dom=(document.getElementById('preview-domain').value||'').trim();
  var qs=['preview=1'];                 // marcador: estamos na preview do admin
  if(dom)qs.push('domain='+encodeURIComponent(dom));
  // CRM tab precisa de entity_type/entity_id pra montar a aba — usa um exemplo.
  if(scr==='/bitrix/crm/tab'){qs.push('entity_type=deal');qs.push('entity_id=1');}
  return scr+'?'+qs.join('&');
}
// Dicas por tela: algumas só fazem sentido com domínio de tenant.
var PREVIEW_HINTS={
  '/dashboard':'Painel que o cliente abre dentro do Bitrix24. Em Demonstração aparece vazio, sem dado de cliente nenhum.',
  '/bitrix/crm/tab':'Aba que aparece no Contato, Lead e Negócio. Escolha um cliente para carregar conversas de verdade.'
};
function atualizarPreviewHint(){
  var scrEl=document.getElementById('preview-screen');var h=document.getElementById('preview-hint');
  if(scrEl&&h)h.textContent=PREVIEW_HINTS[scrEl.value]||'';
}
// Shim do BX24: fora do iframe do Bitrix, o SDK remoto lança
// "Unable to initialize Bitrix24 JS library!" no console. Na preview isso e'
// so ruido — injetamos um BX24 falso ANTES do load pra silenciar. Same-origin
// (/dashboard e /admin no mesmo host) permite escrever no contentWindow.
function injetarBX24Shim(f){
  try{
    var w=f.contentWindow;if(!w)return;
    w.BX24={init:function(cb){try{cb&&cb();}catch(e){}},
      getDomain:function(){return '';},getAuth:function(){return null;},
      callMethod:function(m,p,cb){try{cb&&cb({error:function(){return 'preview';},data:function(){return null;}});}catch(e){}},
      resizeWindow:function(){},fitWindow:function(){},install:function(){},installFinish:function(){},
      isAdmin:function(){return false;},getLang:function(){return 'pt';}};
  }catch(e){/* cross-origin: ignora, nao ha' o que silenciar */}
}
function carregarPreviewFrame(){
  var f=document.getElementById('preview-frame');if(!f)return;
  atualizarPreviewHint();
  // injeta o shim assim que o documento novo existir, antes dos scripts rodarem
  f.onload=function(){injetarBX24Shim(f);};
  f.src=previewURL();
  // tentativa extra: alguns navegadores criam o contentWindow antes do onload
  setTimeout(function(){injetarBX24Shim(f);},0);
}
function abrirPreview(){
  var f=document.getElementById('preview-frame');
  if(f && (f.src==='about:blank' || f.src.indexOf('about:blank')>=0)){carregarPreviewFrame();}
  else{atualizarPreviewHint();}
}
function recarregarPreview(){carregarPreviewFrame();}
function abrirPreviewNovaAba(){window.open(previewURL(),'_blank');}
function abrirSidebar(){document.getElementById('sidebar').classList.add('open');document.getElementById('backdrop').classList.add('open');}
function fecharSidebar(){document.getElementById('sidebar').classList.remove('open');document.getElementById('backdrop').classList.remove('open');}

function fmtBRL(cents){return 'R$ '+((cents||0)/100).toFixed(2).replace('.',',');}
function fmtDate(s){if(!s)return '—';try{return new Date(s).toLocaleString('pt-BR',{day:'2-digit',month:'2-digit',year:'2-digit',hour:'2-digit',minute:'2-digit'});}catch(e){return s;}}
function licBadge(t){
  if(!t.licenca_configurada) return '<span class="badge b-none">Sem licença</span>';
  if(t.expirada) return '<span class="badge b-expired">Vencida</span>';
  if(typeof t.dias_restantes==='number'&&t.dias_restantes<=7) return '<span class="badge b-trial">Vence em '+t.dias_restantes+'d</span>';
  if(!t.valid_until) return '<span class="badge b-active">Sem prazo</span>';
  return '<span class="badge b-active">Em vigor</span>';
}
function featTags(t){
  var f=[];
  if(t.feat_cloud_api)   f.push('Cloud/Templates');
  if(t.feat_automations) f.push('Automações');
  if(t.feat_reports)     f.push('Relatórios');
  if(!f.length) return '<span class="meta">básico</span>';
  return f.map(function(x){return '<span class="badge b-pro">'+x+'</span>';}).join(' ');
}
function fmtDia(s){if(!s)return '—';try{return new Date(s+'T12:00:00').toLocaleDateString('pt-BR');}catch(e){return s;}}

function renderKpis(m){
  var box=document.getElementById('kpis');
  function k(cls,label,val,foot){return '<div class="kpi '+cls+'"><div class="label">'+label+'</div><div class="value">'+val+'</div><div class="foot">'+(foot||'')+'</div></div>';}
  var viv=(m.sessoes_conectadas===undefined?null:m.sessoes_conectadas);
  var sess=(viv===null? (m.sessions_active||0) : viv+' / '+(m.sessions_active||0));
  var sessFoot=(viv===null?'números pareados':'conectadas agora / ativas no banco');
  var filaE=m.fila_entrada||0, filaS=m.fila_saida||0, mortas=m.fila_mortas||0;
  box.innerHTML=
    k('blue','Clientes',m.tenants_total||0,'portais com o app instalado')+
    k(viv!==null&&viv<(m.sessions_active||0)?'amber':'purple','Números conectados',sess,sessFoot)+
    k('green','Mensagens 24h',(m.msgs_24h||0),'↓ '+(m.msgs_entrada_24h||0)+' entrada  ·  ↑ '+(m.msgs_saida_24h||0)+' saída')+
    k(mortas>0?'red':'green','Fila',filaE+filaS,filaE+' entrada · '+filaS+' saída · '+mortas+' presas')+
    k((m.falhas_24h||0)>0?'red':'green','Falhas 24h',m.falhas_24h||0,'mensagens que não entregaram')+
    k('amber','Licenças',(m.licencas_vencendo||0)+(m.licencas_vencidas||0),(m.licencas_em_vigor||0)+' em vigor · '+(m.licencas_vencendo||0)+' vencendo · '+(m.licencas_vencidas||0)+' vencidas');

  // Alertas operacionais: o que exige alguem agir AGORA. Ficavam invisiveis
  // ate' alguem abrir a saude de cada cliente, um por um.
  var al='';
  if(m.tokens_vencidos>0) al+='<div class="alerta">🔑 '+m.tokens_vencidos+' portal(is) com token VENCIDO — enquanto não renovar, nenhuma mensagem do cliente chega no Contact Center.</div>';
  if(m.tenants_sem_sessao>0) al+='<div class="alerta">📵 '+m.tenants_sem_sessao+' cliente(s) sem nenhum número conectado.</div>';
  if(mortas>0) al+='<div class="alerta">📥 '+mortas+' mensagem(ns) presa(s) na fila — reprocesse em Saúde do cliente.</div>';
  var ea=document.getElementById('alertas-op'); if(ea) ea.innerHTML=al;
  document.getElementById('cnt-tenants').textContent=m.tenants_total||0;
  var cv=document.getElementById('cnt-vencendo');
  if(cv) cv.textContent=(m.licencas_vencendo||0)+(m.licencas_vencidas||0);
}
function renderTenants(){
  var q=(document.getElementById('tenant-search').value||'').toLowerCase();
  var fs=document.getElementById('tenant-filter').value;
  var body=document.getElementById('tenants-body');
  var list=TENANTS.filter(function(t){
    if(q&&t.domain.toLowerCase().indexOf(q)<0)return false;
    if(fs==='vencida'&&!t.expirada)return false;
    if(fs==='vencendo'&&!(typeof t.dias_restantes==='number'&&t.dias_restantes>=0&&t.dias_restantes<=7))return false;
    if(fs==='sem_licenca'&&t.licenca_configurada)return false;
    return true;
  });
  var cnt=document.getElementById('tenant-count');
  if(cnt){cnt.textContent=(q||fs)?(list.length+' de '+TENANTS.length):(TENANTS.length+' clientes');}
  if(!list.length){body.innerHTML='<tr><td colspan="7" class="empty">Nenhum cliente encontrado.</td></tr>';return;}
  body.innerHTML=list.map(function(t){
    var conn=(t.connections_qr||0)+' QR';if(t.connections_cloud)conn+=' · '+t.connections_cloud+' Cloud';
    var tokCls={valid:'tok-valid',expiring:'tok-expiring',expired:'tok-expired'}[t.token_status]||'tok-expired';
    var tokLbl={valid:'válido',expiring:'expirando',expired:'expirado'}[t.token_status]||t.token_status;
    var vig=t.valid_until?'<div class="meta">até '+fmtDia(t.valid_until)+'</div>':'';
    var d=encodeURIComponent(t.domain);
    return '<tr><td><div class="domain">'+t.domain+'</div><div class="meta">Linha '+(t.open_line_id||'—')+' · desde '+fmtDate(t.installed_at)+'</div></td>'+
      '<td>'+featTags(t)+'<div class="meta">'+(t.max_sessions||1)+' número(s)</div></td>'+
      '<td>'+licBadge(t)+vig+'</td>'+
      '<td>'+conn+'</td><td>'+(t.msgs_24h||0)+'<div class="meta">'+(t.msgs_inbound_24h||0)+'↓ '+(t.msgs_outbound_24h||0)+'↑</div></td>'+
      '<td class="'+tokCls+'">● '+tokLbl+'</td>'+
      '<td style="text-align:right"><div class="actions"><button class="btn" onclick="toggleMenu(this)">⋯</button><div class="menu">'+
        '<button onclick="abrirLicenca(&#39;'+d+'&#39;)">📄 Licença e pagamentos</button>'+
        '<button onclick="verSaude(&#39;'+d+'&#39;)">🩺 Diagnosticar</button>'+
        '<div class="sep"></div>'+
        '<button onclick="setToolDomain(&#39;'+d+'&#39;)">🔧 Abrir em Ferramentas</button>'+
      '</div></div></td></tr>';
  }).join('');
}
function fechaMenus(){document.querySelectorAll('.menu.open').forEach(function(x){x.classList.remove('open');});}
// Ancora o menu ao botao em coordenadas de viewport (o menu e' position:fixed).
// Alinha pela direita do botao e vira pra cima quando nao cabe embaixo — que
// e' o caso comum, com a tabela de 1 linha perto do rodape.
function posicionaMenu(btn,m){
  var r=btn.getBoundingClientRect();
  var mw=m.offsetWidth, mh=m.offsetHeight, folga=4, borda=8;
  var left=r.right-mw;
  if(left+mw>window.innerWidth-borda) left=window.innerWidth-mw-borda;
  if(left<borda) left=borda;
  var top=r.bottom+folga;
  if(top+mh>window.innerHeight-borda){
    var acima=r.top-folga-mh;
    top = acima>=borda ? acima : Math.max(borda, window.innerHeight-mh-borda);
  }
  m.style.left=left+'px';
  m.style.top=top+'px';
}
function toggleMenu(btn){
  var m=btn.nextElementSibling;
  var was=m.classList.contains('open');
  fechaMenus();
  if(was) return;
  m.classList.add('open');   // precisa estar visivel pra ter offsetWidth/Height
  posicionaMenu(btn,m);
}
document.addEventListener('click',function(e){if(!e.target.closest('.actions'))fechaMenus();});
// Menu fixed nao acompanha rolagem nem redimensionamento: fecha em vez de
// ficar flutuando solto. 'true' captura tambem o scroll da .tablescroll.
window.addEventListener('resize',fechaMenus);
window.addEventListener('scroll',fechaMenus,true);

function setToolDomain(d){irPara('tools');document.getElementById('tool-domain').value=decodeURIComponent(d);}
function preencherSeletorFerramentas(){
  var sel=document.getElementById('tool-domain');
  if(!sel) return;
  var atual=sel.value;
  sel.innerHTML='<option value="">— escolha um cliente —</option>'+
    TENANTS.map(function(t){return '<option value="'+_esc(t.domain)+'">'+_esc(t.domain)+'</option>';}).join('');
  if(atual) sel.value=atual;
}
// Acao que mexe no portal do cliente pede confirmacao nomeando o cliente:
// executar no tenant errado e' o engano facil de cometer numa lista.
function confirmarFerramenta(path,rotulo){
  var dom=document.getElementById('tool-domain').value.trim();
  if(!dom){toast('Escolha um cliente primeiro',false);return;}
  if(!confirm(rotulo+' em '+dom+'?')) return;
  toolAction(path,'GET');
}
function confirmarEsvaziarFilas(){
  if(!confirm('Isto DESCARTA mensagens de clientes que ainda nao foram entregues. Elas nao voltam. Tem certeza?')) return;
  var t=prompt('Para confirmar, digite: ESVAZIAR');
  if(t!=='ESVAZIAR'){toast('cancelado',false);return;}
  globalAction('queue/flush','POST');
}
function toolAction(path,method){var dom=document.getElementById('tool-domain').value.trim();if(!dom){toast('Informe o domínio primeiro',false);return;}runTool('/admin/api/tenant/'+path+'?domain='+encodeURIComponent(dom),method,'tool-output');}
function globalAction(path,method){runTool('/admin/api/'+path,method,'tool-output');}
function runTool(url,method,targetId){var out=document.getElementById(targetId);out.style.display='block';out.textContent='Executando '+method+' '+url+' …';fetch(url,{method:method}).then(function(r){return r.text();}).then(function(t){try{out.textContent=JSON.stringify(JSON.parse(t),null,2);}catch(e){out.textContent=t;}toast('✓ executado',true);}).catch(function(e){out.textContent='Erro: '+e;toast('✗ falha',false);});}

// ── LICENCAS ──
var LICENCAS=[];
function carregarLicencas(){
  fetch('/admin/api/licenses').then(function(r){return r.json();}).then(function(d){
    LICENCAS=d.licenses||[]; renderLicencas();
  }).catch(function(){document.getElementById('licencas-body').innerHTML='<tr><td colspan="6" class="empty">Falha ao carregar.</td></tr>';});
}
function renderLicencas(){
  var q=(document.getElementById('lic-search').value||'').toLowerCase();
  var f=document.getElementById('lic-filter').value;
  var b=document.getElementById('licencas-body');
  var list=LICENCAS.filter(function(l){
    if(q&&(l.domain||'').toLowerCase().indexOf(q)<0)return false;
    if(f==='vencida'&&!l.expirada)return false;
    if(f==='vencendo'&&!(typeof l.dias_restantes==='number'&&l.dias_restantes>=0&&l.dias_restantes<=7))return false;
    if(f==='sem_prazo'&&l.valid_until)return false;
    return true;
  });
  if(!list.length){b.innerHTML='<tr><td colspan="6" class="empty">Nenhuma licenca encontrada.</td></tr>';return;}
  b.innerHTML=list.map(function(l){
    var d=encodeURIComponent(l.domain);
    return '<tr><td class="domain">'+l.domain+'</td>'+
      '<td>'+(l.max_sessions||1)+'</td>'+
      '<td>'+featTags(l)+'</td>'+
      '<td>'+(l.valid_until?fmtDia(l.valid_until):'<span class="meta">sem prazo</span>')+'</td>'+
      '<td>'+licBadge(l)+'</td>'+
      '<td style="text-align:right"><div class="actions"><button class="btn" onclick="toggleMenu(this)">&#8943;</button><div class="menu">'+
        '<button onclick="abrirLicenca(&#39;'+d+'&#39;)">Editar contrato</button>'+
        '<button onclick="abrirPagamento(&#39;'+d+'&#39;)">Registrar pagamento</button>'+
        '<div class="sep"></div>'+
        '<button onclick="verSaude(&#39;'+d+'&#39;)">Diagnosticar</button>'+
      '</div></div></td></tr>';
  }).join('');
}
function _modal(html){
  var ov=document.createElement('div'); ov.id='lic-modal';
  ov.style.cssText='position:fixed;inset:0;background:rgba(2,6,23,.8);backdrop-filter:blur(6px);z-index:99998;display:flex;align-items:center;justify-content:center;padding:20px;overflow:auto';
  ov.innerHTML='<div style="background:var(--panel);border:1px solid var(--border);border-radius:15px;padding:22px;max-width:580px;width:100%">'+html+'</div>';
  document.body.appendChild(ov); return ov;
}
function fecharModalLic(){var m=document.getElementById('lic-modal');if(m)m.remove();}
function abrirLicenca(domEnc){
  var dom=decodeURIComponent(domEnc);
  fetch('/admin/api/license?domain='+encodeURIComponent(dom)).then(function(r){return r.json();}).then(function(d){
    var l=d.license||{};
    function ck(id,lbl,on){return '<label style="display:block;margin:7px 0"><input type="checkbox" id="'+id+'" '+(on?'checked':'')+'> '+lbl+'</label>';}
    _modal('<h3 style="margin:0 0 4px">Contrato de '+dom+'</h3>'+
      '<div class="meta" style="margin-bottom:14px">Marque o que o contrato deste cliente inclui.</div>'+
      '<label class="meta">Numeros WhatsApp</label>'+
      '<input class="dominput" id="lic-max" type="number" min="1" value="'+(l.max_sessions||1)+'">'+
      '<div style="margin:12px 0 4px" class="meta">Beneficios</div>'+
      ck('lic-cloud','WhatsApp Cloud API (Meta) + Templates',l.feat_cloud_api)+
      ck('lic-auto','Automacoes (robos BizProc)',l.feat_automations)+
      ck('lic-rep','Relatorios',l.feat_reports)+
      '<label class="meta" style="display:block;margin-top:12px">Valido ate (vazio = sem prazo)</label>'+
      '<input class="dominput" id="lic-ate" type="date" value="'+(l.valid_until||'')+'">'+
      '<label class="meta" style="display:block;margin-top:12px">Observacoes</label>'+
      '<input class="dominput" id="lic-notes" value="'+String(l.notes||'').replace(/"/g,'&quot;')+'">'+
      '<div style="display:flex;gap:8px;margin-top:18px;justify-content:flex-end">'+
        '<button class="btn" onclick="fecharModalLic()">Cancelar</button>'+
        '<button class="btn" onclick="salvarLicenca(&#39;'+domEnc+'&#39;)">Salvar contrato</button>'+
      '</div>');
  }).catch(function(){toast('falha ao carregar licenca',false);});
}
function salvarLicenca(domEnc){
  var body={domain:decodeURIComponent(domEnc),
    max_sessions:parseInt(document.getElementById('lic-max').value,10)||1,
    feat_cloud_api:document.getElementById('lic-cloud').checked,
    feat_automations:document.getElementById('lic-auto').checked,
    feat_reports:document.getElementById('lic-rep').checked,
    valid_until:document.getElementById('lic-ate').value,
    notes:document.getElementById('lic-notes').value};
  fetch('/admin/api/license',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)})
    .then(function(r){return r.json().then(function(j){return{ok:r.ok,j:j};});})
    .then(function(res){if(res.ok){toast('contrato salvo',true);fecharModalLic();carregarLicencas();carregarTudo();}else{toast(res.j.error||'falha',false);}})
    .catch(function(){toast('erro de conexao',false);});
}
function abrirPagamento(domEnc){
  var dom=decodeURIComponent(domEnc);
  fetch('/admin/api/license?domain='+encodeURIComponent(dom)).then(function(r){return r.json();}).then(function(d){
    var hoje=new Date().toISOString().slice(0,10);
    var pags=(d.pagamentos||[]).map(function(p){
      return '<tr><td class="meta">'+fmtDia(p.paid_at)+'</td><td class="meta">'+fmtDia(p.covers_until)+'</td><td>'+fmtBRL(p.amount_cents)+'</td><td class="meta">'+(p.recorded_by||'-')+'</td></tr>';
    }).join('')||'<tr><td colspan="4" class="empty">Nenhum pagamento registrado.</td></tr>';
    _modal('<h3 style="margin:0 0 4px">Registrar pagamento - '+dom+'</h3>'+
      '<div class="meta" style="margin-bottom:14px">O pagamento estende a vigencia da licenca automaticamente.</div>'+
      '<label class="meta">Pago em</label><input class="dominput" id="pg-pago" type="date" value="'+hoje+'">'+
      '<label class="meta" style="display:block;margin-top:10px">Libera o uso ate</label><input class="dominput" id="pg-ate" type="date">'+
      '<label class="meta" style="display:block;margin-top:10px">Valor (R$)</label><input class="dominput" id="pg-valor" type="number" step="0.01" min="0" placeholder="0,00">'+
      '<label class="meta" style="display:block;margin-top:10px">Forma</label><input class="dominput" id="pg-forma" placeholder="pix, transferencia, boleto...">'+
      '<label class="meta" style="display:block;margin-top:10px">Observacoes</label><input class="dominput" id="pg-notes">'+
      '<div class="section-title" style="margin-top:18px">Historico</div>'+
      '<div class="tablewrap"><div class="tablescroll"><table><thead><tr><th>Pago em</th><th>Cobre ate</th><th>Valor</th><th>Registrado por</th></tr></thead><tbody>'+pags+'</tbody></table></div></div>'+
      '<div style="display:flex;gap:8px;margin-top:18px;justify-content:flex-end">'+
        '<button class="btn" onclick="fecharModalLic()">Cancelar</button>'+
        '<button class="btn" onclick="salvarPagamento(&#39;'+domEnc+'&#39;)">Registrar</button>'+
      '</div>');
  }).catch(function(){toast('falha ao carregar',false);});
}
function salvarPagamento(domEnc){
  var ate=document.getElementById('pg-ate').value;
  if(!ate){toast('Informe ate quando o pagamento libera o uso',false);return;}
  var reais=parseFloat(document.getElementById('pg-valor').value||'0');
  var body={domain:decodeURIComponent(domEnc),
    paid_at:document.getElementById('pg-pago').value,
    covers_until:ate,
    amount_cents:Math.round(reais*100),
    method:document.getElementById('pg-forma').value,
    notes:document.getElementById('pg-notes').value};
  fetch('/admin/api/license/payment',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)})
    .then(function(r){return r.json().then(function(j){return{ok:r.ok,j:j};});})
    .then(function(res){if(res.ok){toast('pagamento registrado',true);fecharModalLic();carregarLicencas();carregarTudo();}else{toast(res.j.error||'falha',false);}})
    .catch(function(){toast('erro de conexao',false);});
}

// ── SAUDE DO CLIENTE (monitoramento + acoes de suporte) ──
var HEALTH_DOM='';
var HEALTH_QR_T=null;

function verSaude(domEnc){
  irPara('health');
  var sel=document.getElementById('health-domain');
  if(sel) sel.value=decodeURIComponent(domEnc);
  carregarHealth();
}
function _card(titulo,corpo,estado,acao){
  var cls = estado==='ruim' ? 'red' : estado==='atencao' ? 'amber' : 'green';
  return '<div class="kpi '+cls+'" style="display:block">'+
    '<div class="label">'+titulo+'</div>'+
    '<div style="margin-top:9px;font-size:.85em;line-height:1.75">'+corpo+'</div>'+
    (acao?'<div style="margin-top:11px">'+acao+'</div>':'')+'</div>';
}
function _l(k,v){return '<div><span class="meta">'+k+':</span> '+v+'</div>';}
function _alerta(t){return '<div style="color:var(--red);font-weight:600;margin-top:7px">! '+t+'</div>';}
function _ok(t){return '<div style="color:var(--green);font-weight:600;margin-top:7px">OK '+t+'</div>';}
function _esc(s){return String(s==null?'':s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');}

function carregarHealth(){
  var sel=document.getElementById('health-domain');
  var dom=(sel&&sel.value||'').trim();
  var out=document.getElementById('health-out');
  pararQR();
  if(!dom){out.innerHTML='<div class="empty">Escolha um cliente para ver o estado do app dele.</div>';return;}
  HEALTH_DOM=dom;
  out.innerHTML='<div class="loading">Coletando estado de '+_esc(dom)+'...</div>';
  fetch('/admin/api/tenant/health?domain='+encodeURIComponent(dom))
  .then(function(r){return r.json();}).then(function(d){
    if(d.error){out.innerHTML='<div class="empty">'+_esc(d.error)+'</div>';return;}
    var b=d.bitrix||{}, t=b.token||{}, ss=d.sessoes||[], m=d.mensagens||{}, f=d.fila||{}, l=d.licenca||{};
    var html='';

    // Barra de acoes de suporte
    html+='<div class="toolbar" style="margin-bottom:14px">'+
      '<button class="btn" onclick="testarConexao()">Testar conexao</button>'+
      '<button class="btn" onclick="abrirPareamento()">Conectar WhatsApp</button>'+
      '<button class="btn" onclick="verComoCliente()">Ver como o cliente ve</button>'+
      '<button class="btn" onclick="abrirCredenciais()">Credenciais do app</button>'+
      '<span class="meta" style="margin-left:auto;align-self:center">'+_esc(dom)+'</span>'+
    '</div>';
    html+='<div id="health-teste"></div>';
    html+='<div class="kpis" style="grid-template-columns:repeat(auto-fit,minmax(330px,1fr));margin-bottom:16px">';

    // 1) Bitrix
    var c='', est='bom';
    if(!b.instalado){ c=_alerta(b.problema||'app nao instalado'); est='ruim'; }
    else{
      c=_l('Linha Aberta','<strong>'+(b.open_line_id||'-')+'</strong>')+
        _l('Conector do app',_esc(b.connector_id||'-'))+
        _l('member_id','<span class="mono">'+_esc(b.member_id||'-')+'</span>')+
        _l('Usuario master',_esc(b.master_user_id||'nao definido'))+
        _l('Instalado em',fmtDate(b.instalado_em))+
        _l('Token', t.estado==='ok'
             ? '<span class="tok-valid">valido</span> ate '+fmtDate(t.expira_em)
             : '<span class="tok-expired">'+_esc(t.estado||'?')+'</span>');
      var semCred=false;
      (b.conectores||[]).forEach(function(x){
        c+=_l('Vinculo','<span class="mono">'+_esc(x.session_jid)+'</span> -> linha '+x.open_line_id);
        if(!x.tem_secret) semCred=true;
      });
      c+=_l('App OAuth', t.client_id_do_token||t.client_id_em_uso
             ? '<span class="mono">'+_esc(t.client_id_do_token||t.client_id_em_uso)+'</span>'
             : '<span class="tok-expired">nao cadastrado</span>');
      // So' e' ALARME quando nao ha' credencial em lugar nenhum. Se o token
      // esta' renovando pela env global, a conta sem credencial propria e'
      // apenas um pendencia — vira problema quando entrar um segundo cliente,
      // porque a env serve a um app so'.
      if(semCred && !t.client_id_em_uso){
        c+=_alerta('sem client_id/client_secret em lugar nenhum: o token NAO renova '+
                   '(o Bitrix responde wrong_client) e as mensagens param de chegar. '+
                   'Cadastre em "Credenciais do app".');
        est='ruim';
      } else if(semCred){
        c+='<div class="meta" style="margin-top:8px;color:#fbbf24">'+
           'Renovando pela credencial global. Este cliente ainda nao tem a propria — '+
           'cadastre em "Credenciais do app" antes de instalar outro portal.</div>';
      }
      if(t.problema_app){ c+=_alerta(t.problema_app); est='ruim'; }
      if(t.estado!=='ok'){ c+=_alerta(t.problema||'token com problema'); est='ruim'; }
      if(b.problema){ c+=_alerta(b.problema); est='ruim'; }
      if(b.problema_conector){ c+=_alerta(b.problema_conector); est='ruim'; }
      if(est==='bom') c+=_ok('integracao saudavel');
    }
    html+=_card('Conexao Bitrix',c,est);

    // 2) Sessoes
    var cs='', es='bom';
    if(!ss.length){ cs='<div class="meta">Nenhum numero conectado.</div>'; es='ruim'; }
    else ss.forEach(function(s){
      cs+='<div style="padding:8px 0;border-bottom:1px solid var(--border)">'+
        _l('Numero','<strong>'+_esc(s.numero||'?')+'</strong> '+
           (s.conectada_agora?'<span class="badge b-active">online</span>'
                             :'<span class="badge b-expired">offline</span>'))+
        _l('Device','<span class="mono">'+_esc(s.jid)+'</span>')+
        _l('Tipo',_esc(s.tipo||'qr'))+
        (s.visto_em?_l('Visto em',fmtDate(s.visto_em)):'')+
        (s.problema?_alerta(s.problema):'')+
        (s.problema_phone?_alerta(s.problema_phone):'')+
      '</div>';
      if(!s.conectada_agora||s.problema||s.problema_phone) es='ruim';
    });
    if(es==='bom') cs+=_ok(ss.length+' numero(s) conectado(s)');
    html+=_card('Sessoes WhatsApp',cs,es,
      '<button class="btn" onclick="abrirPareamento()">Conectar outro numero</button>');

    // 3) Mensagens
    var em=(m.falhas_7d||0)>0?'atencao':'bom';
    var cm=_l('Entrada 24h',m.entrada_24h||0)+_l('Saida 24h',m.saida_24h||0)+
           _l('Falhas 7 dias',m.falhas_7d||0);
    if(m.observacao) cm+=_alerta(m.observacao);
    if((m.falhas_7d||0)===0&&(m.entrada_24h||0)>0) cm+=_ok('fluxo normal');
    html+=_card('Mensagens',cm,em);

    // 4) Fila presa
    var presas=f.presas||0, reent=f.reentregaveis||0, cf, ef, acaoF='';
    if(presas===0){ cf=_ok('nenhuma mensagem presa'); ef='bom'; }
    else{
      ef='ruim';
      cf=_l('Presas na fila',presas)+_l('Reentregaveis',reent)+
        '<div class="meta" style="margin-top:6px">Mensagens de clientes que nao chegaram no Contact Center.</div>';
      if(reent>0) acaoF='<button class="btn" onclick="reprocessarFila()">Reentregar '+reent+' mensagem(ns)</button>';
      else cf+=_alerta('nenhuma reentregavel: as sessoes de origem nao existem mais');
    }
    html+=_card('Fila de mensagens',cf,ef,acaoF);

    // 5) Licenca
    var cl, el='bom';
    if(!l.configurada){ cl='<div class="meta">Licenca nao configurada.</div>'; el='atencao'; }
    else{
      cl=_l('Numeros',l.max_sessions)+_l('Beneficios',featTags(l))+
         _l('Vigencia', l.valid_until?fmtDia(l.valid_until):'sem prazo')+
         _l('Pagamentos',((l.pagamentos||[]).length)+' registrado(s)');
      if(l.expirada){ cl+=_alerta('licenca vencida - avisa, mas nao bloqueia o app'); el='atencao'; }
      else cl+=_ok('licenca em vigor');
    }
    html+=_card('Licenca',cl,el,
      '<button class="btn" onclick="abrirLicenca(&#39;'+encodeURIComponent(dom)+'&#39;)">Editar contrato</button>');
    html+='</div>';

    // 6) Preview + log
    html+='<div id="health-preview"></div>';
    var lg=d.log||[];
    html+='<div class="section-title">Log deste cliente <span class="meta">('+lg.length+' linhas, so deste tenant)</span></div>';
    if(!lg.length) html+='<div class="empty">Nada no buffer para este cliente ainda.</div>';
    else html+='<pre id="health-log" style="background:#05080f;border:1px solid var(--border);border-radius:12px;padding:14px;font-family:ui-monospace,Menlo,monospace;font-size:.74em;line-height:1.55;color:#a7f3d0;overflow:auto;max-height:420px;white-space:pre-wrap"></pre>';

    out.innerHTML=html;
    if(lg.length){
      var pre=document.getElementById('health-log');
      pre.textContent=lg.map(function(x){return x.texto;}).join('\n');
      pre.scrollTop=pre.scrollHeight;
    }
    window._healthPreviewURL=b.preview_url||'';
  }).catch(function(e){out.innerHTML='<div class="empty">Falha: '+_esc(e)+'</div>';});
}

// ── Testar conexao (chamadas reais ao Bitrix) ──
function testarConexao(){
  if(!HEALTH_DOM) return;
  var box=document.getElementById('health-teste');
  box.innerHTML='<div class="loading">Testando integracao contra o Bitrix...</div>';
  fetch('/admin/api/tenant/testar-conexao?domain='+encodeURIComponent(HEALTH_DOM),{method:'POST'})
  .then(function(r){return r.json();}).then(function(d){
    if(d.error){box.innerHTML='<div class="empty">'+_esc(d.error)+'</div>';return;}
    var linhas=(d.testes||[]).map(function(t){
      return '<div style="padding:6px 0;border-bottom:1px solid var(--border)">'+
        (t.ok?'<span class="badge b-active">ok</span>':'<span class="badge b-expired">falhou</span>')+
        ' <strong>'+_esc(t.nome)+'</strong>'+
        '<div class="meta" style="margin-left:2px">'+_esc(t.detalhe)+'</div></div>';
    }).join('');
    box.innerHTML='<div class="kpi '+(d.ok?'green':'red')+'" style="display:block;margin-bottom:16px">'+
      '<div class="label">Teste de conexao — '+(d.ok?'tudo funcionando':'ha problema')+'</div>'+
      '<div style="margin-top:8px;font-size:.85em">'+linhas+'</div></div>';
  }).catch(function(e){box.innerHTML='<div class="empty">Falha: '+_esc(e)+'</div>';});
}

// ── Parear WhatsApp pelo suporte ──
function abrirPareamento(){
  if(!HEALTH_DOM) return;
  _modal('<h3 style="margin:0 0 4px">Conectar WhatsApp — '+_esc(HEALTH_DOM)+'</h3>'+
    '<div class="meta" style="margin-bottom:14px">Digite o numero com DDI e DDD, so digitos. Ex: 5581999998888</div>'+
    '<input class="dominput" id="pair-phone" placeholder="5581999998888" inputmode="numeric">'+
    '<div id="pair-area" style="margin-top:14px"></div>'+
    '<div style="display:flex;gap:8px;margin-top:18px;justify-content:flex-end">'+
      '<button class="btn" onclick="pararQR();fecharModalLic()">Fechar</button>'+
      '<button class="btn" onclick="iniciarPareamento()">Gerar QR Code</button>'+
    '</div>');
}
function iniciarPareamento(){
  var el=document.getElementById('pair-phone');
  var fone=(el.value||'').replace(/\D/g,'');
  if(fone.length<12){toast('Numero invalido: use DDI+DDD+numero, so digitos',false);return;}
  var area=document.getElementById('pair-area');
  area.innerHTML='<div class="loading">Iniciando sessao...</div>';
  fetch('/ui/sessions?domain='+encodeURIComponent(HEALTH_DOM),{
    method:'POST',headers:{'Content-Type':'application/json'},
    body:JSON.stringify({phone:fone})})
  .then(function(r){return r.json().then(function(j){return{ok:r.ok,j:j};});})
  .then(function(res){
    if(!res.ok){area.innerHTML='<div style="color:var(--red)">'+_esc(res.j.error||'falha')+'</div>';return;}
    area.innerHTML='<div class="loading">Aguardando o QR Code...</div>';
    pollQR(fone,area);
  }).catch(function(){area.innerHTML='<div style="color:var(--red)">erro de conexao</div>';});
}
function pollQR(fone,area){
  pararQR();
  var tentativas=0;
  HEALTH_QR_T=setInterval(function(){
    tentativas++;
    if(tentativas>90){pararQR();area.innerHTML='<div style="color:var(--red)">QR expirou. Tente de novo.</div>';return;}
    fetch('/ui/sessions/'+encodeURIComponent(fone)+'/qr?domain='+encodeURIComponent(HEALTH_DOM))
    .then(function(r){return r.json();}).then(function(d){
      if(d.status==='connected'){
        pararQR();
        area.innerHTML='<div style="color:var(--green);font-weight:600">Conectado! '+_esc(d.jid||'')+'</div>';
        toast('WhatsApp conectado',true);
        setTimeout(function(){fecharModalLic();carregarHealth();},1800);
        return;
      }
      if(d.status==='ready'&&d.qr){
        // Renderiza o QR por servico externo de imagem seria bloqueado; usa
        // o texto do QR num canvas simples via API publica do proprio app.
        // PNG do proprio app: o QR e' segredo de pareamento e nao pode ir
        // pra servico externo de imagem.
        area.innerHTML='<div style="background:#fff;padding:12px;border-radius:10px;display:inline-block">'+
            '<img alt="QR Code" style="width:240px;height:240px;display:block" '+
            'src="/ui/sessions/'+encodeURIComponent(fone)+'/qr.png?domain='+encodeURIComponent(HEALTH_DOM)+'&t='+Date.now()+'">'+
          '</div>'+
          '<div class="meta" style="margin-top:10px">Abra o WhatsApp do cliente > Aparelhos conectados > Conectar aparelho.</div>';
      }
    }).catch(function(){});
  },2000);
}
function pararQR(){ if(HEALTH_QR_T){clearInterval(HEALTH_QR_T);HEALTH_QR_T=null;} }

// ── Ver como o cliente ve ──
function verComoCliente(){
  var box=document.getElementById('health-preview');
  if(!box) return;
  if(box.innerHTML){ box.innerHTML=''; return; }  // toggle
  var u=window._healthPreviewURL||('/dashboard?preview=1&domain='+encodeURIComponent(HEALTH_DOM));
  box.innerHTML='<div class="section-title">Como o cliente ve o app no Bitrix24 '+
      '<span class="meta">(mesma tela servida no iframe do Bitrix)</span></div>'+
    '<div style="display:flex;gap:8px;margin-bottom:10px">'+
      '<button class="btn" onclick="document.getElementById(&#39;health-frame&#39;).src=document.getElementById(&#39;health-frame&#39;).src">Recarregar</button>'+
      '<button class="btn" onclick="window.open(&#39;'+u+'&#39;,&#39;_blank&#39;)">Abrir em nova aba</button>'+
    '</div>'+
    '<div style="background:#0b1220;border:1px solid var(--border);border-radius:14px;overflow:hidden;height:70vh;margin-bottom:18px">'+
      '<iframe id="health-frame" src="'+u+'" style="width:100%;height:100%;border:0;background:#0f172a" title="Visao do cliente"></iframe>'+
    '</div>';
}

function abrirCredenciais(){
  var dom=document.getElementById('health-domain').value;
  if(!dom){ toast('Escolha um cliente primeiro',false); return; }
  _modal('<h3 style="margin:0 0 4px">Credenciais do app — '+_esc(dom)+'</h3>'+
    '<div class="meta" style="margin-bottom:14px">'+
      'O app OAuth e instalado por cliente, entao cada portal tem o seu. '+
      'Sem estas credenciais o token nao renova e as mensagens param de chegar.'+
    '</div>'+
    '<label class="meta">client_id</label>'+
    '<input class="dominput" id="cred-id" placeholder="app.0000000000000.00000000" autocomplete="off">'+
    '<label class="meta" style="display:block;margin-top:12px">client_secret</label>'+
    '<input class="dominput" id="cred-secret" type="password" placeholder="nao aparece no log nem na resposta" autocomplete="off">'+
    '<div class="meta" style="margin-top:12px;line-height:1.5">'+
      'Onde achar: no portal do cliente, em <strong>Aplicativos</strong> &rsaquo; o app UC Talk. '+
      'Ao reinstalar o app estes valores MUDAM — atualize aqui antes de testar.'+
    '</div>'+
    '<div id="cred-msg" class="meta" style="margin-top:10px"></div>'+
    '<div style="display:flex;gap:8px;margin-top:18px;justify-content:flex-end">'+
      '<button class="btn" onclick="fecharModalLic()">Cancelar</button>'+
      '<button class="btn" id="cred-btn" onclick="salvarCredenciais()">Salvar credenciais</button>'+
    '</div>');
  setTimeout(function(){var e=document.getElementById('cred-id'); if(e) e.focus();},50);
}

function salvarCredenciais(){
  var dom=document.getElementById('health-domain').value;
  var cid=(document.getElementById('cred-id').value||'').trim();
  var sec=(document.getElementById('cred-secret').value||'').trim();
  var msg=document.getElementById('cred-msg');
  if(!cid||!sec){ msg.style.color='#f87171'; msg.textContent='Preencha os dois campos.'; return; }
  var btn=document.getElementById('cred-btn');
  btn.disabled=true; btn.textContent='Salvando...';
  msg.style.color=''; msg.textContent='';
  fetch('/admin/api/tenant/credenciais',{method:'POST',headers:{'Content-Type':'application/json'},
    body:JSON.stringify({domain:dom,client_id:cid,client_secret:sec})})
  .then(function(r){ return r.json().then(function(j){ return {ok:r.ok,j:j}; }); })
  .then(function(res){
    btn.disabled=false; btn.textContent='Salvar credenciais';
    if(res.ok){
      toast('Credenciais gravadas em '+res.j.conexoes+' conexao(oes)',true);
      fecharModalLic(); setTimeout(carregarHealth,900);
    }else{
      msg.style.color='#f87171'; msg.textContent=res.j.error||'falhou';
    }
  })
  .catch(function(e){
    btn.disabled=false; btn.textContent='Salvar credenciais';
    msg.style.color='#f87171'; msg.textContent='erro: '+e;
  });
}

function reprocessarFila(){
  if(!HEALTH_DOM) return;
  if(!confirm('Reentregar as mensagens presas de '+HEALTH_DOM+'?\n\nElas voltam para a fila de entrada e sao processadas de novo. As de sessoes que nao existem mais sao descartadas.')) return;
  fetch('/admin/api/tenant/reprocessar-fila?domain='+encodeURIComponent(HEALTH_DOM),{method:'POST'})
    .then(function(r){return r.json().then(function(j){return{ok:r.ok,j:j};});})
    .then(function(res){
      if(res.ok){ toast(res.j.reenfileirados+' reenfileirada(s), '+res.j.descartados+' descartada(s)',true); setTimeout(carregarHealth,1500); }
      else toast(res.j.error||'falha',false);
    }).catch(function(){toast('erro de conexao',false);});
}
function preencherSeletorPreview(){
  var sel=document.getElementById('preview-domain');
  if(!sel) return;
  var atual=sel.value;
  sel.innerHTML='<option value="">— Demonstração (sem dados de cliente) —</option>'+
    TENANTS.map(function(t){
      return '<option value="'+_esc(t.domain)+'">'+_esc(t.domain)+'</option>';
    }).join('');
  if(atual) sel.value=atual;
}
function preencherSeletorSaude(){
  var sel=document.getElementById('health-domain');
  if(!sel||!TENANTS) return;
  var atual=sel.value;
  sel.innerHTML='<option value="">— escolha um cliente —</option>'+
    TENANTS.map(function(t){return '<option value="'+_esc(t.domain)+'">'+_esc(t.domain)+'</option>';}).join('');
  if(atual) sel.value=atual;
}

// ── USAGE ──
function carregarUsage(){
  fetch('/admin/api/usage').then(function(r){return r.json();}).then(function(d){
    var b=document.getElementById('usage-body'); var list=d.usage||[];
    if(!list.length){b.innerHTML='<tr><td colspan="7" class="empty">Sem dados de consumo ainda.</td></tr>';return;}
    // Barra relativa ao cliente que mais usa: numero solto nao diz se 4 mil
    // mensagens e muito ou pouco pra este parque.
    var teto=1; list.forEach(function(u){ if((u.msgs_30d||0)>teto) teto=u.msgs_30d; });
    b.innerHTML=list.map(function(u){
      var usados=(u.sessions_qr||0)+(u.sessions_cloud||0);
      var teto_n=u.max_sessions||0;
      // Estourar o contratado e o que o suporte precisa ver de relance.
      var cor=(teto_n&&usados>teto_n)?'var(--red)':(teto_n&&usados===teto_n?'#fbbf24':'var(--muted)');
      var nums='<span style="color:'+cor+';font-weight:700">'+usados+'</span>'+
               (teto_n?'<span class="meta"> / '+teto_n+' contratado(s)</span>':'<span class="meta"> / sem limite</span>')+
               (u.sessions_cloud?'<div class="meta">'+(u.sessions_qr||0)+' QR · '+u.sessions_cloud+' Cloud</div>':'');
      var pct=Math.round(((u.msgs_30d||0)/teto)*100);
      var barra='<div style="background:var(--panel2);border-radius:999px;height:7px;overflow:hidden">'+
                '<div style="width:'+Math.max(pct,2)+'%;height:100%;background:linear-gradient(90deg,#25D366,#60a5fa)"></div></div>';
      var vig;
      if(!u.valid_until){ vig='<span class="meta">sem prazo</span>'; }
      else{
        var dt=new Date(u.valid_until), hoje=new Date();
        var dias=Math.ceil((dt-hoje)/86400000);
        var c=dias<0?'var(--red)':(dias<=7?'#fbbf24':'var(--muted)');
        var txt=dias<0?('vencida há '+Math.abs(dias)+'d'):(dias<=7?('vence em '+dias+'d'):dt.toLocaleDateString('pt-BR'));
        vig='<span style="color:'+c+';font-weight:600">'+txt+'</span>';
      }
      return '<tr>'+
        '<td class="domain">'+_esc(u.domain)+'</td>'+
        '<td>'+(u.msgs_24h||0)+'</td>'+
        '<td>'+(u.msgs_7d||0)+'</td>'+
        '<td><b>'+(u.msgs_30d||0)+'</b></td>'+
        '<td>'+barra+'</td>'+
        '<td>'+nums+'</td>'+
        '<td>'+vig+'</td>'+
      '</tr>';
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

// Renovacoes a vencer na visao geral: o que o suporte precisa cobrar.
// Vem da mesma lista de licencas, ja' ordenada por vencimento.
function renderRenovacoes(list){
  var b=document.getElementById('renovacoes');
  if(!b)return;
  var urg=(list||[]).filter(function(l){
    return l.valid_until && (l.expirada || (typeof l.dias_restantes==='number' && l.dias_restantes<=15));
  }).slice(0,10);
  if(!urg.length){b.innerHTML='<tr><td colspan="4" class="empty">Nenhuma renovacao proxima.</td></tr>';return;}
  b.innerHTML=urg.map(function(l){
    var d=encodeURIComponent(l.domain);
    var quando=l.expirada?('venceu em '+fmtDia(l.valid_until)):(l.dias_restantes+' dia(s)');
    return '<tr><td class="domain">'+l.domain+'</td><td>'+quando+'</td><td>'+licBadge(l)+'</td>'+
      '<td style="text-align:right"><button class="btn" onclick="abrirPagamento(&#39;'+d+'&#39;)">Registrar pagamento</button></td></tr>';
  }).join('');
}

function _quando(iso){
  if(!iso) return '-';
  var d=new Date(iso), ag=new Date(), dif=Math.floor((ag-d)/1000);
  if(dif<60) return 'agora';
  if(dif<3600) return Math.floor(dif/60)+' min';
  if(d.toDateString()===ag.toDateString()) return d.toLocaleTimeString('pt-BR',{hour:'2-digit',minute:'2-digit'});
  return d.toLocaleDateString('pt-BR',{day:'2-digit',month:'2-digit'})+' '+d.toLocaleTimeString('pt-BR',{hour:'2-digit',minute:'2-digit'});
}
function carregarMensagensRecentes(){
  var tb=document.getElementById('msgs-recentes');
  if(!tb) return;
  fetch('/admin/api/mensagens-recentes?limite=40').then(function(r){return r.json();}).then(function(d){
    var ms=d.mensagens||[];
    if(!ms.length){tb.innerHTML='<tr><td colspan="6" class="empty">Nenhuma mensagem ainda.</td></tr>';return;}
    tb.innerHTML=ms.map(function(m){
      var ent=(m.direction==='inbound');
      var falhou=(m.status==='failed');
      var sit=falhou
        ? '<span class="st-fail" title="'+_esc(m.error_msg||'')+'">falhou</span>'
        : '<span class="st-ok">'+_esc(m.status||'')+'</span>';
      var quem=_esc(m.author||'')||'<span class="meta">—</span>';
      return '<tr>'+
        '<td class="meta" style="white-space:nowrap">'+_quando(m.created_at)+'</td>'+
        '<td>'+(_esc(m.domain)||'<span class="meta">sem vínculo</span>')+'</td>'+
        '<td class="'+(ent?'dir-in':'dir-out')+'" title="'+(ent?'do cliente':'do operador')+'">'+(ent?'↓':'↑')+'</td>'+
        '<td class="mono">'+_esc(m.peer||'')+'</td>'+
        '<td><div style="max-width:420px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">'+quem+(m.author?': ':'')+_esc(m.preview||'')+'</div></td>'+
        '<td>'+sit+'</td>'+
      '</tr>';
    }).join('');
  }).catch(function(){tb.innerHTML='<tr><td colspan="6" class="empty">Falha ao carregar mensagens.</td></tr>';});
}

function carregarTudo(){
  carregarMensagensRecentes();
  fetch('/admin/api/licenses').then(function(r){return r.json();}).then(function(d){
    LICENCAS=d.licenses||[];
    renderRenovacoes(LICENCAS);
    if(document.getElementById('page-licencas').classList.contains('active'))renderLicencas();
  }).catch(function(){});
  fetch('/admin/api/metrics').then(function(r){return r.json();}).then(renderKpis).catch(function(){document.getElementById('kpis').innerHTML='<div class="empty">Falha ao carregar métricas.</div>';});
  fetch('/admin/api/tenants').then(function(r){return r.json();}).then(function(d){TENANTS=d.tenants||[];renderTenants();preencherSeletorSaude();preencherSeletorPreview();preencherSeletorFerramentas();}).catch(function(){document.getElementById('tenants-body').innerHTML='<tr><td colspan="7" class="empty">Falha ao carregar clientes.</td></tr>';});
}
carregarTudo();
setInterval(carregarTudo,60000);
</script>
</body>
</html>`
