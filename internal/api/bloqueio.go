// bloqueio.go — bloquear e desbloquear um cliente pelo painel admin.
//
// Bloqueado, o cliente deixa de usar o UC Talk por qualquer caminho:
//
//   - mensagem do WhatsApp nao entra no Bitrix (bitrix/processor.go e o
//     caminho de midia grande em cmd/server/qr_inbound_media.go);
//   - mensagem do operador nao sai — chat do Bitrix, aba do CRM, robo e API
//     passam todos pelo worker de saida (cmd/server/confirmacao.go);
//   - painel do cliente (/ui/*) e aba do CRM (/bitrix/crm/*) respondem 403
//     com codigo "bloqueado", e as telas mostram o aviso (bloqueioJS).
//
// O admin continua vendo tudo — e' quem resolve. O cadastro fica intacto (ver
// db/bloqueio.go pro porque de nao apagar), e desbloquear devolve o acesso na
// hora.
package api

import (
	_ "embed"
	"strings"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"
)

// bloqueioJS cobre a tela do cliente com o aviso quando uma chamada volta
// "bloqueado". Entra no <head> das tres telas do cliente, antes de qualquer
// script delas, pra pegar ja' a primeira chamada.
//
//go:embed assets/bloqueio.js
var bloqueioJS string

func init() {
	tag := "<head>\n<script>" + bloqueioJS + "</script>"
	dashboardHTML = strings.Replace(dashboardHTML, "<head>", tag, 1)
	crmTabHTML = strings.Replace(crmTabHTML, "<head>", tag, 1)
	bitrixAppMenuHTML = strings.Replace(bitrixAppMenuHTML, "<head>", tag, 1)
}

// respostaBloqueado e' o 403 que as telas do cliente reconhecem. O motivo
// do bloqueio e' anotacao interna e NAO vai pro cliente.
func respostaBloqueado(c *fiber.Ctx) error {
	return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
		"error":     "acesso ao UC Talk bloqueado para esta empresa — fale com a UC Technology",
		"codigo":    "bloqueado",
		"bloqueado": true,
	})
}

// POST /admin/api/tenant/bloqueio — bloqueia ou desbloqueia um cliente.
//
// Body: {"domain":"...","bloquear":true,"motivo":"contrato encerrado"}
func (h *handlers) adminBloqueioTenant(c *fiber.Ctx) error {
	var body struct {
		Domain   string `json:"domain"`
		Bloquear bool   `json:"bloquear"`
		Motivo   string `json:"motivo"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "body invalido"})
	}
	dominio := normalizePortalDomain(strings.TrimSpace(body.Domain))
	if dominio == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain obrigatorio"})
	}
	motivo := strings.TrimSpace(body.Motivo)
	// Motivo obrigatorio: bloquear corta o atendimento de uma empresa
	// inteira. Daqui a meses, "por que esse cliente esta' bloqueado?" precisa
	// de resposta na propria tela.
	if body.Bloquear && motivo == "" {
		return c.Status(400).JSON(fiber.Map{"error": "informe o motivo do bloqueio (ex: contrato encerrado em 10/2026)"})
	}

	ator := h.adminActor(c)
	var alterou bool
	var err error
	if body.Bloquear {
		alterou, err = h.repo.BloquearPortal(c.Context(), dominio, motivo, ator)
	} else {
		alterou, err = h.repo.DesbloquearPortal(c.Context(), dominio)
	}
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	if !alterou {
		if body.Bloquear {
			return c.Status(404).JSON(fiber.Map{"error": "cliente nao encontrado: " + dominio})
		}
		return c.JSON(fiber.Map{"ok": true, "mensagem": dominio + " nao estava bloqueado"})
	}

	acao, msg := "tenant.desbloquear", "acesso liberado para "+dominio
	if body.Bloquear {
		acao, msg = "tenant.bloquear", dominio+" bloqueado — mensagens nao entram nem saem, e o painel do cliente mostra o aviso. O cadastro continua intacto."
	}
	h.log.Warn("tenant: bloqueio alterado",
		zap.String("domain", dominio), zap.Bool("bloqueado", body.Bloquear),
		zap.String("motivo", motivo), zap.String("por", ator))
	h.repo.WriteAudit(c.Context(), ator, acao, dominio, motivo, clientIP(c))
	return c.JSON(fiber.Map{"ok": true, "bloqueado": body.Bloquear, "mensagem": msg})
}
