package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/bitrix"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/config"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/queue"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/telemetry"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/whatsapp"
	"go.uber.org/zap"
)

func generateUUID() uuid.UUID { return uuid.New() }

type handlers struct {
	cfg          *config.Config
	repo         *db.Repository
	waManager    *whatsapp.Manager
	bitrixClient *bitrix.Client
	q            *queue.Queue
	metrics      *telemetry.Metrics
	log          *zap.Logger
}

func newHandlers(
	cfg *config.Config,
	repo *db.Repository,
	waManager *whatsapp.Manager,
	bitrixClient *bitrix.Client,
	q *queue.Queue,
	metrics *telemetry.Metrics,
	log *zap.Logger,
) *handlers {
	return &handlers{cfg: cfg, repo: repo, waManager: waManager, bitrixClient: bitrixClient, q: q, metrics: metrics, log: log}
}

// GET /health
func (h *handlers) health(c *fiber.Ctx) error {
	sessions := h.waManager.ListSessions()
	in, out, dead := h.q.Lengths(c.Context())
	return c.JSON(fiber.Map{
		"status":           "ok",
		"active_sessions":  len(sessions),
		"queue_inbound":    in,
		"queue_outbound":   out,
		"queue_dead":       dead,
	})
}

// POST /wa/sessions
// Body: { "phone": "5511999999999" }
// Retorna imediatamente. QR disponível em GET /wa/sessions/:phone/qr
func (h *handlers) addSession(c *fiber.Ctx) error {
	var body struct {
		Phone string `json:"phone"`
	}
	if err := c.BodyParser(&body); err != nil || body.Phone == "" {
		return fiber.NewError(fiber.StatusBadRequest, "phone required")
	}

	if err := h.waManager.AddSession(c.Context(), body.Phone); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}

	return c.JSON(fiber.Map{
		"status": "connecting",
		"phone":  body.Phone,
		"qr_url": "/wa/sessions/" + body.Phone + "/qr",
	})
}

// GET /wa/sessions/:phone/qr — retorna o QR code atual (polling)
func (h *handlers) getSessionQR(c *fiber.Ctx) error {
	phone := c.Params("phone")
	qr := h.waManager.GetQR(phone)
	if qr == "" {
		return c.JSON(fiber.Map{"status": "waiting", "qr": ""})
	}
	return c.JSON(fiber.Map{"status": "ready", "qr": qr})
}

// GET /wa/sessions
func (h *handlers) listSessions(c *fiber.Ctx) error {
	jids := h.waManager.ListSessions()
	return c.JSON(fiber.Map{"sessions": jids, "count": len(jids)})
}

// DELETE /wa/sessions/:jid
func (h *handlers) removeSession(c *fiber.Ctx) error {
	jid := c.Params("jid")
	h.waManager.Disconnect(jid)
	return c.JSON(fiber.Map{"status": "disconnected", "jid": jid})
}

// POST /wa/send
// Body: { "session_jid": "...", "to": "5511999999999@s.whatsapp.net", "text": "Olá!" }
func (h *handlers) sendMessage(c *fiber.Ctx) error {
	var body struct {
		SessionJID string `json:"session_jid"`
		To         string `json:"to"`
		Text       string `json:"text"`
	}
	if err := c.BodyParser(&body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	if body.SessionJID == "" || body.To == "" || body.Text == "" {
		return fiber.NewError(fiber.StatusBadRequest, "session_jid, to, text required")
	}

	// Coloca na fila de saída (não bloqueia a request)
	if err := h.q.PushOutbound(c.Context(), &queue.OutboundJob{
		SessionJID: body.SessionJID,
		ToJID:      body.To,
		Text:       body.Text,
	}); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}

	return c.JSON(fiber.Map{"status": "queued"})
}

