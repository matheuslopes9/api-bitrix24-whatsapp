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
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/itau"
)

// GET /admin/api/itau-diag — diagnostico do host PIX. Quando o "Testar PIX" da
// 404 ("Entidade nao encontrada"), a auth esta OK mas o HOST/rota do produto
// pode ser diferente. Este endpoint tenta criar uma cobranca contra VARIOS
// hosts candidatos e devolve a resposta crua de cada um — assim da' pra ver
// qual host responde 201 (certo) e configurar ITAU_BASE_URL de acordo.
//
// NAO persiste nada. Usa um txid unico por rodada. Requer PIX configurado.
func (h *handlers) adminItauDiag(c *fiber.Ctx) error {
	if !h.itauPIXConfigured() {
		return c.JSON(fiber.Map{"ok": false, "hint": "PIX nao configurado (faltam ITAU_CLIENT_ID/SECRET/CHAVE_PIX)."})
	}
	cli, err := h.newItauClient()
	if err != nil {
		return c.JSON(fiber.Map{"ok": false, "message": err.Error(), "hint": itauHint(err)})
	}

	b := h.cfg.Billing
	chave := b.ItauChavePIX

	// Hosts candidatos pra API PIX PJ (regulatorio-pix). O primeiro e' o que o
	// codigo usa por default; os demais vem da doc/collection do Itau.
	candidatos := []string{
		firstNonEmpty(b.ItauBaseURL, ""), // se o admin ja' setou um ITAU_BASE_URL, testa primeiro
		"https://pix-pj.api.itau.com/regulatorio-pix/v2",
		"https://secure.api.itau/pix_regulatorio-pix/v2",
		"https://secure.api.itau.com.br/pix_regulatorio-pix/v2",
		"https://api.itau.com.br/pix_regulatorio-pix/v2",
		"https://pix-pj.api.itau.com.br/regulatorio-pix/v2",
	}

	var resultados []itau.DiagResultado
	vistos := map[string]bool{}
	for i, base := range candidatos {
		base = strings.TrimSpace(base)
		if base == "" || vistos[base] {
			continue
		}
		vistos[base] = true
		// txid unico por candidato (26-35 alfanumericos).
		txid := "diag" + strings.Repeat("0", 8) + itauTxidFromRef("diaghost"+itoa(i))
		if len(txid) > 35 {
			txid = txid[:35]
		}
		resultados = append(resultados, cli.DiagnosticarCob(c.Context(), base, txid, "1.00", chave))
	}

	// Aponta o melhor candidato (o que deu 200/201).
	var melhor string
	for _, r := range resultados {
		if r.HTTPStatus == 200 || r.HTTPStatus == 201 {
			melhor = r.BaseURL
			break
		}
	}

	return c.JSON(fiber.Map{
		"ok":          melhor != "",
		"host_correto": melhor,
		"resultados":  resultados,
		"hint": firstNonEmpty(
			ternaryStr(melhor != "", "Achamos! Configure ITAU_BASE_URL="+melhor+" no EasyPanel e redeploy.", ""),
			"Nenhum host respondeu 200/201. Veja os corpos abaixo: 404 = rota/produto; erro de rede = host inexistente; 403 = produto nao habilitado.",
		),
	})
}

// GET /admin/api/itau-chave — verifica se a CHAVE PIX configurada esta
// registrada/reconhecida pelo Itau nesse Client ID. Ajuda a diagnosticar o
// erro "documento do solicitante divergente": se a chave nao existe (404), ela
// nao esta vinculada a essa conta no produto Recebimentos PIX.
func (h *handlers) adminItauChave(c *fiber.Ctx) error {
	if !h.itauPIXConfigured() {
		return c.JSON(fiber.Map{"ok": false, "hint": "PIX nao configurado."})
	}
	cli, err := h.newItauClient()
	if err != nil {
		return c.JSON(fiber.Map{"ok": false, "message": err.Error(), "hint": itauHint(err)})
	}
	chave := h.cfg.Billing.ItauChavePIX
	res := cli.ConsultarChave(c.Context(), chave)

	hint := ""
	switch res.HTTPStatus {
	case 200:
		hint = "A chave EXISTE e esta consultavel. Se o /cob ainda da 'documento divergente', fale com o Itau: a chave pode estar num Client ID diferente do certificado."
	case 404:
		hint = "A chave NAO foi encontrada (404). Ela nao esta registrada/vinculada a esse Client ID no produto Recebimentos PIX. Peca ao gerente pra VINCULAR a chave " + maskTail(chave, 6) + " a esse Client ID."
	case 403:
		hint = "Acesso negado (403) ao consultar a chave — produto pode nao estar habilitado."
	default:
		hint = "Veja o corpo abaixo. Erro de rede/host indica problema de conexao."
	}

	return c.JSON(fiber.Map{
		"ok":          res.HTTPStatus == 200,
		"chave":       maskTail(chave, 6),
		"http_status": res.HTTPStatus,
		"corpo":       res.Corpo,
		"erro":        res.Erro,
		"hint":        hint,
	})
}

// ternaryStr — helper minusculo pro diag.
func ternaryStr(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

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
