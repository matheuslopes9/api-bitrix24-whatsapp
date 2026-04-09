package bitrix

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/config"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"go.uber.org/zap"
)

// Client encapsula chamadas REST ao Bitrix24 com renovação automática de token.
type Client struct {
	cfg    *config.BitrixConfig
	repo   *db.Repository
	http   *http.Client
	log    *zap.Logger
}

func NewClient(cfg *config.BitrixConfig, repo *db.Repository, log *zap.Logger) *Client {
	return &Client{
		cfg:  cfg,
		repo: repo,
		http: &http.Client{Timeout: 15 * time.Second},
		log:  log,
	}
}

// ─── OAuth2 ───────────────────────────────────────────────────────────────

// AuthURL retorna a URL para iniciar o OAuth2.
func (c *Client) AuthURL(state string) string {
	return fmt.Sprintf("%s/oauth/authorize/?client_id=%s&response_type=code&redirect_uri=%s&state=%s",
		c.cfg.Domain, c.cfg.ClientID, url.QueryEscape(c.cfg.RedirectURI), state)
}

// ExchangeCode troca o código de autorização por tokens.
func (c *Client) ExchangeCode(ctx context.Context, code string) error {
	resp, err := c.http.PostForm(c.cfg.Domain+"/oauth/token/", url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {c.cfg.ClientID},
		"client_secret": {c.cfg.ClientSecret},
		"redirect_uri":  {c.cfg.RedirectURI},
		"code":          {code},
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return c.saveTokenResponse(ctx, resp.Body)
}

// refreshToken renova o access token usando o refresh token.
func (c *Client) refreshToken(ctx context.Context, t *db.BitrixToken) error {
	resp, err := c.http.PostForm(c.cfg.Domain+"/oauth/token/", url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {c.cfg.ClientID},
		"client_secret": {c.cfg.ClientSecret},
		"refresh_token": {t.RefreshToken},
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return c.saveTokenResponse(ctx, resp.Body)
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	Domain       string `json:"domain"`
}

// SaveToken salva um token diretamente (usado pelo app local no installation handler).
// Normaliza o domain para sempre usar o valor da config (evita mismatch https:// vs sem).
func (c *Client) SaveToken(ctx context.Context, domain, accessToken, refreshToken string, expiresIn int) error {
	// Usa sempre o domain da config para garantir consistência na busca
	normalizedDomain := c.cfg.Domain
	return c.repo.UpsertBitrixToken(ctx, &db.BitrixToken{
		ID:           uuid.New(),
		Domain:       normalizedDomain,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(expiresIn) * time.Second),
	})
}

func (c *Client) saveTokenResponse(ctx context.Context, r io.Reader) error {
	var tr tokenResponse
	if err := json.NewDecoder(r).Decode(&tr); err != nil {
		return err
	}

	domain := tr.Domain
	if domain == "" {
		domain = c.cfg.Domain
	}

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
func (c *Client) token(ctx context.Context) (*db.BitrixToken, error) {
	t, err := c.repo.GetBitrixToken(ctx, c.cfg.Domain)
	if err != nil {
		return nil, fmt.Errorf("get token: %w", err)
	}

	if time.Now().Add(60 * time.Second).After(t.ExpiresAt) {
		if err := c.refreshToken(ctx, t); err != nil {
			return nil, fmt.Errorf("refresh token: %w", err)
		}
		t, err = c.repo.GetBitrixToken(ctx, c.cfg.Domain)
		if err != nil {
			return nil, err
		}
	}
	return t, nil
}

// ─── REST Helper ─────────────────────────────────────────────────────────

func (c *Client) call(ctx context.Context, method string, params map[string]interface{}) (json.RawMessage, error) {
	t, err := c.token(ctx)
	if err != nil {
		return nil, err
	}

	body, _ := json.Marshal(params)
	reqURL := fmt.Sprintf("%s/rest/%s.json?auth=%s", c.cfg.Domain, method, t.AccessToken)

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

// OpenChatSession abre ou retorna uma conversa existente no Open Lines.
func (c *Client) OpenChatSession(ctx context.Context, lineID int, userPhone, userName, userAvatar string) (int64, error) {
	raw, err := c.call(ctx, "imopenlines.session.open", map[string]interface{}{
		"LINE_ID":      lineID,
		"USER_PHONE":   userPhone,
		"USER_NAME":    userName,
		"USER_AVATAR":  userAvatar,
		"USER_CODE":    userPhone,
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

// SendMessage envia uma mensagem de um usuário externo para o chat.
func (c *Client) SendMessage(ctx context.Context, sessionID int64, text string) error {
	_, err := c.call(ctx, "imopenlines.message.add", map[string]interface{}{
		"SESSION_ID": sessionID,
		"MESSAGE":    text,
	})
	return err
}

// UploadToDisk faz upload de um arquivo para o disco do Bitrix24 e retorna o ID do arquivo.
// folderID 0 usa a pasta raiz do usuário atual.
func (c *Client) UploadToDisk(ctx context.Context, fileName string, data []byte) (int64, string, error) {
	// Usa disk.folder.uploadfile com conteúdo base64
	raw, err := c.call(ctx, "disk.folder.uploadfile", map[string]interface{}{
		"id":          0, // 0 = pasta raiz (my drive)
		"data":        map[string]string{"NAME": fileName},
		"fileContent": data, // Go JSON encoderá como base64
	})
	if err != nil {
		// Fallback: disk.storage.uploadfile
		storagesRaw, err2 := c.call(ctx, "disk.storage.getlist", map[string]interface{}{})
		if err2 != nil {
			return 0, "", fmt.Errorf("upload disk: %w (storage list: %v)", err, err2)
		}
		var storages []struct {
			ID int64 `json:"ID"`
		}
		if err2 := json.Unmarshal(storagesRaw, &storages); err2 != nil || len(storages) == 0 {
			return 0, "", fmt.Errorf("upload disk: %w", err)
		}
		raw, err = c.call(ctx, "disk.storage.uploadfile", map[string]interface{}{
			"id":          storages[0].ID,
			"data":        map[string]string{"NAME": fileName},
			"fileContent": data,
		})
		if err != nil {
			return 0, "", fmt.Errorf("upload disk storage: %w", err)
		}
	}

	var file struct {
		ID          int64  `json:"ID"`
		DownloadURL string `json:"DOWNLOAD_URL"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return 0, "", fmt.Errorf("parse upload response: %w", err)
	}
	return file.ID, file.DownloadURL, nil
}

// ─── Im Connector (Open Channel) ─────────────────────────────────────────

// ConnectorMessage representa uma mensagem de cliente enviada ao connector.
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
	ID    string                   `json:"ID"`
	Text  string                   `json:"TEXT,omitempty"`
	Files []ConnectorFile          `json:"FILES,omitempty"`
}

type ConnectorFile struct {
	Name string `json:"name"`
	Link string `json:"link,omitempty"` // URL pública ou ID do arquivo no Bitrix disk
}

type ConnectorChat struct {
	ID string `json:"ID"`
}

// RegisterConnector registra este app como conector de canal externo no Bitrix24.
// Deve ser chamado uma vez durante a instalação do app.
func (c *Client) RegisterConnector(ctx context.Context, connectorID, name, handlerURL string) error {
	// Ícone SVG mínimo exigido pelo Bitrix24 (círculo verde com "W")
	icon := map[string]string{
		"DATA_IMAGE": "data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHZpZXdCb3g9IjAgMCA0OCA0OCI+PGNpcmNsZSBjeD0iMjQiIGN5PSIyNCIgcj0iMjQiIGZpbGw9IiMyNUQzNjYiLz48dGV4dCB4PSIyNCIgeT0iMzIiIGZvbnQtc2l6ZT0iMjQiIGZvbnQtZmFtaWx5PSJBcmlhbCIgZmlsbD0id2hpdGUiIHRleHQtYW5jaG9yPSJtaWRkbGUiPtc8L3RleHQ+PC9zdmc+",
	}
	_, err := c.call(ctx, "imconnector.register", map[string]interface{}{
		"ID":                connectorID,
		"NAME":              name,
		"ICON":              icon,
		"PLACEMENT_HANDLER": handlerURL,
	})
	return err
}

// ActivateConnector ativa o conector em uma Open Line específica.
func (c *Client) ActivateConnector(ctx context.Context, connectorID string, lineID int, active bool) error {
	activeVal := "0"
	if active {
		activeVal = "1"
	}
	_, err := c.call(ctx, "imconnector.activate", map[string]interface{}{
		"CONNECTOR": connectorID,
		"LINE":      lineID,
		"ACTIVE":    activeVal,
	})
	return err
}

// ConnectorSendMessage entrega uma mensagem de cliente ao Contact Center.
// Retorna o chat_id criado/existente no Bitrix24.
func (c *Client) ConnectorSendMessage(ctx context.Context, connectorID string, lineID int, msg ConnectorMessage) (string, error) {
	// Usa callRaw para obter a resposta completa (não só o campo "result")
	t, err := c.token(ctx)
	if err != nil {
		return "", err
	}

	params := map[string]interface{}{
		"CONNECTOR": connectorID,
		"LINE":      lineID,
		"MESSAGES":  []ConnectorMessage{msg},
	}
	body, _ := json.Marshal(params)
	reqURL := fmt.Sprintf("%s/rest/imconnector.send.messages.json?auth=%s", c.cfg.Domain, t.AccessToken)

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

	// Formato real: {"result":{"SUCCESS":true,"DATA":{"RESULT":[{"session":{"CHAT_ID":"6026",...}},...]}}}
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

// ConnectorSetDelivery confirma entrega de mensagem do operador ao canal externo.
func (c *Client) ConnectorSetDelivery(ctx context.Context, connectorID string, lineID int, messageID string) error {
	_, err := c.call(ctx, "imconnector.send.status.delivery", map[string]interface{}{
		"CONNECTOR":  connectorID,
		"LINE":       lineID,
		"MESSAGE_ID": messageID,
	})
	return err
}

// BindEvent registra um webhook para um evento específico do Bitrix24.
func (c *Client) BindEvent(ctx context.Context, event, handlerURL string) error {
	_, err := c.call(ctx, "event.bind", map[string]interface{}{
		"event":   event,
		"handler": handlerURL,
	})
	return err
}

// ─── CRM ──────────────────────────────────────────────────────────────────

// FindOrCreateLead procura um lead pelo telefone ou cria um novo.
func (c *Client) FindOrCreateLead(ctx context.Context, phone, name string) (int64, error) {
	// Busca por telefone
	raw, err := c.call(ctx, "crm.duplicate.findbycomm", map[string]interface{}{
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

	// Cria novo Lead
	raw, err = c.call(ctx, "crm.lead.add", map[string]interface{}{
		"fields": map[string]interface{}{
			"NAME":   name,
			"PHONE":  []map[string]string{{"VALUE": phone, "VALUE_TYPE": "WORK"}},
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

// AddLeadComment adiciona uma nota de texto ao Lead.
func (c *Client) AddLeadComment(ctx context.Context, leadID int64, text string) error {
	_, err := c.call(ctx, "crm.activity.add", map[string]interface{}{
		"fields": map[string]interface{}{
			"OWNER_TYPE_ID": 1,  // Lead
			"OWNER_ID":      leadID,
			"TYPE_ID":       12, // Nota
			"SUBJECT":       "Mensagem WhatsApp",
			"DESCRIPTION":   text,
			"COMPLETED":     "Y",
		},
	})
	return err
}
