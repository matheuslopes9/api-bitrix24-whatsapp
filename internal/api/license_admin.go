// license_admin.go — administracao da licenca: beneficios contratados e
// lancamento de pagamentos.
//
// Substitui plan_admin.go, coupons.go e billing*.go do modelo SaaS. Quem
// lanca e' o suporte; o financeiro nao entra no sistema, e' avisado por
// e-mail (ver license_notify.go).
package api

import (
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"go.uber.org/zap"
)

// parseDataBR aceita ISO 8601 e o YYYY-MM-DD do date-picker do navegador.
func parseDataBR(s string) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return &t, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// GET /admin/api/licenses — todas as licencas, as que vencem primeiro no topo.
func (h *handlers) adminListLicenses(c *fiber.Ctx) error {
	lics, err := h.repo.ListLicenses(c.Context())
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	out := make([]fiber.Map, 0, len(lics))
	for _, l := range lics {
		out = append(out, licenseSummary(l))
	}
	return c.JSON(fiber.Map{"licenses": out, "total": len(out)})
}

// GET /admin/api/license?domain=... — licenca + historico de pagamentos.
func (h *handlers) adminGetLicense(c *fiber.Ctx) error {
	domain := normalizePortalDomain(strings.TrimSpace(c.Query("domain")))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain obrigatorio"})
	}
	lic, err := h.repo.GetLicense(c.Context(), domain)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	pags, err := h.repo.ListPayments(c.Context(), domain, 100)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{
		"domain":     domain,
		"license":    licenseSummary(lic),
		"pagamentos": pagamentosToClient(pags),
	})
}

func pagamentosToClient(pags []*db.LicensePayment) []fiber.Map {
	out := make([]fiber.Map, 0, len(pags))
	for _, p := range pags {
		out = append(out, fiber.Map{
			"id":           p.ID.String(),
			"paid_at":      p.PaidAt.Format("2006-01-02"),
			"covers_until": p.CoversUntil.Format("2006-01-02"),
			"amount_cents": p.AmountCents,
			"method":       p.Method,
			"notes":        p.Notes,
			"recorded_by":  p.RecordedBy,
			"created_at":   p.CreatedAt.Format(time.RFC3339),
		})
	}
	return out
}

