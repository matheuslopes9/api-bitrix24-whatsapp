package bitrix

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"go.uber.org/zap"
)

// redactTokensRe encontra access_token/refresh_token/application_token em
// JSON ou form-encoded e troca por "[redacted]". Usado antes de logar
// payloads que podem conter credenciais OAuth.
var redactTokensRe = regexp.MustCompile(`("?(?:access_token|refresh_token|application_token|AUTH_ID|REFRESH_ID)"?\s*[:=]\s*"?)[^",&\s}]+`)

// RedactTokens substitui valores de campos sensiveis (access_token,
// refresh_token, application_token, AUTH_ID, REFRESH_ID) por [redacted].
// Aceita JSON e form-encoded. Exportado pra uso em outros pacotes que
// loguem payloads Bitrix.
func RedactTokens(s string) string {
	return redactTokensRe.ReplaceAllString(s, `${1}[redacted]`)
}

func redactTokens(s string) string { return RedactTokens(s) }

// TenantCreds contém as credenciais de uma conta Bitrix24 específica.
// Passado por chamada para suportar multi-tenancy sem re-instanciar o client.
type TenantCreds struct {
	Domain       string
	ClientID     string
	ClientSecret string
	RedirectURI  string
}

// normalizeDomain garante https:// e sem trailing slash.
func normalizeDomain(d string) string {
	d = strings.TrimRight(d, "/")
	if !strings.HasPrefix(d, "http") {
		d = "https://" + d
	}
	return d
}

// Client encapsula chamadas REST ao Bitrix24 com renovação automática de token.
// É stateless em relação a tenants — recebe TenantCreds por chamada.
type Client struct {
	repo *db.Repository
	http *http.Client
	log  *zap.Logger
	rl   *rateLimiter // rate limit por (domain, method) — 2 req/s

	// refreshMu serializa a renovacao de token por (domain|client_id).
	// O Bitrix rotaciona o refresh_token a cada uso, entao duas renovacoes
	// simultaneas do mesmo tenant se anulam.
	refreshMu   sync.Mutex
	refreshLock map[string]*sync.Mutex
}

// lockRefresh pega (criando se preciso) o mutex daquele tenant e devolve a
// funcao de liberacao.
func (c *Client) lockRefresh(key string) func() {
	c.refreshMu.Lock()
	if c.refreshLock == nil {
		c.refreshLock = map[string]*sync.Mutex{}
	}
	mu, ok := c.refreshLock[key]
	if !ok {
		mu = &sync.Mutex{}
		c.refreshLock[key] = mu
	}
	c.refreshMu.Unlock()

	mu.Lock()
	return mu.Unlock
}

func NewClient(repo *db.Repository, log *zap.Logger) *Client {
	return &Client{
		repo:        repo,
		// 45s e nao 15s. MEDIDO: a listagem de usuarios manda lotes que voltam
		// com ~800KB; o Bitrix responde em ~3s quando chamado direto, mas pelo
		// app o mesmo lote estourava o limite de 15s ("Client.Timeout exceeded
		// while awaiting headers") e sumia com todo funcionario de ID alto.
		// O limite existe pra nao pendurar worker em chamada morta — 45s ainda
		// cumpre isso, com folga pra resposta grande.
		http:        &http.Client{Timeout: 45 * time.Second},
		log:         log,
		rl:          newRateLimiter(),
		refreshLock: map[string]*sync.Mutex{},
	}
}

// ─── OAuth2 ───────────────────────────────────────────────────────────────

// AuthURL retorna a URL para iniciar o OAuth2 para um tenant específico.
func (c *Client) AuthURL(creds TenantCreds, state string) string {
	domain := normalizeDomain(creds.Domain)
	return fmt.Sprintf("%s/oauth/authorize/?client_id=%s&response_type=code&redirect_uri=%s&state=%s",
		domain, creds.ClientID, url.QueryEscape(creds.RedirectURI), state)
}

// ExchangeCode troca o código de autorização por tokens.
func (c *Client) ExchangeCode(ctx context.Context, creds TenantCreds, code string) error {
	domain := normalizeDomain(creds.Domain)
	resp, err := c.http.PostForm(domain+"/oauth/token/", url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {creds.ClientID},
		"client_secret": {creds.ClientSecret},
		"redirect_uri":  {creds.RedirectURI},
		"code":          {code},
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return c.saveTokenResponse(ctx, creds, resp.Body)
}

// SaveToken salva um token diretamente (usado no installation handler do app local).
// Sanitiza expiresIn: tokens do Bitrix vivem ~1h, então valores absurdos
// (<= 0 ou > 24h) indicam dados corrompidos e são truncados para 1 hora.
// Sem essa proteção, um expires_at no ano 2235 (corrupção observada em produção)
// faz o cliente nunca renovar o token, causando expired_token permanente.
func (c *Client) SaveToken(ctx context.Context, creds TenantCreds, accessToken, refreshToken string, expiresIn int) error {
	domain := normalizeDomain(creds.Domain)
	if expiresIn <= 0 || expiresIn > 86400 {
		c.log.Warn("SaveToken: expiresIn out of range, defaulting to 3600s",
			zap.String("domain", domain), zap.Int("expires_in_received", expiresIn))
		expiresIn = 3600
	}
	return c.repo.UpsertBitrixToken(ctx, &db.BitrixToken{
		ID:           uuid.New(),
		Domain:       domain,
		ClientID:     creds.ClientID, // chave que diferencia Local App do Partner App
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(expiresIn) * time.Second),
	})
}

// lookupToken le o token do banco com a MESMA regra do token(): por
// client_id quando ha' um, com fallback pro mais recente do dominio.
// Devolve nil em qualquer falha — os callers tratam nil como "nao tenho".
func (c *Client) lookupToken(ctx context.Context, creds TenantCreds) *db.BitrixToken {
	domain := normalizeDomain(creds.Domain)
	if creds.ClientID != "" {
		if t, err := c.repo.GetBitrixTokenByClientID(ctx, domain, creds.ClientID); err == nil && t != nil {
			return t
		}
	}
	if t, err := c.repo.GetBitrixToken(ctx, domain); err == nil {
		return t
	}
	return nil
}

// dominioNu tira esquema e barra final: "https://x.bitrix24.com.br/" -> "x.bitrix24.com.br".
func dominioNu(d string) string {
	d = strings.TrimSpace(d)
	d = strings.TrimPrefix(d, "https://")
	d = strings.TrimPrefix(d, "http://")
	return strings.TrimRight(d, "/")
}

// credenciaisDoEmissor devolve as credenciais com que este token PODE ser
// renovado.
//
// O PROBLEMA QUE ISSO RESOLVE: o mesmo portal pode ter token emitido por apps
// diferentes — o app global (Partner, credenciais da config) e o app instalado
// no portal do cliente (Local, credenciais em bitrix_accounts). O caminho de
// entrada usa as credenciais da bitrix_account; quase todo o resto usa as da
// config. Quando divergem — ou quando a config esta' VAZIA, que foi o caso
// observado — o Bitrix responde "wrong_client" em loop, o token vence e
// nenhuma mensagem mais chega no Contact Center.
//
// Ordem:
//  1. credencial do caller ja' e' a do emissor -> usa;
//  2. token sem emissor registrado (linha antiga) e caller tem credencial ->
//     usa, e' o comportamento historico;
//  3. procura nas bitrix_accounts do dominio a credencial COMPLETA do app
//     emissor. Cobre tambem o caso de nao termos credencial nenhuma: ai'
//     serve qualquer account do dominio que tenha client_id e secret.
//
// Devolve false quando nada serve — melhor falhar nomeando os apps do que
// mandar uma requisicao que ja' se sabe que sera' recusada.
func (c *Client) credenciaisDoEmissor(ctx context.Context, creds TenantCreds, t *db.BitrixToken) (TenantCreds, bool) {
	if creds.ClientID != "" && creds.ClientSecret != "" {
		if t.ClientID == "" || t.ClientID == creds.ClientID {
			return creds, true
		}
	}

	accts, err := c.repo.ListBitrixAccountsByDomain(ctx, dominioNu(creds.Domain))
	if err != nil {
		return creds, false
	}
	for _, a := range accts {
		if a.ClientID == "" || a.ClientSecret == "" {
			continue
		}
		// Token com emissor registrado: so' serve o app dele. Token sem
		// emissor (ou sem credencial nossa): qualquer app do dominio serve,
		// porque e' o app instalado naquele portal.
		if t.ClientID != "" && a.ClientID != t.ClientID {
			continue
		}
		corrigida := creds
		corrigida.ClientID = a.ClientID
		corrigida.ClientSecret = a.ClientSecret
		c.log.Info("refresh: usando as credenciais do app instalado no portal",
			zap.String("domain", dominioNu(creds.Domain)),
			zap.String("client_id_do_token", t.ClientID),
			zap.String("client_id_anterior", creds.ClientID),
			zap.String("client_id_usado", a.ClientID))
		return corrigida, true
	}
	return creds, false
}

