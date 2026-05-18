package api

// Handlers de gestao de planos. Dois grupos:
//   1. Super-admin (cookie admin obrigatorio): GET/POST /admin/api/tenant/plan
//      e GET /admin/api/tenant/plans. Pra UC Technology gerenciar manualmente.
//   2. Tenant (cookie tenant): GET /ui/plan. Pra o /dashboard mostrar banner
//      com dias restantes + chamada pra upgrade.

import (
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"
)

// GET /admin/api/tenant/plan?domain=cliente.bitrix24.com
func (h *handlers) adminTenantGetPlan(c *fiber.Ctx) error {
	domain := normalizePortalDomain(strings.TrimSpace(c.Query("domain")))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain obrigatorio"})
	}
	plan, err := h.repo.GetTenantPlan(c.Context(), domain)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	if plan == nil {
		return c.Status(404).JSON(fiber.Map{
			"error":  "tenant sem plano cadastrado",
			"domain": domain,
			"hint":   "Cliente nao instalou o app ainda OU instalou antes do sistema de planos existir. Use POST /admin/api/tenant/plan pra criar.",
		})
	}
	return c.JSON(fiber.Map{
		"domain": domain,
		"plan":   planSummary(plan),
	})
}

// POST /admin/api/tenant/plan
// Body JSON:
//   {
//     "domain": "cliente.bitrix24.com",
//     "plan": "pro",         // 'basic' | 'pro'
//     "status": "active",    // 'trial' | 'active' | 'expired' | 'suspended'
//     "active_until": "2027-05-18T00:00:00Z",  // opcional; null = vitalicio
//     "notes": "cliente piloto X — pago via PIX em 18/05/2026"
//   }
func (h *handlers) adminTenantSetPlan(c *fiber.Ctx) error {
	var req struct {
		Domain      string `json:"domain"`
		Plan        string `json:"plan"`
		Status      string `json:"status"`
		ActiveUntil string `json:"active_until"` // ISO 8601 ou ""
		Notes       string `json:"notes"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "JSON invalido"})
	}
	req.Domain = normalizePortalDomain(strings.TrimSpace(req.Domain))
	req.Plan = strings.ToLower(strings.TrimSpace(req.Plan))
	req.Status = strings.ToLower(strings.TrimSpace(req.Status))
	if req.Domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain obrigatorio"})
	}
	if req.Plan != "basic" && req.Plan != "pro" {
		return c.Status(400).JSON(fiber.Map{"error": "plan deve ser 'basic' ou 'pro'"})
	}
	validStatus := map[string]bool{"trial": true, "active": true, "expired": true, "suspended": true}
	if !validStatus[req.Status] {
		return c.Status(400).JSON(fiber.Map{"error": "status invalido (use trial|active|expired|suspended)"})
	}

	var activeUntil *time.Time
	if s := strings.TrimSpace(req.ActiveUntil); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "active_until invalido (use ISO 8601: 2027-05-18T00:00:00Z)"})
		}
		activeUntil = &t
	}

	if err := h.repo.SetTenantPlan(c.Context(), req.Domain, req.Plan, req.Status,
		activeUntil, req.Notes); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	plan, _ := h.repo.GetTenantPlan(c.Context(), req.Domain)
	h.log.Info("admin: tenant plan updated",
		zap.String("domain", req.Domain),
		zap.String("plan", req.Plan),
		zap.String("status", req.Status))
	return c.JSON(fiber.Map{
		"ok":     true,
		"domain": req.Domain,
		"plan":   planSummary(plan),
	})
}

// GET /admin/api/tenant/plans — lista todos os planos pra UI super-admin.
func (h *handlers) adminListTenantPlans(c *fiber.Ctx) error {
	plans, err := h.repo.ListTenantPlans(c.Context())
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	out := make([]map[string]interface{}, 0, len(plans))
	for _, p := range plans {
		m := planSummary(p)
		m["domain"] = p.Domain
		m["notes"] = p.Notes
		m["created_at"] = p.CreatedAt.Format(time.RFC3339)
		m["updated_at"] = p.UpdatedAt.Format(time.RFC3339)
		out = append(out, m)
	}
	return c.JSON(fiber.Map{"plans": out, "total": len(out)})
}

// GET /ui/plan — dashboard/CRM tab chama pra mostrar banner com status
// do plano + dias restantes do trial. Resposta tambem inclui dicas de
// upgrade quando aplicavel.
func (h *handlers) uiTenantPlan(c *fiber.Ctx) error {
	ctx := c.Context()
	domain, err := h.resolveDashboardDomain(ctx, c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	plan, err := h.repo.GetTenantPlan(ctx, domain)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	if plan == nil {
		// Idempotente: cria trial agora (cobre clientes antigos sem row).
		_ = h.repo.EnsureTenantTrial(ctx, domain)
		plan, _ = h.repo.GetTenantPlan(ctx, domain)
	}
	summary := planSummary(plan)
	summary["domain"] = domain
	// Calcula dias restantes pra UI mostrar countdown.
	if plan != nil && plan.Status == "trial" && plan.TrialEndsAt != nil {
		days := int(time.Until(*plan.TrialEndsAt).Hours() / 24)
		if days < 0 {
			days = 0
		}
		summary["trial_days_remaining"] = days
	}
	// Contagem de sessoes vs limite — pra UI mostrar "2/10".
	count, _ := h.repo.CountActiveSessionsByDomain(ctx, domain)
	summary["sessions_used"] = count
	if plan != nil && plan.HasProFeatures() {
		summary["sessions_limit"] = maxSessionsPro
	} else {
		summary["sessions_limit"] = maxSessionsBasic
	}
	return c.JSON(summary)
}