// GET|POST /bitrix/callback?session_jid=<jid> — handler de instalação do app local Bitrix24
// O Bitrix24 chama este endpoint com event=ONAPPINSTALL e auth[access_token].
// O session_jid identifica qual conta BitrixAccount (já criada via UI) recebe o token.
//
// Aceita também ONIMCONNECTORMESSAGEADD pelo mesmo caminho: o tutorial oficial do
// Bitrix24 usa o mesmo handler URL para o slider de instalação e para os eventos
// — ambos chegam aqui e são distinguidos pelo campo "event" do payload.
func (h *handlers) bitrixOAuthCallback(c *fiber.Ctx) error {
	h.log.Info("bitrix callback received",
		zap.String("method", c.Method()),
		zap.String("raw_body", string(c.Body())),
		zap.String("content_type", c.Get("Content-Type")),
	)

	// session_jid vem como query param na URL de instalação configurada no Bitrix
	sessionJID := c.Query("session_jid")

	// App local envia form-encoded com auth[access_token]
	event := c.FormValue("event")

	// Roteia ONIMCONNECTORMESSAGEADD direto pro handler de connector — o operador
	// respondeu no Open Channel e a mensagem precisa ir pro WhatsApp.
	if event == "ONIMCONNECTORMESSAGEADD" {
		return h.bitrixConnectorEvent(c)
	}
	accessToken := c.FormValue("auth[access_token]")
	refreshToken := c.FormValue("auth[refresh_token]")
	domain := c.FormValue("auth[domain]")
	expiresIn := c.FormValue("auth[expires_in]")

	// ── Detecta fluxo Partner App (Marketplace) ──────────────────────────────
	// Envia: AUTH_ID, REFRESH_ID, AUTH_EXPIRES, member_id — sem domain.
	// Não tem session_jid. Salva em bitrix_portals pelo member_id e retorna 200.
	// O domain real chega depois via BX24.getAuth() no /bitrix/auth.
	isPartnerApp := c.FormValue("AUTH_ID") != "" || c.FormValue("member_id") != ""
	if isPartnerApp && sessionJID == "" {
		partnerToken := c.FormValue("AUTH_ID")
		partnerRefresh := c.FormValue("REFRESH_ID")
		partnerExpires := c.FormValue("AUTH_EXPIRES")
		partnerMemberID := c.FormValue("member_id")

		exp := 3600
		if partnerExpires != "" {
			fmt.Sscanf(partnerExpires, "%d", &exp)
		}

		h.log.Info("partner app install via /bitrix/callback",
			zap.String("member_id", partnerMemberID),
			zap.String("token_prefix", func() string {
				if len(partnerToken) > 8 { return partnerToken[:8] + "..." }
				return partnerToken
			}()),
		)

		// Salva em bitrix_portals com member_id como identificador.
		// Domain será "" por ora — preenchido em /bitrix/auth via BX24.js.
		portal := &db.BitrixPortal{
			ID:           generateUUID(),
			Domain:       partnerMemberID, // placeholder até BX24.js enviar o domain real
			AccessToken:  partnerToken,
			RefreshToken: partnerRefresh,
			ExpiresAt:    time.Now().Add(time.Duration(sanitizeExpiresIn(exp)) * time.Second),
			MemberID:     partnerMemberID,
			ConnectorID:  "whatsapp_uc",
			OpenLineID:   0,
		}
		if err := h.repo.UpsertBitrixPortal(c.Context(), portal); err != nil {
			h.log.Warn("partner install via callback: upsert portal failed", zap.Error(err))
			// Não retorna erro — não pode bloquear o install do Bitrix
		}
		// Redireciona para /bitrix-connect — o Bitrix exibe essa página ao cliente após o install
		return c.Redirect(h.cfg.App.BaseURL()+"/bitrix-connect", fiber.StatusFound)
	}

	// Fallback: tenta JSON
	if accessToken == "" {
		var body struct {
			Event string `json:"event"`
			Auth  struct {
				AccessToken  string `json:"access_token"`
				RefreshToken string `json:"refresh_token"`
				Domain       string `json:"domain"`
				ExpiresIn    int    `json:"expires_in"`
			} `json:"auth"`
		}
		if err := c.BodyParser(&body); err == nil {
			event = body.Event
			accessToken = body.Auth.AccessToken
			refreshToken = body.Auth.RefreshToken
			domain = body.Auth.Domain
		}
	}

	preview := accessToken
	if len(preview) > 10 {
		preview = preview[:10]
	}
	h.log.Info("bitrix install event",
		zap.String("event", event),
		zap.String("domain", domain),
		zap.String("session_jid", sessionJID),
		zap.String("token_preview", preview))

	if accessToken == "" {
		return fiber.NewError(fiber.StatusBadRequest, "access_token missing")
	}

	exp := 3600
	if expiresIn != "" {
		fmt.Sscanf(expiresIn, "%d", &exp)
	}

	// Se temos session_jid, busca a conta cadastrada na UI e salva token/ativa.
	// O JID na URL pode ter device suffix desatualizado (ex: :19 quando a sessão atual é :39).
	// Tenta primeiro pelo JID exato; se não achar, tenta pela sessão ativa com mesmo número.
	if sessionJID != "" {
		acct, err := h.repo.GetBitrixAccountByJID(c.Context(), sessionJID)
		if err != nil {
			// Fallback: procura sessão ativa cujo número bate com o prefixo do JID da URL
			phone := sessionJID
			if idx := strings.Index(phone, ":"); idx != -1 {
				phone = phone[:idx]
			}
			if idx := strings.Index(phone, "@"); idx != -1 {
				phone = phone[:idx]
			}
			for _, activeJID := range h.waManager.ListSessions() {
				if strings.HasPrefix(activeJID, phone) {
					acct, err = h.repo.GetBitrixAccountByJID(c.Context(), activeJID)
					if err == nil {
						h.log.Info("bitrix callback: matched account by phone prefix",
							zap.String("url_jid", sessionJID), zap.String("active_jid", activeJID))
						break
					}
				}
			}
		}
		if err != nil {
			h.log.Warn("bitrix callback: account not found by jid, will save token by domain",
				zap.String("session_jid", sessionJID), zap.Error(err))
			// Cai no bloco abaixo para salvar pelo domain
		} else {
			// Achou o account — salva token e ativa
			creds := bitrix.TenantCreds{
				Domain:       acct.Domain,
				ClientID:     acct.ClientID,
				ClientSecret: acct.ClientSecret,
				RedirectURI:  acct.RedirectURI,
			}
			if err := h.bitrixClient.SaveToken(c.Context(), creds, accessToken, refreshToken, exp); err != nil {
				return fiber.NewError(fiber.StatusInternalServerError, err.Error())
			}
			_ = h.repo.UpdateBitrixAccountStatus(c.Context(), acct.SessionJID, db.BitrixAccountActive)
			appBase := strings.TrimSuffix(acct.RedirectURI, "/bitrix/callback")
			eventURL := appBase + "/bitrix/connector/event"
			go h.activateConnectorForAccount(acct, creds, appBase, eventURL)
			return c.SendStatus(fiber.StatusOK)
		}
	}

	// Salva token pelo domain — funciona mesmo quando o session_jid está desatualizado.
	// O domain é a chave estável: nunca muda, independente do device suffix do JID.
	// Isso garante que o token do app local (INSTALLED:true) sempre fica disponível.
	if domain == "" {
		domain = h.cfg.Bitrix.Domain
	}
	appBase := h.cfg.App.BaseURL()
	eventURL := appBase + "/bitrix/connector/event"

	// Determina as credenciais: se existe um account para esse domain, usa as credenciais dele;
	// caso contrário, usa as credenciais globais da config (app local único).
	callbackCreds := bitrix.TenantCreds{
		Domain:       "https://" + strings.TrimPrefix(strings.TrimPrefix(domain, "https://"), "http://"),
		ClientID:     h.cfg.Bitrix.ClientID,
		ClientSecret: h.cfg.Bitrix.ClientSecret,
		RedirectURI:  h.cfg.Bitrix.RedirectURI,
	}

	// Tenta encontrar um account para esse domain para usar suas credenciais específicas
	normalDomain := normalizePortalParam(domain)
	for _, activeJID := range h.waManager.ListSessions() {
		if a, err := h.repo.GetBitrixAccountByJID(c.Context(), activeJID); err == nil {
			acctDomain := normalizePortalParam(strings.TrimPrefix(a.Domain, "https://"))
			if acctDomain == normalDomain {
				callbackCreds = bitrix.TenantCreds{
					Domain:       a.Domain,
					ClientID:     a.ClientID,
					ClientSecret: a.ClientSecret,
					RedirectURI:  a.RedirectURI,
				}
				break
			}
		}
	}

	if err := h.bitrixClient.SaveToken(c.Context(), callbackCreds, accessToken, refreshToken, exp); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	h.log.Info("bitrix callback: token saved by domain", zap.String("domain", domain))

	go func() {
		ctx := context.Background()
		// Faz unbind de qualquer binding antigo e rebind com token fresco do app local
		if existing, err := h.bitrixClient.ListEventBindings(ctx, callbackCreds); err == nil {
			var bindings []struct {
				Event   string `json:"event"`
				Handler string `json:"handler"`
			}
			if json.Unmarshal(existing, &bindings) == nil {
				for _, b := range bindings {
					if b.Event == "ONIMCONNECTORMESSAGEADD" {
						_ = h.bitrixClient.UnbindEvent(ctx, callbackCreds, b.Event, b.Handler)
					}
				}
			}
		}
		connectorID := "whatsapp_uc"
		if err := h.bitrixClient.RegisterConnector(ctx, callbackCreds, connectorID, "WhatsApp UC", appBase+"/bitrix-connect"); err != nil {
			h.log.Warn("callback: imconnector.register failed", zap.Error(err))
		}
		lineID := h.cfg.Bitrix.OpenLineID
		if lineID <= 0 {
			lineID = 1
		}
		if err := h.bitrixClient.SetConnectorData(ctx, callbackCreds, connectorID, lineID, eventURL); err != nil {
			h.log.Warn("callback: connector.data.set failed", zap.Error(err))
		}
		if err := h.bitrixClient.ActivateConnector(ctx, callbackCreds, connectorID, lineID, true); err != nil {
			h.log.Warn("callback: imconnector.activate failed", zap.Error(err))
		}
		h.log.Info("callback: connector activated via domain fallback", zap.String("domain", domain))
	}()

	return c.SendStatus(fiber.StatusOK)
}

// activateConnectorForAccount faz register+activate+bind em background para um account específico.
func (h *handlers) activateConnectorForAccount(acct *db.BitrixAccount, creds bitrix.TenantCreds, appBase, eventURL string) {
	ctx := context.Background()
	if existing, err := h.bitrixClient.ListEventBindings(ctx, creds); err == nil {
		var bindings []struct {
			Event   string `json:"event"`
			Handler string `json:"handler"`
		}
		if json.Unmarshal(existing, &bindings) == nil {
			for _, b := range bindings {
				if b.Event == "ONIMCONNECTORMESSAGEADD" {
					_ = h.bitrixClient.UnbindEvent(ctx, creds, b.Event, b.Handler)
				}
			}
		}
	}
	if err := h.bitrixClient.RegisterConnector(ctx, creds, acct.ConnectorID, "WhatsApp UC", appBase+"/bitrix-connect"); err != nil {
		h.log.Warn("activateConnectorForAccount: register failed", zap.Error(err))
	}
	if err := h.bitrixClient.SetConnectorData(ctx, creds, acct.ConnectorID, acct.OpenLineID, eventURL); err != nil {
		h.log.Warn("activateConnectorForAccount: set connector data failed", zap.Error(err))
	}
	if err := h.bitrixClient.ActivateConnector(ctx, creds, acct.ConnectorID, acct.OpenLineID, true); err != nil {
		h.log.Warn("activateConnectorForAccount: activate failed", zap.Error(err))
	}
	if err := h.bitrixClient.BindEvent(ctx, creds, "ONIMCONNECTORMESSAGEADD", eventURL); err != nil {
		h.log.Warn("activateConnectorForAccount: event.bind failed", zap.Error(err))
	}
	h.log.Info("activateConnectorForAccount: done", zap.String("domain", acct.Domain), zap.String("jid", acct.SessionJID))
}

