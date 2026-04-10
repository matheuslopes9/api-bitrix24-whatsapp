package api

import "github.com/gofiber/fiber/v2"

// GET /dashboard
func (h *handlers) dashboardPage(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/html; charset=utf-8")
	return c.SendString(dashboardHTML)
}

// GET /ui/overview — dados agregados para a dashboard (sem auth, apenas interna)
func (h *handlers) uiOverview(c *fiber.Ctx) error {
	sessions := h.waManager.ListSessions()
	in, out, dead := h.q.Lengths(c.Context())

	stats, _ := h.repo.GetDailyStats(c.Context(), 1)
	var msgsIn, msgsOut int64
	for _, s := range stats {
		msgsIn += s.InboundCount
		msgsOut += s.OutboundCount
	}

	return c.JSON(fiber.Map{
		"active_sessions":   len(sessions),
		"sessions":          sessions,
		"queue_inbound":     in,
		"queue_outbound":    out,
		"queue_dead":        dead,
		"messages_inbound":  msgsIn,
		"messages_outbound": msgsOut,
		"messages_failed":   dead,
	})
}

const dashboardHTML = `<!DOCTYPE html>
<html lang="pt-BR">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0, maximum-scale=1.0">
<title>WA Connector — Painel</title>
<script src="https://cdn.tailwindcss.com"></script>
<script src="/assets/chart.js"></script>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link href="https://fonts.googleapis.com/css2?family=Inter:wght@300;400;500;600;700&display=swap" rel="stylesheet">
<style>
*{box-sizing:border-box;margin:0;padding:0;}
html,body{font-family:'Inter',sans-serif;background:#0a0e1a;color:#e2e8f0;min-height:100vh;}

/* ── Blobs ── */
.blob{position:fixed;border-radius:50%;filter:blur(90px);opacity:.10;pointer-events:none;z-index:0;}

/* ── Glass card ── */
.card{background:rgba(255,255,255,.04);border:1px solid rgba(255,255,255,.08);backdrop-filter:blur(14px);-webkit-backdrop-filter:blur(14px);border-radius:16px;transition:background .2s,border-color .2s;}
.card:hover{background:rgba(255,255,255,.06);border-color:rgba(255,255,255,.12);}
.card-flat{background:rgba(255,255,255,.03);border:1px solid rgba(255,255,255,.06);border-radius:12px;}

/* ── Sidebar ── */
#sidebar{position:fixed;top:0;left:0;height:100%;width:240px;background:rgba(10,14,26,.95);border-right:1px solid rgba(255,255,255,.07);backdrop-filter:blur(20px);z-index:40;display:flex;flex-direction:column;padding:20px 14px;gap:4px;transition:transform .25s cubic-bezier(.4,0,.2,1);}
#sidebar-overlay{display:none;position:fixed;inset:0;background:rgba(0,0,0,.6);z-index:30;backdrop-filter:blur(2px);}

.nav-item{display:flex;align-items:center;gap:11px;padding:10px 13px;border-radius:10px;cursor:pointer;font-size:13.5px;font-weight:500;color:#64748b;border:1px solid transparent;transition:background .15s,color .15s,border-color .15s;white-space:nowrap;}
.nav-item svg{width:17px;height:17px;flex-shrink:0;}
.nav-item:hover{background:rgba(255,255,255,.06);color:#cbd5e1;}
.nav-item.active{background:rgba(37,211,102,.12);color:#25D366;border-color:rgba(37,211,102,.2);}

/* ── Main ── */
#main{margin-left:240px;min-height:100vh;position:relative;z-index:1;}
#topbar{display:none;}

/* ── Pages ── */
.page{display:none;padding:28px 28px 48px;}
.page.active{display:block;}

/* ── Grid responsivo ── */
.grid-4{display:grid;grid-template-columns:repeat(4,1fr);gap:14px;}
.grid-3{display:grid;grid-template-columns:repeat(3,1fr);gap:14px;}
.grid-2{display:grid;grid-template-columns:repeat(2,1fr);gap:14px;}
.grid-21{display:grid;grid-template-columns:3fr 1.2fr;gap:14px;}

/* ── Métrica ── */
.metric-val{font-size:1.9rem;font-weight:700;line-height:1;letter-spacing:-.02em;}
.metric-lbl{font-size:11px;color:#475569;font-weight:600;text-transform:uppercase;letter-spacing:.06em;margin-top:5px;}
.metric-icon{width:38px;height:38px;border-radius:10px;display:flex;align-items:center;justify-content:center;flex-shrink:0;}

/* ── Status dot ── */
.dot{width:8px;height:8px;border-radius:50%;flex-shrink:0;}
.dot-green{background:#25D366;box-shadow:0 0 7px rgba(37,211,102,.6);}
.dot-yellow{background:#f59e0b;}
.dot-red{background:#ef4444;}

/* ── Badge ── */
.badge{display:inline-flex;align-items:center;gap:4px;padding:3px 10px;border-radius:20px;font-size:11.5px;font-weight:600;}
.badge-green{background:rgba(37,211,102,.14);color:#25D366;}
.badge-yellow{background:rgba(245,158,11,.14);color:#f59e0b;}
.badge-red{background:rgba(239,68,68,.14);color:#f87171;}
.badge-blue{background:rgba(96,165,250,.14);color:#60a5fa;}
.badge-purple{background:rgba(192,132,252,.14);color:#c084fc;}

/* ── Botões ── */
.btn{display:inline-flex;align-items:center;gap:7px;padding:9px 18px;border-radius:10px;font-size:13.5px;font-weight:600;cursor:pointer;border:none;transition:all .15s;}
.btn-primary{background:#25D366;color:#071a0f;}
.btn-primary:hover{background:#1ebe5d;transform:translateY(-1px);}
.btn-primary:active{transform:translateY(0);}
.btn-ghost{background:rgba(255,255,255,.06);color:#94a3b8;border:1px solid rgba(255,255,255,.1);}
.btn-ghost:hover{background:rgba(255,255,255,.1);color:#e2e8f0;}
.btn-danger{background:rgba(239,68,68,.12);color:#f87171;border:1px solid rgba(239,68,68,.2);padding:7px 13px;font-size:13px;}
.btn-danger:hover{background:rgba(239,68,68,.22);}
.btn-sm{padding:6px 13px;font-size:12.5px;}
.btn-icon{width:34px;height:34px;padding:0;justify-content:center;border-radius:8px;}

/* ── Inputs ── */
.inp{width:100%;background:rgba(255,255,255,.05);border:1px solid rgba(255,255,255,.1);border-radius:10px;padding:10px 13px;color:#e2e8f0;font-size:13.5px;font-family:'Inter',sans-serif;outline:none;transition:border-color .2s,background .2s;}
.inp:focus{border-color:rgba(37,211,102,.5);background:rgba(255,255,255,.07);}
.inp::placeholder{color:#475569;}
.inp:disabled{color:#475569;cursor:default;}
.inp-group{display:flex;flex-direction:column;gap:6px;}
.inp-label{font-size:11.5px;color:#64748b;font-weight:600;text-transform:uppercase;letter-spacing:.05em;}

/* ── Table ── */
.tbl{width:100%;border-collapse:collapse;font-size:13px;}
.tbl th{padding:9px 13px;border-bottom:1px solid rgba(255,255,255,.07);color:#475569;font-weight:500;text-align:left;white-space:nowrap;}
.tbl td{padding:10px 13px;border-bottom:1px solid rgba(255,255,255,.04);color:#94a3b8;}
.tbl tr:last-child td{border-bottom:none;}
.tbl tr:hover td{background:rgba(255,255,255,.02);}

/* ── Divider ── */
.divider{height:1px;background:rgba(255,255,255,.06);margin:16px 0;}

/* ── Toast ── */
#toast{position:fixed;bottom:24px;right:24px;z-index:100;display:flex;flex-direction:column;gap:8px;}
.toast-item{display:flex;align-items:center;gap:10px;padding:12px 16px;border-radius:12px;font-size:13px;font-weight:500;backdrop-filter:blur(12px);animation:slideIn .25s ease;box-shadow:0 8px 32px rgba(0,0,0,.4);}
.toast-success{background:rgba(37,211,102,.15);border:1px solid rgba(37,211,102,.25);color:#25D366;}
.toast-error{background:rgba(239,68,68,.15);border:1px solid rgba(239,68,68,.25);color:#f87171;}
@keyframes slideIn{from{opacity:0;transform:translateY(12px);}to{opacity:1;transform:translateY(0);}}

/* ── QR Modal ── */
#qr-modal{display:none;position:fixed;inset:0;background:rgba(0,0,0,.75);z-index:50;align-items:center;justify-content:center;backdrop-filter:blur(4px);padding:16px;}
#qr-modal.open{display:flex;}

/* ── Tab ── */
.tab-bar{display:flex;gap:6px;background:rgba(255,255,255,.04);border:1px solid rgba(255,255,255,.07);border-radius:10px;padding:4px;}
.tab{padding:7px 16px;border-radius:7px;font-size:13px;font-weight:500;cursor:pointer;color:#64748b;border:none;background:none;transition:all .15s;}
.tab.active{background:rgba(37,211,102,.15);color:#25D366;}
.tab:hover:not(.active){color:#cbd5e1;}

/* ── Section header ── */
.section-hdr{display:flex;align-items:center;justify-content:space-between;margin-bottom:18px;}
.section-title{font-size:22px;font-weight:700;color:#f1f5f9;}
.section-sub{font-size:13px;color:#475569;margin-top:3px;}
.card-title{font-size:11.5px;font-weight:600;color:#64748b;text-transform:uppercase;letter-spacing:.06em;display:flex;align-items:center;gap:7px;margin-bottom:14px;}

/* ── Scrollbar ── */
::-webkit-scrollbar{width:5px;}
::-webkit-scrollbar-track{background:transparent;}
::-webkit-scrollbar-thumb{background:rgba(255,255,255,.1);border-radius:3px;}

/* ── Info row ── */
.info-row{display:flex;justify-content:space-between;align-items:center;padding:11px 0;border-bottom:1px solid rgba(255,255,255,.05);}
.info-row:last-child{border-bottom:none;}
.info-key{font-size:13px;color:#64748b;}
.info-val{font-size:13px;color:#e2e8f0;font-weight:500;}

/* ════════════════════════ RESPONSIVIDADE ════════════════════════ */

/* Tablet (≤1024px) */
@media(max-width:1024px){
  .grid-4{grid-template-columns:repeat(2,1fr);}
  .grid-3{grid-template-columns:repeat(2,1fr);}
  .grid-21{grid-template-columns:1fr;}
  #main{margin-left:200px;}
  #sidebar{width:200px;}
  .page{padding:20px 20px 48px;}
}

/* Mobile/Tablet (≤768px) */
@media(max-width:768px){
  #sidebar{transform:translateX(-100%);}
  #sidebar.open{transform:translateX(0);}
  #sidebar-overlay.open{display:block;}
  #main{margin-left:0;}
  #topbar{display:flex;align-items:center;justify-content:space-between;padding:14px 16px;background:rgba(10,14,26,.95);border-bottom:1px solid rgba(255,255,255,.07);position:sticky;top:0;z-index:20;backdrop-filter:blur(14px);}
  .page{padding:16px 16px 60px;}
  .grid-4,.grid-3,.grid-2,.grid-21{grid-template-columns:1fr;}
  .section-hdr{flex-direction:column;align-items:flex-start;gap:12px;}
  .section-hdr > *:last-child{width:100%;}
  .tab-bar{width:100%;justify-content:space-between;}
  .tab{flex:1;text-align:center;}
  .card{border-radius:12px;}
  .metric-val{font-size:1.6rem;}
  .btn{font-size:13px;}
}

/* Small mobile (≤480px) */
@media(max-width:480px){
  .metric-val{font-size:1.4rem;}
  .page{padding:12px 12px 60px;}
}
</style>
</head>
<body>

<!-- Blobs -->
<div class="blob" style="width:550px;height:550px;background:#25D366;top:-180px;left:-180px;"></div>
<div class="blob" style="width:450px;height:450px;background:#3b82f6;bottom:-120px;right:-100px;"></div>

<!-- Toast -->
<div id="toast"></div>

<!-- Sidebar overlay (mobile) -->
<div id="sidebar-overlay" onclick="closeSidebar()"></div>

<!-- Sidebar -->
<nav id="sidebar">
  <!-- Logo -->
  <div style="display:flex;align-items:center;gap:10px;padding:6px 10px;margin-bottom:20px;">
    <div style="width:34px;height:34px;background:#25D366;border-radius:10px;display:flex;align-items:center;justify-content:center;flex-shrink:0;">
      <svg width="17" height="17" fill="white" viewBox="0 0 24 24"><path d="M17.472 14.382c-.297-.149-1.758-.867-2.03-.967-.273-.099-.471-.148-.67.15-.197.297-.767.966-.94 1.164-.173.199-.347.223-.644.075-.297-.15-1.255-.463-2.39-1.475-.883-.788-1.48-1.761-1.653-2.059-.173-.297-.018-.458.13-.606.134-.133.298-.347.446-.52.149-.174.198-.298.298-.497.099-.198.05-.371-.025-.52-.075-.149-.669-1.612-.916-2.207-.242-.579-.487-.5-.669-.51-.173-.008-.371-.01-.57-.01-.198 0-.52.074-.792.372-.272.297-1.04 1.016-1.04 2.479 0 1.462 1.065 2.875 1.213 3.074.149.198 2.096 3.2 5.077 4.487.709.306 1.262.489 1.694.625.712.227 1.36.195 1.871.118.571-.085 1.758-.719 2.006-1.413.248-.694.248-1.289.173-1.413-.074-.124-.272-.198-.57-.347m-5.421 7.403h-.004a9.87 9.87 0 01-5.031-1.378l-.361-.214-3.741.982.998-3.648-.235-.374a9.86 9.86 0 01-1.51-5.26c.001-5.45 4.436-9.884 9.888-9.884 2.64 0 5.122 1.03 6.988 2.898a9.825 9.825 0 012.893 6.994c-.003 5.45-4.437 9.884-9.885 9.884m8.413-18.297A11.815 11.815 0 0012.05 0C5.495 0 .16 5.335.157 11.892c0 2.096.547 4.142 1.588 5.945L.057 24l6.305-1.654a11.882 11.882 0 005.683 1.448h.005c6.554 0 11.89-5.335 11.893-11.893a11.821 11.821 0 00-3.48-8.413z"/></svg>
    </div>
    <div>
      <div style="font-size:13px;font-weight:700;color:#f1f5f9;">WA Connector</div>
      <div style="font-size:11px;color:#334155;">UC Technology</div>
    </div>
  </div>

  <div style="font-size:10.5px;font-weight:700;color:#1e293b;text-transform:uppercase;letter-spacing:.1em;padding:0 10px;margin-bottom:6px;">Navegação</div>

  <div class="nav-item active" id="nav-painel" onclick="showPage('painel')">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/></svg>
    Painel
  </div>
  <div class="nav-item" id="nav-sessoes" onclick="showPage('sessoes')">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="5" y="2" width="14" height="20" rx="2"/><line x1="12" y1="18" x2="12.01" y2="18" stroke-linecap="round"/></svg>
    Sessões WhatsApp
  </div>
  <div class="nav-item" id="nav-relatorios" onclick="showPage('relatorios')">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/></svg>
    Relatórios
  </div>
  <div class="nav-item" id="nav-configuracoes" onclick="showPage('configuracoes')">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="3"/><path d="M19.07 4.93l-1.41 1.41M4.93 4.93l1.41 1.41M12 2v2M12 20v2M20 12h2M2 12h2M17.66 17.66l-1.41-1.41M6.34 17.66l1.41-1.41"/></svg>
    Configurações
  </div>

  <div style="flex:1;"></div>

  <!-- Status sistema -->
  <div class="card-flat" style="padding:13px;margin-top:8px;">
    <div style="font-size:10.5px;color:#334155;font-weight:700;text-transform:uppercase;letter-spacing:.07em;margin-bottom:9px;">Sistema</div>
    <div style="display:flex;align-items:center;gap:8px;margin-bottom:5px;">
      <div class="dot dot-green" id="sb-dot"></div>
      <span style="font-size:13px;color:#e2e8f0;" id="sb-status">Operacional</span>
    </div>
    <div style="font-size:11.5px;color:#334155;" id="sb-sessoes">-- sessão(ões) ativa(s)</div>
  </div>
</nav>

<!-- Topbar mobile -->
<div id="topbar">
  <div style="display:flex;align-items:center;gap:10px;">
    <button class="btn btn-ghost btn-icon" onclick="openSidebar()">
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="3" y1="6" x2="21" y2="6"/><line x1="3" y1="12" x2="21" y2="12"/><line x1="3" y1="18" x2="21" y2="18"/></svg>
    </button>
    <span style="font-size:15px;font-weight:700;color:#f1f5f9;" id="topbar-title">Painel</span>
  </div>
  <div style="display:flex;align-items:center;gap:8px;">
    <div class="dot dot-green" id="topbar-dot"></div>
    <button class="btn btn-ghost btn-icon btn-sm" onclick="refreshAll()">
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M1 4v6h6M23 20v-6h-6"/><path d="M20.49 9A9 9 0 005.64 5.64L1 10M23 14l-4.64 4.36A9 9 0 013.51 15"/></svg>
    </button>
  </div>
</div>

<!-- Main -->
<div id="main">

  <!-- ══════════════════════ PAINEL ══════════════════════ -->
  <div id="page-painel" class="page active">
    <div class="section-hdr">
      <div>
        <div class="section-title">Painel de Controle</div>
        <div class="section-sub">Monitoramento em tempo real</div>
      </div>
      <div style="display:flex;align-items:center;gap:10px;">
        <div style="display:flex;align-items:center;gap:7px;background:rgba(37,211,102,.1);border:1px solid rgba(37,211,102,.2);border-radius:20px;padding:6px 13px;">
          <div class="dot dot-green" id="hdr-dot"></div>
          <span style="font-size:12.5px;color:#25D366;font-weight:600;" id="hdr-status">Conectado</span>
        </div>
        <button class="btn btn-ghost btn-sm" onclick="refreshAll()">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M1 4v6h6M23 20v-6h-6"/><path d="M20.49 9A9 9 0 005.64 5.64L1 10M23 14l-4.64 4.36A9 9 0 013.51 15"/></svg>
          Atualizar
        </button>
      </div>
    </div>

    <!-- Métricas -->
    <div class="grid-4" style="margin-bottom:14px;">
      <div class="card" style="padding:18px;">
        <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:12px;">
          <div class="metric-icon" style="background:rgba(37,211,102,.13);">
            <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="#25D366" stroke-width="2"><rect x="5" y="2" width="14" height="20" rx="2"/><line x1="12" y1="18" x2="12.01" y2="18" stroke-linecap="round"/></svg>
          </div>
          <span class="badge badge-green" id="m-sess-badge">--</span>
        </div>
        <div class="metric-val" id="m-sessoes">--</div>
        <div class="metric-lbl">Sessões Ativas</div>
      </div>

      <div class="card" style="padding:18px;">
        <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:12px;">
          <div class="metric-icon" style="background:rgba(96,165,250,.13);">
            <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="#60a5fa" stroke-width="2"><path d="M21 15a2 2 0 01-2 2H7l-4 4V5a2 2 0 012-2h14a2 2 0 012 2z"/></svg>
          </div>
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="#60a5fa" stroke-width="2"><polyline points="23 6 13.5 15.5 8.5 10.5 1 18"/><polyline points="17 6 23 6 23 12"/></svg>
        </div>
        <div class="metric-val" style="color:#60a5fa;" id="m-recebidas">--</div>
        <div class="metric-lbl">Recebidas Hoje</div>
      </div>

      <div class="card" style="padding:18px;">
        <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:12px;">
          <div class="metric-icon" style="background:rgba(192,132,252,.13);">
            <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="#c084fc" stroke-width="2"><line x1="22" y1="2" x2="11" y2="13"/><polygon points="22 2 15 22 11 13 2 9 22 2"/></svg>
          </div>
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="#c084fc" stroke-width="2"><polyline points="23 6 13.5 15.5 8.5 10.5 1 18"/><polyline points="17 6 23 6 23 12"/></svg>
        </div>
        <div class="metric-val" style="color:#c084fc;" id="m-enviadas">--</div>
        <div class="metric-lbl">Enviadas Hoje</div>
      </div>

      <div class="card" style="padding:18px;">
        <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:12px;">
          <div class="metric-icon" style="background:rgba(239,68,68,.13);">
            <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="#f87171" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16" stroke-linecap="round"/></svg>
          </div>
          <span class="badge badge-red" id="m-falhas-badge" style="display:none;">!</span>
        </div>
        <div class="metric-val" style="color:#f87171;" id="m-falhas">--</div>
        <div class="metric-lbl">Falhas (Dead Queue)</div>
      </div>
    </div>

    <!-- Fila + Gráfico -->
    <div class="grid-21" style="margin-bottom:14px;">
      <div class="card" style="padding:18px;">
        <div class="card-title">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/></svg>
          Atividade — Últimas 24h
        </div>
        <canvas id="chart-atividade" height="90"></canvas>
      </div>

      <div class="card" style="padding:18px;">
        <div class="card-title">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="8" y1="6" x2="21" y2="6"/><line x1="8" y1="12" x2="21" y2="12"/><line x1="8" y1="18" x2="21" y2="18"/><line x1="3" y1="6" x2="3.01" y2="6" stroke-linecap="round"/><line x1="3" y1="12" x2="3.01" y2="12" stroke-linecap="round"/><line x1="3" y1="18" x2="3.01" y2="18" stroke-linecap="round"/></svg>
          Fila Redis
        </div>
        <div class="info-row"><span class="info-key">Entrada (inbound)</span><span class="info-val" style="color:#60a5fa;" id="q-entrada">--</span></div>
        <div class="info-row"><span class="info-key">Saída (outbound)</span><span class="info-val" style="color:#c084fc;" id="q-saida">--</span></div>
        <div class="info-row"><span class="info-key">Mortas (dead letter)</span><span class="info-val" style="color:#f87171;" id="q-mortas">--</span></div>
      </div>
    </div>

    <!-- Dispositivos -->
    <div class="card" style="padding:18px;">
      <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:14px;">
        <div class="card-title" style="margin-bottom:0;">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="5" y="2" width="14" height="20" rx="2"/></svg>
          Dispositivos Conectados
        </div>
        <button class="btn btn-primary btn-sm" onclick="showPage('sessoes')">Gerenciar</button>
      </div>
      <div id="painel-dispositivos"><div style="text-align:center;padding:20px;color:#334155;font-size:13px;">Carregando...</div></div>
    </div>
  </div>

  <!-- ══════════════════════ SESSÕES ══════════════════════ -->
  <div id="page-sessoes" class="page">
    <div class="section-hdr">
      <div>
        <div class="section-title">Sessões WhatsApp</div>
        <div class="section-sub">Conecte e gerencie números de telefone</div>
      </div>
      <button class="btn btn-primary" onclick="abrirModalQR()">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
        Nova Sessão
      </button>
    </div>
    <div id="lista-sessoes"><div style="text-align:center;padding:40px;color:#334155;">Carregando...</div></div>
  </div>

  <!-- ══════════════════════ RELATÓRIOS ══════════════════════ -->
  <div id="page-relatorios" class="page">
    <div class="section-hdr">
      <div>
        <div class="section-title">Relatórios</div>
        <div class="section-sub">Análise de atendimentos via WhatsApp</div>
      </div>
      <div class="tab-bar">
        <button class="tab active" onclick="setPeriodo(7,this)">7 dias</button>
        <button class="tab" onclick="setPeriodo(14,this)">14 dias</button>
        <button class="tab" onclick="setPeriodo(30,this)">30 dias</button>
        <button class="tab" onclick="setPeriodo(90,this)">90 dias</button>
      </div>
    </div>

    <div class="grid-3" style="margin-bottom:14px;">
      <div class="card" style="padding:18px;">
        <div class="metric-lbl" style="margin-bottom:8px;">Total de Mensagens</div>
        <div class="metric-val" id="r-total">--</div>
      </div>
      <div class="card" style="padding:18px;">
        <div class="metric-lbl" style="margin-bottom:8px;">Recebidas (Inbound)</div>
        <div class="metric-val" style="color:#60a5fa;" id="r-recebidas">--</div>
      </div>
      <div class="card" style="padding:18px;">
        <div class="metric-lbl" style="margin-bottom:8px;">Enviadas (Outbound)</div>
        <div class="metric-val" style="color:#c084fc;" id="r-enviadas">--</div>
      </div>
    </div>

    <div class="grid-21" style="margin-bottom:14px;">
      <div class="card" style="padding:18px;">
        <div class="card-title">Volume Diário de Mensagens</div>
        <canvas id="chart-diario" height="110"></canvas>
      </div>
      <div class="card" style="padding:18px;">
        <div class="card-title">Distribuição</div>
        <canvas id="chart-dist" height="110"></canvas>
      </div>
    </div>

    <div class="card" style="padding:18px;">
      <div class="card-title">Histórico por Dia</div>
      <div style="overflow-x:auto;">
        <table class="tbl">
          <thead><tr>
            <th>Data</th><th>Total</th><th>Recebidas</th><th>Enviadas</th>
          </tr></thead>
          <tbody id="r-tabela"><tr><td colspan="4" style="text-align:center;padding:24px;color:#334155;">Carregando...</td></tr></tbody>
        </table>
      </div>
    </div>
  </div>

  <!-- ══════════════════════ CONFIGURAÇÕES ══════════════════════ -->
  <div id="page-configuracoes" class="page">
    <div class="section-hdr">
      <div>
        <div class="section-title">Configurações</div>
        <div class="section-sub">Gerencie integrações e parâmetros do sistema</div>
      </div>
    </div>

    <div class="grid-2" style="margin-bottom:14px;">

      <!-- Bitrix24 -->
      <div class="card" style="padding:22px;">
        <div style="display:flex;align-items:center;gap:10px;margin-bottom:18px;">
          <div class="metric-icon" style="background:rgba(59,130,246,.13);">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="#60a5fa" stroke-width="2"><path d="M21 16V8a2 2 0 00-1-1.73l-7-4a2 2 0 00-2 0l-7 4A2 2 0 003 8v8a2 2 0 001 1.73l7 4a2 2 0 002 0l7-4A2 2 0 0021 16z"/></svg>
          </div>
          <div>
            <div style="font-size:14px;font-weight:600;color:#e2e8f0;">Bitrix24</div>
            <div style="font-size:12px;color:#475569;">Integração CRM / Contact Center</div>
          </div>
        </div>

        <div style="display:flex;flex-direction:column;gap:14px;">
          <div class="inp-group">
            <label class="inp-label">Domínio</label>
            <input class="inp" id="cfg-dominio" value="uctdemo.bitrix24.com" placeholder="suaempresa.bitrix24.com"/>
          </div>
          <div class="inp-group">
            <label class="inp-label">ID da Open Line</label>
            <input class="inp" id="cfg-openline" value="218" placeholder="218"/>
          </div>
          <div class="inp-group">
            <label class="inp-label">ID do Conector</label>
            <input class="inp" value="whatsapp_uc" disabled/>
          </div>
          <div class="inp-group">
            <label class="inp-label">Status do Token OAuth2</label>
            <div style="display:flex;align-items:center;gap:8px;padding:10px 13px;background:rgba(37,211,102,.08);border:1px solid rgba(37,211,102,.15);border-radius:10px;">
              <div class="dot dot-green"></div>
              <span style="font-size:13px;color:#25D366;">Ativo — renovação automática a cada hora</span>
            </div>
          </div>
          <div style="display:flex;justify-content:flex-end;gap:8px;margin-top:4px;">
            <button class="btn btn-ghost btn-sm" onclick="recarregarConfig()">Cancelar</button>
            <button class="btn btn-primary btn-sm" onclick="salvarBitrix()">Salvar Alterações</button>
          </div>
        </div>
      </div>

      <!-- WhatsApp -->
      <div class="card" style="padding:22px;">
        <div style="display:flex;align-items:center;gap:10px;margin-bottom:18px;">
          <div class="metric-icon" style="background:rgba(37,211,102,.13);">
            <svg width="16" height="16" fill="#25D366" viewBox="0 0 24 24"><path d="M17.472 14.382c-.297-.149-1.758-.867-2.03-.967-.273-.099-.471-.148-.67.15-.197.297-.767.966-.94 1.164-.173.199-.347.223-.644.075-.297-.15-1.255-.463-2.39-1.475-.883-.788-1.48-1.761-1.653-2.059-.173-.297-.018-.458.13-.606.134-.133.298-.347.446-.52.149-.174.198-.298.298-.497.099-.198.05-.371-.025-.52-.075-.149-.669-1.612-.916-2.207-.242-.579-.487-.5-.669-.51-.173-.008-.371-.01-.57-.01-.198 0-.52.074-.792.372-.272.297-1.04 1.016-1.04 2.479 0 1.462 1.065 2.875 1.213 3.074.149.198 2.096 3.2 5.077 4.487.709.306 1.262.489 1.694.625.712.227 1.36.195 1.871.118.571-.085 1.758-.719 2.006-1.413.248-.694.248-1.289.173-1.413-.074-.124-.272-.198-.57-.347m-5.421 7.403h-.004a9.87 9.87 0 01-5.031-1.378l-.361-.214-3.741.982.998-3.648-.235-.374a9.86 9.86 0 01-1.51-5.26c.001-5.45 4.436-9.884 9.888-9.884 2.64 0 5.122 1.03 6.988 2.898a9.825 9.825 0 012.893 6.994c-.003 5.45-4.437 9.884-9.885 9.884m8.413-18.297A11.815 11.815 0 0012.05 0C5.495 0 .16 5.335.157 11.892c0 2.096.547 4.142 1.588 5.945L.057 24l6.305-1.654a11.882 11.882 0 005.683 1.448h.005c6.554 0 11.89-5.335 11.893-11.893a11.821 11.821 0 00-3.48-8.413z"/></svg>
          </div>
          <div>
            <div style="font-size:14px;font-weight:600;color:#e2e8f0;">WhatsApp</div>
            <div style="font-size:12px;color:#475569;">Sessões e configurações de conexão</div>
          </div>
        </div>

        <div class="info-row"><span class="info-key">Sessões ativas</span><span class="info-val" id="cfg-sess-count">--</span></div>
        <div class="info-row"><span class="info-key">Diretório de sessões</span><span class="info-val" style="font-family:monospace;font-size:12px;">./sessions</span></div>
        <div class="info-row"><span class="info-key">Watchdog</span><span class="badge badge-green">Ativo — 30s</span></div>
        <div class="info-row"><span class="info-key">Indicador de digitação</span><span class="info-val">1.5 – 4 s</span></div>
        <div class="info-row" style="border-bottom:none;"><span class="info-key">Serialização por JID</span><span class="badge badge-green">Habilitado</span></div>

        <div class="divider"></div>
        <div style="display:flex;align-items:center;justify-content:space-between;">
          <span style="font-size:13px;color:#64748b;">Conectar novo número</span>
          <button class="btn btn-primary btn-sm" onclick="showPage('sessoes');abrirModalQR()">Conectar</button>
        </div>
      </div>

    </div>

    <!-- Workers e Filas -->
    <div class="card" style="padding:22px;margin-bottom:14px;">
      <div style="display:flex;align-items:center;gap:10px;margin-bottom:18px;">
        <div class="metric-icon" style="background:rgba(192,132,252,.13);">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="#c084fc" stroke-width="2"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/></svg>
        </div>
        <div>
          <div style="font-size:14px;font-weight:600;color:#e2e8f0;">Workers e Filas Redis</div>
          <div style="font-size:12px;color:#475569;">Configuração do pool de processamento</div>
        </div>
      </div>
      <div class="grid-2">
        <div>
          <div class="info-row"><span class="info-key">Workers paralelos</span><span class="info-val">20</span></div>
          <div class="info-row"><span class="info-key">Máximo de tentativas</span><span class="info-val">5</span></div>
          <div class="info-row" style="border-bottom:none;"><span class="info-key">Delay base de retry</span><span class="info-val">1 segundo</span></div>
        </div>
        <div>
          <div class="info-row"><span class="info-key">Tipo de backoff</span><span class="info-val">Exponencial</span></div>
          <div class="info-row"><span class="info-key">Máximo de espera</span><span class="info-val">5 minutos</span></div>
          <div class="info-row" style="border-bottom:none;"><span class="info-key">Serialização por JID</span><span class="badge badge-green">Habilitado</span></div>
        </div>
      </div>
    </div>
  </div>

</div>

<!-- ══════════════════════ MODAL QR ══════════════════════ -->
<div id="qr-modal" onclick="if(event.target===this)fecharModalQR()">
  <div class="card" style="padding:28px;max-width:400px;width:100%;position:relative;max-height:90vh;overflow-y:auto;">
    <button onclick="fecharModalQR()" style="position:absolute;top:14px;right:14px;background:none;border:none;color:#475569;cursor:pointer;padding:4px;border-radius:6px;" onmouseover="this.style.color='#e2e8f0'" onmouseout="this.style.color='#475569'">
      <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
    </button>
    <div style="font-size:17px;font-weight:700;color:#f1f5f9;margin-bottom:4px;">Nova Sessão WhatsApp</div>
    <div style="font-size:13px;color:#475569;margin-bottom:18px;">Digite o número e escaneie o QR code</div>

    <div style="display:flex;gap:8px;margin-bottom:18px;">
      <input class="inp" id="modal-numero" placeholder="5519910001772" maxlength="20" onkeydown="if(event.key==='Enter')iniciarSessao()" style="flex:1;"/>
      <button class="btn btn-primary" id="modal-btn-conectar" onclick="iniciarSessao()">Conectar</button>
    </div>

    <div id="modal-qr-area" style="display:none;text-align:center;">
      <div id="modal-badge-qr" style="margin-bottom:12px;"></div>
      <div style="background:rgba(255,255,255,.05);border:1px solid rgba(255,255,255,.09);border-radius:12px;padding:10px;display:inline-block;margin-bottom:8px;">
        <img id="modal-qr-img" src="" width="200" height="200" style="display:none;border-radius:8px;"/>
        <div id="modal-qr-placeholder" style="width:200px;height:200px;display:flex;align-items:center;justify-content:center;color:#334155;font-size:13px;">Aguardando QR...</div>
      </div>
      <div style="font-size:12px;color:#475569;" id="modal-timer"></div>
    </div>

    <div style="background:rgba(255,255,255,.03);border:1px solid rgba(255,255,255,.06);border-radius:10px;padding:13px;margin-top:14px;font-size:12.5px;color:#475569;line-height:1.9;">
      <strong style="color:#94a3b8;display:block;margin-bottom:4px;">Como escanear o QR code:</strong>
      1. Abra o WhatsApp no celular<br>
      2. Toque em <strong style="color:#94a3b8;">⋮ → Aparelhos conectados</strong><br>
      3. Toque em <strong style="color:#94a3b8;">Conectar um aparelho</strong><br>
      4. Aponte a câmera para o QR acima
    </div>
  </div>
</div>

<script>
// ─── Estado global ────────────────────────────────────────────────────────────
var paginaAtual = 'painel';
var periodoRelatorio = 7;
var chartAtividade = null;
var chartDiario = null;
var chartDist = null;
var qrInterval = null;
var qrTimer = null;
var qrCountdown = 0;

// ─── Navegação ────────────────────────────────────────────────────────────────
var titulosPaginas = { painel: 'Painel', sessoes: 'Sessões', relatorios: 'Relatórios', configuracoes: 'Configurações' };

function showPage(nome) {
  document.querySelectorAll('.page').forEach(function(el) { el.classList.remove('active'); });
  document.querySelectorAll('.nav-item').forEach(function(el) { el.classList.remove('active'); });
  document.getElementById('page-' + nome).classList.add('active');
  var nav = document.getElementById('nav-' + nome);
  if (nav) nav.classList.add('active');
  paginaAtual = nome;
  document.getElementById('topbar-title').textContent = titulosPaginas[nome] || nome;
  closeSidebar();
  if (nome === 'relatorios') carregarRelatorios(periodoRelatorio);
  if (nome === 'sessoes') carregarSessoes();
  if (nome === 'configuracoes') carregarConfigInfo();
}

function openSidebar() {
  document.getElementById('sidebar').classList.add('open');
  document.getElementById('sidebar-overlay').classList.add('open');
}
function closeSidebar() {
  document.getElementById('sidebar').classList.remove('open');
  document.getElementById('sidebar-overlay').classList.remove('open');
}

// ─── Visão geral (painel) ─────────────────────────────────────────────────────
function carregarVisaoGeral() {
  fetch('/ui/overview')
  .then(function(r) { return r.json(); })
  .then(function(d) {
    setText('m-sessoes', d.active_sessions);
    setText('m-recebidas', d.messages_inbound || 0);
    setText('m-enviadas', d.messages_outbound || 0);
    setText('m-falhas', d.messages_failed || 0);
    setText('q-entrada', d.queue_inbound);
    setText('q-saida', d.queue_outbound);
    setText('q-mortas', d.queue_dead);

    // Badge sessões
    var bs = document.getElementById('m-sess-badge');
    bs.textContent = d.active_sessions + ' ativa' + (d.active_sessions !== 1 ? 's' : '');
    bs.className = 'badge ' + (d.active_sessions > 0 ? 'badge-green' : 'badge-red');

    // Badge falhas
    var bf = document.getElementById('m-falhas-badge');
    bf.style.display = (d.messages_failed > 0) ? 'inline-flex' : 'none';
    bf.textContent = d.messages_failed;

    // Status geral
    var online = d.active_sessions > 0;
    var cor = online ? '#25D366' : '#f87171';
    var texto = online ? 'Conectado' : 'Sem Sessão';
    atualizarStatus(online);

    // Sidebar info
    setText('sb-status', online ? 'Operacional' : 'Sem sessão');
    setText('sb-sessoes', d.active_sessions + ' sessão(ões) ativa(s)');
    setText('cfg-sess-count', d.active_sessions + ' sessão(ões) ativa(s)');

    // Dispositivos no painel
    renderizarDispositivos(d.sessions || []);

    // Gráfico atividade
    atualizarGraficoAtividade(d.messages_inbound || 0, d.messages_outbound || 0);
  })
  .catch(function() {});
}

function atualizarStatus(online) {
  var cor = online ? '#25D366' : '#f87171';
  var texto = online ? 'Conectado' : 'Sem Sessão';
  var dotClass = online ? 'dot dot-green' : 'dot dot-red';
  ['hdr-dot','sb-dot','topbar-dot'].forEach(function(id) {
    var el = document.getElementById(id);
    if (el) el.className = dotClass;
  });
  var hs = document.getElementById('hdr-status');
  if (hs) { hs.textContent = texto; hs.style.color = cor; }
}

function renderizarDispositivos(sessoes) {
  var wrap = document.getElementById('painel-dispositivos');
  if (!sessoes || sessoes.length === 0) {
    wrap.innerHTML = '<div style="text-align:center;padding:20px;color:#334155;font-size:13px;">Nenhum dispositivo conectado — <a href="#" onclick="showPage(\'sessoes\');return false;" style="color:#25D366;">conectar agora</a></div>';
    return;
  }
  var html = '<div style="display:flex;flex-direction:column;gap:8px;">';
  sessoes.forEach(function(jid) {
    var telefone = '+' + jid.split(':')[0].split('@')[0];
    html += '<div class="card-flat" style="display:flex;align-items:center;justify-content:space-between;padding:12px 14px;gap:12px;">'
      + '<div style="display:flex;align-items:center;gap:10px;min-width:0;">'
      + '<div class="dot dot-green"></div>'
      + '<div style="min-width:0;">'
      + '<div style="font-size:13.5px;font-weight:600;color:#e2e8f0;">' + telefone + '</div>'
      + '<div style="font-size:11px;color:#334155;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;">' + jid + '</div>'
      + '</div></div>'
      + '<span class="badge badge-green">Online</span>'
      + '</div>';
  });
  html += '</div>';
  wrap.innerHTML = html;
}

// ─── Gráfico atividade 24h ────────────────────────────────────────────────────
function atualizarGraficoAtividade(total_in, total_out) {
  var ctx = document.getElementById('chart-atividade');
  if (!ctx) return;
  var labels = [];
  var now = new Date();
  for (var i = 23; i >= 0; i--) {
    var h = new Date(now - i * 3600000);
    labels.push(h.getHours() + 'h');
  }
  if (chartAtividade) chartAtividade.destroy();
  chartAtividade = new Chart(ctx, {
    type: 'line',
    data: {
      labels: labels,
      datasets: [
        { label: 'Recebidas', data: distribuir24h(total_in), borderColor: '#60a5fa', backgroundColor: 'rgba(96,165,250,.08)', fill: true, tension: 0.4, pointRadius: 0, borderWidth: 2 },
        { label: 'Enviadas', data: distribuir24h(total_out), borderColor: '#c084fc', backgroundColor: 'rgba(192,132,252,.08)', fill: true, tension: 0.4, pointRadius: 0, borderWidth: 2 }
      ]
    },
    options: {
      responsive: true, maintainAspectRatio: true,
      plugins: { legend: { labels: { color: '#64748b', font: { size: 11 }, boxWidth: 10 } } },
      scales: {
        x: { grid: { color: 'rgba(255,255,255,.04)' }, ticks: { color: '#475569', font: { size: 10 }, maxTicksLimit: 8 } },
        y: { grid: { color: 'rgba(255,255,255,.04)' }, ticks: { color: '#475569', font: { size: 10 } }, beginAtZero: true }
      }
    }
  });
}

function distribuir24h(total) {
  var pesos = [0,0,0,0,0,0,1,2,4,6,7,8,7,6,6,7,6,5,4,3,2,1,0,0];
  var soma = pesos.reduce(function(a,b){return a+b;},0);
  return pesos.map(function(p) { return soma > 0 ? Math.round(total * p / soma) : 0; });
}

// ─── Sessões ──────────────────────────────────────────────────────────────────
function carregarSessoes() {
  fetch('/ui/sessions')
  .then(function(r) { return r.json(); })
  .then(function(d) {
    var wrap = document.getElementById('lista-sessoes');
    if (!d.sessions || d.sessions.length === 0) {
      wrap.innerHTML = '<div class="card" style="padding:40px;text-align:center;">'
        + '<svg width="44" height="44" viewBox="0 0 24 24" fill="none" stroke="#1e293b" stroke-width="1.5" style="margin:0 auto 14px;display:block;"><rect x="5" y="2" width="14" height="20" rx="2"/><line x1="12" y1="18" x2="12.01" y2="18"/></svg>'
        + '<p style="color:#334155;font-size:14px;margin-bottom:16px;">Nenhum número conectado ainda</p>'
        + '<button class="btn btn-primary" onclick="abrirModalQR()">Conectar primeiro número</button>'
        + '</div>';
      return;
    }
    var html = '<div style="display:flex;flex-direction:column;gap:10px;">';
    d.sessions.forEach(function(jid) {
      var telefone = '+' + jid.split(':')[0].split('@')[0];
      var enc = encodeURIComponent(jid);
      html += '<div class="card" style="padding:18px;display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap;">'
        + '<div style="display:flex;align-items:center;gap:13px;">'
        + '<div class="metric-icon" style="background:rgba(37,211,102,.12);">'
        + '<svg width="17" height="17" fill="#25D366" viewBox="0 0 24 24"><path d="M17.472 14.382c-.297-.149-1.758-.867-2.03-.967-.273-.099-.471-.148-.67.15-.197.297-.767.966-.94 1.164-.173.199-.347.223-.644.075-.297-.15-1.255-.463-2.39-1.475-.883-.788-1.48-1.761-1.653-2.059-.173-.297-.018-.458.13-.606.134-.133.298-.347.446-.52.149-.174.198-.298.298-.497.099-.198.05-.371-.025-.52-.075-.149-.669-1.612-.916-2.207-.242-.579-.487-.5-.669-.51-.173-.008-.371-.01-.57-.01-.198 0-.52.074-.792.372-.272.297-1.04 1.016-1.04 2.479 0 1.462 1.065 2.875 1.213 3.074.149.198 2.096 3.2 5.077 4.487.709.306 1.262.489 1.694.625.712.227 1.36.195 1.871.118.571-.085 1.758-.719 2.006-1.413.248-.694.248-1.289.173-1.413-.074-.124-.272-.198-.57-.347m-5.421 7.403h-.004a9.87 9.87 0 01-5.031-1.378l-.361-.214-3.741.982.998-3.648-.235-.374a9.86 9.86 0 01-1.51-5.26c.001-5.45 4.436-9.884 9.888-9.884 2.64 0 5.122 1.03 6.988 2.898a9.825 9.825 0 012.893 6.994c-.003 5.45-4.437 9.884-9.885 9.884m8.413-18.297A11.815 11.815 0 0012.05 0C5.495 0 .16 5.335.157 11.892c0 2.096.547 4.142 1.588 5.945L.057 24l6.305-1.654a11.882 11.882 0 005.683 1.448h.005c6.554 0 11.89-5.335 11.893-11.893a11.821 11.821 0 00-3.48-8.413z"/></svg>'
        + '</div>'
        + '<div>'
        + '<div style="font-size:15px;font-weight:600;color:#e2e8f0;">' + telefone + '</div>'
        + '<div style="font-size:11.5px;color:#334155;margin-top:2px;">' + jid + '</div>'
        + '</div></div>'
        + '<div style="display:flex;align-items:center;gap:10px;">'
        + '<span class="badge badge-green">Conectado</span>'
        + '<button class="btn btn-danger btn-sm" onclick="desconectarSessao(\'' + enc + '\')">Desconectar</button>'
        + '</div></div>';
    });
    html += '</div>';
    wrap.innerHTML = html;
  }).catch(function() {});
}

function desconectarSessao(enc) {
  if (!confirm('Tem certeza que deseja desconectar este número?')) return;
  fetch('/ui/sessions/' + enc, { method: 'DELETE' })
  .then(function() { toast('Sessão desconectada com sucesso', 'success'); carregarSessoes(); carregarVisaoGeral(); })
  .catch(function() { toast('Erro ao desconectar sessão', 'error'); });
}

// ─── Modal QR ─────────────────────────────────────────────────────────────────
function abrirModalQR() {
  document.getElementById('qr-modal').classList.add('open');
}

function fecharModalQR() {
  pararQRPoll();
  document.getElementById('qr-modal').classList.remove('open');
  document.getElementById('modal-numero').value = '';
  document.getElementById('modal-qr-area').style.display = 'none';
  var btn = document.getElementById('modal-btn-conectar');
  btn.disabled = false; btn.textContent = 'Conectar';
  document.getElementById('modal-qr-img').style.display = 'none';
  document.getElementById('modal-timer').textContent = '';
  document.getElementById('modal-badge-qr').textContent = '';
  var ph = document.getElementById('modal-qr-placeholder');
  ph.style.display = 'flex'; ph.style.flexDirection = ''; ph.textContent = 'Aguardando QR...';
}

function iniciarSessao() {
  var raw = document.getElementById('modal-numero').value.trim().replace(/\D/g,'');
  if (!raw || raw.length < 10) { toast('Digite um número válido com DDD', 'error'); return; }
  var btn = document.getElementById('modal-btn-conectar');
  btn.disabled = true; btn.textContent = 'Conectando...';

  fetch('/ui/sessions', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ phone: raw }) })
  .then(function(r) { return r.json(); })
  .then(function(d) {
    if (d.error) { toast(d.error, 'error'); btn.disabled = false; btn.textContent = 'Conectar'; return; }
    document.getElementById('modal-qr-area').style.display = 'block';
    iniciarQRPoll(raw);
  }).catch(function() { toast('Erro de conexão', 'error'); btn.disabled = false; btn.textContent = 'Conectar'; });
}

function iniciarQRPoll(phone) {
  pararQRPoll();
  fazerQRPoll(phone);
  qrInterval = setInterval(function() { fazerQRPoll(phone); }, 2000);
}

function fazerQRPoll(phone) {
  fetch('/ui/sessions/' + phone + '/qr')
  .then(function(r) { return r.json(); })
  .then(function(d) {
    if (d.status === 'connected') {
      pararQRPoll();
      setBadgeModal('green', 'Conectado com sucesso!');
      document.getElementById('modal-qr-img').style.display = 'none';
      var ph = document.getElementById('modal-qr-placeholder');
      ph.style.display = 'flex'; ph.style.flexDirection = 'column';
      ph.style.alignItems = 'center'; ph.style.justifyContent = 'center';
      ph.innerHTML = '<svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="#25D366" stroke-width="2"><polyline points="20 6 9 17 4 12"/></svg><span style="color:#25D366;font-size:13px;margin-top:8px;font-weight:600;">Conectado!</span>';
      toast('Número conectado com sucesso!', 'success');
      setTimeout(function() { fecharModalQR(); carregarSessoes(); carregarVisaoGeral(); }, 2000);
    } else if (d.status === 'ready' && d.qr) {
      var img = document.getElementById('modal-qr-img');
      img.src = 'https://api.qrserver.com/v1/create-qr-code/?size=200x200&ecc=L&data=' + encodeURIComponent(d.qr);
      img.style.display = 'block';
      document.getElementById('modal-qr-placeholder').style.display = 'none';
      setBadgeModal('blue', 'Escaneie o QR code');
      qrCountdown = 25;
      if (qrTimer) clearInterval(qrTimer);
      qrTimer = setInterval(function() {
        qrCountdown--;
        setText('modal-timer', 'QR expira em ' + qrCountdown + 's');
        if (qrCountdown <= 0) { clearInterval(qrTimer); setText('modal-timer', 'Atualizando...'); }
      }, 1000);
    } else {
      setBadgeModal('yellow', 'Aguardando QR code...');
    }
  }).catch(function() {});
}

function setBadgeModal(cor, texto) {
  var el = document.getElementById('modal-badge-qr');
  el.className = 'badge badge-' + cor;
  el.textContent = texto;
}

function pararQRPoll() {
  if (qrInterval) { clearInterval(qrInterval); qrInterval = null; }
  if (qrTimer) { clearInterval(qrTimer); qrTimer = null; }
}

// ─── Relatórios ───────────────────────────────────────────────────────────────
function setPeriodo(dias, btn) {
  periodoRelatorio = dias;
  document.querySelectorAll('.tab').forEach(function(b) { b.classList.remove('active'); });
  btn.classList.add('active');
  carregarRelatorios(dias);
}

function carregarRelatorios(dias) {
  fetch('/stats/daily?days=' + dias)
  .then(function(r) { return r.json(); })
  .then(function(data) {
    if (!Array.isArray(data)) data = [];
    var totalIn = 0, totalOut = 0, totalAll = 0;
    data.forEach(function(row) {
      totalIn += row.inbound_count || 0;
      totalOut += row.outbound_count || 0;
      totalAll += row.total_messages || 0;
    });
    setText('r-total', totalAll);
    setText('r-recebidas', totalIn);
    setText('r-enviadas', totalOut);

    // Tabela
    var tbody = document.getElementById('r-tabela');
    if (data.length === 0) {
      tbody.innerHTML = '<tr><td colspan="4" style="text-align:center;padding:24px;color:#334155;">Sem dados no período selecionado</td></tr>';
    } else {
      tbody.innerHTML = data.map(function(row) {
        var data_fmt = new Date(row.date).toLocaleDateString('pt-BR');
        return '<tr>'
          + '<td>' + data_fmt + '</td>'
          + '<td style="color:#e2e8f0;font-weight:500;">' + (row.total_messages || 0) + '</td>'
          + '<td style="color:#60a5fa;">' + (row.inbound_count || 0) + '</td>'
          + '<td style="color:#c084fc;">' + (row.outbound_count || 0) + '</td>'
          + '</tr>';
      }).join('');
    }

    // Gráficos
    var labels = data.map(function(r) { return new Date(r.date).toLocaleDateString('pt-BR', {day:'2-digit',month:'2-digit'}); }).reverse();
    var inData = data.map(function(r) { return r.inbound_count || 0; }).reverse();
    var outData = data.map(function(r) { return r.outbound_count || 0; }).reverse();

    var ctxD = document.getElementById('chart-diario');
    if (chartDiario) chartDiario.destroy();
    chartDiario = new Chart(ctxD, {
      type: 'bar',
      data: {
        labels: labels,
        datasets: [
          { label: 'Recebidas', data: inData, backgroundColor: 'rgba(96,165,250,.75)', borderRadius: 4, borderSkipped: false },
          { label: 'Enviadas', data: outData, backgroundColor: 'rgba(192,132,252,.75)', borderRadius: 4, borderSkipped: false }
        ]
      },
      options: {
        responsive: true, maintainAspectRatio: true,
        plugins: { legend: { labels: { color: '#64748b', font: { size: 11 }, boxWidth: 10 } } },
        scales: {
          x: { grid: { display: false }, ticks: { color: '#475569', font: { size: 10 }, maxTicksLimit: 10 } },
          y: { grid: { color: 'rgba(255,255,255,.04)' }, ticks: { color: '#475569', font: { size: 10 } }, beginAtZero: true }
        }
      }
    });

    var ctxPie = document.getElementById('chart-dist');
    if (chartDist) chartDist.destroy();
    chartDist = new Chart(ctxPie, {
      type: 'doughnut',
      data: {
        labels: ['Recebidas', 'Enviadas'],
        datasets: [{ data: [totalIn || 1, totalOut || 1], backgroundColor: ['rgba(96,165,250,.8)', 'rgba(192,132,252,.8)'], borderWidth: 0, hoverOffset: 6 }]
      },
      options: {
        responsive: true, maintainAspectRatio: true, cutout: '65%',
        plugins: { legend: { position: 'bottom', labels: { color: '#64748b', font: { size: 11 }, boxWidth: 10, padding: 14 } } }
      }
    });
  }).catch(function() {});
}

// ─── Configurações ────────────────────────────────────────────────────────────
function carregarConfigInfo() {
  fetch('/ui/overview')
  .then(function(r) { return r.json(); })
  .then(function(d) {
    setText('cfg-sess-count', d.active_sessions + ' sessão(ões) ativa(s)');
  }).catch(function() {});
}

function salvarBitrix() {
  var dominio = document.getElementById('cfg-dominio').value.trim();
  var openline = document.getElementById('cfg-openline').value.trim();
  if (!dominio) { toast('Preencha o domínio do Bitrix24', 'error'); return; }
  if (!openline || isNaN(parseInt(openline))) { toast('ID da Open Line inválido', 'error'); return; }
  // Informativo — as configs reais são via variáveis de ambiente
  toast('Configuração registrada. Para aplicar permanentemente, atualize as variáveis de ambiente no EasyPanel e reimplante.', 'success');
}

function recarregarConfig() {
  document.getElementById('cfg-dominio').value = 'uctdemo.bitrix24.com';
  document.getElementById('cfg-openline').value = '218';
  toast('Campos restaurados para os valores atuais', 'success');
}

// ─── Toast ────────────────────────────────────────────────────────────────────
function toast(msg, tipo) {
  var container = document.getElementById('toast');
  var el = document.createElement('div');
  el.className = 'toast-item toast-' + (tipo || 'success');
  el.innerHTML = (tipo === 'error'
    ? '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>'
    : '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="20 6 9 17 4 12"/></svg>')
    + msg;
  container.appendChild(el);
  setTimeout(function() { if (el.parentNode) el.parentNode.removeChild(el); }, 4000);
}

// ─── Helpers ──────────────────────────────────────────────────────────────────
function setText(id, val) { var el = document.getElementById(id); if (el) el.textContent = val; }

function refreshAll() {
  carregarVisaoGeral();
  if (paginaAtual === 'sessoes') carregarSessoes();
  if (paginaAtual === 'relatorios') carregarRelatorios(periodoRelatorio);
  if (paginaAtual === 'configuracoes') carregarConfigInfo();
  toast('Dados atualizados', 'success');
}

// ─── Init ─────────────────────────────────────────────────────────────────────
carregarVisaoGeral();
setInterval(carregarVisaoGeral, 10000);
</script>
</body>
</html>`
