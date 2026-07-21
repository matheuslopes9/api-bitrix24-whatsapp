package api

// Handlers de gestao de planos. Dois grupos:
//   1. Super-admin (cookie admin obrigatorio): GET/POST /admin/api/tenant/plan
//      e GET /admin/api/tenant/plans. Pra UC Technology gerenciar manualmente.
//   2. Tenant (cookie tenant): GET /ui/plan. Pra o /dashboard mostrar banner
//      com dias restantes + chamada pra upgrade.

import (
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"go.uber.org/zap"
)

// ─── Construtor de planos (plan_definitions) ───────────────────────────────

// GET /admin/api/plan-defs — lista todas as definicoes de plano.
func (h *handlers) adminListPlanDefs(c *fiber.Ctx) error {
	defs, err := h.repo.ListPlanDefinitions(c.Context(), false)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"plans": defs})
}

// POST /admin/api/plan-defs — cria/atualiza um plano (upsert por code).
func (h *handlers) adminSavePlanDef(c *fiber.Ctx) error {
	var p db.PlanDefinition
	if err := c.BodyParser(&p); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "JSON invalido"})
	}
	p.Code = strings.ToLower(strings.TrimSpace(p.Code))
	p.Name = strings.TrimSpace(p.Name)
	if p.Code == "" || p.Name == "" {
		return c.Status(400).JSON(fiber.Map{"error": "code e name obrigatorios"})
	}
	if p.MaxSessions < 1 {
		p.MaxSessions = 1
	}
	// Plano marcado como trial precisa de dias > 0, senao o cliente nasceria
	// com o teste ja vencido.
	if p.IsTrialDefault && p.TrialDays <= 0 {
		return c.Status(400).JSON(fiber.Map{
			"error": "defina os dias de teste (maior que zero) para o plano de trial",
		})
	}
	if p.TrialDays < 0 {
		p.TrialDays = 0
	}
	if err := h.repo.UpsertPlanDefinition(c.Context(), &p); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.repo.WriteAudit(c.Context(), h.adminActor(c), "plan.save", p.Code,
		"preco="+strconv.FormatInt(p.PriceCents, 10), clientIP(c))
	return c.JSON(fiber.Map{"ok": true, "code": p.Code})
}

