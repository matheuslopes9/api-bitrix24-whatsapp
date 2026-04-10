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

	// Busca contadores de mensagens do banco (últimas 24h)
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
		"messages_failed":   dead, // dead queue = falhas permanentes
	})
}

const dashboardHTML = `<!DOCTYPE html>
<html lang="pt-BR" class="dark">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>WA Connector — Dashboard</title>
<script src="https://cdn.tailwindcss.com"></script>
<script src="https://cdn.jsdelivr.net/npm/chart.js@4.4.0/dist/chart.umd.min.js"></script>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link href="https://fonts.googleapis.com/css2?family=Inter:wght@300;400;500;600;700&display=swap" rel="stylesheet">
<script>
tailwind.config = {
  darkMode: 'class',
  theme: {
    extend: {
      fontFamily: { sans: ['Inter', 'sans-serif'] },
      colors: {
        wa: { DEFAULT: '#25D366', dark: '#1ebe5d', light: '#dcfce7' },
        glass: {
          DEFAULT: 'rgba(255,255,255,0.05)',
          border: 'rgba(255,255,255,0.08)',
          hover: 'rgba(255,255,255,0.08)',
        }
      },
      backdropBlur: { xs: '2px' }
    }
  }
}
</script>
<style>
  * { box-sizing: border-box; }
  body { font-family: 'Inter', sans-serif; background: #0a0e1a; color: #e2e8f0; }

  /* Glassmorphism card */
  .glass-card {
    background: rgba(255,255,255,0.04);
    border: 1px solid rgba(255,255,255,0.08);
    backdrop-filter: blur(12px);
    -webkit-backdrop-filter: blur(12px);
    border-radius: 16px;
    transition: background 0.2s, border-color 0.2s, transform 0.15s;
  }
  .glass-card:hover { background: rgba(255,255,255,0.07); border-color: rgba(255,255,255,0.14); }

  /* Sidebar */
  .sidebar-item {
    display: flex; align-items: center; gap: 12px;
    padding: 10px 14px; border-radius: 10px;
    cursor: pointer; transition: background 0.15s, color 0.15s;
    font-size: 14px; font-weight: 500; color: #94a3b8;
    border: 1px solid transparent;
  }
  .sidebar-item:hover { background: rgba(255,255,255,0.06); color: #e2e8f0; }
  .sidebar-item.active { background: rgba(37,211,102,0.12); color: #25D366; border-color: rgba(37,211,102,0.2); }
  .sidebar-item svg { width: 18px; height: 18px; flex-shrink: 0; }

  /* Gradient bg blobs */
  .blob { position: fixed; border-radius: 50%; filter: blur(80px); opacity: 0.12; pointer-events: none; z-index: 0; }

  /* Metric card value */
  .metric-value { font-size: 2rem; font-weight: 700; line-height: 1; }
  .metric-label { font-size: 12px; color: #64748b; font-weight: 500; text-transform: uppercase; letter-spacing: 0.05em; }

  /* Status dot */
  .dot-green { width: 8px; height: 8px; border-radius: 50%; background: #25D366; flex-shrink: 0; box-shadow: 0 0 6px #25D366; }
  .dot-yellow { width: 8px; height: 8px; border-radius: 50%; background: #f59e0b; flex-shrink: 0; }
  .dot-red { width: 8px; height: 8px; border-radius: 50%; background: #ef4444; flex-shrink: 0; }

  /* Tabs */
  .tab-btn { padding: 6px 16px; border-radius: 8px; font-size: 13px; font-weight: 500; cursor: pointer; transition: background 0.15s, color 0.15s; color: #64748b; }
  .tab-btn.active { background: rgba(37,211,102,0.15); color: #25D366; }
  .tab-btn:hover:not(.active) { color: #e2e8f0; }

  /* Input */
  .glass-input {
    background: rgba(255,255,255,0.05); border: 1px solid rgba(255,255,255,0.1);
    border-radius: 10px; padding: 10px 14px; color: #e2e8f0; font-size: 14px;
    width: 100%; outline: none; transition: border-color 0.2s, background 0.2s;
    font-family: 'Inter', sans-serif;
  }
  .glass-input:focus { border-color: rgba(37,211,102,0.5); background: rgba(255,255,255,0.07); }
  .glass-input::placeholder { color: #475569; }

  /* Button */
  .btn-primary { background: #25D366; color: #0a0e1a; font-weight: 600; border-radius: 10px; padding: 10px 20px; font-size: 14px; cursor: pointer; transition: background 0.15s, transform 0.1s; border: none; }
  .btn-primary:hover { background: #1ebe5d; transform: translateY(-1px); }
  .btn-primary:active { transform: translateY(0); }
  .btn-ghost { background: rgba(255,255,255,0.06); color: #94a3b8; font-weight: 500; border-radius: 10px; padding: 10px 20px; font-size: 14px; cursor: pointer; transition: background 0.15s; border: 1px solid rgba(255,255,255,0.1); }
  .btn-ghost:hover { background: rgba(255,255,255,0.1); color: #e2e8f0; }
  .btn-danger { background: rgba(239,68,68,0.15); color: #f87171; font-weight: 500; border-radius: 8px; padding: 7px 14px; font-size: 13px; cursor: pointer; transition: background 0.15s; border: 1px solid rgba(239,68,68,0.2); }
  .btn-danger:hover { background: rgba(239,68,68,0.25); }

  /* Page */
  .page { display: none; }
  .page.active { display: block; }

  /* Scrollbar */
  ::-webkit-scrollbar { width: 6px; }
  ::-webkit-scrollbar-track { background: transparent; }
  ::-webkit-scrollbar-thumb { background: rgba(255,255,255,0.1); border-radius: 3px; }
  ::-webkit-scrollbar-thumb:hover { background: rgba(255,255,255,0.18); }

  /* Badge */
  .badge { display: inline-flex; align-items: center; gap: 5px; padding: 3px 10px; border-radius: 20px; font-size: 12px; font-weight: 500; }
  .badge-green { background: rgba(37,211,102,0.15); color: #25D366; }
  .badge-yellow { background: rgba(245,158,11,0.15); color: #f59e0b; }
  .badge-red { background: rgba(239,68,68,0.15); color: #ef4444; }
  .badge-blue { background: rgba(59,130,246,0.15); color: #60a5fa; }

  /* QR section */
  #qr-modal { display: none; position: fixed; inset: 0; background: rgba(0,0,0,0.7); z-index: 50; align-items: center; justify-content: center; backdrop-filter: blur(4px); }
  #qr-modal.open { display: flex; }
</style>
</head>
<body class="min-h-screen">

<!-- Background blobs -->
<div class="blob" style="width:600px;height:600px;background:#25D366;top:-200px;left:-200px;"></div>
<div class="blob" style="width:500px;height:500px;background:#3b82f6;bottom:-150px;right:-100px;"></div>

<!-- Layout -->
<div style="display:flex;min-height:100vh;position:relative;z-index:1;">

  <!-- Sidebar -->
  <aside style="width:240px;flex-shrink:0;padding:24px 16px;display:flex;flex-direction:column;gap:8px;border-right:1px solid rgba(255,255,255,0.06);position:sticky;top:0;height:100vh;overflow-y:auto;">
    <!-- Logo -->
    <div style="display:flex;align-items:center;gap:10px;padding:8px 14px;margin-bottom:16px;">
      <div style="width:34px;height:34px;background:#25D366;border-radius:10px;display:flex;align-items:center;justify-content:center;flex-shrink:0;">
        <svg width="18" height="18" fill="white" viewBox="0 0 24 24"><path d="M17.472 14.382c-.297-.149-1.758-.867-2.03-.967-.273-.099-.471-.148-.67.15-.197.297-.767.966-.94 1.164-.173.199-.347.223-.644.075-.297-.15-1.255-.463-2.39-1.475-.883-.788-1.48-1.761-1.653-2.059-.173-.297-.018-.458.13-.606.134-.133.298-.347.446-.52.149-.174.198-.298.298-.497.099-.198.05-.371-.025-.52-.075-.149-.669-1.612-.916-2.207-.242-.579-.487-.5-.669-.51-.173-.008-.371-.01-.57-.01-.198 0-.52.074-.792.372-.272.297-1.04 1.016-1.04 2.479 0 1.462 1.065 2.875 1.213 3.074.149.198 2.096 3.2 5.077 4.487.709.306 1.262.489 1.694.625.712.227 1.36.195 1.871.118.571-.085 1.758-.719 2.006-1.413.248-.694.248-1.289.173-1.413-.074-.124-.272-.198-.57-.347m-5.421 7.403h-.004a9.87 9.87 0 01-5.031-1.378l-.361-.214-3.741.982.998-3.648-.235-.374a9.86 9.86 0 01-1.51-5.26c.001-5.45 4.436-9.884 9.888-9.884 2.64 0 5.122 1.03 6.988 2.898a9.825 9.825 0 012.893 6.994c-.003 5.45-4.437 9.884-9.885 9.884m8.413-18.297A11.815 11.815 0 0012.05 0C5.495 0 .16 5.335.157 11.892c0 2.096.547 4.142 1.588 5.945L.057 24l6.305-1.654a11.882 11.882 0 005.683 1.448h.005c6.554 0 11.89-5.335 11.893-11.893a11.821 11.821 0 00-3.48-8.413z"/></svg>
      </div>
      <div>
        <div style="font-size:13px;font-weight:700;color:#e2e8f0;">WA Connector</div>
        <div style="font-size:11px;color:#475569;">UC Technology</div>
      </div>
    </div>

    <div style="font-size:11px;font-weight:600;color:#334155;text-transform:uppercase;letter-spacing:.08em;padding:0 14px;margin-bottom:4px;">Menu</div>

    <div class="sidebar-item active" onclick="showPage('dashboard')">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/></svg>
      Dashboard
    </div>
    <div class="sidebar-item" onclick="showPage('sessions')">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M17 21v-2a4 4 0 00-4-4H5a4 4 0 00-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 00-3-3.87M16 3.13a4 4 0 010 7.75"/></svg>
      Sessões WA
    </div>
    <div class="sidebar-item" onclick="showPage('reports')">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/></svg>
      Relatórios
    </div>
    <div class="sidebar-item" onclick="showPage('config')">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="3"/><path d="M19.07 4.93l-1.41 1.41M4.93 4.93l1.41 1.41M12 2v2M12 20v2M20 12h2M2 12h2M17.66 17.66l-1.41-1.41M6.34 17.66l1.41-1.41"/><path d="M12 8a4 4 0 100 8 4 4 0 000-8z"/></svg>
      Configurações
    </div>

    <div style="flex:1;"></div>

    <!-- Status indicator -->
    <div class="glass-card" style="padding:12px 14px;margin-top:8px;">
      <div style="font-size:11px;color:#64748b;margin-bottom:8px;font-weight:600;">STATUS DO SISTEMA</div>
      <div style="display:flex;align-items:center;gap:8px;margin-bottom:6px;">
        <div class="dot-green" id="sidebar-status-dot"></div>
        <span style="font-size:13px;color:#e2e8f0;" id="sidebar-status-text">Operacional</span>
      </div>
      <div style="font-size:11px;color:#475569;" id="sidebar-sessions-count">-- sessões ativas</div>
    </div>
  </aside>

  <!-- Main content -->
  <main style="flex:1;overflow-y:auto;padding:28px 32px;max-width:1200px;">

    <!-- ═══════════════════════════ DASHBOARD PAGE ═══════════════════════════ -->
    <div id="page-dashboard" class="page active">

      <!-- Header -->
      <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:28px;">
        <div>
          <h1 style="font-size:22px;font-weight:700;color:#f1f5f9;">Dashboard</h1>
          <p style="font-size:13px;color:#64748b;margin-top:2px;">Monitoramento em tempo real</p>
        </div>
        <div style="display:flex;align-items:center;gap:10px;">
          <div style="display:flex;align-items:center;gap:6px;background:rgba(37,211,102,0.1);border:1px solid rgba(37,211,102,0.2);border-radius:20px;padding:6px 12px;">
            <div class="dot-green" id="header-status-dot"></div>
            <span style="font-size:12px;color:#25D366;font-weight:500;" id="header-status-text">Conectado</span>
          </div>
          <button class="btn-ghost" onclick="refreshAll()" style="padding:8px 14px;font-size:13px;">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" style="display:inline;margin-right:6px;vertical-align:middle;"><path d="M1 4v6h6M23 20v-6h-6"/><path d="M20.49 9A9 9 0 005.64 5.64L1 10M23 14l-4.64 4.36A9 9 0 013.51 15"/></svg>
            Atualizar
          </button>
        </div>
      </div>

      <!-- Bento Grid — Métricas principais -->
      <div style="display:grid;grid-template-columns:repeat(4,1fr);gap:16px;margin-bottom:20px;">

        <div class="glass-card" style="padding:20px;">
          <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:14px;">
            <div style="width:38px;height:38px;background:rgba(37,211,102,0.15);border-radius:10px;display:flex;align-items:center;justify-content:center;">
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#25D366" stroke-width="2"><rect x="5" y="2" width="14" height="20" rx="2"/><line x1="12" y1="18" x2="12" y2="18"/></svg>
            </div>
            <span class="badge badge-green" id="badge-sessions">--</span>
          </div>
          <div class="metric-value" id="metric-sessions">--</div>
          <div class="metric-label" style="margin-top:4px;">Sessões Ativas</div>
        </div>

        <div class="glass-card" style="padding:20px;">
          <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:14px;">
            <div style="width:38px;height:38px;background:rgba(59,130,246,0.15);border-radius:10px;display:flex;align-items:center;justify-content:center;">
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#60a5fa" stroke-width="2"><path d="M21 15a2 2 0 01-2 2H7l-4 4V5a2 2 0 012-2h14a2 2 0 012 2z"/></svg>
            </div>
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="#60a5fa" stroke-width="2"><polyline points="23 6 13.5 15.5 8.5 10.5 1 18"/><polyline points="17 6 23 6 23 12"/></svg>
          </div>
          <div class="metric-value" style="color:#60a5fa;" id="metric-inbound">--</div>
          <div class="metric-label" style="margin-top:4px;">Msgs Recebidas</div>
        </div>

        <div class="glass-card" style="padding:20px;">
          <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:14px;">
            <div style="width:38px;height:38px;background:rgba(168,85,247,0.15);border-radius:10px;display:flex;align-items:center;justify-content:center;">
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#c084fc" stroke-width="2"><line x1="22" y1="2" x2="11" y2="13"/><polygon points="22 2 15 22 11 13 2 9 22 2"/></svg>
            </div>
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="#c084fc" stroke-width="2"><polyline points="23 6 13.5 15.5 8.5 10.5 1 18"/><polyline points="17 6 23 6 23 12"/></svg>
          </div>
          <div class="metric-value" style="color:#c084fc;" id="metric-outbound">--</div>
          <div class="metric-label" style="margin-top:4px;">Msgs Enviadas</div>
        </div>

        <div class="glass-card" style="padding:20px;">
          <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:14px;">
            <div style="width:38px;height:38px;background:rgba(239,68,68,0.15);border-radius:10px;display:flex;align-items:center;justify-content:center;">
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#f87171" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>
            </div>
            <span class="badge badge-red" id="badge-failed" style="display:none;">!</span>
          </div>
          <div class="metric-value" style="color:#f87171;" id="metric-failed">--</div>
          <div class="metric-label" style="margin-top:4px;">Falhas</div>
        </div>
      </div>

      <!-- Segunda linha: Fila + Atividade recente -->
      <div style="display:grid;grid-template-columns:1fr 1fr 1fr;gap:16px;margin-bottom:20px;">

        <div class="glass-card" style="padding:20px;">
          <div style="font-size:13px;font-weight:600;color:#94a3b8;margin-bottom:16px;display:flex;align-items:center;gap:8px;">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="8" y1="6" x2="21" y2="6"/><line x1="8" y1="12" x2="21" y2="12"/><line x1="8" y1="18" x2="21" y2="18"/><line x1="3" y1="6" x2="3.01" y2="6"/><line x1="3" y1="12" x2="3.01" y2="12"/><line x1="3" y1="18" x2="3.01" y2="18"/></svg>
            FILA REDIS
          </div>
          <div style="display:flex;flex-direction:column;gap:12px;">
            <div style="display:flex;justify-content:space-between;align-items:center;">
              <span style="font-size:13px;color:#64748b;">Inbound</span>
              <span style="font-size:15px;font-weight:600;color:#60a5fa;" id="q-inbound">--</span>
            </div>
            <div style="height:1px;background:rgba(255,255,255,0.06);"></div>
            <div style="display:flex;justify-content:space-between;align-items:center;">
              <span style="font-size:13px;color:#64748b;">Outbound</span>
              <span style="font-size:15px;font-weight:600;color:#c084fc;" id="q-outbound">--</span>
            </div>
            <div style="height:1px;background:rgba(255,255,255,0.06);"></div>
            <div style="display:flex;justify-content:space-between;align-items:center;">
              <span style="font-size:13px;color:#64748b;">Dead letter</span>
              <span style="font-size:15px;font-weight:600;color:#f87171;" id="q-dead">--</span>
            </div>
          </div>
        </div>

        <div class="glass-card" style="padding:20px;grid-column:span 2;">
          <div style="font-size:13px;font-weight:600;color:#94a3b8;margin-bottom:16px;display:flex;align-items:center;gap:8px;">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/></svg>
            VOLUME DE MENSAGENS — ÚLTIMAS 24H
          </div>
          <canvas id="chart-activity" height="80"></canvas>
        </div>
      </div>

      <!-- Sessões ativas -->
      <div class="glass-card" style="padding:20px;">
        <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:16px;">
          <div style="font-size:13px;font-weight:600;color:#94a3b8;display:flex;align-items:center;gap:8px;">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="5" y="2" width="14" height="20" rx="2"/></svg>
            DISPOSITIVOS CONECTADOS
          </div>
          <button class="btn-primary" onclick="showPage('sessions')" style="padding:7px 14px;font-size:13px;">Gerenciar</button>
        </div>
        <div id="dashboard-sessions-list">
          <div style="text-align:center;padding:24px;color:#475569;font-size:14px;">Carregando...</div>
        </div>
      </div>
    </div>

    <!-- ═══════════════════════════ SESSIONS PAGE ═══════════════════════════ -->
    <div id="page-sessions" class="page">
      <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:28px;">
        <div>
          <h1 style="font-size:22px;font-weight:700;color:#f1f5f9;">Sessões WhatsApp</h1>
          <p style="font-size:13px;color:#64748b;margin-top:2px;">Conecte e gerencie números</p>
        </div>
        <button class="btn-primary" onclick="document.getElementById('qr-modal').classList.add('open')">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" style="display:inline;margin-right:6px;vertical-align:middle;"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
          Nova Sessão
        </button>
      </div>

      <div id="sessions-list-page">
        <div style="text-align:center;padding:40px;color:#475569;">Carregando...</div>
      </div>
    </div>

    <!-- ═══════════════════════════ REPORTS PAGE ═══════════════════════════ -->
    <div id="page-reports" class="page">
      <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:28px;">
        <div>
          <h1 style="font-size:22px;font-weight:700;color:#f1f5f9;">Relatórios</h1>
          <p style="font-size:13px;color:#64748b;margin-top:2px;">Análise de atendimentos</p>
        </div>
        <div style="display:flex;gap:8px;">
          <button class="tab-btn active" onclick="setReportPeriod(7, this)">7 dias</button>
          <button class="tab-btn" onclick="setReportPeriod(14, this)">14 dias</button>
          <button class="tab-btn" onclick="setReportPeriod(30, this)">30 dias</button>
        </div>
      </div>

      <!-- Resumo -->
      <div style="display:grid;grid-template-columns:repeat(3,1fr);gap:16px;margin-bottom:20px;">
        <div class="glass-card" style="padding:20px;">
          <div style="font-size:12px;color:#64748b;font-weight:600;text-transform:uppercase;letter-spacing:.05em;margin-bottom:10px;">Total de mensagens</div>
          <div style="font-size:2rem;font-weight:700;" id="report-total">--</div>
        </div>
        <div class="glass-card" style="padding:20px;">
          <div style="font-size:12px;color:#64748b;font-weight:600;text-transform:uppercase;letter-spacing:.05em;margin-bottom:10px;">Recebidas (inbound)</div>
          <div style="font-size:2rem;font-weight:700;color:#60a5fa;" id="report-inbound">--</div>
        </div>
        <div class="glass-card" style="padding:20px;">
          <div style="font-size:12px;color:#64748b;font-weight:600;text-transform:uppercase;letter-spacing:.05em;margin-bottom:10px;">Enviadas (outbound)</div>
          <div style="font-size:2rem;font-weight:700;color:#c084fc;" id="report-outbound">--</div>
        </div>
      </div>

      <!-- Gráficos -->
      <div style="display:grid;grid-template-columns:2fr 1fr;gap:16px;margin-bottom:20px;">
        <div class="glass-card" style="padding:20px;">
          <div style="font-size:13px;font-weight:600;color:#94a3b8;margin-bottom:16px;">Volume diário</div>
          <canvas id="chart-daily" height="120"></canvas>
        </div>
        <div class="glass-card" style="padding:20px;">
          <div style="font-size:13px;font-weight:600;color:#94a3b8;margin-bottom:16px;">Distribuição</div>
          <canvas id="chart-dist" height="120"></canvas>
        </div>
      </div>

      <!-- Tabela de histórico -->
      <div class="glass-card" style="padding:20px;">
        <div style="font-size:13px;font-weight:600;color:#94a3b8;margin-bottom:16px;">Histórico por dia</div>
        <div style="overflow-x:auto;">
          <table style="width:100%;border-collapse:collapse;font-size:13px;">
            <thead>
              <tr style="color:#475569;text-align:left;">
                <th style="padding:8px 12px;border-bottom:1px solid rgba(255,255,255,0.06);font-weight:500;">Data</th>
                <th style="padding:8px 12px;border-bottom:1px solid rgba(255,255,255,0.06);font-weight:500;">Total</th>
                <th style="padding:8px 12px;border-bottom:1px solid rgba(255,255,255,0.06);font-weight:500;">Recebidas</th>
                <th style="padding:8px 12px;border-bottom:1px solid rgba(255,255,255,0.06);font-weight:500;">Enviadas</th>
              </tr>
            </thead>
            <tbody id="report-table-body">
              <tr><td colspan="4" style="padding:24px;text-align:center;color:#475569;">Carregando...</td></tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>

    <!-- ═══════════════════════════ CONFIG PAGE ═══════════════════════════ -->
    <div id="page-config" class="page">
      <div style="margin-bottom:28px;">
        <h1 style="font-size:22px;font-weight:700;color:#f1f5f9;">Configurações</h1>
        <p style="font-size:13px;color:#64748b;margin-top:2px;">Informações do sistema e integrações</p>
      </div>

      <div style="display:grid;grid-template-columns:1fr 1fr;gap:20px;">

        <!-- Bitrix24 -->
        <div class="glass-card" style="padding:24px;">
          <div style="display:flex;align-items:center;gap:10px;margin-bottom:20px;">
            <div style="width:34px;height:34px;background:rgba(59,130,246,0.15);border-radius:10px;display:flex;align-items:center;justify-content:center;">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="#60a5fa" stroke-width="2"><path d="M21 16V8a2 2 0 00-1-1.73l-7-4a2 2 0 00-2 0l-7 4A2 2 0 003 8v8a2 2 0 001 1.73l7 4a2 2 0 002 0l7-4A2 2 0 0021 16z"/></svg>
            </div>
            <div>
              <div style="font-size:14px;font-weight:600;color:#e2e8f0;">Bitrix24</div>
              <div style="font-size:12px;color:#475569;">Integração CRM</div>
            </div>
          </div>
          <div style="display:flex;flex-direction:column;gap:14px;">
            <div>
              <label style="font-size:12px;color:#64748b;font-weight:500;display:block;margin-bottom:6px;">DOMÍNIO</label>
              <div class="glass-input" style="cursor:default;color:#94a3b8;" id="cfg-bitrix-domain">--</div>
            </div>
            <div>
              <label style="font-size:12px;color:#64748b;font-weight:500;display:block;margin-bottom:6px;">OPEN LINE ID</label>
              <div class="glass-input" style="cursor:default;color:#94a3b8;" id="cfg-line-id">--</div>
            </div>
            <div>
              <label style="font-size:12px;color:#64748b;font-weight:500;display:block;margin-bottom:6px;">CONNECTOR ID</label>
              <div class="glass-input" style="cursor:default;color:#94a3b8;">whatsapp_uc</div>
            </div>
            <div>
              <label style="font-size:12px;color:#64748b;font-weight:500;display:block;margin-bottom:6px;">STATUS DO TOKEN</label>
              <div style="display:flex;align-items:center;gap:8px;padding:10px 14px;background:rgba(37,211,102,0.08);border:1px solid rgba(37,211,102,0.15);border-radius:10px;">
                <div class="dot-green"></div>
                <span style="font-size:13px;color:#25D366;">Token ativo — renovação automática</span>
              </div>
            </div>
          </div>
        </div>

        <!-- WhatsApp -->
        <div class="glass-card" style="padding:24px;">
          <div style="display:flex;align-items:center;gap:10px;margin-bottom:20px;">
            <div style="width:34px;height:34px;background:rgba(37,211,102,0.15);border-radius:10px;display:flex;align-items:center;justify-content:center;">
              <svg width="16" height="16" fill="#25D366" viewBox="0 0 24 24"><path d="M17.472 14.382c-.297-.149-1.758-.867-2.03-.967-.273-.099-.471-.148-.67.15-.197.297-.767.966-.94 1.164-.173.199-.347.223-.644.075-.297-.15-1.255-.463-2.39-1.475-.883-.788-1.48-1.761-1.653-2.059-.173-.297-.018-.458.13-.606.134-.133.298-.347.446-.52.149-.174.198-.298.298-.497.099-.198.05-.371-.025-.52-.075-.149-.669-1.612-.916-2.207-.242-.579-.487-.5-.669-.51-.173-.008-.371-.01-.57-.01-.198 0-.52.074-.792.372-.272.297-1.04 1.016-1.04 2.479 0 1.462 1.065 2.875 1.213 3.074.149.198 2.096 3.2 5.077 4.487.709.306 1.262.489 1.694.625.712.227 1.36.195 1.871.118.571-.085 1.758-.719 2.006-1.413.248-.694.248-1.289.173-1.413-.074-.124-.272-.198-.57-.347m-5.421 7.403h-.004a9.87 9.87 0 01-5.031-1.378l-.361-.214-3.741.982.998-3.648-.235-.374a9.86 9.86 0 01-1.51-5.26c.001-5.45 4.436-9.884 9.888-9.884 2.64 0 5.122 1.03 6.988 2.898a9.825 9.825 0 012.893 6.994c-.003 5.45-4.437 9.884-9.885 9.884m8.413-18.297A11.815 11.815 0 0012.05 0C5.495 0 .16 5.335.157 11.892c0 2.096.547 4.142 1.588 5.945L.057 24l6.305-1.654a11.882 11.882 0 005.683 1.448h.005c6.554 0 11.89-5.335 11.893-11.893a11.821 11.821 0 00-3.48-8.413z"/></svg>
            </div>
            <div>
              <div style="font-size:14px;font-weight:600;color:#e2e8f0;">WhatsApp</div>
              <div style="font-size:12px;color:#475569;">Sessões e conexão</div>
            </div>
          </div>
          <div style="display:flex;flex-direction:column;gap:14px;">
            <div>
              <label style="font-size:12px;color:#64748b;font-weight:500;display:block;margin-bottom:6px;">SESSÕES ATIVAS</label>
              <div class="glass-input" style="cursor:default;color:#94a3b8;" id="cfg-sessions">--</div>
            </div>
            <div>
              <label style="font-size:12px;color:#64748b;font-weight:500;display:block;margin-bottom:6px;">DIRETÓRIO DE SESSÕES</label>
              <div class="glass-input" style="cursor:default;color:#94a3b8;">./sessions</div>
            </div>
            <div>
              <label style="font-size:12px;color:#64748b;font-weight:500;display:block;margin-bottom:6px;">WATCHDOG</label>
              <div style="display:flex;align-items:center;gap:8px;padding:10px 14px;background:rgba(37,211,102,0.08);border:1px solid rgba(37,211,102,0.15);border-radius:10px;">
                <div class="dot-green"></div>
                <span style="font-size:13px;color:#25D366;">Ativo — reconexão automática 30s</span>
              </div>
            </div>
          </div>
        </div>

        <!-- Workers / Filas -->
        <div class="glass-card" style="padding:24px;">
          <div style="display:flex;align-items:center;gap:10px;margin-bottom:20px;">
            <div style="width:34px;height:34px;background:rgba(168,85,247,0.15);border-radius:10px;display:flex;align-items:center;justify-content:center;">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="#c084fc" stroke-width="2"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/></svg>
            </div>
            <div>
              <div style="font-size:14px;font-weight:600;color:#e2e8f0;">Workers & Filas</div>
              <div style="font-size:12px;color:#475569;">Redis queue pool</div>
            </div>
          </div>
          <div style="display:flex;flex-direction:column;gap:12px;">
            <div style="display:flex;justify-content:space-between;align-items:center;padding:10px 0;border-bottom:1px solid rgba(255,255,255,0.06);">
              <span style="font-size:13px;color:#64748b;">Workers paralelos</span>
              <span style="font-size:14px;font-weight:600;color:#c084fc;">20</span>
            </div>
            <div style="display:flex;justify-content:space-between;align-items:center;padding:10px 0;border-bottom:1px solid rgba(255,255,255,0.06);">
              <span style="font-size:13px;color:#64748b;">Max retry</span>
              <span style="font-size:14px;font-weight:600;color:#c084fc;">5</span>
            </div>
            <div style="display:flex;justify-content:space-between;align-items:center;padding:10px 0;border-bottom:1px solid rgba(255,255,255,0.06);">
              <span style="font-size:13px;color:#64748b;">Delay base retry</span>
              <span style="font-size:14px;font-weight:600;color:#c084fc;">1s</span>
            </div>
            <div style="display:flex;justify-content:space-between;align-items:center;padding:10px 0;">
              <span style="font-size:13px;color:#64748b;">Typing delay</span>
              <span style="font-size:14px;font-weight:600;color:#c084fc;">1.5–4s</span>
            </div>
          </div>
        </div>

        <!-- Links rápidos -->
        <div class="glass-card" style="padding:24px;">
          <div style="display:flex;align-items:center;gap:10px;margin-bottom:20px;">
            <div style="width:34px;height:34px;background:rgba(245,158,11,0.15);border-radius:10px;display:flex;align-items:center;justify-content:center;">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="#f59e0b" stroke-width="2"><path d="M18 13v6a2 2 0 01-2 2H5a2 2 0 01-2-2V8a2 2 0 012-2h6"/><polyline points="15 3 21 3 21 9"/><line x1="10" y1="14" x2="21" y2="3"/></svg>
            </div>
            <div>
              <div style="font-size:14px;font-weight:600;color:#e2e8f0;">Links Rápidos</div>
              <div style="font-size:12px;color:#475569;">Acesso direto</div>
            </div>
          </div>
          <div style="display:flex;flex-direction:column;gap:10px;">
            <a href="/health" target="_blank" style="display:flex;align-items:center;justify-content:space-between;padding:10px 14px;background:rgba(255,255,255,0.04);border:1px solid rgba(255,255,255,0.07);border-radius:8px;text-decoration:none;transition:background .15s;cursor:pointer;" onmouseover="this.style.background='rgba(255,255,255,0.08)'" onmouseout="this.style.background='rgba(255,255,255,0.04)'">
              <span style="font-size:13px;color:#e2e8f0;">Health Check</span>
              <span style="font-size:11px;color:#475569;">/health</span>
            </a>
            <a href="/metrics" target="_blank" style="display:flex;align-items:center;justify-content:space-between;padding:10px 14px;background:rgba(255,255,255,0.04);border:1px solid rgba(255,255,255,0.07);border-radius:8px;text-decoration:none;transition:background .15s;" onmouseover="this.style.background='rgba(255,255,255,0.08)'" onmouseout="this.style.background='rgba(255,255,255,0.04)'">
              <span style="font-size:13px;color:#e2e8f0;">Métricas Prometheus</span>
              <span style="font-size:11px;color:#475569;">/metrics</span>
            </a>
            <a href="/connect" target="_blank" style="display:flex;align-items:center;justify-content:space-between;padding:10px 14px;background:rgba(255,255,255,0.04);border:1px solid rgba(255,255,255,0.07);border-radius:8px;text-decoration:none;transition:background .15s;" onmouseover="this.style.background='rgba(255,255,255,0.08)'" onmouseout="this.style.background='rgba(255,255,255,0.04)'">
              <span style="font-size:13px;color:#e2e8f0;">Conectar WhatsApp</span>
              <span style="font-size:11px;color:#475569;">/connect</span>
            </a>
          </div>
        </div>
      </div>
    </div>

  </main>
</div>

<!-- QR Modal -->
<div id="qr-modal" onclick="if(event.target===this)closeQR()">
  <div class="glass-card" style="padding:32px;max-width:420px;width:100%;margin:20px;position:relative;">
    <button onclick="closeQR()" style="position:absolute;top:16px;right:16px;background:none;border:none;color:#64748b;cursor:pointer;padding:4px;">
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
    </button>
    <h2 style="font-size:18px;font-weight:700;color:#f1f5f9;margin-bottom:6px;">Nova Sessão WhatsApp</h2>
    <p style="font-size:13px;color:#64748b;margin-bottom:20px;">Digite o número e escaneie o QR code</p>

    <div style="display:flex;gap:10px;margin-bottom:20px;">
      <input class="glass-input" type="text" id="modal-phone" placeholder="5519910001772" maxlength="20" style="flex:1;" onkeydown="if(event.key==='Enter')modalStartSession()"/>
      <button class="btn-primary" id="modal-btn" onclick="modalStartSession()">Conectar</button>
    </div>

    <div id="modal-qr-section" style="display:none;text-align:center;">
      <div style="margin-bottom:12px;" id="modal-badge"></div>
      <div style="background:rgba(255,255,255,0.06);border:1px solid rgba(255,255,255,0.1);border-radius:12px;padding:12px;display:inline-block;margin-bottom:8px;">
        <img id="modal-qr-img" src="" width="220" height="220" style="display:none;border-radius:6px;"/>
        <div id="modal-qr-placeholder" style="width:220px;height:220px;display:flex;align-items:center;justify-content:center;color:#475569;font-size:13px;">Aguardando QR...</div>
      </div>
      <div style="font-size:12px;color:#64748b;" id="modal-timer"></div>
    </div>

    <div style="background:rgba(255,255,255,0.03);border-radius:10px;padding:14px;margin-top:16px;font-size:12px;color:#64748b;line-height:1.8;">
      <strong style="color:#94a3b8;">Como escanear:</strong><br>
      1. Abra o WhatsApp no celular<br>
      2. Toque em <strong style="color:#94a3b8;">⋮ → Aparelhos conectados</strong><br>
      3. Toque em <strong style="color:#94a3b8;">Conectar um aparelho</strong><br>
      4. Aponte a câmera para o QR acima
    </div>
  </div>
</div>

<script>
// ─── State ───────────────────────────────────────────────────────────────────
var currentPage = 'dashboard';
var reportPeriod = 7;
var chartActivity = null;
var chartDaily = null;
var chartDist = null;
var qrInterval = null;
var qrTimer = null;
var qrCountdown = 0;
var modalPhone = '';

// ─── Navigation ──────────────────────────────────────────────────────────────
function showPage(name) {
  document.querySelectorAll('.page').forEach(function(el) { el.classList.remove('active'); });
  document.querySelectorAll('.sidebar-item').forEach(function(el) { el.classList.remove('active'); });
  document.getElementById('page-' + name).classList.add('active');
  var items = document.querySelectorAll('.sidebar-item');
  var pageMap = ['dashboard','sessions','reports','config'];
  var idx = pageMap.indexOf(name);
  if (idx >= 0) items[idx].classList.add('active');
  currentPage = name;
  if (name === 'reports') loadReports(reportPeriod);
  if (name === 'sessions') loadSessionsPage();
  if (name === 'config') loadConfig();
}

// ─── Overview (dashboard data) ────────────────────────────────────────────────
function loadOverview() {
  fetch('/ui/overview')
  .then(function(r) { return r.json(); })
  .then(function(d) {
    // Metrics
    set('metric-sessions', d.active_sessions);
    set('metric-inbound', d.messages_inbound || 0);
    set('metric-outbound', d.messages_outbound || 0);
    set('metric-failed', d.messages_failed || 0);
    set('q-inbound', d.queue_inbound);
    set('q-outbound', d.queue_outbound);
    set('q-dead', d.queue_dead);

    // Badge sessions
    var bs = document.getElementById('badge-sessions');
    bs.textContent = d.active_sessions + ' ativas';
    bs.className = 'badge ' + (d.active_sessions > 0 ? 'badge-green' : 'badge-red');

    // Failed badge
    var bf = document.getElementById('badge-failed');
    if ((d.messages_failed || 0) > 0) {
      bf.style.display = 'inline-flex';
      bf.textContent = d.messages_failed;
    }

    // Status indicator
    var online = d.active_sessions > 0;
    var statusDot = document.getElementById('header-status-dot');
    var statusText = document.getElementById('header-status-text');
    var sidebarDot = document.getElementById('sidebar-status-dot');
    var sidebarText = document.getElementById('sidebar-status-text');
    statusDot.className = online ? 'dot-green' : 'dot-red';
    statusText.textContent = online ? 'Conectado' : 'Desconectado';
    statusText.style.color = online ? '#25D366' : '#f87171';
    sidebarDot.className = online ? 'dot-green' : 'dot-red';
    sidebarText.textContent = online ? 'Operacional' : 'Sem sessão';
    set('sidebar-sessions-count', d.active_sessions + ' sessão' + (d.active_sessions !== 1 ? 'ões' : '') + ' ativa' + (d.active_sessions !== 1 ? 's' : ''));

    // Config page
    set('cfg-sessions', d.active_sessions + ' sessão' + (d.active_sessions !== 1 ? 'ões' : '') + ' ativa' + (d.active_sessions !== 1 ? 's' : ''));

    // Dashboard sessions list
    renderDashboardSessions(d.sessions || []);

    // Activity chart (simulated based on counters)
    updateActivityChart(d.messages_inbound || 0, d.messages_outbound || 0);
  })
  .catch(function() {});
}

function renderDashboardSessions(sessions) {
  var wrap = document.getElementById('dashboard-sessions-list');
  if (sessions.length === 0) {
    wrap.innerHTML = '<div style="text-align:center;padding:24px;color:#475569;font-size:14px;">Nenhum dispositivo conectado — <a href="#" onclick="showPage(\'sessions\');return false;" style="color:#25D366;">conectar agora</a></div>';
    return;
  }
  var html = '<div style="display:flex;flex-direction:column;gap:8px;">';
  sessions.forEach(function(jid) {
    var phone = jid.split(':')[0].split('@')[0];
    html += '<div style="display:flex;align-items:center;justify-content:space-between;padding:12px 16px;background:rgba(255,255,255,0.03);border:1px solid rgba(255,255,255,0.06);border-radius:10px;">'
      + '<div style="display:flex;align-items:center;gap:10px;">'
      + '<div class="dot-green"></div>'
      + '<div>'
      + '<div style="font-size:13px;font-weight:500;color:#e2e8f0;">+' + phone + '</div>'
      + '<div style="font-size:11px;color:#475569;margin-top:2px;">' + jid + '</div>'
      + '</div>'
      + '</div>'
      + '<span class="badge badge-green">Online</span>'
      + '</div>';
  });
  html += '</div>';
  wrap.innerHTML = html;
}

// ─── Activity Chart ────────────────────────────────────────────────────────────
function updateActivityChart(inbound, outbound) {
  var ctx = document.getElementById('chart-activity');
  if (!ctx) return;
  var labels = [];
  var now = new Date();
  for (var i = 23; i >= 0; i--) {
    var h = new Date(now - i * 3600000);
    labels.push(h.getHours() + 'h');
  }
  // Distribui os valores ao longo do dia (simplificado)
  var inData = distributeOver24(inbound);
  var outData = distributeOver24(outbound);

  if (chartActivity) chartActivity.destroy();
  chartActivity = new Chart(ctx, {
    type: 'line',
    data: {
      labels: labels,
      datasets: [
        { label: 'Recebidas', data: inData, borderColor: '#60a5fa', backgroundColor: 'rgba(96,165,250,0.08)', fill: true, tension: 0.4, pointRadius: 0, borderWidth: 2 },
        { label: 'Enviadas', data: outData, borderColor: '#c084fc', backgroundColor: 'rgba(192,132,252,0.08)', fill: true, tension: 0.4, pointRadius: 0, borderWidth: 2 }
      ]
    },
    options: {
      responsive: true, maintainAspectRatio: true,
      plugins: { legend: { labels: { color: '#64748b', font: { size: 11 }, boxWidth: 10 } } },
      scales: {
        x: { grid: { color: 'rgba(255,255,255,0.04)' }, ticks: { color: '#475569', font: { size: 10 }, maxTicksLimit: 8 } },
        y: { grid: { color: 'rgba(255,255,255,0.04)' }, ticks: { color: '#475569', font: { size: 10 } }, beginAtZero: true }
      }
    }
  });
}

function distributeOver24(total) {
  var data = new Array(24).fill(0);
  if (total <= 0) return data;
  // Distribui com pico no horário comercial
  var weights = [0,0,0,0,0,0,1,2,4,6,7,8,7,6,6,7,6,5,4,3,2,1,0,0];
  var sum = weights.reduce(function(a,b){return a+b;},0);
  for (var i = 0; i < 24; i++) {
    data[i] = Math.round(total * weights[i] / sum);
  }
  return data;
}

// ─── Sessions Page ─────────────────────────────────────────────────────────────
function loadSessionsPage() {
  fetch('/ui/sessions')
  .then(function(r) { return r.json(); })
  .then(function(d) {
    var wrap = document.getElementById('sessions-list-page');
    if (!d.sessions || d.sessions.length === 0) {
      wrap.innerHTML = '<div class="glass-card" style="padding:40px;text-align:center;">'
        + '<svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="#334155" stroke-width="1.5" style="margin:0 auto 16px;display:block;"><rect x="5" y="2" width="14" height="20" rx="2"/><line x1="12" y1="18" x2="12" y2="18"/></svg>'
        + '<p style="color:#475569;font-size:14px;margin-bottom:16px;">Nenhum número conectado ainda</p>'
        + '<button class="btn-primary" onclick="document.getElementById(\'qr-modal\').classList.add(\'open\')">Conectar primeiro número</button>'
        + '</div>';
      return;
    }
    var html = '<div style="display:flex;flex-direction:column;gap:12px;">';
    d.sessions.forEach(function(jid) {
      var phone = jid.split(':')[0].split('@')[0];
      var enc = encodeURIComponent(jid);
      html += '<div class="glass-card" style="padding:20px;display:flex;align-items:center;justify-content:space-between;">'
        + '<div style="display:flex;align-items:center;gap:14px;">'
        + '<div style="width:44px;height:44px;background:rgba(37,211,102,0.12);border-radius:12px;display:flex;align-items:center;justify-content:center;">'
        + '<svg width="20" height="20" fill="#25D366" viewBox="0 0 24 24"><path d="M17.472 14.382c-.297-.149-1.758-.867-2.03-.967-.273-.099-.471-.148-.67.15-.197.297-.767.966-.94 1.164-.173.199-.347.223-.644.075-.297-.15-1.255-.463-2.39-1.475-.883-.788-1.48-1.761-1.653-2.059-.173-.297-.018-.458.13-.606.134-.133.298-.347.446-.52.149-.174.198-.298.298-.497.099-.198.05-.371-.025-.52-.075-.149-.669-1.612-.916-2.207-.242-.579-.487-.5-.669-.51-.173-.008-.371-.01-.57-.01-.198 0-.52.074-.792.372-.272.297-1.04 1.016-1.04 2.479 0 1.462 1.065 2.875 1.213 3.074.149.198 2.096 3.2 5.077 4.487.709.306 1.262.489 1.694.625.712.227 1.36.195 1.871.118.571-.085 1.758-.719 2.006-1.413.248-.694.248-1.289.173-1.413-.074-.124-.272-.198-.57-.347m-5.421 7.403h-.004a9.87 9.87 0 01-5.031-1.378l-.361-.214-3.741.982.998-3.648-.235-.374a9.86 9.86 0 01-1.51-5.26c.001-5.45 4.436-9.884 9.888-9.884 2.64 0 5.122 1.03 6.988 2.898a9.825 9.825 0 012.893 6.994c-.003 5.45-4.437 9.884-9.885 9.884m8.413-18.297A11.815 11.815 0 0012.05 0C5.495 0 .16 5.335.157 11.892c0 2.096.547 4.142 1.588 5.945L.057 24l6.305-1.654a11.882 11.882 0 005.683 1.448h.005c6.554 0 11.89-5.335 11.893-11.893a11.821 11.821 0 00-3.48-8.413z"/></svg>'
        + '</div>'
        + '<div>'
        + '<div style="font-size:15px;font-weight:600;color:#e2e8f0;">+' + phone + '</div>'
        + '<div style="font-size:12px;color:#475569;margin-top:3px;">' + jid + '</div>'
        + '</div>'
        + '</div>'
        + '<div style="display:flex;align-items:center;gap:10px;">'
        + '<span class="badge badge-green">Conectado</span>'
        + '<button class="btn-danger" onclick="disconnectSession(\'' + enc + '\')">Desconectar</button>'
        + '</div>'
        + '</div>';
    });
    html += '</div>';
    wrap.innerHTML = html;
  }).catch(function() {});
}

function disconnectSession(enc) {
  if (!confirm('Desconectar este número?')) return;
  fetch('/ui/sessions/' + enc, { method: 'DELETE' })
  .then(function() { loadSessionsPage(); loadOverview(); })
  .catch(function() {});
}

// ─── QR Modal ─────────────────────────────────────────────────────────────────
function modalStartSession() {
  var raw = document.getElementById('modal-phone').value.trim().replace(/\D/g, '');
  if (!raw || raw.length < 10) { alert('Digite um número válido'); return; }
  modalPhone = raw;
  var btn = document.getElementById('modal-btn');
  btn.disabled = true; btn.textContent = 'Conectando...';

  fetch('/ui/sessions', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ phone: raw }) })
  .then(function(r) { return r.json(); })
  .then(function(d) {
    if (d.error) { alert(d.error); btn.disabled = false; btn.textContent = 'Conectar'; return; }
    document.getElementById('modal-qr-section').style.display = 'block';
    startQRPoll(raw);
  }).catch(function(e) { alert('Erro: ' + e); btn.disabled = false; btn.textContent = 'Conectar'; });
}

function startQRPoll(phone) {
  if (qrInterval) clearInterval(qrInterval);
  doQRPoll(phone);
  qrInterval = setInterval(function() { doQRPoll(phone); }, 2000);
}

function doQRPoll(phone) {
  fetch('/ui/sessions/' + phone + '/qr')
  .then(function(r) { return r.json(); })
  .then(function(d) {
    if (d.status === 'connected') {
      stopQRPoll();
      setModalBadge('green', 'Conectado com sucesso!');
      document.getElementById('modal-qr-img').style.display = 'none';
      document.getElementById('modal-qr-placeholder').innerHTML = '<div style="font-size:40px;">✓</div><div style="color:#25D366;font-size:13px;margin-top:8px;">Conectado!</div>';
      document.getElementById('modal-qr-placeholder').style.display = 'flex';
      document.getElementById('modal-qr-placeholder').style.flexDirection = 'column';
      document.getElementById('modal-qr-placeholder').style.alignItems = 'center';
      document.getElementById('modal-qr-placeholder').style.justifyContent = 'center';
      setTimeout(function() { closeQR(); loadSessionsPage(); loadOverview(); }, 2000);
    } else if (d.status === 'ready' && d.qr) {
      var img = document.getElementById('modal-qr-img');
      img.src = 'https://api.qrserver.com/v1/create-qr-code/?size=220x220&ecc=L&data=' + encodeURIComponent(d.qr);
      img.style.display = 'block';
      document.getElementById('modal-qr-placeholder').style.display = 'none';
      setModalBadge('blue', 'Escaneie o QR code');
      qrCountdown = 25;
      if (qrTimer) clearInterval(qrTimer);
      qrTimer = setInterval(function() {
        qrCountdown--;
        var t = document.getElementById('modal-timer');
        if (t) t.textContent = 'QR expira em ' + qrCountdown + 's';
        if (qrCountdown <= 0) { clearInterval(qrTimer); if (t) t.textContent = 'Atualizando...'; }
      }, 1000);
    } else {
      setModalBadge('yellow', 'Aguardando QR code...');
    }
  }).catch(function() {});
}

function setModalBadge(color, text) {
  var el = document.getElementById('modal-badge');
  el.className = 'badge badge-' + color;
  el.textContent = text;
}

function stopQRPoll() {
  if (qrInterval) { clearInterval(qrInterval); qrInterval = null; }
  if (qrTimer) { clearInterval(qrTimer); qrTimer = null; }
}

function closeQR() {
  stopQRPoll();
  document.getElementById('qr-modal').classList.remove('open');
  document.getElementById('modal-phone').value = '';
  document.getElementById('modal-qr-section').style.display = 'none';
  document.getElementById('modal-btn').disabled = false;
  document.getElementById('modal-btn').textContent = 'Conectar';
  document.getElementById('modal-qr-img').style.display = 'none';
  document.getElementById('modal-qr-placeholder').style.display = 'flex';
  document.getElementById('modal-qr-placeholder').textContent = 'Aguardando QR...';
  document.getElementById('modal-timer').textContent = '';
  document.getElementById('modal-badge').textContent = '';
}

// ─── Reports ─────────────────────────────────────────────────────────────────
function setReportPeriod(days, btn) {
  reportPeriod = days;
  document.querySelectorAll('.tab-btn').forEach(function(b) { b.classList.remove('active'); });
  btn.classList.add('active');
  loadReports(days);
}

function loadReports(days) {
  fetch('/stats/daily?days=' + days)
  .then(function(r) { return r.json(); })
  .then(function(data) {
    if (!Array.isArray(data)) { data = []; }
    var totalIn = 0, totalOut = 0, totalAll = 0;
    data.forEach(function(row) {
      totalIn += row.inbound_count || 0;
      totalOut += row.outbound_count || 0;
      totalAll += row.total_messages || 0;
    });
    set('report-total', totalAll);
    set('report-inbound', totalIn);
    set('report-outbound', totalOut);

    // Table
    var tbody = document.getElementById('report-table-body');
    if (data.length === 0) {
      tbody.innerHTML = '<tr><td colspan="4" style="padding:24px;text-align:center;color:#475569;">Sem dados no período</td></tr>';
    } else {
      tbody.innerHTML = data.map(function(row) {
        var d = new Date(row.date);
        var dateStr = d.toLocaleDateString('pt-BR');
        return '<tr style="border-bottom:1px solid rgba(255,255,255,0.04);">'
          + '<td style="padding:10px 12px;color:#94a3b8;">' + dateStr + '</td>'
          + '<td style="padding:10px 12px;color:#e2e8f0;font-weight:500;">' + (row.total_messages || 0) + '</td>'
          + '<td style="padding:10px 12px;color:#60a5fa;">' + (row.inbound_count || 0) + '</td>'
          + '<td style="padding:10px 12px;color:#c084fc;">' + (row.outbound_count || 0) + '</td>'
          + '</tr>';
      }).join('');
    }

    // Charts
    var labels = data.map(function(r) { return new Date(r.date).toLocaleDateString('pt-BR', {day:'2-digit',month:'2-digit'}); }).reverse();
    var inData = data.map(function(r) { return r.inbound_count || 0; }).reverse();
    var outData = data.map(function(r) { return r.outbound_count || 0; }).reverse();

    var ctxD = document.getElementById('chart-daily');
    if (chartDaily) chartDaily.destroy();
    chartDaily = new Chart(ctxD, {
      type: 'bar',
      data: {
        labels: labels,
        datasets: [
          { label: 'Recebidas', data: inData, backgroundColor: 'rgba(96,165,250,0.7)', borderRadius: 4 },
          { label: 'Enviadas', data: outData, backgroundColor: 'rgba(192,132,252,0.7)', borderRadius: 4 }
        ]
      },
      options: {
        responsive: true, maintainAspectRatio: true,
        plugins: { legend: { labels: { color: '#64748b', font: { size: 11 }, boxWidth: 10 } } },
        scales: {
          x: { grid: { display: false }, ticks: { color: '#475569', font: { size: 10 } } },
          y: { grid: { color: 'rgba(255,255,255,0.04)' }, ticks: { color: '#475569', font: { size: 10 } }, beginAtZero: true }
        }
      }
    });

    var ctxPie = document.getElementById('chart-dist');
    if (chartDist) chartDist.destroy();
    chartDist = new Chart(ctxPie, {
      type: 'doughnut',
      data: {
        labels: ['Recebidas', 'Enviadas'],
        datasets: [{ data: [totalIn || 1, totalOut || 1], backgroundColor: ['rgba(96,165,250,0.8)', 'rgba(192,132,252,0.8)'], borderWidth: 0, hoverOffset: 4 }]
      },
      options: {
        responsive: true, maintainAspectRatio: true,
        cutout: '65%',
        plugins: { legend: { position: 'bottom', labels: { color: '#64748b', font: { size: 11 }, boxWidth: 10, padding: 16 } } }
      }
    });
  }).catch(function() {});
}

// ─── Config ───────────────────────────────────────────────────────────────────
function loadConfig() {
  fetch('/health')
  .then(function(r) { return r.json(); })
  .then(function(d) {
    set('cfg-bitrix-domain', 'uctdemo.bitrix24.com');
    set('cfg-line-id', '218');
    set('cfg-sessions', d.active_sessions + ' sessão' + (d.active_sessions !== 1 ? 'ões' : '') + ' ativa' + (d.active_sessions !== 1 ? 's' : ''));
  }).catch(function() {});
}

// ─── Helpers ──────────────────────────────────────────────────────────────────
function set(id, val) {
  var el = document.getElementById(id);
  if (el) el.textContent = val;
}

function refreshAll() {
  loadOverview();
  if (currentPage === 'sessions') loadSessionsPage();
  if (currentPage === 'reports') loadReports(reportPeriod);
}

// ─── Init ─────────────────────────────────────────────────────────────────────
loadOverview();
setInterval(loadOverview, 10000); // refresh a cada 10s
</script>
</body>
</html>`
