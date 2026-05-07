package api

// Handlers UI para sessões Cloud API (Meta).
// Convivem com os handlers QR Code existentes — não substituem nada.

import (
	"crypto/rand"
	"encoding/hex"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/whatsapp"
	"go.uber.org/zap"
)

// POST /ui/sessions/cloud
// Body: {
//   "phone_number_id":"...", "waba_id":"...", "access_token":"...",
//   "app_secret":"...", "display_phone":"5511999999999",
//   "display_label":"Suporte UCT (Oficial)" (opcional),
//   "verify_token":"..." (opcional — gerado se vazio)
// }
func (h *handlers) uiCreateCloudSession(c *fiber.Ctx) error {
	if h.cloudMgr == nil {
		return c.Status(500).JSON(fiber.Map{"error": "cloud manager não inicializado"})
	}
	var body struct {
		PhoneNumberID string `json:"phone_number_id"`
		WABAID        string `json:"waba_id"`
		AccessToken   string `json:"access_token"`
		AppSecret     string `json:"app_secret"`
		VerifyToken   string `json:"verify_token"`
		DisplayPhone  string `json:"display_phone"`
		DisplayLabel  string `json:"display_label"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	body.PhoneNumberID = strings.TrimSpace(body.PhoneNumberID)
	body.AccessToken = strings.TrimSpace(body.AccessToken)
	body.DisplayPhone = strings.TrimSpace(body.DisplayPhone)
	if body.PhoneNumberID == "" || body.AccessToken == "" {
		return c.Status(400).JSON(fiber.Map{"error": "phone_number_id e access_token são obrigatórios"})
	}
	if body.VerifyToken == "" {
		body.VerifyToken = randomHex(16)
	}

	params := whatsapp.CloudSessionParams{
		PhoneNumberID: body.PhoneNumberID,
		WABAID:        body.WABAID,
		AccessToken:   body.AccessToken,
		AppSecret:     body.AppSecret,
		VerifyToken:   body.VerifyToken,
		DisplayPhone:  body.DisplayPhone,
		DisplayLabel:  body.DisplayLabel,
	}

	sess, err := h.cloudMgr.AddSession(c.Context(), params)
	if err != nil {
		h.log.Warn("uiCreateCloudSession failed", zap.Error(err))
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}

	webhookURL := h.cfg.App.BaseURL() + "/webhook/cloud/" + sess.ID.String()
	return c.JSON(fiber.Map{
		"ok":           true,
		"session_id":   sess.ID.String(),
		"jid":          sess.JID,
		"display_phone": sess.CloudDisplayPhone,
		"webhook_url":  webhookURL,
		"verify_token": sess.CloudVerifyToken,
	})
}

// GET /ui/sessions/cloud/:session_id/webhook-info
// Retorna URL de webhook + verify_token para colar no painel Meta.
func (h *handlers) uiCloudWebhookInfo(c *fiber.Ctx) error {
	idStr := c.Params("session_id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid session_id"})
	}
	sess, err := h.repo.GetSessionByID(c.Context(), id)
	if err != nil || sess == nil {
		return c.Status(404).JSON(fiber.Map{"error": "session not found"})
	}
	if sess.Type != db.SessionTypeCloudAPI {
		return c.Status(400).JSON(fiber.Map{"error": "session não é cloud_api"})
	}
	return c.JSON(fiber.Map{
		"session_id":    sess.ID.String(),
		"jid":           sess.JID,
		"webhook_url":   h.cfg.App.BaseURL() + "/webhook/cloud/" + sess.ID.String(),
		"verify_token":  sess.CloudVerifyToken,
		"display_phone": sess.CloudDisplayPhone,
		"phone_number_id": sess.CloudPhoneNumberID,
	})
}

func randomHex(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