// POST /admin/api/plan-defs/delete — remove plano (bloqueia se em uso).
func (h *handlers) adminDeletePlanDef(c *fiber.Ctx) error {
	var req struct {
		Code string `json:"code"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "JSON invalido"})
	}
	req.Code = strings.ToLower(strings.TrimSpace(req.Code))
	ok, err := h.repo.DeletePlanDefinition(c.Context(), req.Code)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	if !ok {
		return c.Status(409).JSON(fiber.Map{
			"error": "plano em uso por tenants — mova-os pra outro plano antes de excluir",
		})
	}
	h.repo.WriteAudit(c.Context(), h.adminActor(c), "plan.delete", req.Code, "", clientIP(c))
	return c.JSON(fiber.Map{"ok": true})
}

// ─── Config do gateway de pagamento (billing_config) ───────────────────────

// GET /admin/api/billing-config — config atual (SEM a merchant_key).
// Inclui flags indicando o que veio do banco vs env (pro admin saber).
func (h *handlers) adminGetBillingConfig(c *fiber.Ctx) error {
	row, err := h.repo.GetBillingConfig(c.Context())
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	if row == nil {
		row = &db.BillingConfigRow{Environment: "sandbox", ProcessorBoleto: "12", ProcessorPix: "206", ProcessorCard: "1", ActivateDays: 30}
	}
	return c.JSON(fiber.Map{
		"provider":          row.Provider,
		"environment":       row.Environment,
		"merchant_id":       row.MerchantID,
		"has_merchant_key":  row.MerchantKey != "",
		"processor_boleto":  row.ProcessorBoleto,
		"processor_pix":     row.ProcessorPix,
		"processor_card":    row.ProcessorCard,
		"activate_days":     row.ActivateDays,
		"enabled":           row.Enabled,
		"updated_at":        row.UpdatedAt.Format(time.RFC3339),
		// Fallback do env (informativo — mostra o que valeria se o banco vazio)
		"env_merchant_id":   h.cfg.Billing.MaxiPagoMerchantID,
		"env_has_key":       h.cfg.Billing.MaxiPagoMerchantKey != "",
		"postback_url":      h.cfg.App.BaseURL() + "/billing/maxipago/postback",
	})
}

// POST /admin/api/billing-config — salva. merchant_key so' e' atualizada se
// vier preenchida (front manda vazio pra preservar a atual).
func (h *handlers) adminSaveBillingConfig(c *fiber.Ctx) error {
	var req struct {
		Environment     string `json:"environment"`
		MerchantID      string `json:"merchant_id"`
		MerchantKey     string `json:"merchant_key"`
		ProcessorBoleto string `json:"processor_boleto"`
		ProcessorPix    string `json:"processor_pix"`
		ProcessorCard   string `json:"processor_card"`
		ActivateDays    int    `json:"activate_days"`
		TrialDays       int    `json:"trial_days"`
		Enabled         bool   `json:"enabled"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "JSON invalido"})
	}
	env := strings.ToLower(strings.TrimSpace(req.Environment))
	if env != "production" {
		env = "sandbox"
	}
	if req.ActivateDays <= 0 {
		req.ActivateDays = 30
	}
	// trial_days saiu desta tela (agora e' por plano, na aba Planos).
	// Preserva o valor atual do banco pra nao zerar a coluna legada.
	if req.TrialDays <= 0 {
		if cur, _ := h.repo.GetBillingConfig(c.Context()); cur != nil && cur.TrialDays > 0 {
			req.TrialDays = cur.TrialDays
		} else {
			req.TrialDays = 7
		}
	}
	keyChanged := strings.TrimSpace(req.MerchantKey) != ""
	row := &db.BillingConfigRow{
		Provider:        "maxipago",
		Environment:     env,
		MerchantID:      strings.TrimSpace(req.MerchantID),
		MerchantKey:     strings.TrimSpace(req.MerchantKey),
		ProcessorBoleto: strings.TrimSpace(req.ProcessorBoleto),
		ProcessorPix:    strings.TrimSpace(req.ProcessorPix),
		ProcessorCard:   strings.TrimSpace(req.ProcessorCard),
		ActivateDays:    req.ActivateDays,
		TrialDays:       req.TrialDays,
		Enabled:         req.Enabled,
	}
	if err := h.repo.SaveBillingConfig(c.Context(), row, keyChanged); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.repo.WriteAudit(c.Context(), h.adminActor(c), "billing.config", env,
		"merchant="+row.MerchantID+" enabled="+boolStr(row.Enabled), clientIP(c))
	return c.JSON(fiber.Map{"ok": true})
}

// POST /admin/api/billing-config/test?method=pix|boleto — testa as
// credenciais gerando uma cobranca real de valor baixo e reportando o que o
// gateway respondeu. Nao persiste charge. Devolve o XML bruto pra debug.
func (h *handlers) adminTestBillingConfig(c *fiber.Ctx) error {
	bc := h.effectiveBilling(c.Context())
	if bc.MaxiPagoMerchantID == "" || bc.MaxiPagoMerchantKey == "" {
		return c.Status(400).JSON(fiber.Map{"ok": false, "error": "merchant_id/key nao configurados"})
	}
	method := strings.ToLower(strings.TrimSpace(c.Query("method")))
	if method == "" {
		method = "pix"
	}
	ref := "test-" + strings.ReplaceAll(uuid.New().String(), "-", "")[:12]

	var mpResp *mpTransactionResponse
	var raw string
	var err error
	// Valor de teste: R$ 1,00 (alguns processadores recusam centavos).
	const testAmount = int64(100)
	if method == "boleto" {
		mpResp, raw, err = h.maxipagoCreateBoleto(c.Context(), bc, ref, "Teste UC Talk", "Teste",
			testAmount, time.Now().AddDate(0, 0, 3))
	} else {
		mpResp, raw, err = h.maxipagoCreatePix(c.Context(), bc, ref, "Teste UC Talk", testAmount, 600)
	}
	if err != nil {
		return c.JSON(fiber.Map{"ok": false, "method": method, "error": err.Error(), "raw": truncateStr(raw, 1500)})
	}

	payload := mpResp.pixPayload()
	ok := mpResp.ResponseCode == "0" &&
		((method == "pix" && payload != "") || (method == "boleto" && mpResp.BoletoURL != ""))

	// Dica interpretando o erro pro admin saber o que fazer.
	hint := ""
	switch {
	case ok:
		hint = "Credenciais e processador OK."
	case mpResp.ResponseCode == "1":
		hint = "O gateway autenticou mas RECUSOU a transação. Normalmente significa que o " +
			"processador (" + firstNonEmpty(bc.ProcessorPix, "?") + " para PIX) não está habilitado " +
			"nesta conta, ou o meio de pagamento não está contratado. Confirme com o suporte maxiPago " +
			"qual processorID usar na sua loja."
	case strings.Contains(strings.ToLower(raw), "authentication") || mpResp.ResponseCode == "1024":
		hint = "Falha de autenticação — confira Merchant ID e Merchant Key."
	default:
		hint = "Veja a resposta bruta abaixo e confirme com o suporte maxiPago."
	}

	return c.JSON(fiber.Map{
		"ok":            ok,
		"method":        method,
		"response_code": mpResp.ResponseCode,
		"message":       firstNonEmpty(mpResp.ResponseMessage, mpResp.ErrorMessage, mpResp.ProcessorMsg),
		"processor_msg": mpResp.ProcessorMsg,
		"error_message": mpResp.ErrorMessage,
		"order_id":      mpResp.OrderID,
		"processor_id":  map[string]string{"pix": bc.ProcessorPix, "boleto": bc.ProcessorBoleto}[method],
		"environment":   bc.MaxiPagoEnv,
		"hint":          hint,
		"raw":           truncateStr(raw, 1500),
	})
}