// GET /bitrix/oauth/start
func (h *handlers) bitrixOAuthStart(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"info": "App local Bitrix24 — autorização via installation handler em POST /bitrix/callback?session_jid=<jid>",
	})
}

// ─── Bitrix Accounts (multi-tenant) ──────────────────────────────────────────

// POST /ui/bitrix/accounts
// Body: { session_jid, domain, client_id, client_secret, open_line_id, connector_id }
func (h *handlers) uiCreateBitrixAccount(c *fiber.Ctx) error {
	var body struct {
		SessionJID   string `json:"session_jid"`
		Domain       string `json:"domain"`
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		OpenLineID   int    `json:"open_line_id"`
		ConnectorID  string `json:"connector_id"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "json inválido"})
	}
	if body.SessionJID == "" || body.Domain == "" || body.ClientID == "" || body.ClientSecret == "" {
		return c.Status(400).JSON(fiber.Map{"error": "session_jid, domain, client_id e client_secret são obrigatórios"})
	}
	if body.OpenLineID == 0 {
		body.OpenLineID = 1
	}
	if body.ConnectorID == "" {
		body.ConnectorID = "whatsapp_uc"
	}

	// Gera redirect_uri automaticamente baseado no host da request
	scheme := "https"
	host := c.Hostname()
	redirectURI := fmt.Sprintf("%s://%s/bitrix/callback?session_jid=%s", scheme, host, body.SessionJID)

	acct := &db.BitrixAccount{
		ID:           generateUUID(),
		SessionJID:   body.SessionJID,
		Domain:       body.Domain,
		ClientID:     body.ClientID,
		ClientSecret: body.ClientSecret,
		OpenLineID:   body.OpenLineID,
		ConnectorID:  body.ConnectorID,
		RedirectURI:  redirectURI,
		Status:       db.BitrixAccountPending,
	}

	if err := h.repo.UpsertBitrixAccount(c.Context(), acct); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(fiber.Map{
		"status":       "created",
		"redirect_uri": redirectURI,
		"install_url":  fmt.Sprintf("https://%s/bitrix/callback?session_jid=%s", host, body.SessionJID),
		"account":      acct,
	})
}

// GET /ui/bitrix/accounts
func (h *handlers) uiListBitrixAccounts(c *fiber.Ctx) error {
	accounts, err := h.repo.ListBitrixAccounts(c.Context())
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	// Oculta client_secret na listagem
	type safeAccount struct {
		ID          interface{} `json:"id"`
		SessionJID  string      `json:"session_jid"`
		Domain      string      `json:"domain"`
		ClientID    string      `json:"client_id"`
		OpenLineID  int         `json:"open_line_id"`
		ConnectorID string      `json:"connector_id"`
		RedirectURI string      `json:"redirect_uri"`
		Status      string      `json:"status"`
	}
	var safe []safeAccount
	for _, a := range accounts {
		safe = append(safe, safeAccount{
			ID: a.ID, SessionJID: a.SessionJID, Domain: a.Domain,
			ClientID: a.ClientID, OpenLineID: a.OpenLineID,
			ConnectorID: a.ConnectorID, RedirectURI: a.RedirectURI,
			Status: string(a.Status),
		})
	}
	return c.JSON(fiber.Map{"accounts": safe})
}

// DELETE /ui/bitrix/accounts?jid=<session_jid>
func (h *handlers) uiDeleteBitrixAccount(c *fiber.Ctx) error {
	jid := c.Query("jid")
	if jid == "" {
		return c.Status(400).JSON(fiber.Map{"error": "jid required"})
	}
	if err := h.repo.DeleteBitrixAccount(c.Context(), jid); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"status": "deleted", "jid": jid})
}


// GET /ui/bitrix/lines?domain=empresa.bitrix24.com — retorna todas as Open Lines do portal
// Usa imopenlines.config.list.get — uma única chamada REST, sem varredura.
func (h *handlers) uiListOpenLines(c *fiber.Ctx) error {
	domain := normalizePortalParam(c.Query("domain"))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain é obrigatório"})
	}
	portal, err := h.repo.GetBitrixPortalByDomain(c.Context(), domain)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "portal não encontrado: " + domain})
	}
	creds := h.portalToCreds(portal)

	raw, err := h.bitrixClient.ListOpenLines(c.Context(), creds)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	var items []struct {
		ID     interface{} `json:"ID"`
		Name   string      `json:"LINE_NAME"`
		Active string      `json:"ACTIVE"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "parse error: " + err.Error(), "raw": string(raw)})
	}

	type OpenLine struct {
		ID          int    `json:"id"`
		Name        string `json:"name"`
		Active      bool   `json:"active"`
		ConnectorOK bool   `json:"connector_ok"`
	}

	var lines []OpenLine
	for _, item := range items {
		id := 0
		switch v := item.ID.(type) {
		case float64:
			id = int(v)
		case string:
			fmt.Sscanf(v, "%d", &id)
		}
		if id <= 0 {
			continue
		}
		connectorOK := false
		if statusRaw, err := h.bitrixClient.GetConnectorStatus(c.Context(), creds, portal.ConnectorID, id); err == nil && statusRaw != nil {
			var st struct {
				Status     bool `json:"STATUS"`
				Configured bool `json:"CONFIGURED"`
			}
			if json.Unmarshal(statusRaw, &st) == nil {
				connectorOK = st.Status && st.Configured
			}
		}
		lines = append(lines, OpenLine{
			ID: id, Name: item.Name, Active: item.Active == "Y", ConnectorOK: connectorOK,
		})
	}

	return c.JSON(fiber.Map{"domain": domain, "lines": lines, "total": len(lines)})
}

// GET /ui/bitrix/queues — lista portais instalados via Marketplace com info de sessões vinculadas
// ?portal=empresa.bitrix24.com.br → filtra apenas aquele portal (modo cliente)
func (h *handlers) uiListBitrixQueues(c *fiber.Ctx) error {
	portalFilter := normalizePortalParam(c.Query("portal"))

	portals, err := h.repo.ListBitrixPortals(c.Context())
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	// Se vier filtro de portal, mostra apenas o portal do cliente
	if portalFilter != "" {
		var filtered []*db.BitrixPortal
		for _, p := range portals {
			if strings.EqualFold(p.Domain, portalFilter) {
				filtered = append(filtered, p)
				break
			}
		}
		portals = filtered
	}

	activeSessions := h.waManager.ListSessions()

	type portalResp struct {
		ID          interface{} `json:"id"`
		Domain      string      `json:"domain"`
		MemberID    string      `json:"member_id"`
		OpenLineID  int         `json:"open_line_id"`
		ConnectorID string      `json:"connector_id"`
		InstalledAt interface{} `json:"installed_at"`
		Sessions    []string    `json:"linked_sessions"`
	}

	result := make([]portalResp, 0, len(portals))
	for _, p := range portals {
		// Encontra sessões vinculadas a este portal (via bitrix_accounts)
		linked := []string{}
		for _, jid := range activeSessions {
			acct, err := h.repo.GetBitrixAccountByJID(c.Context(), jid)
			if err != nil {
				continue
			}
			// Compara domain normalizado
			acctDomain := strings.TrimPrefix(acct.Domain, "https://")
			acctDomain = strings.TrimPrefix(acctDomain, "http://")
			if strings.EqualFold(acctDomain, p.Domain) {
				linked = append(linked, jid)
			}
		}
		result = append(result, portalResp{
			ID:          p.ID,
			Domain:      p.Domain,
			MemberID:    p.MemberID,
			OpenLineID:  p.OpenLineID,
			ConnectorID: p.ConnectorID,
			InstalledAt: p.InstalledAt,
			Sessions:    linked,
		})
	}

	return c.JSON(fiber.Map{"queues": result})
}