// POST /admin/api/license — grava os beneficios contratados e a vigencia.
func (h *handlers) adminSaveLicense(c *fiber.Ctx) error {
	var req struct {
		Domain          string `json:"domain"`
		MaxSessions     int    `json:"max_sessions"`
		FeatCloudAPI    bool   `json:"feat_cloud_api"`
		FeatAutomations bool   `json:"feat_automations"`
		FeatReports     bool   `json:"feat_reports"`
		FeatSMS         bool   `json:"feat_sms"`
		ValidUntil      string `json:"valid_until"`
		Notes           string `json:"notes"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "JSON invalido"})
	}
	domain := normalizePortalDomain(strings.TrimSpace(req.Domain))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain obrigatorio"})
	}
	validUntil, err := parseDataBR(req.ValidUntil)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "valid_until invalido (use YYYY-MM-DD)"})
	}

	lic := &db.TenantLicense{
		Domain:          domain,
		MaxSessions:     req.MaxSessions,
		FeatCloudAPI:    req.FeatCloudAPI,
		FeatAutomations: req.FeatAutomations,
		FeatReports:     req.FeatReports,
		FeatSMS:         req.FeatSMS,
		ValidUntil:      validUntil,
		Notes:           strings.TrimSpace(req.Notes),
	}
	if err := h.repo.SaveLicenseFeatures(c.Context(), lic); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	ate := "sem prazo"
	if validUntil != nil {
		ate = validUntil.Format("2006-01-02")
	}
	h.log.Info("licenca salva",
		zap.String("domain", domain),
		zap.Int("max_sessions", lic.MaxSessions),
		zap.String("valid_until", ate))
	h.repo.WriteAudit(c.Context(), h.adminActor(c), "license.save", domain,
		"max_sessions="+itoa(lic.MaxSessions)+" cloud="+boolStr(lic.FeatCloudAPI)+
			" auto="+boolStr(lic.FeatAutomations)+" reports="+boolStr(lic.FeatReports)+
			" ate="+ate, clientIP(c))

	// Os robos BizProc dependem da feature de automacoes: ao ligar/desligar,
	// o dropdown no Bitrix precisa refletir na hora — senao o cliente marca a
	// feature e continua sem ver os robos ate' o proximo reconnect.
	h.triggerBPRobotRefresh(domain, "licenca alterada")

	novaLic, _ := h.repo.GetLicense(c.Context(), domain)
	return c.JSON(fiber.Map{"ok": true, "license": licenseSummary(novaLic)})
}

// POST /admin/api/license/payment — lanca um pagamento e estende a vigencia.
func (h *handlers) adminRecordPayment(c *fiber.Ctx) error {
	var req struct {
		Domain      string `json:"domain"`
		PaidAt      string `json:"paid_at"`
		CoversUntil string `json:"covers_until"`
		AmountCents int64  `json:"amount_cents"`
		Method      string `json:"method"`
		Notes       string `json:"notes"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "JSON invalido"})
	}
	domain := normalizePortalDomain(strings.TrimSpace(req.Domain))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain obrigatorio"})
	}
	paidAt, err := parseDataBR(req.PaidAt)
	if err != nil || paidAt == nil {
		return c.Status(400).JSON(fiber.Map{"error": "paid_at obrigatorio (YYYY-MM-DD)"})
	}
	coversUntil, err := parseDataBR(req.CoversUntil)
	if err != nil || coversUntil == nil {
		return c.Status(400).JSON(fiber.Map{"error": "covers_until obrigatorio (YYYY-MM-DD)"})
	}
	if coversUntil.Before(*paidAt) {
		return c.Status(400).JSON(fiber.Map{
			"error": "covers_until nao pode ser anterior a paid_at"})
	}

	// recorded_by e' o operador logado — nao um valor do corpo da request.
	// Quem registrou o pagamento e' fato de auditoria, nao algo que o cliente
	// do endpoint escolhe.
	p := &db.LicensePayment{
		Domain:      domain,
		PaidAt:      *paidAt,
		CoversUntil: *coversUntil,
		AmountCents: req.AmountCents,
		Method:      strings.TrimSpace(req.Method),
		Notes:       strings.TrimSpace(req.Notes),
		RecordedBy:  h.adminActor(c),
	}
	if err := h.repo.RecordPayment(c.Context(), p); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	h.log.Info("pagamento registrado",
		zap.String("domain", domain),
		zap.String("pago_em", paidAt.Format("2006-01-02")),
		zap.String("cobre_ate", coversUntil.Format("2006-01-02")),
		zap.String("por", p.RecordedBy))
	h.repo.WriteAudit(c.Context(), p.RecordedBy, "license.payment", domain,
		"pago="+paidAt.Format("2006-01-02")+" cobre_ate="+coversUntil.Format("2006-01-02")+
			" valor_cents="+itoa64(p.AmountCents), clientIP(c))

	lic, _ := h.repo.GetLicense(c.Context(), domain)
	return c.JSON(fiber.Map{"ok": true, "license": licenseSummary(lic)})
}

// GET /ui/license — o CLIENTE ve (somente leitura) o que contratou.
//
// Sem preco e sem pagamento: o cliente nao negocia pelo app. Serve pra ele
// saber quantos numeros pode conectar e o que esta incluso.
func (h *handlers) uiTenantLicense(c *fiber.Ctx) error {
	ctx := c.Context()
	domain, err := h.resolveDashboardDomain(ctx, c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	f := h.resolveTenantFeatures(ctx, domain)
	usadas, _ := h.repo.CountActiveSessionsByDomain(ctx, normalizePortalDomain(domain))
	return c.JSON(fiber.Map{
		"domain":           domain,
		"max_sessions":     f.MaxSessions,
		"sessions_em_uso":  usadas,
		"feat_cloud_api":   f.CloudAPI,
		"feat_automations": f.Automations,
		"feat_reports":     f.Reports,
		"valid_until":      f.ValidUntil,
		"expirada":         f.Expired,
	})
}

func itoa(n int) string     { return strconv.Itoa(n) }
func itoa64(n int64) string { return strconv.FormatInt(n, 10) }
