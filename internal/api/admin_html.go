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
  <div class="brand"><div class="logo">🛡️</div><h1>UC Talk — Admin</h1></div>
  <p class="desc">Painel de administração. Gerencie tenants, planos, pagamentos e a saúde do sistema.</p>
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
    --bg:#0b1220; --panel:#111927; --panel2:#0f1826; --border:rgba(255,255,255,.07);
    --txt:#e2e8f0; --muted:#94a3b8; --dim:#64748b;
    --green:#25D366; --blue:#60a5fa; --amber:#fbbf24; --red:#f87171; --purple:#a78bfa;
  }
  body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',system-ui,sans-serif;background:var(--bg);color:var(--txt);min-height:100vh}
  a{color:var(--blue);text-decoration:none}

  /* topbar */
  .topbar{position:sticky;top:0;z-index:50;background:rgba(11,18,32,.9);backdrop-filter:blur(10px);border-bottom:1px solid var(--border);padding:12px 22px;display:flex;align-items:center;gap:14px}
  .topbar .logo{width:34px;height:34px;border-radius:9px;background:linear-gradient(135deg,var(--green),#10b981);display:flex;align-items:center;justify-content:center;font-size:17px}
  .topbar h1{font-size:1.05em;font-weight:800}
  .topbar .sub{font-size:.72em;color:var(--dim);font-weight:500}
  .topbar .spacer{flex:1}
  .topbar .env-pill{font-size:.72em;font-weight:700;padding:4px 10px;border-radius:999px;background:rgba(96,165,250,.14);color:var(--blue);border:1px solid rgba(96,165,250,.25)}
  .btn{padding:.5em .9em;border-radius:9px;font-size:.82em;font-weight:700;border:1px solid var(--border);background:rgba(255,255,255,.04);color:var(--txt);cursor:pointer;transition:background .12s}
  .btn:hover{background:rgba(255,255,255,.08)}
  .btn-danger{color:#fecaca;border-color:rgba(248,113,113,.3);background:rgba(127,29,29,.25)}
  .btn-primary{background:linear-gradient(90deg,var(--green),#10b981);color:#052e16;border:0}

  .wrap{max-width:1300px;margin:0 auto;padding:22px}

  /* tabs */
  .tabs{display:flex;gap:6px;margin-bottom:20px;border-bottom:1px solid var(--border);flex-wrap:wrap}
  .tab{padding:10px 16px;font-size:.9em;font-weight:600;color:var(--muted);cursor:pointer;border-bottom:2px solid transparent;margin-bottom:-1px}
  .tab.active{color:var(--txt);border-bottom-color:var(--green)}
  .tab:hover{color:var(--txt)}
  .page{display:none}
  .page.active{display:block}

  /* KPI cards */
  .kpis{display:grid;grid-template-columns:repeat(auto-fit,minmax(170px,1fr));gap:14px;margin-bottom:24px}
  .kpi{background:var(--panel);border:1px solid var(--border);border-radius:14px;padding:16px 18px}
  .kpi .label{font-size:.72em;color:var(--dim);text-transform:uppercase;letter-spacing:.06em;font-weight:700;margin-bottom:8px}
  .kpi .value{font-size:1.9em;font-weight:800;line-height:1}
  .kpi .foot{font-size:.75em;color:var(--muted);margin-top:6px}
  .kpi.green .value{color:var(--green)} .kpi.blue .value{color:var(--blue)}
  .kpi.amber .value{color:var(--amber)} .kpi.red .value{color:var(--red)}
  .kpi.purple .value{color:var(--purple)}

  /* toolbar */
  .toolbar{display:flex;gap:10px;align-items:center;margin-bottom:14px;flex-wrap:wrap}
  .search{flex:1;min-width:220px;padding:.6em .9em;background:var(--panel2);border:1px solid var(--border);border-radius:10px;color:var(--txt);font-size:.9em}
  .search:focus{outline:0;border-color:var(--green)}
  select.filter{padding:.6em .8em;background:var(--panel2);border:1px solid var(--border);border-radius:10px;color:var(--txt);font-size:.85em}

  /* table */
  .tablewrap{background:var(--panel);border:1px solid var(--border);border-radius:14px;overflow:hidden}
  table{width:100%;border-collapse:collapse}
  th{text-align:left;font-size:.72em;color:var(--dim);text-transform:uppercase;letter-spacing:.05em;font-weight:700;padding:12px 14px;border-bottom:1px solid var(--border);background:var(--panel2)}
  td{padding:12px 14px;font-size:.86em;border-bottom:1px solid rgba(255,255,255,.04);vertical-align:middle}
  tr:last-child td{border-bottom:0}
  tr:hover td{background:rgba(255,255,255,.02)}
  .domain{font-weight:600;color:var(--txt)}
  .meta{font-size:.78em;color:var(--dim)}

  /* badges */
  .badge{display:inline-flex;align-items:center;gap:5px;font-size:.72em;font-weight:700;padding:3px 9px;border-radius:999px}
  .b-trial{background:rgba(251,191,36,.15);color:var(--amber)}
  .b-active{background:rgba(37,211,102,.15);color:var(--green)}
  .b-expired{background:rgba(248,113,113,.15);color:var(--red)}
  .b-suspended{background:rgba(148,163,184,.15);color:var(--muted)}
  .b-pro{background:rgba(167,139,250,.15);color:var(--purple)}
  .b-basic{background:rgba(96,165,250,.15);color:var(--blue)}
  .b-none{background:rgba(148,163,184,.1);color:var(--dim)}
  .tok-valid{color:var(--green)} .tok-expiring{color:var(--amber)} .tok-expired{color:var(--red)}
  .dot{width:7px;height:7px;border-radius:50%;display:inline-block}

  /* actions dropdown */
  .actions{position:relative;display:inline-block}
  .actions>.btn{padding:.4em .7em}
  .menu{position:absolute;right:0;top:calc(100% + 4px);background:var(--panel);border:1px solid var(--border);border-radius:10px;min-width:210px;box-shadow:0 14px 40px rgba(0,0,0,.5);z-index:20;overflow:hidden;display:none}
  .menu.open{display:block}
  .menu button{display:block;width:100%;text-align:left;padding:9px 13px;background:none;border:0;color:var(--txt);font-size:.83em;cursor:pointer}
  .menu button:hover{background:rgba(255,255,255,.06)}
  .menu .sep{height:1px;background:var(--border);margin:3px 0}
  .menu .danger{color:#fecaca}

  .empty{text-align:center;padding:40px;color:var(--dim);font-size:.9em}
  .loading{text-align:center;padding:40px;color:var(--muted)}
  .mono{font-family:ui-monospace,'SF Mono',Menlo,monospace;font-size:.9em}

  /* toast */
  #toast{position:fixed;bottom:22px;left:50%;transform:translateX(-50%);background:var(--panel);border:1px solid var(--border);color:var(--txt);padding:.8em 1.3em;border-radius:12px;font-size:.88em;box-shadow:0 12px 40px rgba(0,0,0,.5);z-index:9999;display:none;max-width:90vw}
  #toast.ok{border-color:rgba(37,211,102,.4)} #toast.err{border-color:rgba(248,113,113,.4)}

  /* section tools grid */
  .tools{display:grid;grid-template-columns:repeat(auto-fit,minmax(280px,1fr));gap:14px}
  .toolcard{background:var(--panel);border:1px solid var(--border);border-radius:14px;padding:18px}
  .toolcard h3{font-size:.95em;margin-bottom:6px}
  .toolcard p{font-size:.8em;color:var(--muted);line-height:1.5;margin-bottom:12px}
  .toolcard .row{display:flex;gap:8px;flex-wrap:wrap}
  input.dominput{padding:.55em .8em;background:var(--panel2);border:1px solid var(--border);border-radius:9px;color:var(--txt);font-size:.85em;width:100%;margin-bottom:10px}
  @media(max-width:640px){.topbar h1{font-size:.95em}.wrap{padding:14px}}
</style>
</head>
<body>
<div class="topbar">
  <div class="logo">🛡️</div>
  <div>
    <h1>UC Talk · Painel Admin</h1>
    <div class="sub">Administração central · UC Technology</div>
  </div>
  <span class="env-pill" id="env-pill">carregando…</span>
  <div class="spacer"></div>
  <button class="btn" onclick="carregarTudo()">↻ Atualizar</button>
  <form method="post" action="/admin/logout" style="display:inline"><button class="btn btn-danger" type="submit">Sair</button></form>
</div>

<div class="wrap">
  <div class="tabs">
    <div class="tab active" data-page="overview" onclick="irPara('overview')">Visão geral</div>
    <div class="tab" data-page="tenants" onclick="irPara('tenants')">Tenants</div>
    <div class="tab" data-page="billing" onclick="irPara('billing')">Pagamentos</div>
    <div class="tab" data-page="tools" onclick="irPara('tools')">Ferramentas</div>
  </div>

  <!-- OVERVIEW -->
  <div class="page active" id="page-overview">
    <div class="kpis" id="kpis"><div class="loading">Carregando métricas…</div></div>
    <div class="tablewrap">
      <table>
        <thead><tr><th>Últimos pagamentos</th><th>Tenant</th><th>Plano</th><th>Valor</th><th>Status</th><th>Data</th></tr></thead>
        <tbody id="recent-charges"><tr><td colspan="6" class="loading">Carregando…</td></tr></tbody>
      </table>
    </div>
  </div>

  <!-- TENANTS -->
  <div class="page" id="page-tenants">
    <div class="toolbar">
      <input class="search" id="tenant-search" placeholder="🔎 Buscar por domínio…" oninput="renderTenants()">
      <select class="filter" id="tenant-filter" onchange="renderTenants()">
        <option value="">Todos os status</option>
        <option value="trial">Trial</option>
        <option value="active">Ativo</option>
        <option value="expired">Expirado</option>
        <option value="suspended">Suspenso</option>
        <option value="no_plan">Sem plano</option>
      </select>
      <select class="filter" id="tenant-plan-filter" onchange="renderTenants()">
        <option value="">Todos os planos</option>
        <option value="pro">Pro</option>
        <option value="basic">Básico</option>
      </select>
    </div>
    <div class="tablewrap">
      <table>
        <thead><tr>
          <th>Portal</th><th>Plano</th><th>Status</th><th>Conexões</th>
          <th>Msgs 24h</th><th>Token</th><th style="text-align:right">Ações</th>
        </tr></thead>
        <tbody id="tenants-body"><tr><td colspan="7" class="loading">Carregando tenants…</td></tr></tbody>
      </table>
    </div>
  </div>

  <!-- BILLING -->
  <div class="page" id="page-billing">
    <div class="tablewrap">
      <table>
        <thead><tr><th>Tenant</th><th>Plano</th><th>Método</th><th>Valor</th><th>Status</th><th>Referência</th><th>Criado</th><th>Boleto</th></tr></thead>
        <tbody id="billing-body"><tr><td colspan="8" class="loading">Carregando cobranças…</td></tr></tbody>
      </table>
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
          <button class="btn" onclick="globalAction('cleanup/placeholder-portals','POST')">Limpar portais placeholder</button>
        </div>
      </div>
      <div class="toolcard">
        <h3>🔍 Diagnóstico</h3>
        <p>Dump das tabelas-chave (contagens + amostras) pra depurar quando o painel mostra zeros.</p>
        <div class="row">
          <button class="btn" onclick="globalAction('debug','GET')">Ver diagnóstico</button>
        </div>
      </div>
    </div>
    <pre id="tool-output" style="margin-top:16px;background:var(--panel2);border:1px solid var(--border);border-radius:12px;padding:16px;font-size:.78em;color:var(--muted);overflow:auto;max-height:420px;white-space:pre-wrap;display:none"></pre>
  </div>
</div>

<div id="toast"></div>

<script>
var TENANTS = [];

function toast(msg, ok){
  var t=document.getElementById('toast');
  t.textContent=msg; t.className=ok?'ok':'err'; t.style.display='block';
  clearTimeout(window._tt); window._tt=setTimeout(function(){t.style.display='none';},3800);
}
function irPara(p){
  document.querySelectorAll('.tab').forEach(function(t){t.classList.toggle('active',t.dataset.page===p);});
  document.querySelectorAll('.page').forEach(function(pg){pg.classList.remove('active');});
  document.getElementById('page-'+p).classList.add('active');
}
function fmtBRL(cents){ return 'R$ '+((cents||0)/100).toFixed(2).replace('.',','); }
function fmtDate(s){ if(!s)return '—'; try{return new Date(s).toLocaleString('pt-BR',{day:'2-digit',month:'2-digit',year:'2-digit',hour:'2-digit',minute:'2-digit'});}catch(e){return s;} }
function planBadge(plan,status){
  var s=status||'no_plan';
  var cls={trial:'b-trial',active:'b-active',expired:'b-expired',suspended:'b-suspended',no_plan:'b-none'}[s]||'b-none';
  var lbl={trial:'Trial',active:'Ativo',expired:'Expirado',suspended:'Suspenso',no_plan:'Sem plano'}[s]||s;
  return '<span class="badge '+cls+'">'+lbl+'</span>';
}
function planTag(plan){
  if(plan==='pro')return '<span class="badge b-pro">PRO</span>';
  if(plan==='basic')return '<span class="badge b-basic">BÁSICO</span>';
  return '<span class="badge b-none">—</span>';
}

// ── OVERVIEW ──
function renderKpis(m){
  var box=document.getElementById('kpis');
  function k(cls,label,val,foot){return '<div class="kpi '+cls+'"><div class="label">'+label+'</div><div class="value">'+val+'</div><div class="foot">'+(foot||'')+'</div></div>';}
  box.innerHTML =
    k('blue','Tenants',m.tenants_total,(m.tenants_pro||0)+' Pro · '+(m.tenants_basic||0)+' Básico') +
    k('amber','Em trial',m.tenants_trial,'período de teste') +
    k('green','Ativos (pagos)',m.tenants_active,'assinatura em dia') +
    k('red','Expirados',m.tenants_expired,(m.tenants_suspended||0)+' suspensos') +
    k('green','Receita recebida',fmtBRL(m.revenue_cents_paid),(m.charges_paid||0)+' pagamentos') +
    k('purple','Sessões ativas',m.sessions_active,(m.msgs_24h||0)+' msgs 24h');
}
function renderRecentCharges(charges){
  var b=document.getElementById('recent-charges');
  if(!charges||!charges.length){b.innerHTML='<tr><td colspan="6" class="empty">Nenhum pagamento ainda.</td></tr>';return;}
  b.innerHTML=charges.slice(0,8).map(function(c){
    var st=c.status==='paid'?'<span class="badge b-active">pago</span>':
           c.status==='pending'?'<span class="badge b-trial">pendente</span>':
           '<span class="badge b-none">'+c.status+'</span>';
    return '<tr><td class="mono meta">'+(c.reference_num||'').slice(0,18)+'</td>'+
      '<td class="domain">'+c.domain+'</td><td>'+planTag(c.plan)+'</td>'+
      '<td>'+fmtBRL(c.amount_cents)+'</td><td>'+st+'</td><td class="meta">'+fmtDate(c.created_at)+'</td></tr>';
  }).join('');
}

// ── TENANTS ──
function renderTenants(){
  var q=(document.getElementById('tenant-search').value||'').toLowerCase();
  var fs=document.getElementById('tenant-filter').value;
  var fp=document.getElementById('tenant-plan-filter').value;
  var body=document.getElementById('tenants-body');
  var list=TENANTS.filter(function(t){
    if(q && t.domain.toLowerCase().indexOf(q)<0)return false;
    if(fs && (t.plan_status||'no_plan')!==fs)return false;
    if(fp && t.plan!==fp)return false;
    return true;
  });
  if(!list.length){body.innerHTML='<tr><td colspan="7" class="empty">Nenhum tenant encontrado.</td></tr>';return;}
  body.innerHTML=list.map(function(t){
    var conn=(t.connections_qr||0)+' QR';
    if(t.connections_cloud)conn+=' · '+t.connections_cloud+' Cloud';
    var tokCls={valid:'tok-valid',expiring:'tok-expiring',expired:'tok-expired'}[t.token_status]||'tok-expired';
    var tokLbl={valid:'válido',expiring:'expirando',expired:'expirado'}[t.token_status]||t.token_status;
    var trial='';
    if(t.plan_status==='trial')trial='<div class="meta">'+(t.trial_days_remaining||0)+'d restantes</div>';
    var d=encodeURIComponent(t.domain);
    return '<tr>'+
      '<td><div class="domain">'+t.domain+'</div><div class="meta">Linha '+(t.open_line_id||'—')+' · desde '+fmtDate(t.installed_at)+'</div></td>'+
      '<td>'+planTag(t.plan)+'</td>'+
      '<td>'+planBadge(t.plan,t.plan_status)+trial+'</td>'+
      '<td>'+conn+'</td>'+
      '<td>'+(t.msgs_24h||0)+'<div class="meta">'+(t.msgs_inbound_24h||0)+'↓ '+(t.msgs_outbound_24h||0)+'↑</div></td>'+
      '<td class="'+tokCls+'">● '+tokLbl+'</td>'+
      '<td style="text-align:right"><div class="actions">'+
        '<button class="btn" onclick="toggleMenu(this)">⋯</button>'+
        '<div class="menu">'+
          '<button onclick="planAct(\''+d+'\',\'activate-pro\')">✅ Ativar Pro</button>'+
          '<button onclick="planActBasic(\''+d+'\')">🔵 Ativar Básico</button>'+
          '<button onclick="planAct(\''+d+'\',\'extend-trial\')">⏰ +7 dias de trial</button>'+
          '<div class="sep"></div>'+
          '<button onclick="planAct(\''+d+'\',\'reactivate\')">↻ Reativar</button>'+
          '<button class="danger" onclick="planAct(\''+d+'\',\'suspend\')">⛔ Suspender</button>'+
          '<div class="sep"></div>'+
          '<button onclick="setToolDomain(\''+d+'\')">🔧 Abrir em Ferramentas</button>'+
        '</div>'+
      '</div></td>'+
    '</tr>';
  }).join('');
}
function toggleMenu(btn){
  var m=btn.nextElementSibling; var wasOpen=m.classList.contains('open');
  document.querySelectorAll('.menu.open').forEach(function(x){x.classList.remove('open');});
  if(!wasOpen)m.classList.add('open');
}
document.addEventListener('click',function(e){
  if(!e.target.closest('.actions'))document.querySelectorAll('.menu.open').forEach(function(x){x.classList.remove('open');});
});

// ── BILLING ──
function renderBilling(charges){
  var b=document.getElementById('billing-body');
  if(!charges||!charges.length){b.innerHTML='<tr><td colspan="8" class="empty">Nenhuma cobrança registrada.</td></tr>';return;}
  b.innerHTML=charges.map(function(c){
    var st=c.status==='paid'?'<span class="badge b-active">pago</span>':
           c.status==='pending'?'<span class="badge b-trial">pendente</span>':
           '<span class="badge b-none">'+c.status+'</span>';
    var bol=c.boleto_url?'<a href="'+c.boleto_url+'" target="_blank">abrir ↗</a>':'—';
    return '<tr><td class="domain">'+c.domain+'</td><td>'+planTag(c.plan)+'</td>'+
      '<td class="meta">'+(c.method||'—')+'</td><td>'+fmtBRL(c.amount_cents)+'</td>'+
      '<td>'+st+'</td><td class="mono meta">'+(c.reference_num||'').slice(0,20)+'</td>'+
      '<td class="meta">'+fmtDate(c.created_at)+'</td><td>'+bol+'</td></tr>';
  }).join('');
}

// ── PLAN ACTIONS ──
function planAct(domain,action){
  var map={
    'activate-pro':{url:'/admin/api/tenant/plan/activate-pro',body:{domain:decodeURIComponent(domain)},confirm:'Ativar plano Pro para '+decodeURIComponent(domain)+'?'},
    'extend-trial':{url:'/admin/api/tenant/plan/extend-trial',body:{domain:decodeURIComponent(domain),days:7},confirm:'Adicionar 7 dias de trial?'},
    'suspend':{url:'/admin/api/tenant/plan/suspend',body:{domain:decodeURIComponent(domain)},confirm:'SUSPENDER o acesso deste tenant?'},
    'reactivate':{url:'/admin/api/tenant/plan/reactivate',body:{domain:decodeURIComponent(domain)},confirm:'Reativar acesso?'}
  };
  var a=map[action]; if(!a)return;
  if(!confirm(a.confirm))return;
  fetch(a.url,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(a.body)})
    .then(function(r){return r.json().then(function(j){return{ok:r.ok,j:j};});})
    .then(function(res){
      if(res.ok){toast('✓ '+action+' aplicado',true);carregarTudo();}
      else toast('✗ '+(res.j.error||'falha'),false);
    }).catch(function(){toast('✗ erro de conexão',false);});
}
function planActBasic(domain){
  if(!confirm('Ativar plano Básico (pago) para '+decodeURIComponent(domain)+'?'))return;
  fetch('/admin/api/tenant/plan',{method:'POST',headers:{'Content-Type':'application/json'},
    body:JSON.stringify({domain:decodeURIComponent(domain),plan:'basic',status:'active'})})
    .then(function(r){return r.json().then(function(j){return{ok:r.ok,j:j};});})
    .then(function(res){ res.ok?(toast('✓ Básico ativado',true),carregarTudo()):toast('✗ '+(res.j.error||'falha'),false);})
    .catch(function(){toast('✗ erro de conexão',false);});
}

// ── TOOLS ──
function setToolDomain(d){ irPara('tools'); document.getElementById('tool-domain').value=decodeURIComponent(d); }
function toolAction(path,method){
  var dom=document.getElementById('tool-domain').value.trim();
  if(!dom){toast('Informe o domínio primeiro',false);return;}
  var url='/admin/api/tenant/'+path+'?domain='+encodeURIComponent(dom);
  runTool(url,method);
}
function globalAction(path,method){
  runTool('/admin/api/'+path,method);
}
function runTool(url,method){
  var out=document.getElementById('tool-output');
  out.style.display='block'; out.textContent='Executando '+method+' '+url+' …';
  fetch(url,{method:method}).then(function(r){return r.text();})
    .then(function(t){ try{out.textContent=JSON.stringify(JSON.parse(t),null,2);}catch(e){out.textContent=t;} toast('✓ executado',true);})
    .catch(function(e){out.textContent='Erro: '+e;toast('✗ falha',false);});
}

// ── LOAD ──
function carregarTudo(){
  fetch('/admin/api/metrics').then(function(r){return r.json();}).then(function(m){
    renderKpis(m);
    document.getElementById('env-pill').textContent=(m.tenants_total||0)+' tenants';
  }).catch(function(){document.getElementById('kpis').innerHTML='<div class="empty">Falha ao carregar métricas.</div>';});

  fetch('/admin/api/tenants').then(function(r){return r.json();}).then(function(d){
    TENANTS=d.tenants||[]; renderTenants();
  }).catch(function(){document.getElementById('tenants-body').innerHTML='<tr><td colspan="7" class="empty">Falha ao carregar tenants.</td></tr>';});

  fetch('/admin/api/billing/charges').then(function(r){return r.json();}).then(function(d){
    renderBilling(d.charges||[]); renderRecentCharges(d.charges||[]);
  }).catch(function(){});
}
carregarTudo();
setInterval(carregarTudo,60000);
</script>
</body>
</html>`
