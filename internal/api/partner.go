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

		if err := h.bitrixClient.RegisterConnector(ctx, creds, portal.ConnectorID, "WhatsApp UC", appBaseURL); err != nil {
			h.log.Warn("partner install: imconnector.register failed", zap.String("domain", domain), zap.Error(err))
		}
		// open_line_id=0 → ativa com linha padrão 1; o admin pode ajustar depois
		lineID := portal.OpenLineID
		if lineID == 0 {
			lineID = 1
		}
		if err := h.bitrixClient.ActivateConnector(ctx, creds, portal.ConnectorID, lineID, true); err != nil {
			h.log.Warn("partner install: imconnector.activate failed", zap.String("domain", domain), zap.Error(err))
		}
		eventURL := appBaseURL + "/bitrix/connector/event"
		if err := h.bitrixClient.BindEvent(ctx, creds, "ONIMCONNECTORMESSAGEADD", eventURL); err != nil {
			h.log.Warn("partner install: event.bind failed", zap.String("domain", domain), zap.Error(err))
		}
		h.log.Info("partner install: connector activated", zap.String("domain", domain))
	}()

	return c.JSON(fiber.Map{"status": "installed"})
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

	// Tenta atualizar portal existente; se não existir, cria registro mínimo
	existing, err := h.repo.GetBitrixPortalByDomain(c.Context(), domain)
	if err != nil {
		// Porta ainda não registrada (ex: instalação sem install handler)
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
	existing.ExpiresAt = time.Now().Add(time.Duration(expiresIn) * time.Second)
	if body.MemberID != "" {
		existing.MemberID = body.MemberID
	}

	if err := h.repo.UpsertBitrixPortal(c.Context(), existing); err != nil {
		h.log.Error("partner auth: upsert portal failed", zap.String("domain", domain), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	// Também persiste em bitrix_tokens para que o bitrixClient.call() encontre o token
	creds := h.portalToCreds(existing)
	if err := h.bitrixClient.SaveToken(c.Context(), creds, body.AccessToken, existing.RefreshToken, expiresIn); err != nil {
		h.log.Warn("partner auth: save token failed", zap.String("domain", domain), zap.Error(err))
	}

	h.log.Info("partner auth: token updated", zap.String("domain", domain))
	return c.JSON(fiber.Map{"status": "ok"})
}

// ─── GET /bitrix-connect ─────────────────────────────────────────────────────
//
// Application URL configurado no vendors.bitrix24.com.
// Abre dentro do iframe do Bitrix24 quando o usuário clica em "Conectar WhatsApp".
// Usa BX24.js para obter access_token + domain sem depender de query strings.
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

const bitrixConnectHTML = `<!DOCTYPE html>
<html lang="pt-BR">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Conectar WhatsApp</title>
<!-- SDK oficial do Bitrix24 — obrigatório para BX24.init() e BX24.getAuth() -->
<script src="//api.bitrix24.com/api/v1/"></script>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;background:#f0f2f5;min-height:100vh;display:flex;align-items:center;justify-content:center;padding:20px}
.card{background:#fff;border-radius:16px;box-shadow:0 4px 24px rgba(0,0,0,.10);padding:36px 40px;max-width:480px;width:100%;text-align:center}
.wa-icon{width:64px;height:64px;background:#25D366;border-radius:50%;display:flex;align-items:center;justify-content:center;margin:0 auto 18px}
.wa-icon svg{width:34px;height:34px;fill:#fff}
h1{font-size:22px;font-weight:700;color:#111;margin-bottom:6px}
.subtitle{font-size:14px;color:#666;margin-bottom:28px}
.status-bar{display:flex;align-items:center;justify-content:center;gap:8px;padding:10px 18px;border-radius:10px;font-size:14px;font-weight:600;margin-bottom:22px}
.status-bar.loading{background:#f3f4f6;color:#555}
.status-bar.connected{background:#d4edda;color:#155724}
.status-bar.disconnected{background:#fff3cd;color:#856404}
.status-bar.error{background:#f8d7da;color:#721c24}
.dot{width:10px;height:10px;border-radius:50%;flex-shrink:0}
.dot-green{background:#25D366}
.dot-yellow{background:#ffc107}
.dot-gray{background:#aaa}
.dot-red{background:#dc3545}
#qr-section{display:none;margin-bottom:20px}
.qr-instructions{background:#f8f9fa;border-radius:10px;padding:14px 18px;text-align:left;margin-bottom:16px;font-size:13px;color:#555;line-height:1.8}
.qr-instructions b{color:#333}
#qr-wrap{display:inline-block;padding:12px;background:#fff;border:1.5px solid #e5e7eb;border-radius:10px;margin-bottom:8px}
#qr-wrap img{display:block}
.qr-timer{font-size:12px;color:#888;margin-top:6px;min-height:16px}
.btn{display:inline-flex;align-items:center;gap:8px;padding:11px 22px;background:#25D366;color:#fff;border:none;border-radius:8px;font-size:15px;font-weight:600;cursor:pointer;transition:background .2s;text-decoration:none}
.btn:hover{background:#1ebe5d}
.btn:disabled{background:#aaa;cursor:not-allowed}
.btn-outline{background:#fff;color:#25D366;border:2px solid #25D366}
.btn-outline:hover{background:#f0fff4}
.divider{border:none;border-top:1px solid #eee;margin:22px 0}
.phone-label{font-size:12px;color:#888;margin-bottom:4px;text-align:left}
.phone-input-row{display:flex;gap:10px;margin-bottom:20px}
input[type=text]{flex:1;padding:11px 14px;border:1.5px solid #ddd;border-radius:8px;font-size:15px;outline:none;transition:border-color .2s}
input[type=text]:focus{border-color:#25D366}
.info-msg{font-size:13px;color:#555;line-height:1.6;margin-bottom:16px}
.domain-badge{display:inline-block;background:#e8f5e9;color:#2e7d32;padding:4px 12px;border-radius:20px;font-size:12px;font-weight:600;margin-bottom:16px}
</style>
</head>
<body>
<div class="card">
  <div class="wa-icon">
    <svg viewBox="0 0 24 24"><path d="M17.472 14.382c-.297-.149-1.758-.867-2.03-.967-.273-.099-.471-.148-.67.15-.197.297-.767.966-.94 1.164-.173.199-.347.223-.644.075-.297-.15-1.255-.463-2.39-1.475-.883-.788-1.48-1.761-1.653-2.059-.173-.297-.018-.458.13-.606.134-.133.298-.347.446-.52.149-.174.198-.298.298-.497.099-.198.05-.371-.025-.52-.075-.149-.669-1.612-.916-2.207-.242-.579-.487-.5-.669-.51-.173-.008-.371-.01-.57-.01-.198 0-.52.074-.792.372-.272.297-1.04 1.016-1.04 2.479 0 1.462 1.065 2.875 1.213 3.074.149.198 2.096 3.2 5.077 4.487.709.306 1.262.489 1.694.625.712.227 1.36.195 1.871.118.571-.085 1.758-.719 2.006-1.413.248-.694.248-1.289.173-1.413-.074-.124-.272-.198-.57-.347m-5.421 7.403h-.004a9.87 9.87 0 01-5.031-1.378l-.361-.214-3.741.982.998-3.648-.235-.374a9.86 9.86 0 01-1.51-5.26c.001-5.45 4.436-9.884 9.888-9.884 2.64 0 5.122 1.03 6.988 2.898a9.825 9.825 0 012.893 6.994c-.003 5.45-4.437 9.884-9.885 9.884m8.413-18.297A11.815 11.815 0 0012.05 0C5.495 0 .16 5.335.157 11.892c0 2.096.547 4.142 1.588 5.945L.057 24l6.305-1.654a11.882 11.882 0 005.683 1.448h.005c6.554 0 11.89-5.335 11.893-11.893a11.821 11.821 0 00-3.48-8.413z"/></svg>
  </div>
  <h1>Conectar WhatsApp ao Bitrix24</h1>
  <p class="subtitle">Escaneie o QR Code para vincular seu número WhatsApp</p>

  <!-- Badge do portal atual -->
  <div class="domain-badge" id="portal-badge" style="display:none"></div>

  <!-- Barra de status da conexão -->
  <div class="status-bar loading" id="status-bar">
    <div class="dot dot-gray" id="status-dot"></div>
    <span id="status-text">Inicializando...</span>
  </div>

  <!-- Seção QR Code (exibida quando não há sessão ativa) -->
  <div id="qr-section">
    <div class="qr-instructions">
      <b>Como escanear:</b><br>
      1. Abra o WhatsApp no celular<br>
      2. Toque em <b>⋮ → Aparelhos conectados</b><br>
      3. Toque em <b>Conectar um aparelho</b><br>
      4. Aponte a câmera para o QR Code abaixo
    </div>
    <div id="qr-wrap">
      <img id="qr-img" src="" width="240" height="240" style="display:none"/>
      <div id="qr-placeholder" style="width:240px;height:240px;display:flex;align-items:center;justify-content:center;color:#aaa;font-size:13px">
        Aguardando QR...
      </div>
    </div>
    <div class="qr-timer" id="qr-timer"></div>
  </div>

  <!-- Formulário para iniciar nova sessão (exibido quando não há sessão nem QR em andamento) -->
  <div id="new-session-section" style="display:none">
    <p class="info-msg">Nenhum número WhatsApp vinculado a este portal.<br>Informe o número para iniciar a conexão:</p>
    <div class="phone-label">Número com DDD (somente dígitos)</div>
    <div class="phone-input-row">
      <input type="text" id="phone-input" placeholder="5519910001772" maxlength="20"/>
      <button class="btn" id="btn-start" onclick="iniciarSessao()">Conectar</button>
    </div>
  </div>

  <!-- Estado conectado -->
  <div id="connected-section" style="display:none">
    <p class="info-msg" id="connected-info">WhatsApp conectado com sucesso!</p>
    <button class="btn btn-outline" onclick="verificarStatus()">Atualizar status</button>
  </div>
</div>

<script>
// ─── Estado global ────────────────────────────────────────────────────────
var portalDomain = '';
var portalAccessToken = '';
var currentPhone = '';
var qrPollInterval = null;
var qrTimerInterval = null;
var qrCountdown = 0;
var lastQR = '';

// ─── Inicialização via BX24.js ────────────────────────────────────────────
// BX24.init() garante que o SDK está carregado antes de chamar qualquer método.
BX24.init(function() {
  var auth = BX24.getAuth();
  portalDomain   = auth.domain   || '';
  portalAccessToken = auth.access_token || '';

  if (!portalDomain || !portalAccessToken) {
    setStatus('error', 'dot-red', 'Erro: não foi possível obter credenciais do Bitrix24.');
    return;
  }

  // Exibe o portal detectado
  document.getElementById('portal-badge').textContent = '🌐 ' + portalDomain;
  document.getElementById('portal-badge').style.display = 'inline-block';

  // Salva o token no backend e depois verifica o status da sessão WA
  salvarToken(auth, function() {
    verificarStatus();
  });
});

// ─── Salva token no backend ───────────────────────────────────────────────
function salvarToken(auth, callback) {
  fetch('/bitrix/auth', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({
      domain:        auth.domain        || portalDomain,
      access_token:  auth.access_token  || portalAccessToken,
      refresh_token: auth.refresh_token || '',
      expires_in:    auth.expires_in    || 3600,
      member_id:     auth.member_id     || ''
    })
  })
  .then(function(r) { return r.json(); })
  .then(function(d) {
    if (d.status === 'ok' && callback) callback();
  })
  .catch(function(e) {
    console.warn('salvarToken error:', e);
    // Continua mesmo com erro — o token pode já estar salvo de uma sessão anterior
    if (callback) callback();
  });
}

// ─── Verifica status da sessão WA associada ao portal ────────────────────
function verificarStatus() {
  setStatus('loading', 'dot-gray', 'Verificando conexão WhatsApp...');
  hideAll();

  fetch('/ui/sessions')
  .then(function(r) { return r.json(); })
  .then(function(d) {
    if (d.count > 0) {
      // Há sessão ativa — mostra como conectado
      var jid = d.sessions[0];
      var tel = jid.split(':')[0].replace('@s.whatsapp.net','').replace('@lid','');
      setStatus('connected', 'dot-green', 'Conectado: +' + tel);
      document.getElementById('connected-info').textContent =
        'WhatsApp +' + tel + ' vinculado e pronto para uso no Bitrix24.';
      document.getElementById('connected-section').style.display = 'block';
    } else {
      // Sem sessão ativa — oferece opção de conectar
      setStatus('disconnected', 'dot-yellow', 'Nenhum WhatsApp conectado');
      document.getElementById('new-session-section').style.display = 'block';
    }
  })
  .catch(function(e) {
    setStatus('error', 'dot-red', 'Erro ao verificar sessão.');
    console.error(e);
  });
}

// ─── Inicia nova sessão WA ────────────────────────────────────────────────
function iniciarSessao() {
  var raw = document.getElementById('phone-input').value.trim().replace(/\D/g, '');
  if (!raw || raw.length < 10) {
    alert('Digite um número válido com DDD. Ex: 5519910001772');
    return;
  }
  currentPhone = raw;
  var btn = document.getElementById('btn-start');
  btn.disabled = true;
  btn.textContent = 'Conectando...';

  fetch('/ui/sessions', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({phone: raw})
  })
  .then(function(r) { return r.json(); })
  .then(function(d) {
    if (d.error) { alert(d.error); btn.disabled = false; btn.textContent = 'Conectar'; return; }
    // Sessão iniciada — começa polling do QR
    hideAll();
    setStatus('loading', 'dot-gray', 'Aguardando QR Code...');
    document.getElementById('qr-section').style.display = 'block';
    iniciarQRPoll(raw);
  })
  .catch(function(e) { alert('Erro: ' + e); btn.disabled = false; btn.textContent = 'Conectar'; });
}

// ─── Polling do QR Code ───────────────────────────────────────────────────
function iniciarQRPoll(phone) {
  if (qrPollInterval) clearInterval(qrPollInterval);
  fazerQRPoll(phone);
  qrPollInterval = setInterval(function() { fazerQRPoll(phone); }, 2500);
}

function fazerQRPoll(phone) {
  fetch('/ui/sessions/' + phone + '/qr')
  .then(function(r) { return r.json(); })
  .then(function(d) {
    if (d.status === 'connected') {
      pararQRPoll();
      setStatus('connected', 'dot-green', 'Conectado com sucesso!');
      hideAll();
      document.getElementById('connected-info').textContent =
        'WhatsApp vinculado e pronto para uso no Bitrix24.';
      document.getElementById('connected-section').style.display = 'block';
    } else if (d.status === 'ready' && d.qr && d.qr !== lastQR) {
      lastQR = d.qr;
      exibirQR(d.qr);
      setStatus('loading', 'dot-gray', 'Escaneie o QR Code com o WhatsApp');
    } else if (d.status === 'waiting') {
      setStatus('loading', 'dot-gray', 'Aguardando QR Code...');
    }
  }).catch(function() {});
}

function exibirQR(text) {
  var img = document.getElementById('qr-img');
  img.src = 'https://api.qrserver.com/v1/create-qr-code/?size=240x240&ecc=L&data=' + encodeURIComponent(text);
  img.style.display = 'block';
  document.getElementById('qr-placeholder').style.display = 'none';

  qrCountdown = 25;
  if (qrTimerInterval) clearInterval(qrTimerInterval);
  qrTimerInterval = setInterval(function() {
    qrCountdown--;
    var timerEl = document.getElementById('qr-timer');
    if (qrCountdown > 0) {
      timerEl.textContent = 'QR expira em ' + qrCountdown + 's';
    } else {
      clearInterval(qrTimerInterval);
      timerEl.textContent = 'Atualizando QR...';
    }
  }, 1000);
}

function pararQRPoll() {
  if (qrPollInterval)  { clearInterval(qrPollInterval);  qrPollInterval  = null; }
  if (qrTimerInterval) { clearInterval(qrTimerInterval); qrTimerInterval = null; }
}

// ─── Utilitários de UI ────────────────────────────────────────────────────
function setStatus(type, dotClass, text) {
  var bar  = document.getElementById('status-bar');
  var dot  = document.getElementById('status-dot');
  var span = document.getElementById('status-text');
  bar.className  = 'status-bar ' + type;
  dot.className  = 'dot ' + dotClass;
  span.textContent = text;
}

function hideAll() {
  document.getElementById('qr-section').style.display          = 'none';
  document.getElementById('new-session-section').style.display = 'none';
  document.getElementById('connected-section').style.display   = 'none';
}
</script>
</body>
</html>`
