package api

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/bitrix"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/config"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/queue"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/telemetry"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/whatsapp"
	"go.uber.org/zap"
)

func New(
	cfg *config.Config,
	repo *db.Repository,
	waManager *whatsapp.Manager,
	cloudMgr *whatsapp.CloudManager,
	bitrixClient *bitrix.Client,
	q *queue.Queue,
	metrics *telemetry.Metrics,
	log *zap.Logger,
) *fiber.App {

	app := fiber.New(fiber.Config{
		AppName:      "WhatsApp-Bitrix24 Connector",
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
		ErrorHandler: jsonErrorHandler,
	})

	app.Use(recover.New())
	app.Use(cors.New())
	app.Use(logger.New(logger.Config{
		Format: "[${time}] ${status} ${method} ${path} ${latency}\n",
	}))

	h := newHandlers(cfg, repo, waManager, cloudMgr, bitrixClient, q, metrics, log)

	// ─── Assets estáticos ────────────────────────────────────────────────
	app.Get("/assets/chart.js", h.serveChartJS)
	app.Get("/assets/logo.png", h.serveLogo)
	app.Get("/favicon.ico", h.serveFavicon)
	app.Get("/favicon.png", h.serveFavicon)

	// ─── Raiz → Dashboard ───────────────────────────────────────────────
	app.Get("/", func(c *fiber.Ctx) error {
		return c.Redirect("/dashboard", fiber.StatusFound)
	})

	// ─── Health ──────────────────────────────────────────────────────────
	app.Get("/health", h.health)

	// ─── UI de conexão WhatsApp (sem auth — seguro pois usa proxy interno) ─
	app.Get("/connect", h.connectPage)
	app.Get("/dashboard", h.dashboardPage)
	ui := app.Group("/ui")
	ui.Post("/sessions", h.uiStartSession)
	ui.Get("/sessions/:phone/qr", h.uiGetQR)
	ui.Get("/sessions", h.uiListSessions)
	ui.Delete("/sessions/remove", h.uiDisconnectSession) // jid via query param ?jid=
	ui.Delete("/sessions/:jid", h.uiDisconnectSession)   // fallback legado
	ui.Post("/sessions/refresh-status", h.uiRefreshSessionsStatus)
	ui.Get("/overview", h.uiOverview)
	// ─── Sessões Cloud API (Meta Oficial) ────────────────────────────────
	ui.Post("/sessions/cloud", h.uiCreateCloudSession)
	ui.Get("/sessions/cloud/:session_id/webhook-info", h.uiCloudWebhookInfo)
	// ─── Bitrix Accounts (multi-tenant) ──────────────────────────────────
	ui.Post("/bitrix/accounts", h.uiCreateBitrixAccount)
	ui.Get("/bitrix/accounts", h.uiListBitrixAccounts)
	ui.Delete("/bitrix/accounts", h.uiDeleteBitrixAccount)
	// ─── Filas Bitrix (Partner App portals) ──────────────────────────────────
	ui.Get("/bitrix/queues", h.uiListBitrixQueues)
	ui.Put("/bitrix/queues", h.uiUpdateBitrixQueue)
	ui.Get("/bitrix/lines", h.uiListOpenLines)          // Lista open lines disponíveis no portal
	ui.Post("/bitrix/queues/link", h.uiLinkQueue)       // Cria vínculo portal+sessão+fila
	ui.Delete("/bitrix/queues/link", h.uiUnlinkQueue)   // Remove vínculo
	ui.Post("/bitrix/queues/activate", h.uiActivateConnector) // Força register+activate
	// ─── Permissões CRM (sem auth admin — uso interno do dashboard) ──────
	ui.Get("/permissions/list", h.uiPermissionsList)
	ui.Get("/permissions/user-info", h.uiPermissionsUserInfo)
	ui.Get("/permissions/all-users", h.uiPermissionsAllUsers)
	ui.Post("/permissions/grant", h.uiPermissionsGrant)
	ui.Post("/permissions/revoke", h.uiPermissionsRevoke)
	// ─── Templates de mensagem ──────────────────────────────────────────
	ui.Get("/templates/list", h.uiTemplatesList)
	ui.Get("/templates/debug", h.uiTemplatesDebug)
	ui.Post("/templates/create", h.uiTemplatesCreate)
	ui.Post("/templates/update", h.uiTemplatesUpdate)
	ui.Post("/templates/delete", h.uiTemplatesDelete)
	ui.Get("/templates/meta-list", h.uiTemplatesMetaList)
	ui.Post("/templates/meta-import", h.uiTemplatesMetaImport)

	ui.Get("/history/sessions", h.uiHistorySessions)
	ui.Get("/history/conversations", h.uiHistoryConversations)
	ui.Get("/history/messages", h.uiHistoryMessages)

	// ─── WhatsApp Sessions ───────────────────────────────────────────────
	wa := app.Group("/wa", authMiddleware(cfg.App.Secret))
	wa.Post("/sessions", h.addSession)
	wa.Get("/sessions", h.listSessions)
	wa.Get("/sessions/:phone/qr", h.getSessionQR)
	wa.Delete("/sessions/:jid", h.removeSession)
	wa.Post("/send", h.sendMessage)

	// ─── Webhook Cloud API (Meta) — público, identificado por session_id ──
	app.Get("/webhook/cloud/:session_id", h.cloudWebhookVerify)
	app.Post("/webhook/cloud/:session_id", h.cloudWebhookReceive)

	// ─── Mídia temporária para Cloud API (Meta baixa via link) ────────────
	// Público sem auth — protegido pelo token aleatório de 32 chars no path.
	app.Get("/cloud-media/:token", h.cloudMediaServe)

	// ─── Bitrix24 ────────────────────────────────────────────────────────
	bx := app.Group("/bitrix")
	bx.Get("/oauth/start", h.bitrixOAuthStart)
	bx.Get("/callback", h.bitrixOAuthCallback)
	bx.Post("/callback", h.bitrixOAuthCallback)   // Bitrix local app envia POST no install
	bx.Post("/webhook", h.bitrixWebhook)                // Recebe eventos do Bitrix (legado)
	bx.Post("/connector/event", h.bitrixConnectorEvent) // ONIMCONNECTORMESSAGEADD — reply do operador

	// ─── Debug (sem auth — apenas para diagnóstico) ───────────────────────
	app.Post("/debug/bitrix-event", h.debugBitrixEvent)
	app.Get("/debug/bitrix-event", h.debugBitrixEvent)
	app.Get("/debug/connector-status", h.debugConnectorStatus) // ?domain=...&line=...
	app.Get("/debug/event-bindings", h.debugEventBindings)     // ?domain=...
	app.Get("/debug/connector-list", h.debugConnectorList)     // ?domain=...
	app.Get("/debug/connector-data", h.debugConnectorData)      // ?domain=...&line=...&connector=...
	app.Post("/debug/rebind-event", h.debugRebindEvent)         // body: {domain, handler_url}
	app.Post("/debug/bitrix-call", h.debugBitrixCall)           // body: {domain, method, params}
	app.Get("/debug/dead-queue", h.debugDeadQueue)              // lê jobs da dead queue

	// ─── Partner App (Bitrix24 Marketplace) ──────────────────────────────
	// Endpoints EXCLUSIVOS do fluxo de Partner App — não interferem nos admin acima.
	bx.Get("/install", h.bitrixInstall)               // Bitrix valida a URL com GET antes de salvar
	bx.Post("/install", h.bitrixInstall)              // Application Installer URL (ONAPPINSTALL)
	bx.Post("/auth", h.bitrixPartnerAuth)             // Token do BX24.js enviado pela página /bitrix-connect
	bx.Post("/partner/link", h.bitrixPartnerLink)     // Vincula sessão WA ao portal após QR scan
	app.Get("/bitrix-connect", h.bitrixConnectPage)   // Application URL (abre em iframe no Bitrix24)
	app.Post("/bitrix-connect", h.bitrixConnectPage)  // BX24.installFinish() pode fazer POST aqui
	app.Get("/bitrix-app", h.bitrixAppMenu)           // LEFT_MENU placement — usuarios liberados
	app.Post("/bitrix-app", h.bitrixAppMenu)

	// ─── CRM Tab (aba WhatsApp no detalhe de Contato/Lead/Deal) ──────────
	bx.Get("/crm/tab", h.bitrixCRMTab)
	bx.Post("/crm/tab", h.bitrixCRMTab)
	bx.Get("/crm/entity", h.bitrixCRMEntity)
	bx.Post("/crm/send", h.bitrixCRMSend)
	bx.Get("/crm/sessions", h.bitrixCRMSessions)
	bx.Get("/crm/lines", h.bitrixCRMLines)
	bx.Get("/crm/history", h.bitrixCRMHistory)
	bx.Post("/crm/upload", h.bitrixCRMUpload)
	bx.Get("/crm/debug", h.bitrixCRMDebug) // diagnóstico temporário
	bx.Get("/crm/check-access", h.bitrixCRMCheckAccess)
	bx.Get("/crm/allowed-sessions", h.bitrixCRMAllowedSessions)
	bx.Get("/crm/master/status", h.bitrixCRMMasterStatus)
	bx.Post("/crm/master/set", h.bitrixCRMMasterSet)

	// ─── SMS Provider (Marketing > Campanhas SMS via WhatsApp) ────────────
	// Modulo isolado em sms_provider.go. Bitrix bate em /bitrix/sms/send,
	// UI do dashboard usa os /ui/sms/* abaixo. Nao toca em rotas existentes.
	bx.Post("/sms/send", h.smsProviderSend)
	ui.Get("/sms/status", h.uiSMSStatus)
	ui.Post("/sms/set-session", h.uiSMSSetSession)
	ui.Post("/sms/ack-risk", h.uiSMSAckRisk)
	ui.Get("/sms/messages", h.uiSMSMessages)

	// ─── BizProc Robot (CRM > Automacoes) ─────────────────────────────────
	// Modulo isolado em bp_robot.go. Bitrix bate em /bitrix/bp/send quando
	// o robot "UC Talk: Enviar WhatsApp" dispara em algum workflow CRM.
	bx.Post("/bp/send", h.bpRobotSend)

	// ─── Relatórios (com auth — para clientes externos via X-API-Key) ────
	stats := app.Group("/stats", authMiddleware(cfg.App.Secret))
	stats.Get("/daily", h.dailyStats)
	stats.Get("/queues", h.queueStats)
	stats.Get("/sessions", h.sessionStats)
	stats.Get("/types", h.typeStats)
	stats.Get("/hours", h.hourStats)
	stats.Get("/contacts", h.contactStats)
	stats.Get("/export", h.exportStats)

	// ─── Relatórios (sem auth — para dashboard interno consumir do browser) ─
	// Espelha /stats/* mas sem middleware. Usado pela aba Relatórios do
	// /dashboard que carrega via fetch sem header X-API-Key.
	ui.Get("/stats/daily", h.dailyStats)
	ui.Get("/stats/queues", h.queueStats)
	ui.Get("/stats/sessions", h.sessionStats)
	ui.Get("/stats/types", h.typeStats)
	ui.Get("/stats/hours", h.hourStats)
	ui.Get("/stats/contacts", h.contactStats)
	ui.Get("/stats/export", h.exportStats)

	// ─── Simulador interno (testes sem WA/Bitrix reais) ─────────────────
	app.Get("/sim", h.simPage)
	app.Post("/sim/inbound", h.simInbound)
	app.Post("/sim/outbound", h.simOutbound)
	app.Get("/sim/history", h.simHistory)
	app.Get("/sim/recent", h.simRecent)
	app.Get("/sim/sessions", h.simSessions)
	app.Get("/sim/lidmap", h.simLIDMap)
	app.Post("/sim/clear", h.simClear)

	// ─── Painel super-admin ──────────────────────────────────────────────
	// /admin/login é público; /admin e /admin/api/* exigem cookie assinado.
	app.Get("/admin/login", h.adminLoginPage)
	app.Post("/admin/login", h.adminLoginSubmit)
	app.Get("/admin/logout", h.adminLogout)
	admin := app.Group("/admin", h.requireAdminAuth)
	admin.Get("/", h.adminHome)
	admin.Get("", h.adminHome) // alias sem barra final
	admin.Get("/api/tenants", h.adminListTenants)
	admin.Get("/api/debug", h.adminDebug)
	admin.Post("/api/queue/flush", h.adminFlushQueue)
	admin.Post("/api/cleanup/banned-sessions", h.adminCleanupBannedSessions)
	admin.Post("/api/cleanup/placeholder-portals", h.adminCleanupPlaceholders)
	admin.Post("/api/cleanup/session-files", h.adminCleanupSessionFiles)
	admin.Post("/api/cleanup/legacy-messages", h.adminCleanupLegacyMessages)
	admin.Post("/api/tenant/cleanup/legacy-messages", h.adminTenantCleanupLegacyMessages)
	admin.Post("/api/tenant/cleanup/session-files", h.adminTenantCleanupSessionFiles)
	admin.Get("/api/tenant/users", h.adminTenantListUsers)
	admin.Get("/api/tenant/user-info", h.adminTenantUserInfo)
	admin.Post("/api/tenant/permissions", h.adminTenantSetPermission)
	admin.Get("/api/tenant/master", h.adminTenantMasterStatus)
	admin.Post("/api/tenant/master", h.adminTenantSetMaster)
	admin.Get("/api/tenant/sms-debug", h.adminTenantSMSDebug)
	admin.Post("/api/tenant/sms-register", h.adminTenantSMSRegister)
	admin.Post("/api/tenant/bp-register", h.adminTenantBPRegister)
	admin.Get("/api/tenant/bp-debug", h.adminTenantBPDebug)
	admin.Post("/api/tenant/bp-reregister", h.adminTenantBPReregister)
	// Aliases GET pra facilitar teste rapido via browser
	admin.Get("/api/tenant/bp-register", h.adminTenantBPRegister)
	admin.Get("/api/tenant/bp-reregister", h.adminTenantBPReregister)
	admin.Get("/api/tenant/sms-register", h.adminTenantSMSRegister)

	// ─── Stress test interno — protegido pelo mesmo middleware admin ──────
	stress := app.Group("/stress-test", h.requireAdminAuth)
	stress.Get("/", h.stressTestPage)
	stress.Get("", h.stressTestPage) // alias sem barra final
	stress.Get("/connectors", h.stressTestConnectors)
	stress.Post("/run", h.stressTestRun)

	// ─── Prometheus metrics ──────────────────────────────────────────────
	app.Get("/metrics", metrics.Handler())

	return app
}

func jsonErrorHandler(ctx *fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	if e, ok := err.(*fiber.Error); ok {
		code = e.Code
	}
	return ctx.Status(code).JSON(fiber.Map{"error": err.Error()})
}

func authMiddleware(secret string) fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		if secret == "" {
			return ctx.Next()
		}
		auth := ctx.Get("X-API-Key")
		if auth != secret {
			return fiber.ErrUnauthorized
		}
		return ctx.Next()
	}
}
