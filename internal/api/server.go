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

	h := newHandlers(cfg, repo, waManager, bitrixClient, q, metrics, log)

	// ─── Assets estáticos ────────────────────────────────────────────────
	app.Get("/assets/chart.js", h.serveChartJS)
	app.Get("/assets/logo.png", h.serveLogo)
	app.Get("/favicon.ico", h.serveFavicon)
	app.Get("/favicon.png", h.serveFavicon)

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
	ui.Get("/overview", h.uiOverview)
	// ─── Bitrix Accounts (multi-tenant) ──────────────────────────────────
	ui.Post("/bitrix/accounts", h.uiCreateBitrixAccount)
	ui.Get("/bitrix/accounts", h.uiListBitrixAccounts)
	ui.Delete("/bitrix/accounts", h.uiDeleteBitrixAccount)
	// ─── Filas Bitrix (Partner App portals) ──────────────────────────────────
	ui.Get("/bitrix/queues", h.uiListBitrixQueues)
	ui.Put("/bitrix/queues", h.uiUpdateBitrixQueue)
	ui.Post("/bitrix/queues/link", h.uiLinkQueue)       // Cria vínculo portal+sessão+fila
	ui.Delete("/bitrix/queues/link", h.uiUnlinkQueue)   // Remove vínculo
	ui.Post("/bitrix/queues/activate", h.uiActivateConnector) // Força register+activate

	// ─── WhatsApp Sessions ───────────────────────────────────────────────
	wa := app.Group("/wa", authMiddleware(cfg.App.Secret))
	wa.Post("/sessions", h.addSession)
	wa.Get("/sessions", h.listSessions)
	wa.Get("/sessions/:phone/qr", h.getSessionQR)
	wa.Delete("/sessions/:jid", h.removeSession)
	wa.Post("/send", h.sendMessage)

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

	// ─── Partner App (Bitrix24 Marketplace) ──────────────────────────────
	// Endpoints EXCLUSIVOS do fluxo de Partner App — não interferem nos admin acima.
	bx.Post("/install", h.bitrixInstall)              // Application Installer URL (ONAPPINSTALL)
	bx.Post("/auth", h.bitrixPartnerAuth)             // Token do BX24.js enviado pela página /bitrix-connect
	bx.Post("/partner/link", h.bitrixPartnerLink)     // Vincula sessão WA ao portal após QR scan
	app.Get("/bitrix-connect", h.bitrixConnectPage)   // Application URL (abre em iframe no Bitrix24)

	// ─── Relatórios ──────────────────────────────────────────────────────
	stats := app.Group("/stats", authMiddleware(cfg.App.Secret))
	stats.Get("/daily", h.dailyStats)
	stats.Get("/queues", h.queueStats)

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
