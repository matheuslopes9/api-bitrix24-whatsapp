// alertas_admin.go — a tela que configura os alertas.
//
// A configuracao vive no BANCO, nao no ambiente: trocar destinatario ou
// desligar um aviso nao pode exigir reiniciar o app, porque reiniciar derruba
// o atendimento de todos os clientes. As envs continuam valendo como carga
// inicial, pra instalacao nova nascer funcionando.
package api

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"go.uber.org/zap"
)

// GET /admin/api/alertas/config
func (h *handlers) adminGetConfigAlertas(c *fiber.Ctx) error {
	cfg, err := h.repo.GetConfigAlertas(c.Context())
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{
		"config": cfg,
		// Calculado aqui pra tela nao precisar repetir a regra de "da pra
		// enviar?" em JavaScript e as duas divergirem com o tempo.
		"configurado": cfg.Configurado(),
	})
}

// POST /admin/api/alertas/config
func (h *handlers) adminSalvarConfigAlertas(c *fiber.Ctx) error {
	var body db.ConfigAlertas
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "body invalido"})
	}

	body.SMTPHost = strings.TrimSpace(body.SMTPHost)
	body.EmailSender = strings.TrimSpace(body.EmailSender)
	body.EmailReplyTo = strings.TrimSpace(body.EmailReplyTo)
	body.Destinatarios = strings.TrimSpace(body.Destinatarios)

	// Pisos de sanidade. Janela zero faria o mesmo alerta sair a cada ciclo
	// de 5 minutos — e o time aprenderia a ignorar e-mail do UC Talk, que e'
	// o pior resultado possivel pra um sistema de aviso.
	if body.SMTPPort <= 0 || body.SMTPPort > 65535 {
		return c.Status(400).JSON(fiber.Map{"error": "porta SMTP invalida"})
	}
	if body.TokenJanelaH < 1 {
		body.TokenJanelaH = 1
	}
	if body.SessaoJanelaMin < 5 {
		body.SessaoJanelaMin = 5
	}

	// Ligar um alerta sem ter para onde enviar so' produziria falha silenciosa
	// a cada ciclo. Melhor recusar e dizer o que falta.
	querEnviar := body.TokenAtivo || body.SessaoAtivo || body.LicencaAtivo
	if querEnviar && !body.Configurado() {
		return c.Status(400).JSON(fiber.Map{
			"error": "para manter alertas ligados, preencha servidor, porta, remetente e ao menos um destinatario",
		})
	}

	quem := h.adminActor(c)
	if err := h.repo.SalvarConfigAlertas(c.Context(), &body, quem); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.repo.WriteAudit(c.Context(), quem, "alertas.config.salva", "", "", c.IP())
	h.log.Info("config de alertas salva",
		zap.String("por", quem),
		zap.String("smtp_host", body.SMTPHost),
		zap.String("destinatarios", body.Destinatarios),
		zap.Bool("token", body.TokenAtivo),
		zap.Bool("sessao", body.SessaoAtivo),
		zap.Bool("licenca", body.LicencaAtivo))
	return c.JSON(fiber.Map{"ok": true, "mensagem": "configuracao salva"})
}

// GET /admin/api/alertas/historico — o que ja' foi enviado.
//
// Responde "esse alerta chegou a sair?" sem abrir o log do container, e
// mostra de relance se algum cliente esta' disparando aviso repetido.
func (h *handlers) adminHistoricoAlertas(c *fiber.Ctx) error {
	msgs, err := h.repo.ListarAlertasEnviados(c.Context(), 50)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"total": len(msgs), "alertas": msgs})
}
