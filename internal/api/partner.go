package api

// Handlers exclusivos do Bitrix24 Partner App (Marketplace Global).
//
// Fluxo:
//   1. Cliente instala o app no Marketplace → Bitrix chama POST /bitrix/install
//   2. Cliente abre o menu "Conectar WhatsApp" no Bitrix → abre GET /bitrix-connect em iframe
//   3. A página usa BX24.js (getAuth) e faz POST /bitrix/auth para salvar/atualizar o token
//   4. Com o token salvo, a página exibe o status da sessão WA e o QR Code (se não conectado)
//
// Estes endpoints são INDEPENDENTES dos endpoints admin (/dashboard, /bitrix/callback).
// Não há conflito de rotas.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/bitrix"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"go.uber.org/zap"
)

// ─── POST /bitrix/install ────────────────────────────────────────────────────
//
// Application Installer URL configurado no vendors.bitrix24.com.
// O Bitrix24 chama este endpoint no momento da instalação (ONAPPINSTALL).
// Payload form-encoded com: event, auth[access_token], auth[refresh_token],
// auth[domain], auth[expires_in], auth[member_id].
func (h *handlers) bitrixInstall(c *fiber.Ctx) error {
	// Loga tudo para diagnóstico — body bruto, headers, query string
	h.log.Info("partner install received",
		zap.String("method", c.Method()),
		zap.String("content_type", c.Get("Content-Type")),
		zap.String("query", string(c.Request().URI().QueryString())),
		zap.String("raw_body", string(c.Body())),
	)

	// O Bitrix Partner App (Marketplace) envia no INSTALL:
	//   AUTH_ID, REFRESH_ID, AUTH_EXPIRES, member_id, SERVER_ENDPOINT, APPLICATION_TOKEN
	// App local envia: auth[access_token], auth[refresh_token], auth[domain], auth[expires_in]
	// Suportamos ambos os formatos.

	event := c.FormValue("event")

	// Formato Partner App (Marketplace) — tem precedência
	accessToken := c.FormValue("AUTH_ID")
	refreshToken := c.FormValue("REFRESH_ID")
	expiresInStr := c.FormValue("AUTH_EXPIRES")
	memberID := c.FormValue("member_id")
	serverEndpoint := c.FormValue("SERVER_ENDPOINT") // ex: https://oauth.bitrix.info/rest/
	applicationToken := c.FormValue("APPLICATION_TOKEN")
	domain := c.FormValue("DOMAIN") // geralmente vazio no Marketplace

	// Formato app local — fallback
	if accessToken == "" {
		accessToken = c.FormValue("auth[access_token]")
		refreshToken = c.FormValue("auth[refresh_token]")
		expiresInStr = c.FormValue("auth[expires_in]")
		domain = c.FormValue("auth[domain]")
		memberID = c.FormValue("auth[member_id]")
	}
	// Fallback JSON
	if accessToken == "" {
		var body struct {
			Event string `json:"event"`
			Auth  struct {
				AccessToken  string `json:"access_token"`
				RefreshToken string `json:"refresh_token"`
				Domain       string `json:"domain"`
				MemberID     string `json:"member_id"`
				ExpiresIn    int    `json:"expires_in"`
			} `json:"auth"`
		}
		if err := c.BodyParser(&body); err == nil {
			event = body.Event
			accessToken = body.Auth.AccessToken
			refreshToken = body.Auth.RefreshToken
			domain = body.Auth.Domain
			memberID = body.Auth.MemberID
			expiresInStr = fmt.Sprintf("%d", body.Auth.ExpiresIn)
		}
	}

	// Quando o Bitrix Marketplace não envia domain, usamos o member_id como chave única
	// e o domain configurado como fallback. O domain real será registrado via /bitrix/auth.
	if domain == "" && serverEndpoint != "" {
		// SERVER_ENDPOINT é sempre oauth.bitrix.info — não é o domain do portal
		_ = serverEndpoint
	}
	if domain == "" {
		domain = h.cfg.Bitrix.Domain
	}
	_ = applicationToken // guardado nos logs; poderá ser usado para validação futura

	h.log.Info("partner install parsed",
		zap.String("event", event),
		zap.String("domain", domain),
		zap.String("member_id", memberID),
		zap.String("access_token_prefix", func() string {
			if len(accessToken) > 8 { return accessToken[:8] + "..." }
			return accessToken
		}()),
	)

	// Se ainda não temos token, o Bitrix provavelmente chamou o installer antes
	// do OAuth2 ser concluído (fluxo de teste). Retorna 200 sem erro para
	// não bloquear a instalação — o token chegará via /bitrix/auth pelo BX24.js.
	if accessToken == "" {
		h.log.Warn("partner install: access_token missing — responding 200 to unblock Bitrix install flow",
			zap.String("domain", domain))
		return c.JSON(fiber.Map{"status": "pending_auth"})
	}

	// Normaliza domain: remove https:// e trailing slash para chave consistente
	domain = normalizePortalDomain(domain)

	expiresIn := 3600
	fmt.Sscanf(expiresInStr, "%d", &expiresIn)
	expiresIn = sanitizeExpiresIn(expiresIn)

	portal := &db.BitrixPortal{
		ID:           uuid.New(),
		Domain:       domain,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(expiresIn) * time.Second),
		MemberID:     memberID,
		ConnectorID:  "whatsapp_uc",
		OpenLineID:   0,
	}

	if err := h.repo.UpsertBitrixPortal(c.Context(), portal); err != nil {
		h.log.Error("partner install: upsert portal failed", zap.String("domain", domain), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	// Registra e ativa o imconnector em background — não bloqueia o retorno ao Bitrix
	go func() {
		ctx := context.Background()
		creds := h.portalToCreds(portal)
		appBaseURL := h.cfg.App.BaseURL()
		eventURL := appBaseURL + "/bitrix/connector/event"

		// PLACEMENT_HANDLER = URL da UI de configuração (abre em slider no Bitrix)
		// NÃO é a URL que recebe mensagens do operador
		if err := h.bitrixClient.RegisterConnector(ctx, creds, portal.ConnectorID, "WhatsApp UC", appBaseURL+"/bitrix-connect"); err != nil {
			h.log.Warn("partner install: imconnector.register failed", zap.String("domain", domain), zap.Error(err))
		}
		lineID := portal.OpenLineID
		if lineID == 0 {
			lineID = 1
		}
		// send_message: webhook que o Bitrix chama quando operador responde
		// Funciona independente de INSTALLED:true/false
		if err := h.bitrixClient.SetConnectorData(ctx, creds, portal.ConnectorID, lineID, eventURL); err != nil {
			h.log.Warn("partner install: connector.data.set failed", zap.String("domain", domain), zap.Error(err))
		} else {
			h.log.Info("partner install: send_message webhook set", zap.String("url", eventURL))
		}
		if err := h.bitrixClient.ActivateConnector(ctx, creds, portal.ConnectorID, lineID, true); err != nil {
			h.log.Warn("partner install: imconnector.activate failed", zap.String("domain", domain), zap.Error(err))
		}
		h.log.Info("partner install: connector activated", zap.String("domain", domain))
	}()

	// Redireciona para /bitrix-connect — o Bitrix exibe essa página dentro do iframe
	// imediatamente após a instalação, mostrando ao cliente o fluxo de conexão do WhatsApp.
	return c.Redirect(h.cfg.App.BaseURL()+"/bitrix-connect", fiber.StatusFound)
}

// ─── POST /bitrix/auth ───────────────────────────────────────────────────────
//
// Chamado pela página /bitrix-connect via BX24.getAuth() para salvar/atualizar
// o token do portal no banco. Não requer autenticação (o token em si é a prova).
// Body JSON: { "domain": "empresa.bitrix24.com.br", "access_token": "...", "refresh_token": "..." }
func (h *handlers) bitrixPartnerAuth(c *fiber.Ctx) error {
	var body struct {
		Domain       string `json:"domain"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		MemberID     string `json:"member_id"`
	}
	if err := c.BodyParser(&body); err != nil || body.Domain == "" || body.AccessToken == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "domain e access_token são obrigatórios"})
	}

	domain := normalizePortalDomain(body.Domain)
	expiresIn := body.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}

	// Tenta encontrar registro existente:
	// 1. Por domain (caso já tenha sido migrado antes)
	// 2. Por member_id (caso o install tenha criado o placeholder com member_id como domain)
	// 3. Cria novo registro mínimo se não existir
	existing, err := h.repo.GetBitrixPortalByDomain(c.Context(), domain)
	if err != nil && body.MemberID != "" {
		// Pode ser o placeholder criado no install onde domain = member_id
		existing, err = h.repo.GetBitrixPortalByMemberID(c.Context(), body.MemberID)
		if err == nil && existing.Domain != domain {
			// Migra o placeholder: atualiza o domain para o valor real
			h.log.Info("partner auth: migrating portal domain from placeholder",
				zap.String("old_domain", existing.Domain),
				zap.String("new_domain", domain),
				zap.String("member_id", body.MemberID),
			)
			if migrateErr := h.repo.UpdateBitrixPortalDomain(c.Context(), body.MemberID, domain); migrateErr != nil {
				h.log.Warn("partner auth: migrate domain failed", zap.Error(migrateErr))
			} else {
				existing.Domain = domain
			}
		}
	}
	if err != nil {
		// Nenhum registro encontrado — cria novo
		existing = &db.BitrixPortal{
			ID:          uuid.New(),
			Domain:      domain,
			ConnectorID: "whatsapp_uc",
			OpenLineID:  0,
		}
	}

	existing.AccessToken = body.AccessToken
	if body.RefreshToken != "" {
		existing.RefreshToken = body.RefreshToken
	}
	existing.ExpiresAt = time.Now().Add(time.Duration(sanitizeExpiresIn(expiresIn)) * time.Second)
	if body.MemberID != "" {
		existing.MemberID = body.MemberID
	}

	if err := h.repo.UpsertBitrixPortal(c.Context(), existing); err != nil {
		h.log.Error("partner auth: upsert portal failed", zap.String("domain", domain), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	// Persiste em bitrix_tokens para que o bitrixClient.call() encontre o token
	creds := h.portalToCreds(existing)
	if err := h.bitrixClient.SaveToken(c.Context(), creds, body.AccessToken, existing.RefreshToken, expiresIn); err != nil {
		h.log.Warn("partner auth: save token failed", zap.String("domain", domain), zap.Error(err))
	}

	// Garante que existe um bitrix_account vinculando cada sessão WA ativa a este portal.
	// O ProcessInbound usa bitrix_accounts para saber para qual Bitrix enviar mensagens.
	// Para o Partner App, as credenciais OAuth são as globais do app (client_id/secret da config).
	activeSessions := h.waManager.ListSessions()
	for _, jid := range activeSessions {
		// Verifica se já existe account para este JID
		if _, err := h.repo.GetBitrixAccountByJID(c.Context(), jid); err == nil {
			continue // já existe, não sobrescreve
		}
		lineID := existing.OpenLineID
		if lineID == 0 {
			lineID = 1
		}
		acct := &db.BitrixAccount{
			ID:           generateUUID(),
			SessionJID:   jid,
			Domain:       "https://" + domain,
			ClientID:     h.cfg.Bitrix.ClientID,
			ClientSecret: h.cfg.Bitrix.ClientSecret,
			OpenLineID:   lineID,
			ConnectorID:  existing.ConnectorID,
			RedirectURI:  h.cfg.App.BaseURL() + "/bitrix/callback",
			Status:       db.BitrixAccountActive,
		}
		if err := h.repo.UpsertBitrixAccount(c.Context(), acct); err != nil {
			h.log.Warn("partner auth: auto-create bitrix_account failed",
				zap.String("jid", jid), zap.String("domain", domain), zap.Error(err))
		} else {
			h.log.Info("partner auth: bitrix_account auto-created",
				zap.String("jid", jid), zap.String("domain", domain))
		}
	}

	h.log.Info("partner auth: token updated", zap.String("domain", domain))
	return c.JSON(fiber.Map{"status": "ok", "domain": domain, "sessions": len(activeSessions)})
}

// ─── POST /bitrix/partner/link ───────────────────────────────────────────────
//
// Chamado pelo JS da página /bitrix-connect quando o QR é escaneado.
// Vincula a sessão WA recém-conectada ao portal Bitrix do cliente,
// criando (ou atualizando) um bitrix_account — que é o que o ProcessInbound usa.
// Body JSON: { "domain": "empresa.bitrix24.com", "access_token": "...", "phone": "5519..." }
func (h *handlers) bitrixPartnerLink(c *fiber.Ctx) error {
	var body struct {
		Domain      string `json:"domain"`
		AccessToken string `json:"access_token"`
		Phone       string `json:"phone"` // número ou JID parcial
	}
	if err := c.BodyParser(&body); err != nil || body.Domain == "" || body.Phone == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "domain e phone são obrigatórios"})
	}

	domain := normalizePortalDomain(body.Domain)

	// Busca o portal para obter connector_id e open_line_id
	portal, err := h.repo.GetBitrixPortalByDomain(c.Context(), domain)
	if err != nil {
		h.log.Warn("partner link: portal not found", zap.String("domain", domain), zap.Error(err))
		// Cria portal mínimo para não bloquear
		portal = &db.BitrixPortal{
			Domain:      domain,
			ConnectorID: "whatsapp_uc",
			OpenLineID:  1,
		}
	}

	lineID := portal.OpenLineID
	if lineID == 0 {
		lineID = 1
	}

	// Encontra o JID completo da sessão WA pelo prefixo do número
	phone := strings.TrimPrefix(body.Phone, "+")
	phone = strings.ReplaceAll(phone, " ", "")
	// Remove sufixo @... se vier como JID completo
	if idx := strings.Index(phone, "@"); idx != -1 {
		phone = phone[:idx]
	}
	// Remove device part :XX se vier
	if idx := strings.Index(phone, ":"); idx != -1 {
		phone = phone[:idx]
	}

	sessionJID := ""
	for _, jid := range h.waManager.ListSessions() {
		if strings.HasPrefix(jid, phone) {
			sessionJID = jid
			break
		}
	}
	if sessionJID == "" {
		h.log.Warn("partner link: active session not found for phone", zap.String("phone", phone))
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "sessão WA não encontrada para " + phone})
	}

	acct := &db.BitrixAccount{
		ID:           generateUUID(),
		SessionJID:   sessionJID,
		Domain:       "https://" + domain,
		ClientID:     h.cfg.Bitrix.ClientID,
		ClientSecret: h.cfg.Bitrix.ClientSecret,
		OpenLineID:   lineID,
		ConnectorID:  portal.ConnectorID,
		RedirectURI:  h.cfg.App.BaseURL() + "/bitrix/callback",
		Status:       db.BitrixAccountActive,
	}

	if err := h.repo.UpsertBitrixAccount(c.Context(), acct); err != nil {
		h.log.Error("partner link: upsert bitrix_account failed", zap.String("jid", sessionJID), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	// Salva o token em bitrix_tokens para que o bitrixClient.call() funcione
	if body.AccessToken != "" {
		creds := h.portalToCreds(portal)
		if err := h.bitrixClient.SaveToken(c.Context(), creds, body.AccessToken, portal.RefreshToken, 3600); err != nil {
			h.log.Warn("partner link: save token failed", zap.Error(err))
		}
	}

	// Registra/ativa o connector em background para garantir que está ativo
	go func() {
		ctx := context.Background()
		creds := h.portalToCreds(portal)
		appBase := h.cfg.App.BaseURL()
		eventURL := appBase + "/bitrix/connector/event"
		if err := h.bitrixClient.RegisterConnector(ctx, creds, portal.ConnectorID, "WhatsApp UC", eventURL); err != nil {
			h.log.Warn("partner link: register connector failed", zap.Error(err))
		}
		if err := h.bitrixClient.ActivateConnector(ctx, creds, portal.ConnectorID, lineID, true); err != nil {
			h.log.Warn("partner link: activate connector failed", zap.Error(err))
		}
		if err := h.bitrixClient.BindEvent(ctx, creds, "ONIMCONNECTORMESSAGEADD", eventURL); err != nil {
			h.log.Warn("partner link: bind event failed", zap.Error(err))
		}
		h.log.Info("partner link: connector activated",
			zap.String("jid", sessionJID), zap.String("domain", domain))
	}()

	h.log.Info("partner link: session linked to portal",
		zap.String("jid", sessionJID), zap.String("domain", domain))
	return c.JSON(fiber.Map{"status": "linked", "jid": sessionJID, "domain": domain})
}

// ─── GET /bitrix-connect ─────────────────────────────────────────────────────
//
// Application URL configurado no vendors.bitrix24.com.
// Abre dentro do iframe do Bitrix24 quando o usuário clica no app.
// Salva o token do portal via BX24.js e redireciona para o dashboard completo.
// O dashboard tem: sessões WA, integrações Bitrix24, relatórios e vinculação de canais.
func (h *handlers) bitrixConnectPage(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/html; charset=utf-8")
	// X-Frame-Options não pode ser DENY/SAMEORIGIN — o Bitrix embute em iframe de outro domain
	c.Set("X-Frame-Options", "ALLOWALL")
	return c.SendString(bitrixConnectHTML)
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// normalizePortalDomain remove https://, http:// e trailing slash do domain.
// Mantém a chave consistente no banco independentemente do formato recebido.
func normalizePortalDomain(d string) string {
	d = strings.TrimPrefix(d, "https://")
	d = strings.TrimPrefix(d, "http://")
	d = strings.TrimRight(d, "/")
	return strings.ToLower(d)
}

// sanitizeExpiresIn protege contra valores absurdos de expires_in vindos do Bitrix
// (ou de cálculos errados a partir de tokens já corrompidos no banco). Tokens do
// Bitrix24 vivem 1h; qualquer coisa fora de [1, 86400] é tratada como 3600.
func sanitizeExpiresIn(v int) int {
	if v <= 0 || v > 86400 {
		return 3600
	}
	return v
}

// portalToCreds converte um BitrixPortal em TenantCreds para chamadas ao bitrixClient.
// Usa o APP_BASE_URL da config como RedirectURI (não usado nas chamadas REST, mas obrigatório no struct).
func (h *handlers) portalToCreds(p *db.BitrixPortal) bitrix.TenantCreds {
	return bitrix.TenantCreds{
		Domain:       "https://" + p.Domain,
		ClientID:     h.cfg.Bitrix.ClientID,
		ClientSecret: h.cfg.Bitrix.ClientSecret,
		RedirectURI:  h.cfg.App.BaseURL() + "/bitrix/install",
	}
}

// ─── HTML da página /bitrix-connect ──────────────────────────────────────────
// Página mínima de transição: captura o token do BX24.js, salva no backend
// e redireciona para o dashboard completo. O usuário vê apenas um loading breve.

const bitrixConnectHTML = `<!DOCTYPE html>
<html lang="pt-BR">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>WhatsApp Connector</title>
<script src="//api.bitrix24.com/api/v1/"></script>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;background:#f0f2f5;
     min-height:100vh;display:flex;align-items:center;justify-content:center}
.wrap{text-align:center;padding:32px}
.spinner{width:44px;height:44px;border:4px solid #e2e8f0;border-top-color:#25D366;
         border-radius:50%;animation:spin .8s linear infinite;margin:0 auto 18px}
@keyframes spin{to{transform:rotate(360deg)}}
p{font-size:14px;color:#555}
.err{color:#e53935;font-size:13px;margin-top:12px;display:none}
</style>
</head>
<body>
<div class="wrap">
  <div class="spinner"></div>
  <p id="msg">Conectando ao Bitrix24...</p>
  <p class="err" id="err"></p>
</div>
<script>
function getQueryParam(n){return new URLSearchParams(window.location.search).get(n)||'';}

function salvarERedirecionarAdmin(auth) {
  var domain       = (auth.domain        || '').replace(/^https?:\/\//,'').replace(/\/$/,'');
  var accessToken  = auth.access_token   || '';
  var refreshToken = auth.refresh_token  || '';
  var expiresIn    = auth.expires_in     || 3600;
  var memberID     = auth.member_id      || '';

  if (!domain || !accessToken) {
    document.getElementById('msg').textContent = 'Redirecionando para o painel...';
    window.location.href = '/dashboard';
    return;
  }

  document.getElementById('msg').textContent = 'Autenticando portal ' + domain + '...';

  fetch('/bitrix/auth', {
    method: 'POST',
    headers: {'Content-Type':'application/json'},
    body: JSON.stringify({domain:domain, access_token:accessToken,
                          refresh_token:refreshToken, expires_in:expiresIn, member_id:memberID})
  })
  .then(function(r){return r.json();})
  .then(function(){
    document.getElementById('msg').textContent = 'Abrindo painel...';
    window.location.href = '/dashboard?portal=' + encodeURIComponent(domain);
  })
  .catch(function(){
    // mesmo com erro, redireciona com portal para manter isolamento
    window.location.href = '/dashboard?portal=' + encodeURIComponent(domain);
  });
}

// Timeout: se BX24.init não disparar em 3s, redireciona direto
var t = setTimeout(function(){
  var qsDomain = getQueryParam('DOMAIN') || getQueryParam('domain');
  var qsToken  = getQueryParam('AUTH_ID') || getQueryParam('access_token');
  if (qsDomain && qsToken) {
    salvarERedirecionarAdmin({domain:qsDomain, access_token:qsToken,
      refresh_token:getQueryParam('REFRESH_ID'), member_id:getQueryParam('member_id')});
    return;
  }
  // Sem credenciais — vai para o dashboard sem filtro (admin)
  window.location.href = '/dashboard';
}, 3000);

if (typeof BX24 !== 'undefined') {
  BX24.init(function(){
    clearTimeout(t);
    salvarERedirecionarAdmin(BX24.getAuth());
  });
}
</script>
</body>
</html>`
