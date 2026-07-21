// coupons.go — cupons de desconto configuraveis pelo admin.
//
// Tipos:
//   percent     — desconto percentual sobre o preco do plano
//   amount      — desconto fixo em centavos
//   trial_days  — estende o trial em N dias (sem cobranca; aplicado na hora)
//
// Fluxo do cliente: na aba Assinatura ele digita o cupom -> POST
// /ui/coupon/validate mostra o desconto -> ao gerar a cobranca o cupom vai
// junto e o valor sai ja com desconto (ou o trial e' estendido).
package api

import (
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"go.uber.org/zap"
)

// ─── Admin CRUD ────────────────────────────────────────────────────────────

// GET /admin/api/coupons
func (h *handlers) adminListCoupons(c *fiber.Ctx) error {
	list, err := h.repo.ListCoupons(c.Context())
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"coupons": list})
}

// POST /admin/api/coupons — cria/atualiza (upsert por code).
func (h *handlers) adminSaveCoupon(c *fiber.Ctx) error {
	var req struct {
		Code        string `json:"code"`
		Description string `json:"description"`
		Kind        string `json:"kind"`
		Value       int    `json:"value"`
		PlanCode    string `json:"plan_code"`
		MaxUses     int    `json:"max_uses"`
		Active      bool   `json:"active"`
		ExpiresAt   string `json:"expires_at"` // YYYY-MM-DD ou vazio
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "JSON invalido"})
	}
	code := strings.ToUpper(strings.TrimSpace(req.Code))
	if code == "" {
		return c.Status(400).JSON(fiber.Map{"error": "codigo obrigatorio"})
	}
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	if kind != "percent" && kind != "amount" && kind != "trial_days" {
		return c.Status(400).JSON(fiber.Map{"error": "tipo invalido (percent|amount|trial_days)"})
	}
	if req.Value <= 0 {
		return c.Status(400).JSON(fiber.Map{"error": "valor precisa ser maior que zero"})
	}
	if kind == "percent" && req.Value > 100 {
		return c.Status(400).JSON(fiber.Map{"error": "percentual nao pode passar de 100"})
	}
	var exp *time.Time
	if s := strings.TrimSpace(req.ExpiresAt); s != "" {
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "data de validade invalida (use YYYY-MM-DD)"})
		}
		t = time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, time.UTC)
		exp = &t
	}
	cp := &db.Coupon{
		Code: code, Description: strings.TrimSpace(req.Description),
		Kind: kind, Value: req.Value,
		PlanCode: strings.ToLower(strings.TrimSpace(req.PlanCode)),
		MaxUses:  req.MaxUses, Active: req.Active, ExpiresAt: exp,
		CreatedBy: h.adminActor(c),
	}
	if err := h.repo.UpsertCoupon(c.Context(), cp); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.repo.WriteAudit(c.Context(), h.adminActor(c), "coupon.save", code,
		kind+"="+itoa(req.Value), clientIP(c))
	return c.JSON(fiber.Map{"ok": true, "code": code})
}

