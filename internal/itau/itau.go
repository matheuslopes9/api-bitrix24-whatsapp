// Package itau — cliente PIX Recebimentos direto do Itaú (API regulatorio-pix).
//
// DIFERENTE do MaxiPago: aqui falamos direto com o banco, autenticando por
// mTLS (certificado da empresa) + OAuth client_credentials. O fluxo replica o
// que ja' funciona em producao no projeto de faturamento (boleto): as
// credenciais vao NO CORPO do token (nao Basic auth — o STS rejeita Basic com
// "CN invalido"), e o CN do certificado == client_id valida a identidade.
//
// Produto: "Recebimentos PIX". IMPORTANTE — o certificado da empresa e' o mesmo
// do boleto, mas o Client ID precisa ter o produto PIX habilitado pelo gerente;
// senao a primeira chamada /cob retorna 403 Acesso Negado.
//
// Fluxo de cobranca:
//   1. obterToken()                  -> access_token (cacheado ~5min)
//   2. CriarCobranca(txid, valor)    -> PUT /cob/{txid} -> pixCopiaECola + txid
//   3. ObterQRCode(txid)             -> GET /cob/{txid}/qrcode -> imagem base64
//   4. cliente paga -> Itau chama nosso webhook /billing/itau/pix
package itau

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// Config reune tudo que o cliente Itau precisa. Vem da tela admin (billing) ou
// do env, resolvido pela camada de API antes de instanciar o Client.
type Config struct {
	ClientID     string // ITAU_CLIENT_ID — tambem e' o CN do certificado
	ClientSecret string // ITAU_CLIENT_SECRET
	APIKey       string // x-itau-apikey (se vazio, cai no ClientID — padrao comum)
	ChavePIX     string // chave PIX que recebe (a "chave" no corpo da cobranca)
	CertPath     string // caminho do .crt (ITAU_CERT_PATH)
	KeyPath      string // caminho do .key (ITAU_KEY_PATH)
	Ambiente     string // "producao" | "sandbox"
	BaseURL      string // base da API PIX (regulatorio-pix/v2). Vazio => default por ambiente.
	TokenURL     string // STS. Vazio => default sts.itau.com.br.
}

// URLs padrao. baseURL de producao e' publica; a de sandbox depende do DNS de
// homologacao que o gerente informa (placeholder iupipes_env_dns na collection),
// entao deixamos configuravel via Config.BaseURL.
const (
	defaultTokenURL   = "https://sts.itau.com.br/api/oauth/token"
	defaultBaseURLPrd = "https://pix-pj.api.itau.com/regulatorio-pix/v2"
)

func (c Config) tokenURL() string {
	if c.TokenURL != "" {
		return c.TokenURL
	}
	return defaultTokenURL
}

func (c Config) baseURL() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return defaultBaseURLPrd
}

func (c Config) apiKey() string {
	if c.APIKey != "" {
		return c.APIKey
	}
	return c.ClientID
}

// CertificadoDisponivel diz se o par cert/key existe em disco. Em producao o
// mTLS e' obrigatorio; em sandbox pode nao exigir (permite testar auth sem cert).
func (c Config) CertificadoDisponivel() bool {
	if c.CertPath == "" || c.KeyPath == "" {
		return false
	}
	if _, err := os.Stat(c.CertPath); err != nil {
		return false
	}
	if _, err := os.Stat(c.KeyPath); err != nil {
		return false
	}
	return true
}

// Client fala com a API PIX do Itau. Reutiliza um http.Client com o transport
// mTLS e cacheia o access_token em memoria (thread-safe).
type Client struct {
	cfg  Config
	http *http.Client

	mu       sync.Mutex
	token    string
	tokenExp time.Time
}

// tokenMargem — renova o token 30s antes de expirar (igual ao auth.ts de ref).
const tokenMargem = 30 * time.Second