// PUT /ui/bitrix/queues — atualiza open_line_id de um portal (mantido para compatibilidade)
func (h *handlers) uiUpdateBitrixQueue(c *fiber.Ctx) error {
	var body struct {
		Domain     string `json:"domain"`
		OpenLineID int    `json:"open_line_id"`
	}
	if err := c.BodyParser(&body); err != nil || body.Domain == "" || body.OpenLineID <= 0 {
		return c.Status(400).JSON(fiber.Map{"error": "domain e open_line_id (>0) são obrigatórios"})
	}
	domain := normalizePortalParam(body.Domain)
	portal, err := h.repo.GetBitrixPortalByDomain(c.Context(), domain)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "portal não encontrado: " + domain})
	}
	portal.OpenLineID = body.OpenLineID
	if err := h.repo.UpsertBitrixPortal(c.Context(), portal); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	go func() {
		ctx := context.Background()
		creds := h.portalToCreds(portal)
		if err := h.bitrixClient.ActivateConnector(ctx, creds, portal.ConnectorID, body.OpenLineID, true); err != nil {
			h.log.Warn("uiUpdateBitrixQueue: activate connector failed", zap.String("domain", domain), zap.Error(err))
		} else {
			h.log.Info("uiUpdateBitrixQueue: connector reactivated", zap.String("domain", domain), zap.Int("open_line_id", body.OpenLineID))
		}
	}()
	return c.JSON(fiber.Map{"status": "updated", "domain": domain, "open_line_id": body.OpenLineID})
}

// POST /ui/bitrix/queues/link — cria ou atualiza o vínculo portal+sessão+fila
// Body: { "domain": "empresa.bitrix24.com.br", "session_jid": "5519...@s.whatsapp.net", "open_line_id": 218 }
func (h *handlers) uiLinkQueue(c *fiber.Ctx) error {
	var body struct {
		Domain     string `json:"domain"`
		SessionJID string `json:"session_jid"`
		OpenLineID int    `json:"open_line_id"`
	}
	if err := c.BodyParser(&body); err != nil || body.Domain == "" || body.SessionJID == "" || body.OpenLineID <= 0 {
		return c.Status(400).JSON(fiber.Map{"error": "domain, session_jid e open_line_id (>0) são obrigatórios"})
	}

	domain := normalizePortalParam(body.Domain)

	portal, err := h.repo.GetBitrixPortalByDomain(c.Context(), domain)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "portal não encontrado: " + domain})
	}

	// Cria/atualiza o bitrix_account que o ProcessInbound usa para rotear
	acct := &db.BitrixAccount{
		ID:           generateUUID(),
		SessionJID:   body.SessionJID,
		Domain:       "https://" + domain,
		ClientID:     h.cfg.Bitrix.ClientID,
		ClientSecret: h.cfg.Bitrix.ClientSecret,
		OpenLineID:   body.OpenLineID,
		ConnectorID:  portal.ConnectorID,
		RedirectURI:  h.cfg.App.BaseURL() + "/bitrix/callback",
		Status:       db.BitrixAccountActive,
	}
	if err := h.repo.UpsertBitrixAccount(c.Context(), acct); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	// Atualiza open_line_id padrão do portal se for o primeiro vínculo
	if portal.OpenLineID == 0 {
		portal.OpenLineID = body.OpenLineID
		_ = h.repo.UpsertBitrixPortal(c.Context(), portal)
	}

	// Salva o token em bitrix_tokens para que o ProcessInbound consiga autenticar
	creds := h.portalToCreds(portal)
	if err := h.bitrixClient.SaveToken(c.Context(), creds, portal.AccessToken, portal.RefreshToken,
		int(portal.ExpiresAt.Sub(timeNow()).Seconds())); err != nil {
		h.log.Warn("uiLinkQueue: save token failed", zap.Error(err))
	}

	// Registra e ativa o connector em background.
	// Usa as creds do account recém-criado (app local, INSTALLED:true) para o event.bind.
	localCreds := bitrix.TenantCreds{
		Domain:       acct.Domain,
		ClientID:     acct.ClientID,
		ClientSecret: acct.ClientSecret,
		RedirectURI:  acct.RedirectURI,
	}
	go func() {
		ctx := context.Background()
		appBase := h.cfg.App.BaseURL()
		eventURL := appBase + "/bitrix/connector/event"
		if err := h.bitrixClient.RegisterConnector(ctx, localCreds, portal.ConnectorID, "WhatsApp UC", appBase+"/bitrix-connect"); err != nil {
			h.log.Warn("uiLinkQueue: register connector failed", zap.String("domain", domain), zap.Error(err))
		}
		// send_message: webhook direto para mensagens do operador → WA (independe de INSTALLED)
		if err := h.bitrixClient.SetConnectorData(ctx, localCreds, portal.ConnectorID, body.OpenLineID, eventURL); err != nil {
			h.log.Warn("uiLinkQueue: set connector data failed", zap.String("domain", domain), zap.Error(err))
		} else {
			h.log.Info("uiLinkQueue: connector send_message webhook set", zap.String("url", eventURL))
		}
		if err := h.bitrixClient.ActivateConnector(ctx, localCreds, portal.ConnectorID, body.OpenLineID, true); err != nil {
			h.log.Warn("uiLinkQueue: activate connector failed", zap.String("domain", domain), zap.Int("line", body.OpenLineID), zap.Error(err))
		}
		// Unbind qualquer binding antigo antes de fazer o novo
		if existing, err := h.bitrixClient.ListEventBindings(ctx, localCreds); err == nil {
			var bindings []struct {
				Event   string `json:"event"`
				Handler string `json:"handler"`
			}
			if json.Unmarshal(existing, &bindings) == nil {
				for _, b := range bindings {
					if b.Event == "ONIMCONNECTORMESSAGEADD" {
						_ = h.bitrixClient.UnbindEvent(ctx, localCreds, b.Event, b.Handler)
					}
				}
			}
		}
		if err := h.bitrixClient.BindEvent(ctx, localCreds, "ONIMCONNECTORMESSAGEADD", eventURL); err != nil {
			h.log.Warn("uiLinkQueue: bind event failed (app local)", zap.Error(err))
		} else {
			h.log.Info("uiLinkQueue: event bound with local app token")
		}
		h.log.Info("uiLinkQueue: connector activated",
			zap.String("domain", domain), zap.String("jid", body.SessionJID), zap.Int("line", body.OpenLineID))
	}()

	h.log.Info("queue link created", zap.String("domain", domain), zap.String("jid", body.SessionJID), zap.Int("line", body.OpenLineID))
	return c.JSON(fiber.Map{"status": "linked", "domain": domain, "session_jid": body.SessionJID, "open_line_id": body.OpenLineID})
}

// timeNow retorna time.Now() — extraído para facilitar testes.
var timeNow = func() time.Time { return time.Now() }

