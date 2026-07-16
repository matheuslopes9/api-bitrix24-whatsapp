package api

// Painel super-admin: lista todos os portais Bitrix24 que instalaram o app.
// Acesso por ADMIN_USER/ADMIN_PASSWORD em env var. Cookie de sessão assinado
// com APP_SECRET (HMAC-SHA256, TTL 12h).

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"go.uber.org/zap"
)

// setPartitionedCookie seta cookie via header Set-Cookie cru pra incluir
// atributo `Partitioned` — necessario pra cookies em iframe cross-site
// funcionarem em Chrome 120+ e Safari modernos (CHIPS — Cookies Having
// Independent Partitioned State).
//
// Sem `Partitioned`, browsers modernos bloqueiam o cookie como
// "third-party tracking", mesmo com SameSite=None; Secure. Resultado:
// cookie e' setado pelo backend mas o browser NAO envia em requests
// subsequentes do iframe.
//
// Fiber 2.x nao suporta o atributo nativamente, entao montamos o header
// na mao. fasthttp Append pra nao sobrescrever outros cookies.
func setPartitionedCookie(c *fiber.Ctx, name, value string, expires time.Time, secure bool) {
	parts := []string{
		name + "=" + value,
		"Path=/",
		"Expires=" + expires.UTC().Format("Mon, 02 Jan 2006 15:04:05 GMT"),
		"HttpOnly",
		"SameSite=None",
	}
	if secure {
		parts = append(parts, "Secure")
	}
	parts = append(parts, "Partitioned")
	c.Response().Header.Add("Set-Cookie", strings.Join(parts, "; "))
}

// safePrefix devolve os primeiros n chars de s, ou s inteiro se menor.
// Usado pra logar tokens parcialmente (debug) sem vazar o valor completo.
func safePrefix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

const adminCookieName = "uctalk_admin"
const adminCookieTTL = 12 * time.Hour

// Cookie tenant: assina o domain do portal Bitrix24 do cliente. Setado
// apos /bitrix/auth validar o access_token vindo do BX24.js. Permite que
// chamadas /ui/* subsequentes (do iframe) identifiquem o tenant SEM
// confiar em query string. Inviolavel: HMAC-SHA256(APP_SECRET).
const tenantCookieName = "uctalk_tenant"
const tenantCookieTTL = 12 * time.Hour

// signTenantCookie gera "exp|domain|hmac". domain incluido no MAC pra
// inviabilizar swap entre tenants.
//
// Usa `|` como separador (NAO `.`) porque domains Bitrix tem multiplos
// pontos (b24-xyz.bitrix24.com.br, crm.uctechnology.com.br) e o split
// por ponto quebrava na verificacao quando o domain tinha mais de 1
// segmento apos o sufixo principal.
func signTenantCookie(secret, domain string, expiresAt time.Time) string {
	exp := strconv.FormatInt(expiresAt.Unix(), 10)
	payload := exp + "|" + domain
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return payload + "|" + hex.EncodeToString(mac.Sum(nil))
}

// verifyTenantCookie retorna (domain, ok). Domain so e' confiavel se ok=true.
//
// Aceita tambem o formato legado `exp.domain.hmac` (com pontos) pra
// compatibilidade — mas so' valida via formato novo `|`. Quem ainda tem
// cookie legado precisa refazer o handshake (POST /bitrix/auth).
func verifyTenantCookie(secret, raw string) (string, bool) {
	if raw == "" {
		return "", false
	}
	// Formato novo: exp|domain|hmac
	if strings.Contains(raw, "|") {
		idx1 := strings.Index(raw, "|")
		idx2 := strings.LastIndex(raw, "|")
		if idx1 < 0 || idx2 <= idx1 {
			return "", false
		}
		exp := raw[:idx1]
		domain := raw[idx1+1 : idx2]
		gotMAC := raw[idx2+1:]
		expN, err := strconv.ParseInt(exp, 10, 64)
		if err != nil || time.Now().Unix() > expN {
			return "", false
		}
		payload := exp + "|" + domain
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(payload))
		expected := hex.EncodeToString(mac.Sum(nil))
		if subtle.ConstantTimeCompare([]byte(gotMAC), []byte(expected)) != 1 {
			return "", false
		}
		return domain, true
	}
	// Formato legado (deprecated): exp.domain.hmac — quebra com domain
	// que tem mais de 1 segmento. Retorna false; user precisa refazer
	// handshake pra pegar cookie no formato novo.
	parts := strings.SplitN(raw, ".", 3)
	if len(parts) != 3 {
		return "", false
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", false
	}
	domain := parts[1]
	payload := parts[0] + "." + domain
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	expected := hex.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(parts[2]), []byte(expected)) != 1 {
		return "", false
	}
	return domain, true
}

// signAdminCookie gera "exp.hmac" assinado com HMAC-SHA256(APP_SECRET).
// exp é unix timestamp. Lookup em tempo constante.
func signAdminCookie(secret string, expiresAt time.Time) string {
	exp := strconv.FormatInt(expiresAt.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(exp))
	return exp + "." + hex.EncodeToString(mac.Sum(nil))
}

func verifyAdminCookie(secret, raw string) bool {
	if raw == "" {
		return false
	}
	parts := strings.SplitN(raw, ".", 2)
	if len(parts) != 2 {
		return false
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(parts[0]))
	expected := hex.EncodeToString(mac.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(parts[1]), []byte(expected)) == 1
}

// requireTenantOrAdmin — middleware pra rotas /ui/* que precisam saber
// QUAL tenant esta operando. Aceita 2 caminhos:
//  1. Cookie tenant valido (setado apos /bitrix/auth ok) — domain do cookie
//  2. Cookie admin valido (super-admin acessa de qualquer tenant) — domain
//     extraido de ?domain= ou ?portal= da query (so admin pode confiar nessa query)
// Em ambos, salva o domain validado em c.Locals("tenant_domain") para os
// handlers usarem via resolveDashboardDomain.
func (h *handlers) requireTenantOrAdmin(c *fiber.Ctx) error {
	// 1) Cookie tenant assinado: extrai o domain dele.
	if tenantCookie := c.Cookies(tenantCookieName); tenantCookie != "" {
		if domain, ok := verifyTenantCookie(h.cfg.App.Secret, tenantCookie); ok {
			c.Locals("tenant_domain", domain)
			c.Locals("auth_source", "tenant")
			return c.Next()
		}
	}
	// 2) Cookie admin (super-admin): aceita ?domain= ou ?portal= da query.
	if adminCookie := c.Cookies(adminCookieName); adminCookie != "" {
		if verifyAdminCookie(h.cfg.App.Secret, adminCookie) {
			queryDomain := strings.TrimSpace(c.Query("domain"))
			if queryDomain == "" {
				queryDomain = strings.TrimSpace(c.Query("portal"))
			}
			c.Locals("tenant_domain", normalizePortalDomain(queryDomain))
			c.Locals("auth_source", "admin")
			return c.Next()
		}
	}
	return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
		"error": "unauthorized",
		"hint":  "Abra o app pelo Bitrix24 (instalado) ou faca login em /admin",
	})
}

// requireAdminAuth — middleware: verifica cookie.
//   - APIs (paths que contém /api/ ou /run, ou método != GET) retornam 401 JSON
//   - Páginas HTML retornam redirect 302 para /admin/login (UX de browser)
func (h *handlers) requireAdminAuth(c *fiber.Ctx) error {
	if h.cfg.App.AdminUser == "" || h.cfg.App.AdminPassword == "" {
		return c.Status(503).SendString("admin desabilitado: defina ADMIN_USER e ADMIN_PASSWORD no .env")
	}
	cookie := c.Cookies(adminCookieName)
	if verifyAdminCookie(h.cfg.App.Secret, cookie) {
		return c.Next()
	}
	path := c.Path()
	isAPI := strings.Contains(path, "/api/") || strings.HasSuffix(path, "/run") || strings.HasSuffix(path, "/connectors") || c.Method() != fiber.MethodGet
	if isAPI {
		return c.Status(401).JSON(fiber.Map{"error": "unauthorized"})
	}
	return c.Redirect("/admin/login", fiber.StatusFound)
}

