// conta.go — o admin logado cuida da propria conta.
//
// Ate' aqui so' existia criar/desativar usuario: quem entrava com uma senha
// definida por outra pessoa ficava com ela pra sempre, ou dependia de alguem
// recriar o login. Trocar a propria senha nao deveria exigir favor de
// terceiro nem acesso ao banco.
package api

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/email"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

// senhaMinima e' o mesmo piso usado na criacao de usuario. Repetir a regra em
// dois lugares com valores diferentes seria pior que repetir a constante.
const senhaMinima = 8

// GET /admin/api/me — quem sou eu neste painel.
func (h *handlers) adminEu(c *fiber.Ctx) error {
	email := h.adminActor(c)
	papel, _ := c.Locals("admin_role").(string)
	res := fiber.Map{"email": email, "role": papel}
	// Login root do .env nao existe na tabela — nao da' pra trocar a senha
	// dele por aqui, ela vem do ambiente. Dizer isso evita o usuario tentar
	// e receber um erro que nao explica nada.
	if u, err := h.repo.GetAdminUserByEmail(c.Context(), email); err == nil && u != nil {
		res["nome"] = u.Name
		res["pode_trocar_senha"] = true
	} else {
		res["pode_trocar_senha"] = false
		res["motivo"] = "este login vem das variaveis de ambiente (ADMIN_USER); a senha se troca no ambiente"
	}
	return c.JSON(res)
}

// POST /admin/api/me/password — trocar a propria senha.
//
// Exige a senha ATUAL de proposito: cookie roubado ou estacao destravada nao
// pode virar troca de senha, que trancaria o dono pra fora.
func (h *handlers) adminTrocarMinhaSenha(c *fiber.Ctx) error {
	var body struct {
		SenhaAtual string `json:"senha_atual"`
		SenhaNova  string `json:"senha_nova"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "body invalido"})
	}
	atual := strings.TrimSpace(body.SenhaAtual)
	nova := strings.TrimSpace(body.SenhaNova)
	if atual == "" || nova == "" {
		return c.Status(400).JSON(fiber.Map{"error": "informe a senha atual e a nova"})
	}
	if len(nova) < senhaMinima {
		return c.Status(400).JSON(fiber.Map{"error": "a senha nova precisa de pelo menos 8 caracteres"})
	}
	if nova == atual {
		return c.Status(400).JSON(fiber.Map{"error": "a senha nova tem que ser diferente da atual"})
	}

	quem := h.adminActor(c)
	u, err := h.repo.GetAdminUserByEmail(c.Context(), quem)
	if err != nil || u == nil {
		return c.Status(400).JSON(fiber.Map{
			"error": "este login vem das variaveis de ambiente e a senha se troca no ambiente (ADMIN_PASSWORD)",
		})
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(atual)) != nil {
		h.log.Warn("troca de senha recusada: senha atual incorreta", zap.String("usuario", quem))
		return c.Status(403).JSON(fiber.Map{"error": "senha atual incorreta"})
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(nova), bcrypt.DefaultCost)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "falha ao preparar a senha"})
	}
	if err := h.repo.SetAdminUserPassword(c.Context(), quem, string(hash)); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.repo.WriteAudit(c.Context(), quem, "admin.senha.trocada", quem, "", c.IP())
	h.log.Info("senha trocada pelo proprio usuario", zap.String("usuario", quem))
	return c.JSON(fiber.Map{"ok": true, "mensagem": "senha alterada"})
}

// POST /admin/api/alertas/teste — confirma que o e-mail sai de verdade.
//
// Sem isto, so' se descobre que o envio esta quebrado na hora do incidente —
// que e' exatamente quando nao da tempo de descobrir.
func (h *handlers) adminTestarEmail(c *fiber.Ctx) error {
	cfg, err := h.repo.GetConfigAlertas(c.Context())
	if err != nil || cfg == nil {
		return c.Status(500).JSON(fiber.Map{"error": "falha ao ler a configuracao de alertas"})
	}
	if !cfg.Configurado() {
		return c.Status(400).JSON(fiber.Map{
			"error": "envio nao configurado — preencha servidor, remetente e destinatarios em Alertas",
		})
	}
	rem := remetenteDaConfig(cfg)
	corpo := h.montarAlerta(
		email.CatTeste,
		"Teste de alerta do UC Talk",
		"<p>Se você está lendo este e-mail, o envio de alertas do UC Talk está funcionando.</p>"+
			"<p>Nenhuma ação é necessária — este disparo foi manual, pelo painel.</p>",
		"Nenhuma. Este é apenas um teste de envio.",
		[]email.LinhaContexto{
			{Rotulo: "Disparado por", Valor: h.adminActor(c)},
			{Rotulo: "Origem", Valor: "Painel administrativo — Alertas"},
		})
	if err := rem.Enviar(c.Context(), "[UC Talk] Teste de alerta", corpo); err != nil {
		h.log.Warn("teste de e-mail falhou", zap.Error(err))
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.log.Info("teste de e-mail enviado", zap.Strings("para", rem.Destinatarios()))
	return c.JSON(fiber.Map{"ok": true, "enviado_para": rem.Destinatarios()})
}