// DELETE /ui/bitrix/queues/link?domain=...&jid=... — remove o vínculo portal+sessão
func (h *handlers) uiUnlinkQueue(c *fiber.Ctx) error {
	domain := normalizePortalParam(c.Query("domain"))
	jid := c.Query("jid")
	if domain == "" || jid == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain e jid são obrigatórios"})
	}
	// Remove o bitrix_account para esse JID
	if err := h.repo.DeleteBitrixAccount(c.Context(), jid); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.log.Info("queue link removed", zap.String("domain", domain), zap.String("jid", jid))
	return c.JSON(fiber.Map{"status": "unlinked", "jid": jid})
}

// POST /ui/bitrix/queues/activate — força register+activate do connector no Bitrix
// Body: { "domain": "empresa.bitrix24.com.br", "open_line_id": 218 }
// Retorna resultado detalhado de cada etapa (register, activate, bind).
func (h *handlers) uiActivateConnector(c *fiber.Ctx) error {
	var body struct {
		Domain     string `json:"domain"`
		OpenLineID int    `json:"open_line_id"`
	}
	if err := c.BodyParser(&body); err != nil || body.Domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain é obrigatório"})
	}

	domain := normalizePortalParam(body.Domain)
	portal, err := h.repo.GetBitrixPortalByDomain(c.Context(), domain)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "portal não encontrado: " + domain})
	}

	lineID := body.OpenLineID
	if lineID <= 0 {
		lineID = portal.OpenLineID
	}
	if lineID <= 0 {
		lineID = 1
	}

	// Usa o app local se disponível — o Partner App pode estar com INSTALLED:false
	// o que impede o Bitrix de disparar eventos mesmo com event.bind registrado.
	// O app local tem instalação completa e escopo suficiente.
	creds := h.localCredsForDomain(c.Context(), domain, portal)
	appBase := h.cfg.App.BaseURL()
	h.log.Info("uiActivateConnector: using appBase", zap.String("appBase", appBase), zap.String("event_url", appBase+"/bitrix/connector/event"))
	steps := map[string]string{}

	// Salva token primeiro
	if err := h.bitrixClient.SaveToken(c.Context(), creds, portal.AccessToken, portal.RefreshToken,
		int(portal.ExpiresAt.Sub(timeNow()).Seconds())); err != nil {
		steps["save_token"] = "erro: " + err.Error()
	} else {
		steps["save_token"] = "ok"
	}

	// Verifica qual app está sendo usado
	if appInfo, err := h.bitrixClient.RawCall(c.Context(), creds, "app.info", map[string]interface{}{}); err == nil {
		steps["app_info"] = string(appInfo)
	}

	eventURL := appBase + "/bitrix/connector/event"
	// Register — PLACEMENT_HANDLER é só a UI de configuração (slider), não o endpoint de mensagens
	if err := h.bitrixClient.RegisterConnector(c.Context(), creds, portal.ConnectorID, "WhatsApp UC", appBase+"/bitrix-connect"); err != nil {
		steps["register"] = "erro: " + err.Error()
	} else {
		steps["register"] = "ok"
	}

	// Ativa em TODAS as open lines onde o connector já existe ou na lineID especificada.
	lines := h.discoverOpenLines(c.Context(), creds, portal.ConnectorID, lineID)
	activatedLines := []int{}
	for _, lid := range lines {
		// connector.data.set define o webhook direto para mensagens do operador → WA.
		// Funciona independente de INSTALLED — é um webhook simples, não event.bind.
		if err := h.bitrixClient.SetConnectorData(c.Context(), creds, portal.ConnectorID, lid, eventURL); err != nil {
			steps[fmt.Sprintf("set_data_line_%d", lid)] = "erro: " + err.Error()
		} else {
			steps[fmt.Sprintf("set_data_line_%d", lid)] = "ok"
		}
		if err := h.bitrixClient.ActivateConnector(c.Context(), creds, portal.ConnectorID, lid, true); err != nil {
			steps[fmt.Sprintf("activate_line_%d", lid)] = "erro: " + err.Error()
		} else {
			steps[fmt.Sprintf("activate_line_%d", lid)] = "ok"
			activatedLines = append(activatedLines, lid)
		}
	}
	if len(activatedLines) > 0 {
		steps["activate"] = fmt.Sprintf("ok (linhas: %v)", activatedLines)
	} else {
		steps["activate"] = "nenhuma linha ativada"
	}

	// event.bind deve usar o token do app local (INSTALLED:true).
	// Busca o account ativo para esse domain — ele tem ClientID/Secret do app local.
	// Faz unbind+rebind diretamente na API do Bitrix com o access_token do account.
	bindDone := false
	for _, activeJID := range h.waManager.ListSessions() {
		acct, err := h.repo.GetBitrixAccountByJID(c.Context(), activeJID)
		if err != nil {
			continue
		}
		acctDomain := strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(acct.Domain, "https://"), "http://"))
		if acctDomain != domain {
			continue
		}
		// Usa as creds do app local para buscar o token (ClientID diferente do Partner App)
		localCreds := bitrix.TenantCreds{
			Domain:       acct.Domain,
			ClientID:     acct.ClientID,
			ClientSecret: acct.ClientSecret,
			RedirectURI:  acct.RedirectURI,
		}
		if existing, err := h.bitrixClient.ListEventBindings(c.Context(), localCreds); err == nil {
			var bindings []struct {
				Event   string `json:"event"`
				Handler string `json:"handler"`
			}
			if json.Unmarshal(existing, &bindings) == nil {
				for _, b := range bindings {
					if b.Event == "ONIMCONNECTORMESSAGEADD" {
						_ = h.bitrixClient.UnbindEvent(c.Context(), localCreds, b.Event, b.Handler)
					}
				}
			}
		}
		if err := h.bitrixClient.BindEvent(c.Context(), localCreds, "ONIMCONNECTORMESSAGEADD", eventURL); err != nil {
			steps["bind_event"] = "erro (app local): " + err.Error()
		} else {
			steps["bind_event"] = "ok (app local)"
			bindDone = true
		}
		break
	}
	if !bindDone && steps["bind_event"] == "" {
		// Fallback: usa creds do portal (Partner App)
		if existing, err := h.bitrixClient.ListEventBindings(c.Context(), creds); err == nil {
			var bindings []struct {
				Event   string `json:"event"`
				Handler string `json:"handler"`
			}
			if json.Unmarshal(existing, &bindings) == nil {
				for _, b := range bindings {
					if b.Event == "ONIMCONNECTORMESSAGEADD" {
						_ = h.bitrixClient.UnbindEvent(c.Context(), creds, b.Event, b.Handler)
					}
				}
			}
		}
		if err := h.bitrixClient.BindEvent(c.Context(), creds, "ONIMCONNECTORMESSAGEADD", eventURL); err != nil {
			steps["bind_event"] = "erro (partner): " + err.Error()
		} else {
			steps["bind_event"] = "ok (partner app)"
		}
	}

	h.log.Info("uiActivateConnector result", zap.String("domain", domain), zap.Any("steps", steps))

	// Retorna erro se nenhuma linha foi ativada
	if !strings.HasPrefix(steps["activate"], "ok") {
		return c.Status(500).JSON(fiber.Map{"status": "error", "steps": steps})
	}
	return c.JSON(fiber.Map{"status": "ok", "domain": domain, "open_line_id": lineID, "steps": steps})
}