// refreshToken renova o access token usando o refresh token.
// O endpoint OAuth2 do Bitrix24 é sempre oauth.bitrix.info, nunca o domínio da conta.
func (c *Client) refreshToken(ctx context.Context, creds TenantCreds, t *db.BitrixToken) error {
	// Sem refresh_token nao ha' o que renovar. Antes disso a chamada era
	// feita mesmo assim, falhava, e o caller tentava de novo — gerando a
	// enxurrada de "refreshing bitrix token" com prefixo vazio no log.
	// Falhar aqui, explicito, diz o que realmente precisa acontecer.
	if t.RefreshToken == "" {
		return fmt.Errorf("tenant %s sem refresh_token salvo — o app precisa ser reinstalado/reautorizado no portal Bitrix", creds.Domain)
	}

	// Serializa por dominio+client_id: o Bitrix ROTACIONA o refresh_token a
	// cada renovacao. Com N chamadas concorrentes, a primeira rotaciona e as
	// outras N-1 usam um refresh_token que acabou de ser invalidado — todas
	// falham, e antes do fix acima ainda gravavam vazio por cima. O log de
	// producao mostrou 10+ refreshes simultaneos do mesmo tenant.
	// Renova com as credenciais de QUEM EMITIU o token, nao com as que o
	// caller por acaso carregava — ver credenciaisDoEmissor.
	creds, okEmissor := c.credenciaisDoEmissor(ctx, creds, t)
	if !okEmissor {
		c.log.Error("refresh impossivel: nao ha' client_id/client_secret utilizavel para este portal",
			zap.String("domain", dominioNu(creds.Domain)),
			zap.String("client_id_do_token", t.ClientID),
			zap.String("client_id_da_config", creds.ClientID),
			zap.String("o_que_fazer", "defina BITRIX_CLIENT_ID/BITRIX_CLIENT_SECRET ou reinstale o app no portal pra gravar as credenciais na bitrix_account"))
		return fmt.Errorf("sem credencial utilizavel para renovar o token de %s "+
			"(client_id do token=%q, da config=%q): defina BITRIX_CLIENT_ID/BITRIX_CLIENT_SECRET "+
			"ou reinstale o app no portal", dominioNu(creds.Domain), t.ClientID, creds.ClientID)
	}

	key := normalizeDomain(creds.Domain) + "|" + creds.ClientID
	unlock := c.lockRefresh(key)
	defer unlock()

	// Outra goroutine pode ter renovado enquanto esperavamos o lock —
	// nesse caso o token do banco ja' esta' fresco e nao ha' o que fazer.
	if fresh := c.lookupToken(ctx, creds); fresh != nil && fresh.AccessToken != "" &&
		time.Now().Before(fresh.ExpiresAt.Add(-1*time.Minute)) {
		*t = *fresh
		return nil
	}

	c.log.Info("refreshing bitrix token",
		zap.String("domain", creds.Domain),
		zap.String("refresh_token_prefix", t.RefreshToken[:min(8, len(t.RefreshToken))]))

	resp, err := c.http.PostForm("https://oauth.bitrix.info/oauth/token/", url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {creds.ClientID},
		"client_secret": {creds.ClientSecret},
		"refresh_token": {t.RefreshToken},
	})
	if err != nil {
		c.log.Error("token refresh http error", zap.Error(err))
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	// SEGURANCA: nao logar o body cru — contem access_token/refresh_token.
	// Em caso de erro, o status code ja' indica o problema; log de debug
	// detalhado fica disponivel se DEBUG_LOG_RAW=1 (vide config).
	c.log.Info("token refresh response", zap.Int("status", resp.StatusCode), zap.Int("body_len", len(body)))
	if resp.StatusCode != http.StatusOK {
		// Body so em erro, redatado: tira access_token/refresh_token via regex.
		return fmt.Errorf("token refresh failed: status %d body %s", resp.StatusCode, redactTokens(string(body)))
	}
	return c.saveTokenResponse(ctx, creds, bytes.NewReader(body))
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	Domain       string `json:"domain"`

	// O Bitrix responde HTTP 200 com corpo de erro. Estes dois campos sao o
	// unico lugar que diz O QUE fazer, e sao seguros de logar (nao contem
	// token). Sem eles o log so' dizia "sem access_token/refresh_token", que
	// nao distingue "reautorize o app" de "client_secret errado".
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// acaoParaErroOAuth traduz o codigo do Bitrix na providencia concreta. E' a
// diferenca entre "espera passar" e "alguem precisa reinstalar o app agora".
func acaoParaErroOAuth(code string) string {
	switch code {
	case "invalid_grant", "expired_token":
		return "o refresh_token nao vale mais — reautorize o app no portal Bitrix (Aplicativos > UC Talk > reinstalar)"
	case "invalid_client", "wrong_client":
		return "client_id/client_secret nao batem com o app instalado no portal"
	case "NO_AUTH_FOUND":
		return "o portal nao reconhece esta autorizacao — o app foi removido ou reinstalado por fora"
	case "":
		return "o Bitrix respondeu sem access_token e sem codigo de erro"
	default:
		return "erro OAuth nao mapeado — ver codigo acima"
	}
}

func (c *Client) saveTokenResponse(ctx context.Context, creds TenantCreds, r io.Reader) error {
	var tr tokenResponse
	if err := json.NewDecoder(r).Decode(&tr); err != nil {
		return err
	}
	domain := normalizeDomain(creds.Domain)

	// NUNCA gravar token vazio por cima de um token bom.
	//
	// BUG CRITICO QUE ISTO CORRIGE: o endpoint OAuth do Bitrix as vezes
	// responde HTTP 200 com um CORPO DE ERRO (ex: {"error":"invalid_grant"},
	// 26 bytes — exatamente o body_len visto no log de producao). Como o
	// status era 200, o codigo seguia em frente, decodificava um JSON que
	// nao tem access_token nem refresh_token, e gravava DUAS STRINGS VAZIAS
	// por cima das credenciais validas — com expires_at = agora, porque
	// ExpiresIn tambem vinha 0.
	//
	// A partir dali o tenant estava morto de forma irreversivel: todo
	// refresh mandava refresh_token vazio, recebia erro, e regravava vazio.
	// E' a origem do 'refresh_token_prefix:""' seguido de NO_AUTH_FOUND em
	// loop. Recuperar exigia reinstalar o app no portal.
	//
	// Agora a resposta so' e' aceita se tiver os dois tokens. Caso
	// contrario devolve erro e o token ANTERIOR fica intacto no banco.
	if tr.AccessToken == "" || tr.RefreshToken == "" {
		acao := acaoParaErroOAuth(tr.Error)
		c.log.Error("resposta de token sem access_token/refresh_token — MANTENDO o token anterior",
			zap.String("domain", domain),
			zap.Bool("tem_access", tr.AccessToken != ""),
			zap.Bool("tem_refresh", tr.RefreshToken != ""),
			zap.String("erro_oauth", tr.Error),
			zap.String("descricao", tr.ErrorDescription),
			zap.String("o_que_fazer", acao))
		return fmt.Errorf("resposta de token invalida para %s: erro OAuth %q — %s", domain, tr.Error, acao)
	}
	return c.repo.UpsertBitrixToken(ctx, &db.BitrixToken{
		ID:           uuid.New(),
		Domain:       domain,
		ClientID:     creds.ClientID,
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second),
		Scope:        tr.Scope,
	})
}

// token retorna um token válido para as creds fornecidas, renovando se necessário.
// Usa (domain, client_id) para buscar — cada app tem seu próprio token isolado.
func (c *Client) token(ctx context.Context, creds TenantCreds) (*db.BitrixToken, error) {
	domain := normalizeDomain(creds.Domain)
	var t *db.BitrixToken
	var err error

	if creds.ClientID != "" {
		t, err = c.repo.GetBitrixTokenByClientID(ctx, domain, creds.ClientID)
	}
	if t == nil || err != nil {
		// Fallback: pega o token mais recente do domain (compatibilidade)
		t, err = c.repo.GetBitrixToken(ctx, domain)
	}
	if err != nil {
		return nil, fmt.Errorf("get token for %s (client_id=%s): %w", domain, creds.ClientID, err)
	}

	if time.Now().Add(60 * time.Second).After(t.ExpiresAt) {
		if err := c.refreshToken(ctx, creds, t); err != nil {
			return nil, fmt.Errorf("refresh token: %w", err)
		}
		if creds.ClientID != "" {
			t, err = c.repo.GetBitrixTokenByClientID(ctx, domain, creds.ClientID)
		} else {
			t, err = c.repo.GetBitrixToken(ctx, domain)
		}
		if err != nil {
			return nil, err
		}
	}
	return t, nil
}

// ─── REST Helper ─────────────────────────────────────────────────────────

func (c *Client) call(ctx context.Context, creds TenantCreds, method string, params map[string]interface{}) (json.RawMessage, error) {
	// Retry loop para tratar QUERY_LIMIT_EXCEEDED — Bitrix as vezes recusa
	// mesmo apos nosso rate limiter (concorrencia entre instancias, ou outras
	// integracoes no mesmo portal). Backoff exponencial 200ms -> 400ms -> 800ms.
	const maxRetries = 3
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(200*(1<<(attempt-1))) * time.Millisecond
			c.log.Warn("bitrix call: retrying after rate limit",
				zap.String("method", method),
				zap.Int("attempt", attempt),
				zap.Duration("backoff", backoff))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}
		raw, err := c.callOnce(ctx, creds, method, params)
		if err == nil {
			return raw, nil
		}
		lastErr = err
		if !isRateLimitError(err) {
			return nil, err // erro nao retryable
		}
	}
	return nil, lastErr
}

// callOnce executa UMA chamada REST ao Bitrix24, respeitando o rate limiter
// por (domain, method).
func (c *Client) callOnce(ctx context.Context, creds TenantCreds, method string, params map[string]interface{}) (json.RawMessage, error) {
	t, err := c.token(ctx, creds)
	if err != nil {
		return nil, err
	}

	domain := normalizeDomain(creds.Domain)

	// Bloqueia ate ter slot no rate limit (2 req/s por dominio+method).
	if err := c.rl.wait(ctx, domain, method); err != nil {
		return nil, err
	}

	body, _ := json.Marshal(params)
	reqURL := fmt.Sprintf("%s/rest/%s.json?auth=%s", domain, method, t.AccessToken)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Result           json.RawMessage `json:"result"`
		Error            string          `json:"error"`
		ErrorDescription string          `json:"error_description"`
	}
	if err := json.Unmarshal(rawBody, &result); err != nil {
		return nil, fmt.Errorf("decode bitrix response (status %d, body: %s): %w", resp.StatusCode, string(rawBody), err)
	}
	if result.Error != "" {
		// Alguns "erros" do Bitrix significam so' "o estado que voce quer ja'
		// vale" — bind repetido, delete de algo ausente. Os callers tratam
		// esses casos como sucesso (idempotencia), entao logar WARN aqui so'
		// gera ruido e mascara falha real. Ficam em Info.
		nivel := c.log.Warn
		if ehErroBenigno(result.Error, result.ErrorDescription) {
			nivel = c.log.Info
		}
		nivel("bitrix api error",
			zap.String("method", method),
			zap.Int("status", resp.StatusCode),
			zap.String("error", result.Error),
			zap.String("error_description", result.ErrorDescription),
			zap.String("raw_body", string(rawBody)),
		)
		if result.ErrorDescription != "" {
			return nil, fmt.Errorf("bitrix error: %s — %s", result.Error, result.ErrorDescription)
		}
		return nil, fmt.Errorf("bitrix error: %s", result.Error)
	}
	return result.Result, nil
}