// GET /admin/login — formulário de login.
func (h *handlers) adminLoginPage(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/html; charset=utf-8")
	errMsg := c.Query("err")
	errBlock := ""
	if errMsg != "" {
		errBlock = `<div class="err">` + escapeHTML(errMsg) + `</div>`
	}
	return c.SendString(strings.ReplaceAll(adminLoginHTML, "<!--ERR-->", errBlock))
}

// POST /admin/login — valida credenciais, seta cookie.
func (h *handlers) adminLoginSubmit(c *fiber.Ctx) error {
	user := c.FormValue("user")
	pass := c.FormValue("password")
	if h.cfg.App.AdminUser == "" || h.cfg.App.AdminPassword == "" {
		return c.Status(503).SendString("admin desabilitado")
	}
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(h.cfg.App.AdminUser)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(h.cfg.App.AdminPassword)) == 1
	if !userOK || !passOK {
		return c.Redirect("/admin/login?err=Credenciais+inv%C3%A1lidas", fiber.StatusFound)
	}
	expires := time.Now().Add(adminCookieTTL)
	c.Cookie(&fiber.Cookie{
		Name:     adminCookieName,
		Value:    signAdminCookie(h.cfg.App.Secret, expires),
		Path:     "/",
		Expires:  expires,
		HTTPOnly: true,
		Secure:   strings.HasPrefix(h.cfg.App.PublicURL, "https://"),
		SameSite: "Lax",
	})
	return c.Redirect("/admin", fiber.StatusFound)
}

// POST /admin/logout — limpa cookie.
func (h *handlers) adminLogout(c *fiber.Ctx) error {
	c.Cookie(&fiber.Cookie{
		Name:    adminCookieName,
		Value:   "",
		Path:    "/",
		Expires: time.Unix(0, 0),
	})
	return c.Redirect("/admin/login", fiber.StatusFound)
}

// GET /admin — página principal com cards de cada portal.
func (h *handlers) adminHome(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/html; charset=utf-8")
	return c.SendString(adminHomeHTML)
}

