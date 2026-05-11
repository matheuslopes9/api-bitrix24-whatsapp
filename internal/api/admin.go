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
	"go.uber.org/zap"
)

const adminCookieName = "uctalk_admin"
const adminCookieTTL = 12 * time.Hour

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
