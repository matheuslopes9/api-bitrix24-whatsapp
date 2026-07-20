// plan_subscription.go — endpoints que o CLIENTE (tenant) usa pra gerenciar
// a propria assinatura no dashboard: ver detalhes, cancelar e reativar.
//
// Modelo de cancelamento (padrao SaaS): cancelar NAO corta o acesso na hora.
// O cliente continua usando ate o fim do periodo pago (active_until) e nao
// renova. Ex: assinou 30d, cancelou no 15o -> usa ate o 30o. Reativar antes
// do vencimento volta a renovar normalmente.
package api

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"
)

// GET /ui/plans — planos ATIVOS disponiveis pro cliente assinar (do banco,
// configurados pelo admin). Cliente monta os cards a partir daqui.
func (h *handlers) uiListPlans(c *fiber.Ctx) error {
	defs, err := h.repo.ListPlanDefinitions(c.Context(), true)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	out := make([]map[string]interface{}, 0, len(defs))
	for _, d := range defs {
		out = append(out, planDefToClient(d))
	}
	return c.JSON(fiber.Map{"plans": out})
}

// GET /ui/plan/details — visao completa da assinatura pro cliente:
// estado, plano, dias restantes, data de vencimento/renovacao, se esta
// cancelando, e o historico de cobrancas do tenant.
func (h *handlers) uiPlanDetails(c *fiber.Ctx) error {
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
		_ = h.repo.EnsureTenantTrial(ctx, domain)
		plan, _ = h.repo.GetTenantPlan(ctx, domain)
	}

	out := fiber.Map{
		"domain":            domain,
		"plan":              "none",
		"state":             "expired",
		"has_pro_features":  false,
		"is_access_allowed": false,
	}
	if plan != nil {
		out["plan"] = plan.Plan
		out["status"] = plan.Status
		out["state"] = plan.SubscriptionState()
		out["has_pro_features"] = plan.HasProFeatures()
		out["is_access_allowed"] = plan.IsAccessAllowed()
		out["cancel_at_period_end"] = plan.CancelAtPeriodEnd

		// Data que importa pro cliente: fim do periodo (renovacao ou fim do
		// acesso). Trial usa trial_ends_at; pago usa active_until.
		var periodEnd *time.Time
		if plan.Status == "trial" {
			periodEnd = plan.TrialEndsAt
		} else {
			periodEnd = plan.ActiveUntil
		}
		if periodEnd != nil {
			out["period_end"] = periodEnd.Format(time.RFC3339)
			days := int(time.Until(*periodEnd).Hours() / 24)
			if days < 0 {
				days = 0
			}
			out["days_remaining"] = days

			// Lembrete de renovacao: assinatura PAGA (nao cancelada) vencendo
			// em <=5 dias. O cliente ve um banner "renove" com botao que gera
			// novo boleto. Trial tem o proprio countdown, entao so' pra active.
			if plan.Status == "active" && !plan.CancelAtPeriodEnd && days <= 5 {
				out["renewal_soon"] = true
				out["renewal_days"] = days
			}
		}
		if plan.CancelledAt != nil {
			out["cancelled_at"] = plan.CancelledAt.Format(time.RFC3339)
		}
	}

	// Sessoes vs limite (do plano configuravel, via resolveTenantFeatures).
	feat := h.resolveTenantFeatures(ctx, domain)
	count, _ := h.repo.CountActiveSessionsByDomain(ctx, domain)
	out["sessions_used"] = count
	out["sessions_limit"] = feat.MaxSessions

	// Precos vigentes (pra UI montar os cards de assinatura).
	out["price_basic_cents"] = h.cfg.Billing.BasicPriceCents
	out["price_pro_cents"] = h.cfg.Billing.ProPriceCents
	out["billing_configured"] = h.maxipagoConfigured()

	// Historico de cobrancas do tenant.
	charges, _ := h.repo.ListBillingChargesByDomain(ctx, domain, 12)
	out["charges"] = charges

	return c.JSON(out)
}

// POST /ui/plan/cancel — cliente cancela a renovacao. Mantem acesso ate
// active_until. Idempotente.
func (h *handlers) uiPlanCancel(c *fiber.Ctx) error {
	ctx := c.Context()
	domain, err := h.resolveDashboardDomain(ctx, c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	plan, err := h.repo.GetTenantPlan(ctx, domain)
	if err != nil || plan == nil {
		return c.Status(404).JSON(fiber.Map{"error": "plano nao encontrado"})
	}
	// So' faz sentido cancelar assinatura paga ativa.
	if plan.Status != "active" {
		return c.Status(400).JSON(fiber.Map{
			"error": "so' assinaturas ativas podem ser canceladas",
			"state": plan.SubscriptionState(),
		})
	}
	if err := h.repo.SetPlanCancellation(ctx, domain, true); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.log.Info("plan: cliente cancelou assinatura (uso ate o vencimento)",
		zap.String("domain", domain))

	msg := "Assinatura cancelada. Você continua com acesso completo até o fim do período já pago."
	if plan.ActiveUntil != nil {
		msg = "Assinatura cancelada. Você continua com acesso até " +
			plan.ActiveUntil.Format("02/01/2006") + " e não será cobrado novamente."
	}
	return c.JSON(fiber.Map{"ok": true, "message": msg})
}

// POST /ui/plan/reactivate — reverte o cancelamento (volta a renovar).
// So' funciona se ainda esta dentro do periodo pago.
func (h *handlers) uiPlanReactivate(c *fiber.Ctx) error {
	ctx := c.Context()
	domain, err := h.resolveDashboardDomain(ctx, c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	plan, err := h.repo.GetTenantPlan(ctx, domain)
	if err != nil || plan == nil {
		return c.Status(404).JSON(fiber.Map{"error": "plano nao encontrado"})
	}
	if !plan.IsAccessAllowed() {
		return c.Status(400).JSON(fiber.Map{
			"error": "periodo ja' encerrado — assine novamente pra reativar",
		})
	}
	if err := h.repo.SetPlanCancellation(ctx, domain, false); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.log.Info("plan: cliente reativou assinatura (volta a renovar)",
		zap.String("domain", domain))
	return c.JSON(fiber.Map{"ok": true, "message": "Renovação reativada. Sua assinatura continua normalmente."})
}
