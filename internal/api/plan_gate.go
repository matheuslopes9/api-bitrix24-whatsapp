package api

// Sistema de planos: Basico (default trial) vs Pro (full features).
//
// Politica:
//   - Trial automatico de 7 dias no install (plan=basic, status=trial)
//   - Apos 7d, status=expired -> backend bloqueia tudo, UI mostra "upgrade"
//   - Super-admin ativa Pro manualmente via /admin/api/tenant/plan
//
// Features gateadas (so Pro):
//   - Cloud API session (POST /ui/sessions/cloud)
//   - SMS Campaigns (config + dispatch)
//   - Templates (CRUD + import Meta) — Basico nao ve templates
//   - Multi-sessao (>1 sessao ativa por tenant Basico, ate 10 no Pro)
//   - BizProc robots (atualizar dropdowns / refresh)
//   - Dashboard avancado (relatorios, historico longo)
//
// Features liberadas no Basico:
//   - 1 sessao QR ou 1 Cloud (so conexao)
//   - Aba CRM (envio/recebimento inline em Lead/Deal/Contato)
//   - Permissoes basicas (master user)
//
// 402 Payment Required: convenção HTTP pra "feature paga".

import (
	"context"
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"go.uber.org/zap"
)

// Limite de sessoes ativas por plano.
const (
	maxSessionsBasic = 1
	maxSessionsPro   = 10
)

// planSummary serializa o TenantPlan pra UI/API.
func planSummary(p *db.TenantPlan) map[string]interface{} {
	if p == nil {
		return map[string]interface{}{
			"plan":   "none",
			"status": "unknown",
		}
	}
	out := map[string]interface{}{
		"plan":              p.Plan,
		"status":            p.Status,
		"is_access_allowed": p.IsAccessAllowed(),
		"has_pro_features":  p.HasProFeatures(),
	}
	if p.TrialEndsAt != nil {
		out["trial_ends_at"] = p.TrialEndsAt.Format("2006-01-02T15:04:05Z07:00")
	}
	if p.ActiveUntil != nil {
		out["active_until"] = p.ActiveUntil.Format("2006-01-02T15:04:05Z07:00")
	}
	return out
}

// requireProPlan bloqueia se o tenant nao tem plano Pro ativo. Usado em
// rotas de feature exclusiva (cloud session, sms, templates, etc).
// Resposta 402 Payment Required + JSON com instrucao de upgrade.
//
// Resolve o domain do c.Locals (setado pelo requireTenantOrAdmin que ja'
// rodou antes neste handler). Se nao tem, devolve 401.
func (h *handlers) requireProPlan(c *fiber.Ctx) error {
	domain, ok := c.Locals("tenant_domain").(string)
	if !ok || domain == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "tenant nao identificado"})
	}
	plan, err := h.repo.GetTenantPlan(c.Context(), domain)
	if err != nil {
		h.log.Error("plan gate: GetTenantPlan failed",
			zap.String("domain", domain), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "plan lookup failed"})
	}
	if plan == nil || !plan.IsAccessAllowed() {
		return c.Status(fiber.StatusPaymentRequired).JSON(fiber.Map{
			"error":   "plano expirado ou inativo",
			"code":    "plan_expired",
			"plan":    planSummary(plan),
			"upgrade": "Entre em contato com o comercial UC Technology pra ativar o plano Pro.",
		})
	}
	// Gate por features CONFIGURAVEIS: bloqueia se o plano do tenant nao tem
	// nenhuma feature avancada habilitada (via plan_definitions). Fallback
	// legado dentro de resolveTenantFeatures cobre planos sem definicao.
	feat := h.resolveTenantFeatures(c.Context(), domain)
	if !feat.hasAnyAdvanced() {
		return c.Status(fiber.StatusPaymentRequired).JSON(fiber.Map{
			"error":   "recurso exclusivo de plano com features avançadas",
			"code":    "plan_pro_required",
			"plan":    planSummary(plan),
			"upgrade": "Faça upgrade do plano pra usar esse recurso.",
		})
	}
	c.Locals("tenant_plan", plan)
	return c.Next()
}

// requireBasicAccess bloqueia se o tenant nao tem acesso minimo (trial
// expirado / suspended). Usado em rotas que TODO plano usa (CRM tab,
// envio inline, etc).
func (h *handlers) requireBasicAccess(c *fiber.Ctx) error {
	domain, ok := c.Locals("tenant_domain").(string)
	if !ok || domain == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "tenant nao identificado"})
	}
	plan, err := h.repo.GetTenantPlan(c.Context(), domain)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "plan lookup failed"})
	}
	if plan == nil || !plan.IsAccessAllowed() {
		return c.Status(fiber.StatusPaymentRequired).JSON(fiber.Map{
			"error":   "plano expirado ou inativo",
			"code":    "plan_expired",
			"plan":    planSummary(plan),
			"upgrade": "Entre em contato com o comercial UC Technology pra reativar o app.",
		})
	}
	c.Locals("tenant_plan", plan)
	return c.Next()
}

// requireSessionSlot checa se o tenant pode adicionar +1 sessao ativa.
// Basico: max 1. Pro: max 10. Usado antes de criar sessao QR ou Cloud.
//
// Retorna nil se tem slot. Retorna fiber.Error 402 com detalhe se atingiu.
func (h *handlers) requireSessionSlot(ctx context.Context, domain string) error {
	plan, err := h.repo.GetTenantPlan(ctx, domain)
	if err != nil {
		return err
	}
	if plan == nil || !plan.IsAccessAllowed() {
		return fiber.NewError(fiber.StatusPaymentRequired, "plano expirado")
	}
	count, err := h.repo.CountActiveSessionsByDomain(ctx, domain)
	if err != nil {
		return err
	}
	// Limite vem da definicao de plano configuravel (com fallback legado).
	limit := h.resolveTenantFeatures(ctx, domain).MaxSessions
	if limit <= 0 {
		limit = maxSessionsBasic
	}
	if count >= limit {
		return fiber.NewError(fiber.StatusPaymentRequired,
			"limite de sessoes atingido para o plano "+plan.Plan+": "+
				strconv.Itoa(count)+"/"+strconv.Itoa(limit)+
				". Faca upgrade pra mais sessoes.")
	}
	return nil
}
