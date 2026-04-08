package api

import (
	"fmt"
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/bitrix"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/config"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/queue"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/telemetry"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/whatsapp"
	"go.uber.org/zap"
)

type handlers struct {
	cfg          *config.Config
	repo         *db.Repository
	waManager    *whatsapp.Manager
	bitrixClient *bitrix.Client
	q            *queue.Queue
	metrics      *telemetry.Metrics
	log          *zap.Logger
}

func newHandlers(
	cfg *config.Config,
	repo *db.Repository,
	waManager *whatsapp.Manager,
	bitrixClient *bitrix.Client,
	q *queue.Queue,
	metrics *telemetry.Metrics,
	log *zap.Logger,
) *handlers {
	return &handlers{cfg: cfg, repo: repo, waManager: waManager, bitrixClient: bitrixClient, q: q, metrics: metrics, log: log}
}

// GET /health
func (h *handlers) health(c *fiber.Ctx) error {
	sessions := h.waManager.ListSessions()
	in, out, dead := h.q.Lengths(c.Context())
	return c.JSON(fiber.Map{
		"status":           "ok",
		"active_sessions":  len(sessions),
		"queue_inbound":    in,
		"queue_outbound":   out,
		"queue_dead":       dead,
	})
}

// POST /wa/sessions
// Body: { "phone": "5511999999999" }
// Retorna imediatamente. QR disponível em GET /wa/sessions/:phone/qr
func (h *handlers) addSession(c *fiber.Ctx) error {
	var body struct {
		Phone string `json:"phone"`
	}
	if err := c.BodyParser(&body); err != nil || body.Phone == "" {
		return fiber.NewError(fiber.StatusBadRequest, "phone required")
	}

	if err := h.waManager.AddSession(c.Context(), body.Phone); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}

	return c.JSON(fiber.Map{
		"status": "connecting",
		"phone":  body.Phone,
		"qr_url": "/wa/sessions/" + body.Phone + "/qr",
	})
}

// GET /wa/sessions/:phone/qr — retorna o QR code atual (polling)
func (h *handlers) getSessionQR(c *fiber.Ctx) error {
	phone := c.Params("phone")
	qr := h.waManager.GetQR(phone)
	if qr == "" {
		return c.JSON(fiber.Map{"status": "waiting", "qr": ""})
	}
	return c.JSON(fiber.Map{"status": "ready", "qr": qr})
}

// GET /wa/sessions
func (h *handlers) listSessions(c *fiber.Ctx) error {
	jids := h.waManager.ListSessions()
	return c.JSON(fiber.Map{"sessions": jids, "count": len(jids)})
}

// DELETE /wa/sessions/:jid
func (h *handlers) removeSession(c *fiber.Ctx) error {
	jid := c.Params("jid")
	h.waManager.Disconnect(jid)
	return c.JSON(fiber.Map{"status": "disconnected", "jid": jid})
}

// POST /wa/send
// Body: { "session_jid": "...", "to": "5511999999999@s.whatsapp.net", "text": "Olá!" }
func (h *handlers) sendMessage(c *fiber.Ctx) error {
	var body struct {
		SessionJID string `json:"session_jid"`
		To         string `json:"to"`
		Text       string `json:"text"`
	}
	if err := c.BodyParser(&body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	if body.SessionJID == "" || body.To == "" || body.Text == "" {
		return fiber.NewError(fiber.StatusBadRequest, "session_jid, to, text required")
	}

	// Coloca na fila de saída (não bloqueia a request)
	if err := h.q.PushOutbound(c.Context(), &queue.OutboundJob{
		SessionJID: body.SessionJID,
		ToJID:      body.To,
		Text:       body.Text,
	}); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}

	return c.JSON(fiber.Map{"status": "queued"})
}

// GET|POST /bitrix/callback — handler de instalação do app local Bitrix24
// O Bitrix24 chama este endpoint com event=ONAPPINSTALL e auth[access_token]
func (h *handlers) bitrixOAuthCallback(c *fiber.Ctx) error {
	h.log.Info("bitrix callback received",
		zap.String("method", c.Method()),
		zap.String("raw_body", string(c.Body())),
		zap.String("content_type", c.Get("Content-Type")),
	)

	// App local envia form-encoded
	event := c.FormValue("event")
	accessToken := c.FormValue("auth[access_token]")
	refreshToken := c.FormValue("auth[refresh_token]")
	domain := c.FormValue("auth[domain]")
	expiresIn := c.FormValue("auth[expires_in]")

	// Fallback: tenta JSON
	if accessToken == "" {
		var body struct {
			Event string `json:"event"`
			Auth  struct {
				AccessToken  string `json:"access_token"`
				RefreshToken string `json:"refresh_token"`
				Domain       string `json:"domain"`
				ExpiresIn    int    `json:"expires_in"`
			} `json:"auth"`
		}
		if err := c.BodyParser(&body); err == nil {
			event = body.Event
			accessToken = body.Auth.AccessToken
			refreshToken = body.Auth.RefreshToken
			domain = body.Auth.Domain
		}
	}

	preview := accessToken
	if len(preview) > 10 {
		preview = preview[:10]
	}
	h.log.Info("bitrix install event",
		zap.String("event", event),
		zap.String("domain", domain),
		zap.String("token_preview", preview))

	if accessToken == "" {
		return fiber.NewError(fiber.StatusBadRequest, "access_token missing")
	}

	if domain == "" {
		domain = h.cfg.Bitrix.Domain
	}

	exp := 3600
	if expiresIn != "" {
		fmt.Sscanf(expiresIn, "%d", &exp)
	}

	if err := h.bitrixClient.SaveToken(c.Context(), domain, accessToken, refreshToken, exp); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}

	return c.SendStatus(fiber.StatusOK)
}

// GET /bitrix/oauth/start — não usado em app local, mantido por compatibilidade
func (h *handlers) bitrixOAuthStart(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"info": "App local Bitrix24 — autorização via installation handler em POST /bitrix/callback",
	})
}


// POST /bitrix/webhook — recebe mensagens do operador via Bitrix
func (h *handlers) bitrixWebhook(c *fiber.Ctx) error {
	var payload struct {
		Event string `json:"event"`
		Data  struct {
			SessionID string `json:"SESSION_ID"`
			Message   string `json:"MESSAGE"`
			UserPhone string `json:"USER_PHONE"`
			SessionJID string `json:"WA_SESSION_JID"`
			ToJID     string `json:"WA_TO_JID"`
		} `json:"data"`
	}
	if err := c.BodyParser(&payload); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	if payload.Data.Message == "" {
		return c.SendStatus(fiber.StatusNoContent)
	}

	if err := h.q.PushOutbound(c.Context(), &queue.OutboundJob{
		SessionJID: payload.Data.SessionJID,
		ToJID:      payload.Data.ToJID,
		Text:       payload.Data.Message,
	}); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}

	return c.SendStatus(fiber.StatusOK)
}

// GET /stats/daily?days=7
func (h *handlers) dailyStats(c *fiber.Ctx) error {
	days, _ := strconv.Atoi(c.Query("days", "7"))
	if days < 1 || days > 90 {
		days = 7
	}
	stats, err := h.repo.GetDailyStats(c.Context(), days)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	return c.JSON(stats)
}

// GET /stats/queues
func (h *handlers) queueStats(c *fiber.Ctx) error {
	in, out, dead := h.q.Lengths(c.Context())
	return c.JSON(fiber.Map{
		"inbound":  in,
		"outbound": out,
		"dead":     dead,
	})
}