// ─── Im Open Lines (Omnichannel) ──────────────────────────────────────────

func (c *Client) OpenChatSession(ctx context.Context, creds TenantCreds, lineID int, userPhone, userName, userAvatar string) (int64, error) {
	raw, err := c.call(ctx, creds, "imopenlines.session.open", map[string]interface{}{
		"LINE_ID":     lineID,
		"USER_PHONE":  userPhone,
		"USER_NAME":   userName,
		"USER_AVATAR": userAvatar,
		"USER_CODE":   userPhone,
	})
	if err != nil {
		return 0, err
	}
	var result struct {
		SessionID int64 `json:"SESSION_ID"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return 0, err
	}
	return result.SessionID, nil
}

func (c *Client) SendMessage(ctx context.Context, creds TenantCreds, sessionID int64, text string) error {
	_, err := c.call(ctx, creds, "imopenlines.message.add", map[string]interface{}{
		"SESSION_ID": sessionID,
		"MESSAGE":    text,
	})
	return err
}

// uniqueFileName adiciona timestamp ao nome do arquivo para evitar DISK_OBJ_22000.
func uniqueFileName(name string) string {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	ts := time.Now().Format("20060102_150405")
	return fmt.Sprintf("%s_%s%s", base, ts, ext)
}

// UploadToDisk faz upload de um arquivo para o Bitrix24 Disk.
func (c *Client) UploadToDisk(ctx context.Context, creds TenantCreds, fileName string, data []byte) (int64, string, error) {
	storagesRaw, err := c.call(ctx, creds, "disk.storage.getlist", map[string]interface{}{})
	if err != nil {
		return 0, "", fmt.Errorf("disk.storage.getlist: %w", err)
	}

	var storages []struct {
		ID         string `json:"ID"`
		EntityType string `json:"ENTITY_TYPE"`
	}
	if err := json.Unmarshal(storagesRaw, &storages); err != nil || len(storages) == 0 {
		return 0, "", fmt.Errorf("no storage found (raw: %s)", string(storagesRaw))
	}

	storageID := storages[0].ID
	for _, s := range storages {
		if s.EntityType == "common" {
			storageID = s.ID
			break
		}
	}

	uniqueName := uniqueFileName(fileName)
	c.log.Info("uploading to disk storage", zap.String("storage_id", storageID), zap.String("file", uniqueName))

	b64 := base64.StdEncoding.EncodeToString(data)
	raw, err := c.call(ctx, creds, "disk.storage.uploadfile", map[string]interface{}{
		"id":          storageID,
		"data":        map[string]string{"NAME": uniqueName},
		"fileContent": []string{uniqueName, b64},
	})
	c.log.Info("disk.storage.uploadfile raw", zap.String("raw", string(raw)), zap.Error(err))
	if err != nil {
		return 0, "", fmt.Errorf("disk.storage.uploadfile: %w", err)
	}

	var result struct {
		ID          json.RawMessage `json:"ID"`
		DownloadURL string          `json:"DOWNLOAD_URL"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return 0, "", fmt.Errorf("parse upload response: %w", err)
	}

	var fileID int64
	if err := json.Unmarshal(result.ID, &fileID); err != nil {
		var idStr string
		if err2 := json.Unmarshal(result.ID, &idStr); err2 == nil {
			fmt.Sscanf(idStr, "%d", &fileID)
		}
	}
	return fileID, result.DownloadURL, nil
}

// ─── Im Connector (Open Channel) ─────────────────────────────────────────

type ConnectorMessage struct {
	User    ConnectorUser    `json:"user"`
	Message ConnectorMsgBody `json:"message"`
	Chat    ConnectorChat    `json:"chat"`
}

type ConnectorUser struct {
	ID    string `json:"ID"`
	Name  string `json:"NAME"`
	Phone string `json:"PHONE"`
}

type ConnectorMsgBody struct {
	ID    string          `json:"ID"`
	Text  string          `json:"TEXT,omitempty"`
	Files []ConnectorFile `json:"FILES,omitempty"`
}

type ConnectorFile struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

type ConnectorChat struct {
	ID string `json:"ID"`
}

// RegisterConnector registra este app como conector de canal externo no Bitrix24.
// PLACEMENT_HANDLER é a URL que o Bitrix usa para entregar o evento ONIMCONNECTORMESSAGEADD
// quando o operador responde no Contact Center. Deve apontar para o endpoint que processa
// a mensagem, não para a raiz do app.
// Ref: https://apidocs.bitrix24.com/api-reference/imopenlines/imconnector/imconnector-register.html
func (c *Client) RegisterConnector(ctx context.Context, creds TenantCreds, connectorID, name, placementHandlerURL string) error {
	icon := map[string]string{
		"DATA_IMAGE": "data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHZpZXdCb3g9IjAgMCA0OCA0OCI+PGNpcmNsZSBjeD0iMjQiIGN5PSIyNCIgcj0iMjQiIGZpbGw9IiMyNUQzNjYiLz48dGV4dCB4PSIyNCIgeT0iMzIiIGZvbnQtc2l6ZT0iMjQiIGZvbnQtZmFtaWx5PSJBcmlhbCIgZmlsbD0id2hpdGUiIHRleHQtYW5jaG9yPSJtaWRkbGUiPtc8L3RleHQ+PC9zdmc+",
	}
	raw, err := c.call(ctx, creds, "imconnector.register", map[string]interface{}{
		"ID":                connectorID,
		"NAME":              name,
		"ICON":              icon,
		"PLACEMENT_HANDLER": placementHandlerURL,
	})
	c.log.Info("imconnector.register response", zap.String("raw", string(raw)), zap.Error(err))
	return err
}

// SetConnectorData configura os dados do canal externo no Bitrix24.
// DATA aceita apenas: ID, URL, URL_IM, NAME — não existe send_message aqui.
// A entrega de mensagens do operador é feita via event.bind (ONIMCONNECTORMESSAGEADD),
// não via connector.data.set.
func (c *Client) SetConnectorData(ctx context.Context, creds TenantCreds, connectorID string, lineID int, _ string) error {
	raw, err := c.call(ctx, creds, "imconnector.connector.data.set", map[string]interface{}{
		"CONNECTOR": connectorID,
		"LINE":      lineID,
		"DATA": map[string]interface{}{
			"ID":   connectorID,
			"NAME": "WhatsApp UC",
		},
	})
	c.log.Info("imconnector.connector.data.set response", zap.String("raw", string(raw)), zap.Error(err))
	return err
}

// ActivateConnector ativa o conector em uma Open Line específica.
func (c *Client) ActivateConnector(ctx context.Context, creds TenantCreds, connectorID string, lineID int, active bool) error {
	activeVal := "0"
	if active {
		activeVal = "1"
	}
	raw, err := c.call(ctx, creds, "imconnector.activate", map[string]interface{}{
		"CONNECTOR": connectorID,
		"LINE":      lineID,
		"ACTIVE":    activeVal,
	})
	c.log.Info("imconnector.activate response", zap.String("raw", string(raw)), zap.Error(err))
	return err
}

