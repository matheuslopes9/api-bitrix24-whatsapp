package api

// alertas_silencio.go — ligar e desligar os alertas de um cliente.
//
// POR QUE EXISTE: cliente que cancelou continua no banco, e deve continuar. O
// historico de conversas, as licencas e os pagamentos sao registro — apagar o
// cliente para parar de receber e-mail seria destruir informacao por causa de
// ruido.
//
// So' que, enquanto ele fica la', o token vence e o numero cai. O time passa a
// receber alerta de contrato encerrado, misturado com os alertas que importam.
// E alerta que nao exige acao e' pior que alerta nenhum: ensina o plantao a
// ignorar a caixa, e o proximo aviso real chega no meio do ruido que ja'
// aprenderam a pular.
//
// POR QUE SILENCIAR NAO PODE SER SILENCIOSO: um cliente mudo que volta a
// operar deixaria de ser avisado para sempre, sem ninguem notar. Entao o
// silencio aparece na tela de Alertas, na Saude do cliente e na auditoria —
// quem olhar para o cliente ve que ele esta' mudo antes de concluir que esta'
// tudo bem.

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"
)

// GET /admin/api/alertas/clientes — todos os portais, com o estado do alerta.
//
// Devolve a lista INTEIRA, nao so' os silenciados: a pergunta da tela e' "de
// quem estamos sendo avisados?", e uma lista so' de mudos nao responde isso.
func (h *handlers) adminAlertasClientes(c *fiber.Ctx) error {
	ctx := c.Context()
	portais, err := h.repo.ListBitrixPortals(ctx)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	mudos, err := h.repo.ListarSilenciados(ctx)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	porDominio := map[string]fiber.Map{}
	for _, m := range mudos {
		porDominio[normalizeDomainKey(m.Domain)] = fiber.Map{
			"motivo":         m.Motivo,
			"silenciado_em":  m.SilenciadoEm.Format("2006-01-02 15:04"),
			"silenciado_por": m.SilenciadoPor,
		}
	}

	lista := make([]fiber.Map, 0, len(portais))
	for _, p := range portais {
		// Mesma regra da lista de Tenants: linha placeholder do install pelo
		// Marketplace (domain == member_id) nao e' cliente.
		if p.Domain == p.MemberID {
			continue
		}
		item := fiber.Map{"domain": p.Domain, "silenciado": false}
		if info, mudo := porDominio[normalizeDomainKey(p.Domain)]; mudo {
			item["silenciado"] = true
			item["motivo"] = info["motivo"]
			item["silenciado_em"] = info["silenciado_em"]
			item["silenciado_por"] = info["silenciado_por"]
		}
		lista = append(lista, item)
	}
	return c.JSON(fiber.Map{
		"clientes":          lista,
		"total":             len(lista),
		"total_silenciados": len(mudos),
	})
}

// POST /admin/api/alertas/cliente — liga/desliga os alertas de um cliente.
//
// Body: {"domain":"...","silenciado":true,"motivo":"contrato encerrado"}
func (h *handlers) adminAlertasSilenciarCliente(c *fiber.Ctx) error {
	var body struct {
		Domain     string `json:"domain"`
		Silenciado bool   `json:"silenciado"`
		Motivo     string `json:"motivo"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "body invalido"})
	}
	dominio := normalizePortalDomain(strings.TrimSpace(body.Domain))
	if dominio == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain obrigatorio"})
	}
	motivo := strings.TrimSpace(body.Motivo)
	// Motivo obrigatorio ao silenciar: daqui a seis meses, "por que paramos de
	// ser avisados deste cliente?" e' a primeira pergunta de quem estranhar, e
	// uma linha sem motivo nao responde nada.
	if body.Silenciado && motivo == "" {
		return c.Status(400).JSON(fiber.Map{
			"error": "informe o motivo (ex: contrato encerrado em 09/2026) — " +
				"sem ele ninguem sabe depois por que este cliente ficou mudo",
		})
	}

	ator := h.adminActor(c)
	if err := h.repo.DefinirSilencio(c.Context(), dominio, motivo, ator, body.Silenciado); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	acao := "alertas.reativar"
	msg := "alertas reativados para " + dominio
	if body.Silenciado {
		acao = "alertas.silenciar"
		msg = "alertas silenciados para " + dominio + " — o cliente continua no sistema, com todo o historico"
	}
	h.log.Info("alertas: estado do cliente alterado",
		zap.String("domain", dominio), zap.Bool("silenciado", body.Silenciado),
		zap.String("motivo", motivo), zap.String("por", ator))
	h.repo.WriteAudit(c.Context(), ator, acao, dominio, motivo, clientIP(c))

	return c.JSON(fiber.Map{"ok": true, "silenciado": body.Silenciado, "mensagem": msg})
}
