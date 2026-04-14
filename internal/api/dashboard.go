package api

import "github.com/gofiber/fiber/v2"

// GET /dashboard
func (h *handlers) dashboardPage(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/html; charset=utf-8")
	return c.SendString(dashboardHTML)
}

// GET /ui/overview — dados agregados para a dashboard (sem auth, apenas interna)
// ?portal=empresa.bitrix24.com.br → filtra apenas sessões daquele portal
func (h *handlers) uiOverview(c *fiber.Ctx) error {
	portal := normalizePortalParam(c.Query("portal"))

	allSessions := h.waManager.ListSessions()
	sessions := allSessions

	if portal != "" {
		sessions = h.sessionsForPortal(c.Context(), portal, allSessions)
	}

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
		"portal":            portal,
	})
}

const dashboardHTML = `<!DOCTYPE html>
<html lang="pt-BR">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0, maximum-scale=1.0">
<title>WA Connector — Painel</title>
<script src="/assets/chart.js"></script>
<link rel="icon" type="image/png" href="/assets/logo.png">
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

/* ════════════════════════ TEMA CLARO ════════════════════════ */
body.tema-claro{background:#f1f5f9;color:#0f172a;}
body.tema-claro .blob{opacity:.06;}
body.tema-claro .card{background:rgba(255,255,255,.85);border-color:rgba(0,0,0,.08);}
body.tema-claro .card:hover{background:rgba(255,255,255,.95);border-color:rgba(0,0,0,.12);}
body.tema-claro .card-flat{background:rgba(0,0,0,.04);border-color:rgba(0,0,0,.07);}
body.tema-claro #sidebar{background:rgba(241,245,249,.97);border-color:rgba(0,0,0,.08);}
body.tema-claro #topbar{background:rgba(241,245,249,.97);border-color:rgba(0,0,0,.08);}
body.tema-claro .nav-item{color:#64748b;}
body.tema-claro .nav-item:hover{background:rgba(0,0,0,.05);color:#1e293b;}
body.tema-claro .nav-item.active{background:rgba(37,211,102,.12);color:#16a34a;border-color:rgba(37,211,102,.25);}
body.tema-claro .metric-val{color:#0f172a;}
body.tema-claro .metric-lbl{color:#64748b;}
body.tema-claro .section-title{color:#0f172a;}
body.tema-claro .section-sub{color:#64748b;}
body.tema-claro .card-title{color:#64748b;}
body.tema-claro .info-key{color:#64748b;}
body.tema-claro .info-val{color:#0f172a;}
body.tema-claro .info-row{border-color:rgba(0,0,0,.06);}
body.tema-claro .inp{background:rgba(0,0,0,.04);border-color:rgba(0,0,0,.12);color:#0f172a;}
body.tema-claro .inp:focus{border-color:rgba(37,211,102,.5);background:#fff;}
body.tema-claro .inp::placeholder{color:#94a3b8;}
body.tema-claro .btn-ghost{background:rgba(0,0,0,.05);color:#475569;border-color:rgba(0,0,0,.1);}
body.tema-claro .btn-ghost:hover{background:rgba(0,0,0,.09);color:#0f172a;}
body.tema-claro .tbl th{color:#64748b;border-color:rgba(0,0,0,.08);}
body.tema-claro .tbl td{color:#475569;border-color:rgba(0,0,0,.05);}
body.tema-claro .tbl tr:hover td{background:rgba(0,0,0,.02);}
body.tema-claro .divider{background:rgba(0,0,0,.07);}
body.tema-claro #sidebar .card-flat{background:rgba(0,0,0,.04);border-color:rgba(0,0,0,.07);}
body.tema-claro #sidebar #sb-status{color:#0f172a;}
body.tema-claro #sidebar #sb-sessoes{color:#64748b;}
body.tema-claro #btn-tema{background:rgba(0,0,0,.04);border-color:rgba(0,0,0,.08);color:#475569;}
body.tema-claro #btn-tema:hover{background:rgba(0,0,0,.08) !important;color:#0f172a !important;}
body.tema-claro ::-webkit-scrollbar-thumb{background:rgba(0,0,0,.12);}

/* Textos inline hardcoded — forçar cor no tema claro */
/* Cards de sessão: número e JID */
body.tema-claro #lista-sessoes .card [style*="color:#e2e8f0"],
body.tema-claro #lista-sessoes .card [style*="color: #e2e8f0"]{color:#0f172a !important;}
body.tema-claro #lista-sessoes .card [style*="color:#334155"],
body.tema-claro #lista-sessoes .card [style*="color: #334155"]{color:#64748b !important;}
/* Painel: dispositivos conectados */
body.tema-claro #painel-dispositivos [style*="color:#e2e8f0"]{color:#0f172a !important;}
body.tema-claro #painel-dispositivos [style*="color:#334155"]{color:#64748b !important;}
/* Integrações: textos dos cards */
body.tema-claro #lista-integracoes [style*="color:#f1f5f9"],
body.tema-claro #lista-integracoes [style*="color:#e2e8f0"]{color:#0f172a !important;}
body.tema-claro #lista-integracoes [style*="color:#475569"]{color:#475569 !important;}
body.tema-claro #lista-integracoes [style*="color:#334155"]{color:#64748b !important;}
body.tema-claro #lista-integracoes [style*="color:#94a3b8"]{color:#475569 !important;}
body.tema-claro #lista-integracoes [style*="background:rgba(255,255,255,.03)"]{background:rgba(0,0,0,.04) !important;}
/* Títulos de seção inline */
body.tema-claro [style*="color:#f1f5f9"]{color:#0f172a !important;}
body.tema-claro [style*="color:#e2e8f0"]{color:#1e293b !important;}
body.tema-claro [style*="color:#334155"]{color:#64748b !important;}
body.tema-claro [style*="color:#94a3b8"]{color:#475569 !important;}
body.tema-claro [style*="color:#64748b"]{color:#64748b !important;}
body.tema-claro [style*="color:#475569"]{color:#475569 !important;}
/* Fundos de chip/detalhe inline */
body.tema-claro [style*="background:rgba(255,255,255,.03)"],
body.tema-claro [style*="background:rgba(255,255,255,.04)"],
body.tema-claro [style*="background:rgba(255,255,255,.05)"]{background:rgba(0,0,0,.04) !important;}
body.tema-claro [style*="border-bottom:1px solid rgba(255,255,255,.05)"],
body.tema-claro [style*="border-bottom:1px solid rgba(255,255,255,.06)"]{border-bottom-color:rgba(0,0,0,.07) !important;}
body.tema-claro [style*="border-top:1px solid rgba(255,255,255,.06)"]{border-top-color:rgba(0,0,0,.07) !important;}
/* Card "Nenhum dispositivo/sessão" empty state */
body.tema-claro #painel-dispositivos [style*="color:#334155"],
body.tema-claro #lista-sessoes [style*="color:#334155"]{color:#64748b !important;}
body.tema-claro #lista-sessoes .card [style*="background:rgba(255,255,255,.03)"]{background:rgba(0,0,0,.03) !important;}
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
  <div style="padding:6px 10px;margin-bottom:20px;display:flex;flex-direction:column;align-items:center;text-align:center;">
    <img src="/assets/logo.png" alt="UC Technology" style="max-width:100px;height:auto;display:block;"/>
    <div style="margin-top:7px;">
      <div style="font-size:13px;font-weight:700;color:#f1f5f9;">WA Connector</div>
      <div style="font-size:11px;color:#334155;">UC Technology</div>
    </div>
  </div>

  <!-- Badge do portal (visível apenas no modo cliente) -->
  <div id="sidebar-portal-badge" style="display:none;margin:-8px 0 12px;padding:7px 10px;background:rgba(37,211,102,.08);border:1px solid rgba(37,211,102,.2);border-radius:8px;font-size:11px;color:#25D366;font-weight:600;text-align:center;word-break:break-all;line-height:1.4;"></div>

  <div style="font-size:10.5px;font-weight:700;color:#1e293b;text-transform:uppercase;letter-spacing:.1em;padding:0 10px;margin-bottom:6px;">Navegação</div>

  <div class="nav-item active" id="nav-painel" onclick="showPage('painel')">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/></svg>
    Painel
  </div>
  <div class="nav-item" id="nav-sessoes" onclick="showPage('sessoes')">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="5" y="2" width="14" height="20" rx="2"/><line x1="12" y1="18" x2="12.01" y2="18" stroke-linecap="round"/></svg>
    Sessões WhatsApp
  </div>
  <div class="nav-item" id="nav-integracoes" onclick="showPage('integracoes')">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M10 13a5 5 0 007.54.54l3-3a5 5 0 00-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 00-7.54-.54l-3 3a5 5 0 007.07 7.07l1.71-1.71"/></svg>
    Integrações Bitrix
  </div>
  <div class="nav-item" id="nav-filas" onclick="showPage('filas')">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M17 21v-2a4 4 0 00-4-4H5a4 4 0 00-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 00-3-3.87"/><path d="M16 3.13a4 4 0 010 7.75"/></svg>
    Filas Bitrix
  </div>
  <div class="nav-item" id="nav-relatorios" onclick="showPage('relatorios')">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/></svg>
    Relatórios
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

  <!-- Toggle tema -->
  <button id="btn-tema" onclick="toggleTema()" style="margin-top:10px;width:100%;display:flex;align-items:center;justify-content:center;gap:9px;padding:10px 13px;border-radius:10px;cursor:pointer;font-size:13px;font-weight:500;border:1px solid rgba(255,255,255,.08);background:rgba(255,255,255,.04);color:#64748b;transition:background .15s,color .15s;" onmouseover="this.style.background='rgba(255,255,255,.08)';this.style.color='#cbd5e1'" onmouseout="this.style.background='rgba(255,255,255,.04)';this.style.color='#64748b'">
    <span id="tema-icone" style="width:16px;height:16px;display:flex;align-items:center;justify-content:center;">
      <!-- ícone preenchido pelo JS -->
    </span>
    <span id="tema-label">Modo Claro</span>
  </button>
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
    <div class="card" style="padding:18px;margin-bottom:14px;">
      <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:14px;">
        <div class="card-title" style="margin-bottom:0;">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="5" y="2" width="14" height="20" rx="2"/></svg>
          Dispositivos Conectados
        </div>
        <button class="btn btn-primary btn-sm" onclick="showPage('sessoes')">Gerenciar</button>
      </div>
      <div id="painel-dispositivos"><div style="text-align:center;padding:20px;color:#334155;font-size:13px;">Carregando...</div></div>
    </div>

    <!-- Sistema e Workers -->
    <div class="grid-2">
      <!-- WhatsApp info -->
      <div class="card" style="padding:20px;">
        <div class="card-title">
          <svg width="13" height="13" fill="#25D366" viewBox="0 0 24 24"><path d="M17.472 14.382c-.297-.149-1.758-.867-2.03-.967-.273-.099-.471-.148-.67.15-.197.297-.767.966-.94 1.164-.173.199-.347.223-.644.075-.297-.15-1.255-.463-2.39-1.475-.883-.788-1.48-1.761-1.653-2.059-.173-.297-.018-.458.13-.606.134-.133.298-.347.446-.52.149-.174.198-.298.298-.497.099-.198.05-.371-.025-.52-.075-.149-.669-1.612-.916-2.207-.242-.579-.487-.5-.669-.51-.173-.008-.371-.01-.57-.01-.198 0-.52.074-.792.372-.272.297-1.04 1.016-1.04 2.479 0 1.462 1.065 2.875 1.213 3.074.149.198 2.096 3.2 5.077 4.487.709.306 1.262.489 1.694.625.712.227 1.36.195 1.871.118.571-.085 1.758-.719 2.006-1.413.248-.694.248-1.289.173-1.413-.074-.124-.272-.198-.57-.347m-5.421 7.403h-.004a9.87 9.87 0 01-5.031-1.378l-.361-.214-3.741.982.998-3.648-.235-.374a9.86 9.86 0 01-1.51-5.26c.001-5.45 4.436-9.884 9.888-9.884 2.64 0 5.122 1.03 6.988 2.898a9.825 9.825 0 012.893 6.994c-.003 5.45-4.437 9.884-9.885 9.884m8.413-18.297A11.815 11.815 0 0012.05 0C5.495 0 .16 5.335.157 11.892c0 2.096.547 4.142 1.588 5.945L.057 24l6.305-1.654a11.882 11.882 0 005.683 1.448h.005c6.554 0 11.89-5.335 11.893-11.893a11.821 11.821 0 00-3.48-8.413z"/></svg>
          WhatsApp
        </div>
        <div class="info-row"><span class="info-key">Sessões ativas</span><span class="info-val" id="cfg-sess-count">--</span></div>
        <div class="info-row"><span class="info-key">Watchdog</span><span class="badge badge-green">Ativo — 30s</span></div>
        <div class="info-row"><span class="info-key">Indicador de digitação</span><span class="info-val">1.5 – 4 s</span></div>
        <div class="info-row"><span class="info-key">Serialização por JID</span><span class="badge badge-green">Habilitado</span></div>
        <div class="info-row" style="border-bottom:none;"><span class="info-key">Novo número</span><button class="btn btn-primary btn-sm" onclick="showPage('sessoes');abrirModalQR()">Conectar</button></div>
      </div>
      <!-- Workers e Filas -->
      <div class="card" style="padding:20px;">
        <div class="card-title">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="#c084fc" stroke-width="2"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/></svg>
          Workers e Filas Redis
        </div>
        <div class="info-row"><span class="info-key">Workers paralelos</span><span class="info-val">20</span></div>
        <div class="info-row"><span class="info-key">Máximo de tentativas</span><span class="info-val">5</span></div>
        <div class="info-row"><span class="info-key">Tipo de backoff</span><span class="info-val">Exponencial</span></div>
        <div class="info-row"><span class="info-key">Máximo de espera</span><span class="info-val">5 minutos</span></div>
        <div class="info-row" style="border-bottom:none;"><span class="info-key">Serialização por JID</span><span class="badge badge-green">Habilitado</span></div>
      </div>
    </div>
  </div>

  <!-- ══════════════════════ SESSÕES ══════════════════════ -->
  <div id="page-sessoes" class="page">
    <div class="section-hdr">
      <div>
        <div class="section-title">Sessões WhatsApp</div>
        <div class="section-sub">Conecte e gerencie números de telefone</div>
      </div>
      <button class="btn btn-primary" id="btn-nova-sessao" onclick="abrirModalQR()">
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

  <!-- ══════════════════════ FILAS BITRIX ══════════════════════ -->
  <div id="page-filas" class="page">
    <div class="section-hdr">
      <div>
        <div class="section-title">Filas Bitrix</div>
        <div class="section-sub">Conecte cada portal do Contact Center a uma fila (Open Line)</div>
      </div>
      <button class="btn btn-ghost btn-sm" onclick="carregarFilas()">
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M1 4v6h6M23 20v-6h-6"/><path d="M20.49 9A9 9 0 005.64 5.64L1 10M23 14l-4.64 4.36A9 9 0 013.51 15"/></svg>
        Atualizar
      </button>
    </div>

    <!-- Info box -->
    <div class="card-flat" style="padding:14px 18px;margin-bottom:18px;display:flex;align-items:flex-start;gap:12px;">
      <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="#60a5fa" stroke-width="2" style="flex-shrink:0;margin-top:2px;"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="8"/><line x1="12" y1="12" x2="12" y2="16"/></svg>
      <div style="font-size:12.5px;color:#64748b;line-height:1.7;">
        <strong style="color:#94a3b8;">O que é uma Fila?</strong> No Bitrix24 Contact Center, cada <em>Open Line</em> é uma fila de atendimento.
        Configure aqui qual fila (ID) receberá as mensagens WhatsApp de cada portal instalado.
        O ID correto está em <strong style="color:#94a3b8;">Bitrix24 → Contact Center → Open Lines</strong> — coluna ID.
      </div>
    </div>

    <div id="lista-filas">
      <div style="text-align:center;padding:40px;color:#334155;font-size:13px;">Carregando...</div>
    </div>
  </div>

  <!-- ══════════════════════ INTEGRAÇÕES ══════════════════════ -->
  <div id="page-integracoes" class="page">
    <div class="section-hdr">
      <div>
        <div class="section-title">Integrações Bitrix24</div>
        <div class="section-sub">Vincule cada número WhatsApp a um portal Bitrix24</div>
      </div>
      <button class="btn btn-primary" onclick="abrirModalIntegracao()">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
        Nova Integração
      </button>
    </div>

    <!-- Lista de integrações -->
    <div id="lista-integracoes">
      <div style="text-align:center;padding:40px;color:#334155;font-size:13px;">Carregando...</div>
    </div>
  </div>

</div>

<!-- ══════════════════════ MODAL CONFIRMAÇÃO ══════════════════════ -->
<div id="confirm-modal" style="display:none;position:fixed;inset:0;background:rgba(0,0,0,.75);z-index:60;align-items:center;justify-content:center;backdrop-filter:blur(4px);padding:16px;">
  <div class="card" style="padding:28px;max-width:360px;width:100%;text-align:center;">
    <div style="width:48px;height:48px;background:rgba(239,68,68,.12);border-radius:14px;display:flex;align-items:center;justify-content:center;margin:0 auto 16px;">
      <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="#f87171" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16" stroke-linecap="round"/></svg>
    </div>
    <div style="font-size:16px;font-weight:700;color:#f1f5f9;margin-bottom:8px;">Desconectar número?</div>
    <div style="font-size:13px;color:#64748b;margin-bottom:24px;" id="confirm-msg">Esta ação irá encerrar a sessão WhatsApp e remover o dispositivo.</div>
    <div style="display:flex;gap:10px;justify-content:center;">
      <button class="btn btn-ghost" style="flex:1;" onclick="fecharConfirm()">Cancelar</button>
      <button class="btn" style="flex:1;background:rgba(239,68,68,.15);color:#f87171;border:1px solid rgba(239,68,68,.25);" id="confirm-ok-btn">Desconectar</button>
    </div>
  </div>
</div>

<!-- ══════════════════════ MODAL INTEGRAÇÃO ══════════════════════ -->
<div id="int-modal" style="display:none;position:fixed;inset:0;background:rgba(0,0,0,.75);z-index:60;align-items:center;justify-content:center;backdrop-filter:blur(4px);padding:16px;" onclick="if(event.target===this)fecharModalIntegracao()">
  <div class="card" style="padding:28px;max-width:540px;width:100%;position:relative;max-height:90vh;overflow-y:auto;">
    <button onclick="fecharModalIntegracao()" style="position:absolute;top:14px;right:14px;background:none;border:none;color:#475569;cursor:pointer;padding:4px;border-radius:6px;" onmouseover="this.style.color='#e2e8f0'" onmouseout="this.style.color='#475569'">
      <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
    </button>
    <div style="font-size:17px;font-weight:700;color:#f1f5f9;margin-bottom:4px;" id="int-modal-title">Nova Integração</div>
    <div style="font-size:13px;color:#475569;margin-bottom:22px;" id="int-modal-sub">Vincule um número WhatsApp a um portal Bitrix24</div>

    <div style="display:flex;flex-direction:column;gap:14px;">
      <div class="inp-group" id="int-jid-group">
        <label class="inp-label">Número WhatsApp (sessão)</label>
        <select class="inp" id="int-jid">
          <option value="">Selecione o número conectado...</option>
        </select>
      </div>
      <div class="inp-group">
        <label class="inp-label">Domínio Bitrix24</label>
        <input class="inp" id="int-domain" placeholder="empresa.bitrix24.com.br"/>
      </div>
      <div style="display:grid;grid-template-columns:1fr 1fr;gap:14px;">
        <div class="inp-group">
          <label class="inp-label">Client ID do App</label>
          <input class="inp" id="int-client-id" placeholder="local.XXXXXXXXXX.XXXXXXXXXX"/>
        </div>
        <div class="inp-group">
          <label class="inp-label">Client Secret</label>
          <input class="inp" id="int-client-secret" type="password" placeholder="••••••••••••••••"/>
        </div>
        <div class="inp-group">
          <label class="inp-label">ID da Open Line</label>
          <input class="inp" id="int-openline" type="number" placeholder="1" value="1"/>
        </div>
        <div class="inp-group">
          <label class="inp-label">ID do Conector</label>
          <input class="inp" id="int-connector" placeholder="whatsapp_uc" value="whatsapp_uc"/>
        </div>
      </div>
    </div>

    <!-- Resultado após salvar -->
    <div id="int-resultado" style="margin-top:20px;display:none;padding:16px;border-radius:12px;border:1px solid rgba(37,211,102,.25);background:rgba(37,211,102,.06);">
      <div style="font-size:13px;font-weight:600;color:#25D366;margin-bottom:10px;">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" style="vertical-align:-2px;margin-right:5px;"><polyline points="20 6 9 17 4 12"/></svg>
        Integração salva! Instale o app no Bitrix24:
      </div>
      <div style="font-size:12px;color:#64748b;margin-bottom:8px;">Cole esta URL como <strong style="color:#94a3b8;">URL do Handler</strong> no seu app local no Bitrix24:</div>
      <div style="display:flex;gap:8px;align-items:center;margin-bottom:12px;">
        <input class="inp" id="int-install-url" readonly style="font-family:monospace;font-size:11.5px;flex:1;"/>
        <button class="btn btn-ghost btn-sm" onclick="copiarURL()">Copiar</button>
      </div>
      <div style="font-size:11.5px;color:#475569;line-height:1.9;">
        1. Bitrix24 → <strong style="color:#94a3b8;">Aplicativos → Desenvolver → Seu App</strong><br>
        2. Cole a URL em <strong style="color:#94a3b8;">"URL do handler"</strong> e clique <strong style="color:#94a3b8;">Instalar</strong><br>
        3. O status muda para <strong style="color:#25D366;">Ativo</strong> automaticamente
      </div>
    </div>

    <div style="display:flex;gap:10px;justify-content:flex-end;margin-top:22px;" id="int-modal-actions">
      <button class="btn btn-ghost" onclick="fecharModalIntegracao()">Cancelar</button>
      <button class="btn btn-primary" onclick="salvarIntegracao()" id="int-modal-save-btn">Salvar e Gerar Link</button>
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

// ─── Isolamento por portal ────────────────────────────────────────────────────
// Quando a URL contém ?portal=empresa.bitrix24.com.br, o dashboard filtra
// apenas os dados daquele portal. Sem o param, mostra tudo (modo admin).
var PORTAL = (function() {
  try { return new URLSearchParams(window.location.search).get('portal') || ''; } catch(e) { return ''; }
})();

// Adiciona ?portal= a uma URL de API se estivermos em modo portal
function apiUrl(base) {
  if (!PORTAL) return base;
  var sep = base.indexOf('?') !== -1 ? '&' : '?';
  return base + sep + 'portal=' + encodeURIComponent(PORTAL);
}

// Aplica modo portal: esconde menus de admin, mostra badge do portal
(function() {
  if (!PORTAL) return;
  // Esconde menus que não fazem sentido para o cliente (só admin vê)
  ['nav-integracoes'].forEach(function(id) {
    var el = document.getElementById(id);
    if (el) el.style.display = 'none';
  });
  // Esconde botão "Nova Sessão" na página de sessões (cliente não cadastra sessões)
  var btnNovaSessao = document.getElementById('btn-nova-sessao');
  if (btnNovaSessao) btnNovaSessao.style.display = 'none';
  // Mostra badge do portal no sidebar
  var portalBadge = document.getElementById('sidebar-portal-badge');
  if (portalBadge) {
    portalBadge.style.display = 'block';
    portalBadge.textContent = PORTAL;
  }
})();

// ─── Navegação ────────────────────────────────────────────────────────────────
var titulosPaginas = { painel: 'Painel', sessoes: 'Sessões', filas: 'Filas Bitrix', relatorios: 'Relatórios', integracoes: 'Integrações Bitrix' };

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
  if (nome === 'integracoes') carregarIntegracoes();
  if (nome === 'filas') carregarFilas();
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
  fetch(apiUrl('/ui/overview'))
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

// ─── Helpers de cor para gráficos (adapta ao tema) ───────────────────────────
function chartGridColor() {
  return document.body.classList.contains('tema-claro') ? 'rgba(0,0,0,.08)' : 'rgba(255,255,255,.06)';
}
function chartTickColor() {
  return document.body.classList.contains('tema-claro') ? '#64748b' : '#475569';
}
function chartLegendColor() {
  return document.body.classList.contains('tema-claro') ? '#475569' : '#64748b';
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
      plugins: { legend: { labels: { color: chartLegendColor(), font: { size: 11 }, boxWidth: 10 } } },
      scales: {
        x: { grid: { color: chartGridColor() }, ticks: { color: chartTickColor(), font: { size: 10 }, maxTicksLimit: 8 } },
        y: { grid: { color: chartGridColor() }, ticks: { color: chartTickColor(), font: { size: 10 } }, beginAtZero: true }
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
  fetch(apiUrl('/ui/sessions'))
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
  var telefone = '+' + decodeURIComponent(enc).split(':')[0].split('@')[0];
  abrirConfirm('Desconectar ' + telefone + '?\nO dispositivo será removido do WhatsApp.', function() {
    fetch('/ui/sessions/remove?jid=' + enc, { method: 'DELETE' })
    .then(function() { toast('Sessão desconectada com sucesso', 'success'); carregarSessoes(); carregarVisaoGeral(); })
    .catch(function() { toast('Erro ao desconectar sessão', 'error'); });
  });
}

// ─── Modal de confirmação ─────────────────────────────────────────────────────
var confirmCallback = null;
function abrirConfirm(msg, cb) {
  confirmCallback = cb;
  document.getElementById('confirm-msg').textContent = msg;
  var m = document.getElementById('confirm-modal');
  m.style.display = 'flex';
}
function fecharConfirm() {
  document.getElementById('confirm-modal').style.display = 'none';
  confirmCallback = null;
}
document.getElementById('confirm-ok-btn').addEventListener('click', function() {
  var cb = confirmCallback;
  fecharConfirm();
  if (cb) cb();
});
document.getElementById('confirm-modal').addEventListener('click', function(e) {
  if (e.target === this) fecharConfirm();
});

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
  fetch(apiUrl('/ui/sessions/' + phone + '/qr'))
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
        plugins: { legend: { labels: { color: chartLegendColor(), font: { size: 11 }, boxWidth: 10 } } },
        scales: {
          x: { grid: { display: false }, ticks: { color: chartTickColor(), font: { size: 10 }, maxTicksLimit: 10 } },
          y: { grid: { color: chartGridColor() }, ticks: { color: chartTickColor(), font: { size: 10 } }, beginAtZero: true }
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
        plugins: { legend: { position: 'bottom', labels: { color: chartLegendColor(), font: { size: 11 }, boxWidth: 10, padding: 14 } } }
      }
    });
  }).catch(function() {});
}



// ─── Integrações Bitrix24 ────────────────────────────────────────────────────
var intModalMode = 'new'; // 'new' | 'edit'
var intEditJID = '';

function carregarIntegracoes() {
  fetch('/ui/bitrix/accounts')
  .then(function(r) { return r.json(); })
  .then(function(resp) {
    var data = resp.accounts || [];
    var wrap = document.getElementById('lista-integracoes');
    if (!Array.isArray(data) || data.length === 0) {
      wrap.innerHTML = '<div class="card" style="padding:48px;text-align:center;">'
        + '<svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="#1e293b" stroke-width="1.3" style="margin:0 auto 16px;display:block;"><path d="M10 13a5 5 0 007.54.54l3-3a5 5 0 00-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 00-7.54-.54l-3 3a5 5 0 007.07 7.07l1.71-1.71"/></svg>'
        + '<p style="color:#475569;font-size:14px;margin-bottom:20px;">Nenhuma integração configurada ainda</p>'
        + '<button class="btn btn-primary" onclick="abrirModalIntegracao()">Configurar primeira integração</button>'
        + '</div>';
      return;
    }
    var html = '<div style="display:flex;flex-direction:column;gap:12px;">';
    data.forEach(function(acct) {
      var ativo = acct.status === 'active';
      var statusBadge = ativo
        ? '<span class="badge badge-green"><svg width="7" height="7" viewBox="0 0 8 8" style="margin-right:3px;"><circle cx="4" cy="4" r="4" fill="#25D366"/></svg>Ativo</span>'
        : '<span class="badge badge-yellow">Pendente instalação</span>';
      var telefone = '+' + acct.session_jid.split(':')[0].split('@')[0];
      var enc = encodeURIComponent(acct.session_jid);
      html += '<div class="card" style="padding:20px;">'
        // ── Cabeçalho do card
        + '<div style="display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap;">'
        + '<div style="display:flex;align-items:center;gap:14px;">'
        + '<div class="metric-icon" style="background:rgba(59,130,246,.12);width:44px;height:44px;">'
        + '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#60a5fa" stroke-width="2"><path d="M21 16V8a2 2 0 00-1-1.73l-7-4a2 2 0 00-2 0l-7 4A2 2 0 003 8v8a2 2 0 001 1.73l7 4a2 2 0 002 0l7-4A2 2 0 0021 16z"/></svg>'
        + '</div>'
        + '<div>'
        + '<div style="font-size:15px;font-weight:700;color:#f1f5f9;">' + telefone + '</div>'
        + '<div style="font-size:12px;color:#475569;margin-top:2px;">' + (acct.domain || '') + '</div>'
        + '</div></div>'
        + '<div style="display:flex;align-items:center;gap:8px;">'
        + statusBadge
        + '<button class="btn btn-ghost btn-sm" data-acct=\'' + JSON.stringify(acct).replace(/'/g, "&#39;") + '\' onclick="editarIntegracao(JSON.parse(this.dataset.acct))" style="gap:5px;">'
        + '<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M11 4H4a2 2 0 00-2 2v14a2 2 0 002 2h14a2 2 0 002-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 013 3L12 15l-4 1 1-4 9.5-9.5z"/></svg>'
        + 'Editar</button>'
        + '<button class="btn btn-danger btn-sm" onclick="excluirIntegracao(\'' + enc + '\')" style="gap:5px;">'
        + '<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 01-2 2H8a2 2 0 01-2-2L5 6"/><path d="M10 11v6M14 11v6"/></svg>'
        + 'Remover</button>'
        + '</div></div>'
        // ── Detalhes
        + '<div style="display:grid;grid-template-columns:repeat(3,1fr);gap:8px;margin-top:16px;padding-top:16px;border-top:1px solid rgba(255,255,255,.06);">'
        + '<div style="background:rgba(255,255,255,.03);border-radius:8px;padding:10px 12px;">'
        + '<div style="font-size:10.5px;color:#475569;font-weight:600;text-transform:uppercase;letter-spacing:.05em;margin-bottom:4px;">Open Line</div>'
        + '<div style="font-size:13px;font-weight:600;color:#e2e8f0;">' + (acct.open_line_id || '—') + '</div>'
        + '</div>'
        + '<div style="background:rgba(255,255,255,.03);border-radius:8px;padding:10px 12px;">'
        + '<div style="font-size:10.5px;color:#475569;font-weight:600;text-transform:uppercase;letter-spacing:.05em;margin-bottom:4px;">Conector</div>'
        + '<div style="font-size:13px;font-weight:600;color:#e2e8f0;font-family:monospace;">' + (acct.connector_id || '—') + '</div>'
        + '</div>'
        + '<div style="background:rgba(255,255,255,.03);border-radius:8px;padding:10px 12px;">'
        + '<div style="font-size:10.5px;color:#475569;font-weight:600;text-transform:uppercase;letter-spacing:.05em;margin-bottom:4px;">Client ID</div>'
        + '<div style="font-size:11px;color:#94a3b8;font-family:monospace;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;">' + (acct.client_id || '—') + '</div>'
        + '</div></div>'
        + '</div>';
    });
    html += '</div>';
    wrap.innerHTML = html;
  }).catch(function() {
    document.getElementById('lista-integracoes').innerHTML = '<div style="text-align:center;padding:24px;color:#f87171;font-size:13px;">Erro ao carregar integrações</div>';
  });
}

function _limparModalIntegracao() {
  document.getElementById('int-jid').value = '';
  document.getElementById('int-domain').value = '';
  document.getElementById('int-client-id').value = '';
  document.getElementById('int-client-secret').value = '';
  document.getElementById('int-openline').value = '1';
  document.getElementById('int-connector').value = 'whatsapp_uc';
  document.getElementById('int-resultado').style.display = 'none';
  document.getElementById('int-modal-save-btn').textContent = 'Salvar e Gerar Link';
}

function abrirModalIntegracao() {
  intModalMode = 'new';
  intEditJID = '';
  _limparModalIntegracao();
  document.getElementById('int-modal-title').textContent = 'Nova Integração';
  document.getElementById('int-modal-sub').textContent = 'Vincule um número WhatsApp a um portal Bitrix24';
  document.getElementById('int-jid-group').style.display = 'block';
  // Popula select de sessões
  fetch(apiUrl('/ui/sessions'))
  .then(function(r) { return r.json(); })
  .then(function(d) {
    var sel = document.getElementById('int-jid');
    sel.innerHTML = '<option value="">Selecione o número conectado...</option>';
    if (d.sessions) {
      d.sessions.forEach(function(jid) {
        var telefone = '+' + jid.split(':')[0].split('@')[0];
        var opt = document.createElement('option');
        opt.value = jid;
        opt.textContent = telefone + '  (' + jid + ')';
        sel.appendChild(opt);
      });
    }
  }).catch(function() {});
  document.getElementById('int-modal').style.display = 'flex';
}

function editarIntegracao(acct) {
  intModalMode = 'edit';
  intEditJID = acct.session_jid;
  _limparModalIntegracao();
  var telefone = '+' + acct.session_jid.split(':')[0].split('@')[0];
  document.getElementById('int-modal-title').textContent = 'Editar integração';
  document.getElementById('int-modal-sub').textContent = 'Editando: ' + telefone + ' → ' + (acct.domain || '');
  document.getElementById('int-jid-group').style.display = 'none';
  document.getElementById('int-domain').value = acct.domain || '';
  document.getElementById('int-client-id').value = acct.client_id || '';
  document.getElementById('int-client-secret').value = '';
  document.getElementById('int-openline').value = acct.open_line_id || 1;
  document.getElementById('int-connector').value = acct.connector_id || 'whatsapp_uc';
  document.getElementById('int-modal-save-btn').textContent = 'Salvar Alterações';
  document.getElementById('int-modal').style.display = 'flex';
}

function fecharModalIntegracao() {
  document.getElementById('int-modal').style.display = 'none';
  _limparModalIntegracao();
}

function salvarIntegracao() {
  var jid = intModalMode === 'edit' ? intEditJID : document.getElementById('int-jid').value.trim();
  var domain = document.getElementById('int-domain').value.trim();
  var clientId = document.getElementById('int-client-id').value.trim();
  var clientSecret = document.getElementById('int-client-secret').value.trim();
  var openLine = parseInt(document.getElementById('int-openline').value) || 1;
  var connectorId = document.getElementById('int-connector').value.trim() || 'whatsapp_uc';

  if (!jid) { toast('Selecione um número WhatsApp', 'error'); return; }
  if (!domain) { toast('Preencha o domínio Bitrix24', 'error'); return; }
  if (!clientId) { toast('Preencha o Client ID', 'error'); return; }
  if (intModalMode === 'new' && !clientSecret) { toast('Preencha o Client Secret', 'error'); return; }

  var btn = document.getElementById('int-modal-save-btn');
  btn.disabled = true;
  btn.textContent = 'Salvando...';

  var payload = { session_jid: jid, domain: domain, client_id: clientId, open_line_id: openLine, connector_id: connectorId };
  if (clientSecret) payload.client_secret = clientSecret;

  fetch('/ui/bitrix/accounts', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload)
  })
  .then(function(r) { return r.json(); })
  .then(function(d) {
    btn.disabled = false;
    btn.textContent = intModalMode === 'edit' ? 'Salvar Alterações' : 'Salvar e Gerar Link';
    if (d.error) { toast(d.error, 'error'); return; }
    if (d.install_url) {
      document.getElementById('int-install-url').value = d.install_url;
      document.getElementById('int-resultado').style.display = 'block';
    }
    toast(intModalMode === 'edit' ? 'Integração atualizada!' : 'Integração salva! Copie a URL e instale o app.', 'success');
    carregarIntegracoes();
  })
  .catch(function() {
    btn.disabled = false;
    btn.textContent = intModalMode === 'edit' ? 'Salvar Alterações' : 'Salvar e Gerar Link';
    toast('Erro ao salvar integração', 'error');
  });
}

function copiarURL() {
  var input = document.getElementById('int-install-url');
  var val = input.value;
  if (!val) return;
  try {
    navigator.clipboard.writeText(val).then(function() {
      toast('URL copiada para a área de transferência', 'success');
    }).catch(function() { _copiarFallback(input); });
  } catch(e) { _copiarFallback(input); }
}
function _copiarFallback(input) {
  input.select();
  document.execCommand('copy');
  toast('URL copiada', 'success');
}

function excluirIntegracao(enc) {
  var jid = decodeURIComponent(enc);
  var telefone = '+' + jid.split(':')[0].split('@')[0];
  abrirConfirm('Remover a integração do número ' + telefone + '?\nO vínculo com o Bitrix24 será desfeito.', function() {
    fetch('/ui/bitrix/accounts?jid=' + enc, { method: 'DELETE' })
    .then(function(r) {
      if (r.ok) { toast('Integração removida', 'success'); carregarIntegracoes(); }
      else { toast('Erro ao remover integração', 'error'); }
    })
    .catch(function() { toast('Erro ao remover integração', 'error'); });
  });
}

// ─── Filas Bitrix ─────────────────────────────────────────────────────────────
function carregarFilas() {
  var wrap = document.getElementById('lista-filas');
  fetch(apiUrl('/ui/bitrix/queues'))
  .then(function(r) { return r.json(); })
  .then(function(resp) {
    var data = resp.queues || [];
    if (!Array.isArray(data) || data.length === 0) {
      wrap.innerHTML = '<div class="card" style="padding:48px;text-align:center;">'
        + '<svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="#1e293b" stroke-width="1.3" style="margin:0 auto 16px;display:block;"><path d="M17 21v-2a4 4 0 00-4-4H5a4 4 0 00-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 00-3-3.87"/><path d="M16 3.13a4 4 0 010 7.75"/></svg>'
        + '<p style="color:#475569;font-size:14px;margin-bottom:8px;">Nenhum portal instalado via Marketplace</p>'
        + '<p style="color:#334155;font-size:12.5px;">Instale o app no Bitrix24 Marketplace para que os portais apareçam aqui.</p>'
        + '</div>';
      return;
    }
    var html = '<div style="display:flex;flex-direction:column;gap:12px;">';
    data.forEach(function(q) {
      var sessHtml = '';
      if (q.linked_sessions && q.linked_sessions.length > 0) {
        sessHtml = q.linked_sessions.map(function(jid) {
          var tel = '+' + jid.split(':')[0].split('@')[0];
          return '<span class="badge badge-green" style="margin-right:4px;">' + tel + '</span>';
        }).join('');
      } else {
        sessHtml = '<span style="font-size:12px;color:#475569;">Nenhuma sessão vinculada</span>';
      }
      var instaladoEm = q.installed_at ? new Date(q.installed_at).toLocaleDateString('pt-BR') : '—';
      html += '<div class="card" style="padding:20px;">'
        // Cabeçalho
        + '<div style="display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap;margin-bottom:16px;">'
        + '<div style="display:flex;align-items:center;gap:14px;">'
        + '<div class="metric-icon" style="background:rgba(192,132,252,.12);width:44px;height:44px;">'
        + '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#c084fc" stroke-width="2"><path d="M17 21v-2a4 4 0 00-4-4H5a4 4 0 00-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 00-3-3.87"/><path d="M16 3.13a4 4 0 010 7.75"/></svg>'
        + '</div>'
        + '<div>'
        + '<div style="font-size:15px;font-weight:700;color:#f1f5f9;">' + q.domain + '</div>'
        + '<div style="font-size:11.5px;color:#475569;margin-top:2px;">Instalado em ' + instaladoEm + '</div>'
        + '</div></div>'
        + '<span class="badge badge-purple">Marketplace</span>'
        + '</div>'
        // Sessões vinculadas
        + '<div style="margin-bottom:14px;">'
        + '<div style="font-size:10.5px;color:#475569;font-weight:600;text-transform:uppercase;letter-spacing:.05em;margin-bottom:7px;">Sessões WhatsApp vinculadas</div>'
        + sessHtml
        + '</div>'
        // Editor de Open Line
        + '<div style="padding-top:14px;border-top:1px solid rgba(255,255,255,.06);">'
        + '<div style="font-size:10.5px;color:#475569;font-weight:600;text-transform:uppercase;letter-spacing:.05em;margin-bottom:8px;">Fila de Atendimento (Open Line ID)</div>'
        + '<div style="display:flex;align-items:center;gap:10px;flex-wrap:wrap;">'
        + '<input class="inp" type="number" min="1" id="fila-line-' + q.domain + '" value="' + (q.open_line_id || 1) + '" style="max-width:110px;" placeholder="1"/>'
        + '<div style="font-size:12px;color:#475569;">Conector: <span style="color:#94a3b8;font-family:monospace;">' + (q.connector_id || 'whatsapp_uc') + '</span></div>'
        + '<button class="btn btn-primary btn-sm" onclick="salvarFila(\'' + q.domain + '\')" style="margin-left:auto;">Salvar Fila</button>'
        + '</div>'
        + '<div style="font-size:11.5px;color:#475569;margin-top:8px;">Encontre o ID em Bitrix24 → <strong style="color:#94a3b8;">Contact Center → Open Lines</strong></div>'
        + '</div>'
        + '</div>';
    });
    html += '</div>';
    wrap.innerHTML = html;
  })
  .catch(function() {
    wrap.innerHTML = '<div style="text-align:center;padding:24px;color:#f87171;font-size:13px;">Erro ao carregar filas</div>';
  });
}

function salvarFila(domain) {
  var input = document.getElementById('fila-line-' + domain);
  if (!input) return;
  var lineId = parseInt(input.value);
  if (!lineId || lineId < 1) { toast('ID da fila deve ser maior que zero', 'error'); return; }

  fetch('/ui/bitrix/queues', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ domain: domain, open_line_id: lineId })
  })
  .then(function(r) { return r.json(); })
  .then(function(d) {
    if (d.error) { toast(d.error, 'error'); return; }
    toast('Fila atualizada para ' + domain + ' → Open Line ' + lineId, 'success');
    carregarFilas();
  })
  .catch(function() { toast('Erro ao salvar fila', 'error'); });
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
  if (paginaAtual === 'integracoes') carregarIntegracoes();
  if (paginaAtual === 'filas') carregarFilas();
  toast('Dados atualizados', 'success');
}

// ─── Tema claro/escuro ────────────────────────────────────────────────────────
var SOL_CLARO = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 640 640" width="16" height="16"><path fill="currentColor" d="M320 32C328.4 32 336.3 36.4 340.6 43.7L396.1 136.3L500.9 110C509.1 108 517.8 110.4 523.7 116.3C529.6 122.2 532 131 530 139.1L503.7 243.8L596.4 299.3C603.6 303.6 608.1 311.5 608.1 319.9C608.1 328.3 603.7 336.2 596.4 340.5L503.7 396.1L530 500.8C532 509 529.6 517.7 523.7 523.6C517.8 529.5 509 532 500.9 530L396.2 503.7L340.7 596.4C336.4 603.6 328.5 608.1 320.1 608.1C311.7 608.1 303.8 603.7 299.5 596.4L243.9 503.7L139.2 530C131 532 122.4 529.6 116.4 523.7C110.4 517.8 108 509 110 500.8L136.2 396.1L43.6 340.6C36.4 336.2 32 328.4 32 320C32 311.6 36.4 303.7 43.7 299.4L136.3 243.9L110 139.1C108 130.9 110.3 122.3 116.3 116.3C122.3 110.3 131 108 139.2 110L243.9 136.2L299.4 43.6L301.2 41C305.7 35.3 312.6 31.9 320 31.9zM320 176C240.5 176 176 240.5 176 320C176 399.5 240.5 464 320 464C399.5 464 464 399.5 464 320C464 240.5 399.5 176 320 176zM320 416C267 416 224 373 224 320C224 267 267 224 320 224C373 224 416 267 416 320C416 373 373 416 320 416z"/></svg>';
var LUA_CLARA = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 640 640" width="16" height="16"><path fill="currentColor" d="M320 64C178.6 64 64 178.6 64 320C64 461.4 178.6 576 320 576C388.8 576 451.3 548.8 497.3 504.6C504.6 497.6 506.7 486.7 502.6 477.5C498.5 468.3 488.9 462.6 478.8 463.4C473.9 463.8 469 464 464 464C362.4 464 280 381.6 280 280C280 207.9 321.5 145.4 382.1 115.2C391.2 110.7 396.4 100.9 395.2 90.8C394 80.7 386.6 72.5 376.7 70.3C358.4 66.2 339.4 64 320 64z"/></svg>';

function _aplicarTema(claro) {
  var btn = document.getElementById('btn-tema');
  var icone = document.getElementById('tema-icone');
  var label = document.getElementById('tema-label');
  if (claro) {
    document.body.classList.add('tema-claro');
    icone.innerHTML = LUA_CLARA;
    label.textContent = 'Modo Escuro';
    if (btn) { btn.style.background = 'rgba(0,0,0,.04)'; btn.style.borderColor = 'rgba(0,0,0,.08)'; btn.style.color = '#475569'; }
  } else {
    document.body.classList.remove('tema-claro');
    icone.innerHTML = SOL_CLARO;
    label.textContent = 'Modo Claro';
    if (btn) { btn.style.background = 'rgba(255,255,255,.04)'; btn.style.borderColor = 'rgba(255,255,255,.08)'; btn.style.color = '#64748b'; }
  }
}

function toggleTema() {
  var claro = !document.body.classList.contains('tema-claro');
  _aplicarTema(claro);
  try { localStorage.setItem('tema', claro ? 'claro' : 'escuro'); } catch(e) {}
  // Recria gráficos com as novas cores
  carregarVisaoGeral();
  if (paginaAtual === 'relatorios') carregarRelatorios(periodoRelatorio);
}

// Aplica tema salvo ao carregar
(function() {
  var saved = '';
  try { saved = localStorage.getItem('tema') || ''; } catch(e) {}
  _aplicarTema(saved === 'claro');
})();

// ─── Init ─────────────────────────────────────────────────────────────────────
carregarVisaoGeral();
setInterval(carregarVisaoGeral, 10000);
</script>
</body>
</html>`
