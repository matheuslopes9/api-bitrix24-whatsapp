package itau

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// BoletoConfig complementa a Config com os dados da conta PJ necessarios pra
// emitir boleto (Cash Management V2). Separado da Config PIX porque a base de
// URL e os campos de conta so' importam pro boleto.
type BoletoConfig struct {
	BaseURL  string // base cash_management/v2 (vazio => default producao)
	Agencia  string // 4 digitos
	Conta    string // 7 digitos
	ContaDAC string // 1 digito
	Carteira string // 109
	Especie  string // 08
	Etapa    string // "efetivacao" (real) | "validacao" (teste)
}

const defaultBoletoURLPrd = "https://api.itau.com.br/cash_management/v2"

func (bc BoletoConfig) baseURL() string {
	if bc.BaseURL != "" {
		return strings.TrimRight(bc.BaseURL, "/")
	}
	return defaultBoletoURLPrd
}

// EntradaBoleto — dados pra registrar um boleto. Espelha EntradaBoleto do TS.
type EntradaBoleto struct {
	PagadorTipoDoc  string // "CNPJ" | "CPF"
	PagadorDoc      string // so' digitos
	PagadorNome     string
	PagadorEmail    string
	Logradouro      string
	Bairro          string
	Cidade          string
	UF              string
	CEP             string
	ValorCentavos   int64
	Vencimento      string // AAAA-MM-DD
	SeuNumero       string // identificador nosso (<=10 chars no titulo)
	NossoNumero     string // controle — crescente e unico
}

// Boleto — resultado da emissao.
type Boleto struct {
	IDBoleto       string
	NossoNumero    string
	LinhaDigitavel string
	CodigoBarras   string
	PixCopiaCola   string // se Bolecode habilitado
}

// digits mantem so' [0-9].
func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// padLeft zero-padding a esquerda ate n; se maior, corta pegando os n' ultimos.
func padLeft(s string, n int) string {
	s = digits(s)
	for len(s) < n {
		s = "0" + s
	}
	if len(s) > n {
		s = s[len(s)-n:]
	}
	return s
}

// valor17 — centavos em string de 17 digitos zero-padded (formato Itau).
func valor17(centavos int64) string {
	return padLeft(fmt.Sprintf("%d", centavos), 17)
}

// idBeneficiario = Agencia(4)+Conta(7)+DAC(1).
func (bc BoletoConfig) idBeneficiario() string {
	return padLeft(bc.Agencia, 4) + padLeft(bc.Conta, 7) +
		firstDigit(bc.ContaDAC)
}

func firstDigit(s string) string {
	d := digits(s)
	if d == "" {
		return "0"
	}
	return d[len(d)-1:]
}

// montarPayloadBoleto monta o corpo do POST /boletos (envolto em {data:...}
// pelo chamador). Fiel ao mapBoleto.ts.
func (bc BoletoConfig) montarPayload(e EntradaBoleto) map[string]interface{} {
	var tipoPessoa map[string]interface{}
	if strings.EqualFold(e.PagadorTipoDoc, "CNPJ") {
		tipoPessoa = map[string]interface{}{
			"codigo_tipo_pessoa": "J",
			"numero_cadastro_nacional_pessoa_juridica": digits(e.PagadorDoc),
		}
	} else {
		tipoPessoa = map[string]interface{}{
			"codigo_tipo_pessoa":          "F",
			"numero_cadastro_pessoa_fisica": digits(e.PagadorDoc),
		}
	}

	formaEnvio := "impressão"
	if e.PagadorEmail != "" {
		formaEnvio = "email"
	}

	nossoNum := padLeft(e.NossoNumero, 8)
	if len(digits(e.NossoNumero)) > 8 {
		nossoNum = padLeft(e.NossoNumero, 16)
	}

	pagador := map[string]interface{}{
		"pessoa": map[string]interface{}{
			"nome_pessoa": e.PagadorNome,
			"tipo_pessoa": tipoPessoa,
		},
		"endereco": map[string]interface{}{
			"nome_logradouro": e.Logradouro,
			"nome_bairro":     e.Bairro,
			"nome_cidade":     e.Cidade,
			"sigla_UF":        e.UF,
			"numero_CEP":      digits(e.CEP),
		},
	}
	if e.PagadorEmail != "" {
		pagador["texto_endereco_email"] = e.PagadorEmail
	}

	seuNum := e.SeuNumero
	if len(seuNum) > 10 {
		seuNum = seuNum[:10] // max 10 no Itau
	}

	dadoBoleto := map[string]interface{}{
		"descricao_instrumento_cobranca": "boleto",
		"tipo_boleto":                    "a vista",
		"forma_envio":                    formaEnvio,
		"codigo_carteira":                bc.Carteira,
		"codigo_especie":                 bc.Especie,
		"data_emissao":                   time.Now().Format("2006-01-02"),
		"valor_total_titulo":             valor17(e.ValorCentavos),
		"desconto_expresso":              false,
		"pagador":                        pagador,
		"dados_individuais_boleto": []map[string]interface{}{
			{
				"numero_nosso_numero": nossoNum,
				"data_vencimento":     e.Vencimento,
				"valor_titulo":        valor17(e.ValorCentavos),
				"texto_seu_numero":    seuNum,
			},
		},
	}

	etapa := bc.Etapa
	if etapa == "" {
		etapa = "efetivacao"
	}
	return map[string]interface{}{
		"etapa_processo_boleto": etapa,
		"codigo_canal_operacao": "API",
		"beneficiario":          map[string]interface{}{"id_beneficiario": bc.idBeneficiario()},
		"dado_boleto":           dadoBoleto,
	}
}

