// billing_itau.go — integracao do checkout com o PIX direto do Itau.
//
// Quando ITAU_CLIENT_ID esta configurado, o metodo "pix" do checkout usa o
// Itau (mTLS) em vez do MaxiPago. Boleto continua no MaxiPago. Ver decisao em
// docs/aprendizados: "PIX = Itau, boleto = MaxiPago".
package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/uctechnology/api-bitrix24-whatsapp/internal/itau"
)

// itauPIXConfigured diz se da' pra cobrar PIX pelo Itau: precisa de client id,
// secret e chave PIX. O certificado e' checado na hora de criar o client (em
// producao e' obrigatorio; sandbox pode dispensar).
func (h *handlers) itauPIXConfigured() bool {
	b := h.cfg.Billing
	return b.ItauClientID != "" && b.ItauClientSecret != "" && b.ItauChavePIX != ""
}

// itauBoletoConfig monta a config de boleto a partir da config (env).
func (h *handlers) itauBoletoConfig() itau.BoletoConfig {
	b := h.cfg.Billing
	return itau.BoletoConfig{
		BaseURL:  b.ItauBoletoURL,
		Agencia:  b.ItauAgencia,
		Conta:    b.ItauConta,
		ContaDAC: b.ItauContaDAC,
		Carteira: b.ItauCarteira,
		Especie:  b.ItauEspecie,
		Etapa:    b.ItauEtapa,
	}
}

// itauBoletoConfigured — boleto Itau precisa da mesma auth do PIX (client id +
// secret + certificado) mais os dados de conta. Como conta tem defaults, basta
// checar auth.
func (h *handlers) itauBoletoConfigured() bool {
	b := h.cfg.Billing
	return b.ItauClientID != "" && b.ItauClientSecret != "" &&
		b.ItauAgencia != "" && b.ItauConta != ""
}

// newItauClient monta o cliente Itau a partir da config (env). Retorna erro se
// o certificado for exigido (producao) e nao existir.
func (h *handlers) newItauClient() (*itau.Client, error) {
	b := h.cfg.Billing
	return itau.New(itau.Config{
		ClientID:     b.ItauClientID,
		ClientSecret: b.ItauClientSecret,
		APIKey:       b.ItauAPIKey,
		ChavePIX:     b.ItauChavePIX,
		CertPath:     b.ItauCertPath,
		KeyPath:      b.ItauKeyPath,
		Ambiente:     b.ItauEnv,
		BaseURL:      b.ItauBaseURL,
	})
}

// itauPIXCharge cria uma cobranca PIX no Itau e devolve o copia-e-cola + QR.
// txid do Itau: 26-35 chars, so' [a-zA-Z0-9]. Derivamos do reference (que ja'
// vem sem hifens) garantindo o tamanho.
func (h *handlers) itauPIXCharge(ctx context.Context, ref, planLabel, valorReais string) (copyPaste, qrBase64 string, err error) {
	cli, err := h.newItauClient()
	if err != nil {
		return "", "", err
	}

	txid := itauTxidFromRef(ref)
	cob, err := cli.CriarCobranca(ctx, txid, valorReais, "Assinatura UC Talk "+planLabel)
	if err != nil {
		return "", "", err
	}
	copyPaste = cob.PixCopiaECola

	// Busca a imagem do QR (best-effort — se falhar, o copia-e-cola ja' resolve).
	if qr, qerr := cli.ObterQRCode(ctx, cob.Txid); qerr == nil {
		qrBase64 = qr.ImagemBase64
		if copyPaste == "" {
			copyPaste = qr.EMV
		}
	}
	return copyPaste, qrBase64, nil
}

