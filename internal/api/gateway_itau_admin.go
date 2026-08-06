// gateway_itau_admin.go — endpoints do painel admin pro gateway Itaú.
//
// Substitui a antiga config editável do MaxiPago. Com o Itaú, tudo é env
// (client id/secret, chave PIX, certificado mTLS montado no volume), então a
// UI é só LEITURA (status) + um botão de TESTE que gera cobrança real.
package api

import (
	"os"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// maskTail mostra só os últimos n caracteres, mascarando o resto. Pra exibir
// client id / chave sem vazar o valor inteiro no painel.
func maskTail(s string, keep int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) <= keep {
		return strings.Repeat("•", len(s))
	}
	return strings.Repeat("•", len(s)-keep) + s[len(s)-keep:]
}

// GET /admin/api/itau-status — status readonly do gateway Itaú pro painel.
// Nada de secret aqui: client id e chave PIX mascarados, e um booleano dizendo
// se o certificado existe no volume.
func (h *handlers) adminItauStatus(c *fiber.Ctx) error {
	b := h.cfg.Billing

	certExists := false
	if b.ItauCertPath != "" {
		if _, err := os.Stat(b.ItauCertPath); err == nil {
			certExists = true
		}
	}
	keyExists := false
	if b.ItauKeyPath != "" {
		if _, err := os.Stat(b.ItauKeyPath); err == nil {
			keyExists = true
		}
	}

	env := b.ItauEnv
	if env == "" {
		env = "sandbox"
	}

	return c.JSON(fiber.Map{
		"pix_configured":    h.itauPIXConfigured(),
		"boleto_configured": h.itauBoletoConfigured(),
		"client_id":         maskTail(b.ItauClientID, 6),
		"chave_pix":         maskTail(b.ItauChavePIX, 8),
		"environment":       env,
		"cert_path":         b.ItauCertPath,
		"cert_exists":       certExists,
		"key_exists":        keyExists,
		"agencia":           b.ItauAgencia,
		"conta":             b.ItauConta,
		"carteira":          b.ItauCarteira,
		"activate_days":     b.ActivateDays,
	})
}

// POST /admin/api/itau-test?method=pix|boleto — testa as credenciais Itaú de
// verdade: PIX gera uma cobrança de R$ 1,00 (validando cert + token + produto
// habilitado); boleto emite em etapa "validacao" (não gera título real). Não
// persiste charge nenhuma. É aqui que o certificado é de fato exercitado — o
// boot não testa mTLS.
func (h *handlers) adminItauTest(c *fiber.Ctx) error {
	method := strings.ToLower(strings.TrimSpace(c.Query("method")))
	if method == "" {
		method = "pix"
	}

	// reference de teste — não colide com cobranças reais (prefixo test-).
	ref := "test" + strings.ReplaceAll(digitsOnly(strings.ReplaceAll(c.Get("X-Request-Id"), "-", "")), " ", "")
	if len(ref) < 8 {
		ref = "testuctalk000000"
	}

	if method == "boleto" {
		if !h.itauBoletoConfigured() {
			return c.JSON(fiber.Map{"ok": false, "method": "boleto",
				"hint": "Boleto Itaú não configurado — faltam ITAU_CLIENT_ID/SECRET e agência/conta."})
		}
		// Força etapa "validacao" pro teste não emitir título real.
		cli, err := h.newItauClient()
		if err != nil {
			return c.JSON(fiber.Map{"ok": false, "method": "boleto", "message": err.Error(),
				"hint": itauHint(err)})
		}
		bc := h.itauBoletoConfig()
		bc.Etapa = "validacao"
		_, err = h.itauBoletoValidacao(c.Context(), cli, bc, ref)
		if err != nil {
			return c.JSON(fiber.Map{"ok": false, "method": "boleto", "message": err.Error(),
				"hint": itauHint(err)})
		}
		return c.JSON(fiber.Map{"ok": true, "method": "boleto", "environment": bc.Etapa,
			"hint": "Boleto validado (etapa 'validacao' — nenhum título real foi emitido)."})
	}

	// PIX — cobrança real de R$ 1,00.
	if !h.itauPIXConfigured() {
		return c.JSON(fiber.Map{"ok": false, "method": "pix",
			"hint": "PIX Itaú não configurado — faltam ITAU_CLIENT_ID/SECRET e ITAU_CHAVE_PIX."})
	}
	copyPaste, _, err := h.itauPIXCharge(c.Context(), ref, "TESTE", "1.00")
	if err != nil {
		return c.JSON(fiber.Map{"ok": false, "method": "pix", "message": err.Error(),
			"hint": itauHint(err)})
	}
	return c.JSON(fiber.Map{"ok": true, "method": "pix",
		"environment": firstNonEmpty(h.cfg.Billing.ItauEnv, "sandbox"),
		"copy_paste":  truncateStr(copyPaste, 60) + "…",
		"hint":        "PIX gerado com sucesso — certificado, token e produto Recebimentos PIX estão OK."})
}

// itauHint traduz erros comuns do Itaú numa dica acionável pro admin.
func itauHint(err error) string {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "403") || strings.Contains(msg, "acesso negado"):
		return "HTTP 403 — o produto (Recebimentos PIX ou Boleto/Cash Management) provavelmente NÃO está habilitado pro seu Client ID. Fale com o gerente Itaú."
	case strings.Contains(msg, "cn") || strings.Contains(msg, "certificad") || strings.Contains(msg, "tls") || strings.Contains(msg, "x509"):
		return "Falha de certificado — confira se itau.crt/itau.key estão no volume e se o CN do certificado é igual ao Client ID."
	case strings.Contains(msg, "token") || strings.Contains(msg, "oauth") || strings.Contains(msg, "401"):
		return "Falha de autenticação — confira ITAU_CLIENT_ID e ITAU_CLIENT_SECRET."
	default:
		return "Veja a mensagem acima e confirme credenciais/produto com o Itaú."
	}
}