// POST /bitrix/connector/event — recebe ONIMCONNECTORMESSAGEADD ou send_message do Bitrix24
// O operador respondeu no Contact Center → encaminha para o WhatsApp correto.
// O Bitrix pode enviar form-encoded (event.bind) ou JSON (connector.data.set send_message).
func (h *handlers) bitrixConnectorEvent(c *fiber.Ctx) error {
	h.log.Info("connector event received",
		zap.String("method", c.Method()),
		zap.String("content_type", c.Get("Content-Type")),
		zap.String("raw_body", string(c.Body())),
	)

	// Tenta form-encoded primeiro (ONIMCONNECTORMESSAGEADD via event.bind)
	connector := c.FormValue("data[CONNECTOR]")
	lineStr := c.FormValue("data[LINE]")
	chatIDRaw := c.FormValue("data[MESSAGES][0][chat][id]")
	imChatID := c.FormValue("data[MESSAGES][0][im][chat_id]")
	imMsgID := c.FormValue("data[MESSAGES][0][im][message_id]")
	text := c.FormValue("data[MESSAGES][0][message][text]")
	userID := c.FormValue("data[MESSAGES][0][message][user_id]")
	fileDownloadLink := c.FormValue("data[MESSAGES][0][message][files][0][downloadLink]")
	fileName := c.FormValue("data[MESSAGES][0][message][files][0][name]")
	fileMime := c.FormValue("data[MESSAGES][0][message][files][0][mime]")

	// Se não veio form-encoded, tenta JSON (send_message via connector.data.set)
	if connector == "" && strings.Contains(c.Get("Content-Type"), "application/json") {
		var payload struct {
			Connector string `json:"CONNECTOR"`
			Line      interface{} `json:"LINE"`
			Messages  []struct {
				Chat struct {
					ID string `json:"id"`
				} `json:"chat"`
				Im struct {
					ChatID    interface{} `json:"chat_id"`
					MessageID interface{} `json:"message_id"`
				} `json:"im"`
				Message struct {
					Text   string      `json:"text"`
					UserID interface{} `json:"user_id"`
					Files  []struct {
						DownloadLink string `json:"downloadLink"`
						Name         string `json:"name"`
						Mime         string `json:"mime"`
					} `json:"files"`
				} `json:"message"`
			} `json:"MESSAGES"`
		}
		if err := json.Unmarshal(c.Body(), &payload); err == nil && payload.Connector != "" {
			connector = payload.Connector
			lineStr = fmt.Sprintf("%v", payload.Line)
			if len(payload.Messages) > 0 {
				msg := payload.Messages[0]
				chatIDRaw = msg.Chat.ID
				imChatID = fmt.Sprintf("%v", msg.Im.ChatID)
				imMsgID = fmt.Sprintf("%v", msg.Im.MessageID)
				text = msg.Message.Text
				userID = fmt.Sprintf("%v", msg.Message.UserID)
				if len(msg.Message.Files) > 0 {
					fileDownloadLink = msg.Message.Files[0].DownloadLink
					fileName = msg.Message.Files[0].Name
					fileMime = msg.Message.Files[0].Mime
				}
			}
			h.log.Info("connector event: parsed as JSON")
		}
	}

	// Normaliza o JID do chat — remove device suffix ":47"
	chatID := sanitizeJID(chatIDRaw)

	h.log.Info("connector event parsed",
		zap.String("connector", connector),
		zap.String("line", lineStr),
		zap.String("chat_id_raw", chatIDRaw),
		zap.String("chat_id_normalized", chatID),
		zap.String("im_chat_id", imChatID),
		zap.String("im_msg_id", imMsgID),
		zap.String("user_id", userID),
		zap.String("text", text),
		zap.String("file_url", fileDownloadLink),
	)

	// user_id=0 é mensagem automática do sistema (welcome message), ignora
	if userID == "0" || userID == "" {
		h.log.Info("connector event: ignored (system message or no user_id)")
		return c.SendStatus(fiber.StatusOK)
	}
	// Precisa de texto OU arquivo
	if text == "" && fileDownloadLink == "" {
		h.log.Info("connector event: ignored (no text and no file)")
		return c.SendStatus(fiber.StatusOK)
	}
	if chatID == "" {
		h.log.Warn("connector event: ignored (chat_id empty)")
		return c.SendStatus(fiber.StatusOK)
	}

	cleanText := stripBBCode(text)
	ctx := context.Background()

	// Busca o contato pelo JID normalizado — tem o WAPhone real (número de telefone)
	contact, err := h.repo.GetContactByWAJID(ctx, chatID)

	// toJID: usa o número de telefone real do contato quando disponível.
	// chatID pode ser "@lid" (LID do WhatsApp) que o whatsmeow não consegue enviar diretamente.
	// WAPhone tem o número real (ex: "5519910001772") → enviamos para "5519910001772@s.whatsapp.net".
	toJID := chatID
	if err == nil && contact.WAPhone != "" {
		toJID = contact.WAPhone + "@s.whatsapp.net"
	}

	if err != nil {
		h.log.Warn("connector event: contact not found by normalized JID, trying sessions directly",
			zap.String("chat_id", chatID),
			zap.String("chat_id_raw", chatIDRaw),
			zap.Error(err),
		)
		sessions := h.waManager.ListSessions()
		if len(sessions) == 1 {
			sessionJID := sessions[0]
			line := 0
			fmt.Sscanf(lineStr, "%d", &line)
			if err := h.q.PushOutbound(ctx, &queue.OutboundJob{
				SessionJID:      sessionJID,
				ToJID:           toJID,
				Text:            cleanText,
				BitrixConnector: connector,
				BitrixLine:      line,
				BitrixImChatID:  imChatID,
				BitrixImMsgID:   imMsgID,
				BitrixChatExtID: chatIDRaw,
				FileURL:         fileDownloadLink,
				FileName:        fileName,
				FileMime:        fileMime,
			}); err != nil {
				h.log.Error("connector event: push outbound failed (fallback)", zap.Error(err))
				return c.SendStatus(fiber.StatusOK)
			}
			h.log.Info("outbound job queued (fallback — single session)",
				zap.String("to_jid", toJID),
				zap.String("session_jid", sessionJID),
				zap.String("text", cleanText),
			)
			return c.SendStatus(fiber.StatusOK)
		}
		return c.SendStatus(fiber.StatusOK)
	}

	// Descobre qual sessão WA usar a partir do contato
	sessionJID := ""
	if contact.SessionID != nil {
		sess, err := h.repo.GetSessionByID(ctx, *contact.SessionID)
		if err == nil {
			sessionJID = sess.JID
		} else {
			h.log.Warn("connector event: GetSessionByID failed",
				zap.String("contact_jid", contact.WAJID),
				zap.Error(err),
			)
		}
	}
	// Fallback: se o contato não tem session_id, usa a única sessão ativa
	if sessionJID == "" {
		sessions := h.waManager.ListSessions()
		if len(sessions) == 1 {
			sessionJID = sessions[0]
			h.log.Info("connector event: using only active session as fallback",
				zap.String("session_jid", sessionJID),
			)
		} else {
			h.log.Warn("connector event: no session found for contact",
				zap.String("chat_id", chatID),
				zap.Int("active_sessions", len(sessions)),
			)
			return c.SendStatus(fiber.StatusOK)
		}
	}

	line := 0
	fmt.Sscanf(lineStr, "%d", &line)

	if err := h.q.PushOutbound(ctx, &queue.OutboundJob{
		SessionJID:      sessionJID,
		ToJID:           toJID,
		Text:            cleanText,
		BitrixConnector: connector,
		BitrixLine:      line,
		BitrixImChatID:  imChatID,
		BitrixImMsgID:   imMsgID,
		BitrixChatExtID: chatIDRaw,
		FileURL:         fileDownloadLink,
		FileName:        fileName,
		FileMime:        fileMime,
	}); err != nil {
		h.log.Error("connector event: push outbound failed", zap.Error(err))
		return c.SendStatus(fiber.StatusOK)
	}

	h.log.Info("outbound job queued for JID",
		zap.String("to_jid", toJID),
		zap.String("session_jid", sessionJID),
		zap.String("text", cleanText),
		zap.String("connector", connector),
		zap.Int("line", line),
	)

	return c.SendStatus(fiber.StatusOK)
}

