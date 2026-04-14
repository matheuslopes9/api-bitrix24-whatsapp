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
	"strings"
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
}

func NewClient(repo *db.Repository, log *zap.Logger) *Client {
	return &Client{
		repo: repo,
		http: &http.Client{Timeout: 15 * time.Second},
		log:  log,
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
func (c *Client) SaveToken(ctx context.Context, creds TenantCreds, accessToken, refreshToken string, expiresIn int) error {
	domain := normalizeDomain(creds.Domain)
	return c.repo.UpsertBitrixToken(ctx, &db.BitrixToken{
		ID:           uuid.New(),
		Domain:       domain,
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
	// Sempre usa o domain da creds — a resposta do refresh retorna "oauth.bitrix.info"
	// como domain, o que quebraria a busca por token.
	domain := normalizeDomain(creds.Domain)
	return c.repo.UpsertBitrixToken(ctx, &db.BitrixToken{
		ID:           uuid.New(),
		Domain:       domain,
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second),
		Scope:        tr.Scope,
	})
}

// token retorna um token válido, renovando se necessário.
func (c *Client) token(ctx context.Context, creds TenantCreds) (*db.BitrixToken, error) {
	domain := normalizeDomain(creds.Domain)
	t, err := c.repo.GetBitrixToken(ctx, domain)
	if err != nil {
		return nil, fmt.Errorf("get token for %s: %w", domain, err)
	}
	if time.Now().Add(60 * time.Second).After(t.ExpiresAt) {
		if err := c.refreshToken(ctx, creds, t); err != nil {
			return nil, fmt.Errorf("refresh token: %w", err)
		}
		t, err = c.repo.GetBitrixToken(ctx, domain)
		if err != nil {
			return nil, err
		}
	}
	return t, nil
}

// ─── REST Helper ─────────────────────────────────────────────────────────

func (c *Client) call(ctx context.Context, creds TenantCreds, method string, params map[string]interface{}) (json.RawMessage, error) {
	t, err := c.token(ctx, creds)
	if err != nil {
		return nil, err
	}

	domain := normalizeDomain(creds.Domain)
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

	var result struct {
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if result.Error != "" {
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
// handlerURL é a base pública do servidor (ex: https://app.easypanel.host).
// O MESSAGES_HANDLER recebe os eventos ONIMCONNECTORMESSAGEADD (operador → WA).
func (c *Client) RegisterConnector(ctx context.Context, creds TenantCreds, connectorID, name, handlerURL string) error {
	icon := map[string]string{
		"DATA_IMAGE": "data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHZpZXdCb3g9IjAgMCA0OCA0OCI+PGNpcmNsZSBjeD0iMjQiIGN5PSIyNCIgcj0iMjQiIGZpbGw9IiMyNUQzNjYiLz48dGV4dCB4PSIyNCIgeT0iMzIiIGZvbnQtc2l6ZT0iMjQiIGZvbnQtZmFtaWx5PSJBcmlhbCIgZmlsbD0id2hpdGUiIHRleHQtYW5jaG9yPSJtaWRkbGUiPtc8L3RleHQ+PC9zdmc+",
	}
	raw, err := c.call(ctx, creds, "imconnector.register", map[string]interface{}{
		"ID":                connectorID,
		"NAME":              name,
		"ICON":              icon,
		"PLACEMENT_HANDLER": handlerURL,
	})
	c.log.Info("imconnector.register response", zap.String("raw", string(raw)), zap.Error(err))
	return err
}

// SetConnectorData configura o handler de mensagens outbound (operador → WA).
// O Bitrix24 chama SEND_MESSAGE quando o operador envia uma resposta no Contact Center.
func (c *Client) SetConnectorData(ctx context.Context, creds TenantCreds, connectorID string, lineID int, sendMessageURL string) error {
	raw, err := c.call(ctx, creds, "imconnector.connector.data.set", map[string]interface{}{
		"CONNECTOR": connectorID,
		"LINE":      lineID,
		"DATA": map[string]interface{}{
			"SEND_MESSAGE": sendMessageURL,
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
func (c *Client) ConnectorSetOutboundDelivery(ctx context.Context, creds TenantCreds, connectorID string, lineID int, imChatID, imMsgID, waMessageID, chatExtID string) error {
	ts := fmt.Sprintf("%d", time.Now().Unix())
	raw, err := c.call(ctx, creds, "imconnector.send.status.delivery", map[string]interface{}{
		"CONNECTOR": connectorID,
		"LINE":      fmt.Sprintf("%d", lineID),
		"MESSAGES": []map[string]interface{}{
			{
				"im": map[string]string{
					"chat_id":    imChatID,
					"message_id": imMsgID,
				},
				"message": map[string]interface{}{
					"id":   []string{waMessageID},
					"date": ts,
				},
				"chat": map[string]string{
					"id": chatExtID,
				},
			},
		},
	})
	c.log.Info("imconnector.send.status.delivery outbound raw", zap.String("raw", string(raw)), zap.Error(err))
	return err
}

// BindEvent registra um webhook para um evento específico do Bitrix24.
func (c *Client) BindEvent(ctx context.Context, creds TenantCreds, event, handlerURL string) error {
	_, err := c.call(ctx, creds, "event.bind", map[string]interface{}{
		"event":   event,
		"handler": handlerURL,
	})
	return err
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