// RegistrarBoleto emite um boleto na API Cash Management do Itau. Reusa o mesmo
// mTLS + OAuth do Client (produto boleto usa o mesmo certificado do PIX).
func (c *Client) RegistrarBoleto(ctx context.Context, bc BoletoConfig, e EntradaBoleto) (*Boleto, error) {
	tok, err := c.obterToken(ctx)
	if err != nil {
		return nil, err
	}

	payload := map[string]interface{}{"data": bc.montarPayload(e)}
	bodyBytes, _ := json.Marshal(payload)

	endpoint := bc.baseURL() + "/boletos"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-itau-apikey", c.cfg.apiKey())
	req.Header.Set("x-itau-correlationID", e.SeuNumero)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("itau: falha ao registrar boleto: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == http.StatusForbidden {
			return nil, fmt.Errorf("itau: acesso negado (HTTP 403) ao emitir boleto — verifique se o produto Boleto/Cash Management esta habilitado para o Client ID")
		}
		return nil, fmt.Errorf("itau: rejeitou o boleto (HTTP %d): %s", resp.StatusCode, truncate(string(raw), 300))
	}

	return parseBoletoResp(raw)
}

// StatusBoleto e' o resultado de uma consulta: se o boleto ja' foi pago/
// liquidado (Pago=true) e a situacao textual crua do Itau (pra log/debug).
type StatusBoleto struct {
	NossoNumero string
	Pago        bool
	Situacao    string // texto cru do Itau (ex: "EM ABERTO", "LIQUIDADO", "BAIXADO")
}