// GET /admin/api/metrics — KPIs globais pro dashboard super-admin.
func (h *handlers) adminMetrics(c *fiber.Ctx) error {
	m, err := h.repo.GetAdminMetrics(c.Context())
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(m)
}

// GET /admin/api/billing/charges — ultimas cobrancas de todos os tenants.
func (h *handlers) adminBillingCharges(c *fiber.Ctx) error {
	charges, err := h.repo.ListRecentBillingCharges(c.Context(), 40)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"charges": charges})
}

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
	// Features resolvidas (via plan_definitions configuravel + fallback).
	feat := h.resolveTenantFeatures(ctx, domain)
	count, _ := h.repo.CountActiveSessionsByDomain(ctx, domain)
	summary["sessions_used"] = count
	summary["sessions_limit"] = feat.MaxSessions
	summary["feat_templates"] = feat.Templates
	summary["feat_automations"] = feat.Automations
	summary["feat_sms"] = feat.SMS
	summary["feat_reports"] = feat.Reports
	// has_pro_features agora reflete "tem alguma feature avancada" (pra a UI
	// decidir mostrar/esconder abas). Preservado pra compat.
	summary["has_pro_features"] = feat.hasAnyAdvanced()
	return c.JSON(summary)
}

// ─── Atalhos pra UI admin ──────────────────────────────────────────────────
//
// Cada endpoint chama SetTenantPlan com os parametros certos, evitando que
// o front tenha que reconstruir o body inteiro. Idempotentes — chamar
// 2x produz o mesmo estado final.