// ReconcileBoletos consulta no Itaú os boletos pendentes e, pros que já foram
// pagos, marca a cobrança e libera o plano — exatamente como o webhook do PIX
// faz, mas por POLLING (o boleto não tem webhook de "pago").
//
// Retorna quantos foram liberados nesta rodada. Best-effort: erros por boleto
// são logados e a rodada continua (um boleto problemático não trava os outros).
func (h *handlers) ReconcileBoletos(ctx context.Context) int {
	if !h.itauBoletoConfigured() {
		return 0 // boleto não configurado — nada a fazer
	}
	pend, err := h.repo.ListPendingBoletoCharges(ctx, 10, 100)
	if err != nil {
		h.log.Warn("reconcile boleto: listar pendentes falhou", zap.Error(err))
		return 0
	}
	if len(pend) == 0 {
		return 0
	}

	cli, err := h.newItauClient()
	if err != nil {
		h.log.Warn("reconcile boleto: cliente Itaú indisponível", zap.Error(err))
		return 0
	}
	bc := h.itauBoletoConfig()

	liberados := 0
	for _, charge := range pend {
		// nosso número foi salvo em MPTransactionID no checkout (billing.go).
		nn := charge.MPTransactionID
		if nn == "" {
			continue
		}
		st, err := cli.ConsultarBoleto(ctx, bc, nn)
		if err != nil {
			h.log.Warn("reconcile boleto: consulta falhou",
				zap.String("nosso_numero", nn), zap.String("ref", charge.ReferenceNum), zap.Error(err))
			continue
		}
		if !st.Pago {
			continue // ainda em aberto
		}

		changed, err := h.repo.MarkBillingChargePaid(ctx, charge.ReferenceNum,
			"boleto liquidado (reconciliação) situacao="+st.Situacao)
		if err != nil {
			h.log.Error("reconcile boleto: marcar pago falhou", zap.Error(err))
			continue
		}
		if !changed {
			continue // já estava pago (idempotente)
		}

		paidPlan := charge.Plan
		if paidPlan != "basic" && paidPlan != "pro" {
			paidPlan = "pro"
		}
		until := time.Now().AddDate(0, 0, h.cfg.Billing.ActivateDays)
		note := fmt.Sprintf("pagamento BOLETO Itaú plano=%s ref=%s nn=%s em %s",
			paidPlan, charge.ReferenceNum, nn, time.Now().Format("2006-01-02 15:04"))
		if err := h.repo.SetTenantPlan(ctx, charge.Domain, paidPlan, "active", &until, note); err != nil {
			h.log.Error("reconcile boleto: ativar plano falhou",
				zap.String("domain", charge.Domain), zap.Error(err))
			continue
		}
		h.repo.WriteAudit(ctx, "sistema:itau-boleto", "tenant.pago.boleto", charge.Domain,
			fmt.Sprintf("plano=%s ref=%s nn=%s ate=%s", paidPlan, charge.ReferenceNum, nn,
				until.Format("2006-01-02")), "reconciliacao")
		h.log.Info("reconcile boleto: BOLETO CONFIRMADO — plano ativado",
			zap.String("domain", charge.Domain), zap.String("plan", paidPlan),
			zap.String("reference", charge.ReferenceNum), zap.Time("active_until", until))
		liberados++
	}
	return liberados
}

// itauBoletoResult reune o retorno util de um boleto pro checkout.
type itauBoletoResult struct {
	LinhaDigitavel string
	CodigoBarras   string
	NossoNumero    string
	PixCopiaCola   string // se Bolecode
}

// itauBoletoCharge emite um boleto no Itau. Gera o "nosso numero" crescente/
// unico (carteira 109) e registra. payerName/payerDoc identificam o pagador.
func (h *handlers) itauBoletoCharge(ctx context.Context, ref, planLabel string, amount int64, payerName, payerDoc string, vencimento string) (*itauBoletoResult, error) {
	cli, err := h.newItauClient()
	if err != nil {
		return nil, err
	}
	nn, err := h.repo.ProximoNossoNumero(ctx)
	if err != nil {
		return nil, fmt.Errorf("falha ao gerar nosso numero: %w", err)
	}

	tipoDoc := "CNPJ"
	if len(strings.ReplaceAll(payerDoc, ".", "")) <= 11 && payerDoc != "" {
		tipoDoc = "CPF"
	}

	bol, err := cli.RegistrarBoleto(ctx, h.itauBoletoConfig(), itau.EntradaBoleto{
		PagadorTipoDoc: tipoDoc,
		PagadorDoc:     payerDoc,
		PagadorNome:    payerName,
		ValorCentavos:  amount,
		Vencimento:     vencimento,
		SeuNumero:      ref,
		NossoNumero:    fmt.Sprintf("%d", nn),
	})
	if err != nil {
		return nil, err
	}
	return &itauBoletoResult{
		LinhaDigitavel: bol.LinhaDigitavel,
		CodigoBarras:   bol.CodigoBarras,
		NossoNumero:    bol.NossoNumero,
		PixCopiaCola:   bol.PixCopiaCola,
	}, nil
}

