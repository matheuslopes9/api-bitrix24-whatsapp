// billing.go — integracao com o gateway maxiPago (Rede) pra cobranca dos
// planos UC Talk.
//
// Fluxo:
//   1. Trial de 7 dias expira -> dashboard mostra popup "veja os planos"
//   2. /planos -> botao "Assinar Pro" -> POST /ui/billing/checkout
//   3. Backend cria transacao BOLETO no maxiPago (XML API) e devolve a URL
//      do boleto pro cliente pagar
//   4. maxiPago notifica mudanca de status no POST /billing/maxipago/postback
//      (URL configurada no portal maxiPago em Configuracoes > Notificacoes)
//   5. Pagamento confirmado -> SetTenantPlan(domain, pro, active, +N dias)
//      — liberacao AUTOMATICA, sem intervencao manual
//
// API maxiPago (manual v2.0.3): XML por POST.
//   Sandbox:   https://testapi.maxipago.net/UniversalAPI/postXML
//   Producao:  https://api.maxipago.net/UniversalAPI/postXML
// Autenticacao: <verification><merchantId/><merchantKey/></verification>
// em cada request. Sandbox: processorID 1 = simulador cartao, 12 = boleto
// de teste. responseCode 0 = sucesso.
package api

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/config"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"go.uber.org/zap"
)

// ─── Cliente XML maxiPago ──────────────────────────────────────────────────

// effectiveBilling resolve a config de billing: PRIORIDADE ao banco
// (billing_config, editavel pela UI admin), com FALLBACK pro env. Campos
// vazios no banco caem no env correspondente. Assim o usuario configura o
// gateway pela tela sem depender de env/deploy, mas o env ainda funciona.
func (h *handlers) effectiveBilling(ctx context.Context) config.BillingConfig {
	b := h.cfg.Billing // base = env
	row, _ := h.repo.GetBillingConfig(ctx)
	if row == nil {
		return b
	}
	if row.Environment != "" {
		b.MaxiPagoEnv = row.Environment
	}
	if row.MerchantID != "" {
		b.MaxiPagoMerchantID = row.MerchantID
	}
	if row.MerchantKey != "" {
		b.MaxiPagoMerchantKey = row.MerchantKey
	}
	if row.ProcessorBoleto != "" {
		b.ProcessorBoleto = row.ProcessorBoleto
	}
	if row.ProcessorPix != "" {
		b.ProcessorPix = row.ProcessorPix
	}
	if row.ProcessorCard != "" {
		b.ProcessorCard = row.ProcessorCard
	}
	if row.ActivateDays > 0 {
		b.ActivateDays = row.ActivateDays
	}
	// Preco do plano continua vindo do env/plan_definitions (nao aqui).
	return b
}

func maxipagoBaseURLFor(bc config.BillingConfig) string {
	if strings.EqualFold(bc.MaxiPagoEnv, "production") {
		return "https://api.maxipago.net"
	}
	return "https://testapi.maxipago.net"
}

// maxipagoConfigured usa a config efetiva (banco+env). Alem das credenciais,
// respeita a flag "enabled" da config do banco (se existir e for false,
// considera desabilitado).
func (h *handlers) maxipagoConfigured(ctx context.Context) bool {
	bc := h.effectiveBilling(ctx)
	if bc.MaxiPagoMerchantID == "" || bc.MaxiPagoMerchantKey == "" {
		return false
	}
	// Se ha config no banco e ela esta desabilitada, respeita.
	if row, _ := h.repo.GetBillingConfig(ctx); row != nil && row.MerchantID != "" && !row.Enabled {
		return false
	}
	return true
}

// mpTransactionRequest — payload minimo pra venda com boleto (manual 2.0.3).
type mpTransactionRequest struct {
	XMLName      xml.Name       `xml:"transaction-request"`
	Version      string         `xml:"version"`
	Verification mpVerification `xml:"verification"`
	Order        mpOrder        `xml:"order"`
}