// ConnectorSendMessage entrega uma mensagem de cliente ao Contact Center.
func (c *Client) ConnectorSendMessage(ctx context.Context, creds TenantCreds, connectorID string, lineID int, msg ConnectorMessage) (string, error) {
	t, err := c.token(ctx, creds)
	if err != nil {
		return "", err
	}

	domain := normalizeDomain(creds.Domain)
	params := map[string]interface{}{
		"CONNECTOR": connectorID,
		"LINE":      lineID,
		"MESSAGES":  []ConnectorMessage{msg},
	}
	body, _ := json.Marshal(params)
	reqURL := fmt.Sprintf("%s/rest/imconnector.send.messages.json?auth=%s", domain, t.AccessToken)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	rawBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	c.log.Info("imconnector.send.messages raw response", zap.String("raw", string(rawBytes)))

	var envelope struct {
		Result struct {
			Success bool `json:"SUCCESS"`
			Data    struct {
				Result []struct {
					Success bool `json:"SUCCESS"`
					Session struct {
						ID     string `json:"ID"`
						ChatID string `json:"CHAT_ID"`
					} `json:"session"`
				} `json:"RESULT"`
			} `json:"DATA"`
		} `json:"result"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rawBytes, &envelope); err == nil {
		if envelope.Error != "" {
			return "", fmt.Errorf("bitrix error: %s", envelope.Error)
		}
		for _, r := range envelope.Result.Data.Result {
			if r.Session.ChatID != "" && r.Session.ChatID != "0" {
				return r.Session.ChatID, nil
			}
		}
	}
	return "", nil
}

// ConnectorSetDelivery confirma entrega de mensagem inbound ao Contact Center.
func (c *Client) ConnectorSetDelivery(ctx context.Context, creds TenantCreds, connectorID string, lineID int, messageID string) error {
	raw, err := c.call(ctx, creds, "imconnector.send.status.delivery", map[string]interface{}{
		"CONNECTOR": connectorID,
		"LINE":      fmt.Sprintf("%d", lineID),
		"MESSAGES": []map[string]string{
			{"id": messageID, "status": "delivered"},
		},
	})
	c.log.Info("imconnector.send.status.delivery raw", zap.String("raw", string(raw)), zap.Error(err))
	return err
}

// ConnectorSetOutboundDelivery confirma entrega de mensagem outbound ao operador.
// Ref: https://apidocs.bitrix24.com/api-reference/imopenlines/imconnector/imconnector-send-status-delivery.html
// Campos obrigatórios conforme doc:
//
//	im.chat_id e im.message_id → integers
//	message.id → array de strings (mesmo para mensagem única)
//	message.date → unix timestamp integer
func (c *Client) ConnectorSetOutboundDelivery(ctx context.Context, creds TenantCreds, connectorID string, lineID int, imChatID, imMsgID, waMessageID, chatExtID string) error {
	// Converte chat_id e message_id para int — a API exige integer, não string
	chatIDInt, _ := strconv.Atoi(imChatID)
	msgIDInt, _ := strconv.Atoi(imMsgID)

	payload := map[string]interface{}{
		"CONNECTOR": connectorID,
		"LINE":      lineID,
		"MESSAGES": []map[string]interface{}{
			{
				"im": map[string]interface{}{
					"chat_id":    chatIDInt,
					"message_id": msgIDInt,
				},
				"message": map[string]interface{}{
					"id":   []string{waMessageID}, // doc: array mesmo para mensagem única
					"date": time.Now().Unix(),
				},
				"chat": map[string]interface{}{
					"id": chatExtID,
				},
			},
		},
	}
	c.log.Info("imconnector.send.status.delivery outbound request",
		zap.String("connector", connectorID),
		zap.Int("line", lineID),
		zap.String("im_chat_id", imChatID),
		zap.String("im_msg_id", imMsgID),
		zap.String("wa_message_id", waMessageID),
		zap.String("chat_ext_id", chatExtID),
		zap.Int("im_chat_id_int", chatIDInt),
		zap.Int("im_msg_id_int", msgIDInt),
	)
	raw, err := c.call(ctx, creds, "imconnector.send.status.delivery", payload)
	c.log.Info("imconnector.send.status.delivery outbound response", zap.String("raw", string(raw)), zap.Error(err))
	return err
}

// ConnectorSetOutboundError marca uma mensagem outbound como FALHA no Bitrix.
// O Bitrix24 não tem método nativo de "status error" — só delivery/reading.
// Tentar update.messages com files retorna "Incomplete data" e enviar
// delivery sintético gera falsa confirmação ("viewed by"), que é pior.
//
// Estratégia:
//  1. imconnector.delete.messages — remove a bolha original do operador
//     (o nome do .bak some da conversa, não vira "entregue");
//  2. imconnector.send.messages — injeta uma msg inbound de sistema com
//     o motivo da falha. Operador vê notificação clara, cliente real
//     no WhatsApp não recebe nada.
func (c *Client) ConnectorSetOutboundError(ctx context.Context, creds TenantCreds, connectorID string, lineID int, imChatID, imMsgID, chatExtID, errorMsg string) error {
	chatIDInt, _ := strconv.Atoi(imChatID)
	msgIDInt, _ := strconv.Atoi(imMsgID)

	// 1) Apaga a bolha original do operador.
	deletePayload := map[string]interface{}{
		"CONNECTOR": connectorID,
		"LINE":      lineID,
		"MESSAGES": []map[string]interface{}{
			{
				"im": map[string]interface{}{
					"chat_id":    chatIDInt,
					"message_id": msgIDInt,
				},
				"chat": map[string]interface{}{
					"id": chatExtID,
				},
				"message": map[string]interface{}{
					"id": "failed_" + imMsgID,
				},
			},
		},
	}
	c.log.Info("imconnector.delete.messages (error) request",
		zap.String("connector", connectorID),
		zap.Int("line", lineID),
		zap.String("im_chat_id", imChatID),
		zap.String("im_msg_id", imMsgID),
		zap.String("chat_ext_id", chatExtID),
	)
	rawDel, errDel := c.call(ctx, creds, "imconnector.delete.messages", deletePayload)
	c.log.Info("imconnector.delete.messages (error) response",
		zap.String("raw", string(rawDel)), zap.Error(errDel))

	// 2) Injeta msg de sistema (inbound) na conversa avisando a falha.
	sysMsg := ConnectorMessage{
		User: ConnectorUser{
			ID:   chatExtID,
			Name: "Sistema",
		},
		Message: ConnectorMsgBody{
			ID:   "fail_" + imMsgID + "_" + strconv.FormatInt(time.Now().Unix(), 10),
			Text: "❌ FALHA NO ENVIO: " + errorMsg,
		},
		Chat: ConnectorChat{
			ID: chatExtID,
		},
	}
	if _, err := c.ConnectorSendMessage(ctx, creds, connectorID, lineID, sysMsg); err != nil {
		c.log.Warn("imconnector.send.messages (error notice) failed", zap.Error(err))
		return err
	}
	return errDel
}

// ehErroBenigno marca as respostas de erro do Bitrix que representam
// "estado desejado ja' atingido", e nao uma falha que alguem precise
// investigar. Mantidas em Info pra nao afogar erro de verdade no log.
func ehErroBenigno(code, description string) bool {
	if strings.Contains(description, "Handler already binded") {
		return true
	}
	switch code {
	case "ERROR_ACTIVITY_NOT_FOUND": // delete de robot que nao existe
		return true
	}
	return false
}

// errHandlerJaRegistrado reconhece a recusa do Bitrix quando o mesmo par
// (evento, handler) ja' esta registrado no portal. O Bitrix devolve isso
// como ERROR_CORE com a descricao "Unable to set event handler: Handler
// already binded".
func errHandlerJaRegistrado(err error) bool {
	return err != nil && strings.Contains(err.Error(), "Handler already binded")
}

// BindEvent registra um webhook para um evento do Bitrix24.
//
// "Handler already binded" NAO e' falha: e' o Bitrix dizendo que o estado
// desejado ja' vale. Antes esse caso subia como erro e aparecia no log como
// "bitrix api error — ERROR_CORE" a cada boot/reinstalacao, o que poluia o
// log e, pior, mascarava falha de bind de verdade no meio do ruido.
// Tratamos o bind como idempotente: o que importa e' o handler estar la'.
func (c *Client) BindEvent(ctx context.Context, creds TenantCreds, event, handlerURL string) error {
	raw, err := c.call(ctx, creds, "event.bind", map[string]interface{}{
		"event":   event,
		"handler": handlerURL,
	})
	if errHandlerJaRegistrado(err) {
		c.log.Info("event.bind: handler ja' registrado, nada a fazer",
			zap.String("event", event),
			zap.String("handler", handlerURL),
			zap.String("domain", creds.Domain),
		)
		return nil
	}
	c.log.Info("event.bind response",
		zap.String("event", event),
		zap.String("handler", handlerURL),
		zap.String("domain", creds.Domain),
		zap.String("raw", string(raw)),
		zap.Error(err),
	)
	return err
}

// UnbindEvent remove um webhook de evento do Bitrix24.
// Necessário para limpar bindings antigos com URLs desatualizadas antes de rebind.
func (c *Client) UnbindEvent(ctx context.Context, creds TenantCreds, event, handlerURL string) error {
	params := map[string]interface{}{"event": event}
	if handlerURL != "" {
		params["handler"] = handlerURL
	}
	raw, err := c.call(ctx, creds, "event.unbind", params)
	c.log.Info("event.unbind response",
		zap.String("event", event),
		zap.String("handler", handlerURL),
		zap.String("domain", creds.Domain),
		zap.String("raw", string(raw)),
		zap.Error(err),
	)
	return err
}

// GetOpenLineConfig retorna a configuração de uma Open Line pelo ID.
// Retorna nil se a linha não existir (Bitrix retorna false).
func (c *Client) GetOpenLineConfig(ctx context.Context, creds TenantCreds, lineID int) (json.RawMessage, error) {
	raw, err := c.call(ctx, creds, "imopenlines.config.get", map[string]interface{}{
		"CONFIG_ID": lineID,
	})
	if err != nil {
		return nil, err
	}
	if string(raw) == "false" || string(raw) == "null" {
		return nil, nil
	}
	return raw, nil
}

// ListOpenLines retorna todas as Open Lines do portal de uma vez.
// Usa imopenlines.config.list.get que suporta paginação (limit/offset).
func (c *Client) ListOpenLines(ctx context.Context, creds TenantCreds) (json.RawMessage, error) {
	return c.call(ctx, creds, "imopenlines.config.list.get", map[string]interface{}{
		"PARAMS": map[string]interface{}{
			"select": []string{"ID", "LINE_NAME", "ACTIVE"},
			"order":  map[string]string{"ID": "ASC"},
			"limit":  200,
			"offset": 0,
		},
	})
}

// GetConnectorStatus retorna o status do connector em uma Open Line específica.
// Crítico para diagnosticar por que ONIMCONNECTORMESSAGEADD não dispara — se o
// connector não estiver ATIVO na linha onde o operador responde, o evento nunca sai.
func (c *Client) GetConnectorStatus(ctx context.Context, creds TenantCreds, connectorID string, lineID int) (json.RawMessage, error) {
	return c.call(ctx, creds, "imconnector.status", map[string]interface{}{
		"CONNECTOR": connectorID,
		"LINE":      lineID,
	})
}

// ListEventBindings retorna todos os event handlers registrados para o app no portal.
// Útil para ver se ONIMCONNECTORMESSAGEADD está realmente bindado e qual handler URL.
func (c *Client) ListEventBindings(ctx context.Context, creds TenantCreds) (json.RawMessage, error) {
	return c.call(ctx, creds, "event.get", map[string]interface{}{})
}

// GetConnectorList retorna a lista de connectors registrados no portal.
func (c *Client) GetConnectorList(ctx context.Context, creds TenantCreds) (json.RawMessage, error) {
	return c.call(ctx, creds, "imconnector.list", map[string]interface{}{})
}

// RawCall executa qualquer método REST no Bitrix24 e retorna o resultado bruto.
// Usado pelo endpoint /debug/bitrix-call para diagnóstico e operações manuais.
func (c *Client) RawCall(ctx context.Context, creds TenantCreds, method string, params map[string]interface{}) (json.RawMessage, error) {
	return c.call(ctx, creds, method, params)
}

// RawHTTPGet faz GET em uma URL completa e retorna o body bruto.
// Usado para chamar APIs do Bitrix com token direto na URL (sem passar pelo banco).
func (c *Client) RawHTTPGet(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	// extrai o campo "result" se existir
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal(body, &envelope) == nil && envelope.Result != nil {
		return envelope.Result, nil
	}
	return body, nil
}

// RawHTTPPost faz POST em uma URL completa com params JSON e retorna o body bruto.
func (c *Client) RawHTTPPost(ctx context.Context, url string, params map[string]interface{}) ([]byte, error) {
	body, _ := json.Marshal(params)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// GetConnectorData retorna os dados configurados de um connector em uma linha específica.
// Mostra o campo HANDLER que o Bitrix usa para entregar ONIMCONNECTORMESSAGEADD.
func (c *Client) GetConnectorData(ctx context.Context, creds TenantCreds, connectorID string, lineID int) (json.RawMessage, error) {
	return c.call(ctx, creds, "imconnector.connector.data.get", map[string]interface{}{
		"CONNECTOR": connectorID,
		"LINE":      lineID,
	})
}

// ─── CRM ──────────────────────────────────────────────────────────────────

func (c *Client) FindOrCreateLead(ctx context.Context, creds TenantCreds, phone, name string) (int64, error) {
	raw, err := c.call(ctx, creds, "crm.duplicate.findbycomm", map[string]interface{}{
		"type":   "PHONE",
		"values": []string{phone},
	})
	if err == nil {
		var res struct {
			LEAD []int64 `json:"LEAD"`
		}
		if err := json.Unmarshal(raw, &res); err == nil && len(res.LEAD) > 0 {
			return res.LEAD[0], nil
		}
	}

	raw, err = c.call(ctx, creds, "crm.lead.add", map[string]interface{}{
		"fields": map[string]interface{}{
			"NAME":      name,
			"PHONE":     []map[string]string{{"VALUE": phone, "VALUE_TYPE": "WORK"}},
			"STATUS_ID": "NEW",
			"SOURCE_ID": "WEB",
		},
	})
	if err != nil {
		return 0, err
	}

	var leadID int64
	if err := json.Unmarshal(raw, &leadID); err != nil {
		return 0, err
	}
	return leadID, nil
}

// GetContact retorna dados de um contato do CRM pelo ID.
func (c *Client) GetContact(ctx context.Context, creds TenantCreds, contactID string) (json.RawMessage, error) {
	return c.call(ctx, creds, "crm.contact.get", map[string]interface{}{
		"id": contactID,
	})
}

// GetLead retorna dados de um lead do CRM pelo ID.
func (c *Client) GetLead(ctx context.Context, creds TenantCreds, leadID string) (json.RawMessage, error) {
	return c.call(ctx, creds, "crm.lead.get", map[string]interface{}{
		"id": leadID,
	})
}

// GetDeal retorna dados de um deal do CRM pelo ID.
func (c *Client) GetDeal(ctx context.Context, creds TenantCreds, dealID string) (json.RawMessage, error) {
	return c.call(ctx, creds, "crm.deal.get", map[string]interface{}{
		"id": dealID,
	})
}

// OpenChatSessionByCode abre ou retorna uma sessão de Open Channel usando USER_CODE.
// USER_CODE format: "<connector>|<lineID>|<ext_chat_id>|<ext_user_id>"
// Retorna o CHAT_ID da sessão criada/existente.
func (c *Client) OpenChatSessionByCode(ctx context.Context, creds TenantCreds, userCode string) (string, error) {
	raw, err := c.call(ctx, creds, "imopenlines.session.open", map[string]interface{}{
		"USER_CODE": userCode,
	})
	if err != nil {
		return "", err
	}
	// Resposta pode ser o CHAT_ID direto (int) ou objeto {"CHAT_ID": N}
	var chatID int64
	if json.Unmarshal(raw, &chatID) == nil && chatID > 0 {
		return strconv.FormatInt(chatID, 10), nil
	}
	var obj struct {
		ChatID int64 `json:"CHAT_ID"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.ChatID > 0 {
		return strconv.FormatInt(obj.ChatID, 10), nil
	}
	return "", fmt.Errorf("imopenlines.session.open: unexpected response: %s", string(raw))
}

// GetCRMChats retorna chats do Open Channel vinculados a uma entidade CRM.
// ACTIVE_ONLY=N retorna todos os chats, inclusive encerrados.
func (c *Client) GetCRMChats(ctx context.Context, creds TenantCreds, entityType, entityID string) (json.RawMessage, error) {
	return c.call(ctx, creds, "imopenlines.crm.chat.get", map[string]interface{}{
		"CRM_ENTITY_TYPE": entityType, // "CONTACT", "LEAD", "DEAL"
		"CRM_ENTITY":      entityID,
		"ACTIVE_ONLY":     "N",
	})
}

// SendOperatorMessage envia uma mensagem do OPERADOR no chat do Open Channel.
// Tenta im.message.add primeiro; fallback para imopenlines.crm.message.add.
// Retorna erro se ambos falharem — nunca retorna nil em falha silenciosa.
func (c *Client) SendOperatorMessage(ctx context.Context, creds TenantCreds, chatID, message string) (string, error) {
	raw, err := c.call(ctx, creds, "im.message.add", map[string]interface{}{
		"DIALOG_ID": "chat" + chatID,
		"MESSAGE":   message,
	})
	if err != nil {
		c.log.Warn("im.message.add failed, trying imopenlines.crm.message.add",
			zap.String("chat_id", chatID), zap.Error(err))
		chatIDInt, _ := strconv.ParseInt(chatID, 10, 64)
		raw, err = c.call(ctx, creds, "imopenlines.crm.message.add", map[string]interface{}{
			"CHAT_ID": chatIDInt,
			"MESSAGE": message,
		})
		if err != nil {
			return "", fmt.Errorf("SendOperatorMessage: im.message.add e imopenlines.crm.message.add falharam: %w", err)
		}
	}
	// Resposta pode ser int direto ou {"result": N}
	var msgID int64
	if json.Unmarshal(raw, &msgID) == nil && msgID > 0 {
		return strconv.FormatInt(msgID, 10), nil
	}
	var obj struct {
		Result interface{} `json:"result"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Result != nil {
		return fmt.Sprintf("%v", obj.Result), nil
	}
	// Chegou aqui = API retornou algo mas não é um ID — considera enviado
	c.log.Warn("SendOperatorMessage: resposta inesperada mas sem erro", zap.String("raw", string(raw)))
	return "sent", nil
}

// GetCRMChatLastID retorna apenas o último CHAT_ID vinculado a uma entidade CRM.
func (c *Client) GetCRMChatLastID(ctx context.Context, creds TenantCreds, entityType, entityID string) (string, error) {
	raw, err := c.call(ctx, creds, "imopenlines.crm.chat.getLastId", map[string]interface{}{
		"CRM_ENTITY_TYPE": entityType,
		"CRM_ENTITY":      entityID,
	})
	if err != nil {
		return "", err
	}
	// Resposta: número direto ou {"result": N}
	var id int64
	if json.Unmarshal(raw, &id) == nil && id > 0 {
		return strconv.FormatInt(id, 10), nil
	}
	var obj struct {
		Result int64 `json:"result"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Result > 0 {
		return strconv.FormatInt(obj.Result, 10), nil
	}
	return "", nil
}

// GetSessionHistory retorna o histórico de mensagens de um chat Open Channel.
// Usa imopenlines.session.history.get que é o método correto para Open Channel.
func (c *Client) GetSessionHistory(ctx context.Context, creds TenantCreds, chatID string, limit int) (json.RawMessage, error) {
	if limit <= 0 {
		limit = 50
	}
	id, _ := strconv.ParseInt(chatID, 10, 64)
	return c.call(ctx, creds, "imopenlines.session.history.get", map[string]interface{}{
		"CHAT_ID": id,
		"LIMIT":   limit,
	})
}

// FindChatByPhone busca o CHAT_ID de uma sessão Open Channel pelo número de telefone.
// USER_CODE no Bitrix tem formato: "<connector>|<lineID>|<phone>|<phone>"
// Busca as sessões recentes e filtra pelo telefone no USER_CODE.
func (c *Client) FindChatByPhone(ctx context.Context, creds TenantCreds, phone string) (string, json.RawMessage, error) {
	// Tenta buscar pelo USER_CODE parcial (apenas o número)
	raw, err := c.call(ctx, creds, "imopenlines.session.list", map[string]interface{}{
		"FILTER": map[string]interface{}{
			"=USER_CODE": "%" + phone + "%",
		},
		"ORDER": map[string]string{"DATE_CREATE": "DESC"},
		"LIMIT": 50,
	})
	if err != nil {
		// Fallback: sem filtro, pega as 50 mais recentes
		raw, err = c.call(ctx, creds, "imopenlines.session.list", map[string]interface{}{
			"ORDER": map[string]string{"DATE_CREATE": "DESC"},
			"LIMIT": 50,
		})
		if err != nil {
			return "", nil, err
		}
	}

	chatID := extractSessionChatID(raw, phone)
	return chatID, raw, nil
}

// extractSessionChatID percorre a lista de sessões e retorna o CHAT_ID
// da sessão cujo USER_CODE contém o número de telefone.
func extractSessionChatID(raw json.RawMessage, phone string) string {
	type session struct {
		ID       interface{} `json:"ID"`
		ChatID   interface{} `json:"CHAT_ID"`
		UserCode string      `json:"USER_CODE"`
	}

	// Tenta estrutura {sessions: [...]}
	var wrapped struct {
		Sessions []session `json:"sessions"`
	}
	if json.Unmarshal(raw, &wrapped) == nil && len(wrapped.Sessions) > 0 {
		for _, s := range wrapped.Sessions {
			if strings.Contains(s.UserCode, phone) {
				id := fmt.Sprintf("%v", s.ChatID)
				if id != "0" && id != "" && id != "<nil>" {
					return id
				}
			}
		}
	}

	// Tenta array direto [...]
	var arr []session
	if json.Unmarshal(raw, &arr) == nil {
		for _, s := range arr {
			if strings.Contains(s.UserCode, phone) {
				id := fmt.Sprintf("%v", s.ChatID)
				if id != "0" && id != "" && id != "<nil>" {
					return id
				}
			}
		}
		// Se não achou por telefone mas tem sessões, retorna a mais recente
		if len(arr) > 0 && phone == "" {
			return fmt.Sprintf("%v", arr[0].ChatID)
		}
	}
	return ""
}

// GetRecentChats retorna os chats recentes do Open Channel (im.recent.list).
func (c *Client) GetRecentChats(ctx context.Context, creds TenantCreds, limit int) (json.RawMessage, error) {
	if limit <= 0 {
		limit = 50
	}
	return c.call(ctx, creds, "im.recent.list", map[string]interface{}{
		"LIMIT": limit,
	})
}

// GetChatMessages retorna as mensagens de um chat pelo CHAT_ID.
func (c *Client) GetChatMessages(ctx context.Context, creds TenantCreds, chatID string, limit int) (json.RawMessage, error) {
	if limit <= 0 {
		limit = 50
	}
	return c.call(ctx, creds, "im.dialog.messages.get", map[string]interface{}{
		"DIALOG_ID": "chat" + chatID,
		"LIMIT":     limit,
	})
}

// BindPlacement registra um widget de aba customizada no CRM.
func (c *Client) BindPlacement(ctx context.Context, creds TenantCreds, placement, handlerURL, title string) error {
	_, err := c.call(ctx, creds, "placement.bind", map[string]interface{}{
		"PLACEMENT":   placement,
		"HANDLER":     handlerURL,
		"TITLE":       title,
		"DESCRIPTION": "Enviar mensagem WhatsApp diretamente do CRM",
	})
	return err
}

// ListPlacements retorna a lista de placements registrados no portal pelo
// nosso app. Cada item normalizado pra { "placement": "...", "handler": "..." }.
//
// Bitrix24 tem 2 formatos historicos pra esse endpoint:
//  1. Array de objetos: [{ placement, handler, title, ... }] (antigo)
//  2. Array de strings: ["PLACEMENT_NAME_1", "PLACEMENT_NAME_2", ...] (novo)
//
// Quando vem como array de strings, o Bitrix retorna so' OS NOMES dos
// placements DISPONIVEIS no portal (nao os ja' registrados pelo nosso
// app). Nesse caso, devolvemos array vazio — sem info de handler, o
// dedup nao tem como funcionar; melhor pular e deixar o Bitrix lidar
// com duplicatas (que so' acontecem em LEFT_MENU, que ja' removemos).
func (c *Client) ListPlacements(ctx context.Context, creds TenantCreds) ([]map[string]interface{}, error) {
	raw, err := c.call(ctx, creds, "placement.list", map[string]interface{}{})
	if err != nil {
		return nil, err
	}

	// Tenta formato 1: array de objetos.
	var arrObjects []map[string]interface{}
	if err := json.Unmarshal(raw, &arrObjects); err == nil {
		return arrObjects, nil
	}

	// Tenta formato 2: array de strings — versao nova do Bitrix retorna
	// so' os nomes (catalogo de placements disponiveis). Nao da' pra
	// fazer dedup com isso, mas tambem nao e' erro. Devolve vazio.
	var arrStrings []string
	if err := json.Unmarshal(raw, &arrStrings); err == nil {
		return []map[string]interface{}{}, nil
	}

	// Tenta formato 3: { "result": [...] }.
	var wrapObjects struct {
		Result []map[string]interface{} `json:"result"`
	}
	if err := json.Unmarshal(raw, &wrapObjects); err == nil && wrapObjects.Result != nil {
		return wrapObjects.Result, nil
	}
	var wrapStrings struct {
		Result []string `json:"result"`
	}
	if err := json.Unmarshal(raw, &wrapStrings); err == nil && wrapStrings.Result != nil {
		return []map[string]interface{}{}, nil
	}

	return nil, fmt.Errorf("parse placement.list: formato desconhecido (raw: %s)", string(raw))
}

// UnbindPlacement remove o registro de um placement pelo identificador.
// Aceita o ID inteiro (vem em placement.list como "id") ou o nome do
// placement (ex: "CRM_LEAD_DETAIL_TAB") — Bitrix aceita ambos via
// PLACEMENT + HANDLER ou via ID. Mais robusto: passa ID quando souber.
func (c *Client) UnbindPlacement(ctx context.Context, creds TenantCreds, placementName, handlerURL string) error {
	params := map[string]interface{}{}
	if placementName != "" {
		params["PLACEMENT"] = placementName
	}
	if handlerURL != "" {
		params["HANDLER"] = handlerURL
	}
	_, err := c.call(ctx, creds, "placement.unbind", params)
	return err
}

func (c *Client) AddLeadComment(ctx context.Context, creds TenantCreds, leadID int64, text string) error {
	_, err := c.call(ctx, creds, "crm.activity.add", map[string]interface{}{
		"fields": map[string]interface{}{
			"OWNER_TYPE_ID": 1,
			"OWNER_ID":      leadID,
			"TYPE_ID":       12,
			"SUBJECT":       "Mensagem WhatsApp",
			"DESCRIPTION":   text,
			"COMPLETED":     "Y",
		},
	})
	return err
}

// ─── Users ────────────────────────────────────────────────────────────────

// BitrixUser representa um usuario do portal Bitrix24 (resultado de user.get).
type BitrixUser struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	LastName string `json:"last_name"`
	Email    string `json:"email"`
	Active   bool   `json:"active"`
	IsAdmin  bool   `json:"is_admin"`
	Position string `json:"position"`
	// Extranet: usuario externo (parceiro/cliente convidado). Nao deve aparecer
	// nas permissoes de atendimento — so colaboradores internos.
	Extranet bool `json:"extranet"`
	// Bot: usuarios sinteticos (bots de chat, integracoes). Tambem nao
	// devem aparecer no painel de permissoes.
	Bot bool `json:"bot"`
	// Sinais explicitos de "e' gente de fora", que o im.user.list.get
	// devolve e que nao estavam sendo usados:
	//   Network   — usuario da Rede Bitrix24 (parceiro de outro portal)
	//   Connector — usuario criado por conector (contato externo virando user)
	//   Intranet  — colaborador interno. TRI-ESTADO de proposito: ver
	//               ehInterno(). Nem toda resposta traz o campo, e tratar
	//               ausencia como "false" filtraria TODO MUNDO.
	Network   bool  `json:"network"`
	Connector bool  `json:"connector"`
	Intranet  *bool `json:"intranet_user,omitempty"`
}

// ListAllUsers tenta listar TODOS os usuarios ativos do portal iterando IDs
// de 1 ate maxID (default 500) em chunks de 50 via im.user.list.get.
// Bitrix nao tem metodo que liste todos sem scope `user` — esta eh a
// abordagem mais robusta com scopes que temos (im).
//
// As chamadas sao feitas em paralelo (10 goroutines simultaneas) com pequeno
// rate limit interno. Usuarios inexistentes/inativos sao silenciosamente
// omitidos pelo Bitrix. Retorno: lista deduplicada e ordenada por nome.
// IDsDaFilaDaLinha devolve os user IDs que atendem a Linha Aberta.
//
// POR QUE ISTO E' A FONTE MAIS CONFIAVEL: o metodo de LISTAR usuarios
// (user.get) exige o scope `user`, que este app nao tem — medido no portal
// do cliente: "insufficient_scope". E im.user.list.get exige IDs explicitos,
// o que obrigava a SONDAR faixas de ID. Sondagem nao alcanca ID esparso: o
// portal do teclife tem usuarios em 7..463 e um em 12195, e nenhuma varredura
// por faixas razoaveis chega la'.
//
// A configuracao da Linha Aberta, por outro lado, JA' LISTA a fila:
//
//	QUEUE = ["13","21","463","37","27","12195","7","137"]
//
// Sao exatamente as pessoas que atendem — que e' quem precisa de permissao
// de envio. Sem adivinhar ID nenhum, e com o scope `imopenlines` que o app
// ja' possui.
func (c *Client) IDsDaFilaDaLinha(ctx context.Context, creds TenantCreds, lineID int) ([]string, error) {
	if lineID <= 0 {
		return nil, nil
	}
	raw, err := c.GetOpenLineConfig(ctx, creds, lineID)
	if err != nil || raw == nil {
		return nil, err
	}
	var cfg struct {
		Queue []string `json:"QUEUE"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	return cfg.Queue, nil
}

// listarViaUserGet lista usuarios pelo metodo PROPRIO de listagem do Bitrix,
// com paginacao real — sem precisar adivinhar ID nenhum.
//
// Exige o scope `user`. Quando o app era publicado no Marketplace esse scope
// nao estava disponivel, e por isso a versao antiga sondava IDs. Com a
// instalacao local (a UC Technology instala e controla os scopes) ele passa a
// ser possivel. Se o portal ainda nao concedeu, devolve erro e o caller cai
// no modo de sondagem.
func (c *Client) listarViaUserGet(ctx context.Context, creds TenantCreds) ([]BitrixUser, error) {
	var todos []BitrixUser
	start := 0
	vistos := map[string]bool{}
	// Pagina ate' o Bitrix parar de devolver gente. Sem teto de usuarios: o
	// criterio e' "ativo", nao uma quantidade que alguem chutou. O limite de
	// voltas abaixo existe so' pra nao girar pra sempre se a API repetir
	// pagina — e' guarda contra loop, nao contra portal grande (10 mil
	// voltas = 500 mil usuarios).
	for volta := 0; volta < 10000; volta++ {
		raw, err := c.call(ctx, creds, "user.get", map[string]interface{}{
			"FILTER": map[string]interface{}{"ACTIVE": true},
			"start":  start,
		})
		if err != nil {
			return nil, err
		}
		var linhas []map[string]interface{}
		if err := json.Unmarshal(raw, &linhas); err != nil {
			return nil, err
		}
		if len(linhas) == 0 {
			break
		}
		novos := 0
		for _, r := range linhas {
			id := stringField(r, "ID")
			if id == "" || vistos[id] {
				continue
			}
			vistos[id] = true
			novos++
			tipo := strings.ToLower(stringField(r, "USER_TYPE"))
			interno := tipo == "employee"
			todos = append(todos, BitrixUser{
				Intranet: &interno,
				ID:       stringField(r, "ID"),
				Name:     stringField(r, "NAME"),
				LastName: stringField(r, "LAST_NAME"),
				Email:    stringField(r, "EMAIL"),
				Position: stringField(r, "WORK_POSITION"),
				Active:   boolField(r, "ACTIVE"),
				Extranet: tipo == "extranet",
				Bot:      tipo == "bot",
			})
		}
		// Pagina que nao trouxe ninguem novo = a API esta repetindo. Para,
		// senao o laco giraria ate' o limite de voltas.
		if novos == 0 {
			break
		}
		// Pagina menor que o tamanho padrao significa que acabou.
		if len(linhas) < 50 {
			break
		}
		start += 50
	}
	if len(todos) == 0 {
		return nil, fmt.Errorf("user.get nao devolveu usuarios")
	}
	return todos, nil
}

// ListAllUsers devolve os colaboradores internos ativos do portal.
//
// BUG QUE ISTO CORRIGE: a versao antiga NAO listava usuarios — ela SONDAVA
// os IDs de 1 ate maxID (1000), 50 por chamada, porque im.user.list.get
// exige IDs explicitos. Quem tivesse ID acima do teto simplesmente nunca era
// consultado, e usuario novo de portal antigo cai justamente ai: o ID do
// Bitrix e' sequencial por portal e conta tambem usuarios excluidos,
// externos e bots, entao passa de 1000 rapido. Sintoma: "usuario novo nao
// aparece nas permissoes, mesmo atualizando".
//
// Agora tenta primeiro o metodo de listagem de verdade (user.get, paginado).
// So' cai na sondagem se o portal nao tiver concedido o scope `user` — e
// nesse caso a sondagem passou a ser ADAPTATIVA em vez de parar num teto
// fixo (ver comentario abaixo).
func (c *Client) ListAllUsers(ctx context.Context, creds TenantCreds, maxID int) ([]BitrixUser, error) {
	return c.ListAllUsersDaLinha(ctx, creds, maxID, 0)
}

// ListAllUsersDaLinha e' o ListAllUsers com a fila da Linha Aberta somada.
//
// Une TRES fontes, porque nenhuma sozinha e' completa neste app:
//
//  1. fila da Linha Aberta — completa pros atendentes, e a unica que
//     alcanca ID alto (ex: 12195). Nao precisa de scope extra.
//  2. user.get — completa de verdade, mas exige o scope `user`, que o
//     portal pode nao ter concedido.
//  3. sondagem de IDs — ultimo recurso; nao alcanca ID esparso.
//
// Cada fonte que falhar simplesmente nao contribui. O resultado e' a uniao,
// deduplicada.
func (c *Client) ListAllUsersDaLinha(ctx context.Context, creds TenantCreds, maxID, lineID int) ([]BitrixUser, error) {
	users, _, err := c.ListAllUsersComOrigem(ctx, creds, maxID, lineID)
	return users, err
}

// ListAllUsersComOrigem devolve tambem se a lista e' COMPLETA.
//
// Importa porque, sem o scope `user`, nao existe jeito de enumerar todos os
// usuarios do portal — medido contra o portal do cliente:
//
//	user.get          insufficient_scope
//	user.search       insufficient_scope
//	department.get    insufficient_scope
//	mobile.user.get   funciona, mas devolve sempre os mesmos 10 e ignora
//	                  start/PAGE — nao pagina
//	im.user.list.get  exige IDs explicitos
//
// Sobram a fila da Linha Aberta (completa so' pros atendentes) e a sondagem
// de IDs (nao alcanca ID esparso). O resultado e' necessariamente PARCIAL, e
// a tela precisa dizer isso — senao o operador ve 12 usuarios e conclui que
// o portal tem 12.
func (c *Client) ListAllUsersComOrigem(ctx context.Context, creds TenantCreds, maxID, lineID int) ([]BitrixUser, bool, error) {
	// PRAZO MAXIMO. Sem isto a listagem pode pendurar a tela pra sempre: e'
	// dezenas de chamadas ao Bitrix, atras de um rate limiter de 2 req/s, e
	// se o portal ficar lento (ou devolver QUERY_LIMIT_EXCEEDED, que ainda
	// dispara 3 retries com backoff) o total nao tem teto. Foi o que se viu
	// em producao: a tela de Permissoes ficou no "Carregando..." e o request
	// nao voltava nem depois de 5 minutos.
	//
	// Lista parcial e' melhor que spinner eterno: o operador ve quem deu
	// tempo de carregar e o log avisa que truncou.
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()

	var coletados []BitrixUser

	// Fonte 1: a fila da Linha Aberta. Barata (1 chamada) e e' a unica que
	// acha ID alto sem adivinhar.
	if ids, err := c.IDsDaFilaDaLinha(ctx, creds, lineID); err == nil && len(ids) > 0 {
		if users, uerr := c.GetUserByIDs(ctx, creds, ids); uerr == nil {
			coletados = append(coletados, users...)
			c.log.Info("usuarios da fila da Linha Aberta",
				zap.Int("line", lineID), zap.Int("total", len(users)))
		}
	}

	// Fonte 2: listagem de verdade, se o portal concedeu o scope `user`.
	if users, err := c.listarViaUserGet(ctx, creds); err == nil {
		c.log.Info("ListAllUsers via user.get (lista COMPLETA)", zap.Int("total", len(users)))
		return filtrarInternosAtivos(append(coletados, users...)), true, nil
	} else {
		c.log.Info("user.get indisponivel — sondando IDs (conceda o scope `user` no app pra ficar completo e rapido)",
			zap.Error(err))
	}
	// SONDAGEM EM LOTES GRANDES.
	//
	// im.user.list.get aceita MUITO mais IDs por chamada do que os 50 que
	// este codigo usava. Medido contra o portal do cliente:
	//
	//     1000 ids -> 1,3s     2000 ids -> 1,5s     5000 ids -> 3,2s
	//
	// Com 50 por chamada, cobrir 1..1000 custava 20 chamadas atras de um
	// rate limiter de 2 req/s — mais de 10s pra alcancar apenas o ID 1000.
	// Com 5000 por chamada, a MESMA faixa sai em uma chamada, e da' pra
	// varrer ate' 20 mil em quatro. E' o que finalmente alcanca os
	// funcionarios de ID alto (1587, 12195) sem depender da fila.
	//
	// Para por EVIDENCIA: segue enquanto achar gente, desiste depois de
	// lotes seguidos vazios. Portal com numeracao esparsa (muita exclusao)
	// tem buracos longos, entao um lote vazio sozinho nao basta.
	// 2000 e nao 5000. MEDIDO no portal do cliente: um lote de 5000 IDs na
	// faixa alta leva MAIS de 15s e estoura o timeout do http.Client —
	// "context deadline exceeded". Com 2000 o lote volta com folga.
	const tamanhoLote = 2000
	const lotesVaziosParaDesistir = 3
	// Um lote que falha nao pode encerrar a varredura: quem tem ID acima da
	// falha sumia da lista inteira. Segue pro proximo intervalo e so' desiste
	// depois de varias falhas seguidas, que aí indicam problema real.
	const falhasSeguidasParaDesistir = 3
	const idMaximoAbsoluto = 200000 // guarda contra loop, nao limite de portal

	brutos := coletados
	lotesVazios := 0
	falhasSeguidas := 0
	inicio := 1
	for inicio <= idMaximoAbsoluto {
		if ctx.Err() != nil {
			c.log.Warn("ListAllUsers: prazo esgotado, lista parcial",
				zap.Int("brutos", len(brutos)), zap.Int("ate_id", inicio-1))
			break
		}
		fim := inicio + tamanhoLote - 1
		ids := make([]string, 0, tamanhoLote)
		for id := inicio; id <= fim; id++ {
			ids = append(ids, strconv.Itoa(id))
		}

		users, err := c.GetUserByIDs(ctx, creds, ids)
		if err != nil && ctx.Err() == nil {
			// Uma tentativa a mais antes de dar o lote por perdido: a falha
			// observada era timeout, e timeout costuma nao se repetir.
			users, err = c.GetUserByIDs(ctx, creds, ids)
		}
		if err != nil {
			// BUG QUE ISSO CORRIGE: aqui era "break". Bastava UM lote falhar
			// pra varredura inteira parar, e todo funcionario com ID acima
			// daquele ponto desaparecia do painel de permissoes — sem erro
			// visivel, a lista so' vinha curta. No portal do cliente o lote
			// de 20000+ estourava o timeout e sumiam 6 funcionarios.
			falhasSeguidas++
			c.log.Warn("ListAllUsers: lote falhou, seguindo para o proximo intervalo",
				zap.Int("de", inicio), zap.Int("ate", fim),
				zap.Int("falhas_seguidas", falhasSeguidas), zap.Error(err))
			if falhasSeguidas >= falhasSeguidasParaDesistir {
				c.log.Error("ListAllUsers: falhas seguidas demais, lista pode estar incompleta",
					zap.Int("ate_id", inicio-1))
				break
			}
			inicio = fim + 1
			continue
		}
		falhasSeguidas = 0
		if len(users) == 0 {
			lotesVazios++
			if lotesVazios >= lotesVaziosParaDesistir {
				break
			}
		} else {
			lotesVazios = 0
			brutos = append(brutos, users...)
		}
		inicio = fim + 1
	}
	c.log.Info("ListAllUsers via sondagem em lotes",
		zap.Int("brutos", len(brutos)), zap.Int("ate_id", inicio-1))
	return filtrarInternosAtivos(brutos), false, nil
}

// filtrarInternosAtivos deixa so' quem pode operar atendimento, e ordena por
// nome. Compartilhado pelos dois caminhos (user.get e sondagem) pra que a
// regra de quem aparece no painel nao possa divergir entre eles.
// ehInternoAtivo decide quem pode operar atendimento e portanto aparecer no
// painel de permissoes.
//
// Exclui, em ordem de evidencia:
//   - inativo: demitido/desativado
//   - bot / type != "user": usuario sintetico (chatbot, integracao)
//   - extranet: convidado externo (parceiro, cliente)
//   - network: usuario da Rede Bitrix24, de OUTRO portal
//   - connector: contato externo que virou usuario por um conector
//   - intranet_user == false: o Bitrix afirmando que nao e' do quadro
//
// intranet_user e' TRI-ESTADO de proposito. Nem toda fonte traz o campo, e
// tratar ausencia como "nao e' interno" esvaziaria a lista inteira — falha
// bem pior que deixar passar um externo. So' exclui quando o campo VEM e
// diz false.
func ehInternoAtivo(u BitrixUser) bool {
	if !u.Active || u.Bot || u.Extranet || u.Network || u.Connector {
		return false
	}
	if u.Intranet != nil && !*u.Intranet {
		return false
	}
	return true
}

func filtrarInternosAtivos(brutos []BitrixUser) []BitrixUser {
	seen := map[string]bool{}
	all := make([]BitrixUser, 0, len(brutos))
	for _, u := range brutos {
		if u.ID == "" || seen[u.ID] {
			continue
		}
		if !ehInternoAtivo(u) {
			continue
		}
		seen[u.ID] = true
		all = append(all, u)
	}
	sort.SliceStable(all, func(i, j int) bool {
		ni := strings.ToLower(all[i].Name + " " + all[i].LastName)
		nj := strings.ToLower(all[j].Name + " " + all[j].LastName)
		return ni < nj
	})
	return all
}

// GetUserByIDs busca informacoes de usuarios pelo ID via im.user.list.get
// (scope `im`, que o app ja possui — diferente do user.get que exige scope `user`
// e nao esta disponivel para nosso app Marketplace).
//
// Aceita slice de IDs (strings ou int convertidos para string). Retorna os
// usuarios na mesma ordem. Usuarios nao encontrados sao omitidos do retorno.
func (c *Client) GetUserByIDs(ctx context.Context, creds TenantCreds, userIDs []string) ([]BitrixUser, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}
	// im.user.list.get aceita ID como array de inteiros
	idsInt := make([]int, 0, len(userIDs))
	for _, s := range userIDs {
		var n int
		_, _ = fmt.Sscanf(s, "%d", &n)
		if n > 0 {
			idsInt = append(idsInt, n)
		}
	}
	if len(idsInt) == 0 {
		return nil, nil
	}
	raw, err := c.call(ctx, creds, "im.user.list.get", map[string]interface{}{
		"ID":          idsInt,
		"RESULT_TYPE": "array",
	})
	if err != nil {
		return nil, err
	}
	// Resposta: array de objetos. Campos lowercase: id, name, first_name, last_name,
	// email, work_position, active, etc.
	var rows []map[string]interface{}
	if err := json.Unmarshal(raw, &rows); err != nil {
		// fallback: pode vir como mapa keyed por ID
		var byID map[string]map[string]interface{}
		if err2 := json.Unmarshal(raw, &byID); err2 == nil {
			for _, r := range byID {
				rows = append(rows, r)
			}
		} else {
			return nil, err
		}
	}
	out := make([]BitrixUser, 0, len(rows))
	for _, r := range rows {
		u := BitrixUser{
			ID:       stringField(r, "id"),
			Name:     stringField(r, "first_name"),
			LastName: stringField(r, "last_name"),
			Email:    stringField(r, "email"),
			Position: stringField(r, "work_position"),
		}
		if u.ID == "" {
			u.ID = stringField(r, "ID")
		}
		// active: pode ser bool ou "Y"/"N"
		if v, ok := r["active"]; ok {
			switch x := v.(type) {
			case bool:
				u.Active = x
			case string:
				u.Active = strings.EqualFold(x, "Y") || strings.EqualFold(x, "true")
			}
		} else {
			u.Active = true
		}
		// extranet / bot — vem como bool no im.user.list.get
		u.Extranet = boolField(r, "extranet")
		u.Bot = boolField(r, "bot")
		u.Network = boolField(r, "network")
		u.Connector = boolField(r, "connector")
		// intranet_user so' e' considerado quando VEM na resposta. Ausente
		// nao significa "externo" — significa que aquela fonte nao informa.
		if v, ok := r["intranet_user"]; ok && v != nil {
			b := boolField(r, "intranet_user")
			u.Intranet = &b
		}
		// type: "user" e' pessoa; qualquer outra coisa (bot etc.) nao entra.
		if t := strings.ToLower(stringField(r, "type")); t != "" && t != "user" {
			u.Bot = true
		}
		// O 'name' completo (se o backend ja juntou) pode estar em 'name'
		if u.Name == "" && u.LastName == "" {
			full := stringField(r, "name")
			u.Name = full
		}
		out = append(out, u)
	}
	return out, nil
}

// boolField extrai bool de um campo que pode vir como bool, "Y"/"N" ou
// "true"/"false". Vazio/ausente => false.
func boolField(m map[string]interface{}, key string) bool {
	v, ok := m[key]
	if !ok || v == nil {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return strings.EqualFold(x, "Y") || strings.EqualFold(x, "true") || x == "1"
	case float64:
		return x != 0
	}
	return false
}

func stringField(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok && v != nil {
		return fmt.Sprintf("%v", v)
	}
	return ""
}

// ─── Message Service (Bitrix Marketing > Campanhas SMS) ───────────────────
// Permite registrar nosso app como "provedor SMS" do Bitrix. O cliente vai
// em Marketing > Campanhas SMS, escolhe "UC Talk" como provedor, dispara —
// Bitrix faz POST no nosso webhook por destinatario. Nos entregamos via
// WhatsApp e reportamos status com message.status.update.
//
// Requer scope `messageservice` no manifest do app no Marketplace.

// RegisterSMSSender cadastra o app como provedor SMS no portal.
// Idempotente: chamar de novo so' atualiza o registro existente (Bitrix
// trata como upsert pelo CODE).
//
//	code: identificador unico do sender (ex: "uctalk_whatsapp")
//	name: nome exibido no menu (pode ser localizado via map em vez de string)
//	handlerURL: URL HTTPS publica do nosso endpoint que recebe os envios
func (c *Client) RegisterSMSSender(ctx context.Context, creds TenantCreds, code, name, handlerURL string) error {
	_, err := c.call(ctx, creds, "messageservice.sender.add", map[string]interface{}{
		"CODE":    code,
		"TYPE":    "SMS",
		"HANDLER": handlerURL,
		"NAME":    name,
	})
	return err
}

// DeleteSMSSender remove o sender. Chamado no fluxo de uninstall do app
// pra nao deixar entrada orfa no portal do cliente.
func (c *Client) DeleteSMSSender(ctx context.Context, creds TenantCreds, code string) error {
	_, err := c.call(ctx, creds, "messageservice.sender.delete", map[string]interface{}{
		"CODE": code,
	})
	return err
}

// UpdateSMSMessageStatus reporta ao Bitrix o status final de uma mensagem
// que ele nos pediu pra enviar. Status validos: queued|sent|delivered|
// undelivered|failed. Bitrix mostra na UI da campanha.
func (c *Client) UpdateSMSMessageStatus(ctx context.Context, creds TenantCreds, code, bitrixMessageID, status string) error {
	_, err := c.call(ctx, creds, "messageservice.message.status.update", map[string]interface{}{
		"CODE":       code,
		"MESSAGE_ID": bitrixMessageID,
		"STATUS":     status,
	})
	return err
}

// ─── BizProc (Automacoes do CRM Bitrix24) ─────────────────────────────────
// Permite registrar nosso app como "atividade customizada" no menu
// CRM > Automacoes > Regras de Automacao (e tambem em Modelos de Processo).
// Cliente arrasta a atividade "UC Talk: Enviar WhatsApp" pra um robot/fluxo,
// configura destinatario+modo+mensagem, e quando o robot dispara o Bitrix
// POSTa no nosso handler.

// RegisterBPRobot cadastra a atividade no portal. Wrapper generico — usado
// pelo callsite que monta PROPERTIES customizado. Idempotente.
//
// Ref: apidocs.bitrix24.com/api-reference/bizproc/bizproc-robot/bizproc-robot-add.html
func (c *Client) RegisterBPRobot(ctx context.Context, creds TenantCreds, code, name, handlerURL string, properties map[string]interface{}) error {
	_, err := c.call(ctx, creds, "bizproc.robot.add", map[string]interface{}{
		"CODE":             code,
		"HANDLER":          handlerURL,
		"AUTH_USER_ID":     1,
		"NAME":             map[string]string{"en": name, "pt-BR": name},
		"USE_SUBSCRIPTION": "N",
		"PROPERTIES":       properties,
	})
	return err
}

// DeleteBPRobot remove a atividade do portal. Usado no uninstall.
// Remover um robot que nao existe NAO e' erro: o estado desejado (robot
// ausente) ja' vale. O Bitrix responde ERROR_ACTIVITY_NOT_FOUND nesse caso.
//
// Isso importa porque triggerBPRobotRefresh chama DeleteBPRobot(legacy) em
// TODO refresh, so' pra garantir a limpeza do robot antigo. Na esmagadora
// maioria dos portais esse robot nunca existiu, entao cada refresh gerava um
// "bitrix api error — ERROR_ACTIVITY_NOT_FOUND" no log. Combinado com o
// ciclo de reconexao de 30s (corrigido em outro commit), virava um erro a
// cada 30s por tenant — ruido que escondia problema de verdade.
func (c *Client) DeleteBPRobot(ctx context.Context, creds TenantCreds, code string) error {
	_, err := c.call(ctx, creds, "bizproc.robot.delete", map[string]interface{}{
		"CODE": code,
	})
	if err != nil && strings.Contains(err.Error(), "ERROR_ACTIVITY_NOT_FOUND") {
		return nil
	}
	return err
}

// ListBPRobots retorna os robots registrados pelo app neste portal.
// Util pra debug: confirma se o bizproc.robot.add do install/retry
// realmente persistiu, e com qual payload.
func (c *Client) ListBPRobots(ctx context.Context, creds TenantCreds) (string, error) {
	raw, err := c.call(ctx, creds, "bizproc.robot.list", map[string]interface{}{})
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// ListSMSSenders retorna os senders registrados pelo app neste portal.
// Util pra debug: confirma se o messageservice.sender.add do install
// realmente persistiu. Tambem revela se o app esta sem scope `messageservice`
// (nesse caso retorna ACCESS_DENIED).
func (c *Client) ListSMSSenders(ctx context.Context, creds TenantCreds) (string, error) {
	raw, err := c.call(ctx, creds, "messageservice.sender.list", map[string]interface{}{})
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// IDsDeContatosDoNegocio devolve os contatos vinculados a um negocio.
//
// Usado quando o negocio nao tem CONTACT_ID primario: no Bitrix um negocio
// pode ter varios contatos sem nenhum marcado como principal, e nesse caso
// o campo CONTACT_ID vem 0 — mas ha' contato vinculado.
func (c *Client) IDsDeContatosDoNegocio(ctx context.Context, creds TenantCreds, dealID string) ([]string, error) {
	raw, err := c.call(ctx, creds, "crm.deal.contact.items.get", map[string]interface{}{
		"id": dealID,
	})
	if err != nil {
		return nil, err
	}
	var itens []struct {
		ContactID interface{} `json:"CONTACT_ID"`
	}
	if err := json.Unmarshal(raw, &itens); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(itens))
	for _, it := range itens {
		if it.ContactID == nil {
			continue
		}
		id := fmt.Sprintf("%v", it.ContactID)
		if id != "" && id != "0" {
			out = append(out, id)
		}
	}
	return out, nil
}
