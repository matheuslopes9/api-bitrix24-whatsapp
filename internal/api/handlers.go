package api

import (
	"context"
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
			ExpiresAt:    time.Now().Add(time.Duration(exp) * time.Second),
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

	// Se temos session_jid, busca a conta cadastrada na UI e salva token/ativa
	if sessionJID != "" {
		acct, err := h.repo.GetBitrixAccountByJID(c.Context(), sessionJID)
		if err != nil {
			h.log.Warn("bitrix callback: account not found for jid, saving with domain from response",
				zap.String("session_jid", sessionJID), zap.Error(err))
		} else {
			creds := bitrix.TenantCreds{
				Domain:       acct.Domain,
				ClientID:     acct.ClientID,
				ClientSecret: acct.ClientSecret,
				RedirectURI:  acct.RedirectURI,
			}
			if err := h.bitrixClient.SaveToken(c.Context(), creds, accessToken, refreshToken, exp); err != nil {
				return fiber.NewError(fiber.StatusInternalServerError, err.Error())
			}
			_ = h.repo.UpdateBitrixAccountStatus(c.Context(), sessionJID, db.BitrixAccountActive)

			eventURL := strings.TrimSuffix(acct.RedirectURI, "/bitrix/callback") + "/bitrix/connector/event"
			go func() {
				ctx := context.Background()
				if err := h.bitrixClient.RegisterConnector(ctx, creds, acct.ConnectorID, "WhatsApp UC", acct.RedirectURI); err != nil {
					h.log.Warn("imconnector.register failed", zap.Error(err))
				}
				if err := h.bitrixClient.ActivateConnector(ctx, creds, acct.ConnectorID, acct.OpenLineID, true); err != nil {
					h.log.Warn("imconnector.activate failed", zap.Error(err))
				}
				if err := h.bitrixClient.BindEvent(ctx, creds, "ONIMCONNECTORMESSAGEADD", eventURL); err != nil {
					h.log.Warn("event.bind failed", zap.Error(err))
				}
				h.log.Info("bitrix account activated", zap.String("domain", acct.Domain), zap.String("jid", sessionJID))
			}()
			return c.SendStatus(fiber.StatusOK)
		}
	}

	// Fallback sem session_jid: usa domain vindo da resposta do Bitrix
	if domain == "" {
		domain = h.cfg.Bitrix.Domain
	}
	fallbackCreds := bitrix.TenantCreds{
		Domain:       domain,
		ClientID:     h.cfg.Bitrix.ClientID,
		ClientSecret: h.cfg.Bitrix.ClientSecret,
		RedirectURI:  h.cfg.Bitrix.RedirectURI,
	}
	if err := h.bitrixClient.SaveToken(c.Context(), fallbackCreds, accessToken, refreshToken, exp); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}

	eventURL := strings.TrimSuffix(h.cfg.Bitrix.RedirectURI, "/bitrix/callback") + "/bitrix/connector/event"
	go func() {
		ctx := context.Background()
		if err := h.bitrixClient.RegisterConnector(ctx, fallbackCreds, "whatsapp_uc", "WhatsApp UC", h.cfg.Bitrix.RedirectURI); err != nil {
			h.log.Warn("imconnector.register failed", zap.Error(err))
		}
		if err := h.bitrixClient.ActivateConnector(ctx, fallbackCreds, "whatsapp_uc", h.cfg.Bitrix.OpenLineID, true); err != nil {
			h.log.Warn("imconnector.activate failed", zap.Error(err))
		}
		if err := h.bitrixClient.BindEvent(ctx, fallbackCreds, "ONIMCONNECTORMESSAGEADD", eventURL); err != nil {
			h.log.Warn("event.bind failed", zap.Error(err))
		}
	}()

	return c.SendStatus(fiber.StatusOK)
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

// PUT /ui/bitrix/queues — atualiza open_line_id de um portal
// Body: { "domain": "empresa.bitrix24.com.br", "open_line_id": 3 }
func (h *handlers) uiUpdateBitrixQueue(c *fiber.Ctx) error {
	var body struct {
		Domain     string `json:"domain"`
		OpenLineID int    `json:"open_line_id"`
	}
	if err := c.BodyParser(&body); err != nil || body.Domain == "" || body.OpenLineID <= 0 {
		return c.Status(400).JSON(fiber.Map{"error": "domain e open_line_id (>0) são obrigatórios"})
	}

	domain := strings.TrimPrefix(body.Domain, "https://")
	domain = strings.TrimPrefix(domain, "http://")
	domain = strings.ToLower(strings.TrimRight(domain, "/"))

	portal, err := h.repo.GetBitrixPortalByDomain(c.Context(), domain)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "portal não encontrado: " + domain})
	}

	portal.OpenLineID = body.OpenLineID
	if err := h.repo.UpsertBitrixPortal(c.Context(), portal); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	// Atualiza também todos os bitrix_accounts vinculados a este portal
	accounts, _ := h.repo.ListBitrixAccounts(c.Context())
	for _, acct := range accounts {
		acctDomain := strings.TrimPrefix(acct.Domain, "https://")
		acctDomain = strings.TrimPrefix(acctDomain, "http://")
		if strings.EqualFold(acctDomain, domain) {
			acct.OpenLineID = body.OpenLineID
			_ = h.repo.UpsertBitrixAccount(c.Context(), acct)
		}
	}

	// Reativa o connector com a nova linha em background
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

