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
	}
	return c.SendStatus(fiber.StatusOK)
}
