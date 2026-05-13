package api

// HTML embarcado das páginas /admin/* — separado de admin.go para não poluir.

const adminLoginHTML = `<!doctype html>
<html lang="pt-br">
<head>
<meta charset="utf-8">
<title>Admin — UC Talk</title>
<style>
  body { font-family: -apple-system, system-ui, sans-serif; margin: 0; background: #0f172a; color: #e2e8f0; min-height: 100vh; display: flex; align-items: center; justify-content: center; }
  .card { background: #1e293b; padding: 2.5em 2.5em 2em; border-radius: 10px; width: 360px; box-shadow: 0 10px 40px rgba(0,0,0,0.3); }
  h1 { margin: 0 0 0.5em; font-size: 1.4em; }
  .desc { color: #94a3b8; font-size: 0.9em; margin-bottom: 1.6em; }
  label { display: block; margin-bottom: 0.3em; font-size: 0.85em; color: #cbd5e1; }
  input { width: 100%; padding: 0.6em 0.8em; box-sizing: border-box; background: #0f172a; border: 1px solid #334155; border-radius: 5px; color: #f1f5f9; font-size: 1em; font-family: inherit; }
  input:focus { outline: 0; border-color: #3b82f6; }
  .field { margin-bottom: 1em; }
  button { width: 100%; padding: 0.8em; background: #3b82f6; color: white; border: 0; border-radius: 5px; font-size: 1em; font-weight: 600; cursor: pointer; margin-top: 0.5em; }
  button:hover { background: #2563eb; }
  .err { background: #7f1d1d; color: #fecaca; padding: 0.7em 1em; border-radius: 5px; font-size: 0.88em; margin-bottom: 1em; }
</style>
</head>
<body>
<form class="card" method="post" action="/admin/login">
  <h1>Painel Admin</h1>
  <p class="desc">UC Talk — visualizar todos os portais Bitrix24 que instalaram o app.</p>
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
</form>
</body>
</html>`

