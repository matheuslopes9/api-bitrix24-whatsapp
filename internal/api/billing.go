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
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"go.uber.org/zap"
)

// ─── Cliente XML maxiPago ──────────────────────────────────────────────────

func (h *handlers) maxipagoBaseURL() string {
	if strings.EqualFold(h.cfg.Billing.MaxiPagoEnv, "production") {
		return "https://api.maxipago.net"
	}
	return "https://testapi.maxipago.net"
}

func (h *handlers) maxipagoConfigured() bool {
	return h.cfg.Billing.MaxiPagoMerchantID != "" && h.cfg.Billing.MaxiPagoMerchantKey != ""
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
}

type mpBoleto struct {
	ExpirationDate string `xml:"expirationDate"` // YYYY-MM-DD
	Number         string `xml:"number"`         // nosso numero (sequencial do lojista)
	Instructions   string `xml:"instructions"`
}

type mpPayment struct {
	ChargeTotal string `xml:"chargeTotal"` // "199.00"
}

// mpTransactionResponse — campos que interessam da resposta.
type mpTransactionResponse struct {
	XMLName         xml.Name `xml:"transaction-response"`
	ResponseCode    string   `xml:"responseCode"`
	ResponseMessage string   `xml:"responseMessage"`
	OrderID         string   `xml:"orderID"`
	TransactionID   string   `xml:"transactionID"`
	ReferenceNum    string   `xml:"referenceNum"`
	BoletoURL       string   `xml:"boletoUrl"`
	ErrorMessage    string   `xml:"errorMessage"`
	ProcessorMsg    string   `xml:"processorMessage"`
}

// maxipagoCreateBoleto cria uma venda-boleto e retorna (resposta, raw, err).
func (h *handlers) maxipagoCreateBoleto(ctx context.Context, referenceNum, payerName, planLabel string, amountCents int64, dueDate time.Time) (*mpTransactionResponse, string, error) {
	reqBody := mpTransactionRequest{
		Version: "3.1.1.15",
		Verification: mpVerification{
			MerchantID:  h.cfg.Billing.MaxiPagoMerchantID,
			MerchantKey: h.cfg.Billing.MaxiPagoMerchantKey,
		},
		Order: mpOrder{Sale: mpSale{
			ProcessorID:  h.cfg.Billing.ProcessorBoleto,
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

	xmlBytes, err := xml.Marshal(reqBody)
	if err != nil {
		return nil, "", err
	}
	payload := []byte(xml.Header + string(xmlBytes))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		h.maxipagoBaseURL()+"/UniversalAPI/postXML", bytes.NewReader(payload))
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

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ─── Endpoints ─────────────────────────────────────────────────────────────

// POST /ui/billing/checkout — body {method:"boleto", payer_name:"..."}
// Cria a cobranca do plano Pro no maxiPago e devolve a URL do boleto.
// Requer cookie tenant (requireTenantOrAdmin) — o domain vem do contexto.
func (h *handlers) uiBillingCheckout(c *fiber.Ctx) error {
	if !h.maxipagoConfigured() {
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
	}
	_ = c.BodyParser(&body)
	if body.Method == "" {
		body.Method = "boleto"
	}
	if body.Method != "boleto" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "metodo nao suportado ainda — use boleto",
		})
	}
	// 2 planos pagos: basic e pro. Trial e' so' o periodo de teste do Basico.
	plan := strings.ToLower(strings.TrimSpace(body.Plan))
	var amount int64
	switch plan {
	case "basic":
		amount = int64(h.cfg.Billing.BasicPriceCents)
	case "", "pro":
		plan = "pro"
		amount = int64(h.cfg.Billing.ProPriceCents)
	default:
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "plano invalido — use basic ou pro",
		})
	}
	payerName := strings.TrimSpace(body.PayerName)
	if payerName == "" {
		payerName = domain
	}

	ref := "uctalk-" + strings.ReplaceAll(uuid.New().String(), "-", "")[:16]
	due := time.Now().AddDate(0, 0, 5) // vence em 5 dias

	planLabel := "Basico"
	if plan == "pro" {
		planLabel = "Pro"
	}
	ctx := c.Context()
	mpResp, raw, err := h.maxipagoCreateBoleto(ctx, ref, payerName, planLabel, amount, due)
	if err != nil {
		h.log.Error("billing: maxipago boleto falhou",
			zap.String("domain", domain), zap.Error(err))
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{
			"error": "falha ao gerar boleto no gateway: " + err.Error(),
		})
	}
	h.log.Info("billing: maxipago resposta",
		zap.String("domain", domain),
		zap.String("reference", ref),
		zap.String("response_code", mpResp.ResponseCode),
		zap.String("raw", truncateStr(raw, 500)))

	if mpResp.ResponseCode != "0" || mpResp.BoletoURL == "" {
		msg := mpResp.ErrorMessage
		if msg == "" {
			msg = mpResp.ResponseMessage
		}
		if msg == "" {
			msg = mpResp.ProcessorMsg
		}
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{
			"error":         "gateway recusou a cobranca: " + msg,
			"response_code": mpResp.ResponseCode,
		})
	}

	charge := &db.BillingCharge{
		ID:              uuid.New(),
		Domain:          domain,
		Plan:            plan,
		Method:          "boleto",
		AmountCents:     amount,
		ReferenceNum:    ref,
		MPOrderID:       mpResp.OrderID,
		MPTransactionID: mpResp.TransactionID,
		BoletoURL:       mpResp.BoletoURL,
	}
	if err := h.repo.CreateBillingCharge(ctx, charge); err != nil {
		h.log.Error("billing: persistir cobranca falhou", zap.Error(err))
		// Nao aborta — o boleto ja existe no gateway; devolve a URL mesmo assim.
	}

	return c.JSON(fiber.Map{
		"ok":            true,
		"plan":          plan,
		"reference_num": ref,
		"boleto_url":    mpResp.BoletoURL,
		"amount_cents":  amount,
		"due_date":      due.Format("2006-01-02"),
		"hint":          "Abra o boleto e pague. Assim que o maxiPago confirmar, o plano e' liberado automaticamente.",
	})
}

