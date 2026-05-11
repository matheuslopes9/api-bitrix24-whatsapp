package api

// Painel super-admin: lista todos os portais Bitrix24 que instalaram o app.
// Acesso por ADMIN_USER/ADMIN_PASSWORD em env var. Cookie de sessão assinado
// com APP_SECRET (HMAC-SHA256, TTL 12h).

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"
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

// requireAdminAuth — middleware: verifica cookie.
//   - APIs (paths que contém /api/ ou /run, ou método != GET) retornam 401 JSON
//   - Páginas HTML retornam redirect 302 para /admin/login (UX de browser)
func (h *handlers) requireAdminAuth(c *fiber.Ctx) error {
	if h.cfg.App.AdminUser == "" || h.cfg.App.AdminPassword == "" {
		return c.Status(503).SendString("admin desabilitado: defina ADMIN_USER e ADMIN_PASSWORD no .env")
	}
	cookie := c.Cookies(adminCookieName)
	if verifyAdminCookie(h.cfg.App.Secret, cookie) {
		return c.Next()
	}
	path := c.Path()
	isAPI := strings.Contains(path, "/api/") || strings.HasSuffix(path, "/run") || strings.HasSuffix(path, "/connectors") || c.Method() != fiber.MethodGet
	if isAPI {
		return c.Status(401).JSON(fiber.Map{"error": "unauthorized"})
	}
	return c.Redirect("/admin/login", fiber.StatusFound)
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
// status do token OAuth. Usa queries agregadas (GROUP BY) — 3 queries no total,
// independente da quantidade de portais. Escala para 1000+ tenants.
func (h *handlers) adminListTenants(c *fiber.Ctx) error {
	ctx := c.Context()
	portals, err := h.repo.ListBitrixPortals(ctx)
	if err != nil {
		h.log.Error("admin: ListBitrixPortals failed", zap.Error(err))
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	now := time.Now()

	// 4 queries agregadas — uma vez só, independente de N portais.
	sessionsByDomain, err := h.repo.AllDomainSessionCounts(ctx)
	if err != nil {
		h.log.Error("admin: AllDomainSessionCounts failed", zap.Error(err))
		return c.Status(500).JSON(fiber.Map{"error": "session counts: " + err.Error()})
	}
	msgs24hByDomain, err := h.repo.AllDomainMessageCounts(ctx, now.Add(-24*time.Hour))
	if err != nil {
		h.log.Error("admin: AllDomainMessageCounts(24h) failed", zap.Error(err))
		return c.Status(500).JSON(fiber.Map{"error": "msg counts 24h: " + err.Error()})
	}
	msgs1hByDomain, err := h.repo.AllDomainMessageCounts(ctx, now.Add(-time.Hour))
	if err != nil {
		h.log.Error("admin: AllDomainMessageCounts(1h) failed", zap.Error(err))
		return c.Status(500).JSON(fiber.Map{"error": "msg counts 1h: " + err.Error()})
	}
	// expiry dos tokens VIVOS (bitrix_tokens é atualizado a cada refresh,
	// diferente de bitrix_portals.expires_at que so reflete o install inicial)
	tokenExpByDomain, err := h.repo.AllDomainTokenExpiry(ctx)
	if err != nil {
		h.log.Error("admin: AllDomainTokenExpiry failed", zap.Error(err))
		return c.Status(500).JSON(fiber.Map{"error": "token expiry: " + err.Error()})
	}

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

	cards := make([]tenantCard, 0, len(portals))
	for _, p := range portals {
		card := tenantCard{
			ID:          p.ID.String(),
			Domain:      p.Domain,
			MemberID:    p.MemberID,
			InstalledAt: p.InstalledAt,
			UpdatedAt:   p.UpdatedAt,
			OpenLineID:  p.OpenLineID,
		}
		// Status do token: usa o expires_at do bitrix_tokens (atualizado a cada
		// refresh, TTL ~1h do access). Se nao tem token na tabela, marca expirado.
		// access_token vence em 1h, mas o refresh_token (TTL 30d) renova auto.
		// Entao so consideramos "expired" se passou de 30 dias sem renovar — sinal
		// de que o refresh tambem morreu e precisa reinstalar.
		if exp, ok := tokenExpByDomain[p.Domain]; ok {
			card.TokenExpAt = exp
			refreshTokenLimit := exp.Add(30 * 24 * time.Hour) // exp do access + 30d do refresh
			switch {
			case now.After(refreshTokenLimit):
				card.TokenStatus = "expired"
			case now.After(refreshTokenLimit.Add(-3 * 24 * time.Hour)):
				card.TokenStatus = "expiring"
			default:
				card.TokenStatus = "valid"
			}
		} else {
			// Sem token salvo — provavelmente nunca foi feito refresh ou install
			// nao completou. Mostra como problema.
			card.TokenExpAt = p.ExpiresAt
			card.TokenStatus = "expired"
		}
		if s, ok := sessionsByDomain[p.Domain]; ok {
			card.ConnQR = s.QR
			card.ConnCloud = s.Cloud
		}
		if m, ok := msgs24hByDomain[p.Domain]; ok {
			card.MsgsInbound = m.Inbound
			card.MsgsOutbound = m.Outbound
			card.Msgs24h = m.Inbound + m.Outbound
		}
		if m, ok := msgs1hByDomain[p.Domain]; ok {
			card.Msgs1h = m.Inbound + m.Outbound
		}
		cards = append(cards, card)
	}

	return c.JSON(fiber.Map{
		"tenants":      cards,
		"total":        len(cards),
		"generated_at": now.Format(time.RFC3339),
	})
}

// POST /admin/api/queue/flush — limpa filas do Redis.
// Body JSON: {"kinds":["inbound","outbound","dead"]}. Default: limpa só outbound
// (inbound real e dead-letter ficam preservados a menos que pedidos).
func (h *handlers) adminFlushQueue(c *fiber.Ctx) error {
	var req struct {
		Kinds []string `json:"kinds"`
	}
	_ = c.BodyParser(&req)
	if len(req.Kinds) == 0 {
		req.Kinds = []string{"outbound"}
	}
	removed, err := h.q.Flush(c.Context(), req.Kinds)
	if err != nil {
		h.log.Error("admin: queue flush failed", zap.Error(err))
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.log.Info("admin: queue flushed", zap.Any("removed", removed))
	return c.JSON(fiber.Map{"removed": removed})
}

// escapeHTML simples para mensagem de erro.
func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}