// itauBoletoValidacao registra um boleto em etapa "validacao" — o Itau valida
// os dados e a auth (cert + token + produto) SEM emitir titulo real. Usado pelo
// teste do painel. Gera um nosso numero de verdade (a etapa validacao aceita).
func (h *handlers) itauBoletoValidacao(ctx context.Context, cli *itau.Client, bc itau.BoletoConfig, ref string) (*itau.Boleto, error) {
	nn, err := h.repo.ProximoNossoNumero(ctx)
	if err != nil {
		return nil, fmt.Errorf("falha ao gerar nosso numero: %w", err)
	}
	return cli.RegistrarBoleto(ctx, bc, itau.EntradaBoleto{
		PagadorTipoDoc: "CNPJ",
		PagadorDoc:     "11620571000145",
		PagadorNome:    "Teste UC Talk",
		Logradouro:     "Rua Teste",
		Bairro:         "Centro",
		Cidade:         "Sao Paulo",
		UF:             "SP",
		CEP:            "01310100",
		ValorCentavos:  100,
		Vencimento:     time.Now().AddDate(0, 0, 3).Format("2006-01-02"),
		SeuNumero:      ref,
		NossoNumero:    fmt.Sprintf("%d", nn),
	})
}

// itauTxidFromRef normaliza um reference "uctalk-<16hex>" num txid valido do
// Itau (26-35 chars alfanumericos). Remove nao-alfanumericos e completa com
// zeros se ficar curto.
func itauTxidFromRef(ref string) string {
	var b strings.Builder
	for _, r := range ref {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	s := b.String()
	for len(s) < 26 {
		s += "0"
	}
	if len(s) > 35 {
		s = s[:35]
	}
	return s
}

// billingItauPixWebhook — o Itaú notifica aqui quando um PIX é pago.
// Rota registrada como /billing/itau/pix (o Itaú acrescenta /pix à URL que
// você cadastra SEM o sufixo — ver docs/aprendizados). Reconcilia por txid,
// marca a cobrança paga (idempotente) e libera o plano automaticamente.
//
// Sempre responde 200 (mesmo em erro nosso) para o Itaú não re-tentar em loop;
// os problemas ficam no log.
func (h *handlers) billingItauPixWebhook(c *fiber.Ctx) error {
	raw := c.Body()
	h.log.Info("billing: webhook PIX Itaú recebido",
		zap.String("content_type", c.Get("Content-Type")),
		zap.String("raw", truncateStr(string(raw), 2000)))

	notes, err := itau.ParseWebhook(raw)
	if err != nil || len(notes) == 0 {
		h.log.Warn("billing: webhook Itaú sem notificações reconhecíveis",
			zap.Error(err))
		return c.SendStatus(fiber.StatusOK)
	}

	ctx := c.Context()
	for _, n := range notes {
		charge, err := h.repo.GetBillingChargeByTxid(ctx, n.Txid)
		if err != nil || charge == nil {
			h.log.Warn("billing: webhook Itaú — cobrança não encontrada pro txid",
				zap.String("txid", n.Txid), zap.Error(err))
			continue
		}
		changed, err := h.repo.MarkBillingChargePaid(ctx, charge.ReferenceNum, truncateStr(string(raw), 4000))
		if err != nil {
			h.log.Error("billing: webhook Itaú — marcar pago falhou", zap.Error(err))
			continue
		}
		if !changed {
			continue // duplicado — idempotente
		}

		paidPlan := charge.Plan
		if paidPlan != "basic" && paidPlan != "pro" {
			paidPlan = "pro"
		}
		until := time.Now().AddDate(0, 0, h.cfg.Billing.ActivateDays)
		note := fmt.Sprintf("pagamento PIX Itaú plano=%s ref=%s e2e=%s em %s",
			paidPlan, charge.ReferenceNum, n.EndToEndID, time.Now().Format("2006-01-02 15:04"))
		if err := h.repo.SetTenantPlan(ctx, charge.Domain, paidPlan, "active", &until, note); err != nil {
			h.log.Error("billing: webhook Itaú — ativar plano falhou",
				zap.String("domain", charge.Domain), zap.Error(err))
			continue
		}
		h.log.Info("billing: PIX Itaú CONFIRMADO — plano ativado",
			zap.String("domain", charge.Domain), zap.String("plan", paidPlan),
			zap.String("reference", charge.ReferenceNum), zap.Time("active_until", until))
		h.repo.WriteAudit(ctx, "sistema:itau-pix", "tenant.pago.pix", charge.Domain,
			fmt.Sprintf("plano=%s ref=%s e2e=%s ate=%s", paidPlan, charge.ReferenceNum,
				n.EndToEndID, until.Format("2006-01-02")), "webhook-itau")
	}
	return c.SendStatus(fiber.StatusOK)
}
