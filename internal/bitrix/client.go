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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"go.uber.org/zap"
)

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
}

func NewClient(repo *db.Repository, log *zap.Logger) *Client {
	return &Client{
		repo: repo,
		http: &http.Client{Timeout: 15 * time.Second},
		log:  log,
		rl:   newRateLimiter(),
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

// refreshToken renova o access token usando o refresh token.
// O endpoint OAuth2 do Bitrix24 é sempre oauth.bitrix.info, nunca o domínio da conta.
func (c *Client) refreshToken(ctx context.Context, creds TenantCreds, t *db.BitrixToken) error {
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
	c.log.Info("token refresh response", zap.Int("status", resp.StatusCode), zap.String("body", string(body)))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("token refresh failed: status %d body %s", resp.StatusCode, string(body))
	}
	return c.saveTokenResponse(ctx, creds, bytes.NewReader(body))
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	Domain       string `json:"domain"`
}

func (c *Client) saveTokenResponse(ctx context.Context, creds TenantCreds, r io.Reader) error {
	var tr tokenResponse
	if err := json.NewDecoder(r).Decode(&tr); err != nil {
		return err
	}
	domain := normalizeDomain(creds.Domain)
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
		c.log.Warn("bitrix api error",
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
//   im.chat_id e im.message_id → integers
//   message.id → array de strings (mesmo para mensagem única)
//   message.date → unix timestamp integer
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
//   1) imconnector.delete.messages — remove a bolha original do operador
//      (o nome do .bak some da conversa, não vira "entregue");
//   2) imconnector.send.messages — injeta uma msg inbound de sistema com
//      o motivo da falha. Operador vê notificação clara, cliente real
//      no WhatsApp não recebe nada.
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

// BindEvent registra um webhook para um evento do Bitrix24.
func (c *Client) BindEvent(ctx context.Context, creds TenantCreds, event, handlerURL string) error {
	raw, err := c.call(ctx, creds, "event.bind", map[string]interface{}{
		"event":   event,
		"handler": handlerURL,
	})
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
		"PLACEMENT": placement,
		"HANDLER":   handlerURL,
		"TITLE":     title,
		"DESCRIPTION": "Enviar mensagem WhatsApp diretamente do CRM",
	})
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
}

// ListAllUsers tenta listar TODOS os usuarios ativos do portal iterando IDs
// de 1 ate maxID (default 500) em chunks de 50 via im.user.list.get.
// Bitrix nao tem metodo que liste todos sem scope `user` — esta eh a
// abordagem mais robusta com scopes que temos (im).
//
// As chamadas sao feitas em paralelo (10 goroutines simultaneas) com pequeno
// rate limit interno. Usuarios inexistentes/inativos sao silenciosamente
// omitidos pelo Bitrix. Retorno: lista deduplicada e ordenada por nome.
func (c *Client) ListAllUsers(ctx context.Context, creds TenantCreds, maxID int) ([]BitrixUser, error) {
	if maxID <= 0 {
		maxID = 500
	}
	const chunkSize = 50
	const concurrency = 10

	type chunkResult struct {
		users []BitrixUser
		err   error
	}

	// Monta lista de chunks: [1..50], [51..100], ...
	var chunks [][]string
	for start := 1; start <= maxID; start += chunkSize {
		end := start + chunkSize - 1
		if end > maxID {
			end = maxID
		}
		ids := make([]string, 0, end-start+1)
		for id := start; id <= end; id++ {
			ids = append(ids, fmt.Sprintf("%d", id))
		}
		chunks = append(chunks, ids)
	}

	sem := make(chan struct{}, concurrency)
	results := make(chan chunkResult, len(chunks))
	var wg sync.WaitGroup
	for _, ch := range chunks {
		wg.Add(1)
		sem <- struct{}{}
		go func(ids []string) {
			defer wg.Done()
			defer func() { <-sem }()
			users, err := c.GetUserByIDs(ctx, creds, ids)
			results <- chunkResult{users: users, err: err}
		}(ch)
	}
	wg.Wait()
	close(results)

	seen := map[string]bool{}
	var all []BitrixUser
	for r := range results {
		if r.err != nil {
			c.log.Warn("ListAllUsers chunk failed", zap.Error(r.err))
			continue
		}
		for _, u := range r.users {
			if u.ID == "" || seen[u.ID] {
				continue
			}
			// So colaboradores INTERNOS ATIVOS aparecem no painel de permissoes:
			// - !Active: usuario demitido/desativado, nao deve operar atendimento.
			// - Extranet: convidados externos (parceiros, clientes), nao sao
			//   atendentes — Bitrix mistura na mesma lista mas separa pelo flag.
			// - Bot: usuarios sinteticos (chatbots, integracoes do proprio Bitrix).
			if !u.Active || u.Extranet || u.Bot {
				continue
			}
			seen[u.ID] = true
			all = append(all, u)
		}
	}
	// Ordena por nome
	sort.SliceStable(all, func(i, j int) bool {
		ni := strings.ToLower(all[i].Name + " " + all[i].LastName)
		nj := strings.ToLower(all[j].Name + " " + all[j].LastName)
		return ni < nj
	})
	return all, nil
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
//   code: identificador unico do sender (ex: "uctalk_whatsapp")
//   name: nome exibido no menu (pode ser localizado via map em vez de string)
//   handlerURL: URL HTTPS publica do nosso endpoint que recebe os envios
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
func (c *Client) DeleteBPRobot(ctx context.Context, creds TenantCreds, code string) error {
	_, err := c.call(ctx, creds, "bizproc.robot.delete", map[string]interface{}{
		"CODE": code,
	})
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