// POST /admin/api/coupons/delete
func (h *handlers) adminDeleteCoupon(c *fiber.Ctx) error {
	var req struct {
		Code string `json:"code"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "JSON invalido"})
	}
	if err := h.repo.DeleteCoupon(c.Context(), req.Code); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.repo.WriteAudit(c.Context(), h.adminActor(c), "coupon.delete", req.Code, "", clientIP(c))
	return c.JSON(fiber.Map{"ok": true})
}

func itoa(n int) string { return strconv.Itoa(n) }

// ─── Validacao / aplicacao (cliente) ───────────────────────────────────────

// couponCheck resultado da validacao de um cupom pra um tenant+plano.
type couponCheck struct {
	Valid          bool   `json:"valid"`
	Reason         string `json:"reason,omitempty"`
	Code           string `json:"code,omitempty"`
	Kind           string `json:"kind,omitempty"`
	Description    string `json:"description,omitempty"`
	DiscountCents  int64  `json:"discount_cents,omitempty"`
	FinalCents     int64  `json:"final_cents,omitempty"`
	TrialDaysAdded int    `json:"trial_days_added,omitempty"`
}

// validateCoupon checa validade e calcula o desconto sobre amountCents.
// Nao consome o cupom — so' avalia.
func (h *handlers) validateCoupon(c *fiber.Ctx, code, domain, planCode string, amountCents int64) couponCheck {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return couponCheck{Valid: false, Reason: "informe um cupom"}
	}
	cp, err := h.repo.GetCoupon(c.Context(), code)
	if err != nil || cp == nil {
		return couponCheck{Valid: false, Reason: "cupom não encontrado"}
	}
	if !cp.Active {
		return couponCheck{Valid: false, Reason: "cupom inativo"}
	}
	if cp.ExpiresAt != nil && cp.ExpiresAt.Before(time.Now()) {
		return couponCheck{Valid: false, Reason: "cupom expirado"}
	}
	if cp.MaxUses > 0 && cp.UsedCount >= cp.MaxUses {
		return couponCheck{Valid: false, Reason: "cupom esgotado"}
	}
	if cp.PlanCode != "" && planCode != "" && cp.PlanCode != planCode {
		return couponCheck{Valid: false, Reason: "cupom não vale para este plano"}
	}
	if domain != "" && h.repo.CouponRedeemedBy(c.Context(), code, domain) {
		return couponCheck{Valid: false, Reason: "cupom já utilizado nesta conta"}
	}

	out := couponCheck{Valid: true, Code: cp.Code, Kind: cp.Kind, Description: cp.Description}
	switch cp.Kind {
	case "percent":
		out.DiscountCents = amountCents * int64(cp.Value) / 100
	case "amount":
		out.DiscountCents = int64(cp.Value)
	case "trial_days":
		out.TrialDaysAdded = cp.Value
	}
	if out.DiscountCents > amountCents {
		out.DiscountCents = amountCents
	}
	out.FinalCents = amountCents - out.DiscountCents
	return out
}

// POST /ui/coupon/validate — body {code, plan}. Preview do desconto.
func (h *handlers) uiValidateCoupon(c *fiber.Ctx) error {
	domain, _ := c.Locals("tenant_domain").(string)
	var req struct {
		Code string `json:"code"`
		Plan string `json:"plan"`
	}
	_ = c.BodyParser(&req)
	plan := strings.ToLower(strings.TrimSpace(req.Plan))
	amount := h.planPriceCents(c, plan)
	res := h.validateCoupon(c, req.Code, domain, plan, amount)
	return c.JSON(fiber.Map{
		"check":         res,
		"original_cents": amount,
	})
}

// POST /ui/coupon/apply — body {code}. Aplica cupom de trial_days na hora
// (os de desconto sao aplicados no checkout, nao aqui).
func (h *handlers) uiApplyCoupon(c *fiber.Ctx) error {
	domain, ok := c.Locals("tenant_domain").(string)
	if !ok || domain == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "tenant nao identificado"})
	}
	var req struct {
		Code string `json:"code"`
	}
	_ = c.BodyParser(&req)
	res := h.validateCoupon(c, req.Code, domain, "", 0)
	if !res.Valid {
		return c.Status(400).JSON(fiber.Map{"error": res.Reason})
	}
	if res.Kind != "trial_days" {
		return c.Status(400).JSON(fiber.Map{
			"error": "este cupom é de desconto — use no momento de assinar",
		})
	}
	if err := h.repo.ExtendTrial(c.Context(), domain, res.TrialDaysAdded); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	if err := h.repo.RedeemCoupon(c.Context(), res.Code, domain, "", 0, res.TrialDaysAdded); err != nil {
		h.log.Warn("coupon: registrar uso falhou", zap.String("code", res.Code), zap.Error(err))
	}
	h.log.Info("coupon: trial estendido",
		zap.String("domain", domain), zap.String("code", res.Code), zap.Int("dias", res.TrialDaysAdded))
	return c.JSON(fiber.Map{
		"ok":      true,
		"message": "Cupom aplicado! Seu teste foi estendido em " + itoa(res.TrialDaysAdded) + " dia(s).",
	})
}

// planPriceCents devolve o preco do plano (do construtor de planos, com
// fallback pro env). Usado pra calcular desconto.
func (h *handlers) planPriceCents(c *fiber.Ctx, planCode string) int64 {
	if planCode == "" {
		planCode = "pro"
	}
	if def, _ := h.repo.GetPlanDefinition(c.Context(), planCode); def != nil {
		return def.PriceCents
	}
	if planCode == "basic" {
		return int64(h.cfg.Billing.BasicPriceCents)
	}
	return int64(h.cfg.Billing.ProPriceCents)
}