// POST /debug/bitrix-event — loga o payload completo para testes manuais (sem autenticação)
func (h *handlers) debugBitrixEvent(c *fiber.Ctx) error {
	h.log.Info("DEBUG bitrix-event",
		zap.String("method", c.Method()),
		zap.String("content_type", c.Get("Content-Type")),
		zap.String("raw_body", string(c.Body())),
		zap.String("query", string(c.Request().URI().QueryString())),
	)
	return c.JSON(fiber.Map{
		"status":       "logged",
		"body_length":  len(c.Body()),
		"content_type": c.Get("Content-Type"),
	})
}

// GET /debug/dead-queue — lê até 20 jobs da dead queue para diagnóstico
func (h *handlers) debugDeadQueue(c *fiber.Ctx) error {
	items, err := h.q.PeekDead(c.Context(), 20)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"count": len(items), "items": items})
}

// GET /debug/connector-status?domain=...&line=218&connector=whatsapp_uc
// Retorna o status real do connector na linha — crítico para diagnosticar
// por que ONIMCONNECTORMESSAGEADD não dispara.
func (h *handlers) debugConnectorStatus(c *fiber.Ctx) error {
	domain := normalizePortalParam(c.Query("domain"))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain é obrigatório"})
	}
	line := 0
	fmt.Sscanf(c.Query("line"), "%d", &line)
	if line == 0 {
		return c.Status(400).JSON(fiber.Map{"error": "line é obrigatório"})
	}
	connectorID := c.Query("connector")
	if connectorID == "" {
		connectorID = "whatsapp_uc"
	}

	portal, err := h.repo.GetBitrixPortalByDomain(c.Context(), domain)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "portal não encontrado: " + domain})
	}

	creds := h.portalToCreds(portal)
	raw, err := h.bitrixClient.GetConnectorStatus(c.Context(), creds, connectorID, line)
	result := fiber.Map{
		"domain":       domain,
		"connector_id": connectorID,
		"line":         line,
	}
	if err != nil {
		result["error"] = err.Error()
	}
	if raw != nil {
		result["raw"] = json.RawMessage(raw)
	}
	return c.JSON(result)
}

// GET /debug/event-bindings?domain=...
// Lista todos os event handlers registrados — confirma se ONIMCONNECTORMESSAGEADD
// está bindado e qual a URL atual do handler.
func (h *handlers) debugEventBindings(c *fiber.Ctx) error {
	domain := normalizePortalParam(c.Query("domain"))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain é obrigatório"})
	}

	portal, err := h.repo.GetBitrixPortalByDomain(c.Context(), domain)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "portal não encontrado: " + domain})
	}

	creds := h.portalToCreds(portal)
	raw, err := h.bitrixClient.ListEventBindings(c.Context(), creds)
	result := fiber.Map{"domain": domain}
	if err != nil {
		result["error"] = err.Error()
	}
	if raw != nil {
		result["raw"] = json.RawMessage(raw)
	}
	return c.JSON(result)
}

// POST /debug/bitrix-call — executa qualquer método REST no Bitrix24 diretamente
// Body JSON: { "domain": "uctdemo.bitrix24.com", "method": "event.unbind", "params": {...}, "use_local_app": true }
// use_local_app=true usa as credenciais do app local (bitrix_account) em vez do Partner App (portal).
func (h *handlers) debugBitrixCall(c *fiber.Ctx) error {
	var body struct {
		Domain      string                 `json:"domain"`
		Method      string                 `json:"method"`
		Params      map[string]interface{} `json:"params"`
		UseLocalApp bool                   `json:"use_local_app"`
	}
	if err := c.BodyParser(&body); err != nil || body.Domain == "" || body.Method == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain e method são obrigatórios"})
	}
	domain := normalizePortalParam(body.Domain)
	if body.Params == nil {
		body.Params = map[string]interface{}{}
	}

	var creds bitrix.TenantCreds
	if body.UseLocalApp {
		// Usa o token do app local salvo via /bitrix/callback
		sessions := h.waManager.ListSessions()
		var acct *db.BitrixAccount
		for _, jid := range sessions {
			a, err := h.repo.GetBitrixAccountByJID(c.Context(), jid)
			if err == nil && strings.EqualFold(strings.TrimPrefix(a.Domain, "https://"), domain) {
				acct = a
				break
			}
		}
		if acct == nil {
			return c.Status(404).JSON(fiber.Map{"error": "bitrix_account não encontrado para domain: " + domain})
		}
		creds = bitrix.TenantCreds{
			Domain:       acct.Domain,
			ClientID:     acct.ClientID,
			ClientSecret: acct.ClientSecret,
			RedirectURI:  acct.RedirectURI,
		}
	} else {
		portal, err := h.repo.GetBitrixPortalByDomain(c.Context(), domain)
		if err != nil {
			return c.Status(404).JSON(fiber.Map{"error": "portal não encontrado: " + domain})
		}
		creds = h.portalToCreds(portal)
	}

	raw, err := h.bitrixClient.RawCall(c.Context(), creds, body.Method, body.Params)
	result := fiber.Map{"domain": domain, "method": body.Method, "use_local_app": body.UseLocalApp}
	if err != nil {
		result["error"] = err.Error()
	}
	if raw != nil {
		result["raw"] = json.RawMessage(raw)
	}
	return c.JSON(result)
}

// POST /debug/rebind-event — força unbind+rebind do ONIMCONNECTORMESSAGEADD
// Body JSON: { "domain": "uctdemo.bitrix24.com", "handler_url": "https://..." }
// Se handler_url vier vazio, usa o appBase+"/bitrix/connector/event" padrão.
func (h *handlers) debugRebindEvent(c *fiber.Ctx) error {
	var body struct {
		Domain     string `json:"domain"`
		HandlerURL string `json:"handler_url"`
	}
	if err := c.BodyParser(&body); err != nil || body.Domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain é obrigatório"})
	}
	domain := normalizePortalParam(body.Domain)
	portal, err := h.repo.GetBitrixPortalByDomain(c.Context(), domain)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "portal não encontrado: " + domain})
	}
	creds := h.portalToCreds(portal)
	handlerURL := body.HandlerURL
	if handlerURL == "" {
		handlerURL = h.cfg.App.BaseURL() + "/bitrix/connector/event"
	}
	steps := map[string]string{}

	// Lista todos os bindings existentes e remove cada um pelo handler URL específico.
	// O Bitrix não aceita unbind sem handler — precisamos iterar e remover um a um.
	existing, _ := h.bitrixClient.ListEventBindings(c.Context(), creds)
	var bindings []struct {
		Event   string `json:"event"`
		Handler string `json:"handler"`
	}
	removed := 0
	if existing != nil && json.Unmarshal(existing, &bindings) == nil {
		for _, b := range bindings {
			if b.Event == "ONIMCONNECTORMESSAGEADD" {
				if err := h.bitrixClient.UnbindEvent(c.Context(), creds, b.Event, b.Handler); err == nil {
					removed++
				}
			}
		}
	}
	steps["unbind"] = fmt.Sprintf("removidos %d bindings", removed)

	if err := h.bitrixClient.BindEvent(c.Context(), creds, "ONIMCONNECTORMESSAGEADD", handlerURL); err != nil {
		steps["bind"] = "erro: " + err.Error()
	} else {
		steps["bind"] = "ok"
	}

	// Verifica o estado atual após rebind
	raw, _ := h.bitrixClient.ListEventBindings(c.Context(), creds)
	return c.JSON(fiber.Map{
		"domain":      domain,
		"handler_url": handlerURL,
		"steps":       steps,
		"bindings":    json.RawMessage(raw),
	})
}