// POST /billing/maxipago/postback — notificacao de status do maxiPago.
// PUBLICO (o gateway chama). Configure esta URL no portal maxiPago:
//   Configuracoes da Loja > URL de notificacao:
//   https://uctalk.uctechnology.com.br/billing/maxipago/postback
//
// Formato do POST varia (form-encoded com campos ou xml=<...>). Parser
// TOLERANTE: procura referenceNum + indicadores de pago no corpo bruto e
// loga o payload inteiro pra auditar/afinar no sandbox.
func (h *handlers) billingMaxipagoPostback(c *fiber.Ctx) error {
	raw := string(c.Body())
	h.log.Info("billing: postback maxipago recebido",
		zap.String("content_type", c.Get("Content-Type")),
		zap.String("raw", truncateStr(raw, 2000)))

	// referenceNum: tenta form field, depois regex-free scan no corpo.
	ref := c.FormValue("referenceNum")
	if ref == "" {
		ref = extractXMLTag(raw, "referenceNum")
	}
	if ref == "" {
		ref = extractXMLTag(raw, "referenceNumber")
	}
	if ref == "" {
		h.log.Warn("billing: postback sem referenceNum — ignorado")
		return c.SendStatus(fiber.StatusOK) // 200 pro gateway nao re-tentar infinito
	}

	// Estado: transactionState numerico e/ou texto. Manual: estados de
	// transacao — "Captured"/"Paga". Boleto pago = state 3 (captured) na
	// pratica do sandbox; tambem aceitamos palavras-chave.
	state := c.FormValue("transactionState")
	if state == "" {
		state = extractXMLTag(raw, "transactionState")
	}
	statusWord := strings.ToLower(raw)
	paid := state == "3" ||
		strings.Contains(statusWord, ">captured<") ||
		strings.Contains(statusWord, ">paid<") ||
		strings.Contains(statusWord, ">pago<") ||
		strings.Contains(statusWord, "boleto pago")

	if !paid {
		h.log.Info("billing: postback nao-pago (aguardando)",
			zap.String("reference", ref), zap.String("state", state))
		return c.SendStatus(fiber.StatusOK)
	}

	ctx := c.Context()
	charge, err := h.repo.GetBillingChargeByReference(ctx, ref)
	if err != nil || charge == nil {
		h.log.Warn("billing: postback pago mas cobranca nao encontrada",
			zap.String("reference", ref), zap.Error(err))
		return c.SendStatus(fiber.StatusOK)
	}

	changed, err := h.repo.MarkBillingChargePaid(ctx, ref, truncateStr(raw, 4000))
	if err != nil {
		h.log.Error("billing: marcar pago falhou", zap.Error(err))
		return c.SendStatus(fiber.StatusOK)
	}
	if !changed {
		// Ja estava paga (postback duplicado) — idempotente.
		return c.SendStatus(fiber.StatusOK)
	}

	// LIBERACAO AUTOMATICA: ativa O PLANO PAGO (basic ou pro) por N dias.
	paidPlan := charge.Plan
	if paidPlan != "basic" && paidPlan != "pro" {
		paidPlan = "pro" // defensivo: charge legada sem plano valido
	}
	until := time.Now().AddDate(0, 0, h.cfg.Billing.ActivateDays)
	notes := fmt.Sprintf("pagamento maxipago plano=%s ref=%s em %s",
		paidPlan, ref, time.Now().Format("2006-01-02 15:04"))
	if err := h.repo.SetTenantPlan(ctx, charge.Domain, paidPlan, "active", &until, notes); err != nil {
		h.log.Error("billing: ativar plano falhou",
			zap.String("domain", charge.Domain), zap.Error(err))
		return c.SendStatus(fiber.StatusOK)
	}

	h.log.Info("billing: PAGAMENTO CONFIRMADO — plano ativado",
		zap.String("domain", charge.Domain),
		zap.String("plan", paidPlan),
		zap.String("reference", ref),
		zap.Time("active_until", until))
	return c.SendStatus(fiber.StatusOK)
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

// extractXMLTag extrai o conteudo de <tag>...</tag> de um corpo bruto sem
// parsear o XML inteiro (o postback pode nao ser XML valido completo).
func extractXMLTag(body, tag string) string {
	openTag := "<" + tag + ">"
	closeTag := "</" + tag + ">"
	i := strings.Index(body, openTag)
	if i < 0 {
		return ""
	}
	j := strings.Index(body[i+len(openTag):], closeTag)
	if j < 0 {
		return ""
	}
	return strings.TrimSpace(body[i+len(openTag) : i+len(openTag)+j])
}
