package api

// Painel super-admin: lista todos os portais Bitrix24 que instalaram o app.
// Acesso por ADMIN_USER/ADMIN_PASSWORD em env var. Cookie de sessão assinado
// com APP_SECRET (HMAC-SHA256, TTL 12h).

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

const adminCookieName = "uctalk_admin"
const adminCookieTTL = 12 * time.Hour

// signAdminCookie gera "exp.hmac" assinado com HMAC-SHA256(APP_SECRET).
// exp é unix timestamp. Lookup em tempo constante.
func signAdminCookie(secret string, expiresAt time.Time) string {
	exp := strconv.FormatInt(expiresAt.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(exp))
	return exp + "." + hex.EncodeToString(mac.Sum(nil))
}

func verifyAdminCookie(secret, raw string) bool {
	if raw == "" {
		return false
	}
	parts := strings.SplitN(raw, ".", 2)
	if len(parts) != 2 {
		return false
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(parts[0]))
	expected := hex.EncodeToString(mac.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(parts[1]), []byte(expected)) == 1
}

// requireAdminAuth — middleware: verifica cookie. Redirect para /admin/login se falhar.
func (h *handlers) requireAdminAuth(c *fiber.Ctx) error {
	if h.cfg.App.AdminUser == "" || h.cfg.App.AdminPassword == "" {
		return c.Status(503).SendString("admin desabilitado: defina ADMIN_USER e ADMIN_PASSWORD no .env")
	}
	cookie := c.Cookies(adminCookieName)
	if !verifyAdminCookie(h.cfg.App.Secret, cookie) {
		return c.Redirect("/admin/login", fiber.StatusFound)
	}
	return c.Next()
}

// GET /admin/login — formulário de login.
func (h *handlers) adminLoginPage(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/html; charset=utf-8")
	errMsg := c.Query("err")
	errBlock := ""
	if errMsg != "" {
		errBlock = `<div class="err">` + escapeHTML(errMsg) + `</div>`
	}
	return c.SendString(strings.ReplaceAll(adminLoginHTML, "<!--ERR-->", errBlock))
}

// POST /admin/login — valida credenciais, seta cookie.
func (h *handlers) adminLoginSubmit(c *fiber.Ctx) error {
	user := c.FormValue("user")
	pass := c.FormValue("password")
	if h.cfg.App.AdminUser == "" || h.cfg.App.AdminPassword == "" {
		return c.Status(503).SendString("admin desabilitado")
	}
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(h.cfg.App.AdminUser)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(h.cfg.App.AdminPassword)) == 1
	if !userOK || !passOK {
		return c.Redirect("/admin/login?err=Credenciais+inv%C3%A1lidas", fiber.StatusFound)
	}
	expires := time.Now().Add(adminCookieTTL)
	c.Cookie(&fiber.Cookie{
		Name:     adminCookieName,
		Value:    signAdminCookie(h.cfg.App.Secret, expires),
		Path:     "/",
		Expires:  expires,
		HTTPOnly: true,
		Secure:   strings.HasPrefix(h.cfg.App.PublicURL, "https://"),
		SameSite: "Lax",
	})
	return c.Redirect("/admin", fiber.StatusFound)
}

// POST /admin/logout — limpa cookie.
func (h *handlers) adminLogout(c *fiber.Ctx) error {
	c.Cookie(&fiber.Cookie{
		Name:    adminCookieName,
		Value:   "",
		Path:    "/",
		Expires: time.Unix(0, 0),
	})
	return c.Redirect("/admin/login", fiber.StatusFound)
}

// GET /admin — página principal com cards de cada portal.
func (h *handlers) adminHome(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/html; charset=utf-8")
	return c.SendString(adminHomeHTML)
}

// GET /admin/api/tenants — dados JSON para popular os cards.
// Agrega por portal Bitrix (bitrix_portals): conexões WA (QR/Cloud), msgs 24h/1h,
// status do token OAuth.
func (h *handlers) adminListTenants(c *fiber.Ctx) error {
	ctx := c.Context()
	portals, err := h.repo.ListBitrixPortals(ctx)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	now := time.Now()
	type tenantCard struct {
		ID           string    `json:"id"`
		Domain       string    `json:"domain"`
		MemberID     string    `json:"member_id"`
		InstalledAt  time.Time `json:"installed_at"`
		UpdatedAt    time.Time `json:"updated_at"`
		TokenExpAt   time.Time `json:"token_expires_at"`
		TokenStatus  string    `json:"token_status"` // valid | expiring | expired
		OpenLineID   int       `json:"open_line_id"`
		ConnQR       int       `json:"connections_qr"`
		ConnCloud    int       `json:"connections_cloud"`
		Msgs24h      int       `json:"msgs_24h"`
		Msgs1h       int       `json:"msgs_1h"`
		MsgsInbound  int       `json:"msgs_inbound_24h"`
		MsgsOutbound int       `json:"msgs_outbound_24h"`
	}

	out := make([]tenantCard, 0, len(portals))
	for _, p := range portals {
		card := tenantCard{
			ID:          p.ID.String(),
			Domain:      p.Domain,
			MemberID:    p.MemberID,
			InstalledAt: p.InstalledAt,
			UpdatedAt:   p.UpdatedAt,
			TokenExpAt:  p.ExpiresAt,
			OpenLineID:  p.OpenLineID,
		}
		// Status do token
		if p.ExpiresAt.Before(now) {
			card.TokenStatus = "expired"
		} else if p.ExpiresAt.Before(now.Add(7 * 24 * time.Hour)) {
			card.TokenStatus = "expiring"
		} else {
			card.TokenStatus = "valid"
		}
		// Conexões WA por dominio
		qr, cloud, _ := h.repo.CountSessionsByDomain(ctx, p.Domain)
		card.ConnQR = qr
		card.ConnCloud = cloud
		// Atividade
		in24, out24, _ := h.repo.CountMessagesByDomain(ctx, p.Domain, now.Add(-24*time.Hour))
		in1, out1, _ := h.repo.CountMessagesByDomain(ctx, p.Domain, now.Add(-time.Hour))
		card.Msgs24h = in24 + out24
		card.Msgs1h = in1 + out1
		card.MsgsInbound = in24
		card.MsgsOutbound = out24
		out = append(out, card)
	}

	return c.JSON(fiber.Map{
		"tenants":     out,
		"total":       len(out),
		"generated_at": time.Now().Format(time.RFC3339),
	})
}

// escapeHTML simples para mensagem de erro.
func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}

// _ avoid unused fmt
var _ = fmt.Sprintf