// ConsultarBoleto consulta a situacao de um boleto pelo nosso_numero, pra
// reconciliar pagamento (o boleto NAO tem webhook como o PIX). Reusa o mesmo
// mTLS + OAuth.
//
// ⚠️ ENDPOINT/CAMPO A CONFIRMAR COM O ITAU: a rota de consulta e o nome do
// campo de situacao variam conforme a versao do produto Cash Management. O
// padrao mais comum e' GET /boletos/{id_beneficiario}/{nosso_numero} com um
// campo "codigo_situacao_geral_boleto" ou "situacao_geral_boleto" no retorno.
// Ajuste `endpoint` e `situacaoPaga()` abaixo conforme a doc da sua conta.
func (c *Client) ConsultarBoleto(ctx context.Context, bc BoletoConfig, nossoNumero string) (*StatusBoleto, error) {
	tok, err := c.obterToken(ctx)
	if err != nil {
		return nil, err
	}

	// GET /boletos/{id_beneficiario}/{nosso_numero}  (confirmar com o Itau)
	endpoint := bc.baseURL() + "/boletos/" + bc.idBeneficiario() + "/" + digits(nossoNumero)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-itau-apikey", c.cfg.apiKey())
	req.Header.Set("x-itau-correlationID", nossoNumero)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("itau: falha ao consultar boleto: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusNotFound {
		// Boleto ainda nao indexado / nao encontrado — trata como "em aberto".
		return &StatusBoleto{NossoNumero: nossoNumero, Pago: false, Situacao: "NAO_ENCONTRADO"}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("itau: consulta de boleto HTTP %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}

	return parseStatusBoleto(raw, nossoNumero), nil
}

// parseStatusBoleto interpreta o retorno da consulta. Procura o campo de
// situacao em varios nomes possiveis (o envelope {data:...} do v2 varia) e
// decide se esta pago via situacaoPaga().
func parseStatusBoleto(raw []byte, nossoNumero string) *StatusBoleto {
	st := &StatusBoleto{NossoNumero: nossoNumero}

	var top map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil {
		return st
	}
	data := top
	if d, ok := top["data"]; ok {
		var inner map[string]json.RawMessage
		if json.Unmarshal(d, &inner) == nil {
			data = inner
		}
	}

	// Tenta varios nomes de campo de situacao (Itau usa nomes diferentes por versao).
	for _, key := range []string{
		"situacao_geral_boleto", "codigo_situacao_geral_boleto",
		"situacao", "status", "descricao_situacao",
	} {
		if v, ok := data[key]; ok {
			var s string
			if json.Unmarshal(v, &s) == nil && s != "" {
				st.Situacao = s
				break
			}
		}
	}
	st.Pago = situacaoPaga(st.Situacao)
	return st
}

// situacaoPaga decide, a partir do texto/codigo de situacao, se o boleto foi
// efetivamente pago. Cobre os rotulos mais comuns; ajuste conforme sua conta.
func situacaoPaga(situacao string) bool {
	s := strings.ToUpper(strings.TrimSpace(situacao))
	switch s {
	case "LIQUIDADO", "PAGO", "BAIXADO_POR_PAGAMENTO", "07", "06":
		return true
	}
	// Heuristica de fallback: qualquer situacao que contenha "LIQUID" ou "PAG".
	return strings.Contains(s, "LIQUID") || strings.Contains(s, "PAGO")
}

// parseBoletoResp extrai os dados do retorno v2 do Itau (envelope {data:...}).
func parseBoletoResp(raw []byte) (*Boleto, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, fmt.Errorf("itau: resposta de boleto ilegivel: %w", err)
	}
	data := top
	if d, ok := top["data"]; ok {
		var inner map[string]json.RawMessage
		if json.Unmarshal(d, &inner) == nil {
			data = inner
		}
	}

	b := &Boleto{}
	if v, ok := data["id_boleto"]; ok {
		_ = json.Unmarshal(v, &b.IDBoleto)
	}

	// dado_boleto.dados_individuais_boleto[0]
	if db, ok := data["dado_boleto"]; ok {
		var dbObj struct {
			NossoNumero string `json:"nosso_numero"`
			Individuais []struct {
				NumeroNossoNumero  string `json:"numero_nosso_numero"`
				NumeroLinhaDigitavel string `json:"numero_linha_digitavel"`
				LinhaDigitavel     string `json:"linha_digitavel"`
				CodigoBarras       string `json:"codigo_barras"`
				DadosQRCode        struct {
					EMV string `json:"emv"`
				} `json:"dados_qrcode"`
			} `json:"dados_individuais_boleto"`
			DadosQRCode struct {
				EMV string `json:"emv"`
			} `json:"dados_qrcode"`
		}
		if json.Unmarshal(db, &dbObj) == nil {
			b.NossoNumero = dbObj.NossoNumero
			b.PixCopiaCola = dbObj.DadosQRCode.EMV
			if len(dbObj.Individuais) > 0 {
				ind := dbObj.Individuais[0]
				if ind.NumeroNossoNumero != "" {
					b.NossoNumero = ind.NumeroNossoNumero
				}
				b.LinhaDigitavel = firstNonEmptyStr(ind.NumeroLinhaDigitavel, ind.LinhaDigitavel)
				b.CodigoBarras = ind.CodigoBarras
				if ind.DadosQRCode.EMV != "" {
					b.PixCopiaCola = ind.DadosQRCode.EMV
				}
			}
		}
	}
	return b, nil
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