// GET /admin/api/tenants — dados JSON para popular os cards.
// Agrega por portal Bitrix (bitrix_portals): conexões WA (QR/Cloud), msgs 24h/1h,
// status do token OAuth. Usa queries agregadas (GROUP BY) — 3 queries no total,
// independente da quantidade de portais. Escala para 1000+ tenants.
func (h *handlers) adminListTenants(c *fiber.Ctx) error {
	ctx := c.Context()
	portals, err := h.repo.ListBitrixPortals(ctx)
	if err != nil {
		h.log.Error("admin: ListBitrixPortals failed", zap.Error(err))
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	now := time.Now()

	// 4 queries agregadas — uma vez só, independente de N portais.
	sessionsByDomain, err := h.repo.AllDomainSessionCounts(ctx)
	if err != nil {
		h.log.Error("admin: AllDomainSessionCounts failed", zap.Error(err))
		return c.Status(500).JSON(fiber.Map{"error": "session counts: " + err.Error()})
	}
	msgs24hByDomain, err := h.repo.AllDomainMessageCounts(ctx, now.Add(-24*time.Hour))
	if err != nil {
		h.log.Error("admin: AllDomainMessageCounts(24h) failed", zap.Error(err))
		return c.Status(500).JSON(fiber.Map{"error": "msg counts 24h: " + err.Error()})
	}
	msgs1hByDomain, err := h.repo.AllDomainMessageCounts(ctx, now.Add(-time.Hour))
	if err != nil {
		h.log.Error("admin: AllDomainMessageCounts(1h) failed", zap.Error(err))
		return c.Status(500).JSON(fiber.Map{"error": "msg counts 1h: " + err.Error()})
	}
	// expiry dos tokens VIVOS (bitrix_tokens é atualizado a cada refresh,
	// diferente de bitrix_portals.expires_at que so reflete o install inicial)
	tokenExpByDomain, err := h.repo.AllDomainTokenExpiry(ctx)
	if err != nil {
		h.log.Error("admin: AllDomainTokenExpiry failed", zap.Error(err))
		return c.Status(500).JSON(fiber.Map{"error": "token expiry: " + err.Error()})
	}
	// Planos por dominio: 1 query agregada pra evitar N+1 no loop.
	allPlans, _ := h.repo.ListTenantPlans(ctx)
	plansByDomain := map[string]*db.TenantPlan{}
	for _, pl := range allPlans {
		plansByDomain[normalizeDomainKey(pl.Domain)] = pl
	}

	type tenantCard struct {
		ID           string    `json:"id"`
		Domain       string    `json:"domain"`
		MemberID     string    `json:"member_id"`
		InstalledAt  time.Time `json:"installed_at"`
		UpdatedAt    time.Time `json:"updated_at"`
		TokenExpAt   time.Time `json:"token_expires_at"`
		TokenStatus  string    `json:"token_status"` // valid | expiring | expired
		OpenLineID   int       `json:"open_line_id"`
		ConnQR       int       `json:"connections_qr"`
		ConnCloud    int       `json:"connections_cloud"`
		Msgs24h      int       `json:"msgs_24h"`
		Msgs1h       int       `json:"msgs_1h"`
		MsgsInbound  int       `json:"msgs_inbound_24h"`
		MsgsOutbound int       `json:"msgs_outbound_24h"`
		// Plano (basic|pro) / status (trial|active|expired|suspended)
		Plan              string     `json:"plan"`
		PlanStatus        string     `json:"plan_status"`
		TrialEndsAt       *time.Time `json:"trial_ends_at,omitempty"`
		ActiveUntil       *time.Time `json:"active_until,omitempty"`
		TrialDaysRemain   int        `json:"trial_days_remaining"`
		PlanIsAccessOK    bool       `json:"plan_access_allowed"`
		PlanIsPro         bool       `json:"plan_has_pro_features"`
		PlanNotes         string     `json:"plan_notes,omitempty"`
	}

	cards := make([]tenantCard, 0, len(portals))
	for _, p := range portals {
		// Esconde placeholders: quando o app é instalado via Marketplace, o
		// install handler cria uma row com domain = member_id (porque o domain
		// real só chega depois pelo BX24.getAuth no iframe). Se o cliente
		// abriu o iframe, criamos uma segunda row com o domain certo —
		// e a row placeholder vira lixo. Filtramos aqui para nao poluir o painel.
		if p.Domain == p.MemberID {
			continue
		}
		card := tenantCard{
			ID:          p.ID.String(),
			Domain:      p.Domain,
			MemberID:    p.MemberID,
			InstalledAt: p.InstalledAt,
			UpdatedAt:   p.UpdatedAt,
			OpenLineID:  p.OpenLineID,
		}
		// Key normalizada para lookup nos maps agregados (que usam mesma
		// normalizacao: strip https:// / http:// / www., lowercase).
		key := normalizeDomainKey(p.Domain)

		// Status do token: usa o expires_at do bitrix_tokens (atualizado a cada
		// refresh, TTL ~1h do access). Se nao tem token na tabela, marca expirado.
		// access_token vence em 1h, mas o refresh_token (TTL 30d) renova auto.
		// Entao so consideramos "expired" se passou de 30 dias sem renovar — sinal
		// de que o refresh tambem morreu e precisa reinstalar.
		if exp, ok := tokenExpByDomain[key]; ok {
			card.TokenExpAt = exp
			refreshTokenLimit := exp.Add(30 * 24 * time.Hour) // exp do access + 30d do refresh
			switch {
			case now.After(refreshTokenLimit):
				card.TokenStatus = "expired"
			case now.After(refreshTokenLimit.Add(-3 * 24 * time.Hour)):
				card.TokenStatus = "expiring"
			default:
				card.TokenStatus = "valid"
			}
		} else {
			// Sem token salvo — provavelmente nunca foi feito refresh ou install
			// nao completou. Mostra como problema.
			card.TokenExpAt = p.ExpiresAt
			card.TokenStatus = "expired"
		}
		if s, ok := sessionsByDomain[key]; ok {
			card.ConnQR = s.QR
			card.ConnCloud = s.Cloud
		}
		if m, ok := msgs24hByDomain[key]; ok {
			card.MsgsInbound = m.Inbound
			card.MsgsOutbound = m.Outbound
			card.Msgs24h = m.Inbound + m.Outbound
		}
		if m, ok := msgs1hByDomain[key]; ok {
			card.Msgs1h = m.Inbound + m.Outbound
		}
		// Plano do tenant — pra UI mostrar status e botoes de acao.
		if pl, ok := plansByDomain[key]; ok {
			card.Plan = pl.Plan
			card.PlanStatus = pl.Status
			card.TrialEndsAt = pl.TrialEndsAt
			card.ActiveUntil = pl.ActiveUntil
			card.PlanIsAccessOK = pl.IsAccessAllowed()
			card.PlanIsPro = pl.HasProFeatures()
			card.PlanNotes = pl.Notes
			if pl.TrialEndsAt != nil {
				days := int(pl.TrialEndsAt.Sub(now).Hours() / 24)
				if days < 0 {
					days = 0
				}
				card.TrialDaysRemain = days
			}
		} else {
			// Tenant sem plano cadastrado (legacy ou nao instalou via /bitrix/auth).
			// Mostra como "no_plan" pra UI poder oferecer "Criar trial agora".
			card.Plan = "none"
			card.PlanStatus = "no_plan"
		}
		cards = append(cards, card)
	}

	return c.JSON(fiber.Map{
		"tenants":      cards,
		"total":        len(cards),
		"generated_at": now.Format(time.RFC3339),
	})
}

// GET /admin/api/debug — dump diagnóstico das tabelas-chave.
// Conta linhas em cada tabela + retorna primeiros N registros (sem dados sensíveis).
// Útil para diagnosticar quando o painel mostra zeros e a gente não sabe onde está
// quebrando.
func (h *handlers) adminDebug(c *fiber.Ctx) error {
	ctx := c.Context()
	pool := h.repo.Pool()
	out := fiber.Map{}

	// Contagens
	counts := fiber.Map{}
	for _, tbl := range []string{"bitrix_portals", "bitrix_accounts", "bitrix_tokens", "whatsapp_sessions", "messages"} {
		var n int64
		if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM "+tbl).Scan(&n); err != nil {
			counts[tbl] = "err: " + err.Error()
		} else {
			counts[tbl] = n
		}
	}
	out["counts"] = counts

	// Sample bitrix_portals
	rows, err := pool.Query(ctx, `SELECT domain, member_id, open_line_id, expires_at, installed_at FROM bitrix_portals ORDER BY installed_at DESC LIMIT 10`)
	if err == nil {
		var portals []fiber.Map
		for rows.Next() {
			var domain, memberID string
			var openLine int
			var exp, installedAt time.Time
			rows.Scan(&domain, &memberID, &openLine, &exp, &installedAt)
			portals = append(portals, fiber.Map{
				"domain":       domain,
				"member_id":    memberID,
				"open_line_id": openLine,
				"expires_at":   exp,
				"installed_at": installedAt,
			})
		}
		rows.Close()
		out["bitrix_portals_sample"] = portals
	}

	// Sample bitrix_accounts
	rows, err = pool.Query(ctx, `SELECT session_jid, domain, open_line_id, connector_id, status FROM bitrix_accounts ORDER BY created_at DESC LIMIT 10`)
	if err == nil {
		var accts []fiber.Map
		for rows.Next() {
			var sessionJID, domain, connID, status string
			var openLine int
			rows.Scan(&sessionJID, &domain, &openLine, &connID, &status)
			accts = append(accts, fiber.Map{
				"session_jid":  sessionJID,
				"domain":       domain,
				"open_line_id": openLine,
				"connector_id": connID,
				"status":       status,
			})
		}
		rows.Close()
		out["bitrix_accounts_sample"] = accts
	}

	// Sample whatsapp_sessions
	rows, err = pool.Query(ctx, `SELECT jid, phone, status, type FROM whatsapp_sessions ORDER BY created_at DESC LIMIT 10`)
	if err == nil {
		var sess []fiber.Map
		for rows.Next() {
			var jid, phone, status, typ string
			rows.Scan(&jid, &phone, &status, &typ)
			sess = append(sess, fiber.Map{"jid": jid, "phone": phone, "status": status, "type": typ})
		}
		rows.Close()
		out["whatsapp_sessions_sample"] = sess
	}

	// Domains únicos em bitrix_accounts (pra comparar com bitrix_portals.domain)
	rows, err = pool.Query(ctx, `SELECT DISTINCT domain FROM bitrix_accounts`)
	if err == nil {
		var domains []string
		for rows.Next() {
			var d string
			rows.Scan(&d)
			domains = append(domains, d)
		}
		rows.Close()
		out["bitrix_accounts_distinct_domains"] = domains
	}

	// Teste do JOIN normalizado: quantas linhas casam?
	var joinCount int64
	pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM bitrix_accounts ba
		JOIN whatsapp_sessions s
		  ON REGEXP_REPLACE(s.jid, ':[0-9]+@', '@') = REGEXP_REPLACE(ba.session_jid, ':[0-9]+@', '@')
	`).Scan(&joinCount)
	out["join_test_accounts_x_sessions"] = joinCount

	// Diagnostico: phones distintos nas sessoes (pra detectar valor estranho como "cloud")
	rows, err = pool.Query(ctx, `SELECT DISTINCT phone FROM whatsapp_sessions WHERE phone IS NOT NULL`)
	if err == nil {
		var phones []string
		for rows.Next() {
			var p string
			rows.Scan(&p)
			phones = append(phones, p)
		}
		rows.Close()
		out["whatsapp_sessions_distinct_phones"] = phones
	}

	// Diagnostico: from_jid distintos das msgs outbound recentes
	rows, err = pool.Query(ctx, `
		SELECT from_jid, COUNT(*) AS cnt
		FROM messages
		WHERE direction = 'outbound'
		  AND created_at >= NOW() - INTERVAL '7 days'
		GROUP BY from_jid
		ORDER BY cnt DESC
		LIMIT 10`)
	if err == nil {
		var fjs []fiber.Map
		for rows.Next() {
			var fj string
			var cnt int64
			rows.Scan(&fj, &cnt)
			fjs = append(fjs, fiber.Map{"from_jid": fj, "count": cnt})
		}
		rows.Close()
		out["messages_distinct_from_jid_7d"] = fjs
	}

	// Diagnostico: 5 amostras de mensagens com from_jid ou to_jid problematico
	rows, err = pool.Query(ctx, `
		SELECT direction, from_jid, to_jid, created_at
		FROM messages
		WHERE from_jid IS NULL OR from_jid = '' OR to_jid IS NULL OR to_jid = ''
		   OR from_jid NOT LIKE '%@%' OR to_jid NOT LIKE '%@%'
		LIMIT 10`)
	if err == nil {
		var weird []fiber.Map
		for rows.Next() {
			var dir, fromJID, toJID string
			var createdAt time.Time
			rows.Scan(&dir, &fromJID, &toJID, &createdAt)
			weird = append(weird, fiber.Map{
				"direction":  dir,
				"from_jid":   fromJID,
				"to_jid":     toJID,
				"created_at": createdAt,
			})
		}
		rows.Close()
		out["messages_with_weird_jid"] = weird
	}

	return c.JSON(out)
}

// POST /admin/api/cleanup/banned-sessions — remove whatsapp_sessions com status='banned'.
func (h *handlers) adminCleanupBannedSessions(c *fiber.Ctx) error {
	n, err := h.repo.DeleteBannedSessions(c.Context())
	if err != nil {
		h.log.Error("admin: delete banned sessions failed", zap.Error(err))
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.log.Info("admin: deleted banned sessions", zap.Int64("count", n))
	return c.JSON(fiber.Map{"deleted": n})
}

// POST /admin/api/tenant/cleanup/legacy-messages?domain=... — apaga msgs
// "lixo" (from_jid/to_jid vazio ou cloud@s.whatsapp.net) SO daquele tenant.
// Body opcional ignorado.
func (h *handlers) adminTenantCleanupLegacyMessages(c *fiber.Ctx) error {
	domain := strings.TrimSpace(c.Query("domain"))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain query param obrigatorio"})
	}
	key := normalizeDomainKey(domain)
	n, err := h.repo.DeleteLegacyMessagesByDomain(c.Context(), key)
	if err != nil {
		h.log.Error("admin: tenant cleanup legacy msgs failed",
			zap.String("domain", domain), zap.Error(err))
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.log.Info("admin: tenant legacy msgs deleted",
		zap.String("domain", domain), zap.Int64("count", n))
	return c.JSON(fiber.Map{"deleted": n, "domain": domain})
}

// POST /admin/api/tenant/cleanup/session-files?domain=... — apaga arquivos
// .db/.db-shm/.db-wal dos telefones QR vinculados a esse tenant.
// USAR COM CUIDADO: forca reescaneamento de QR desse cliente.
func (h *handlers) adminTenantCleanupSessionFiles(c *fiber.Ctx) error {
	if h.waManager == nil {
		return c.Status(503).JSON(fiber.Map{"error": "WhatsApp Manager nao inicializado"})
	}
	domain := strings.TrimSpace(c.Query("domain"))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain query param obrigatorio"})
	}
	key := normalizeDomainKey(domain)
	phones, err := h.repo.ListPhonesByDomain(c.Context(), key)
	if err != nil {
		h.log.Error("admin: list phones by domain failed",
			zap.String("domain", domain), zap.Error(err))
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	if len(phones) == 0 {
		return c.JSON(fiber.Map{
			"removed": []string{}, "count": 0, "bytes_freed": 0,
			"note": "Nenhum telefone QR encontrado para esse tenant",
		})
	}
	removed, bytes, err := h.waManager.CleanupSessionFilesForPhones(phones)
	if err != nil {
		h.log.Error("admin: tenant cleanup session files failed",
			zap.String("domain", domain), zap.Error(err))
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.log.Info("admin: tenant session files cleaned",
		zap.String("domain", domain),
		zap.Strings("phones", phones),
		zap.Int("removed", len(removed)),
		zap.Int64("bytes_freed", bytes))
	return c.JSON(fiber.Map{
		"phones":      phones,
		"removed":     removed,
		"count":       len(removed),
		"bytes_freed": bytes,
	})
}

// POST /admin/api/cleanup/legacy-messages — apaga msgs com from_jid/to_jid
// vazio ou com o valor antigo 'cloud@s.whatsapp.net' (bug ja corrigido,
// mas o lixo historico ainda polui relatorios).
func (h *handlers) adminCleanupLegacyMessages(c *fiber.Ctx) error {
	n, err := h.repo.DeleteLegacyMessages(c.Context())
	if err != nil {
		h.log.Error("admin: delete legacy messages failed", zap.Error(err))
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.log.Info("admin: deleted legacy messages", zap.Int64("count", n))
	return c.JSON(fiber.Map{"deleted": n})
}

// POST /admin/api/cleanup/session-files — remove .db/.db-shm/.db-wal orfaos
// do diretorio de sessoes (Cloud que nao deveria ter arquivo + sessoes
// nao-active + sidecars sem .db principal).
func (h *handlers) adminCleanupSessionFiles(c *fiber.Ctx) error {
	if h.waManager == nil {
		return c.Status(503).JSON(fiber.Map{"error": "WhatsApp Manager nao inicializado"})
	}
	removed, bytes, err := h.waManager.CleanupOrphanSessionFiles(c.Context())
	if err != nil {
		h.log.Error("admin: cleanup session files failed", zap.Error(err))
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.log.Info("admin: session files cleaned",
		zap.Int("removed", len(removed)), zap.Int64("bytes_freed", bytes))
	return c.JSON(fiber.Map{
		"removed":     removed,
		"count":       len(removed),
		"bytes_freed": bytes,
	})
}

// POST /admin/api/cleanup/placeholder-portals — remove bitrix_portals com domain=member_id.
func (h *handlers) adminCleanupPlaceholders(c *fiber.Ctx) error {
	n, err := h.repo.DeletePlaceholderPortals(c.Context())
	if err != nil {
		h.log.Error("admin: delete placeholder portals failed", zap.Error(err))
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.log.Info("admin: deleted placeholder portals", zap.Int64("count", n))
	return c.JSON(fiber.Map{"deleted": n})
}

// POST /admin/api/queue/flush — limpa filas do Redis.
// Body JSON: {"kinds":["inbound","outbound","dead"]}. Default: limpa só outbound
// (inbound real e dead-letter ficam preservados a menos que pedidos).
func (h *handlers) adminFlushQueue(c *fiber.Ctx) error {
	var req struct {
		Kinds []string `json:"kinds"`
	}
	_ = c.BodyParser(&req)
	if len(req.Kinds) == 0 {
		req.Kinds = []string{"outbound"}
	}
	removed, err := h.q.Flush(c.Context(), req.Kinds)
	if err != nil {
		h.log.Error("admin: queue flush failed", zap.Error(err))
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.log.Info("admin: queue flushed", zap.Any("removed", removed))
	return c.JSON(fiber.Map{"removed": removed})
}

// ─── CRM Permissions ─────────────────────────────────────────────────────

// GET /admin/api/tenant/users?domain=... — lista usuarios LIBERADOS desse tenant.
// Modelo novo (post-migration 018): cada user pode ter varias linhas (uma por
// session_jid liberado). Aqui dedupe: mostra cada user uma vez, com a contagem
// de numeros liberados.
func (h *handlers) adminTenantListUsers(c *fiber.Ctx) error {
	domain := strings.TrimSpace(c.Query("domain"))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain query param obrigatorio"})
	}
	ctx := c.Context()
	key := normalizeDomainKey(domain)
	perms, err := h.repo.ListCrmPermissionsByDomain(ctx, key)
	if err != nil {
		h.log.Error("admin: ListCrmPermissions failed", zap.Error(err))
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	type userOut struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		HasAccess     bool   `json:"has_access"`
		GrantedAt     string `json:"granted_at,omitempty"`
		SessionsCount int    `json:"sessions_count"`
	}
	byID := map[string]*userOut{}
	for _, p := range perms {
		u, ok := byID[p.UserID]
		if !ok {
			u = &userOut{
				ID:        p.UserID,
				Name:      p.UserName,
				HasAccess: true,
				GrantedAt: p.GrantedAt.Format("2006-01-02 15:04"),
			}
			byID[p.UserID] = u
		}
		u.SessionsCount++
	}
	out := make([]*userOut, 0, len(byID))
	for _, u := range byID {
		out = append(out, u)
	}
	return c.JSON(fiber.Map{"users": out, "granted": len(out)})
}

// GET /admin/api/tenant/user-info?domain=...&user_id=...
// Busca info de UM user no Bitrix via im.user.list.get (scope `im`).
// Usado pelo modal admin antes de liberar (preview do nome).
func (h *handlers) adminTenantUserInfo(c *fiber.Ctx) error {
	domain := strings.TrimSpace(c.Query("domain"))
	userID := strings.TrimSpace(c.Query("user_id"))
	if domain == "" || userID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain e user_id obrigatorios"})
	}
	ctx := c.Context()
	portal, err := h.repo.GetBitrixPortalByDomain(ctx, normalizePortalDomain(domain))
	if err != nil || portal == nil {
		return c.Status(404).JSON(fiber.Map{"error": "portal nao encontrado"})
	}
	creds := h.portalToCreds(portal)
	users, err := h.bitrixClient.GetUserByIDs(ctx, creds, []string{userID})
	if err != nil {
		h.log.Error("admin: GetUserByIDs failed",
			zap.String("domain", domain), zap.String("user_id", userID), zap.Error(err))
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	if len(users) == 0 {
		return c.Status(404).JSON(fiber.Map{"error": "usuario " + userID + " nao encontrado no portal"})
	}
	u := users[0]
	fullName := strings.TrimSpace(u.Name + " " + u.LastName)
	if fullName == "" {
		fullName = "User #" + u.ID
	}
	return c.JSON(fiber.Map{
		"id":        u.ID,
		"name":      fullName,
		"first":     u.Name,
		"last":      u.LastName,
		"email":     u.Email,
		"position":  u.Position,
		"active":    u.Active,
	})
}

// POST /admin/api/tenant/permissions?domain=...
// Body JSON: {"user_id": "123", "user_name": "Joao", "action": "grant|revoke"}
func (h *handlers) adminTenantSetPermission(c *fiber.Ctx) error {
	domain := strings.TrimSpace(c.Query("domain"))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain query param obrigatorio"})
	}
	var req struct {
		UserID     string `json:"user_id"`
		UserName   string `json:"user_name"`
		Action     string `json:"action"`
		SessionJID string `json:"session_jid"` // vazio = wildcard (todas as sessoes)
	}
	if err := c.BodyParser(&req); err != nil || req.UserID == "" || req.Action == "" {
		return c.Status(400).JSON(fiber.Map{"error": "user_id e action sao obrigatorios"})
	}
	key := normalizeDomainKey(domain)
	switch req.Action {
	case "grant":
		if err := h.repo.GrantSessionPermission(c.Context(), key, req.UserID, req.UserName, req.SessionJID, "super-admin"); err != nil {
			h.log.Error("admin: GrantSessionPermission failed", zap.Error(err))
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		h.log.Info("admin: granted session access",
			zap.String("domain", key), zap.String("user_id", req.UserID),
			zap.String("session_jid", req.SessionJID))
		return c.JSON(fiber.Map{"ok": true, "action": "granted"})
	case "revoke":
		removed, err := h.repo.RevokeSessionPermission(c.Context(), key, req.UserID, req.SessionJID)
		if err != nil {
			h.log.Error("admin: RevokeSessionPermission failed", zap.Error(err))
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		h.log.Info("admin: revoked session access",
			zap.String("domain", key), zap.String("user_id", req.UserID),
			zap.String("session_jid", req.SessionJID),
			zap.Bool("removed", removed))
		return c.JSON(fiber.Map{"ok": true, "action": "revoked", "removed": removed})
	default:
		return c.Status(400).JSON(fiber.Map{"error": "action deve ser 'grant' ou 'revoke'"})
	}
}

// GET /admin/api/tenant/master?domain=... — status do master + lista de
// internos ativos + sessoes do tenant. Tudo num so' shot pra UI nao
// precisar de varias chamadas.
func (h *handlers) adminTenantMasterStatus(c *fiber.Ctx) error {
	domain := strings.TrimSpace(c.Query("domain"))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain obrigatorio"})
	}
	ctx := c.Context()
	key := normalizeDomainKey(domain)

	portal, err := h.repo.GetBitrixPortalByDomain(ctx, key)
	if err != nil || portal == nil {
		return c.Status(404).JSON(fiber.Map{"error": "portal nao encontrado"})
	}
	creds := h.portalToCreds(portal)

	// Internos ativos do Bitrix (mesmo helper do CRM tab)
	users, err := h.bitrixClient.ListAllUsers(ctx, creds, 1000)
	if err != nil {
		h.log.Warn("admin master status: ListAllUsers failed", zap.Error(err))
		users = nil
	}
	usersOut := make([]fiber.Map, 0, len(users))
	var masterName string
	for _, u := range users {
		full := strings.TrimSpace(u.Name + " " + u.LastName)
		if full == "" {
			full = "User #" + u.ID
		}
		if u.ID == portal.LegacyAdminUserID {
			masterName = full
		}
		usersOut = append(usersOut, fiber.Map{
			"id":    u.ID,
			"name":  full,
			"email": u.Email,
		})
	}

	// Sessoes do tenant (ativas + desconectadas, mesmo do /dashboard)
	sessions, _ := h.repo.ListSessionsByDomain(ctx, key)
	sessOut := make([]fiber.Map, 0, len(sessions))
	for _, s := range sessions {
		phone := s.Phone
		if phone == "" {
			phone = s.CloudDisplayPhone
		}
		sessOut = append(sessOut, fiber.Map{
			"jid":    s.JID,
			"phone":  phone,
			"type":   s.Type,
			"status": s.Status,
		})
	}

	// Permissoes ja' liberadas (user x session_jid)
	perms, _ := h.repo.ListCrmPermissionsByDomain(ctx, key)
	byUser := map[string][]string{}
	for _, p := range perms {
		byUser[p.UserID] = append(byUser[p.UserID], p.SessionJID)
	}
	// Anexa allowed_sessions a cada user
	for i, u := range usersOut {
		sess := byUser[u["id"].(string)]
		if sess == nil {
			sess = []string{}
		}
		usersOut[i]["allowed_sessions"] = sess
	}

	return c.JSON(fiber.Map{
		"domain":           portal.Domain,
		"master_user_id":   portal.LegacyAdminUserID,
		"master_user_name": masterName,
		"configured":       portal.LegacyAdminUserID != "",
		"users":            usersOut,
		"sessions":         sessOut,
	})
}

// POST /admin/api/tenant/master?domain=... — body {user_id, user_name}.
// Super-admin: bypass total do guard de master. Pode setar/trocar
// qualquer um (atende caso de cliente perdeu acesso ao master antigo).
func (h *handlers) adminTenantSetMaster(c *fiber.Ctx) error {
	domain := strings.TrimSpace(c.Query("domain"))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain obrigatorio"})
	}
	var req struct {
		UserID   string `json:"user_id"`
		UserName string `json:"user_name"`
	}
	if err := c.BodyParser(&req); err != nil || req.UserID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "user_id obrigatorio"})
	}
	ctx := c.Context()
	key := normalizeDomainKey(domain)

	if err := h.repo.SetMasterUserForce(ctx, key, req.UserID, req.UserName); err != nil {
		h.log.Error("admin: SetMasterUserForce failed", zap.Error(err))
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.log.Info("admin: master set",
		zap.String("domain", key), zap.String("user_id", req.UserID))
	return c.JSON(fiber.Map{"ok": true, "master_user_id": req.UserID, "master_user_name": req.UserName})
}

// normalizeDomainKey deixa o domain no mesmo formato usado pelas queries
// agregadas (que aplicam LOWER + REGEXP_REPLACE para strip protocolo/www).
// Importante: bitrix_portals.domain e bitrix_accounts.domain podem ter
// formatos diferentes em prod (com ou sem "https://"). Sem normalizar,
// o lookup nos maps falha e o card mostra zero.
func normalizeDomainKey(d string) string {
	s := strings.ToLower(strings.TrimSpace(d))
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "www.")
	s = strings.TrimSuffix(s, "/")
	return s
}

// escapeHTML simples para mensagem de erro.
func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}

// ─── SMS Provider — debug + retry manual (super-admin) ───────────────────

// GET /admin/api/tenant/sms-debug?domain=... — mostra o que o Bitrix tem
// registrado como SMS senders DESTE app no portal. Diagnostica scope
// faltando, registro nao persistido, etc.
func (h *handlers) adminTenantSMSDebug(c *fiber.Ctx) error {
	domain := strings.TrimSpace(c.Query("domain"))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain obrigatorio"})
	}
	ctx := c.Context()
	portal, err := h.repo.GetBitrixPortalByDomain(ctx, normalizeDomainKey(domain))
	if err != nil || portal == nil {
		return c.Status(404).JSON(fiber.Map{"error": "portal nao encontrado"})
	}
	creds := h.portalToCreds(portal)
	raw, listErr := h.bitrixClient.ListSMSSenders(ctx, creds)
	out := fiber.Map{
		"domain":              portal.Domain,
		"default_session_jid": portal.DefaultSMSSessionJID,
		"expected_sender_code": SMSSenderCode,
		"expected_handler_url": h.cfg.App.BaseURL() + smsHandlerPath,
	}
	if listErr != nil {
		out["list_error"] = listErr.Error()
		out["hint"] = "Se o erro contem 'ACCESS_DENIED' ou 'INVALID_TOKEN', o scope `messageservice` provavelmente NAO esta no manifest do app no Marketplace. Adicione e re-instale."
	} else {
		out["bitrix_senders_raw"] = raw
	}
	return c.JSON(out)
}

// POST /admin/api/tenant/sms-register?domain=... — forca registro do
// nosso provedor SMS no portal. Util quando o auto-register do install
// falhou (scope faltando, racing etc).
func (h *handlers) adminTenantSMSRegister(c *fiber.Ctx) error {
	domain := strings.TrimSpace(c.Query("domain"))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain obrigatorio"})
	}
	ctx := c.Context()
	portal, err := h.repo.GetBitrixPortalByDomain(ctx, normalizeDomainKey(domain))
	if err != nil || portal == nil {
		return c.Status(404).JSON(fiber.Map{"error": "portal nao encontrado"})
	}
	creds := h.portalToCreds(portal)
	handlerURL := h.cfg.App.BaseURL() + smsHandlerPath
	if err := h.bitrixClient.RegisterSMSSender(ctx, creds, SMSSenderCode, "UC Talk WhatsApp", handlerURL); err != nil {
		h.log.Error("admin: SMS register failed",
			zap.String("domain", portal.Domain), zap.Error(err))
		return c.Status(500).JSON(fiber.Map{
			"error": err.Error(),
			"hint":  "Se o erro contem 'ACCESS_DENIED', o scope `messageservice` esta faltando no manifest do app. Adicione no Marketplace e o cliente precisa re-instalar/re-autorizar.",
		})
	}
	h.log.Info("admin: SMS sender registered manually",
		zap.String("domain", portal.Domain),
		zap.String("handler", handlerURL))
	return c.JSON(fiber.Map{"ok": true, "handler_url": handlerURL, "sender_code": SMSSenderCode})
}

// POST /admin/api/tenant/bp-register?domain=... — forca registro do robot
// no portal. Util quando auto-register do install falhou (scope `bizproc`
// faltando no manifest, p.ex).
func (h *handlers) adminTenantBPRegister(c *fiber.Ctx) error {
	domain := strings.TrimSpace(c.Query("domain"))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain obrigatorio"})
	}
	ctx := c.Context()
	portal, err := h.repo.GetBitrixPortalByDomain(ctx, normalizeDomainKey(domain))
	if err != nil || portal == nil {
		return c.Status(404).JSON(fiber.Map{"error": "portal nao encontrado"})
	}
	handlerURL := h.cfg.App.BaseURL() + bpRobotHandlerPath
	// Registra os 2 robots (Nao Oficial + Oficial) + limpa legado.
	// Erros sao logados internamente; aqui devolvemos status agregado.
	h.RegisterBPRobotForPortal(ctx, portal)
	h.log.Info("admin: BP robots registered manually",
		zap.String("domain", portal.Domain),
		zap.String("handler", handlerURL))
	return c.JSON(fiber.Map{
		"ok":          true,
		"handler_url": handlerURL,
		"robot_codes": []string{BPRobotCodeUnofficial, BPRobotCodeOfficial},
		"hint":        "Verifique /admin/api/tenant/bp-debug?domain=... para confirmar registro. Se faltar, conferir scope `bizproc` no manifest e re-instalar o app.",
	})
}

// GET /admin/api/tenant/bp-debug?domain=... — lista o que o Bitrix tem
// como BizProc robots registrados pelo nosso app. Diagnostica registro
// fantasma, properties mal-formadas, etc.
func (h *handlers) adminTenantBPDebug(c *fiber.Ctx) error {
	domain := strings.TrimSpace(c.Query("domain"))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain obrigatorio"})
	}
	ctx := c.Context()
	portal, err := h.repo.GetBitrixPortalByDomain(ctx, normalizeDomainKey(domain))
	if err != nil || portal == nil {
		return c.Status(404).JSON(fiber.Map{"error": "portal nao encontrado"})
	}
	creds := h.portalToCreds(portal)
	raw, listErr := h.bitrixClient.ListBPRobots(ctx, creds)
	out := fiber.Map{
		"domain": portal.Domain,
		"expected_robot_codes": []string{
			BPRobotCodeUnofficial,
			BPRobotCodeOfficial,
		},
		"legacy_robot_code":    BPRobotCodeLegacy,
		"expected_handler_url": h.cfg.App.BaseURL() + bpRobotHandlerPath,
	}
	if listErr != nil {
		out["list_error"] = listErr.Error()
	} else {
		out["bitrix_robots_raw"] = raw
	}
	return c.JSON(out)
}

// GET /admin/api/tenant/bp-debug-sessions?domain=...
// Diagnostico do dropdown de SESSAO vazio no robot. Mostra:
//   - sessions_raw: todas as sessoes que ListSessionsByDomain retorna
//     (jid, phone, status, type) — o que o robot enxerga do banco
//   - session_options_qr: o map final {jid:label} que vira o dropdown QR
//     (so' sessoes status=active E type!=cloud)
//   - bitrix_accounts_jids: os session_jid vinculados ao dominio em
//     bitrix_accounts (pra detectar descasamento de device suffix, ex:
//     whatsapp_sessions tem :68 mas bitrix_accounts tem :66)
//
// Se sessions_raw mostra a sessao mas session_options_qr vem vazio, a
// causa e' status != active OU o device suffix descasado no vinculo.
func (h *handlers) adminTenantBPDebugSessions(c *fiber.Ctx) error {
	domain := strings.TrimSpace(c.Query("domain"))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain obrigatorio"})
	}
	ctx := c.Context()
	domainKey := normalizePortalDomain(domain)

	sessions, sErr := h.repo.ListSessionsByDomain(ctx, domainKey)
	sessRaw := []map[string]interface{}{}
	for _, s := range sessions {
		sessRaw = append(sessRaw, map[string]interface{}{
			"jid":    s.JID,
			"phone":  s.Phone,
			"status": string(s.Status),
			"type":   string(s.Type),
			"name":   s.DisplayName,
		})
	}

	optsQR, _ := h.sessionOptionsForDomain(ctx, domainKey, false)
	optsCloud, _ := h.sessionOptionsForDomain(ctx, domainKey, true)

	// JIDs vinculados em bitrix_accounts (pra ver descasamento de suffix).
	acctJIDs, _ := h.repo.ListBitrixAccountJIDsByDomain(ctx, domainKey)

	// TODAS as sessoes cruas de whatsapp_sessions (sem filtro de domain) —
	// pra ver o jid/status REAL da sessao conectada e comparar com o vinculo
	// em bitrix_accounts. Se aqui aparece a sessao mas sessions_raw (filtrada
	// por domain) esta vazia, o problema e' o vinculo whatsapp_sessions x
	// bitrix_accounts (jid divergente ou sessao nao vinculada ao domain).
	allSess, _ := h.repo.ListAllSessions(ctx)
	allRaw := []map[string]interface{}{}
	for _, s := range allSess {
		allRaw = append(allRaw, map[string]interface{}{
			"jid":    s.JID,
			"phone":  s.Phone,
			"status": string(s.Status),
			"type":   string(s.Type),
		})
	}

	out := fiber.Map{
		"domain":                domainKey,
		"sessions_raw":          sessRaw,
		"all_whatsapp_sessions": allRaw,
		"session_options_qr":    optsQR,
		"session_options_cloud": optsCloud,
		"bitrix_accounts_jids":  acctJIDs,
		"hint":                  "Compare all_whatsapp_sessions (jid+status reais) vs bitrix_accounts_jids. Se o jid/numero base bate mas sessions_raw vazio -> status banned ou gate de dominio. Se numero base difere -> sessao nao vinculada ao domain.",
	}
	if sErr != nil {
		out["sessions_error"] = sErr.Error()
	}
	return c.JSON(out)
}

// POST /admin/api/tenant/bp-reregister?domain=... — forca delete + add
// do robot. Util quando o registro persistiu mas com payload invalido
// (ex: PROPERTIES que o Bitrix nao renderiza). Garante state limpo.
func (h *handlers) adminTenantBPReregister(c *fiber.Ctx) error {
	domain := strings.TrimSpace(c.Query("domain"))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain obrigatorio"})
	}
	ctx := c.Context()
	portal, err := h.repo.GetBitrixPortalByDomain(ctx, normalizeDomainKey(domain))
	if err != nil || portal == nil {
		return c.Status(404).JSON(fiber.Map{"error": "portal nao encontrado"})
	}
	creds := h.portalToCreds(portal)
	// Delete best-effort dos 3 codes (2 atuais + legado), depois re-registra
	// os 2 atuais. Garante state limpo se algum payload anterior ficou ruim.
	_ = h.bitrixClient.DeleteBPRobot(ctx, creds, BPRobotCodeUnofficial)
	_ = h.bitrixClient.DeleteBPRobot(ctx, creds, BPRobotCodeOfficial)
	_ = h.bitrixClient.DeleteBPRobot(ctx, creds, BPRobotCodeLegacy)
	handlerURL := h.cfg.App.BaseURL() + bpRobotHandlerPath
	h.RegisterBPRobotForPortal(ctx, portal)
	h.log.Info("admin: BP robots re-registered manually",
		zap.String("domain", portal.Domain),
		zap.String("handler", handlerURL))
	return c.JSON(fiber.Map{
		"ok":          true,
		"action":      "deleted_then_added",
		"handler_url": handlerURL,
		"robot_codes": []string{BPRobotCodeUnofficial, BPRobotCodeOfficial},
	})
}

// GET|POST /admin/api/tenant/seed-templates?domain=...
// Cadastra um conjunto de templates NAO OFICIAIS de exemplo pra testar as
// automacoes BizProc (o dropdown "Message template" do robot precisa de ao
// menos 1 template pra sair de "nao ajustado"). Idempotente por titulo: se
// um template com o mesmo titulo ja' existe no dominio, pula (nao duplica).
// Dispara o refresh dos robots ao final pra popular o dropdown.
//
// As variaveis usam a sintaxe do CRM Bitrix ({{Contato}}, {{Nome}}, etc.)
// — no envio real, o Bitrix substitui pelos campos da entidade. No robot,
// o body do template e' enviado como texto livre (Nao Oficial via QR).
func (h *handlers) adminTenantSeedTemplates(c *fiber.Ctx) error {
	domain := strings.TrimSpace(c.Query("domain"))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain obrigatorio"})
	}
	ctx := c.Context()
	// Usa normalizePortalDomain (mesma normalizacao do fluxo de templates via
	// resolveDashboardDomain) pra garantir que o domain key dos templates
	// criados bate EXATAMENTE com o que o robot le em ListMessageTemplates.
	domainKey := normalizePortalDomain(domain)
	portal, err := h.repo.GetBitrixPortalByDomain(ctx, domainKey)
	if err != nil || portal == nil {
		return c.Status(404).JSON(fiber.Map{"error": "portal nao encontrado"})
	}

	// Conjunto de templates de teste. Titulo curto + body com variaveis CRM.
	seeds := []struct {
		Title string
		Body  string
	}{
		{
			"Boas-vindas",
			"Ola *{{Nome}}*! 👋\n\nSeja bem-vindo(a) a UC Technology. Recebemos seu contato e em breve um de nossos especialistas vai falar com voce.\n\nQualquer duvida, e so responder por aqui.",
		},
		{
			"Confirmacao de agendamento",
			"Oi *{{Nome}}*, tudo bem?\n\nConfirmando seu agendamento com a UC Technology. Se precisar remarcar, e so avisar por aqui.\n\nAte breve! 📅",
		},
		{
			"Primeiro contato (Lead)",
			"Ola *{{Nome}}*! 😊\n\nAqui e da UC Technology. Vimos seu interesse e gostariamos de entender melhor como podemos te ajudar. Qual o melhor horario pra conversarmos?",
		},
		{
			"Follow-up de proposta",
			"Oi *{{Nome}}*!\n\nPassando pra saber se voce teve a chance de analisar a nossa proposta. Fico a disposicao pra esclarecer qualquer ponto. 📄",
		},
		{
			"Agradecimento pos-venda",
			"*{{Nome}}*, muito obrigado pela confianca! 🙏\n\nFoi um prazer atender voce. Qualquer necessidade futura, a UC Technology esta a disposicao.",
		},
	}

	// Templates ja existentes no dominio (dedup por titulo, case-insensitive).
	existing, _ := h.repo.ListMessageTemplates(ctx, domainKey)
	existingTitles := map[string]bool{}
	for _, t := range existing {
		existingTitles[strings.ToLower(strings.TrimSpace(t.Title))] = true
	}

	created := []map[string]string{}
	skipped := []string{}
	for _, s := range seeds {
		if existingTitles[strings.ToLower(s.Title)] {
			skipped = append(skipped, s.Title)
			continue
		}
		// Nao Oficial: meta_template_name vazio, sem lang/vars.
		id, cerr := h.repo.CreateMessageTemplate(ctx, domainKey, s.Title, s.Body, "admin-seed", "", "", 0)
		if cerr != nil {
			h.log.Warn("seed-templates: create falhou",
				zap.String("domain", domainKey), zap.String("title", s.Title), zap.Error(cerr))
			continue
		}
		created = append(created, map[string]string{"id": id.String(), "title": s.Title})
	}

	// Popula o dropdown do robot com os templates recem criados.
	if len(created) > 0 {
		h.triggerBPRobotRefresh(domainKey, "seed_templates")
	}

	h.log.Info("admin: seed templates done",
		zap.String("domain", domainKey),
		zap.Int("created", len(created)),
		zap.Int("skipped", len(skipped)))

	return c.JSON(fiber.Map{
		"ok":      true,
		"domain":  domainKey,
		"created": created,
		"skipped": skipped,
		"hint":    "Abra o robot no Bitrix (CRM > Automacoes) — o dropdown 'Message template' agora lista os templates. Se ainda vazio, rode /admin/api/tenant/bp-reregister?domain=...",
	})
}

// GET /admin/api/tenant/placements?domain=...
// Lista placements registrados no portal Bitrix24. Util pra diagnosticar
// placements orfaos ("Application was not found" no menu) — quando o app
// foi reinstalado e o ID mudou mas placement antigo permanece.
func (h *handlers) adminTenantListPlacements(c *fiber.Ctx) error {
	domain := strings.TrimSpace(c.Query("domain"))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain obrigatorio"})
	}
	ctx := c.Context()
	portal, err := h.repo.GetBitrixPortalByDomain(ctx, normalizeDomainKey(domain))
	if err != nil || portal == nil {
		return c.Status(404).JSON(fiber.Map{"error": "portal nao encontrado"})
	}
	creds := h.portalToCreds(portal)
	placements, err := h.bitrixClient.ListPlacements(ctx, creds)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error": err.Error(),
			"hint":  "Se token expirou, cliente precisa abrir o app no Bitrix uma vez pra renovar.",
		})
	}
	return c.JSON(fiber.Map{
		"domain":     portal.Domain,
		"placements": placements,
		"count":      len(placements),
	})
}

// POST /admin/api/tenant/placements/cleanup?domain=...
// Apaga TODOS os placements do nosso app no portal. Usado quando o app
// foi reinstalado e placements antigos ficaram orfaos ("Application was
// not found"). Depois do cleanup, chama RegisterPlacementsForPortal pra
// re-registrar com URLs/IDs atuais.
func (h *handlers) adminTenantPlacementsCleanup(c *fiber.Ctx) error {
	domain := strings.TrimSpace(c.Query("domain"))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain obrigatorio"})
	}
	ctx := c.Context()
	portal, err := h.repo.GetBitrixPortalByDomain(ctx, normalizeDomainKey(domain))
	if err != nil || portal == nil {
		return c.Status(404).JSON(fiber.Map{"error": "portal nao encontrado"})
	}
	creds := h.portalToCreds(portal)

	// 1) Lista placements registrados
	placements, listErr := h.bitrixClient.ListPlacements(ctx, creds)
	if listErr != nil {
		return c.Status(500).JSON(fiber.Map{
			"error": "placement.list falhou: " + listErr.Error(),
			"hint":  "Se token expirou, cliente precisa abrir o app no Bitrix uma vez pra renovar.",
		})
	}

	// 2) Unbind cada um (so do nosso app — Bitrix garante que placement.list
	//    so devolve placements do app autenticado).
	unbindResults := []map[string]interface{}{}
	for _, p := range placements {
		placementName, _ := p["placement"].(string)
		handler, _ := p["handler"].(string)
		if placementName == "" {
			continue
		}
		err := h.bitrixClient.UnbindPlacement(ctx, creds, placementName, handler)
		result := map[string]interface{}{
			"placement": placementName,
			"handler":   handler,
		}
		if err != nil {
			result["status"] = "failed"
			result["error"] = err.Error()
		} else {
			result["status"] = "unbound"
		}
		unbindResults = append(unbindResults, result)
	}

	// 3) Re-registra os placements corretos (CRM tabs + LEFT_MENU)
	h.RegisterPlacementsForPortal(ctx, portal.Domain, creds)

	h.log.Info("admin: placements cleanup done",
		zap.String("domain", portal.Domain),
		zap.Int("unbound", len(unbindResults)))

	return c.JSON(fiber.Map{
		"ok":              true,
		"domain":          portal.Domain,
		"unbound":         unbindResults,
		"reregistered":    []string{"CRM_CONTACT_DETAIL_TAB", "CRM_LEAD_DETAIL_TAB", "CRM_DEAL_DETAIL_TAB"},
		"hint":            "Reabra o app no Bitrix em 5s. Se ainda 'Application was not found', desinstale e reinstale pelo Marketplace.",
	})
}

// GET /admin/api/tenant/portal-debug?domain=...
// Dump do que temos pra esse portal no banco. Diagnostico pra
// APPLICATION_NOT_FOUND — verifica se token e' do install atual ou de
// um zombie antigo.
func (h *handlers) adminTenantPortalDebug(c *fiber.Ctx) error {
	domain := strings.TrimSpace(c.Query("domain"))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain obrigatorio"})
	}
	ctx := c.Context()
	portal, err := h.repo.GetBitrixPortalByDomain(ctx, normalizeDomainKey(domain))
	if err != nil || portal == nil {
		return c.Status(404).JSON(fiber.Map{
			"error":  "portal nao encontrado no banco",
			"domain": domain,
			"hint":   "Cliente nao instalou o app OU instalou e ainda nao chegou no POST /bitrix/auth.",
		})
	}
	// Mascara tokens, mostra so prefixo pra diagnostico.
	mask := func(s string) string {
		if len(s) <= 8 {
			return "(empty)"
		}
		return s[:8] + "..." + s[len(s)-4:]
	}
	return c.JSON(fiber.Map{
		"domain":              portal.Domain,
		"member_id":           portal.MemberID,
		"access_token_prefix": mask(portal.AccessToken),
		"refresh_token_prefix": mask(portal.RefreshToken),
		"app_token_prefix":    mask(portal.ApplicationToken),
		"expires_at":          portal.ExpiresAt,
		"connector_id":        portal.ConnectorID,
		"installed_at":        portal.InstalledAt,
		"updated_at":          portal.UpdatedAt,
		"current_client_id":   h.cfg.Bitrix.ClientID,
		"hint":                "Se updated_at e' antigo, o cliente nunca refez handshake. Pede pra ele abrir o app no Bitrix.",
	})
}

// POST /admin/api/tenant/portal-purge?domain=...
// REMOVE completamente o portal do nosso banco (bitrix_portals + tokens
// + accounts). Forca o proximo install a tratar como portal totalmente
// novo. Usar so quando temos APPLICATION_NOT_FOUND persistente.
//
// CUIDADO: apaga tambem sessions vinculadas via bitrix_accounts, mas
// nao remove rows em whatsapp_sessions (preserva historico de mensagens).
// Apenas desliga o vinculo portal -> sessao.
func (h *handlers) adminTenantPortalPurge(c *fiber.Ctx) error {
	domain := strings.TrimSpace(c.Query("domain"))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain obrigatorio"})
	}
	confirm := strings.TrimSpace(c.Query("confirm"))
	if confirm != "yes" {
		return c.Status(400).JSON(fiber.Map{
			"error": "confirmacao obrigatoria",
			"hint":  "Adicione &confirm=yes pra confirmar. Vai apagar bitrix_portals + bitrix_accounts + bitrix_tokens do dominio.",
		})
	}
	ctx := c.Context()
	deleted, err := h.repo.PurgePortalCompletely(ctx, normalizeDomainKey(domain))
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.log.Warn("admin: portal PURGED",
		zap.String("domain", domain),
		zap.Any("deleted", deleted))
	return c.JSON(fiber.Map{
		"ok":      true,
		"domain":  domain,
		"deleted": deleted,
		"hint":    "Cliente deve agora REINSTALAR o app pelo Marketplace. Proximo /bitrix/auth vai criar portal limpo.",
	})
}

// GET|POST /admin/api/tenant/placements/force-unbind?domain=...
// Endpoint NUCLEAR pra placements verdadeiramente orfaos: tenta unbind
// direto pra cada placement conhecido, sem listar antes. Tenta multiplas
// variacoes de HANDLER (atual + dominios antigos comuns) pra cobrir o
// caso onde o placement orfao foi criado quando o app usava outra URL.
//
// Como sempre passamos so PLACEMENT (sem HANDLER especifico), Bitrix
// remove TODOS os placements daquele tipo registrados pelo nosso app
// — cobrindo o caso onde o portal tem 2+ apps zumbis do mesmo client_id.
//
// Depois rechama RegisterPlacementsForPortal pra deixar 1 limpo.
func (h *handlers) adminTenantPlacementsForceUnbind(c *fiber.Ctx) error {
	domain := strings.TrimSpace(c.Query("domain"))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain obrigatorio"})
	}
	ctx := c.Context()
	portal, err := h.repo.GetBitrixPortalByDomain(ctx, normalizeDomainKey(domain))
	if err != nil || portal == nil {
		return c.Status(404).JSON(fiber.Map{"error": "portal nao encontrado"})
	}
	creds := h.portalToCreds(portal)

	// Lista de placements que o nosso app potencialmente registrou em algum
	// momento. Sem HANDLER -> Bitrix unbind todos do nosso app naquele slot.
	allPlacements := []string{
		"CRM_CONTACT_DETAIL_TAB",
		"CRM_LEAD_DETAIL_TAB",
		"CRM_DEAL_DETAIL_TAB",
		"LEFT_MENU",
		"DEFAULT", // catch-all caso o cliente tenha placements muito antigos
	}

	results := []map[string]interface{}{}
	for _, p := range allPlacements {
		err := h.bitrixClient.UnbindPlacement(ctx, creds, p, "") // sem HANDLER
		r := map[string]interface{}{"placement": p}
		if err != nil {
			// Erros tipo "placement not found" sao normais aqui — significam que
			// nao havia nada pra apagar nesse slot. Reportamos mas nao bloqueia.
			r["status"] = "noop_or_failed"
			r["error"] = err.Error()
		} else {
			r["status"] = "unbound"
		}
		results = append(results, r)
	}

	// Tenta re-registrar limpo. Se isso falha tambem com APPLICATION_NOT_FOUND,
	// o portal ta' realmente quebrado e cliente precisa reinstalar.
	h.RegisterPlacementsForPortal(ctx, portal.Domain, creds)

	h.log.Info("admin: placements FORCE unbound",
		zap.String("domain", portal.Domain),
		zap.Int("attempts", len(results)))

	return c.JSON(fiber.Map{
		"ok":           true,
		"domain":       portal.Domain,
		"attempts":     results,
		"reregistered": []string{"CRM_CONTACT_DETAIL_TAB", "CRM_LEAD_DETAIL_TAB", "CRM_DEAL_DETAIL_TAB"},
		"hint":         "Recarregue o Bitrix em 5-10s (Ctrl+F5). Se duplicata persistir, o placement orfao esta vinculado a OUTRO client_id (app deletado em vendors.bitrix24.com) e so o suporte Bitrix consegue apagar.",
	})
}