// POST /bitrix/connector/event — recebe ONIMCONNECTORMESSAGEADD do Bitrix24
// O operador respondeu no Contact Center → encaminha para o WhatsApp correto
func (h *handlers) bitrixConnectorEvent(c *fiber.Ctx) error {
	h.log.Info("connector event received", zap.String("body", string(c.Body())))

	// Bitrix envia form-encoded
	connector := c.FormValue("data[CONNECTOR]")
	lineStr := c.FormValue("data[LINE]")
	chatID := c.FormValue("data[MESSAGES][0][chat][id]")      // JID externo (ex: "127586399207476:47@lid")
	imChatID := c.FormValue("data[MESSAGES][0][im][chat_id]") // ID interno do chat no Bitrix (ex: "6026")
	imMsgID := c.FormValue("data[MESSAGES][0][im][message_id]") // ID interno da msg no Bitrix (ex: "196946")
	text := c.FormValue("data[MESSAGES][0][message][text]")
	userID := c.FormValue("data[MESSAGES][0][message][user_id]")

	h.log.Info("connector event parsed",
		zap.String("connector", connector),
		zap.String("line", lineStr),
		zap.String("chat_id", chatID),
		zap.String("user_id", userID),
		zap.String("text", text))

	// Arquivo enviado pelo operador (outbound)
	fileDownloadLink := c.FormValue("data[MESSAGES][0][message][files][0][downloadLink]")
	fileName := c.FormValue("data[MESSAGES][0][message][files][0][name]")
	fileMime := c.FormValue("data[MESSAGES][0][message][files][0][mime]")

	// user_id=0 é mensagem automática do sistema (welcome message), ignora
	if userID == "0" || userID == "" {
		return c.SendStatus(fiber.StatusOK)
	}
	// Precisa de texto OU arquivo
	if text == "" && fileDownloadLink == "" {
		return c.SendStatus(fiber.StatusOK)
	}
	if chatID == "" {
		return c.SendStatus(fiber.StatusOK)
	}

	// Remove BBCode do Bitrix24 ([b]...[/b], [br], etc.)
	cleanText := stripBBCode(text)

	// Sanitiza o JID: "127586399207476:47@lid" → "127586399207476@s.whatsapp.net"
	toJID := sanitizeJID(chatID)

	ctx := context.Background()

	// Busca o contato pelo JID original (como foi salvo no banco)
	contact, err := h.repo.GetContactByWAJID(ctx, chatID)
	if err != nil {
		h.log.Warn("connector event: contact not found", zap.String("jid", chatID), zap.Error(err))
		return c.SendStatus(fiber.StatusOK)
	}

	// Descobre qual sessão WA usar
	sessionJID := ""
	if contact.SessionID != nil {
		sess, err := h.repo.GetSessionByID(ctx, *contact.SessionID)
		if err == nil {
			sessionJID = sess.JID
		}
	}
	if sessionJID == "" {
		h.log.Warn("connector event: no session found for contact", zap.String("jid", chatID))
		return c.SendStatus(fiber.StatusOK)
	}

	line := 0
	fmt.Sscanf(lineStr, "%d", &line)

	// Coloca na fila de saída
	if err := h.q.PushOutbound(ctx, &queue.OutboundJob{
		SessionJID:      sessionJID,
		ToJID:           toJID,
		Text:            cleanText,
		BitrixConnector: connector,
		BitrixLine:      line,
		BitrixImChatID:  imChatID,
		BitrixImMsgID:   imMsgID,
		BitrixChatExtID: chatID,
		FileURL:         fileDownloadLink,
		FileName:        fileName,
		FileMime:        fileMime,
	}); err != nil {
		h.log.Error("connector event: push outbound failed", zap.Error(err))
		return c.SendStatus(fiber.StatusOK)
	}

	h.log.Info("operator reply queued",
		zap.String("to_jid", toJID),
		zap.String("session", sessionJID),
		zap.String("text", cleanText))

	return c.SendStatus(fiber.StatusOK)
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
