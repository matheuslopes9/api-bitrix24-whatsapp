package api

import "github.com/gofiber/fiber/v2"

// GET /dashboard
func (h *handlers) dashboardPage(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/html; charset=utf-8")
	return c.SendString(dashboardHTML)
}

// GET /ui/overview — dados agregados para a dashboard (sem auth, apenas interna)
//
// "Sessoes ativas" no painel/sidebar le whatsapp_sessions.status='active'
// (qr + cloud), nao h.waManager.ListSessions() — esse ultimo so conta
// whatsmeow em memoria, ignora Cloud API. Tenants que so usam Cloud
// apareciam como "0 sessões ativas / Sem sessão" mesmo com a sessao
// funcionando.
func (h *handlers) uiOverview(c *fiber.Ctx) error {
	in, out, dead := h.q.Lengths(c.Context())

	stats, _ := h.repo.GetDailyStats(c.Context(), 1)
	var msgsIn, msgsOut int64
	for _, s := range stats {
		msgsIn += s.InboundCount
		msgsOut += s.OutboundCount
	}

	dbSessions, _ := h.repo.ListActiveSessions(c.Context())
	jids := make([]string, 0, len(dbSessions))
	for _, s := range dbSessions {
		jids = append(jids, s.JID)
	}

	return c.JSON(fiber.Map{
		"active_sessions":   len(jids),
		"sessions":          jids,
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
<title>UC Talk — Painel</title>
<script src="/assets/chart.js"></script>
<link rel="icon" type="image/png" href="/assets/logo.png">
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Plus+Jakarta+Sans:wght@300;400;500;600;700;800&display=swap" rel="stylesheet">
<style>
*{box-sizing:border-box;margin:0;padding:0;}
html,body{font-family:'Plus Jakarta Sans',system-ui,-apple-system,sans-serif;background:#0a0e1a;color:#e2e8f0;min-height:100vh;-webkit-font-smoothing:antialiased;}

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
.btn{display:inline-flex;align-items:center;gap:7px;padding:9px 18px;border-radius:11px;font-size:13.5px;font-weight:700;cursor:pointer;border:none;transition:all .15s;letter-spacing:.01em;font-family:'Plus Jakarta Sans',system-ui,sans-serif;}
.btn-primary{background:linear-gradient(135deg,#25D366,#1ebe5d);color:#041a0a;box-shadow:0 2px 12px rgba(37,211,102,.25);}
.btn-primary:hover{background:linear-gradient(135deg,#2de870,#25D366);transform:translateY(-1px);box-shadow:0 4px 18px rgba(37,211,102,.35);}
.btn-primary:active{transform:translateY(0);}
.btn-ghost{background:rgba(255,255,255,.06);color:#94a3b8;border:1.5px solid rgba(255,255,255,.1);}
.btn-ghost:hover{background:rgba(255,255,255,.1);color:#e2e8f0;border-color:rgba(255,255,255,.16);}
.btn-danger{background:rgba(239,68,68,.12);color:#f87171;border:1.5px solid rgba(239,68,68,.2);padding:7px 13px;font-size:13px;}
.btn-danger:hover{background:rgba(239,68,68,.22);}
.btn-sm{padding:6px 13px;font-size:12.5px;}
.btn-icon{width:34px;height:34px;padding:0;justify-content:center;border-radius:9px;}

/* ── Inputs ── */
.inp{width:100%;background:rgba(255,255,255,.06);border:1.5px solid rgba(255,255,255,.1);border-radius:12px;padding:11px 15px;color:#e2e8f0;font-size:13.5px;font-weight:500;font-family:'Plus Jakarta Sans',system-ui,sans-serif;outline:none;transition:border-color .2s,background .2s,box-shadow .2s;letter-spacing:.01em;}
.inp:focus{border-color:rgba(37,211,102,.55);background:rgba(255,255,255,.08);box-shadow:0 0 0 3px rgba(37,211,102,.1);}
.inp::placeholder{color:#3d4f66;font-weight:400;}
.inp:disabled{color:#3d4f66;cursor:default;opacity:.7;}
select.inp{appearance:none;-webkit-appearance:none;background-color:rgba(255,255,255,.06);background-image:url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='14' height='14' viewBox='0 0 24 24' fill='none' stroke='%2364748b' stroke-width='2.5' stroke-linecap='round' stroke-linejoin='round'%3E%3Cpolyline points='6 9 12 15 18 9'/%3E%3C/svg%3E");background-repeat:no-repeat;background-position:right 13px center;padding-right:38px;cursor:pointer;}
select.inp option{background:#131929;color:#e2e8f0;padding:10px 14px;font-family:'Plus Jakarta Sans',system-ui,sans-serif;}
select.inp optgroup{background:#0f1623;color:#64748b;font-weight:700;font-size:11px;}
select.inp:focus{border-color:rgba(37,211,102,.55);box-shadow:0 0 0 3px rgba(37,211,102,.1);}
.inp-group{display:flex;flex-direction:column;gap:7px;}
.export-item{display:flex;align-items:center;gap:9px;width:100%;padding:9px 11px;border-radius:8px;background:none;border:none;color:#cbd5e1;font-size:12.5px;font-weight:500;font-family:'Plus Jakarta Sans',system-ui,sans-serif;cursor:pointer;text-align:left;transition:background .15s,color .15s;}
.export-item:hover{background:rgba(255,255,255,.07);color:#f1f5f9;}
.inp-label{font-size:11px;color:#64748b;font-weight:700;text-transform:uppercase;letter-spacing:.07em;}

/* ── Modal ── */
.modal-box{background:#111827;border:1.5px solid rgba(255,255,255,.1);border-radius:20px;padding:28px;box-shadow:0 24px 64px rgba(0,0,0,.6);}

/* ── Custom Select (dropdown 100% CSS/JS, sem nativo) ── */
.cselect{position:relative;width:100%;}
.cselect-trigger{width:100%;background:rgba(255,255,255,.06);border:1.5px solid rgba(255,255,255,.1);border-radius:12px;padding:11px 38px 11px 15px;color:#e2e8f0;font-size:13.5px;font-weight:500;font-family:'Plus Jakarta Sans',system-ui,sans-serif;cursor:pointer;text-align:left;display:flex;align-items:center;justify-content:space-between;gap:8px;transition:border-color .2s,background .2s,box-shadow .2s;user-select:none;}
.cselect-trigger:hover{background:rgba(255,255,255,.08);}
.cselect-trigger.open,.cselect-trigger:focus{border-color:rgba(37,211,102,.55);background:rgba(255,255,255,.08);box-shadow:0 0 0 3px rgba(37,211,102,.1);outline:none;}
.cselect-trigger .cselect-placeholder{color:#3d4f66;font-weight:400;}
.cselect-trigger .cselect-arrow{flex-shrink:0;transition:transform .2s;color:#64748b;}
.cselect-trigger.open .cselect-arrow{transform:rotate(180deg);}
.cselect-dropdown{display:none;position:absolute;top:calc(100% + 4px);left:0;right:0;background:#131929;border:1.5px solid rgba(255,255,255,.12);border-radius:14px;box-shadow:0 16px 48px rgba(0,0,0,.6);z-index:99999;overflow:hidden;}
.cselect-dropdown.open{display:flex;flex-direction:column;max-height:260px;}
.cselect-search{padding:10px 12px 6px;flex-shrink:0;background:#131929;}
#fila-openline-options{overflow-y:auto;flex:1;}
.cselect-search input{width:100%;background:rgba(255,255,255,.07);border:1.5px solid rgba(255,255,255,.1);border-radius:9px;padding:8px 12px;color:#e2e8f0;font-size:13px;font-family:'Plus Jakarta Sans',system-ui,sans-serif;outline:none;}
.cselect-search input:focus{border-color:rgba(37,211,102,.5);}
.cselect-option{padding:10px 15px;font-size:13.5px;font-weight:500;color:#cbd5e1;cursor:pointer;transition:background .12s,color .12s;display:flex;align-items:center;gap:8px;}
.cselect-option:hover{background:rgba(255,255,255,.07);color:#f1f5f9;}
.cselect-option.selected{background:rgba(37,211,102,.12);color:#25D366;}
.cselect-option.highlighted{background:rgba(37,211,102,.08);}
.cselect-empty{padding:16px 15px;font-size:13px;color:#475569;text-align:center;}
::-webkit-scrollbar{width:4px;}
::-webkit-scrollbar-track{background:transparent;}
::-webkit-scrollbar-thumb{background:rgba(255,255,255,.1);border-radius:4px;}

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

/* ── Animations ── */
@keyframes spin{from{transform:rotate(0deg)}to{transform:rotate(360deg)}}

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
body.tema-claro .inp{background:rgba(0,0,0,.04);border-color:rgba(0,0,0,.14);color:#0f172a;}
body.tema-claro .inp:focus{border-color:rgba(37,211,102,.5);background:#fff;box-shadow:0 0 0 3px rgba(37,211,102,.1);}
body.tema-claro .inp::placeholder{color:#94a3b8;}
body.tema-claro select.inp{background-color:rgba(0,0,0,.04);}
body.tema-claro select.inp option{background:#fff;color:#0f172a;}
body.tema-claro .modal-box{background:#f8fafc;border-color:rgba(0,0,0,.1);}
body.tema-claro .cselect-trigger{background:rgba(0,0,0,.04);border-color:rgba(0,0,0,.14);color:#0f172a;}
body.tema-claro .cselect-trigger:hover{background:rgba(0,0,0,.07);}
body.tema-claro .cselect-trigger.open{border-color:rgba(37,211,102,.5);background:#fff;}
body.tema-claro .cselect-placeholder{color:#94a3b8;}
body.tema-claro .cselect-dropdown{background:#fff;border-color:rgba(0,0,0,.1);box-shadow:0 16px 48px rgba(0,0,0,.12);}
body.tema-claro .cselect-search{background:#fff;border-bottom:1px solid rgba(0,0,0,.06);}
body.tema-claro .cselect-search input{background:rgba(0,0,0,.05);border-color:rgba(0,0,0,.1);color:#0f172a;}
body.tema-claro .cselect-option{color:#334155;}
body.tema-claro .cselect-option:hover{background:rgba(0,0,0,.05);color:#0f172a;}
body.tema-claro .cselect-option.selected{background:rgba(37,211,102,.1);color:#16a34a;}
body.tema-claro .cselect-empty{color:#94a3b8;}
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
      <div style="font-size:13px;font-weight:700;color:#f1f5f9;">UC Talk</div>
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
  <div class="nav-item" id="nav-filas" onclick="showPage('filas')">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M17 21v-2a4 4 0 00-4-4H5a4 4 0 00-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 00-3-3.87"/><path d="M16 3.13a4 4 0 010 7.75"/></svg>
    Filas Bitrix
  </div>
  <div class="nav-item" id="nav-permissoes" onclick="showPage('permissoes')">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>
    Permissões
  </div>
  <div class="nav-item" id="nav-templates" onclick="showPage('templates')">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="8" y1="13" x2="16" y2="13"/><line x1="8" y1="17" x2="13" y2="17"/></svg>
    Templates
  </div>
  <div class="nav-item" id="nav-historico" onclick="showPage('historico')">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/></svg>
    Histórico
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
        <div class="section-sub">Conecte e gerencie quantos números forem necessários (QR ou API Oficial)</div>
      </div>
      <div id="sessoes-actions" style="display:flex;gap:8px;flex-wrap:wrap;">
        <button class="btn btn-ghost" id="btn-refresh-status" onclick="atualizarStatusSessoes()" title="Verifica saúde de cada sessão (QR e Oficial)">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" id="btn-refresh-icon"><polyline points="23 4 23 10 17 10"/><polyline points="1 20 1 14 7 14"/><path d="M3.51 9a9 9 0 0114.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0020.49 15"/></svg>
          Atualizar status
        </button>
        <button class="btn btn-primary" id="btn-nova-sessao" onclick="abrirModalNovaSessao('qr')" title="Conectar via Multi-Device (whatsmeow)">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
          Multi-Device
        </button>
        <button class="btn btn-ghost" id="btn-nova-sessao-cloud" onclick="abrirModalNovaSessao('cloud')" title="Conectar via WhatsApp Business API (Meta Oficial)" style="border:1px solid rgba(59,130,246,.4);color:#60a5fa;">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
          API Oficial
        </button>
      </div>
    </div>
    <div id="lista-sessoes"><div style="text-align:center;padding:40px;color:#334155;">Carregando...</div></div>
  </div>

  <!-- ══════════════════════ RELATÓRIOS ══════════════════════ -->
  <div id="page-relatorios" class="page">
    <div class="section-hdr">
      <div>
        <div class="section-title">Relatórios</div>
        <div class="section-sub">Análise detalhada de atendimentos via WhatsApp</div>
      </div>
      <div style="display:flex;gap:8px;align-items:center;flex-wrap:wrap;">
        <div class="tab-bar">
          <button class="tab active" onclick="setPeriodo(7,this)">7 dias</button>
          <button class="tab" onclick="setPeriodo(14,this)">14 dias</button>
          <button class="tab" onclick="setPeriodo(30,this)">30 dias</button>
          <button class="tab" onclick="setPeriodo(90,this)">90 dias</button>
        </div>
        <div style="position:relative;" id="export-menu-wrap">
          <button class="btn btn-ghost btn-sm" onclick="toggleExportMenu()" style="gap:6px;">
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 15v4a2 2 0 01-2 2H5a2 2 0 01-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>
            Exportar
            <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="6 9 12 15 18 9"/></svg>
          </button>
          <div id="export-dropdown" style="display:none;position:absolute;right:0;top:calc(100% + 6px);background:#1e293b;border:1px solid rgba(255,255,255,.1);border-radius:10px;padding:6px;min-width:220px;z-index:50;box-shadow:0 8px 24px rgba(0,0,0,.4);">
            <div style="font-size:10px;color:#475569;font-weight:700;text-transform:uppercase;letter-spacing:.06em;padding:4px 10px 6px;">Relatório atual</div>
            <button class="export-item" onclick="exportar('csv')">
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="#4ade80" stroke-width="2"><path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="16" y1="13" x2="8" y2="13"/><line x1="16" y1="17" x2="8" y2="17"/></svg>
              CSV (separado por ponto-vírgula)
            </button>
            <button class="export-item" onclick="exportar('xlsx')">
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="#34d399" stroke-width="2"><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M3 9h18M9 21V9"/></svg>
              Excel XLSX (moderno)
            </button>
            <button class="export-item" onclick="exportar('xls')">
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="#6ee7b7" stroke-width="2"><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M3 9h18M9 21V9"/></svg>
              Excel XLS (legado)
            </button>
            <button class="export-item" onclick="exportar('xml')">
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="#60a5fa" stroke-width="2"><polyline points="16 18 22 12 16 6"/><polyline points="8 6 2 12 8 18"/></svg>
              XML
            </button>
            <div style="border-top:1px solid rgba(255,255,255,.06);margin:6px 0;"></div>
            <div style="font-size:10px;color:#475569;font-weight:700;text-transform:uppercase;letter-spacing:.06em;padding:4px 10px 6px;">Exportar tudo</div>
            <button class="export-item" onclick="exportarTodos('xlsx')">
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="#f59e0b" stroke-width="2"><path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>
              Todos os relatórios (XLSX)
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- Totais -->
    <div class="grid-4" style="margin-bottom:14px;">
      <div class="card" style="padding:18px;">
        <div class="metric-lbl" style="margin-bottom:8px;">Total de Mensagens</div>
        <div class="metric-val" id="r-total">--</div>
      </div>
      <div class="card" style="padding:18px;">
        <div class="metric-lbl" style="margin-bottom:8px;">Recebidas</div>
        <div class="metric-val" style="color:#60a5fa;" id="r-recebidas">--</div>
      </div>
      <div class="card" style="padding:18px;">
        <div class="metric-lbl" style="margin-bottom:8px;">Enviadas</div>
        <div class="metric-val" style="color:#c084fc;" id="r-enviadas">--</div>
      </div>
      <div class="card" style="padding:18px;">
        <div class="metric-lbl" style="margin-bottom:8px;">Falhas</div>
        <div class="metric-val" style="color:#f87171;" id="r-falhas">--</div>
      </div>
    </div>

    <!-- Gráficos principais -->
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

    <!-- Gráficos secundários -->
    <div class="grid-2" style="margin-bottom:14px;">
      <div class="card" style="padding:18px;">
        <div class="card-title">Volume por Hora do Dia</div>
        <canvas id="chart-horas" height="110"></canvas>
      </div>
      <div class="card" style="padding:18px;">
        <div class="card-title">Tipos de Mensagem</div>
        <canvas id="chart-tipos" height="110"></canvas>
      </div>
    </div>

    <!-- Por número WA -->
    <div class="card" style="padding:18px;margin-bottom:14px;">
      <div class="card-title">Por Número WhatsApp (Fila)</div>
      <div style="overflow-x:auto;">
        <table class="tbl">
          <thead><tr>
            <th>Número</th><th>Tipo</th><th>Total</th><th style="color:#60a5fa;">Recebidas</th><th style="color:#c084fc;">Enviadas</th><th style="color:#f87171;">Falhas</th>
          </tr></thead>
          <tbody id="r-sessoes"><tr><td colspan="6" style="text-align:center;padding:24px;color:#334155;">Carregando...</td></tr></tbody>
        </table>
      </div>
    </div>

    <!-- Top contatos -->
    <div class="card" style="padding:18px;margin-bottom:14px;">
      <div class="card-title">Top 20 Contatos</div>
      <div style="overflow-x:auto;">
        <table class="tbl">
          <thead><tr>
            <th>Contato</th><th>Telefone</th><th>Total</th><th style="color:#60a5fa;">Recebidas</th><th style="color:#c084fc;">Enviadas</th>
          </tr></thead>
          <tbody id="r-contatos"><tr><td colspan="5" style="text-align:center;padding:24px;color:#334155;">Carregando...</td></tr></tbody>
        </table>
      </div>
    </div>

    <!-- Histórico diário -->
    <div class="card" style="padding:18px;">
      <div class="card-title">Histórico por Dia</div>
      <div style="overflow-x:auto;">
        <table class="tbl">
          <thead><tr>
            <th>Data</th><th>Total</th><th style="color:#60a5fa;">Recebidas</th><th style="color:#c084fc;">Enviadas</th>
          </tr></thead>
          <tbody id="r-tabela"><tr><td colspan="4" style="text-align:center;padding:24px;color:#334155;">Carregando...</td></tr></tbody>
        </table>
      </div>
    </div>
  </div>

  <!-- ══════════════════════ PERMISSÕES CRM ══════════════════════ -->
  <div id="page-permissoes" class="page">
    <div class="section-hdr">
      <div>
        <div class="section-title">Permissões CRM</div>
        <div class="section-sub">Libere quais usuários do Bitrix24 podem acessar o CRM tab</div>
      </div>
    </div>

    <!-- Info box -->
    <div class="card-flat" style="padding:14px 18px;margin-bottom:18px;display:flex;align-items:flex-start;gap:12px;">
      <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="#60a5fa" stroke-width="2" style="flex-shrink:0;margin-top:2px;"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="8"/><line x1="12" y1="12" x2="12" y2="16"/></svg>
      <div style="font-size:12.5px;color:#64748b;line-height:1.7;">
        Quem aparece na lista abaixo pode usar a aba <strong style="color:#cbd5e1;">UC Talk</strong> dentro do CRM do Bitrix24 (Contato, Lead, Deal). <br>
        Lista vazia = ninguém acessa. Para encontrar o ID do usuário no Bitrix24, abra o perfil dele e copie da URL: <code style="background:rgba(255,255,255,.06);padding:1px 5px;border-radius:3px">/company/personal/user/<b>ID</b>/</code>
      </div>
    </div>

    <!-- Bloco: liberar novo usuário (busca por nome ou ID) -->
    <div class="card" style="padding:18px;margin-bottom:16px;">
      <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:10px;">
        <div style="font-size:13px;color:#cbd5e1;font-weight:600;">Liberar novo usuário</div>
        <button class="btn btn-ghost btn-sm" id="perm-refresh-btn" onclick="carregarTodosUsuarios(true)" title="Forçar atualização da lista do Bitrix" style="font-size:11px;color:#94a3b8;">
          ↻ Atualizar lista
        </button>
      </div>
      <input type="text" id="perm-search-input" class="inp" placeholder="Buscar por nome, email ou ID..." style="width:100%;margin-bottom:10px;" oninput="filtrarTodosUsuarios()">
      <div id="perm-search-results" style="background:rgba(0,0,0,.18);border:1px solid rgba(255,255,255,.06);border-radius:8px;max-height:420px;overflow-y:auto;">
        <div style="padding:18px;text-align:center;color:#475569;font-size:12px;">Carregando usuários do Bitrix...</div>
      </div>
      <div id="perm-preview" style="display:none;margin-top:14px;padding:13px;background:rgba(37,211,102,.07);border:1px solid rgba(37,211,102,.25);border-radius:8px;">
        <div id="perm-preview-body" style="font-size:13px;color:#e2e8f0;margin-bottom:10px;"></div>
        <div style="display:flex;gap:8px;">
          <button class="btn btn-primary btn-sm" id="perm-grant-btn" onclick="permGrantUser()">✓ Liberar acesso</button>
          <button class="btn btn-ghost btn-sm" onclick="permCancelarSelecao()" style="color:#94a3b8;">Cancelar</button>
        </div>
      </div>
    </div>

    <!-- Bloco: lista de liberados -->
    <div class="card" style="padding:0;">
      <div style="padding:14px 18px;border-bottom:1px solid rgba(255,255,255,.06);display:flex;justify-content:space-between;align-items:center;">
        <div style="font-size:13px;color:#cbd5e1;font-weight:600;">Usuários liberados</div>
        <span style="font-size:11px;color:#64748b;" id="perm-status">—</span>
      </div>
      <div id="perm-list" style="padding:8px;">
        <div style="padding:24px;text-align:center;color:#334155;font-size:13px;">Carregando...</div>
      </div>
    </div>
  </div>

  <!-- ══════════════════════ TEMPLATES DE MENSAGEM ══════════════════════ -->
  <div id="page-templates" class="page">
    <div class="section-hdr">
      <div>
        <div class="section-title">Templates de Mensagem</div>
        <div class="section-sub">Quick replies que aparecem para os atendentes no compositor do CRM</div>
      </div>
      <button class="btn btn-primary" onclick="abrirModalTemplate()">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
        Novo Template
      </button>
    </div>

    <!-- Info box -->
    <div class="card-flat" style="padding:14px 18px;margin-bottom:18px;display:flex;align-items:flex-start;gap:12px;">
      <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="#60a5fa" stroke-width="2" style="flex-shrink:0;margin-top:2px;"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="8"/><line x1="12" y1="12" x2="12" y2="16"/></svg>
      <div style="font-size:12.5px;color:#64748b;line-height:1.7;">
        Templates aparecem na aba <strong style="color:#cbd5e1;">UC Talk</strong> dentro do CRM, no botão de templates ao lado do anexo. Atendente clica e o texto cai direto no compositor. <br>
        <span style="color:#475569;">Use para saudações, respostas frequentes, condições comerciais — qualquer texto repetido vira clique.</span>
      </div>
    </div>

    <!-- Lista -->
    <div class="card" style="padding:0;">
      <div style="padding:14px 18px;border-bottom:1px solid rgba(255,255,255,.06);display:flex;justify-content:space-between;align-items:center;">
        <div style="font-size:13px;color:#cbd5e1;font-weight:600;">Templates cadastrados</div>
        <span style="font-size:11px;color:#64748b;" id="tpl-status">—</span>
      </div>
      <div id="tpl-list" style="padding:8px;">
        <div style="padding:24px;text-align:center;color:#334155;font-size:13px;">Carregando...</div>
      </div>
    </div>
  </div>

  <!-- Modal Template -->
  <div id="modal-tpl" style="display:none;position:fixed;inset:0;background:rgba(0,0,0,.6);z-index:1000;align-items:center;justify-content:center;padding:20px;">
    <div style="background:#0f172a;border:1px solid #334155;border-radius:12px;width:100%;max-width:560px;box-shadow:0 20px 60px rgba(0,0,0,.5);">
      <div style="padding:16px 20px;border-bottom:1px solid rgba(255,255,255,.06);display:flex;justify-content:space-between;align-items:center;">
        <div style="font-size:14px;font-weight:600;color:#e2e8f0;" id="modal-tpl-titulo">Novo Template</div>
        <button onclick="fecharModalTemplate()" style="background:none;border:0;color:#64748b;cursor:pointer;font-size:18px;">✕</button>
      </div>
      <div style="padding:20px;">
        <input type="hidden" id="tpl-edit-id" value="">
        <div style="font-size:11px;color:#94a3b8;font-weight:600;text-transform:uppercase;letter-spacing:.06em;margin-bottom:6px;">Título</div>
        <input type="text" id="tpl-title-input" class="inp" placeholder="Ex: Saudação inicial" style="width:100%;margin-bottom:14px;" maxlength="120">
        <div style="font-size:11px;color:#94a3b8;font-weight:600;text-transform:uppercase;letter-spacing:.06em;margin-bottom:6px;">Mensagem</div>
        <textarea id="tpl-body-input" class="inp" placeholder="Digite o texto da mensagem... Pode ter várias linhas." rows="6" style="width:100%;resize:vertical;min-height:120px;font-family:inherit;"></textarea>
        <div style="font-size:11px;color:#475569;margin-top:6px;">Tip: use \n com Shift+Enter para quebrar linha. Emojis 😀 funcionam.</div>
      </div>
      <div style="padding:14px 20px;border-top:1px solid rgba(255,255,255,.06);display:flex;justify-content:flex-end;gap:8px;">
        <button class="btn btn-ghost" onclick="fecharModalTemplate()" style="color:#94a3b8;">Cancelar</button>
        <button class="btn btn-primary" id="tpl-save-btn" onclick="salvarTemplate()">Salvar</button>
      </div>
    </div>
  </div>

  <!-- ══════════════════════ HISTÓRICO DE CONVERSAS ══════════════════════ -->
  <div id="page-historico" class="page">
    <div class="section-hdr">
      <div>
        <div class="section-title">Histórico de Conversas</div>
        <div class="section-sub">Veja todas as mensagens trocadas por sessão WhatsApp (apenas texto)</div>
      </div>
    </div>

    <!-- Seletor de sessão -->
    <div class="card" style="padding:14px 18px;margin-bottom:16px;display:flex;align-items:center;gap:12px;flex-wrap:wrap;">
      <div style="font-size:11px;color:#94a3b8;font-weight:600;text-transform:uppercase;letter-spacing:.06em;">Sessão:</div>
      <select id="hist-session-select" class="inp" style="flex:1;min-width:240px;max-width:520px;" onchange="onHistSessionChange()">
        <option value="">Carregando sessões...</option>
      </select>
      <button class="btn btn-ghost btn-sm" onclick="carregarHistoricoSessoes()" title="Atualizar sessões" style="font-size:11px;color:#94a3b8;">↻</button>
    </div>

    <!-- Layout 2 colunas: conversas | mensagens -->
    <div style="display:grid;grid-template-columns:340px 1fr;gap:14px;height:calc(100vh - 280px);min-height:480px;">
      <!-- Coluna esquerda: lista de conversas -->
      <div class="card" style="padding:0;overflow:hidden;display:flex;flex-direction:column;">
        <div style="padding:12px 14px;border-bottom:1px solid rgba(255,255,255,.06);display:flex;justify-content:space-between;align-items:center;">
          <div style="font-size:12px;color:#cbd5e1;font-weight:600;">Conversas</div>
          <span id="hist-conv-count" style="font-size:11px;color:#64748b;">—</span>
        </div>
        <input type="text" id="hist-search" placeholder="Filtrar por número..." class="inp" style="margin:8px 10px;font-size:12px;" oninput="filtrarHistConversas()">
        <div id="hist-conv-list" style="flex:1;overflow-y:auto;padding:4px;">
          <div style="padding:24px;text-align:center;color:#475569;font-size:12px;">Escolha uma sessão acima.</div>
        </div>
      </div>

      <!-- Coluna direita: mensagens -->
      <div class="card" style="padding:0;overflow:hidden;display:flex;flex-direction:column;">
        <div style="padding:12px 14px;border-bottom:1px solid rgba(255,255,255,.06);display:flex;justify-content:space-between;align-items:center;gap:10px;">
          <div style="min-width:0;flex:1;">
            <div id="hist-msg-title" style="font-size:13px;color:#e2e8f0;font-weight:600;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;">Selecione uma conversa</div>
            <div id="hist-msg-sub" style="font-size:11px;color:#64748b;margin-top:2px;"></div>
          </div>
        </div>
        <div id="hist-msg-body" style="flex:1;overflow-y:auto;padding:14px 18px;background:rgba(0,0,0,.18);">
          <div style="padding:60px 18px;text-align:center;color:#334155;font-size:13px;line-height:1.7;">
            <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" style="opacity:.4;margin-bottom:10px;"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/></svg>
            <div>Selecione uma conversa à esquerda<br>para ver as mensagens.</div>
          </div>
        </div>
      </div>
    </div>
  </div>

  <!-- ══════════════════════ FILAS BITRIX ══════════════════════ -->
  <div id="page-filas" class="page">
    <div class="section-hdr">
      <div>
        <div class="section-title">Filas Bitrix</div>
        <div class="section-sub">Vincule cada número WhatsApp a uma fila do Contact Center</div>
      </div>
      <button class="btn btn-primary" onclick="abrirModalFila()">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
        Novo Vínculo
      </button>
    </div>

    <!-- Info box -->
    <div class="card-flat" style="padding:14px 18px;margin-bottom:18px;display:flex;align-items:flex-start;gap:12px;">
      <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="#60a5fa" stroke-width="2" style="flex-shrink:0;margin-top:2px;"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="8"/><line x1="12" y1="12" x2="12" y2="16"/></svg>
      <div style="font-size:12.5px;color:#64748b;line-height:1.7;">
        <strong style="color:#94a3b8;">Como funciona:</strong> Cada vínculo define que mensagens recebidas em um número WhatsApp serão entregues
        a uma fila específica (Open Line ID) do portal Bitrix24. Você pode ter vários vínculos por portal.
        O ID da fila está em <strong style="color:#94a3b8;">Bitrix24 → Contact Center → Open Lines</strong>.
      </div>
    </div>

    <div id="lista-filas">
      <div style="text-align:center;padding:40px;color:#334155;font-size:13px;">Carregando...</div>
    </div>
  </div>


</div>

<!-- ══════════════════════ MODAL FILA ══════════════════════ -->
<div id="fila-modal" style="display:none;position:fixed;inset:0;background:rgba(0,0,0,.8);z-index:60;align-items:center;justify-content:center;backdrop-filter:blur(8px);padding:16px;" onclick="if(event.target===this)fecharModalFila()">
  <div class="modal-box" style="max-width:500px;width:100%;position:relative;">
    <button onclick="fecharModalFila()" style="position:absolute;top:16px;right:16px;background:rgba(255,255,255,.06);border:1.5px solid rgba(255,255,255,.1);border-radius:8px;color:#64748b;cursor:pointer;padding:6px;display:flex;align-items:center;justify-content:center;transition:all .15s;" onmouseover="this.style.background='rgba(255,255,255,.1)';this.style.color='#e2e8f0'" onmouseout="this.style.background='rgba(255,255,255,.06)';this.style.color='#64748b'">
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
    </button>
    <div style="font-size:18px;font-weight:800;color:#f1f5f9;margin-bottom:4px;letter-spacing:-.01em;">Novo Vínculo de Fila</div>
    <div style="font-size:13px;color:#475569;margin-bottom:24px;font-weight:400;">Vincule um número WhatsApp a uma fila do Contact Center Bitrix24</div>

    <div style="display:flex;flex-direction:column;gap:14px;">
      <div class="inp-group">
        <label class="inp-label">Portal Bitrix24</label>
        <select class="inp" id="fila-portal">
          <option value="">Selecione o portal...</option>
        </select>
      </div>
      <div class="inp-group">
        <label class="inp-label">Número WhatsApp</label>
        <select class="inp" id="fila-sessao">
          <option value="">Selecione o número conectado...</option>
        </select>
      </div>
      <div class="inp-group">
        <label class="inp-label">Open Line (Fila de Atendimento)</label>
        <div style="display:flex;gap:8px;align-items:center;">
          <select class="inp" id="fila-openline" style="flex:1;" disabled>
            <option value="">Selecione o portal primeiro...</option>
          </select>
          <button type="button" class="btn btn-ghost btn-sm" id="fila-buscar-linhas-btn" onclick="buscarLinhasBitrix()" style="white-space:nowrap;padding:0 14px;height:46px;flex-shrink:0;" disabled>
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
            Buscar
          </button>
        </div>
        <span style="font-size:11px;color:#475569;margin-top:4px;display:block;" id="fila-linhas-hint">Selecione o portal e clique em "Buscar" para listar as Open Lines disponíveis.</span>
      </div>
    </div>

    <div style="display:flex;gap:10px;justify-content:flex-end;margin-top:22px;">
      <button class="btn btn-ghost" onclick="fecharModalFila()">Cancelar</button>
      <button class="btn btn-primary" id="fila-modal-save-btn" onclick="salvarVinculoFila()">Criar Vínculo e Ativar</button>
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

<!-- ══════════════════════ MODAL NOVA SESSÃO (QR ou Cloud API) ══════════════════════ -->
<div id="qr-modal" onclick="if(event.target===this)fecharModalQR()">
  <div class="card" style="padding:28px;max-width:440px;width:100%;position:relative;max-height:92vh;overflow-y:auto;">
    <button onclick="fecharModalQR()" style="position:absolute;top:14px;right:14px;background:none;border:none;color:#475569;cursor:pointer;padding:4px;border-radius:6px;" onmouseover="this.style.color='#e2e8f0'" onmouseout="this.style.color='#475569'">
      <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
    </button>
    <div style="font-size:17px;font-weight:700;color:#f1f5f9;margin-bottom:4px;">Nova Sessão WhatsApp</div>
    <div style="font-size:13px;color:#475569;margin-bottom:16px;">Escolha entre Multi-Device ou WhatsApp Business API</div>

    <!-- Tabs -->
    <div style="display:flex;gap:6px;background:rgba(255,255,255,.04);border:1px solid rgba(255,255,255,.06);border-radius:10px;padding:4px;margin-bottom:18px;">
      <button id="ns-tab-qr" onclick="trocarTipoSessao('qr')" style="flex:1;padding:9px;border:none;background:rgba(37,211,102,.18);color:#25D366;border-radius:8px;font-size:12.5px;font-weight:700;cursor:pointer;">📱 Multi-Device</button>
      <button id="ns-tab-cloud" onclick="trocarTipoSessao('cloud')" style="flex:1;padding:9px;border:none;background:transparent;color:#64748b;border-radius:8px;font-size:12.5px;font-weight:700;cursor:pointer;">☁️ API Oficial</button>
    </div>

    <!-- ─── Form Multi-Device ─── -->
    <div id="ns-mode-qr">
      <label style="display:block;font-size:11px;color:#64748b;margin-bottom:6px;font-weight:600;letter-spacing:.04em;text-transform:uppercase;">Número do WhatsApp</label>
      <div style="display:flex;gap:8px;margin-bottom:18px;">
        <input class="inp" id="modal-numero" placeholder="5519910001772" maxlength="20" onkeydown="if(event.key==='Enter')iniciarSessao()" style="flex:1;min-width:0;"/>
        <button class="btn btn-primary" id="modal-btn-conectar" onclick="iniciarSessao()" style="white-space:nowrap;flex-shrink:0;">Conectar</button>
      </div>

      <div id="modal-qr-area" style="display:none;">
        <div style="display:flex;flex-direction:column;align-items:center;gap:12px;">
          <div id="modal-badge-qr"></div>
          <div style="background:#ffffff;border-radius:12px;padding:12px;box-shadow:0 4px 20px rgba(0,0,0,.4);">
            <div style="position:relative;width:200px;height:200px;">
              <img id="modal-qr-img" src="" width="200" height="200" style="position:absolute;top:0;left:0;display:none;"/>
              <div id="modal-qr-placeholder" style="position:absolute;top:0;left:0;width:200px;height:200px;display:flex;align-items:center;justify-content:center;color:#94a3b8;font-size:13px;text-align:center;background:#f1f5f9;border-radius:4px;">Aguardando QR...</div>
            </div>
          </div>
          <div style="font-size:12px;color:#94a3b8;height:18px;font-weight:500;" id="modal-timer"></div>
        </div>
      </div>

      <div style="background:rgba(255,255,255,.03);border:1px solid rgba(255,255,255,.06);border-radius:10px;padding:14px 16px;margin-top:14px;font-size:12.5px;color:#94a3b8;line-height:1.8;">
        <strong style="color:#cbd5e1;display:block;margin-bottom:6px;font-size:12px;letter-spacing:.02em;">Como escanear:</strong>
        <div>1. Abra o WhatsApp no celular</div>
        <div>2. Toque em <strong style="color:#cbd5e1;">&#8942; &rarr; Aparelhos conectados</strong></div>
        <div>3. Toque em <strong style="color:#cbd5e1;">Conectar um aparelho</strong></div>
        <div>4. Aponte a câmera para o QR acima</div>
      </div>
    </div>

    <!-- ─── Form Cloud API (Oficial) ─── -->
    <div id="ns-mode-cloud" style="display:none;">
      <div style="background:rgba(59,130,246,.08);border:1px solid rgba(59,130,246,.25);border-radius:10px;padding:12px;margin-bottom:14px;font-size:12px;color:#94a3b8;line-height:1.7;">
        <strong style="color:#60a5fa;">WhatsApp Business API (Meta Oficial)</strong><br>
        Configuração requer um app Meta com webhook HTTPS configurado.
        <button class="btn btn-ghost btn-sm" onclick="abrirGuiaCloud()" style="margin-top:8px;color:#60a5fa;border:1px solid rgba(59,130,246,.4);width:100%;">
          📖 Ver guia passo a passo (Meta for Developers)
        </button>
      </div>
      <label style="display:block;font-size:11px;color:#64748b;margin-bottom:4px;font-weight:600;">Telefone (E.164, sem +)</label>
      <input class="inp" id="cl-display" placeholder="5519910001772" style="width:100%;margin-bottom:10px;"/>
      <label style="display:block;font-size:11px;color:#64748b;margin-bottom:4px;font-weight:600;">Phone Number ID *</label>
      <input class="inp" id="cl-pnid" placeholder="123456789012345" style="width:100%;margin-bottom:10px;"/>
      <label style="display:block;font-size:11px;color:#64748b;margin-bottom:4px;font-weight:600;">WABA ID (opcional)</label>
      <input class="inp" id="cl-waba" placeholder="(opcional)" style="width:100%;margin-bottom:10px;"/>
      <label style="display:block;font-size:11px;color:#64748b;margin-bottom:4px;font-weight:600;">Access Token *</label>
      <input class="inp" id="cl-token" placeholder="EAAxxxxxxxxxxxx..." style="width:100%;margin-bottom:10px;"/>
      <label style="display:block;font-size:11px;color:#64748b;margin-bottom:4px;font-weight:600;">App Secret</label>
      <input class="inp" id="cl-secret" placeholder="(necessário para validar webhooks)" style="width:100%;margin-bottom:14px;"/>
      <button class="btn btn-primary" id="cl-btn-conectar" onclick="iniciarSessaoCloud()" style="width:100%;">Conectar via API Oficial</button>

      <!-- Painel pós-cadastro -->
      <div id="cl-result" style="display:none;margin-top:16px;background:rgba(34,197,94,.08);border:1px solid rgba(34,197,94,.3);border-radius:10px;padding:14px;font-size:12px;color:#94a3b8;line-height:1.7;">
        <strong style="color:#4ade80;display:block;margin-bottom:6px;">✓ Conta cadastrada!</strong>
        Cole estes valores no painel da Meta:
        <div style="margin-top:10px;font-weight:600;color:#cbd5e1;">Callback URL (HTTPS):</div>
        <div id="cl-out-url" style="background:rgba(0,0,0,.25);padding:7px 10px;border-radius:6px;border:1px solid rgba(255,255,255,.06);word-break:break-all;font-family:monospace;font-size:11px;margin-top:3px;cursor:pointer;color:#e2e8f0;" onclick="copiarTexto(this)" title="Clique para copiar"></div>
        <div style="margin-top:8px;font-weight:600;color:#cbd5e1;">Verify Token:</div>
        <div id="cl-out-token" style="background:rgba(0,0,0,.25);padding:7px 10px;border-radius:6px;border:1px solid rgba(255,255,255,.06);word-break:break-all;font-family:monospace;font-size:11px;margin-top:3px;cursor:pointer;color:#e2e8f0;" onclick="copiarTexto(this)" title="Clique para copiar"></div>
        <div style="display:flex;gap:8px;margin-top:12px;">
          <button class="btn btn-ghost btn-sm" onclick="abrirGuiaCloud()" style="flex:1;color:#60a5fa;border:1px solid rgba(59,130,246,.4);">📖 Ver guia</button>
          <button class="btn btn-primary btn-sm" onclick="fecharModalQR()" style="flex:1;">✓ Concluir</button>
        </div>
      </div>
    </div>
  </div>
</div>

<!-- ══════════════════════ MODAL GUIA CLOUD API ══════════════════════ -->
<div id="guia-cloud-modal" onclick="if(event.target===this)fecharGuiaCloud()" style="display:none;position:fixed;inset:0;background:rgba(0,0,0,.7);z-index:60;align-items:center;justify-content:center;padding:20px;">
  <div class="card" style="padding:0;max-width:680px;width:100%;position:relative;max-height:90vh;display:flex;flex-direction:column;overflow:hidden;">
    <!-- Header -->
    <div style="padding:22px 28px 18px;border-bottom:1px solid rgba(255,255,255,.06);position:relative;flex-shrink:0;">
      <button onclick="fecharGuiaCloud()" style="position:absolute;top:16px;right:18px;background:none;border:none;color:#475569;cursor:pointer;padding:4px;border-radius:6px;" onmouseover="this.style.color='#e2e8f0'" onmouseout="this.style.color='#475569'">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
      </button>
      <div style="display:flex;align-items:center;gap:10px;margin-bottom:4px;">
        <div style="width:34px;height:34px;border-radius:8px;background:rgba(59,130,246,.18);display:flex;align-items:center;justify-content:center;">
          <svg width="18" height="18" fill="#60a5fa" viewBox="0 0 24 24"><path d="M12 2C6.48 2 2 6.48 2 12c0 1.82.49 3.53 1.34 5L2 22l5.15-1.32A10 10 0 1012 2z"/></svg>
        </div>
        <div style="font-size:18px;font-weight:700;color:#f1f5f9;">Configurar WhatsApp Business API</div>
      </div>
      <div style="font-size:13px;color:#475569;">Guia oficial Meta — siga os passos para conectar seu número via API Oficial</div>
    </div>

    <!-- Body com scroll -->
    <div style="padding:22px 28px;overflow-y:auto;font-size:13.5px;color:#cbd5e1;line-height:1.7;">

      <!-- Pré-requisitos -->
      <div style="background:rgba(245,158,11,.08);border:1px solid rgba(245,158,11,.3);border-radius:10px;padding:13px 16px;margin-bottom:18px;">
        <div style="font-weight:700;color:#fbbf24;margin-bottom:6px;display:flex;align-items:center;gap:6px;">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="#fbbf24" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16" stroke-linecap="round"/></svg>
          Pré-requisitos
        </div>
        <ul style="margin:0;padding-left:20px;font-size:12.5px;color:#94a3b8;">
          <li>Conta no <a href="https://business.facebook.com" target="_blank" style="color:#60a5fa;">Meta Business Manager</a></li>
          <li>Telefone <strong style="color:#cbd5e1;">não usado</strong> em WhatsApp comum (será migrado para Business)</li>
          <li>Servidor com <strong style="color:#cbd5e1;">HTTPS válido</strong> (CA-assinado, não autoassinado) — o seu já tem ✓</li>
        </ul>
      </div>

      <!-- Passo 1 -->
      <div style="display:flex;gap:14px;margin-bottom:20px;">
        <div style="flex-shrink:0;width:32px;height:32px;border-radius:50%;background:rgba(59,130,246,.18);color:#60a5fa;font-weight:700;font-size:14px;display:flex;align-items:center;justify-content:center;">1</div>
        <div style="flex:1;">
          <div style="font-weight:700;color:#f1f5f9;margin-bottom:4px;">Criar app no Meta for Developers</div>
          <div style="color:#94a3b8;font-size:12.5px;">
            Acesse <a href="https://developers.facebook.com" target="_blank" style="color:#60a5fa;">developers.facebook.com</a> →
            <strong style="color:#cbd5e1;">Meus Apps → Criar app</strong> → Tipo <strong style="color:#cbd5e1;">"Business"</strong>.<br>
            Dê um nome (ex: "UC Talk WhatsApp") e vincule à sua Business Manager.
          </div>
        </div>
      </div>

      <!-- Passo 2 -->
      <div style="display:flex;gap:14px;margin-bottom:20px;">
        <div style="flex-shrink:0;width:32px;height:32px;border-radius:50%;background:rgba(59,130,246,.18);color:#60a5fa;font-weight:700;font-size:14px;display:flex;align-items:center;justify-content:center;">2</div>
        <div style="flex:1;">
          <div style="font-weight:700;color:#f1f5f9;margin-bottom:4px;">Adicionar produto WhatsApp</div>
          <div style="color:#94a3b8;font-size:12.5px;">
            No painel do app: <strong style="color:#cbd5e1;">Adicionar produtos → WhatsApp → Configurar</strong>.<br>
            Aceite os termos. Será gerada uma <strong style="color:#cbd5e1;">conta WhatsApp Business (WABA)</strong> de teste.
          </div>
        </div>
      </div>

      <!-- Passo 3 -->
      <div style="display:flex;gap:14px;margin-bottom:20px;">
        <div style="flex-shrink:0;width:32px;height:32px;border-radius:50%;background:rgba(59,130,246,.18);color:#60a5fa;font-weight:700;font-size:14px;display:flex;align-items:center;justify-content:center;">3</div>
        <div style="flex:1;">
          <div style="font-weight:700;color:#f1f5f9;margin-bottom:4px;">Pegar credenciais</div>
          <div style="color:#94a3b8;font-size:12.5px;margin-bottom:8px;">
            Em <strong style="color:#cbd5e1;">WhatsApp → API Setup</strong> copie:
          </div>
          <ul style="margin:0;padding-left:18px;font-size:12px;color:#94a3b8;">
            <li><strong style="color:#cbd5e1;">Phone Number ID</strong> (ex: 123456789012345)</li>
            <li><strong style="color:#cbd5e1;">WhatsApp Business Account ID (WABA)</strong></li>
            <li><strong style="color:#cbd5e1;">Access Token</strong> — gere um <strong>System User Token permanente</strong> (não use o temporário de 24h)</li>
            <li><strong style="color:#cbd5e1;">App Secret</strong> em <strong>Configurações → Básico</strong> (clique em "Mostrar" para revelar)</li>
          </ul>
        </div>
      </div>

      <!-- Passo 4 -->
      <div style="display:flex;gap:14px;margin-bottom:20px;">
        <div style="flex-shrink:0;width:32px;height:32px;border-radius:50%;background:rgba(34,197,94,.2);color:#4ade80;font-weight:700;font-size:14px;display:flex;align-items:center;justify-content:center;">4</div>
        <div style="flex:1;">
          <div style="font-weight:700;color:#f1f5f9;margin-bottom:4px;">Cadastrar no UC Talk</div>
          <div style="color:#94a3b8;font-size:12.5px;">
            Volte ao painel <strong style="color:#cbd5e1;">UC Talk → Sessões WhatsApp → API Oficial</strong> e cole:
            telefone, Phone Number ID, WABA ID, Access Token, App Secret.<br>
            Clique <strong style="color:#cbd5e1;">"Conectar via API Oficial"</strong>. O sistema valida o token chamando a API da Meta antes de salvar.
          </div>
        </div>
      </div>

      <!-- Passo 5 -->
      <div style="display:flex;gap:14px;margin-bottom:20px;">
        <div style="flex-shrink:0;width:32px;height:32px;border-radius:50%;background:rgba(34,197,94,.2);color:#4ade80;font-weight:700;font-size:14px;display:flex;align-items:center;justify-content:center;">5</div>
        <div style="flex:1;">
          <div style="font-weight:700;color:#f1f5f9;margin-bottom:4px;">Configurar webhook no Meta</div>
          <div style="color:#94a3b8;font-size:12.5px;margin-bottom:8px;">
            Após cadastrar, copie a <strong style="color:#cbd5e1;">Callback URL</strong> e o <strong style="color:#cbd5e1;">Verify Token</strong> que aparecem no painel verde de sucesso. Volte ao Meta:
          </div>
          <ol style="margin:0;padding-left:18px;font-size:12px;color:#94a3b8;">
            <li><strong style="color:#cbd5e1;">WhatsApp → Configuration → Webhook → Edit</strong></li>
            <li>Cole a <strong style="color:#cbd5e1;">Callback URL</strong> e o <strong style="color:#cbd5e1;">Verify Token</strong></li>
            <li>Clique <strong style="color:#cbd5e1;">Verify and save</strong> (Meta faz GET na URL para validar)</li>
            <li>Em <strong style="color:#cbd5e1;">Webhook fields</strong>, clique <strong>Manage</strong> e assine: <strong style="color:#4ade80;">messages</strong> e <strong style="color:#4ade80;">message_status</strong></li>
          </ol>
        </div>
      </div>

      <!-- Passo 6 -->
      <div style="display:flex;gap:14px;margin-bottom:14px;">
        <div style="flex-shrink:0;width:32px;height:32px;border-radius:50%;background:rgba(34,197,94,.2);color:#4ade80;font-weight:700;font-size:14px;display:flex;align-items:center;justify-content:center;">6</div>
        <div style="flex:1;">
          <div style="font-weight:700;color:#f1f5f9;margin-bottom:4px;">Vincular à fila Bitrix</div>
          <div style="color:#94a3b8;font-size:12.5px;">
            Vá em <strong style="color:#cbd5e1;">Filas Bitrix</strong> e crie um vínculo entre essa sessão (badge OFICIAL azul) e uma Open Line do seu portal Bitrix24.<br>
            Sem isso, mensagens recebidas não chegam no atendimento.
          </div>
        </div>
      </div>

      <!-- Caixa SSL -->
      <div style="background:rgba(34,197,94,.08);border:1px solid rgba(34,197,94,.3);border-radius:10px;padding:13px 16px;margin-top:14px;">
        <div style="font-weight:700;color:#4ade80;margin-bottom:4px;display:flex;align-items:center;gap:6px;">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="#4ade80" stroke-width="2"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/></svg>
          SSL/TLS — requisito Meta
        </div>
        <div style="font-size:12px;color:#94a3b8;">
          A Meta exige que a Callback URL use <strong style="color:#cbd5e1;">HTTPS com certificado válido emitido por uma CA</strong> (Let's Encrypt, DigiCert, etc).
          Certificados <strong>autoassinados não são aceitos</strong>. Sua URL <code style="background:rgba(0,0,0,.3);padding:1px 5px;border-radius:4px;">uctalk.uctechnology.com.br</code> já atende esse requisito ✓
        </div>
      </div>

    </div>

    <!-- Footer -->
    <div style="padding:14px 28px;border-top:1px solid rgba(255,255,255,.06);display:flex;gap:10px;justify-content:space-between;align-items:center;flex-shrink:0;">
      <a href="https://developers.facebook.com/docs/whatsapp/cloud-api/get-started" target="_blank" style="font-size:12px;color:#60a5fa;text-decoration:none;">📖 Documentação oficial Meta →</a>
      <button class="btn btn-primary" onclick="fecharGuiaCloud()">Entendi</button>
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
var qrLastCode = ''; // ultimo conteudo de QR mostrado — evita resetar timer a cada poll

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

// Aplica modo portal: apenas mostra badge do portal no sidebar.
// Botões de "Nova Sessão" continuam visíveis para que o admin possa
// gerenciar sessões mesmo acessando pelo iframe do Bitrix.
(function() {
  if (!PORTAL) return;
  var portalBadge = document.getElementById('sidebar-portal-badge');
  if (portalBadge) {
    portalBadge.style.display = 'block';
    portalBadge.textContent = PORTAL;
  }
})();

// ─── Navegação ────────────────────────────────────────────────────────────────
var titulosPaginas = { painel: 'Painel', sessoes: 'Sessões', filas: 'Filas Bitrix', permissoes: 'Permissões CRM', templates: 'Templates de Mensagem', historico: 'Histórico de Conversas', relatorios: 'Relatórios' };

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
  if (nome === 'filas') carregarFilas();
  if (nome === 'permissoes') carregarPermissoes();
  if (nome === 'templates') carregarTemplatesDashboard();
  if (nome === 'historico') carregarHistoricoSessoes();
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
    // JID Cloud vem como "cloud:<phone_id>@s.whatsapp.net" — sem telefone embutido.
    // QR vem como "<phone>:NN@s.whatsapp.net" — strip device suffix e dominio.
    var isCloud = jid.indexOf('cloud:') === 0;
    var telefone = isCloud
      ? 'Cloud API'
      : '+' + jid.split('@')[0].split(':')[0];
    var badge = isCloud
      ? '<span class="badge" style="background:rgba(96,165,250,.18);color:#60a5fa;">Cloud</span>'
      : '<span class="badge badge-green">Online</span>';
    html += '<div class="card-flat" style="display:flex;align-items:center;justify-content:space-between;padding:12px 14px;gap:12px;">'
      + '<div style="display:flex;align-items:center;gap:10px;min-width:0;">'
      + '<div class="dot dot-green"></div>'
      + '<div style="min-width:0;">'
      + '<div style="font-size:13.5px;font-weight:600;color:#e2e8f0;">' + telefone + '</div>'
      + '<div style="font-size:11px;color:#334155;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;">' + jid + '</div>'
      + '</div></div>'
      + badge
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
        + '<div style="display:flex;gap:8px;justify-content:center;flex-wrap:wrap;">'
        + '<button class="btn btn-primary" onclick="abrirModalNovaSessao(\'qr\')">📱 Conectar via Multi-Device</button>'
        + '<button class="btn btn-ghost" onclick="abrirModalNovaSessao(\'cloud\')" style="border:1px solid rgba(59,130,246,.4);color:#60a5fa;">☁️ API Oficial (Meta)</button>'
        + '</div>'
        + '</div>';
      return;
    }
    var html = '<div style="display:flex;flex-direction:column;gap:10px;">';
    // Usa details (com type) se backend já retornou; senão monta do array de jids.
    var lista = (d.details && d.details.length) ? d.details : d.sessions.map(function(j){return {jid:j,type:'qr'};});
    lista.forEach(function(s) {
      var jid = s.jid || s;
      var tipo = s.type || 'qr';
      var enc = encodeURIComponent(jid);
      var telefone, jidLabel, iconBg, iconColor, badgeTipo;
      if (tipo === 'cloud_api') {
        telefone = s.phone ? '+' + s.phone : (s.label || jid);
        jidLabel = jid;
        iconBg = 'rgba(59,130,246,.14)';
        iconColor = '#60a5fa';
        badgeTipo = '<span style="font-size:10px;background:rgba(59,130,246,.18);color:#60a5fa;padding:3px 9px;border-radius:11px;font-weight:700;letter-spacing:.04em;">OFICIAL</span>';
      } else {
        telefone = '+' + jid.split(':')[0].split('@')[0];
        jidLabel = jid;
        iconBg = 'rgba(37,211,102,.12)';
        iconColor = '#25D366';
        badgeTipo = '<span style="font-size:10px;background:rgba(37,211,102,.15);color:#25D366;padding:3px 9px;border-radius:11px;font-weight:700;letter-spacing:.04em;">MULTI-DEVICE</span>';
      }
      var btnWebhook = '';
      if (tipo === 'cloud_api') {
        var sidEnc = encodeURIComponent(s.session_id || jid);
        btnWebhook = '<button class="btn btn-ghost btn-sm" onclick="verWebhookCloud(\'' + sidEnc + '\')" title="Ver URL do webhook" style="border:1px solid rgba(59,130,246,.3);color:#60a5fa;">📋 Webhook</button>';
      }
      html += '<div class="card" style="padding:18px;display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap;">'
        + '<div style="display:flex;align-items:center;gap:13px;">'
        + '<div class="metric-icon" style="background:' + iconBg + ';">'
        + '<svg width="17" height="17" fill="' + iconColor + '" viewBox="0 0 24 24"><path d="M17.472 14.382c-.297-.149-1.758-.867-2.03-.967-.273-.099-.471-.148-.67.15-.197.297-.767.966-.94 1.164-.173.199-.347.223-.644.075-.297-.15-1.255-.463-2.39-1.475-.883-.788-1.48-1.761-1.653-2.059-.173-.297-.018-.458.13-.606.134-.133.298-.347.446-.52.149-.174.198-.298.298-.497.099-.198.05-.371-.025-.52-.075-.149-.669-1.612-.916-2.207-.242-.579-.487-.5-.669-.51-.173-.008-.371-.01-.57-.01-.198 0-.52.074-.792.372-.272.297-1.04 1.016-1.04 2.479 0 1.462 1.065 2.875 1.213 3.074.149.198 2.096 3.2 5.077 4.487.709.306 1.262.489 1.694.625.712.227 1.36.195 1.871.118.571-.085 1.758-.719 2.006-1.413.248-.694.248-1.289.173-1.413-.074-.124-.272-.198-.57-.347m-5.421 7.403h-.004a9.87 9.87 0 01-5.031-1.378l-.361-.214-3.741.982.998-3.648-.235-.374a9.86 9.86 0 01-1.51-5.26c.001-5.45 4.436-9.884 9.888-9.884 2.64 0 5.122 1.03 6.988 2.898a9.825 9.825 0 012.893 6.994c-.003 5.45-4.437 9.884-9.885 9.884m8.413-18.297A11.815 11.815 0 0012.05 0C5.495 0 .16 5.335.157 11.892c0 2.096.547 4.142 1.588 5.945L.057 24l6.305-1.654a11.882 11.882 0 005.683 1.448h.005c6.554 0 11.89-5.335 11.893-11.893a11.821 11.821 0 00-3.48-8.413z"/></svg>'
        + '</div>'
        + '<div>'
        + '<div style="font-size:15px;font-weight:600;color:#e2e8f0;display:flex;align-items:center;gap:8px;">' + telefone + ' ' + badgeTipo + '</div>'
        + '<div style="font-size:11.5px;color:#334155;margin-top:2px;">' + jidLabel + '</div>'
        + '</div></div>'
        + '<div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap;">'
        + '<span class="badge badge-green">Conectado</span>'
        + btnWebhook
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
  trocarTipoSessao('qr');
}

// Abre o modal de nova sessão e seleciona o tab desejado (qr | cloud).
function abrirModalNovaSessao(modo) {
  document.getElementById('qr-modal').classList.add('open');
  trocarTipoSessao(modo === 'cloud' ? 'cloud' : 'qr');
}

// Atualiza o status de saúde de TODAS as sessões (QR + Cloud).
// Faz um Ping em cada uma e mostra resultado consolidado.
function atualizarStatusSessoes() {
  var btn = document.getElementById('btn-refresh-status');
  var icon = document.getElementById('btn-refresh-icon');
  if (btn) btn.disabled = true;
  if (icon) icon.style.animation = 'spin 0.8s linear infinite';

  fetch('/ui/sessions/refresh-status', {method:'POST'})
  .then(function(r){ return r.json(); })
  .then(function(d) {
    if (btn) btn.disabled = false;
    if (icon) icon.style.animation = '';
    if (!d.ok) { toast('Erro ao verificar status', 'error'); return; }

    var problemas = [];
    (d.sessions||[]).forEach(function(s) {
      if (!s.healthy) problemas.push((s.label || s.jid) + ': ' + (s.error || s.status));
    });

    if (d.healthy === d.total && d.total > 0) {
      toast('Todas as ' + d.total + ' sessões estão OK', 'success');
    } else if (d.total === 0) {
      toast('Nenhuma sessão cadastrada', 'error');
    } else {
      toast(d.healthy + '/' + d.total + ' sessões OK. ' + problemas.length + ' com problema.', 'error');
      console.warn('Sessões com problema:', problemas);
    }
    // Recarrega a lista visual com os novos status
    carregarSessoes();
  })
  .catch(function(e){
    if (btn) btn.disabled = false;
    if (icon) icon.style.animation = '';
    toast('Falha: ' + e, 'error');
  });
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
  // Reset Cloud
  ['cl-display','cl-pnid','cl-waba','cl-token','cl-secret'].forEach(function(id){
    var el = document.getElementById(id); if (el) el.value = '';
  });
  var cr = document.getElementById('cl-result'); if (cr) cr.style.display = 'none';
  var clBtn = document.getElementById('cl-btn-conectar');
  if (clBtn) { clBtn.disabled = false; clBtn.textContent = 'Conectar via API Oficial'; }
}

// Alterna entre tabs QR e Cloud no modal de nova sessão.
function trocarTipoSessao(modo) {
  var qr = document.getElementById('ns-mode-qr');
  var cl = document.getElementById('ns-mode-cloud');
  var tQ = document.getElementById('ns-tab-qr');
  var tC = document.getElementById('ns-tab-cloud');
  if (!qr || !cl || !tQ || !tC) return;
  if (modo === 'cloud') {
    qr.style.display = 'none';
    cl.style.display = 'block';
    tQ.style.background = 'transparent'; tQ.style.color = '#64748b';
    tC.style.background = 'rgba(59,130,246,.18)'; tC.style.color = '#60a5fa';
  } else {
    qr.style.display = 'block';
    cl.style.display = 'none';
    tC.style.background = 'transparent'; tC.style.color = '#64748b';
    tQ.style.background = 'rgba(37,211,102,.18)'; tQ.style.color = '#25D366';
  }
}

// Cadastra uma sessão Cloud API (Meta) e mostra a URL do webhook + verify token.
function iniciarSessaoCloud() {
  var pnid = document.getElementById('cl-pnid').value.trim();
  var waba = document.getElementById('cl-waba').value.trim();
  var token = document.getElementById('cl-token').value.trim();
  var secret = document.getElementById('cl-secret').value.trim();
  var disp = document.getElementById('cl-display').value.replace(/\D/g,'');
  if (!pnid || !token) { toast('Phone Number ID e Access Token são obrigatórios', 'error'); return; }
  var btn = document.getElementById('cl-btn-conectar');
  btn.disabled = true; btn.textContent = 'Validando credenciais...';
  fetch('/ui/sessions/cloud', {
    method:'POST',
    headers:{'Content-Type':'application/json'},
    body: JSON.stringify({
      phone_number_id: pnid,
      waba_id: waba,
      access_token: token,
      app_secret: secret,
      display_phone: disp
    })
  })
  .then(function(r){ return r.json(); })
  .then(function(d){
    btn.disabled = false; btn.textContent = 'Conectar via API Oficial';
    if (d.error) { toast('Erro: ' + d.error, 'error'); return; }
    document.getElementById('cl-out-url').textContent = d.webhook_url;
    document.getElementById('cl-out-token').textContent = d.verify_token;
    document.getElementById('cl-result').style.display = 'block';
    toast('Conta Cloud API cadastrada com sucesso', 'success');
    carregarSessoes();
  })
  .catch(function(e){
    btn.disabled = false; btn.textContent = 'Conectar via API Oficial';
    toast('Falha: ' + e, 'error');
  });
}

// Abre/fecha o modal com o guia passo-a-passo de configuração Cloud API.
function abrirGuiaCloud() {
  var m = document.getElementById('guia-cloud-modal');
  if (m) m.style.display = 'flex';
}
function fecharGuiaCloud() {
  var m = document.getElementById('guia-cloud-modal');
  if (m) m.style.display = 'none';
}

// Mostra um popup com a URL do webhook e o verify_token de uma sessão Cloud,
// para permitir reconfigurar no Meta sem precisar cadastrar de novo.
function verWebhookCloud(sidEnc) {
  fetch('/ui/sessions/cloud/' + sidEnc + '/webhook-info')
  .then(function(r){ return r.json(); })
  .then(function(d){
    if (d.error) { toast(d.error, 'error'); return; }
    var existing = document.getElementById('webhook-info-modal');
    if (existing) existing.remove();
    var html = '<div id="webhook-info-modal" onclick="if(event.target===this)this.remove()" style="display:flex;position:fixed;inset:0;background:rgba(0,0,0,.65);z-index:62;align-items:center;justify-content:center;padding:20px;">'
      + '<div class="card" style="padding:24px;max-width:560px;width:100%;position:relative;">'
      + '<button onclick="document.getElementById(\'webhook-info-modal\').remove()" style="position:absolute;top:14px;right:16px;background:none;border:none;color:#475569;cursor:pointer;padding:4px;border-radius:6px;">'
      + '<svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg></button>'
      + '<div style="font-size:16px;font-weight:700;color:#f1f5f9;margin-bottom:4px;">📋 Webhook desta sessão</div>'
      + '<div style="font-size:12.5px;color:#475569;margin-bottom:18px;">Cole no Meta for Developers → WhatsApp → Configuration → Webhook → Edit</div>'
      + '<div style="font-size:12px;font-weight:600;color:#cbd5e1;margin-bottom:4px;">Telefone: <span style="color:#60a5fa;">+' + (d.display_phone || '') + '</span></div>'
      + '<div style="font-size:11.5px;color:#475569;margin-bottom:14px;">Phone Number ID: ' + (d.phone_number_id || '?') + '</div>'
      + '<div style="font-weight:600;color:#cbd5e1;font-size:12px;margin-bottom:4px;">Callback URL (HTTPS):</div>'
      + '<div style="background:rgba(0,0,0,.3);padding:9px 12px;border-radius:7px;border:1px solid rgba(255,255,255,.06);word-break:break-all;font-family:monospace;font-size:11.5px;cursor:pointer;color:#e2e8f0;margin-bottom:12px;" onclick="copiarTexto(this)" title="Clique para copiar">' + d.webhook_url + '</div>'
      + '<div style="font-weight:600;color:#cbd5e1;font-size:12px;margin-bottom:4px;">Verify Token:</div>'
      + '<div style="background:rgba(0,0,0,.3);padding:9px 12px;border-radius:7px;border:1px solid rgba(255,255,255,.06);word-break:break-all;font-family:monospace;font-size:11.5px;cursor:pointer;color:#e2e8f0;margin-bottom:14px;" onclick="copiarTexto(this)" title="Clique para copiar">' + d.verify_token + '</div>'
      + '<div style="display:flex;gap:8px;justify-content:flex-end;">'
      + '<button class="btn btn-ghost btn-sm" onclick="document.getElementById(\'webhook-info-modal\').remove();abrirGuiaCloud();">📖 Ver guia completo</button>'
      + '<button class="btn btn-primary btn-sm" onclick="document.getElementById(\'webhook-info-modal\').remove()">Fechar</button>'
      + '</div></div></div>';
    document.body.insertAdjacentHTML('beforeend', html);
  })
  .catch(function(){ toast('Erro ao carregar dados do webhook', 'error'); });
}

// Copia o texto de um elemento para clipboard com feedback visual.
function copiarTexto(el) {
  var t = el.textContent;
  var done = function() {
    var orig = el.style.background;
    el.style.background = 'rgba(34,197,94,.25)';
    setTimeout(function(){ el.style.background = orig; }, 700);
    toast('Copiado para área de transferência', 'success');
  };
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(t).then(done).catch(function(){
      var ta = document.createElement('textarea');
      ta.value = t; document.body.appendChild(ta); ta.select();
      try { document.execCommand('copy'); done(); } catch(e){}
      document.body.removeChild(ta);
    });
  } else {
    var ta = document.createElement('textarea');
    ta.value = t; document.body.appendChild(ta); ta.select();
    try { document.execCommand('copy'); done(); } catch(e){}
    document.body.removeChild(ta);
  }
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
      // Atualiza img + reinicia timer SOMENTE se o conteudo do QR mudou.
      // Sem isso, cada poll de 2s reseta o contador e a img re-baixa.
      if (d.qr !== qrLastCode) {
        qrLastCode = d.qr;
        img.src = 'https://api.qrserver.com/v1/create-qr-code/?size=200x200&ecc=L&data=' + encodeURIComponent(d.qr);
        img.style.display = 'block';
        document.getElementById('modal-qr-placeholder').style.display = 'none';
        setBadgeModal('blue', 'Escaneie o QR code');
        qrCountdown = 30;
        if (qrTimer) clearInterval(qrTimer);
        qrTimer = setInterval(function() {
          qrCountdown--;
          if (qrCountdown <= 0) {
            clearInterval(qrTimer);
            setText('modal-timer', 'Atualizando...');
            return;
          }
          setText('modal-timer', 'QR expira em ' + qrCountdown + 's');
        }, 1000);
        setText('modal-timer', 'QR expira em ' + qrCountdown + 's');
      }
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
  qrLastCode = '';
}

// ─── Relatórios ───────────────────────────────────────────────────────────────
function setPeriodo(dias, btn) {
  periodoRelatorio = dias;
  document.querySelectorAll('.tab').forEach(function(b) { b.classList.remove('active'); });
  btn.classList.add('active');
  carregarRelatorios(dias);
}

var chartHoras = null, chartTipos = null;
var exportMenuOpen = false;

// Relatório atualmente visível — mapeado pelo tab ativo
var reportAtual = 'daily';

function toggleExportMenu() {
  exportMenuOpen = !exportMenuOpen;
  document.getElementById('export-dropdown').style.display = exportMenuOpen ? 'block' : 'none';
}

// Fecha o menu ao clicar fora
document.addEventListener('click', function(e) {
  var wrap = document.getElementById('export-menu-wrap');
  if (wrap && !wrap.contains(e.target)) {
    exportMenuOpen = false;
    var dd = document.getElementById('export-dropdown');
    if (dd) dd.style.display = 'none';
  }
});

function exportar(format) {
  exportMenuOpen = false;
  document.getElementById('export-dropdown').style.display = 'none';
  var url = '/ui/stats/export?days=' + periodoRelatorio + '&report=' + reportAtual + '&format=' + format;
  window.location.href = url;
}

function exportarTodos(format) {
  exportMenuOpen = false;
  document.getElementById('export-dropdown').style.display = 'none';
  var reports = ['daily', 'sessions', 'types', 'hours', 'contacts'];
  // Baixa cada relatório com delay de 500ms entre eles para não bloquear o browser
  reports.forEach(function(r, i) {
    setTimeout(function() {
      var url = '/ui/stats/export?days=' + periodoRelatorio + '&report=' + r + '&format=' + format;
      var a = document.createElement('a');
      a.href = url;
      a.download = '';
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
    }, i * 600);
  });
}

function carregarRelatorios(dias) {
  // Busca todos os dados em paralelo
  Promise.all([
    fetch('/ui/stats/daily?days='    + dias).then(function(r){return r.json();}),
    fetch('/ui/stats/sessions?days=' + dias).then(function(r){return r.json();}),
    fetch('/ui/stats/types?days='    + dias).then(function(r){return r.json();}),
    fetch('/ui/stats/hours?days='    + dias).then(function(r){return r.json();}),
    fetch('/ui/stats/contacts?days=' + dias + '&limit=20').then(function(r){return r.json();})
  ]).then(function(results) {
    var daily    = Array.isArray(results[0]) ? results[0] : [];
    var sessions = Array.isArray(results[1]) ? results[1] : [];
    var types    = Array.isArray(results[2]) ? results[2] : [];
    var hours    = Array.isArray(results[3]) ? results[3] : [];
    var contacts = Array.isArray(results[4]) ? results[4] : [];

    // ── Totais ──
    var totalIn = 0, totalOut = 0, totalAll = 0, totalFailed = 0;
    daily.forEach(function(row) {
      totalIn  += row.inbound_count  || 0;
      totalOut += row.outbound_count || 0;
      totalAll += row.total_messages || 0;
    });
    sessions.forEach(function(row) { totalFailed += row.failed_count || 0; });
    setText('r-total',    totalAll);
    setText('r-recebidas', totalIn);
    setText('r-enviadas',  totalOut);
    setText('r-falhas',    totalFailed);

    // ── Histórico diário ──
    var tbody = document.getElementById('r-tabela');
    if (daily.length === 0) {
      tbody.innerHTML = '<tr><td colspan="4" style="text-align:center;padding:24px;color:#334155;">Sem dados no período</td></tr>';
    } else {
      tbody.innerHTML = daily.map(function(row) {
        return '<tr>'
          + '<td>' + new Date(row.date).toLocaleDateString('pt-BR') + '</td>'
          + '<td style="color:#e2e8f0;font-weight:500;">' + (row.total_messages||0) + '</td>'
          + '<td style="color:#60a5fa;">' + (row.inbound_count||0) + '</td>'
          + '<td style="color:#c084fc;">' + (row.outbound_count||0) + '</td>'
          + '</tr>';
      }).join('');
    }

    // ── Por sessão/número ──
    var tbodySess = document.getElementById('r-sessoes');
    if (sessions.length === 0) {
      tbodySess.innerHTML = '<tr><td colspan="6" style="text-align:center;padding:24px;color:#334155;">Sem dados no período</td></tr>';
    } else {
      tbodySess.innerHTML = sessions.map(function(row) {
        // phone só vira "+ddd..." se for puramente numérico; senão mostra o JID cru.
        var tel = (row.phone && /^[0-9]+$/.test(row.phone)) ? '+' + row.phone : (row.session_jid || row.phone || '—');
        var pill = row.kind === 'cloud'
          ? '<span style="font-size:10px;background:rgba(59,130,246,.18);color:#60a5fa;padding:3px 9px;border-radius:11px;font-weight:700;letter-spacing:.04em;">OFICIAL</span>'
          : '<span style="font-size:10px;background:rgba(37,211,102,.15);color:#25D366;padding:3px 9px;border-radius:11px;font-weight:700;letter-spacing:.04em;">NÃO OFICIAL</span>';
        return '<tr>'
          + '<td style="font-weight:600;color:#e2e8f0;">' + tel + '</td>'
          + '<td>' + pill + '</td>'
          + '<td style="font-weight:500;">' + (row.total_messages||0) + '</td>'
          + '<td style="color:#60a5fa;">' + (row.inbound_count||0) + '</td>'
          + '<td style="color:#c084fc;">' + (row.outbound_count||0) + '</td>'
          + '<td style="color:#f87171;">' + (row.failed_count||0) + '</td>'
          + '</tr>';
      }).join('');
    }

    // ── Top contatos ──
    var tbodyC = document.getElementById('r-contatos');
    if (contacts.length === 0) {
      tbodyC.innerHTML = '<tr><td colspan="5" style="text-align:center;padding:24px;color:#334155;">Sem dados no período</td></tr>';
    } else {
      tbodyC.innerHTML = contacts.map(function(row) {
        var nome = row.wa_name || row.wa_jid || '—';
        var tel  = row.wa_phone ? '+' + row.wa_phone : '—';
        return '<tr>'
          + '<td style="font-weight:600;color:#e2e8f0;">' + nome + '</td>'
          + '<td style="color:#64748b;font-size:12px;">' + tel + '</td>'
          + '<td style="font-weight:500;">' + (row.total_messages||0) + '</td>'
          + '<td style="color:#60a5fa;">' + (row.inbound_count||0) + '</td>'
          + '<td style="color:#c084fc;">' + (row.outbound_count||0) + '</td>'
          + '</tr>';
      }).join('');
    }

    // ── Gráfico diário ──
    var labels  = daily.map(function(r){ return new Date(r.date).toLocaleDateString('pt-BR',{day:'2-digit',month:'2-digit'}); }).reverse();
    var inData  = daily.map(function(r){ return r.inbound_count||0; }).reverse();
    var outData = daily.map(function(r){ return r.outbound_count||0; }).reverse();
    var ctxD = document.getElementById('chart-diario');
    if (chartDiario) chartDiario.destroy();
    chartDiario = new Chart(ctxD, {
      type: 'bar',
      data: { labels: labels, datasets: [
        { label:'Recebidas', data:inData,  backgroundColor:'rgba(96,165,250,.75)', borderRadius:4, borderSkipped:false },
        { label:'Enviadas',  data:outData, backgroundColor:'rgba(192,132,252,.75)', borderRadius:4, borderSkipped:false }
      ]},
      options: { responsive:true, maintainAspectRatio:true,
        plugins:{ legend:{ labels:{ color:chartLegendColor(), font:{size:11}, boxWidth:10 } } },
        scales:{ x:{ grid:{display:false}, ticks:{color:chartTickColor(),font:{size:10},maxTicksLimit:10} },
                 y:{ grid:{color:chartGridColor()}, ticks:{color:chartTickColor(),font:{size:10}}, beginAtZero:true } } }
    });

    // ── Gráfico distribuição ──
    var ctxPie = document.getElementById('chart-dist');
    if (chartDist) chartDist.destroy();
    chartDist = new Chart(ctxPie, {
      type: 'doughnut',
      data: { labels:['Recebidas','Enviadas'],
        datasets:[{ data:[totalIn||1, totalOut||1], backgroundColor:['rgba(96,165,250,.8)','rgba(192,132,252,.8)'], borderWidth:0, hoverOffset:6 }] },
      options: { responsive:true, maintainAspectRatio:true, cutout:'65%',
        plugins:{ legend:{ position:'bottom', labels:{ color:chartLegendColor(), font:{size:11}, boxWidth:10, padding:14 } } } }
    });

    // ── Gráfico por hora ──
    var hoursLabels = [], hoursData = [];
    for (var h = 0; h < 24; h++) {
      hoursLabels.push(h + 'h');
      var found = hours.find(function(r){ return r.hour === h; });
      hoursData.push(found ? (found.total_messages||0) : 0);
    }
    var ctxH = document.getElementById('chart-horas');
    if (chartHoras) chartHoras.destroy();
    chartHoras = new Chart(ctxH, {
      type: 'bar',
      data: { labels:hoursLabels, datasets:[
        { label:'Mensagens', data:hoursData, backgroundColor:'rgba(52,211,153,.7)', borderRadius:3, borderSkipped:false }
      ]},
      options: { responsive:true, maintainAspectRatio:true,
        plugins:{ legend:{ display:false } },
        scales:{ x:{ grid:{display:false}, ticks:{color:chartTickColor(),font:{size:9},maxTicksLimit:24} },
                 y:{ grid:{color:chartGridColor()}, ticks:{color:chartTickColor(),font:{size:10}}, beginAtZero:true } } }
    });

    // ── Gráfico por tipo ──
    var tipoLabels = types.map(function(r){ return r.message_type||'outro'; });
    var tipoData   = types.map(function(r){ return r.total_messages||0; });
    var tipoCores  = ['rgba(96,165,250,.8)','rgba(192,132,252,.8)','rgba(52,211,153,.8)','rgba(251,191,36,.8)','rgba(248,113,113,.8)','rgba(167,243,208,.8)'];
    var ctxT = document.getElementById('chart-tipos');
    if (chartTipos) chartTipos.destroy();
    chartTipos = new Chart(ctxT, {
      type: 'doughnut',
      data: { labels:tipoLabels,
        datasets:[{ data:tipoData.length ? tipoData : [1], backgroundColor:tipoLabels.length ? tipoCores.slice(0,tipoLabels.length) : ['rgba(71,85,105,.5)'], borderWidth:0, hoverOffset:6 }] },
      options: { responsive:true, maintainAspectRatio:true, cutout:'60%',
        plugins:{ legend:{ position:'bottom', labels:{ color:chartLegendColor(), font:{size:11}, boxWidth:10, padding:12 } } } }
    });

  }).catch(function(e){ console.error('relatorios error', e); });
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

// ─── Permissões CRM ──────────────────────────────────────────────────────
var _permCache = [];           // cache de usuários liberados
var _permPreviewUser = null;
var _permAllUsers = [];        // cache de TODOS os usuários do portal Bitrix
var _permAllUsersLoaded = false;

// Helper local de escape HTML (o dashboard nao tinha um)
function _permEsc(s) {
  if (s == null) return '';
  return String(s).replace(/[&<>"']/g, function(c){
    return ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'})[c];
  });
}

function carregarPermissoes() {
  var box = document.getElementById('perm-list');
  box.innerHTML = '<div style="padding:24px;text-align:center;color:#334155;font-size:13px;">Carregando...</div>';
  document.getElementById('perm-preview').style.display = 'none';
  if (document.getElementById('perm-search-input')) {
    document.getElementById('perm-search-input').value = '';
  }
  _permPreviewUser = null;
  fetch(apiUrl('/ui/permissions/list'))
    .then(function(r){ return r.json(); })
    .then(function(d) {
      if (!d || d.error) {
        box.innerHTML = '<div style="padding:18px;color:#f87171;font-size:13px;text-align:center;">Erro: ' + ((d && d.error) || 'falha') + '</div>';
        return;
      }
      _permCache = d.users || [];
      renderPermList();
      document.getElementById('perm-status').textContent = _permCache.length + ' usuário(s) liberado(s)';
    })
    .catch(function(err) {
      box.innerHTML = '<div style="padding:18px;color:#f87171;font-size:13px;text-align:center;">Erro de rede: ' + err.message + '</div>';
    });
  // Carrega lista de TODOS os usuários do Bitrix em paralelo
  if (!_permAllUsersLoaded) carregarTodosUsuarios(false);
}

function carregarTodosUsuarios(forceRefresh) {
  var results = document.getElementById('perm-search-results');
  if (!results) return;
  results.innerHTML = '<div style="padding:18px;text-align:center;color:#475569;font-size:12px;">Carregando usuários do Bitrix... (pode levar alguns segundos)</div>';
  var btn = document.getElementById('perm-refresh-btn');
  if (forceRefresh && btn) { btn.disabled = true; btn.innerHTML = '↻ Atualizando...'; }
  var url = apiUrl('/ui/permissions/all-users');
  if (forceRefresh) {
    url += (url.indexOf('?') !== -1 ? '&' : '?') + 'refresh=1';
  }
  fetch(url)
    .then(function(r){ return r.json().then(function(d){ return {ok: r.ok, data: d}; }); })
    .then(function(res) {
      if (!res.ok) {
        results.innerHTML = '<div style="padding:18px;color:#f87171;font-size:12px;text-align:center;">Erro: ' + _permEsc(res.data.error || 'falha') + '</div>';
        return;
      }
      _permAllUsers = res.data.users || [];
      _permAllUsersLoaded = true;
      var grantedSet = {};
      _permCache.forEach(function(u) { grantedSet[u.user_id] = true; });
      _permAllUsers.forEach(function(u) { u.has_access = !!grantedSet[u.id]; });
      filtrarTodosUsuarios();
      if (forceRefresh) toast('Lista atualizada: ' + _permAllUsers.length + ' usuários', 'success');
    })
    .catch(function(err) {
      results.innerHTML = '<div style="padding:18px;color:#f87171;font-size:12px;text-align:center;">Erro de rede: ' + _permEsc(err.message) + '</div>';
    })
    .then(function() {
      if (btn) { btn.disabled = false; btn.innerHTML = '↻ Atualizar lista'; }
    });
}

function filtrarTodosUsuarios() {
  var results = document.getElementById('perm-search-results');
  if (!results) return;
  var q = (document.getElementById('perm-search-input').value || '').toLowerCase();
  if (!_permAllUsersLoaded) {
    results.innerHTML = '<div style="padding:18px;text-align:center;color:#475569;font-size:12px;">Carregando usuários do Bitrix...</div>';
    return;
  }
  var list = _permAllUsers;
  if (q) {
    list = list.filter(function(u) {
      return (u.name||'').toLowerCase().indexOf(q) !== -1
          || (u.email||'').toLowerCase().indexOf(q) !== -1
          || String(u.id).indexOf(q) !== -1
          || (u.position||'').toLowerCase().indexOf(q) !== -1;
    });
  }
  var totalMatched = list.length;
  if (list.length === 0) {
    results.innerHTML = '<div style="padding:18px;text-align:center;color:#475569;font-size:12px;">Nenhum usuário encontrado</div>';
    return;
  }
  results.innerHTML = list.map(function(u) {
    var initials = (u.name||'?')[0].toUpperCase();
    var badge = u.has_access
      ? '<span style="font-size:9px;color:#25D366;background:rgba(37,211,102,.15);padding:2px 6px;border-radius:8px;font-weight:700;">JÁ LIBERADO</span>'
      : '';
    var inactive = u.active === false
      ? '<span style="font-size:9px;color:#f87171;background:rgba(248,113,113,.15);padding:2px 6px;border-radius:8px;font-weight:700;margin-left:4px;">INATIVO</span>'
      : '';
    var rowStyle = u.has_access
      ? 'opacity:.55;cursor:not-allowed;'
      : 'cursor:pointer;';
    var click = u.has_access ? '' : 'onclick="selecionarUsuarioParaLiberar(\'' + _permEsc(u.id) + '\')"';
    return ''
      + '<div ' + click + ' style="display:flex;align-items:center;gap:10px;padding:9px 14px;border-bottom:1px solid rgba(255,255,255,.04);transition:background .12s;' + rowStyle + '"'
      + (u.has_access ? '' : ' onmouseover="this.style.background=\'rgba(255,255,255,.04)\'" onmouseout="this.style.background=\'transparent\'"') + '>'
      +   '<div style="width:28px;height:28px;border-radius:50%;background:rgba(96,165,250,.18);color:#60a5fa;display:flex;align-items:center;justify-content:center;font-size:11px;font-weight:700;flex-shrink:0;">' + _permEsc(initials) + '</div>'
      +   '<div style="flex:1;min-width:0;">'
      +     '<div style="font-size:13px;color:#e2e8f0;font-weight:600;display:flex;align-items:center;gap:6px;flex-wrap:wrap;">' + _permEsc(u.name) + ' <span style="color:#475569;font-weight:400;font-size:11px;">#' + _permEsc(u.id) + '</span>' + badge + inactive + '</div>'
      +     (u.email ? '<div style="font-size:11px;color:#64748b;">' + _permEsc(u.email) + (u.position ? ' · ' + _permEsc(u.position) : '') + '</div>' : (u.position ? '<div style="font-size:11px;color:#64748b;">' + _permEsc(u.position) + '</div>' : ''))
      +   '</div>'
      + '</div>';
  }).join('') + '<div style="padding:8px;text-align:center;color:#475569;font-size:11px;background:rgba(0,0,0,.2);">' + totalMatched + ' usuário(s)' + (q ? ' encontrado(s)' : ' ativo(s) no portal') + '</div>';
}

function selecionarUsuarioParaLiberar(userID) {
  var u = _permAllUsers.find(function(x) { return x.id === userID; });
  if (!u) return;
  if (u.has_access) {
    toast('Usuário ' + userID + ' já está liberado', 'error');
    return;
  }
  _permPreviewUser = { id: u.id, name: u.name, email: u.email, position: u.position, active: u.active };
  // Preenche o input com o nome selecionado (mais visual)
  document.getElementById('perm-search-input').value = u.name + ' (#' + u.id + ')';
  var html = ''
    + '<div style="font-size:14px;color:#e2e8f0;font-weight:600;margin-bottom:4px;">' + _permEsc(u.name) + ' <span style="color:#475569;font-weight:400;font-size:12px;">#' + _permEsc(u.id) + '</span></div>'
    + (u.email ? '<div style="font-size:12px;color:#94a3b8;">' + _permEsc(u.email) + '</div>' : '')
    + (u.position ? '<div style="font-size:11.5px;color:#64748b;">' + _permEsc(u.position) + '</div>' : '')
    + (u.active === false ? '<div style="font-size:11.5px;color:#f87171;margin-top:4px;">⚠ Usuário inativo no Bitrix</div>' : '');
  document.getElementById('perm-preview-body').innerHTML = html;
  document.getElementById('perm-preview').style.display = 'block';
  // Scroll suave até o preview
  document.getElementById('perm-preview').scrollIntoView({ behavior: 'smooth', block: 'nearest' });
}

function permCancelarSelecao() {
  _permPreviewUser = null;
  document.getElementById('perm-preview').style.display = 'none';
  document.getElementById('perm-search-input').value = '';
  filtrarTodosUsuarios();
}

function renderPermList() {
  var box = document.getElementById('perm-list');
  if (_permCache.length === 0) {
    box.innerHTML = '<div style="padding:24px;text-align:center;color:#334155;font-size:13px;">Nenhum usuário liberado ainda. Use o formulário acima para adicionar.</div>';
    return;
  }
  box.innerHTML = _permCache.map(function(u) {
    var name = u.user_name || ('Usuário #' + u.user_id);
    return ''
      + '<div style="display:flex;align-items:center;gap:10px;padding:10px 14px;border-bottom:1px solid rgba(255,255,255,.04);">'
      +   '<div style="width:30px;height:30px;border-radius:50%;background:rgba(37,211,102,.18);color:#25D366;display:flex;align-items:center;justify-content:center;font-size:11px;font-weight:700;">' + (name[0] || '?').toUpperCase() + '</div>'
      +   '<div style="flex:1;min-width:0;">'
      +     '<div style="font-size:13.5px;color:#e2e8f0;font-weight:600;">' + _permEsc(name) + ' <span style="color:#475569;font-weight:400;font-size:12px;">#' + _permEsc(u.user_id) + '</span></div>'
      +     (u.granted_at ? '<div style="font-size:11px;color:#475569;">Liberado em ' + _permEsc(u.granted_at) + '</div>' : '')
      +   '</div>'
      +   '<button class="btn btn-ghost btn-sm" style="color:#f87171;border:1px solid rgba(248,113,113,.3);" onclick="permRevoke(\'' + _permEsc(u.user_id) + '\', this)">Remover</button>'
      + '</div>';
  }).join('');
}

function permGrantUser() {
  if (!_permPreviewUser) return;
  var u = _permPreviewUser;
  var btn = document.getElementById('perm-grant-btn');
  btn.disabled = true; btn.textContent = 'Liberando...';
  fetch(apiUrl('/ui/permissions/grant'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ user_id: u.id, user_name: u.name }),
  })
    .then(function(r){ return r.json().then(function(d){ return {ok: r.ok, data: d}; }); })
    .then(function(res) {
      if (!res.ok) { toast('Erro: ' + (res.data.error || 'falha'), 'error'); return; }
      toast('Usuário liberado com sucesso!', 'success');
      _permPreviewUser = null;
      document.getElementById('perm-preview').style.display = 'none';
      var inp = document.getElementById('perm-search-input');
      if (inp) inp.value = '';
      // Atualiza flag has_access localmente para refletir na busca
      var cached = _permAllUsers.find(function(x) { return x.id === u.id; });
      if (cached) cached.has_access = true;
      carregarPermissoes();
    })
    .catch(function(err) { toast('Erro de rede: ' + err.message, 'error'); })
    .then(function() { btn.disabled = false; btn.textContent = '✓ Liberar acesso'; });
}

function permRevoke(userID, btn) {
  if (!confirm('Remover acesso do usuário #' + userID + '?')) return;
  btn.disabled = true;
  fetch(apiUrl('/ui/permissions/revoke'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ user_id: userID }),
  })
    .then(function(r){ return r.json().then(function(d){ return {ok: r.ok, data: d}; }); })
    .then(function(res) {
      if (!res.ok) { toast('Erro: ' + (res.data.error || 'falha'), 'error'); btn.disabled = false; return; }
      toast('Acesso removido', 'success');
      // Atualiza flag has_access localmente
      var cached = _permAllUsers.find(function(x) { return x.id === userID; });
      if (cached) cached.has_access = false;
      carregarPermissoes();
    })
    .catch(function(err) { toast('Erro de rede: ' + err.message, 'error'); btn.disabled = false; });
}

// ─── Templates de Mensagem ──────────────────────────────────────────────────
var _tplCache = [];

function _tplEsc(s) {
  if (s == null) return '';
  return String(s).replace(/[&<>"']/g, function(c){
    return ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'})[c];
  });
}

function carregarTemplatesDashboard() {
  var box = document.getElementById('tpl-list');
  box.innerHTML = '<div style="padding:24px;text-align:center;color:#334155;font-size:13px;">Carregando...</div>';
  fetch(apiUrl('/ui/templates/list'))
    .then(function(r){ return r.json(); })
    .then(function(d){
      if (!d || d.error) {
        box.innerHTML = '<div style="padding:18px;color:#f87171;font-size:13px;text-align:center;">Erro: ' + ((d && d.error) || 'falha') + '</div>';
        return;
      }
      _tplCache = d.templates || d.items || [];
      renderTplList();
      document.getElementById('tpl-status').textContent = _tplCache.length + ' template(s)';
    })
    .catch(function(err){
      box.innerHTML = '<div style="padding:18px;color:#f87171;font-size:13px;text-align:center;">Erro de rede: ' + err.message + '</div>';
    });
}

function renderTplList() {
  var box = document.getElementById('tpl-list');
  if (!_tplCache.length) {
    box.innerHTML = '<div style="padding:30px 18px;text-align:center;color:#475569;font-size:13px;line-height:1.6;">Nenhum template cadastrado ainda.<br><span style="color:#334155;font-size:12px;">Clique em <strong>Novo Template</strong> acima para criar o primeiro.</span></div>';
    return;
  }
  var html = '';
  for (var i = 0; i < _tplCache.length; i++) {
    var t = _tplCache[i];
    var preview = (t.body || '').replace(/\n/g, ' ').slice(0, 140);
    if ((t.body || '').length > 140) preview += '...';
    html += '<div style="display:flex;align-items:flex-start;justify-content:space-between;gap:14px;padding:13px 14px;border-bottom:1px solid rgba(255,255,255,.04);">'
         +   '<div style="flex:1;min-width:0;">'
         +     '<div style="font-size:13px;font-weight:600;color:#e2e8f0;margin-bottom:3px;">' + _tplEsc(t.title || '(sem título)') + '</div>'
         +     '<div style="font-size:12px;color:#64748b;line-height:1.5;">' + _tplEsc(preview) + '</div>'
         +   '</div>'
         +   '<div style="display:flex;gap:6px;flex-shrink:0;">'
         +     '<button class="btn btn-ghost btn-sm" onclick="abrirModalTemplate(\'' + t.id + '\')" style="font-size:11px;color:#94a3b8;">Editar</button>'
         +     '<button class="btn btn-ghost btn-sm" onclick="deletarTemplate(\'' + t.id + '\')" style="font-size:11px;color:#f87171;">Excluir</button>'
         +   '</div>'
         + '</div>';
  }
  box.innerHTML = html;
}

function abrirModalTemplate(id) {
  var modal = document.getElementById('modal-tpl');
  modal.style.display = 'flex';
  if (id) {
    var t = _tplCache.find(function(x){ return x.id === id; });
    if (!t) { fecharModalTemplate(); return; }
    document.getElementById('modal-tpl-titulo').textContent = 'Editar Template';
    document.getElementById('tpl-edit-id').value = id;
    document.getElementById('tpl-title-input').value = t.title || '';
    document.getElementById('tpl-body-input').value = t.body || '';
  } else {
    document.getElementById('modal-tpl-titulo').textContent = 'Novo Template';
    document.getElementById('tpl-edit-id').value = '';
    document.getElementById('tpl-title-input').value = '';
    document.getElementById('tpl-body-input').value = '';
  }
  setTimeout(function(){ document.getElementById('tpl-title-input').focus(); }, 50);
}

function fecharModalTemplate() {
  document.getElementById('modal-tpl').style.display = 'none';
}

function salvarTemplate() {
  var id = document.getElementById('tpl-edit-id').value;
  var title = (document.getElementById('tpl-title-input').value || '').trim();
  var body = (document.getElementById('tpl-body-input').value || '').trim();
  if (!title) { toast('Título é obrigatório', 'error'); return; }
  if (!body)  { toast('Mensagem é obrigatória', 'error'); return; }

  var btn = document.getElementById('tpl-save-btn');
  btn.disabled = true;
  btn.textContent = 'Salvando...';

  var url, payload;
  if (id) {
    var u = apiUrl('/ui/templates/update');
    url = u + (u.indexOf('?') !== -1 ? '&' : '?') + 'id=' + encodeURIComponent(id);
    payload = { title: title, body: body };
  } else {
    url = apiUrl('/ui/templates/create');
    payload = { title: title, body: body, created_by: '' };
  }

  fetch(url, {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify(payload),
  })
    .then(function(r){ return r.json(); })
    .then(function(d){
      btn.disabled = false;
      btn.textContent = 'Salvar';
      if (!d || d.error) { toast(((d && d.error) || 'Falha'), 'error'); return; }
      toast(id ? 'Template atualizado' : 'Template criado', 'success');
      fecharModalTemplate();
      carregarTemplatesDashboard();
    })
    .catch(function(err){
      btn.disabled = false;
      btn.textContent = 'Salvar';
      toast('Erro de rede: ' + err.message, 'error');
    });
}

function deletarTemplate(id) {
  if (!confirm('Excluir este template?')) return;
  var u = apiUrl('/ui/templates/delete');
  var url = u + (u.indexOf('?') !== -1 ? '&' : '?') + 'id=' + encodeURIComponent(id);
  fetch(url, {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
  })
    .then(function(r){ return r.json(); })
    .then(function(d){
      if (!d || d.error) { toast(((d && d.error) || 'Falha'), 'error'); return; }
      toast('Template excluído', 'success');
      carregarTemplatesDashboard();
    })
    .catch(function(err){ toast('Erro de rede: ' + err.message, 'error'); });
}

// ─── Histórico de Conversas ─────────────────────────────────────────────────
var _histSessions = [];
var _histActiveJid = '';
var _histConvs = [];
var _histActivePhone = '';

function _histEsc(s) {
  if (s == null) return '';
  return String(s).replace(/[&<>"']/g, function(c){
    return ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'})[c];
  });
}

function _histFmtTime(iso) {
  if (!iso) return '';
  try {
    var d = new Date(iso);
    var hoje = new Date();
    if (d.toDateString() === hoje.toDateString()) {
      return d.toLocaleTimeString('pt-BR', {hour:'2-digit', minute:'2-digit'});
    }
    var diff = (hoje - d) / 86400000;
    if (diff < 7) {
      return ['Dom','Seg','Ter','Qua','Qui','Sex','Sáb'][d.getDay()];
    }
    return d.toLocaleDateString('pt-BR', {day:'2-digit', month:'2-digit'});
  } catch(e) { return ''; }
}

function _histFmtFull(iso) {
  if (!iso) return '';
  try {
    return new Date(iso).toLocaleString('pt-BR', {
      day:'2-digit', month:'2-digit', year:'numeric',
      hour:'2-digit', minute:'2-digit'
    });
  } catch(e) { return ''; }
}

function carregarHistoricoSessoes() {
  var sel = document.getElementById('hist-session-select');
  sel.innerHTML = '<option value="">Carregando sessões...</option>';
  fetch(apiUrl('/ui/history/sessions'))
    .then(function(r){ return r.json(); })
    .then(function(d){
      if (!d || d.error) {
        sel.innerHTML = '<option value="">Erro: ' + ((d && d.error) || 'falha') + '</option>';
        return;
      }
      _histSessions = d.sessions || [];
      if (!_histSessions.length) {
        sel.innerHTML = '<option value="">Nenhuma sessão ativa neste portal</option>';
        document.getElementById('hist-conv-list').innerHTML =
          '<div style="padding:24px;text-align:center;color:#475569;font-size:12px;">Sem sessões ativas.</div>';
        return;
      }
      var opts = '<option value="">— escolha uma sessão —</option>';
      for (var i = 0; i < _histSessions.length; i++) {
        var s = _histSessions[i];
        var tipo = s.type === 'cloud_api' ? ' (Cloud)' : ' (QR)';
        var statusTxt = s.status === 'active' ? '' : ' [desconectada]';
        var lbl = '+' + (s.phone || s.jid) + tipo + statusTxt;
        opts += '<option value="' + _histEsc(s.jid) + '">' + _histEsc(lbl) + '</option>';
      }
      sel.innerHTML = opts;
      // se só uma sessão, seleciona ela
      if (_histSessions.length === 1) {
        sel.value = _histSessions[0].jid;
        onHistSessionChange();
      } else {
        // Se tem multiplas, pre-seleciona a primeira ATIVA (sao ordenadas: ativas primeiro)
        var firstActive = _histSessions.find(function(x){ return x.status === 'active'; });
        if (firstActive) {
          sel.value = firstActive.jid;
          onHistSessionChange();
        }
      }
    })
    .catch(function(err){
      sel.innerHTML = '<option value="">Erro de rede</option>';
      toast('Erro de rede: ' + err.message, 'error');
    });
}

function onHistSessionChange() {
  var sel = document.getElementById('hist-session-select');
  _histActiveJid = sel.value;
  _histActivePhone = '';
  document.getElementById('hist-msg-title').textContent = 'Selecione uma conversa';
  document.getElementById('hist-msg-sub').textContent = '';
  document.getElementById('hist-msg-body').innerHTML =
    '<div style="padding:60px 18px;text-align:center;color:#334155;font-size:13px;line-height:1.7;">'
    + '<svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" style="opacity:.4;margin-bottom:10px;"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/></svg>'
    + '<div>Selecione uma conversa à esquerda<br>para ver as mensagens.</div>'
    + '</div>';
  if (!_histActiveJid) {
    document.getElementById('hist-conv-list').innerHTML =
      '<div style="padding:24px;text-align:center;color:#475569;font-size:12px;">Escolha uma sessão acima.</div>';
    document.getElementById('hist-conv-count').textContent = '—';
    return;
  }
  carregarHistConversas();
}

function _histAppendQuery(base, qs) {
  var sep = base.indexOf('?') !== -1 ? '&' : '?';
  return base + sep + qs;
}

function carregarHistConversas() {
  var box = document.getElementById('hist-conv-list');
  box.innerHTML = '<div style="padding:24px;text-align:center;color:#475569;font-size:12px;">Carregando...</div>';
  var url = _histAppendQuery(apiUrl('/ui/history/conversations'), 'session_jid=' + encodeURIComponent(_histActiveJid));
  fetch(url)
    .then(function(r){ return r.json(); })
    .then(function(d){
      if (!d || d.error) {
        box.innerHTML = '<div style="padding:18px;color:#f87171;font-size:12px;text-align:center;">Erro: ' + ((d && d.error) || 'falha') + '</div>';
        return;
      }
      _histConvs = d.conversations || [];
      renderHistConvList();
      document.getElementById('hist-conv-count').textContent = _histConvs.length + ' conversa(s)';
    })
    .catch(function(err){
      box.innerHTML = '<div style="padding:18px;color:#f87171;font-size:12px;text-align:center;">Erro de rede: ' + err.message + '</div>';
    });
}

function renderHistConvList() {
  var box = document.getElementById('hist-conv-list');
  var q = (document.getElementById('hist-search').value || '').trim().toLowerCase();
  var filtered = q ? _histConvs.filter(function(c){ return (c.phone||'').toLowerCase().indexOf(q) !== -1; }) : _histConvs;
  if (!filtered.length) {
    box.innerHTML = '<div style="padding:30px 18px;text-align:center;color:#475569;font-size:12px;">'
      + (q ? 'Nenhum número casou com o filtro.' : 'Nenhuma conversa nesta sessão.') + '</div>';
    return;
  }
  var html = '';
  for (var i = 0; i < filtered.length; i++) {
    var c = filtered[i];
    var active = (c.phone === _histActivePhone) ? 'background:rgba(37,211,102,.10);border-left:3px solid #25D366;' : 'border-left:3px solid transparent;';
    var dirIcon = c.last_direction === 'outbound' ? '↗ ' : '↘ ';
    var preview = (c.last_message || '').replace(/\n/g,' ').slice(0, 60);
    if ((c.last_message || '').length > 60) preview += '...';
    html += '<div class="hist-conv-item" data-phone="' + _histEsc(c.phone) + '" onclick="abrirHistConversa(\'' + _histEsc(c.phone) + '\')" '
         +  'style="padding:10px 12px;cursor:pointer;border-bottom:1px solid rgba(255,255,255,.03);transition:background .12s;' + active + '" '
         +  'onmouseover="if(this.dataset.phone!==\'' + _histEsc(_histActivePhone) + '\')this.style.background=\'rgba(255,255,255,.03)\'" '
         +  'onmouseout="if(this.dataset.phone!==\'' + _histEsc(_histActivePhone) + '\')this.style.background=\'transparent\'">'
         +    '<div style="display:flex;justify-content:space-between;align-items:baseline;gap:8px;margin-bottom:3px;">'
         +      '<div style="font-size:13px;font-weight:600;color:#e2e8f0;">+' + _histEsc(c.phone) + '</div>'
         +      '<div style="font-size:10.5px;color:#64748b;flex-shrink:0;">' + _histEsc(_histFmtTime(c.last_at)) + '</div>'
         +    '</div>'
         +    '<div style="font-size:11.5px;color:#64748b;line-height:1.4;">'
         +      '<span style="color:#475569;">' + dirIcon + '</span>' + _histEsc(preview)
         +    '</div>'
         +    '<div style="font-size:10px;color:#334155;margin-top:3px;">' + c.total + ' mensagens</div>'
         +  '</div>';
  }
  box.innerHTML = html;
}

function filtrarHistConversas() {
  renderHistConvList();
}

function abrirHistConversa(phone) {
  _histActivePhone = phone;
  renderHistConvList(); // re-render para destacar ativo
  var body = document.getElementById('hist-msg-body');
  body.innerHTML = '<div style="padding:30px;text-align:center;color:#475569;font-size:12px;">Carregando mensagens...</div>';
  document.getElementById('hist-msg-title').textContent = '+' + phone;
  document.getElementById('hist-msg-sub').textContent = 'Carregando...';

  var url = _histAppendQuery(apiUrl('/ui/history/messages'),
    'session_jid=' + encodeURIComponent(_histActiveJid)
    + '&phone=' + encodeURIComponent(phone));
  fetch(url)
    .then(function(r){ return r.json(); })
    .then(function(d){
      if (!d || d.error) {
        body.innerHTML = '<div style="padding:30px;color:#f87171;font-size:12px;text-align:center;">Erro: ' + ((d && d.error) || 'falha') + '</div>';
        return;
      }
      renderHistMessages(d.messages || []);
      document.getElementById('hist-msg-sub').textContent = (d.count || 0) + ' mensagens';
    })
    .catch(function(err){
      body.innerHTML = '<div style="padding:30px;color:#f87171;font-size:12px;text-align:center;">Erro de rede: ' + err.message + '</div>';
    });
}

function renderHistMessages(msgs) {
  var body = document.getElementById('hist-msg-body');
  if (!msgs.length) {
    body.innerHTML = '<div style="padding:60px;text-align:center;color:#334155;font-size:13px;">Sem mensagens nesta conversa.</div>';
    return;
  }
  var html = '';
  var lastDate = '';
  for (var i = 0; i < msgs.length; i++) {
    var m = msgs[i];
    var d = new Date(m.created_at);
    var dateLabel = d.toLocaleDateString('pt-BR', {day:'2-digit', month:'2-digit', year:'numeric'});
    if (dateLabel !== lastDate) {
      html += '<div style="text-align:center;margin:14px 0 10px;"><span style="font-size:10.5px;color:#475569;background:rgba(255,255,255,.04);padding:3px 10px;border-radius:10px;">' + _histEsc(dateLabel) + '</span></div>';
      lastDate = dateLabel;
    }
    var hora = d.toLocaleTimeString('pt-BR', {hour:'2-digit', minute:'2-digit'});
    var out = m.direction === 'outbound';
    var author = m.author_name ? ('<div style="font-size:10.5px;color:' + (out ? 'rgba(255,255,255,.7)' : '#94a3b8') + ';font-weight:600;margin-bottom:2px;">' + _histEsc(m.author_name) + '</div>') : '';
    var align = out ? 'flex-end' : 'flex-start';
    var bg = out ? '#075e54' : '#262d31';
    var color = '#e9edef';
    html += '<div style="display:flex;justify-content:' + align + ';margin-bottom:6px;">'
         +    '<div style="max-width:72%;background:' + bg + ';color:' + color + ';padding:7px 11px;border-radius:8px;font-size:13px;line-height:1.45;word-wrap:break-word;">'
         +      author
         +      '<div>' + _histEsc(m.text || '').replace(/\n/g, '<br>') + '</div>'
         +      '<div style="font-size:10px;color:rgba(255,255,255,.55);text-align:right;margin-top:3px;">' + _histEsc(hora) + '</div>'
         +    '</div>'
         +  '</div>';
  }
  body.innerHTML = html;
  // scroll pra ultima msg
  body.scrollTop = body.scrollHeight;
}

// ─── Filas Bitrix ─────────────────────────────────────────────────────────────
var _portaisCache = []; // cache dos portais para o modal

function carregarFilas() {
  var wrap = document.getElementById('lista-filas');
  // Busca portais e contas em paralelo
  Promise.all([
    fetch(apiUrl('/ui/bitrix/queues')).then(function(r){return r.json();}),
    fetch(apiUrl('/ui/bitrix/accounts')).then(function(r){return r.json();})
  ])
  .then(function(results) {
    var portais = results[0].queues || [];
    var contas  = results[1].accounts || [];
    _portaisCache = portais;

    if (portais.length === 0) {
      wrap.innerHTML = '<div class="card" style="padding:48px;text-align:center;">'
        + '<svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="#1e293b" stroke-width="1.3" style="margin:0 auto 16px;display:block;"><path d="M17 21v-2a4 4 0 00-4-4H5a4 4 0 00-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 00-3-3.87"/><path d="M16 3.13a4 4 0 010 7.75"/></svg>'
        + '<p style="color:#475569;font-size:14px;margin-bottom:8px;">Nenhum portal instalado via Marketplace</p>'
        + '<p style="color:#334155;font-size:12.5px;">Instale o app no Bitrix24 Marketplace para que os portais apareçam aqui.</p>'
        + '</div>';
      return;
    }

    var html = '<div style="display:flex;flex-direction:column;gap:16px;">';

    // Agrupa contas por portal
    portais.forEach(function(q) {
      var vinculos = contas.filter(function(a) {
        var d = (a.domain||'').replace(/^https?:\/\//,'').replace(/\/$/,'').toLowerCase();
        return d === q.domain;
      });

      var instaladoEm = q.installed_at ? new Date(q.installed_at).toLocaleDateString('pt-BR') : '—';
      var domEnc = encodeURIComponent(q.domain);

      html += '<div class="card" style="padding:20px;">';

      // Cabeçalho do portal
      html += '<div style="display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap;margin-bottom:18px;">'
        + '<div style="display:flex;align-items:center;gap:13px;">'
        + '<div class="metric-icon" style="background:rgba(192,132,252,.12);width:44px;height:44px;">'
        + '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#c084fc" stroke-width="2"><path d="M17 21v-2a4 4 0 00-4-4H5a4 4 0 00-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 00-3-3.87"/><path d="M16 3.13a4 4 0 010 7.75"/></svg>'
        + '</div>'
        + '<div>'
        + '<div style="font-size:15px;font-weight:700;color:#f1f5f9;">' + q.domain + '</div>'
        + '<div style="font-size:11.5px;color:#475569;margin-top:2px;">Instalado em ' + instaladoEm + ' · conector: <span style="font-family:monospace;">' + (q.connector_id||'whatsapp_uc') + '</span></div>'
        + '</div></div>'
        + '<div style="display:flex;gap:8px;align-items:center;flex-wrap:wrap;">'
        + '<span class="badge badge-purple">Marketplace</span>'
        + '<button class="btn btn-ghost btn-sm" id="btn-reg-' + domEnc + '" onclick="ativarConnector(\'' + q.domain + '\',0)" title="Força register+activate no Bitrix24">'
        + '<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="23 4 23 10 17 10"/><polyline points="1 20 1 14 7 14"/><path d="M3.51 9a9 9 0 0114.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0020.49 15"/></svg>'
        + ' Registrar Connector</button>'
        + '</div></div>';

      // Vínculos existentes
      if (vinculos.length > 0) {
        html += '<div style="display:flex;flex-direction:column;gap:8px;margin-bottom:14px;">';
        vinculos.forEach(function(v) {
          // Telefone para exibição: usa display_phone do backend; fallback ao split do JID.
          var tel = '';
          if (v.display_phone) {
            tel = '+' + v.display_phone;
          } else if (v.session_jid.indexOf('cloud:') === 0) {
            tel = 'WhatsApp Oficial';
          } else {
            tel = '+' + v.session_jid.split(':')[0].split('@')[0];
          }
          var jidEnc = encodeURIComponent(v.session_jid);
          var tipoBadge = (v.session_type === 'cloud_api')
            ? '<span style="font-size:9.5px;background:rgba(59,130,246,.18);color:#60a5fa;padding:2px 7px;border-radius:10px;font-weight:700;letter-spacing:.04em;margin-left:6px;">OFICIAL</span>'
            : '<span style="font-size:9.5px;background:rgba(37,211,102,.15);color:#25D366;padding:2px 7px;border-radius:10px;font-weight:700;letter-spacing:.04em;margin-left:6px;">QR</span>';
          html += '<div style="display:flex;align-items:center;justify-content:space-between;gap:10px;padding:11px 14px;background:rgba(255,255,255,.03);border:1px solid rgba(255,255,255,.06);border-radius:10px;flex-wrap:wrap;">'
            + '<div style="display:flex;align-items:center;gap:10px;">'
            + '<div class="dot dot-green"></div>'
            + '<div>'
            + '<div style="font-size:13.5px;font-weight:600;color:#e2e8f0;display:flex;align-items:center;gap:6px;">' + tel + tipoBadge + '</div>'
            + '<div style="font-size:11px;color:#475569;">' + v.session_jid + '</div>'
            + '</div></div>'
            + '<div style="display:flex;align-items:center;gap:10px;">'
            + '<span class="badge badge-blue">Open Line ' + (v.open_line_id||'?') + '</span>'
            + '<button class="btn btn-danger btn-sm" onclick="removerVinculo(\'' + domEnc + '\',\'' + jidEnc + '\')">'
            + '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 01-2 2H8a2 2 0 01-2-2L5 6"/></svg>'
            + ' Remover</button>'
            + '</div></div>';
        });
        html += '</div>';
      } else {
        html += '<div style="padding:12px 14px;background:rgba(255,255,255,.02);border:1px dashed rgba(255,255,255,.08);border-radius:10px;font-size:12.5px;color:#475569;margin-bottom:14px;text-align:center;">'
          + 'Nenhum número vinculado — clique em <strong style="color:#94a3b8;">Novo Vínculo</strong> para conectar um WhatsApp a este portal.'
          + '</div>';
      }

      // Botão adicionar vínculo neste portal
      html += '<div style="border-top:1px solid rgba(255,255,255,.06);padding-top:14px;">'
        + '<button class="btn btn-ghost btn-sm" onclick="abrirModalFila(\'' + q.domain + '\')">'
        + '<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>'
        + ' Adicionar Número a este Portal</button>'
        + '</div>';

      html += '</div>'; // fim card
    });

    html += '</div>';
    wrap.innerHTML = html;
  })
  .catch(function() {
    wrap.innerHTML = '<div style="text-align:center;padding:24px;color:#f87171;font-size:13px;">Erro ao carregar filas</div>';
  });
}

function abrirModalFila(prePortal) {
  // Popula select de portais
  var selPortal = document.getElementById('fila-portal');
  selPortal.innerHTML = '<option value="">Selecione o portal...</option>';
  _portaisCache.forEach(function(q) {
    var opt = document.createElement('option');
    opt.value = q.domain;
    opt.textContent = q.domain;
    if ((prePortal && q.domain === prePortal) || (!prePortal && PORTAL && q.domain.toLowerCase() === PORTAL.toLowerCase())) {
      opt.selected = true;
    }
    selPortal.appendChild(opt);
  });
  selPortal.disabled = !!PORTAL;

  // Quando muda o portal, reseta o select de linhas
  selPortal.onchange = function() {
    var hasDomain = !!selPortal.value;
    document.getElementById('fila-buscar-linhas-btn').disabled = !hasDomain;
    var sel = document.getElementById('fila-openline');
    sel.innerHTML = '<option value="">' + (hasDomain ? 'Clique em "Buscar" para carregar...' : 'Selecione o portal primeiro...') + '</option>';
    sel.disabled = true;
    document.getElementById('fila-linhas-hint').textContent = hasDomain
      ? 'Clique em "Buscar" para carregar as Open Lines disponíveis no portal.'
      : 'Selecione o portal e clique em "Buscar".';
  };

  // Se portal já veio pré-selecionado, habilita botão e dispara busca automaticamente
  var temPortal = !!selPortal.value;
  document.getElementById('fila-buscar-linhas-btn').disabled = !temPortal;
  var selLine = document.getElementById('fila-openline');
  selLine.innerHTML = '<option value="">' + (temPortal ? 'Clique em "Buscar"...' : 'Selecione o portal primeiro...') + '</option>';
  selLine.disabled = true;
  if (temPortal) { buscarLinhasBitrix(); }

  // Popula select de sessões — usa details (com type e phone real) quando disponível
  fetch('/ui/sessions')
  .then(function(r){return r.json();})
  .then(function(d){
    var sel = document.getElementById('fila-sessao');
    sel.innerHTML = '<option value="">Selecione o número conectado...</option>';
    var lista = (d.details && d.details.length) ? d.details : (d.sessions||[]).map(function(j){return {jid:j,type:'qr'};});
    lista.forEach(function(s) {
      var jid = s.jid || s;
      var tel, sufixo;
      if (s.type === 'cloud_api') {
        tel = s.phone ? '+' + s.phone : 'WhatsApp Oficial';
        sufixo = ' (Oficial)';
      } else {
        tel = '+' + jid.split(':')[0].split('@')[0];
        sufixo = ' (QR)';
      }
      var opt = document.createElement('option');
      opt.value = jid;
      opt.textContent = tel + sufixo;
      sel.appendChild(opt);
    });
    if (lista.length === 0) {
      sel.innerHTML = '<option value="">Nenhum número conectado — conecte primeiro em Sessões</option>';
    }
  }).catch(function(){});

  document.getElementById('fila-modal-save-btn').disabled = false;
  document.getElementById('fila-modal-save-btn').textContent = 'Criar Vínculo e Ativar';
  document.getElementById('fila-modal').style.display = 'flex';
}

// ─── Custom Select ────────────────────────────────────────────────────────────
var _cselectData = {}; // id → [{value, label, color}]

// ─── Custom Select refatorado ─────────────────────────────────────────────────
// Estratégia: ao abrir, move o dropdown para o <body> com position:fixed
// para escapar de qualquer overflow:hidden ou stacking context de modais.
// Ao fechar, devolve o dropdown para dentro do .cselect original.

var _cselectOpen = null; // id do cselect atualmente aberto

function toggleCSelect(id) {
  if (_cselectOpen === id) {
    _fecharCSelect(id);
  } else {
    if (_cselectOpen) _fecharCSelect(_cselectOpen);
    _abrirCSelect(id);
  }
}

function _abrirCSelect(id) {
  var trigger  = document.getElementById(id + '-trigger');
  var dropdown = document.getElementById(id + '-dropdown');
  var wrap     = document.getElementById(id + '-wrap');
  if (!trigger || !dropdown) return;

  // Move para o body para escapar de overflow:hidden e z-index de modais
  document.body.appendChild(dropdown);

  var rect = trigger.getBoundingClientRect();
  dropdown.style.position  = 'fixed';
  dropdown.style.left      = rect.left + 'px';
  dropdown.style.width     = rect.width + 'px';
  dropdown.style.top       = (rect.bottom + 4) + 'px';
  dropdown.style.maxHeight = Math.min(260, window.innerHeight - rect.bottom - 16) + 'px';
  dropdown.style.zIndex    = '99999';
  dropdown.classList.add('open');
  trigger.classList.add('open');
  _cselectOpen = id;

  var search = document.getElementById(id + '-search');
  if (search) { search.value = ''; filtrarCSelect(id, ''); setTimeout(function(){ search.focus(); }, 60); }
}

function _fecharCSelect(id) {
  var dropdown = document.getElementById(id + '-dropdown');
  var trigger  = document.getElementById(id + '-trigger');
  var wrap     = document.getElementById(id + '-wrap');
  if (!dropdown) return;

  dropdown.classList.remove('open');
  if (trigger) trigger.classList.remove('open');

  // Devolve o dropdown para dentro do .cselect original
  if (wrap && dropdown.parentNode === document.body) {
    wrap.appendChild(dropdown);
  }
  _cselectOpen = null;
}

function filtrarCSelect(id, q) {
  var opts = document.getElementById(id + '-options');
  if (!opts) return;
  var items = _cselectData[id] || [];
  q = (q||'').toLowerCase();
  var filtered = q ? items.filter(function(i){ return i.label.toLowerCase().indexOf(q) >= 0; }) : items;
  if (filtered.length === 0) {
    opts.innerHTML = '<div class="cselect-empty">Nenhum resultado encontrado</div>';
    return;
  }
  var hiddenInput = document.getElementById(id);
  var selVal = hiddenInput ? hiddenInput.value : '';
  opts.innerHTML = filtered.map(function(item) {
    var cls = 'cselect-option' + (String(item.value) === String(selVal) ? ' selected' : '');
    var color = item.color ? 'color:' + item.color + ';' : '';
    var check = item.color === '#4ade80'
      ? '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="#4ade80" stroke-width="2.5"><polyline points="20 6 9 17 4 12"/></svg>'
      : '<span style="width:12px;display:inline-block;"></span>';
    return '<div class="' + cls + '" style="' + color + '" onclick="selecionarCSelect(\'' + id + '\',' + JSON.stringify(item.value) + ',' + JSON.stringify(item.label) + ')">'
      + check + item.label + '</div>';
  }).join('');
}

function selecionarCSelect(id, value, label) {
  var hidden = document.getElementById(id);
  var lbl    = document.getElementById(id + '-label');
  if (!hidden || !lbl) return;
  hidden.value = value;
  lbl.textContent = label;
  lbl.classList.remove('cselect-placeholder');
  _fecharCSelect(id);
  var ev = new Event('change');
  hidden.dispatchEvent(ev);
}

function setCSelectPlaceholder(id, text) {
  var lbl    = document.getElementById(id + '-label');
  var hidden = document.getElementById(id);
  var opts   = document.getElementById(id + '-options');
  if (lbl) { lbl.textContent = text; lbl.classList.add('cselect-placeholder'); }
  if (hidden) hidden.value = '';
  _cselectData[id] = [];
  if (opts) opts.innerHTML = '<div class="cselect-empty">' + text + '</div>';
}

// Fecha ao clicar fora — verifica se o clique foi no trigger ou no dropdown (mesmo no body)
document.addEventListener('click', function(e) {
  if (!_cselectOpen) return;
  var trigger  = document.getElementById(_cselectOpen + '-trigger');
  var dropdown = document.getElementById(_cselectOpen + '-dropdown');
  var inTrigger  = trigger  && trigger.contains(e.target);
  var inDropdown = dropdown && dropdown.contains(e.target);
  if (!inTrigger && !inDropdown) {
    _fecharCSelect(_cselectOpen);
  }
});

function buscarLinhasBitrix() {
  var domain = document.getElementById('fila-portal').value.trim();
  if (!domain) { toast('Selecione o portal primeiro', 'error'); return; }

  var btn  = document.getElementById('fila-buscar-linhas-btn');
  var hint = document.getElementById('fila-linhas-hint');
  var sel  = document.getElementById('fila-openline');

  btn.disabled = true;
  btn.innerHTML = '<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" style="animation:spin .8s linear infinite"><polyline points="23 4 23 10 17 10"/><path d="M20.49 15a9 9 0 11-2.12-9.36L23 10"/></svg>';
  sel.innerHTML = '<option value="">Buscando linhas...</option>';
  sel.disabled = true;
  hint.textContent = 'Varrendo Open Lines no portal, aguarde...';

  fetch(apiUrl('/ui/bitrix/lines?domain=' + encodeURIComponent(domain)))
  .then(function(r){ return r.json(); })
  .then(function(d) {
    btn.disabled = false;
    btn.innerHTML = '<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg> Buscar';

    var lines = d.lines || [];
    if (lines.length === 0) {
      sel.innerHTML = '<option value="">Nenhuma Open Line encontrada</option>';
      hint.textContent = 'Nenhuma Open Line encontrada. Verifique as permissões do app.';
      return;
    }

    // Popula o <select> nativo
    sel.innerHTML = '<option value="">Selecione a Open Line...</option>';
    lines.forEach(function(l) {
      var opt = document.createElement('option');
      opt.value = l.id;
      opt.textContent = l.name + ' (ID: ' + l.id + ')' + (l.connector_ok ? ' ✓' : '');
      sel.appendChild(opt);
    });
    sel.disabled = false;

    var ativos = lines.filter(function(l){ return l.connector_ok; });
    hint.textContent = lines.length + ' linhas encontradas'
      + (ativos.length > 0 ? ' · ' + ativos.length + ' já com connector ativo (✓)' : '')
      + '. Selecione a linha desejada.';
  })
  .catch(function() {
    btn.disabled = false;
    btn.innerHTML = '<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg> Buscar';
    sel.innerHTML = '<option value="">Erro ao buscar linhas</option>';
    hint.textContent = 'Erro ao buscar linhas. Verifique se o token do portal é válido.';
  });
}

function fecharModalFila() {
  document.getElementById('fila-modal').style.display = 'none';
}

function salvarVinculoFila() {
  var domain   = document.getElementById('fila-portal').value.trim();
  var jid      = document.getElementById('fila-sessao').value.trim();
  var lineId   = parseInt(document.getElementById('fila-openline').value);
  if (!domain) { toast('Selecione o portal Bitrix24', 'error'); return; }
  if (!jid)    { toast('Selecione o número WhatsApp', 'error'); return; }
  if (!lineId || lineId < 1) { toast('Selecione uma Open Line (clique em "Buscar Linhas")', 'error'); return; }

  var btn = document.getElementById('fila-modal-save-btn');
  btn.disabled = true; btn.textContent = 'Criando vínculo...';

  fetch('/ui/bitrix/queues/link', {
    method: 'POST',
    headers: {'Content-Type':'application/json'},
    body: JSON.stringify({domain: domain, session_jid: jid, open_line_id: lineId})
  })
  .then(function(r){return r.json();})
  .then(function(d) {
    if (d.error) { toast(d.error, 'error'); btn.disabled=false; btn.textContent='Criar Vínculo e Ativar'; return; }
    toast('Vínculo criado! Registrando connector no Bitrix24...', 'success');
    fecharModalFila();
    carregarFilas();
    // Ativa o connector após criar o vínculo
    ativarConnector(domain, lineId);
  })
  .catch(function(){ toast('Erro ao criar vínculo', 'error'); btn.disabled=false; btn.textContent='Criar Vínculo e Ativar'; });
}

function removerVinculo(domEnc, jidEnc) {
  var jid = decodeURIComponent(jidEnc);
  var tel = '+' + jid.split(':')[0].split('@')[0];
  abrirConfirm('Remover o vínculo do número ' + tel + '?\nAs mensagens desse número deixarão de chegar no Bitrix24.', function() {
    fetch('/ui/bitrix/queues/link?domain=' + domEnc + '&jid=' + jidEnc, {method:'DELETE'})
    .then(function(r){return r.json();})
    .then(function(d) {
      if (d.error) { toast(d.error, 'error'); return; }
      toast('Vínculo removido', 'success');
      carregarFilas();
    })
    .catch(function(){ toast('Erro ao remover vínculo', 'error'); });
  });
}

function ativarConnector(domain, lineId) {
  var btnId = 'btn-reg-' + encodeURIComponent(domain);
  var btn = document.getElementById(btnId);
  if (btn) { btn.disabled=true; btn.textContent=' Registrando...'; }

  fetch('/ui/bitrix/queues/activate', {
    method: 'POST',
    headers: {'Content-Type':'application/json'},
    body: JSON.stringify({domain: domain, open_line_id: lineId||0})
  })
  .then(function(r){return r.json();})
  .then(function(d) {
    if (btn) { btn.disabled=false; btn.innerHTML='<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="23 4 23 10 17 10"/><polyline points="1 20 1 14 7 14"/><path d="M3.51 9a9 9 0 0114.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0020.49 15"/></svg> Registrar Connector'; }
    if (d.status === 'ok') {
      var detalhes = Object.entries(d.steps||{}).map(function(e){return e[0]+': '+e[1];}).join(' | ');
      toast('Connector registrado com sucesso! ' + detalhes, 'success');
    } else {
      var erros = Object.entries(d.steps||{}).filter(function(e){return e[1]!=='ok';}).map(function(e){return e[0]+': '+e[1];}).join(' | ');
      toast('Erro ao registrar: ' + erros, 'error');
    }
  })
  .catch(function(){ if(btn){btn.disabled=false;} toast('Erro ao registrar connector', 'error'); });
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
