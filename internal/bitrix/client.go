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
func (c *Client) SaveToken(ctx context.Context, domain, accessToken, refreshToken string, expiresIn int) error {
	return c.repo.UpsertBitrixToken(ctx, &db.BitrixToken{
		ID:           uuid.New(),
		Domain:       domain,
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

// SendFile envia um arquivo para o chat.
func (c *Client) SendFile(ctx context.Context, sessionID int64, fileName string, fileData []byte) error {
	_, err := c.call(ctx, "imopenlines.message.add", map[string]interface{}{
		"SESSION_ID": sessionID,
		"FILES": []map[string]interface{}{
			{
				"name":    fileName,
				"content": fileData,
			},
		},
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
