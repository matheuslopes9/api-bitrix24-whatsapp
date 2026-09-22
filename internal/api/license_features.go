// license_features.go — resolve o que o contrato de cada cliente libera.
//
// Substitui plan_features.go + plan_gate.go do modelo SaaS antigo.
//
// MUDANCA DE FUNDO: antes, o tenant guardava o CODIGO do plano
// (tenant_plans.plan = 'pro') e as flags de feature viviam numa SEGUNDA
// tabela, plan_definitions. Bastava o catalogo dessincronizar — o que
// acontece ao editar planos pela UI, que reescreve a linha inteira — pro
// cliente ficar rotulado "Pro" e sem nenhuma feature. Era o bug do "Pro nao
// funciona".
//
// Agora os beneficios ficam na licenca do PROPRIO cliente. Uma leitura, sem
// indirecao, nada pra dessincronizar.
package api

import (
	"context"
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"go.uber.org/zap"
)

// Limite de sessoes quando o cliente ainda nao tem licenca configurada.
const maxSessionsPadrao = 1

// TenantFeatures e' o conjunto resolvido de beneficios de um cliente.
type TenantFeatures struct {
	MaxSessions int

	CloudAPI    bool // Cloud API (Meta) + Templates
	Automations bool // robos BizProc
	Reports     bool
	SMS         bool // oculto na UI por enquanto

	// Expired diz que a vigencia passou. E' so' pra AVISAR na interface:
	// nenhum caminho de codigo bloqueia por causa dele. A decisao de
	// produto e' que inadimplencia nunca derruba o atendimento do cliente
	// final — quem cobra e' o comercial.
	Expired    bool
	ValidUntil string // ISO 8601, vazio quando a licenca nao tem prazo
}

// resolveTenantFeatures le a licenca do cliente.
//
// Fail-OPEN no minimo: cliente sem licenca (portal legado, install que ainda
// nao passou pelo admin) recebe o basico — 1 sessao, sem features avancadas
// — em vez de ficar travado. Melhor um cliente com menos features do que um
// cliente sem atendimento.
func (h *handlers) resolveTenantFeatures(ctx context.Context, domain string) *TenantFeatures {
	f := &TenantFeatures{MaxSessions: maxSessionsPadrao}

	lic, err := h.repo.GetLicense(ctx, normalizePortalDomain(domain))
	if err != nil {
		h.log.Warn("licenca: leitura falhou — liberando o minimo (fail-open)",
			zap.String("domain", domain), zap.Error(err))
		return f
	}
	if lic == nil {
		return f
	}

	f.MaxSessions = lic.MaxSessions
	if f.MaxSessions < 1 {
		f.MaxSessions = maxSessionsPadrao
	}
	f.CloudAPI = lic.FeatCloudAPI
	f.Automations = lic.FeatAutomations
	f.Reports = lic.FeatReports
	f.SMS = lic.FeatSMS
	f.Expired = lic.Expired()
	if lic.ValidUntil != nil {
		f.ValidUntil = lic.ValidUntil.Format("2006-01-02")
	}
	return f
}

// licenseSummary serializa a licenca pra UI (admin e painel do cliente).
func licenseSummary(lic *db.TenantLicense) fiber.Map {
	if lic == nil {
		return fiber.Map{
			"configurada":  false,
			"max_sessions": maxSessionsPadrao,
		}
	}
	out := fiber.Map{
		"configurada":      true,
		"domain":           lic.Domain,
		"max_sessions":     lic.MaxSessions,
		"feat_cloud_api":   lic.FeatCloudAPI,
		"feat_automations": lic.FeatAutomations,
		"feat_reports":     lic.FeatReports,
		"feat_sms":         lic.FeatSMS,
		"notes":            lic.Notes,
		"expirada":         lic.Expired(),
	}
	if lic.ValidUntil != nil {
		out["valid_until"] = lic.ValidUntil.Format("2006-01-02")
		if dias, ok := lic.DaysUntilExpiry(); ok {
			out["dias_restantes"] = dias
		}
	}
	return out
}

// ─── Gates de feature ─────────────────────────────────────────────────────
//
// Os gates barram apenas o que NAO esta no contrato. Nenhum deles olha para
// vigencia: licenca vencida avisa, nao bloqueia.

// bloqueioFeature devolve o 403 padrao de "fora do contrato".
//
// 403, e nao o 402 Payment Required de antes: nao ha' mais auto-servico de
// pagamento pro cliente destravar sozinho. O recurso simplesmente nao faz
// parte do que ele contratou, e quem resolve isso e' o comercial.
func bloqueioFeature(c *fiber.Ctx, recurso string) error {
	return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
		"error": "recurso não incluído no seu contrato: " + recurso,
		"code":  "feature_nao_contratada",
		"hint":  "Fale com a UC Technology para incluir esse recurso.",
	})
}

// requireCloudAPI protege as rotas de Cloud API (Meta) e Templates.
func (h *handlers) requireCloudAPI(c *fiber.Ctx) error {
	domain, ok := c.Locals("tenant_domain").(string)
	if !ok || domain == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "tenant nao identificado"})
	}
	if !h.resolveTenantFeatures(c.Context(), domain).CloudAPI {
		return bloqueioFeature(c, "WhatsApp Cloud API e Templates")
	}
	return c.Next()
}

// requireAutomations protege as rotas dos robos BizProc.
func (h *handlers) requireAutomations(c *fiber.Ctx) error {
	domain, ok := c.Locals("tenant_domain").(string)
	if !ok || domain == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "tenant nao identificado"})
	}
	if !h.resolveTenantFeatures(c.Context(), domain).Automations {
		return bloqueioFeature(c, "Automações (robôs BizProc)")
	}
	return c.Next()
}

// requireSessionSlot checa se o cliente pode adicionar mais uma sessao.
// O limite vem do contrato (tenant_licenses.max_sessions).
func (h *handlers) requireSessionSlot(ctx context.Context, domain string) error {
	domain = normalizePortalDomain(domain)
	count, err := h.repo.CountActiveSessionsByDomain(ctx, domain)
	if err != nil {
		return err
	}
	limite := h.resolveTenantFeatures(ctx, domain).MaxSessions
	if count >= limite {
		return fiber.NewError(fiber.StatusForbidden,
			"limite de números do contrato atingido: "+
				strconv.Itoa(count)+"/"+strconv.Itoa(limite)+
				". Fale com a UC Technology para ampliar.")
	}
	return nil
}