type mpVerification struct {
	MerchantID  string `xml:"merchantId"`
	MerchantKey string `xml:"merchantKey"`
}

type mpOrder struct {
	Sale mpSale `xml:"sale"`
}

type mpSale struct {
	ProcessorID       string              `xml:"processorID"`
	ReferenceNum      string              `xml:"referenceNum"`
	Billing           *mpBilling          `xml:"billing,omitempty"`
	TransactionDetail mpTransactionDetail `xml:"transactionDetail"`
	Payment           mpPayment           `xml:"payment"`
}

type mpBilling struct {
	Name string `xml:"name,omitempty"`
}

type mpTransactionDetail struct {
	PayType mpPayType `xml:"payType"`
}

type mpPayType struct {
	Boleto *mpBoleto `xml:"boleto,omitempty"`
	Pix    *mpPix    `xml:"pix,omitempty"`
}

type mpBoleto struct {
	ExpirationDate string `xml:"expirationDate"` // YYYY-MM-DD
	Number         string `xml:"number"`         // nosso numero (sequencial do lojista)
	Instructions   string `xml:"instructions"`
}

// mpPix — bloco PIX (maxiPago apidocs). processorID 206. expirationTime em
// segundos ate o QR expirar; paymentInfo e' a descricao mostrada ao pagador.
type mpPix struct {
	ExpirationTime string `xml:"expirationTime,omitempty"` // segundos (ex: 3600)
	PaymentInfo    string `xml:"paymentInfo,omitempty"`    // descricao da cobranca
}

type mpPayment struct {
	ChargeTotal string `xml:"chargeTotal"` // "199.00"
}

// mpTransactionResponse — campos que interessam da resposta (boleto + pix +
// cartao). Nomes de tags variam entre versoes do maxiPago; capturamos os
// mais comuns. O que nao vier fica vazio.
type mpTransactionResponse struct {
	XMLName         xml.Name `xml:"transaction-response"`
	ResponseCode    string   `xml:"responseCode"`
	ResponseMessage string   `xml:"responseMessage"`
	OrderID         string   `xml:"orderID"`
	TransactionID   string   `xml:"transactionID"`
	ReferenceNum    string   `xml:"referenceNum"`
	BoletoURL       string   `xml:"boletoUrl"`
	// PIX (maxiPago apidocs): <emv> = copia-e-cola; <imagem_base64> = PNG
	// base64 do QR. Fallbacks pra variacoes de nome por seguranca.
	EMV          string `xml:"emv"`
	ImagemBase64 string `xml:"imagem_base64"`
	PixCopyPaste string `xml:"pixCopyPaste"`
	QRCode       string `xml:"qrCode"`
	ErrorMessage string `xml:"errorMessage"`
	ProcessorMsg string `xml:"processorMessage"`
	// AuthCode: presente em cartao aprovado.
	AuthCode string `xml:"authCode"`
}

