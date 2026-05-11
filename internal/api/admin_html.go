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
    <label for="user">Usu&aacute;rio</label>
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
  <div class="summary" id="summary"></div>

  <div class="toolbar">
    <input type="text" id="search" placeholder="Buscar dom&iacute;nio...">
    <button id="filterAll">Todos</button>
    <button id="filterIssues">S&oacute; com problemas</button>
  </div>

  <div id="grid" class="grid">
    <div class="loading">Carregando...</div>
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
    box('Conex&otilde;es WA', totalConn),
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
  if (t.connections_qr > 0) conns.push('<span class="pill qr">' + t.connections_qr + ' QR</span>');
  if (t.connections_cloud > 0) conns.push('<span class="pill cloud">' + t.connections_cloud + ' Cloud</span>');
  if (conns.length === 0) conns.push('<span class="pill none">Sem conex&atilde;o</span>');

  const installed = new Date(t.installed_at).toLocaleDateString('pt-BR');
  const tokenLabel = t.token_status === 'valid' ? 'Token v&aacute;lido' : (t.token_status === 'expiring' ? 'Token expirando' : 'Token expirado');

  return '' +
    '<div class="card ' + cls + '">' +
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

load();
// auto-refresh a cada 60s
setInterval(load, 60000);
</script>
</body>
</html>`