// GET /debug/connector-data?domain=...&line=218&connector=whatsapp_uc
// Retorna os dados do connector em uma linha — inclui o campo HANDLER configurado
// via imconnector.register, que é o endpoint que o Bitrix usa para entregar mensagens.
func (h *handlers) debugConnectorData(c *fiber.Ctx) error {
	domain := normalizePortalParam(c.Query("domain"))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain é obrigatório"})
	}
	line := 0
	fmt.Sscanf(c.Query("line"), "%d", &line)
	if line == 0 {
		return c.Status(400).JSON(fiber.Map{"error": "line é obrigatório"})
	}
	connectorID := c.Query("connector")
	if connectorID == "" {
		connectorID = "whatsapp_uc"
	}

	portal, err := h.repo.GetBitrixPortalByDomain(c.Context(), domain)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "portal não encontrado: " + domain})
	}

	creds := h.portalToCreds(portal)
	raw, err := h.bitrixClient.GetConnectorData(c.Context(), creds, connectorID, line)
	result := fiber.Map{"domain": domain, "connector": connectorID, "line": line}
	if err != nil {
		result["error"] = err.Error()
	}
	if raw != nil {
		result["raw"] = json.RawMessage(raw)
	}
	return c.JSON(result)
}

// GET /debug/connector-list?domain=...
// Lista os connectors registrados no portal — permite verificar se o campo HANDLER
// está configurado com a URL correta após o imconnector.register.
func (h *handlers) debugConnectorList(c *fiber.Ctx) error {
	domain := normalizePortalParam(c.Query("domain"))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain é obrigatório"})
	}

	portal, err := h.repo.GetBitrixPortalByDomain(c.Context(), domain)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "portal não encontrado: " + domain})
	}

	creds := h.portalToCreds(portal)
	raw, err := h.bitrixClient.GetConnectorList(c.Context(), creds)
	result := fiber.Map{"domain": domain}
	if err != nil {
		result["error"] = err.Error()
	}
	if raw != nil {
		result["raw"] = json.RawMessage(raw)
	}
	return c.JSON(result)
}

// ─── Helpers de credenciais e descoberta de linhas ───────────────────────────

// localCredsForDomain retorna as credenciais do app LOCAL para um domínio.
// O app local tem INSTALLED:true no Bitrix, o que é obrigatório para receber eventos.
// Se não encontrar app local, cai de volta para as credenciais do portal (Partner App).
func (h *handlers) localCredsForDomain(ctx context.Context, domain string, portal *db.BitrixPortal) bitrix.TenantCreds {
	for _, jid := range h.waManager.ListSessions() {
		acct, err := h.repo.GetBitrixAccountByJID(ctx, jid)
		if err != nil {
			continue
		}
		acctDomain := strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(acct.Domain, "https://"), "http://"))
		if acctDomain == domain && acct.ClientID != "" && acct.ClientSecret != "" {
			return bitrix.TenantCreds{
				Domain:       acct.Domain,
				ClientID:     acct.ClientID,
				ClientSecret: acct.ClientSecret,
				RedirectURI:  acct.RedirectURI,
			}
		}
	}
	return h.portalToCreds(portal)
}

// discoverOpenLines retorna apenas a linha especificada.
func (h *handlers) discoverOpenLines(_ context.Context, _ bitrix.TenantCreds, _ string, lineID int) []int {
	if lineID <= 0 {
		return []int{1}
	}
	return []int{lineID}
}

// ─── Helpers de filtragem por portal ─────────────────────────────────────────

// normalizePortalParam normaliza o parâmetro ?portal= da query string,
// removendo https://, http:// e trailing slash — igual ao que fazemos no banco.
func normalizePortalParam(p string) string {
	p = strings.TrimPrefix(p, "https://")
	p = strings.TrimPrefix(p, "http://")
	p = strings.TrimRight(p, "/")
	return strings.ToLower(strings.TrimSpace(p))
}

// sessionsForPortal retorna apenas as sessões WA que estão vinculadas ao portal
// via bitrix_accounts (que é a tabela que o ProcessInbound usa para roteamento).
func (h *handlers) sessionsForPortal(ctx context.Context, portal string, allSessions []string) []string {
	var filtered []string
	for _, jid := range allSessions {
		acct, err := h.repo.GetBitrixAccountByJID(ctx, jid)
		if err != nil {
			continue
		}
		acctDomain := strings.TrimPrefix(acct.Domain, "https://")
		acctDomain = strings.TrimPrefix(acctDomain, "http://")
		acctDomain = strings.ToLower(strings.TrimRight(acctDomain, "/"))
		if acctDomain == portal {
			filtered = append(filtered, jid)
		}
	}
	return filtered
}

// sanitizeJID normaliza JIDs do Bitrix para formato aceito pelo whatsmeow.
// "127586399207476:47@lid" → "127586399207476@lid" (remove device part, mantém @lid)
// "5511999@s.whatsapp.net" → mantém como está
func sanitizeJID(jid string) string {
	if idx := strings.Index(jid, ":"); idx != -1 {
		suffix := ""
		if at := strings.Index(jid, "@"); at != -1 {
			suffix = jid[at:] // "@lid" ou "@s.whatsapp.net"
		}
		return jid[:idx] + suffix
	}
	return jid
}

// stripBBCode remove tags BBCode do Bitrix24 ([b], [/b], [br], etc.).
func stripBBCode(s string) string {
	// [br] → newline
	s = strings.ReplaceAll(s, "[br]", "\n")
	// Remove todas as outras tags [tag] e [/tag]
	result := strings.Builder{}
	inTag := false
	for _, ch := range s {
		if ch == '[' {
			inTag = true
			continue
		}
		if ch == ']' {
			inTag = false
			continue
		}
		if !inTag {
			result.WriteRune(ch)
		}
	}
	return strings.TrimSpace(result.String())
}

// POST /bitrix/webhook — recebe mensagens do operador via Bitrix
func (h *handlers) bitrixWebhook(c *fiber.Ctx) error {
	var payload struct {
		Event string `json:"event"`
		Data  struct {
			SessionID string `json:"SESSION_ID"`
			Message   string `json:"MESSAGE"`
			UserPhone string `json:"USER_PHONE"`
			SessionJID string `json:"WA_SESSION_JID"`
			ToJID     string `json:"WA_TO_JID"`
		} `json:"data"`
	}
	if err := c.BodyParser(&payload); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	if payload.Data.Message == "" {
		return c.SendStatus(fiber.StatusNoContent)
	}

	if err := h.q.PushOutbound(c.Context(), &queue.OutboundJob{
		SessionJID: payload.Data.SessionJID,
		ToJID:      payload.Data.ToJID,
		Text:       payload.Data.Message,
	}); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}

	return c.SendStatus(fiber.StatusOK)
}

// GET /stats/daily?days=7
func (h *handlers) dailyStats(c *fiber.Ctx) error {
	days, _ := strconv.Atoi(c.Query("days", "7"))
	if days < 1 || days > 90 {
		days = 7
	}
	stats, err := h.repo.GetDailyStats(c.Context(), days)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	return c.JSON(stats)
}

// GET /stats/queues
func (h *handlers) queueStats(c *fiber.Ctx) error {
	in, out, dead := h.q.Lengths(c.Context())
	return c.JSON(fiber.Map{
		"inbound":  in,
		"outbound": out,
		"dead":     dead,
	})
}
