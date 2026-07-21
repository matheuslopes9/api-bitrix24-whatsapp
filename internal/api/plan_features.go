// plan_features.go — resolve as features/limites de um tenant a partir da
// definicao de plano configuravel (plan_definitions), com fallback pro
// comportamento hardcoded legado. Substitui os gates baseados em
// HasProFeatures() por consulta as flags do plano.
package api

import (
	"context"

	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
)

// TenantFeatures e' o conjunto resolvido de features/limites de um tenant.
type TenantFeatures struct {
	AccessAllowed   bool // acesso minimo (nao expirado/suspenso)
	MaxSessions     int
	Templates       bool // templates + Cloud API Meta
	Automations     bool // robots BizProc
	SMS             bool // campanhas SMS
	Reports         bool // relatorios + historico longo
	PlanCode        string
	PlanName        string
	IsPro           bool // rotulo (compat)
}

// resolveTenantFeatures cruza o tenant_plan (status/vigencia) com a
// plan_definition (flags). Fallback: se nao ha definicao no banco, usa a
// regra antiga (plan=='pro' && access -> tudo; senao so' conexao).
func (h *handlers) resolveTenantFeatures(ctx context.Context, domain string) *TenantFeatures {
	f := &TenantFeatures{MaxSessions: maxSessionsBasic}

	plan, _ := h.repo.GetTenantPlan(ctx, domain)
	if plan == nil {
		return f
	}
	f.AccessAllowed = plan.IsAccessAllowed()
	f.PlanCode = plan.Plan
	if !f.AccessAllowed {
		// Sem acesso: nenhuma feature, limite minimo.
		return f
	}

	def, _ := h.repo.GetPlanDefinition(ctx, plan.Plan)
	if def == nil {
		// Fallback legado: pro libera tudo; basico so' conexao.
		if plan.HasProFeatures() {
			f.MaxSessions = maxSessionsPro
			f.Templates, f.Automations, f.SMS, f.Reports, f.IsPro = true, true, true, true, true
			f.PlanName = "Pro"
		} else {
			f.PlanName = "Básico"
		}
		return f
	}

	// As flags do PLANO valem — inclusive em trial. O Trial e' um plano
	// separado (code 'trial') com as proprias features, configuraveis na aba
	// Planos: se o admin quiser dar Pro completo no teste, e' so' marcar as
	// features no plano Trial. Vale enquanto IsAccessAllowed (ja' checado).
	f.PlanName = def.Name
	f.IsPro = def.IsPro
	f.MaxSessions = def.MaxSessions
	f.Templates = def.FeatTemplates
	f.Automations = def.FeatAutomations
	f.SMS = def.FeatSMS
	f.Reports = def.FeatReports
	return f
}

// hasProLike retorna true se o tenant tem QUALQUER feature avancada — usado
// pra decidir o gate generico "requireProPlan" (que na pratica bloqueia
// features avancadas). Consideramos "pro-like" quem tem templates OU
// automations OU sms OU reports.
func (f *TenantFeatures) hasAnyAdvanced() bool {
	return f.Templates || f.Automations || f.SMS || f.Reports
}

// planDefToClient serializa uma PlanDefinition pro cliente (sem campos admin).
func planDefToClient(p *db.PlanDefinition) map[string]interface{} {
	return map[string]interface{}{
		"code":             p.Code,
		"name":             p.Name,
		"description":      p.Description,
		"price_cents":      p.PriceCents,
		"max_sessions":     p.MaxSessions,
		"feat_templates":   p.FeatTemplates,
		"feat_automations": p.FeatAutomations,
		"feat_sms":         p.FeatSMS,
		"feat_reports":     p.FeatReports,
		"is_pro":           p.IsPro,
	}
}