// POST /admin/api/tenant/plan/extend-trial
// Body: {"domain":"x.bitrix24.com","days":7}
// Adiciona N dias ao trial atual (ou recria trial se ja' expirou).
// status='trial', plan='basic'. Limpa active_until.
func (h *handlers) adminTenantExtendTrial(c *fiber.Ctx) error {
	var req struct {
		Domain string `json:"domain"`
		Days   int    `json:"days"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "JSON invalido"})
	}
	domain := normalizePortalDomain(strings.TrimSpace(req.Domain))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain obrigatorio"})
	}
	if req.Days <= 0 || req.Days > 365 {
		return c.Status(400).JSON(fiber.Map{"error": "days deve ser 1..365"})
	}
	// Pega plano atual pra calcular novo trial_ends_at.
	cur, _ := h.repo.GetTenantPlan(c.Context(), domain)
	base := time.Now()
	if cur != nil && cur.TrialEndsAt != nil && cur.TrialEndsAt.After(base) {
		// Trial ainda valido — soma os dias ao fim atual.
		base = *cur.TrialEndsAt
	}
	newEnd := base.Add(time.Duration(req.Days) * 24 * time.Hour)
	notes := ""
	if cur != nil {
		notes = cur.Notes
	}
	// SetTenantPlan nao mexe em trial_ends_at — usamos query direta.
	if err := h.repo.SetTenantTrialEndsAt(c.Context(), domain, newEnd, notes); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.log.Info("admin: trial extended",
		zap.String("domain", domain),
		zap.Int("days_added", req.Days),
		zap.Time("new_end", newEnd))
	plan, _ := h.repo.GetTenantPlan(c.Context(), domain)
	return c.JSON(fiber.Map{
		"ok":            true,
		"domain":        domain,
		"days_added":    req.Days,
		"trial_ends_at": newEnd.Format(time.RFC3339),
		"plan":          planSummary(plan),
	})
}

// POST /admin/api/tenant/plan/activate-pro
// Body: {"domain":"x.bitrix24.com","active_until":"2027-12-31T23:59:59Z","notes":"..."}
// Se active_until vazio, ativa Pro vitalicio (NULL = sem expiracao).
func (h *handlers) adminTenantActivatePro(c *fiber.Ctx) error {
	var req struct {
		Domain      string `json:"domain"`
		ActiveUntil string `json:"active_until"`
		Notes       string `json:"notes"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "JSON invalido"})
	}
	domain := normalizePortalDomain(strings.TrimSpace(req.Domain))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain obrigatorio"})
	}
	var activeUntil *time.Time
	if s := strings.TrimSpace(req.ActiveUntil); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			// Tenta formato simples YYYY-MM-DD (data-picker do navegador)
			t, err = time.Parse("2006-01-02", s)
			if err != nil {
				return c.Status(400).JSON(fiber.Map{"error": "active_until invalido (use YYYY-MM-DD ou ISO 8601)"})
			}
			// Vai ate o fim do dia escolhido.
			t = time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, time.UTC)
		}
		activeUntil = &t
	}
	if err := h.repo.SetTenantPlan(c.Context(), domain, "pro", "active",
		activeUntil, strings.TrimSpace(req.Notes)); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	label := "vitalicio"
	if activeUntil != nil {
		label = activeUntil.Format("2006-01-02")
	}
	h.log.Info("admin: Pro ativado",
		zap.String("domain", domain),
		zap.String("until", label))
	plan, _ := h.repo.GetTenantPlan(c.Context(), domain)
	return c.JSON(fiber.Map{
		"ok":     true,
		"domain": domain,
		"plan":   planSummary(plan),
	})
}

// POST /admin/api/tenant/plan/suspend
// Body: {"domain":"x.bitrix24.com","notes":"motivo"}
// Suspende a conta — cliente ve "plano suspenso" e tudo /ui/* retorna 402.
func (h *handlers) adminTenantSuspend(c *fiber.Ctx) error {
	var req struct {
		Domain string `json:"domain"`
		Notes  string `json:"notes"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "JSON invalido"})
	}
	domain := normalizePortalDomain(strings.TrimSpace(req.Domain))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain obrigatorio"})
	}
	// Preserva plan atual (basic|pro) mas muda status pra suspended.
	cur, _ := h.repo.GetTenantPlan(c.Context(), domain)
	plan := "basic"
	if cur != nil && cur.Plan != "" {
		plan = cur.Plan
	}
	if err := h.repo.SetTenantPlan(c.Context(), domain, plan, "suspended",
		nil, strings.TrimSpace(req.Notes)); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.log.Info("admin: tenant suspenso", zap.String("domain", domain))
	plan2, _ := h.repo.GetTenantPlan(c.Context(), domain)
	return c.JSON(fiber.Map{"ok": true, "domain": domain, "plan": planSummary(plan2)})
}

// POST /admin/api/tenant/plan/reactivate
// Body: {"domain":"x.bitrix24.com"}
// Tira de suspended. Volta pro plan atual com status='active' (Pro) ou
// recria trial 7d (Basico/sem plano).
func (h *handlers) adminTenantReactivate(c *fiber.Ctx) error {
	var req struct {
		Domain string `json:"domain"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "JSON invalido"})
	}
	domain := normalizePortalDomain(strings.TrimSpace(req.Domain))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain obrigatorio"})
	}
	cur, _ := h.repo.GetTenantPlan(c.Context(), domain)
	if cur != nil && cur.Plan == "pro" {
		// Reativa Pro mantendo active_until original.
		if err := h.repo.SetTenantPlan(c.Context(), domain, "pro", "active",
			cur.ActiveUntil, cur.Notes); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
	} else {
		// Sem plano ou basic — recria trial 7d.
		newEnd := time.Now().Add(7 * 24 * time.Hour)
		notes := ""
		if cur != nil {
			notes = cur.Notes
		}
		if err := h.repo.SetTenantTrialEndsAt(c.Context(), domain, newEnd, notes); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
	}
	h.log.Info("admin: tenant reativado", zap.String("domain", domain))
	plan, _ := h.repo.GetTenantPlan(c.Context(), domain)
	return c.JSON(fiber.Map{"ok": true, "domain": domain, "plan": planSummary(plan)})
}