// pixPayload devolve o texto copia-e-cola (EMV) do PIX, com fallbacks.
func (r *mpTransactionResponse) pixPayload() string {
	for _, s := range []string{r.EMV, r.PixCopyPaste, r.QRCode} {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// maxipagoCreateBoleto cria uma venda-boleto e retorna (resposta, raw, err).
func (h *handlers) maxipagoCreateBoleto(ctx context.Context, bc config.BillingConfig, referenceNum, payerName, planLabel string, amountCents int64, dueDate time.Time) (*mpTransactionResponse, string, error) {
	reqBody := mpTransactionRequest{
		Version: "3.1.1.15",
		Verification: mpVerification{
			MerchantID:  bc.MaxiPagoMerchantID,
			MerchantKey: bc.MaxiPagoMerchantKey,
		},
		Order: mpOrder{Sale: mpSale{
			ProcessorID:  bc.ProcessorBoleto,
			ReferenceNum: referenceNum,
			Billing:      &mpBilling{Name: payerName},
			TransactionDetail: mpTransactionDetail{PayType: mpPayType{Boleto: &mpBoleto{
				ExpirationDate: dueDate.Format("2006-01-02"),
				Number:         fmt.Sprintf("%d", time.Now().Unix()%1000000),
				Instructions:   "UC Talk - Plano " + planLabel + ". Nao receber apos o vencimento.",
			}}},
			Payment: mpPayment{ChargeTotal: fmt.Sprintf("%d.%02d", amountCents/100, amountCents%100)},
		}},
	}
	return h.maxipagoPostXML(ctx, bc, reqBody)
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// maxipagoPostXML envia o request XML e devolve (resposta parseada, raw, err).
// Compartilhado por boleto/pix/cartao. bc decide o ambiente (base URL).
func (h *handlers) maxipagoPostXML(ctx context.Context, bc config.BillingConfig, reqBody mpTransactionRequest) (*mpTransactionResponse, string, error) {
	xmlBytes, err := xml.Marshal(reqBody)
	if err != nil {
		return nil, "", err
	}
	payload := []byte(xml.Header + string(xmlBytes))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		maxipagoBaseURLFor(bc)+"/UniversalAPI/postXML", bytes.NewReader(payload))
	if err != nil {
		return nil, "", err
	}
	httpReq.Header.Set("Content-Type", "text/xml; charset=UTF-8")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	rawBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	raw := string(rawBytes)

	var out mpTransactionResponse
	if err := xml.Unmarshal(rawBytes, &out); err != nil {
		return nil, raw, fmt.Errorf("maxipago: resposta nao-XML (%s): %s", resp.Status, truncateStr(raw, 300))
	}
	return &out, raw, nil
}

// maxipagoCreatePix cria uma cobranca PIX. QR expira em expirSeconds.
func (h *handlers) maxipagoCreatePix(ctx context.Context, bc config.BillingConfig, referenceNum, payerName string, amountCents int64, expirSeconds int) (*mpTransactionResponse, string, error) {
	reqBody := mpTransactionRequest{
		Version: "3.1.1.15",
		Verification: mpVerification{
			MerchantID:  bc.MaxiPagoMerchantID,
			MerchantKey: bc.MaxiPagoMerchantKey,
		},
		Order: mpOrder{Sale: mpSale{
			ProcessorID:  bc.ProcessorPix,
			ReferenceNum: referenceNum,
			Billing:      &mpBilling{Name: payerName},
			TransactionDetail: mpTransactionDetail{PayType: mpPayType{Pix: &mpPix{
				ExpirationTime: fmt.Sprintf("%d", expirSeconds),
				PaymentInfo:    "UC Talk - assinatura",
			}}},
			Payment: mpPayment{ChargeTotal: fmt.Sprintf("%d.%02d", amountCents/100, amountCents%100)},
		}},
	}
	return h.maxipagoPostXML(ctx, bc, reqBody)
}

// ─── Endpoints ─────────────────────────────────────────────────────────────

// POST /ui/billing/checkout — body {method:"boleto", payer_name:"..."}
// Cria a cobranca do plano Pro no maxiPago e devolve a URL do boleto.
// Requer cookie tenant (requireTenantOrAdmin) — o domain vem do contexto.
func (h *handlers) uiBillingCheckout(c *fiber.Ctx) error {
	// Gateway = Itaú (PIX + boleto). Bloqueia se não configurado.
	if !h.itauPIXConfigured() && !h.itauBoletoConfigured() {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "gateway de pagamento nao configurado — entre em contato com o comercial UC Technology",
			"code":  "billing_not_configured",
		})
	}
	domain, ok := c.Locals("tenant_domain").(string)
	if !ok || domain == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "tenant nao identificado"})
	}
	var body struct {
		Plan      string `json:"plan"`
		Method    string `json:"method"`
		PayerName string `json:"payer_name"`
		Coupon    string `json:"coupon"`
	}
	_ = c.BodyParser(&body)
	method := strings.ToLower(strings.TrimSpace(body.Method))
	if method == "" {
		method = "boleto"
	}
	if method != "boleto" && method != "pix" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "metodo nao suportado — use boleto ou pix",
		})
	}
	// Plano vem do construtor (plan_definitions); fallback pros 2 legados.
	plan := strings.ToLower(strings.TrimSpace(body.Plan))
	if plan == "" {
		plan = "pro"
	}
	planLabel := plan
	amount := h.planPriceCents(c, plan)
	if def, _ := h.repo.GetPlanDefinition(c.Context(), plan); def != nil {
		planLabel = def.Name
		if !def.Active {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "plano indisponivel para assinatura",
			})
		}
	} else if plan != "basic" && plan != "pro" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "plano invalido",
		})
	}
	if amount <= 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "plano sem preco configurado",
		})
	}

	// CUPOM: aplica desconto sobre o valor (percent/amount). Cupons de
	// trial_days nao entram aqui — sao aplicados em /ui/coupon/apply.
	originalAmount := amount
	couponCode := ""
	var couponDiscount int64
	if cp := strings.TrimSpace(body.Coupon); cp != "" {
		chk := h.validateCoupon(c, cp, domain, plan, amount)
		if !chk.Valid {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "cupom inválido: " + chk.Reason,
				"code":  "invalid_coupon",
			})
		}
		if chk.Kind == "trial_days" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "este cupom estende o teste — aplique-o na aba de assinatura, não no pagamento",
			})
		}
		couponCode = chk.Code
		couponDiscount = chk.DiscountCents
		amount = chk.FinalCents
		if amount <= 0 {
			amount = 100 // minimo simbolico (R$1) — gateway recusa valor zero
		}
	}

	payerName := strings.TrimSpace(body.PayerName)
	if payerName == "" {
		payerName = domain
	}

	ref := "uctalk-" + strings.ReplaceAll(uuid.New().String(), "-", "")[:16]
	ctx := c.Context()

	// Consome o cupom (best-effort). Feito antes das chamadas ao Itau porque a
	// cobranca sera criada logo em seguida; se a criacao falhar, o cupom volta
	// a ficar disponivel na proxima tentativa (RedeemCoupon e' idempotente por
	// tentativa — nao dobra desconto).
	redeemCupom := func() {
		if couponCode == "" {
			return
		}
		if err := h.repo.RedeemCoupon(ctx, couponCode, domain, plan, couponDiscount, 0); err != nil {
			h.log.Warn("billing: registrar uso do cupom falhou",
				zap.String("code", couponCode), zap.Error(err))
		}
	}
	addCupomOut := func(out fiber.Map) fiber.Map {
		if couponCode != "" {
			out["coupon"] = couponCode
			out["original_cents"] = originalAmount
			out["discount_cents"] = couponDiscount
		}
		return out
	}

	// ─── PIX Itaú ──────────────────────────────────────────────────────
	if method == "pix" {
		valorReais := fmt.Sprintf("%d.%02d", amount/100, amount%100)
		copyPaste, qrBase64, ierr := h.itauPIXCharge(ctx, ref, planLabel, valorReais)
		if ierr != nil {
			h.log.Error("billing: itau PIX falhou",
				zap.String("domain", domain), zap.Error(ierr))
			return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{
				"error": "falha ao gerar PIX no Itaú: " + ierr.Error(),
			})
		}
		if copyPaste == "" {
			return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{
				"error": "Itaú não retornou o copia-e-cola do PIX",
			})
		}
		charge := &db.BillingCharge{
			ID:              uuid.New(),
			Domain:          domain,
			Plan:            plan,
			Method:          "pix",
			AmountCents:     amount,
			ReferenceNum:    ref,
			MPTransactionID: itauTxidFromRef(ref), // txid — o webhook reconcilia por ele
			BoletoURL:       copyPaste,            // reaproveita o campo pro copia-e-cola
		}
		if err := h.repo.CreateBillingCharge(ctx, charge); err != nil {
			h.log.Error("billing: persistir cobranca PIX Itaú falhou", zap.Error(err))
		}
		redeemCupom()
		h.log.Info("billing: PIX Itaú criado",
			zap.String("domain", domain), zap.String("reference", ref),
			zap.Int64("amount_cents", amount))
		out := addCupomOut(fiber.Map{
			"ok":             true,
			"plan":           plan,
			"method":         "pix",
			"reference_num":  ref,
			"amount_cents":   amount,
			"pix_copy_paste": copyPaste,
			"hint":           "Pague o PIX pelo app do seu banco. A liberação é automática em segundos.",
		})
		if qrBase64 != "" {
			out["pix_qr_base64"] = qrBase64
		}
		return c.JSON(out)
	}

	// ─── Boleto Itaú (Cash Management) ─────────────────────────────────
	due := time.Now().AddDate(0, 0, 5) // vence em 5 dias
	// Documento do pagador: usa o domínio como fallback de identificação; o
	// pagador real é a empresa cliente. Sem CNPJ do tenant, emite com o CNPJ da
	// própria conta beneficiária (aceito pelo Itaú para boleto de serviço).
	payerDoc := digitsOnly(body.PayerName) // se vier CNPJ/CPF no campo, usa; senão vazio
	bol, berr := h.itauBoletoCharge(ctx, ref, planLabel, amount, payerName, payerDoc, due.Format("2006-01-02"))
	if berr != nil {
		h.log.Error("billing: itau boleto falhou",
			zap.String("domain", domain), zap.Error(berr))
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{
			"error": "falha ao gerar boleto no Itaú: " + berr.Error(),
		})
	}
	if bol.LinhaDigitavel == "" {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{
			"error": "Itaú não retornou a linha digitável do boleto",
		})
	}
	charge := &db.BillingCharge{
		ID:              uuid.New(),
		Domain:          domain,
		Plan:            plan,
		Method:          "boleto",
		AmountCents:     amount,
		ReferenceNum:    ref,
		MPTransactionID: bol.NossoNumero, // nosso número — reconciliação do boleto
		BoletoURL:       bol.LinhaDigitavel,
	}
	if err := h.repo.CreateBillingCharge(ctx, charge); err != nil {
		h.log.Error("billing: persistir cobranca boleto Itaú falhou", zap.Error(err))
	}
	redeemCupom()
	h.log.Info("billing: boleto Itaú criado",
		zap.String("domain", domain), zap.String("reference", ref),
		zap.String("nosso_numero", bol.NossoNumero), zap.Int64("amount_cents", amount))
	out := addCupomOut(fiber.Map{
		"ok":              true,
		"plan":            plan,
		"method":          "boleto",
		"reference_num":   ref,
		"amount_cents":    amount,
		"linha_digitavel": bol.LinhaDigitavel,
		"codigo_barras":   bol.CodigoBarras,
		"nosso_numero":    bol.NossoNumero,
		"due_date":        due.Format("2006-01-02"),
		"hint":            "Copie a linha digitável e pague no seu banco. A liberação é automática após a compensação.",
	})
	if bol.PixCopiaCola != "" {
		out["pix_copy_paste"] = bol.PixCopiaCola // Bolecode: boleto com PIX
	}
	return c.JSON(out)
}

// digitsOnly retorna só os dígitos de s (para extrair CNPJ/CPF de um campo).
func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// firstNonEmpty devolve a primeira string nao-vazia (helper de mensagens).
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// GET /ui/billing/charges — historico de cobrancas do tenant atual.
func (h *handlers) uiBillingCharges(c *fiber.Ctx) error {
	domain, ok := c.Locals("tenant_domain").(string)
	if !ok || domain == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "tenant nao identificado"})
	}
	charges, err := h.repo.ListBillingChargesByDomain(c.Context(), domain, 20)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"charges": charges})
}