// New monta o Client. Carrega o certificado mTLS se disponivel; em producao,
// exige. Retorna erro se producao sem certificado, ou cert/key ilegiveis.
func New(cfg Config) (*Client, error) {
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}

	if cfg.CertificadoDisponivel() {
		cert, err := tls.LoadX509KeyPair(cfg.CertPath, cfg.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("itau: falha ao carregar certificado mTLS (%s / %s): %w",
				cfg.CertPath, cfg.KeyPath, err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	} else if strings.EqualFold(cfg.Ambiente, "producao") {
		return nil, fmt.Errorf("itau: certificado mTLS obrigatorio em producao nao encontrado (%s / %s)",
			cfg.CertPath, cfg.KeyPath)
	}

	return &Client{
		cfg: cfg,
		http: &http.Client{
			Timeout:   30 * time.Second,
			Transport: &http.Transport{TLSClientConfig: tlsCfg},
		},
	}, nil
}

// obterToken devolve um access_token valido, do cache ou renovando via STS.
// Credenciais NO CORPO (client_credentials), nunca Basic auth.
func (c *Client) obterToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.token != "" && time.Now().Before(c.tokenExp.Add(-tokenMargem)) {
		return c.token, nil
	}

	body := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.cfg.ClientID},
		"client_secret": {c.cfg.ClientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.cfg.tokenURL(), strings.NewReader(body.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	// Headers que a collection oficial do Itau envia na obtencao de token.
	// Alguns ambientes exigem; enviar sempre nao atrapalha.
	req.Header.Set("x-itau-flowID", "1")
	req.Header.Set("x-itau-correlationID", "2")

	resp, err := c.http.Do(req)
	if err != nil {
		// NAO propagar o erro cru: pode carregar headers com o secret. Mensagem generica.
		return "", fmt.Errorf("itau: falha na autenticacao (sem resposta do STS)")
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		// Loga status, nunca o corpo do request (que tem o secret).
		return "", fmt.Errorf("itau: STS retornou HTTP %d ao obter token", resp.StatusCode)
	}

	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.AccessToken == "" {
		return "", fmt.Errorf("itau: resposta de token invalida (sem access_token)")
	}

	exp := out.ExpiresIn
	if exp <= 0 {
		exp = 300 // 5 min default
	}
	c.token = out.AccessToken
	c.tokenExp = time.Now().Add(time.Duration(exp) * time.Second)
	return c.token, nil
}

// ─── Cobranca PIX imediata (/cob) ──────────────────────────────────────────

// Cobranca e' o retorno util de uma criacao de cobranca: o codigo copia-e-cola
// (EMV) que o cliente cola no app do banco, e o txid pra reconciliacao.
type Cobranca struct {
	Txid          string
	PixCopiaECola string // campo pixCopiaECola do Itau (o "EMV")
	Location      string // URL do QR dinamico
	Status        string // ATIVA, CONCLUIDA, ...
}

// erro do dominio PIX (bcb) — o Itau devolve {type,title,status,detail,...}.
type pixErro struct {
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
}

// CriarCobranca emite um QR imediato com txid definido por nos (PUT /cob/{txid}).
// valorReais no formato "123.45" (string, 2 casas). txid: 26-35 chars [a-zA-Z0-9].
// solicitacao: texto opcional ao pagador (ex: "Assinatura UC Talk Pro").
func (c *Client) CriarCobranca(ctx context.Context, txid, valorReais, solicitacao string) (*Cobranca, error) {
	tok, err := c.obterToken(ctx)
	if err != nil {
		return nil, err
	}

	payload := map[string]interface{}{
		"valor": map[string]string{"original": valorReais},
		"chave": c.cfg.ChavePIX,
	}
	if solicitacao != "" {
		payload["solicitacaoPagador"] = solicitacao
	}
	bodyBytes, _ := json.Marshal(payload)

	endpoint := c.cfg.baseURL() + "/cob/" + url.PathEscape(txid)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	c.setHeaders(req, tok)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("itau: falha ao criar cobranca: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, c.mapErro(resp.StatusCode, raw)
	}

	var out struct {
		Txid          string `json:"txid"`
		PixCopiaECola string `json:"pixCopiaECola"`
		Location      string `json:"location"`
		Status        string `json:"status"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("itau: resposta de cobranca ilegivel: %w", err)
	}
	if out.Txid == "" {
		out.Txid = txid
	}
	return &Cobranca{
		Txid:          out.Txid,
		PixCopiaECola: out.PixCopiaECola,
		Location:      out.Location,
		Status:        out.Status,
	}, nil
}

// QRCode e' o retorno de ObterQRCode: o EMV e a imagem PNG em base64 pra exibir.
type QRCode struct {
	EMV          string // copia-e-cola
	ImagemBase64 string // PNG base64 (pra <img src="data:image/png;base64,...">)
}

// ObterQRCode busca a imagem do QR de uma cobranca (GET /cob/{txid}/qrcode).
// O corpo vem como {"value": {"emv","imagem_base64",...}}.
func (c *Client) ObterQRCode(ctx context.Context, txid string) (*QRCode, error) {
	tok, err := c.obterToken(ctx)
	if err != nil {
		return nil, err
	}
	endpoint := c.cfg.baseURL() + "/cob/" + url.PathEscape(txid) + "/qrcode"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req, tok)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("itau: falha ao obter QR: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, c.mapErro(resp.StatusCode, raw)
	}

	var out struct {
		Value struct {
			EMV          string `json:"emv"`
			ImagemBase64 string `json:"imagem_base64"`
		} `json:"value"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("itau: resposta de QR ilegivel: %w", err)
	}
	return &QRCode{EMV: out.Value.EMV, ImagemBase64: out.Value.ImagemBase64}, nil
}

// setHeaders aplica auth e os headers padrao das chamadas da API PIX.
func (c *Client) setHeaders(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("x-itau-apikey", c.cfg.apiKey())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
}

// DiagResultado e' o retorno cru de um teste de diagnostico contra um host.
type DiagResultado struct {
	BaseURL    string `json:"base_url"`
	Endpoint   string `json:"endpoint"`
	HTTPStatus int    `json:"http_status"`
	Corpo      string `json:"corpo"`  // resposta crua (truncada)
	Erro       string `json:"erro"`   // erro de rede/TLS, se houver
}

// DiagnosticarCob tenta criar uma cobranca PIX contra uma baseURL ESPECIFICA
// (sobrescrevendo a config) e devolve status + corpo cru, sem mascarar. Serve
// pra descobrir qual host/rota o produto PIX PJ usa quando a chamada normal da
// 404. NAO persiste nada. valorReais ex: "1.00".
func (c *Client) DiagnosticarCob(ctx context.Context, baseURL, txid, valorReais, chave string) DiagResultado {
	base := strings.TrimRight(baseURL, "/")
	endpoint := base + "/cob/" + url.PathEscape(txid)
	res := DiagResultado{BaseURL: base, Endpoint: endpoint}

	tok, err := c.obterToken(ctx)
	if err != nil {
		res.Erro = "token: " + err.Error()
		return res
	}

	payload := map[string]interface{}{
		"valor": map[string]string{"original": valorReais},
		"chave": chave,
	}
	bodyBytes, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		res.Erro = err.Error()
		return res
	}
	c.setHeaders(req, tok)

	resp, err := c.http.Do(req)
	if err != nil {
		res.Erro = err.Error() // erro de rede/DNS/TLS — revela host inexistente
		return res
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	res.HTTPStatus = resp.StatusCode
	res.Corpo = truncate(string(raw), 500)
	return res
}

// mapErro transforma um corpo de erro do Itau numa mensagem util (sem vazar nada).
func (c *Client) mapErro(status int, raw []byte) error {
	var pe pixErro
	if json.Unmarshal(raw, &pe) == nil && pe.Title != "" {
		// 403 aqui geralmente = produto PIX nao habilitado pro Client ID.
		if status == http.StatusForbidden {
			return fmt.Errorf("itau: acesso negado (HTTP 403) — verifique se o produto Recebimentos PIX esta habilitado para o Client ID. Detalhe: %s", pe.Detail)
		}
		return fmt.Errorf("itau: %s (HTTP %d): %s", pe.Title, status, pe.Detail)
	}
	return fmt.Errorf("itau: erro HTTP %d ao chamar a API PIX", status)
}