const adminHomeHTML = `<!doctype html>
<html lang="pt-br">
<head>
<meta charset="utf-8">
<title>UC Talk — Painel Admin</title>
<style>
  * { box-sizing: border-box; }
  body { font-family: -apple-system, system-ui, sans-serif; margin: 0; background: #f1f5f9; color: #0f172a; }
  header { background: #1e293b; color: #e2e8f0; padding: 1em 2em; display: flex; align-items: center; justify-content: space-between; }
  header h1 { margin: 0; font-size: 1.2em; }
  .top-actions { display: flex; gap: 1em; align-items: center; }
  .badge { background: #334155; color: #cbd5e1; padding: 0.3em 0.8em; border-radius: 999px; font-size: 0.85em; }
  .logout { color: #93c5fd; text-decoration: none; font-size: 0.9em; }
  main { padding: 2em; max-width: 1400px; margin: 0 auto; }
  .summary { display: grid; grid-template-columns: repeat(4, 1fr); gap: 1em; margin-bottom: 1.5em; }
  .summary .box { background: white; padding: 1em 1.2em; border-radius: 8px; box-shadow: 0 1px 3px rgba(0,0,0,0.06); }
  .summary .label { font-size: 0.78em; color: #64748b; text-transform: uppercase; letter-spacing: 0.05em; }
  .summary .value { font-size: 1.8em; font-weight: 700; margin-top: 0.3em; }
  .toolbar { display: flex; gap: 0.5em; margin-bottom: 1em; align-items: center; flex-wrap: wrap; }
  .toolbar input { flex: 1; min-width: 200px; padding: 0.5em 0.8em; border: 1px solid #cbd5e1; border-radius: 5px; font-size: 0.95em; font-family: inherit; }
  .toolbar button { padding: 0.5em 1em; background: white; border: 1px solid #cbd5e1; border-radius: 5px; cursor: pointer; font-size: 0.9em; }
  .toolbar button:hover { background: #f8fafc; }
  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(330px, 1fr)); gap: 1em; }
  .card { background: white; border-radius: 8px; padding: 1.2em; box-shadow: 0 1px 3px rgba(0,0,0,0.06); border-left: 4px solid #3b82f6; }
  .card.expired { border-left-color: #dc2626; }
  .card.expiring { border-left-color: #d97706; }
  .card .domain { font-weight: 600; font-size: 1.05em; margin-bottom: 0.2em; word-break: break-all; }
  .card .meta { font-size: 0.78em; color: #64748b; margin-bottom: 0.9em; }
  .stats { display: grid; grid-template-columns: 1fr 1fr; gap: 0.6em; margin-bottom: 0.8em; }
  .stat { background: #f8fafc; padding: 0.55em 0.8em; border-radius: 5px; }
  .stat .k { font-size: 0.72em; color: #64748b; text-transform: uppercase; }
  .stat .v { font-size: 1.1em; font-weight: 600; margin-top: 0.15em; }
  .conns { display: flex; gap: 0.4em; margin-bottom: 0.7em; }
  .pill { padding: 0.2em 0.7em; border-radius: 999px; font-size: 0.78em; font-weight: 500; }
  .pill.qr { background: #dbeafe; color: #1e40af; }
  .pill.cloud { background: #dcfce7; color: #166534; }
  .pill.none { background: #f1f5f9; color: #94a3b8; }
  .token { font-size: 0.78em; padding: 0.3em 0.6em; border-radius: 4px; display: inline-block; }
  .token.valid { background: #dcfce7; color: #166534; }
  .token.expiring { background: #fef3c7; color: #92400e; }
  .token.expired { background: #fee2e2; color: #991b1b; }
  .loading, .empty { text-align: center; color: #64748b; padding: 3em; }

  /* Menu de acoes no card */
  .card { position: relative; }
  .card-menu-btn { position: absolute; top: 0.7em; right: 0.7em; background: transparent; border: 0; color: #94a3b8; cursor: pointer; padding: 0.25em 0.5em; border-radius: 4px; font-size: 1.1em; line-height: 1; }
  .card-menu-btn:hover { background: #f1f5f9; color: #0f172a; }
  .card-menu { position: absolute; top: 2.4em; right: 0.7em; background: white; border: 1px solid #e2e8f0; border-radius: 6px; box-shadow: 0 4px 12px rgba(0,0,0,0.1); z-index: 10; min-width: 220px; display: none; }
  .card-menu.open { display: block; }
  .card-menu button { width: 100%; text-align: left; padding: 0.6em 0.9em; background: white; border: 0; cursor: pointer; font-size: 0.85em; color: #1e293b; font-family: inherit; }
  .card-menu button:hover { background: #f8fafc; }
  .card-menu button.danger { color: #b91c1c; }
  .card-menu .divider { height: 1px; background: #e2e8f0; margin: 0.2em 0; }

  /* Modal de permissoes */
  .modal-overlay { display: none; position: fixed; inset: 0; background: rgba(15,23,42,.6); z-index: 100; align-items: center; justify-content: center; padding: 1em; }
  .modal-overlay.open { display: flex; }
  .modal-box { background: white; border-radius: 10px; width: 100%; max-width: 720px; max-height: 88vh; display: flex; flex-direction: column; overflow: hidden; box-shadow: 0 20px 60px rgba(0,0,0,0.3); }
  .modal-hdr { padding: 1em 1.4em; border-bottom: 1px solid #e2e8f0; display: flex; justify-content: space-between; align-items: center; }
  .modal-hdr h2 { margin: 0; font-size: 1.1em; }
  .modal-hdr .close { background: none; border: 0; cursor: pointer; font-size: 1.3em; color: #64748b; padding: 0.1em 0.4em; }
  .modal-body { padding: 1em 1.4em; overflow-y: auto; flex: 1; }
  .perm-search { width: 100%; padding: 0.55em 0.8em; border: 1px solid #cbd5e1; border-radius: 5px; font-size: 0.95em; margin-bottom: 0.8em; font-family: inherit; }
  .perm-list { display: flex; flex-direction: column; gap: 0.3em; }
  .perm-row { display: flex; align-items: center; gap: 0.8em; padding: 0.7em 0.9em; border: 1px solid #e2e8f0; border-radius: 6px; }
  .perm-row.granted { border-color: #86efac; background: #f0fdf4; }
  .perm-row .info { flex: 1; min-width: 0; }
  .perm-row .name { font-weight: 600; font-size: 0.95em; }
  .perm-row .email { font-size: 0.78em; color: #64748b; word-break: break-all; }
  .perm-row .pos { font-size: 0.75em; color: #94a3b8; }
  .perm-row .grant-btn { padding: 0.4em 0.9em; border-radius: 5px; cursor: pointer; font-size: 0.85em; font-weight: 600; font-family: inherit; }
  .perm-row .grant-btn.add { background: #2563eb; color: white; border: 0; }
  .perm-row .grant-btn.add:hover { background: #1d4ed8; }
  .perm-row .grant-btn.rm { background: white; color: #b91c1c; border: 1px solid #fecaca; }
  .perm-row .grant-btn.rm:hover { background: #fee2e2; }
  .modal-footer { padding: 0.8em 1.4em; border-top: 1px solid #e2e8f0; font-size: 0.82em; color: #64748b; display: flex; justify-content: space-between; }

  /* Abas */
  .tabs { display: flex; gap: 0.3em; margin-bottom: 1.5em; border-bottom: 1px solid #cbd5e1; }
  .tab { padding: 0.7em 1.4em; background: transparent; border: 0; border-bottom: 2px solid transparent; cursor: pointer; font-size: 0.95em; color: #64748b; font-family: inherit; font-weight: 500; }
  .tab.active { color: #2563eb; border-bottom-color: #2563eb; }
  .tab:hover:not(.active) { color: #0f172a; }
  .tab-content { display: none; }
  .tab-content.active { display: block; }

  /* Stress test */
  fieldset { border: 1px solid #cbd5e1; border-radius: 6px; padding: 1em 1.4em; margin-bottom: 1em; background: white; }
  legend { font-weight: 600; padding: 0 0.5em; color: #0f172a; }
  fieldset label { display: block; margin: 0.6em 0 0.2em; font-size: 0.92em; color: #334155; }
  fieldset input, fieldset select { width: 100%; padding: 0.55em; border: 1px solid #cbd5e1; border-radius: 4px; box-sizing: border-box; font-family: inherit; font-size: 0.95em; }
  .row2 { display: grid; grid-template-columns: 1fr 1fr; gap: 1em; }
  .hint { font-size: 0.82em; color: #64748b; margin-top: 0.3em; }
  #runStressBtn { background: #2563eb; color: white; border: 0; padding: 0.8em 1.6em; border-radius: 5px; font-size: 1em; font-weight: 600; cursor: pointer; }
  #runStressBtn:hover { background: #1d4ed8; }
  #runStressBtn:disabled { background: #94a3b8; cursor: not-allowed; }
  .stress-result { margin-top: 1.5em; }
  .metric-grid { display: grid; grid-template-columns: repeat(4, 1fr); gap: 0.8em; margin: 1em 0; }
  .metric { background: white; padding: 0.8em; border-radius: 6px; text-align: center; box-shadow: 0 1px 3px rgba(0,0,0,0.06); }
  .metric .ml { font-size: 0.78em; color: #64748b; text-transform: uppercase; letter-spacing: 0.05em; }
  .metric .mv { font-size: 1.5em; font-weight: 700; margin-top: 0.2em; }
  .latency-table { width: 100%; border-collapse: collapse; margin-top: 1em; background: white; border-radius: 6px; overflow: hidden; box-shadow: 0 1px 3px rgba(0,0,0,0.06); }
  .latency-table td, .latency-table th { padding: 0.7em 1em; text-align: left; border-bottom: 1px solid #eee; }
  .latency-table th { background: #f8fafc; font-size: 0.85em; }
  pre.json { background: #1e293b; color: #e2e8f0; padding: 1em; border-radius: 6px; overflow-x: auto; font-size: 0.85em; }
  .spinner { display: inline-block; width: 1em; height: 1em; border: 2px solid #fff; border-top-color: transparent; border-radius: 50%; animation: spin 0.7s linear infinite; vertical-align: -0.15em; margin-right: 0.5em; }
  @keyframes spin { to { transform: rotate(360deg); } }
  .ok { color: #16a34a; } .warn-c { color: #d97706; } .err-c { color: #dc2626; }
</style>
</head>
<body>
<header>
  <h1>UC Talk — Painel Admin</h1>
  <div class="top-actions">
    <span class="badge" id="totalBadge">— portais</span>
    <button id="refreshBtn" style="background:#475569;color:white;border:0;padding:0.4em 1em;border-radius:5px;cursor:pointer;font-size:0.85em;">Atualizar</button>
    <button id="flushBtn" style="background:#b91c1c;color:white;border:0;padding:0.4em 1em;border-radius:5px;cursor:pointer;font-size:0.85em;">Limpar filas</button>
    <a href="/admin/logout" class="logout">Sair</a>
  </div>
</header>
<main>
  <div class="tabs">
    <button class="tab active" data-tab="tenants">Portais</button>
    <button class="tab" data-tab="stress">Stress Test</button>
    <button class="tab" data-tab="debug">Debug</button>
  </div>

  <div id="tab-tenants" class="tab-content active">
    <div class="summary" id="summary"></div>
    <div class="toolbar">
      <input type="text" id="search" placeholder="Buscar domínio...">
      <button id="filterAll">Todos</button>
      <button id="filterIssues">Só com problemas</button>
    </div>
    <div id="grid" class="grid">
      <div class="loading">Carregando...</div>
    </div>
  </div>

  <div id="tab-stress" class="tab-content">
    <p style="color:#64748b;margin-top:0;">Dispara N POSTs concorrentes em <code>/bitrix/connector/event</code> simulando o evento <code>ONIMCONNECTORMESSAGEADD</code>. Bate no próprio processo (loopback).</p>
    <form id="stressForm">
      <fieldset>
        <legend>Carga</legend>
        <div class="row2">
          <div>
            <label for="s-concurrent">Conversas simultâneas</label>
            <input type="number" id="s-concurrent" value="50" min="1" max="500">
            <div class="hint">Cada conversa = um chat_id distinto. Max 500.</div>
          </div>
          <div>
            <label for="s-msgs">Msgs por conversa</label>
            <input type="number" id="s-msgs" value="1" min="1" max="50">
            <div class="hint">Sequenciais dentro de cada goroutine. Max 50.</div>
          </div>
        </div>
      </fieldset>
      <fieldset>
        <legend>Alvo</legend>
        <label for="s-connector">Conector</label>
        <select id="s-connector"><option value="">Carregando...</option></select>
        <div class="hint">Lista carregada de <code>bitrix_accounts</code>. QR e Cloud aparecem juntos.</div>
        <label for="s-timeout">Timeout HTTP por request (s)</label>
        <input type="number" id="s-timeout" value="30" min="5" max="120">
      </fieldset>
      <button type="submit" id="runStressBtn">Rodar teste</button>
    </form>
    <div id="stressResult" class="stress-result"></div>
  </div>

  <!-- Modal de Permissoes por Numero -->
  <div id="permModal" class="modal-overlay" onclick="if(event.target===this)closePermissionsModal()">
    <div class="modal-box" style="max-width:780px">
      <div class="modal-hdr">
        <h2>Permissões &mdash; <span id="permDomainLabel"></span></h2>
        <button class="close" onclick="closePermissionsModal()">&times;</button>
      </div>
      <div class="modal-body">
        <!-- Card do master -->
        <div id="permMasterCard" style="background:#fef3c7;border:1px solid #fde68a;border-radius:6px;padding:0.8em 1em;margin-bottom:1em;display:flex;align-items:center;gap:0.8em;flex-wrap:wrap">
          <div style="flex:1;min-width:200px">
            <div style="font-size:0.7em;color:#92400e;font-weight:700;text-transform:uppercase;letter-spacing:.05em">Usuário Master</div>
            <div id="permMasterName" style="font-size:1em;color:#1e293b;font-weight:600;margin-top:0.15em">—</div>
            <div style="font-size:0.78em;color:#78716c;margin-top:0.15em">Recebe wildcard automático. Use <strong>Trocar master</strong> em caso de saída/troca de responsável.</div>
          </div>
          <button id="permMasterBtn" onclick="abrirEscolherMaster()" style="padding:0.5em 1em;background:#0ea5e9;color:white;border:0;border-radius:5px;cursor:pointer;font-size:0.85em;font-weight:600;font-family:inherit">Definir master</button>
        </div>

        <!-- Filtro + lista de internos ativos com chips -->
        <input type="text" id="permFilter" placeholder="Filtrar usuários por nome ou e-mail..." style="width:100%;padding:0.5em 0.7em;border:1px solid #cbd5e1;border-radius:5px;font-size:0.9em;font-family:inherit;margin-bottom:0.7em" oninput="renderAdminPermUsers()">
        <div id="permList" style="max-height:420px;overflow-y:auto;border:1px solid #e2e8f0;border-radius:6px;background:#f8fafc">
          <div class="loading" style="padding:1.5em;text-align:center;color:#94a3b8">Carregando...</div>
        </div>
      </div>
      <div class="modal-footer">
        <span id="permStatus">&mdash;</span>
        <span style="color:#94a3b8">Super-admin: pode editar qualquer tenant.</span>
      </div>
    </div>
  </div>

  <!-- Sub-modal: escolher master -->
  <div id="permMasterModal" class="modal-overlay" onclick="if(event.target===this)fecharEscolherMaster()" style="z-index:2000">
    <div class="modal-box" style="max-width:520px">
      <div class="modal-hdr">
        <h2>Definir usuário master</h2>
        <button class="close" onclick="fecharEscolherMaster()">&times;</button>
      </div>
      <div class="modal-body">
        <div style="background:#fef2f2;border:1px solid #fecaca;border-radius:6px;padding:0.7em 1em;margin-bottom:0.8em;font-size:0.85em;color:#991b1b">
          <strong>⚠</strong> O master tem wildcard automático. Se já existir um master, ele <strong>perde a permissão</strong> ao ser substituído.
        </div>
        <input type="text" id="permMasterFilter" placeholder="Filtrar..." style="width:100%;padding:0.5em 0.7em;border:1px solid #cbd5e1;border-radius:5px;font-size:0.9em;font-family:inherit;margin-bottom:0.7em" oninput="renderEscolherMasterList()">
        <div id="permMasterList" style="max-height:340px;overflow-y:auto;border:1px solid #e2e8f0;border-radius:6px;background:#f8fafc">
          <div style="padding:1.5em;text-align:center;color:#94a3b8">Selecione um usuário...</div>
        </div>
      </div>
    </div>
  </div>

  <div id="tab-debug" class="tab-content">
    <p style="color:#64748b;margin-top:0;">Dump diagnóstico das tabelas-chave (contagens + amostras). Use para identificar quando o painel mostra zeros e você não sabe onde está quebrando.</p>
    <div style="display:flex;gap:0.5em;flex-wrap:wrap;margin-bottom:1em;">
      <button id="reloadDebug" style="padding:0.5em 1em;background:white;border:1px solid #cbd5e1;border-radius:5px;cursor:pointer;">Recarregar</button>
      <button id="cleanBanned" style="padding:0.5em 1em;background:#fef3c7;border:1px solid #fde68a;border-radius:5px;cursor:pointer;color:#92400e;">Limpar sessões banned</button>
      <button id="cleanPlaceholders" style="padding:0.5em 1em;background:#fee2e2;border:1px solid #fecaca;border-radius:5px;cursor:pointer;color:#991b1b;">Limpar portais placeholder</button>
      <button id="cleanSessionFiles" style="padding:0.5em 1em;background:#fee2e2;border:1px solid #fecaca;border-radius:5px;cursor:pointer;color:#991b1b;">Limpar arquivos de sessão (.db)</button>
      <button id="cleanLegacyMsgs" style="padding:0.5em 1em;background:#fee2e2;border:1px solid #fecaca;border-radius:5px;cursor:pointer;color:#991b1b;">Limpar msgs legacy</button>
    </div>
    <pre class="json" id="debugOut">Carregando...</pre>
  </div>
</main>

<script>
let allTenants = [];
let filterMode = 'all';

async function load() {
  document.getElementById('grid').innerHTML = '<div class="loading">Carregando...</div>';
  try {
    const r = await fetch('/admin/api/tenants');
    if (r.status === 401 || r.redirected) { window.location = '/admin/login'; return; }
    const data = await r.json();
    if (!r.ok) {
      document.getElementById('grid').innerHTML = '<div class="empty">Erro: ' + (data.error || r.status) + '</div>';
      return;
    }
    allTenants = data.tenants || [];
    renderSummary();
    renderGrid();
  } catch (e) {
    document.getElementById('grid').innerHTML = '<div class="empty">Erro de rede: ' + e.message + '</div>';
  }
}

function renderSummary() {
  const t = allTenants;
  const total = t.length;
  const expired = t.filter(x => x.token_status === 'expired').length;
  const expiring = t.filter(x => x.token_status === 'expiring').length;
  const totalMsgs24h = t.reduce((s, x) => s + (x.msgs_24h || 0), 0);
  const totalConn = t.reduce((s, x) => s + (x.connections_qr || 0) + (x.connections_cloud || 0), 0);

  document.getElementById('totalBadge').textContent = total + ' portais';
  document.getElementById('summary').innerHTML = [
    box('Portais', total),
    box('Conexões WA', totalConn),
    box('Msgs (24h)', totalMsgs24h.toLocaleString('pt-BR')),
    box('Tokens c/ problema', (expired + expiring), expired > 0 ? 'err' : (expiring > 0 ? 'warn' : '')),
  ].join('');
}

function box(label, value, cls) {
  const color = cls === 'err' ? '#dc2626' : (cls === 'warn' ? '#d97706' : '#0f172a');
  return '<div class="box"><div class="label">' + label + '</div><div class="value" style="color:' + color + '">' + value + '</div></div>';
}

function renderGrid() {
  const q = (document.getElementById('search').value || '').toLowerCase();
  let list = allTenants;
  if (q) list = list.filter(t => t.domain.toLowerCase().includes(q));
  if (filterMode === 'issues') list = list.filter(t => t.token_status !== 'valid' || (t.connections_qr + t.connections_cloud) === 0);

  if (list.length === 0) {
    document.getElementById('grid').innerHTML = '<div class="empty">Nenhum portal encontrado.</div>';
    return;
  }
  document.getElementById('grid').innerHTML = list.map(cardHTML).join('');
}

function cardHTML(t) {
  const cls = t.token_status === 'expired' ? 'expired' : (t.token_status === 'expiring' ? 'expiring' : '');
  const conns = [];
  if (t.connections_qr > 0) conns.push('<span class="pill qr">' + t.connections_qr + ' Multi-Device</span>');
  if (t.connections_cloud > 0) conns.push('<span class="pill cloud">' + t.connections_cloud + ' Oficial</span>');
  if (conns.length === 0) conns.push('<span class="pill none">Sem conexão</span>');

  const installed = new Date(t.installed_at).toLocaleDateString('pt-BR');
  const tokenLabel = t.token_status === 'valid' ? 'Token válido' : (t.token_status === 'expiring' ? 'Token expirando' : 'Token expirado');

  const domainAttr = encodeURIComponent(t.domain);
  return '' +
    '<div class="card ' + cls + '" data-domain="' + escapeHTML(t.domain) + '">' +
      '<button class="card-menu-btn" onclick="toggleCardMenu(event, this)" title="Ações">&#8942;</button>' +
      '<div class="card-menu">' +
        '<button onclick="openPermissionsModal(\'' + domainAttr + '\')">Gerenciar permissões CRM</button>' +
        '<div class="divider"></div>' +
        '<button onclick="tenantAction(\'legacy-messages\',\'' + domainAttr + '\', this)">Limpar msgs legacy</button>' +
        '<button class="danger" onclick="tenantAction(\'session-files\',\'' + domainAttr + '\', this)">Limpar arquivos .db (QR)</button>' +
      '</div>' +
      '<div class="domain">' + escapeHTML(t.domain) + '</div>' +
      '<div class="meta">Instalado em ' + installed + ' &middot; Open Line ' + (t.open_line_id || '—') + '</div>' +
      '<div class="conns">' + conns.join('') + '</div>' +
      '<div class="stats">' +
        '<div class="stat"><div class="k">Msgs 24h</div><div class="v">' + (t.msgs_24h || 0).toLocaleString('pt-BR') + '</div></div>' +
        '<div class="stat"><div class="k">Msgs 1h</div><div class="v">' + (t.msgs_1h || 0) + '</div></div>' +
        '<div class="stat"><div class="k">Recebidas 24h</div><div class="v">' + (t.msgs_inbound_24h || 0).toLocaleString('pt-BR') + '</div></div>' +
        '<div class="stat"><div class="k">Enviadas 24h</div><div class="v">' + (t.msgs_outbound_24h || 0).toLocaleString('pt-BR') + '</div></div>' +
      '</div>' +
      '<span class="token ' + t.token_status + '">' + tokenLabel + '</span>' +
    '</div>';
}

function toggleCardMenu(ev, btn) {
  ev.stopPropagation();
  const menu = btn.parentElement.querySelector('.card-menu');
  // fecha outros menus abertos
  document.querySelectorAll('.card-menu.open').forEach(m => { if (m !== menu) m.classList.remove('open'); });
  menu.classList.toggle('open');
}

// fecha menu ao clicar fora
document.addEventListener('click', (ev) => {
  if (!ev.target.closest('.card-menu') && !ev.target.classList.contains('card-menu-btn')) {
    document.querySelectorAll('.card-menu.open').forEach(m => m.classList.remove('open'));
  }
});

async function tenantAction(action, domainEnc, btn) {
  const domain = decodeURIComponent(domainEnc);
  let confirmMsg, endpoint;
  if (action === 'legacy-messages') {
    confirmMsg = 'Apagar msgs legacy (from_jid/to_jid vazio ou "cloud@s.whatsapp.net") do tenant\n\n' + domain + '?';
    endpoint = '/admin/api/tenant/cleanup/legacy-messages?domain=' + domainEnc;
  } else if (action === 'session-files') {
    confirmMsg = 'Apagar arquivos .db dos telefones QR (Multi-Device) deste tenant?\n\n' + domain + '\n\nO cliente vai precisar escanear o QR novamente!';
    endpoint = '/admin/api/tenant/cleanup/session-files?domain=' + domainEnc;
  } else {
    return;
  }
  if (!confirm(confirmMsg)) return;
  btn.disabled = true;
  try {
    const r = await fetch(endpoint, { method: 'POST' });
    if (r.status === 401) { window.location = '/admin/login'; return; }
    const data = await r.json();
    if (!r.ok) { alert('Erro: ' + (data.error || r.status)); return; }
    if (action === 'legacy-messages') {
      alert((data.deleted || 0) + ' mensagens removidas para ' + domain);
    } else {
      const mb = ((data.bytes_freed || 0) / 1024 / 1024).toFixed(2);
      alert((data.count || 0) + ' arquivos removidos (' + mb + ' MB)\nTelefones: ' + (data.phones || []).join(', '));
    }
    load(); // recarrega cards
  } catch (e) {
    alert('Erro de rede: ' + e.message);
  } finally {
    btn.disabled = false;
    document.querySelectorAll('.card-menu.open').forEach(m => m.classList.remove('open'));
  }
}

function escapeHTML(s) {
  return String(s).replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
}

document.getElementById('refreshBtn').addEventListener('click', load);
document.getElementById('search').addEventListener('input', renderGrid);
document.getElementById('filterAll').addEventListener('click', () => { filterMode = 'all'; renderGrid(); });
document.getElementById('filterIssues').addEventListener('click', () => { filterMode = 'issues'; renderGrid(); });

document.getElementById('flushBtn').addEventListener('click', async () => {
  const choice = prompt('Limpar quais filas? Digite: outbound, inbound, dead — ou "tudo" para os 3.\n\nA fila INBOUND nao deveria ser limpa em producao (msgs reais de clientes).', 'outbound');
  if (!choice) return;
  let kinds;
  if (choice.toLowerCase() === 'tudo') kinds = ['inbound','outbound','dead'];
  else kinds = choice.split(',').map(s => s.trim()).filter(Boolean);
  if (kinds.length === 0) return;
  if (!confirm('Confirma limpar: ' + kinds.join(', ') + '?')) return;
  try {
    const r = await fetch('/admin/api/queue/flush', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ kinds: kinds })
    });
    const data = await r.json();
    if (!r.ok) { alert('Erro: ' + (data.error || r.status)); return; }
    alert('Removidos:\n' + Object.entries(data.removed).map(([k,v]) => '  ' + k + ': ' + v).join('\n'));
    load();
  } catch (e) {
    alert('Erro de rede: ' + e.message);
  }
});

// ─── Abas ────────────────────────────────────────────────────────────────
document.querySelectorAll('.tab').forEach(tab => {
  tab.addEventListener('click', () => {
    const target = tab.dataset.tab;
    document.querySelectorAll('.tab').forEach(t => t.classList.toggle('active', t === tab));
    document.querySelectorAll('.tab-content').forEach(c => c.classList.toggle('active', c.id === 'tab-' + target));
    if (target === 'stress' && !stressConnectorsLoaded) loadStressConnectors();
    if (target === 'debug') loadDebug();
  });
});

// ─── Stress Test ─────────────────────────────────────────────────────────
let stressConnectorsLoaded = false;
async function loadStressConnectors() {
  const sel = document.getElementById('s-connector');
  try {
    const r = await fetch('/stress-test/connectors');
    if (r.status === 401) { window.location = '/admin/login'; return; }
    const data = await r.json();
    if (!r.ok || !data.connectors) {
      sel.innerHTML = '<option value="">Erro: ' + (data.error || 'falha') + '</option>';
      return;
    }
    if (data.connectors.length === 0) {
      sel.innerHTML = '<option value="">(nenhum conector cadastrado)</option>';
      return;
    }
    sel.innerHTML = data.connectors.map(c => {
      const kindLabel = c.kind === 'cloud' ? 'Oficial' : 'Multi-Device';
      const label = '[' + kindLabel + '] ' + c.connector_id + ' — line ' + c.line;
      const val = c.connector_id + '|' + c.line;
      return '<option value="' + val + '">' + escapeHTML(label) + '</option>';
    }).join('');
    stressConnectorsLoaded = true;
  } catch (err) {
    sel.innerHTML = '<option value="">Erro de rede: ' + err.message + '</option>';
  }
}

document.getElementById('stressForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  const sel = document.getElementById('s-connector');
  const btn = document.getElementById('runStressBtn');
  const result = document.getElementById('stressResult');
  const sv = sel.value;
  if (!sv || !sv.includes('|')) {
    result.innerHTML = '<p class="err-c"><strong>Selecione um conector válido.</strong></p>';
    return;
  }
  const [connector, lineStr] = sv.split('|');
  btn.disabled = true;
  btn.innerHTML = '<span class="spinner"></span> Rodando...';
  result.innerHTML = '<p style="color:#64748b">Disparando requisições... aguarde.</p>';

  const body = {
    concurrent: parseInt(document.getElementById('s-concurrent').value, 10),
    msgs_per_conv: parseInt(document.getElementById('s-msgs').value, 10),
    connector: connector,
    line: parseInt(lineStr, 10),
    timeout_sec: parseInt(document.getElementById('s-timeout').value, 10),
  };
  try {
    const r = await fetch('/stress-test/run', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    if (r.status === 401) { window.location = '/admin/login'; return; }
    const data = await r.json();
    if (!r.ok) {
      result.innerHTML = '<p class="err-c"><strong>Erro:</strong> ' + (data.error || r.status) + '</p>';
      return;
    }
    renderStressResult(data);
  } catch (err) {
    result.innerHTML = '<p class="err-c"><strong>Erro de rede:</strong> ' + err.message + '</p>';
  } finally {
    btn.disabled = false;
    btn.textContent = 'Rodar teste';
  }
});

function renderStressResult(d) {
  const result = document.getElementById('stressResult');
  const successPct = (100 * d.success / d.total).toFixed(1);
  const pctClass = d.success === d.total ? 'ok' : (successPct >= 95 ? 'warn-c' : 'err-c');
  const lat = d.latency_ms || {};
  const elapsed = (d.elapsed_ms / 1000).toFixed(2);
  const tput = d.throughput.toFixed(1);

  let html = '<h3>Resultado</h3>';
  html += '<div class="metric-grid">';
  html += sMetric('Tempo total', elapsed + 's');
  html += sMetric('Throughput', tput + ' req/s');
  html += sMetric('Sucesso', successPct + '%', pctClass);
  html += sMetric('Total', d.total);
  html += '</div>';

  if (d.latency_ms) {
    html += '<h4>Latência</h4>';
    html += '<table class="latency-table">';
    html += '<tr><th>min</th><th>avg</th><th>p50</th><th>p95</th><th>p99</th><th>max</th></tr>';
    html += '<tr>';
    html += '<td>' + lat.min + ' ms</td><td>' + lat.avg + ' ms</td><td>' + lat.p50 + ' ms</td>';
    html += '<td>' + lat.p95 + ' ms</td><td>' + lat.p99 + ' ms</td><td>' + lat.max + ' ms</td>';
    html += '</tr></table>';
  }
  html += '<h4>Status HTTP</h4>';
  html += '<pre class="json">' + JSON.stringify(d.status_counts, null, 2) + '</pre>';
  if (d.errors > 0) {
    html += '<h4 class="err-c">Erros (' + d.errors + ')</h4>';
    html += '<pre class="json">' + (d.first_errors || []).join('\n') + '</pre>';
  }
  result.innerHTML = html;
}

function sMetric(label, value, cls) {
  return '<div class="metric"><div class="ml">' + label + '</div><div class="mv ' + (cls||'') + '">' + value + '</div></div>';
}

// ─── Debug ───────────────────────────────────────────────────────────────
async function loadDebug() {
  const out = document.getElementById('debugOut');
  out.textContent = 'Carregando...';
  try {
    const r = await fetch('/admin/api/debug');
    if (r.status === 401) { window.location = '/admin/login'; return; }
    const data = await r.json();
    out.textContent = JSON.stringify(data, null, 2);
  } catch (err) {
    out.textContent = 'Erro de rede: ' + err.message;
  }
}
document.getElementById('reloadDebug').addEventListener('click', loadDebug);

async function cleanupAction(endpoint, label) {
  if (!confirm('Confirma: ' + label + '?')) return;
  try {
    const r = await fetch(endpoint, { method: 'POST' });
    if (r.status === 401) { window.location = '/admin/login'; return; }
    const data = await r.json();
    if (!r.ok) { alert('Erro: ' + (data.error || r.status)); return; }
    alert(label + ': ' + (data.deleted || 0) + ' removidos.');
    loadDebug();
    load(); // atualiza tambem o card de Portais
  } catch (e) { alert('Erro de rede: ' + e.message); }
}
document.getElementById('cleanBanned').addEventListener('click',
  () => cleanupAction('/admin/api/cleanup/banned-sessions', 'Limpar sessões banned'));
document.getElementById('cleanPlaceholders').addEventListener('click',
  () => cleanupAction('/admin/api/cleanup/placeholder-portals', 'Limpar portais placeholder'));

document.getElementById('cleanLegacyMsgs').addEventListener('click',
  () => cleanupAction('/admin/api/cleanup/legacy-messages',
    'Apagar mensagens com from_jid/to_jid vazio ou "cloud@s.whatsapp.net" (lixo historico)'));

document.getElementById('cleanSessionFiles').addEventListener('click', async () => {
  if (!confirm('Apagar arquivos .db de sessoes inativas/orfaas?\n\nIsso vai apagar:\n- .db de sessoes Cloud (que nao deveriam ter)\n- .db de sessoes nao-active no banco\n- .db-shm/.db-wal sem .db principal\n\nSessoes Multi-Device ATIVAS sao preservadas.')) return;
  try {
    const r = await fetch('/admin/api/cleanup/session-files', { method: 'POST' });
    if (r.status === 401) { window.location = '/admin/login'; return; }
    const data = await r.json();
    if (!r.ok) { alert('Erro: ' + (data.error || r.status)); return; }
    const mb = (data.bytes_freed / 1024 / 1024).toFixed(2);
    alert('Removidos: ' + data.count + ' arquivos (' + mb + ' MB liberados)\n\n' + (data.removed||[]).join('\n'));
    loadDebug();
  } catch (e) { alert('Erro de rede: ' + e.message); }
});

// ─── Modal de Permissoes CRM ─────────────────────────────────────────────
// ─── Permissoes por Numero (admin master) ──────────────────────────────
let _permDomain = '';
let _permState = { master_user_id:'', master_user_name:'', users:[], sessions:[] };
let _permLoading = {};

async function openPermissionsModal(domainEnc) {
  const domain = decodeURIComponent(domainEnc);
  _permDomain = domain;
  document.getElementById('permDomainLabel').textContent = domain;
  document.getElementById('permList').innerHTML = '<div class="loading" style="padding:1.5em;text-align:center;color:#94a3b8">Carregando usuários do Bitrix...</div>';
  document.getElementById('permFilter').value = '';
  document.getElementById('permMasterName').textContent = '—';
  document.getElementById('permStatus').textContent = '—';
  document.getElementById('permModal').classList.add('open');
  document.querySelectorAll('.card-menu.open').forEach(m => m.classList.remove('open'));
  await loadPermState();
}

function closePermissionsModal() {
  document.getElementById('permModal').classList.remove('open');
  _permDomain = '';
  _permState = { master_user_id:'', master_user_name:'', users:[], sessions:[] };
}

async function loadPermState() {
  try {
    const r = await fetch('/admin/api/tenant/master?domain=' + encodeURIComponent(_permDomain));
    if (r.status === 401) { window.location = '/admin/login'; return; }
    const data = await r.json();
    if (!r.ok) {
      document.getElementById('permList').innerHTML = '<div class="empty" style="padding:1.5em;text-align:center;color:#dc2626">Erro: ' + escapeHTML(data.error || r.status) + '</div>';
      return;
    }
    _permState = {
      master_user_id: data.master_user_id || '',
      master_user_name: data.master_user_name || '',
      users: data.users || [],
      sessions: data.sessions || [],
    };
    renderAdminMasterCard();
    renderAdminPermUsers();
  } catch (e) {
    document.getElementById('permList').innerHTML = '<div class="empty" style="padding:1.5em;text-align:center;color:#dc2626">Erro de rede: ' + escapeHTML(e.message) + '</div>';
  }
}

function renderAdminMasterCard() {
  const nameEl = document.getElementById('permMasterName');
  const btn = document.getElementById('permMasterBtn');
  if (!_permState.master_user_id) {
    nameEl.innerHTML = '<span style="color:#92400e">Não configurado</span>';
    btn.textContent = 'Definir master';
  } else {
    const lbl = _permState.master_user_name || ('User #' + _permState.master_user_id);
    nameEl.innerHTML = escapeHTML(lbl) + ' <span style="color:#94a3b8;font-weight:400;font-size:0.85em">#' + escapeHTML(_permState.master_user_id) + '</span>';
    btn.textContent = 'Trocar master';
  }
}

function renderAdminPermUsers() {
  const box = document.getElementById('permList');
  const q = (document.getElementById('permFilter').value || '').trim().toLowerCase();
  const sessions = _permState.sessions;
  let list = _permState.users;
  if (q) {
    list = list.filter(u =>
      (u.name||'').toLowerCase().indexOf(q) !== -1
      || (u.email||'').toLowerCase().indexOf(q) !== -1
      || String(u.id||'').indexOf(q) !== -1
    );
  }

  const totalGranted = _permState.users.filter(u => (u.allowed_sessions||[]).length > 0).length;
  document.getElementById('permStatus').textContent =
    _permState.users.length + ' interno(s) · ' + totalGranted + ' com pelo menos 1 número · ' + sessions.length + ' número(s)';

  if (!list.length) {
    box.innerHTML = '<div class="empty" style="padding:1.5em;text-align:center;color:#94a3b8;font-size:0.9em">Nenhum usuário casou.</div>';
    return;
  }
  if (!sessions.length) {
    box.innerHTML = '<div class="empty" style="padding:1.5em;text-align:center;color:#94a3b8;font-size:0.9em">Nenhuma sessão configurada neste tenant.</div>';
    return;
  }

  let html = '';
  for (let i = 0; i < list.length; i++) {
    const u = list[i];
    const ini = (u.name||'?')[0].toUpperCase();
    const allowedSet = {};
    (u.allowed_sessions||[]).forEach(jid => { allowedSet[jid] = true; });
    const hasWildcard = !!allowedSet[''];
    const isMaster = u.id === _permState.master_user_id;

    let chips = '';
    for (let j = 0; j < sessions.length; j++) {
      const s = sessions[j];
      const on = hasWildcard || !!allowedSet[s.jid];
      const bg = on ? '#bbf7d0' : '#f1f5f9';
      const fg = on ? '#166534' : '#475569';
      const border = on ? '1px solid #86efac' : '1px solid #cbd5e1';
      const ico = on ? '✓' : '+';
      const tipo = s.type === 'cloud_api' ? ' Cloud' : ' QR';
      const lbl = '+' + (s.phone || s.jid) + tipo;
      const clickable = !isMaster;
      const click = clickable ? ('onclick="adminPermToggle(\'' + escapeHTML(u.id) + '\', \'' + escapeHTML(u.name||'') + '\', \'' + escapeHTML(s.jid) + '\', ' + (on?'true':'false') + ')"') : '';
      const cursor = clickable ? 'pointer' : 'not-allowed';
      const opacity = clickable ? '1' : '.65';
      chips += '<button ' + click + ' style="display:inline-flex;align-items:center;gap:4px;padding:3px 9px;border-radius:12px;background:' + bg + ';color:' + fg + ';border:' + border + ';font-size:0.78em;font-weight:600;cursor:' + cursor + ';margin:2px;opacity:' + opacity + ';font-family:inherit">'
            +    '<span style="font-weight:700">' + ico + '</span> ' + escapeHTML(lbl)
            +  '</button>';
    }

    const masterBadge = isMaster
      ? '<span style="display:inline-block;font-size:0.7em;color:#92400e;background:#fef3c7;padding:1px 7px;border-radius:8px;font-weight:700;margin-left:0.4em">★ MASTER · TODOS</span>'
      : (hasWildcard ? '<span style="display:inline-block;font-size:0.7em;color:#92400e;background:#fef3c7;padding:1px 7px;border-radius:8px;font-weight:700;margin-left:0.4em">LEGACY: TODOS</span>' : '');

    html += '<div style="padding:0.7em 0.9em;border-bottom:1px solid #e2e8f0">'
         +    '<div style="display:flex;align-items:center;gap:0.6em;margin-bottom:0.3em">'
         +      '<div style="width:26px;height:26px;border-radius:50%;background:#dbeafe;color:#1e40af;display:flex;align-items:center;justify-content:center;font-size:0.78em;font-weight:700">' + escapeHTML(ini) + '</div>'
         +      '<div style="flex:1;min-width:0">'
         +        '<div style="font-size:0.9em;color:#1e293b;font-weight:600">' + escapeHTML(u.name||('User #'+u.id)) + ' <span style="color:#94a3b8;font-weight:400;font-size:0.85em">#' + escapeHTML(u.id) + '</span>' + masterBadge + '</div>'
         +        (u.email ? '<div style="font-size:0.75em;color:#64748b">' + escapeHTML(u.email) + '</div>' : '')
         +      '</div>'
         +    '</div>'
         +    '<div style="padding-left:34px">' + chips + '</div>'
         +  '</div>';
  }
  box.innerHTML = html;
}

async function adminPermToggle(userID, userName, sessionJID, isOn) {
  const key = userID + '|' + sessionJID;
  if (_permLoading[key]) return;
  _permLoading[key] = true;
  try {
    const r = await fetch('/admin/api/tenant/permissions?domain=' + encodeURIComponent(_permDomain), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        user_id: userID,
        user_name: userName,
        session_jid: sessionJID,
        action: isOn ? 'revoke' : 'grant',
      }),
    });
    if (r.status === 401) { window.location = '/admin/login'; return; }
    const data = await r.json();
    if (!r.ok) { alert('Erro: ' + (data.error || r.status)); return; }
    // Atualiza estado local
    const u = _permState.users.find(x => x.id === userID);
    if (u) {
      const set = (u.allowed_sessions||[]).filter(j => j !== sessionJID);
      if (!isOn) set.push(sessionJID);
      u.allowed_sessions = set;
    }
    renderAdminPermUsers();
  } catch (e) {
    alert('Erro de rede: ' + e.message);
  } finally {
    delete _permLoading[key];
  }
}

// ─── Sub-modal: escolher master ────────────────────────────────────────
function abrirEscolherMaster() {
  document.getElementById('permMasterFilter').value = '';
  document.getElementById('permMasterModal').classList.add('open');
  renderEscolherMasterList();
}
function fecharEscolherMaster() {
  document.getElementById('permMasterModal').classList.remove('open');
}
function renderEscolherMasterList() {
  const box = document.getElementById('permMasterList');
  const q = (document.getElementById('permMasterFilter').value || '').trim().toLowerCase();
  let list = _permState.users;
  if (q) {
    list = list.filter(u =>
      (u.name||'').toLowerCase().indexOf(q) !== -1
      || (u.email||'').toLowerCase().indexOf(q) !== -1
      || String(u.id||'').indexOf(q) !== -1
    );
  }
  if (!list.length) {
    box.innerHTML = '<div style="padding:1.5em;text-align:center;color:#94a3b8;font-size:0.9em">Nenhum colaborador interno ativo.</div>';
    return;
  }
  let html = '';
  for (let i = 0; i < list.length; i++) {
    const u = list[i];
    const ini = (u.name||'?')[0].toUpperCase();
    const isCurrent = u.id === _permState.master_user_id;
    html += '<div class="pm-row" data-uid="' + escapeHTML(u.id) + '" data-uname="' + escapeHTML(u.name||'') + '" '
         +  'style="display:flex;align-items:center;gap:0.6em;padding:0.6em 0.9em;border-bottom:1px solid #e2e8f0;cursor:' + (isCurrent?'default':'pointer') + ';' + (isCurrent?'opacity:.55;':'') + '">'
         +    '<div style="width:26px;height:26px;border-radius:50%;background:#dbeafe;color:#1e40af;display:flex;align-items:center;justify-content:center;font-size:0.78em;font-weight:700">' + escapeHTML(ini) + '</div>'
         +    '<div style="flex:1;min-width:0">'
         +      '<div style="font-size:0.9em;color:#1e293b;font-weight:600">' + escapeHTML(u.name||('User #'+u.id)) + ' <span style="color:#94a3b8;font-weight:400;font-size:0.85em">#' + escapeHTML(u.id) + '</span>' + (isCurrent?' <span style="color:#92400e;font-size:0.75em">(atual)</span>':'') + '</div>'
         +      (u.email ? '<div style="font-size:0.75em;color:#64748b">' + escapeHTML(u.email) + '</div>' : '')
         +    '</div>'
         +  '</div>';
  }
  box.innerHTML = html;
  box.querySelectorAll('.pm-row').forEach(row => {
    row.addEventListener('click', async () => {
      const uid = row.dataset.uid, uname = row.dataset.uname;
      if (uid === _permState.master_user_id) return;
      await confirmarSetMaster(uid, uname);
    });
  });
}
async function confirmarSetMaster(userID, userName) {
  const wasConfigured = !!_permState.master_user_id;
  const msg = wasConfigured
    ? 'Trocar master para "' + userName + '"?\n\nO master atual perde a permissão wildcard.'
    : 'Definir "' + userName + '" como master?\n\nEle recebe wildcard automático (libera todos os números).';
  if (!confirm(msg)) return;
  try {
    const r = await fetch('/admin/api/tenant/master?domain=' + encodeURIComponent(_permDomain), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ user_id: userID, user_name: userName }),
    });
    if (r.status === 401) { window.location = '/admin/login'; return; }
    const data = await r.json();
    if (!r.ok) { alert('Erro: ' + (data.error || r.status)); return; }
    fecharEscolherMaster();
    await loadPermState();
  } catch (e) {
    alert('Erro de rede: ' + e.message);
  }
}

load();
// auto-refresh da aba de portais a cada 60s
setInterval(() => {
  if (document.getElementById('tab-tenants').classList.contains('active')) load();
}, 60000);
</script>
</body>
</html>`
