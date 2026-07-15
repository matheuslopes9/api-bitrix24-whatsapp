package api

// Handlers para a aba WhatsApp no CRM do Bitrix24.
//
// Fluxo:
//   1. placement.bind registra CRM_CONTACT_DETAIL_TAB (e LEAD, DEAL) apontando para GET /bitrix/crm/tab
//   2. Bitrix abre a URL em iframe quando o operador abre um Contato/Lead/Deal
//   3. A página usa BX24.placement.info() para descobrir entityType e entityId
//   4. Busca o telefone do contato via /bitrix/crm/entity e exibe sessões WA disponíveis
//   5. Operador clica "Iniciar conversa" → POST /bitrix/crm/send que abre a sessão e envia

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/bitrix"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/queue"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/whatsapp"
	"go.uber.org/zap"
)

// GET|POST /bitrix/crm/tab — página iframe exibida na aba WhatsApp do CRM
func (h *handlers) bitrixCRMTab(c *fiber.Ctx) error {
	// HTML inline com JS embutido — sem no-store o iframe do Bitrix cacheia
	// por horas e mudancas de UI nao chegam ao operador.
	c.Set("Cache-Control", "no-store, no-cache, must-revalidate")
	c.Set("Pragma", "no-cache")
	c.Set("Expires", "0")

	h.maybeSetTenantCookieFromBitrixPost(c)
	return c.Type("html").SendString(crmTabHTML)
}

// ─── Endpoints publicos de permissoes (chamados do /dashboard interno) ─────
// Diferenca dos endpoints /admin/api/tenant/*: nao exigem cookie admin.
// Quem acessa o /dashboard ja tem acesso ao backoffice do app, entao a
// barreira aqui eh validar que o domain pedido EXISTE em bitrix_portals.

// resolveDashboardDomain extrai o domain do tenant atual.
//
// SEGURANCA: a partir do middleware requireTenantOrAdmin (server.go), o
// domain vem assinado em c.Locals("tenant_domain"). Query string nao e'
// mais usada como fonte primaria — atacante podia passar ?domain=
// qualquer um e acessar dados alheios.
//
// Caso o middleware nao tenha rodado (rota legada / teste local), faz
// fallback pro unico portal cadastrado.
func (h *handlers) resolveDashboardDomain(ctx context.Context, c *fiber.Ctx) (string, error) {
	if d, ok := c.Locals("tenant_domain").(string); ok && strings.TrimSpace(d) != "" {
		return normalizePortalDomain(d), nil
	}
	// Fallback: unico portal (dev local / sanity).
	portals, err := h.repo.ListBitrixPortals(ctx)
	if err != nil {
		return "", err
	}
	if len(portals) == 1 {
		return portals[0].Domain, nil
	}
	return "", fmt.Errorf("tenant nao identificado — abra o app pelo Bitrix24")
}

// GET /ui/permissions/list — lista permissoes (user x session_jid) do
// dominio. Agrupa por user_id para o frontend renderizar como
// "Joao: +5519... +5511...".
func (h *handlers) uiPermissionsList(c *fiber.Ctx) error {
	ctx := c.Context()
	domain, err := h.resolveDashboardDomain(ctx, c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	key := strings.ToLower(domain)
	perms, err := h.repo.ListCrmPermissionsByDomain(ctx, key)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	// Agrupa por user_id
	type userEntry struct {
		UserID    string   `json:"user_id"`
		UserName  string   `json:"user_name"`
		Sessions  []string `json:"sessions"`  // session_jids liberados (string vazia = wildcard)
		GrantedAt string   `json:"granted_at"`
	}
	byUser := map[string]*userEntry{}
	for _, p := range perms {
		e, ok := byUser[p.UserID]
		if !ok {
			e = &userEntry{
				UserID:    p.UserID,
				UserName:  p.UserName,
				Sessions:  []string{},
				GrantedAt: p.GrantedAt.Format("2006-01-02 15:04"),
			}
			byUser[p.UserID] = e
		}
		e.Sessions = append(e.Sessions, p.SessionJID)
	}
	out := make([]*userEntry, 0, len(byUser))
	for _, e := range byUser {
		out = append(out, e)
	}
	return c.JSON(fiber.Map{
		"users":   out,
		"total":   len(out),
		"domain":  domain,
	})
}

// Cache em memoria de usuarios listados por dominio. TTL 10min.
// Sem locks complexos — uma race condition aqui so causa 1 chamada extra ao
// Bitrix, nao corrompe dado.
type allUsersCacheEntry struct {
	users     []bitrix.BitrixUser
	cachedAt  time.Time
}

var (
	allUsersCacheMu sync.RWMutex
	allUsersCache   = map[string]allUsersCacheEntry{}
)

const allUsersCacheTTL = 10 * time.Minute

// GET /ui/permissions/all-users?refresh=1 — lista todos usuarios ativos do portal.
// Itera IDs 1..500 em batches via im.user.list.get. Cache de 10min por dominio.
func (h *handlers) uiPermissionsAllUsers(c *fiber.Ctx) error {
	ctx := c.Context()
	domain, err := h.resolveDashboardDomain(ctx, c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	refresh := c.Query("refresh") == "1"
	key := strings.ToLower(domain)

	if !refresh {
		allUsersCacheMu.RLock()
		entry, ok := allUsersCache[key]
		allUsersCacheMu.RUnlock()
		if ok && time.Since(entry.cachedAt) < allUsersCacheTTL {
			return c.JSON(buildAllUsersResponse(ctx, h, key, entry.users, true))
		}
	}

	portal, err := h.repo.GetBitrixPortalByDomain(ctx, domain)
	if err != nil || portal == nil {
		return c.Status(404).JSON(fiber.Map{"error": "portal nao encontrado: " + domain})
	}
	creds := h.portalToCreds(portal)
	users, err := h.bitrixClient.ListAllUsers(ctx, creds, 1000)
	if err != nil {
		h.log.Error("ui: ListAllUsers failed", zap.String("domain", domain), zap.Error(err))
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	allUsersCacheMu.Lock()
	allUsersCache[key] = allUsersCacheEntry{users: users, cachedAt: time.Now()}
	allUsersCacheMu.Unlock()
	h.log.Info("ui: ListAllUsers ok", zap.String("domain", domain), zap.Int("count", len(users)))
	return c.JSON(buildAllUsersResponse(ctx, h, key, users, false))
}

// buildAllUsersResponse retorna lista de users com as session_jids ja
// liberadas para cada um, pra dashboard renderizar checkboxes.
func buildAllUsersResponse(ctx context.Context, h *handlers, domainKey string, users []bitrix.BitrixUser, fromCache bool) fiber.Map {
	perms, _ := h.repo.ListCrmPermissionsByDomain(ctx, domainKey)
	byUser := map[string][]string{}
	for _, p := range perms {
		byUser[p.UserID] = append(byUser[p.UserID], p.SessionJID)
	}
	out := make([]fiber.Map, 0, len(users))
	for _, u := range users {
		full := strings.TrimSpace(u.Name + " " + u.LastName)
		if full == "" {
			full = "User #" + u.ID
		}
		sess := byUser[u.ID]
		if sess == nil {
			sess = []string{}
		}
		out = append(out, fiber.Map{
			"id":               u.ID,
			"name":             full,
			"email":            u.Email,
			"position":         u.Position,
			"active":           u.Active,
			"allowed_sessions": sess, // pode incluir "" = wildcard legacy
		})
	}
	return fiber.Map{
		"users":         out,
		"total":         len(out),
		"granted_users": len(byUser),
		"from_cache":    fromCache,
	}
}

// GET /ui/permissions/user-info?user_id=N — busca info de 1 user no Bitrix.
func (h *handlers) uiPermissionsUserInfo(c *fiber.Ctx) error {
	userID := strings.TrimSpace(c.Query("user_id"))
	if userID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "user_id obrigatorio"})
	}
	ctx := c.Context()
	domain, err := h.resolveDashboardDomain(ctx, c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	portal, err := h.repo.GetBitrixPortalByDomain(ctx, domain)
	if err != nil || portal == nil {
		return c.Status(404).JSON(fiber.Map{"error": "portal nao encontrado: " + domain})
	}
	creds := h.portalToCreds(portal)
	users, err := h.bitrixClient.GetUserByIDs(ctx, creds, []string{userID})
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	if len(users) == 0 {
		return c.Status(404).JSON(fiber.Map{"error": "usuario " + userID + " nao encontrado"})
	}
	u := users[0]
	full := strings.TrimSpace(u.Name + " " + u.LastName)
	if full == "" {
		full = "User #" + u.ID
	}
	return c.JSON(fiber.Map{
		"id": u.ID, "name": full, "first": u.Name, "last": u.LastName,
		"email": u.Email, "position": u.Position, "active": u.Active,
	})
}

// POST /ui/permissions/grant — Body: {"user_id":"N","user_name":"X"}
func (h *handlers) uiPermissionsGrant(c *fiber.Ctx) error {
	return h.uiPermissionsMutate(c, true)
}

// POST /ui/permissions/revoke — Body: {"user_id":"N"}
func (h *handlers) uiPermissionsRevoke(c *fiber.Ctx) error {
	return h.uiPermissionsMutate(c, false)
}

func (h *handlers) uiPermissionsMutate(c *fiber.Ctx, grant bool) error {
	ctx := c.Context()
	domain, err := h.resolveDashboardDomain(ctx, c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	var req struct {
		UserID       string `json:"user_id"`
		UserName     string `json:"user_name"`
		SessionJID   string `json:"session_jid"`
		CallerUserID string `json:"caller_user_id"` // quem esta executando a acao (precisa ser master)
	}
	if err := c.BodyParser(&req); err != nil || req.UserID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "user_id obrigatorio"})
	}
	if req.SessionJID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "session_jid obrigatorio"})
	}
	if req.CallerUserID == "" {
		return c.Status(403).JSON(fiber.Map{"error": "caller_user_id obrigatorio — apenas o master pode alterar permissoes"})
	}
	key := strings.ToLower(domain)

	// Guard: so o master atual do tenant pode mexer em permissoes.
	portal, err := h.repo.GetBitrixPortalByDomain(ctx, normalizePortalDomain(domain))
	if err != nil || portal == nil {
		return c.Status(404).JSON(fiber.Map{"error": "portal nao encontrado"})
	}
	if portal.LegacyAdminUserID == "" {
		return c.Status(409).JSON(fiber.Map{"error": "master nao configurado — execute o onboarding primeiro"})
	}
	if portal.LegacyAdminUserID != req.CallerUserID {
		return c.Status(403).JSON(fiber.Map{"error": "apenas o usuario master pode alterar permissoes"})
	}

	if grant {
		if err := h.repo.GrantSessionPermission(ctx, key, req.UserID, req.UserName, req.SessionJID, "dashboard"); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		h.log.Info("ui: granted session access",
			zap.String("domain", key), zap.String("user_id", req.UserID),
			zap.String("session_jid", req.SessionJID))
		return c.JSON(fiber.Map{"ok": true, "action": "granted"})
	}
	removed, err := h.repo.RevokeSessionPermission(ctx, key, req.UserID, req.SessionJID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.log.Info("ui: revoked session access",
		zap.String("domain", key), zap.String("user_id", req.UserID),
		zap.String("session_jid", req.SessionJID))
	return c.JSON(fiber.Map{"ok": true, "action": "revoked", "removed": removed})
}

// ─── Templates de mensagem (quick replies) ─────────────────────────────────

// POST /ui/bp-robots/refresh — forca re-register dos 2 robots BizProc no
// Bitrix do dominio. Util quando o cliente conecta nova sessao WhatsApp
// (QR ou Cloud) e quer ver no dropdown imediatamente, sem reinstalar o
// app. Tambem util pra debug.
func (h *handlers) uiBPRobotsRefresh(c *fiber.Ctx) error {
	ctx := c.Context()
	domain, err := h.resolveDashboardDomain(ctx, c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	h.triggerBPRobotRefresh(domain, "manual_ui_refresh")
	return c.JSON(fiber.Map{
		"ok":     true,
		"domain": domain,
		"hint":   "Refresh em background — abra Bitrix > CRM > Automacoes em 5s pra ver dropdowns atualizados.",
	})
}

// POST /ui/templates/purge-broken — apaga rows com created_by="meta-import"
// e meta_template_name vazio. Usado UMA vez pra limpar a bagunca causada
// pela migration 023 que dropava as colunas a cada restart.
// Seguranca: so toca rows do domain atual, so com prefix meta-import.
func (h *handlers) uiTemplatesPurgeBroken(c *fiber.Ctx) error {
	ctx := c.Context()
	domain, err := h.resolveDashboardDomain(ctx, c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	deleted, err := h.repo.DeleteBrokenMetaImports(ctx, domain)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.log.Info("templates: purged broken meta-import rows",
		zap.String("domain", domain), zap.Int("deleted", deleted))
	return c.JSON(fiber.Map{"ok": true, "deleted": deleted, "domain": domain})
}

// GET /ui/templates/debug — diagnostico: lista templates com flag oficial
// computada server-side. Util pra confirmar se meta_template_name esta
// preenchido no banco (vs filtro JS errado no front).
func (h *handlers) uiTemplatesDebug(c *fiber.Ctx) error {
	ctx := c.Context()
	domain, err := h.resolveDashboardDomain(ctx, c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	tpls, err := h.repo.ListMessageTemplates(ctx, domain)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	rows := make([]fiber.Map, 0, len(tpls))
	for _, t := range tpls {
		mn := strings.TrimSpace(t.MetaTemplateName)
		rows = append(rows, fiber.Map{
			"id":                     t.ID.String(),
			"title":                  t.Title,
			"meta_template_name_raw": t.MetaTemplateName,
			"meta_template_name_len": len(t.MetaTemplateName),
			"meta_template_lang":     t.MetaTemplateLang,
			"meta_template_vars":     t.MetaTemplateVars,
			"created_by":             t.CreatedBy,
			"is_official":            mn != "",
		})
	}
	return c.JSON(fiber.Map{"domain": domain, "rows": rows, "total": len(rows)})
}

// GET /ui/templates/list — lista templates do dominio atual.
// Inclui contadores por categoria pra UI separar abas sem ter que filtrar
// 2x o array.
func (h *handlers) uiTemplatesList(c *fiber.Ctx) error {
	ctx := c.Context()
	domain, err := h.resolveDashboardDomain(ctx, c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	tpls, err := h.repo.ListMessageTemplates(ctx, domain)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	countOfficial := 0
	countUnofficial := 0
	for _, t := range tpls {
		if strings.TrimSpace(t.MetaTemplateName) != "" {
			countOfficial++
		} else {
			countUnofficial++
		}
	}
	return c.JSON(fiber.Map{
		"templates":        tpls,
		"total":            len(tpls),
		"count_official":   countOfficial,
		"count_unofficial": countUnofficial,
		"domain":           domain,
	})
}

// POST /ui/templates/create — body {title, body, created_by}
func (h *handlers) uiTemplatesCreate(c *fiber.Ctx) error {
	ctx := c.Context()
	domain, err := h.resolveDashboardDomain(ctx, c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	var req struct {
		Title            string `json:"title"`
		Body             string `json:"body"`
		CreatedBy        string `json:"created_by"`
		MetaTemplateName string `json:"meta_template_name"`
		MetaTemplateLang string `json:"meta_template_lang"`
		MetaTemplateVars int    `json:"meta_template_vars"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "JSON invalido"})
	}
	req.Title = strings.TrimSpace(req.Title)
	req.Body = strings.TrimSpace(req.Body)
	req.MetaTemplateName = strings.TrimSpace(req.MetaTemplateName)
	req.MetaTemplateLang = strings.TrimSpace(req.MetaTemplateLang)
	if req.Title == "" || req.Body == "" {
		return c.Status(400).JSON(fiber.Map{"error": "title e body obrigatorios"})
	}
	id, err := h.repo.CreateMessageTemplate(ctx, domain, req.Title, req.Body, req.CreatedBy,
		req.MetaTemplateName, req.MetaTemplateLang, req.MetaTemplateVars)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	// Refresh dropdowns dos robots BizProc — template novo precisa aparecer.
	h.triggerBPRobotRefresh(domain, "template_create")
	return c.JSON(fiber.Map{"id": id.String()})
}

// POST /ui/templates/update?id=... — body {title, body, meta_*}
func (h *handlers) uiTemplatesUpdate(c *fiber.Ctx) error {
	ctx := c.Context()
	domain, err := h.resolveDashboardDomain(ctx, c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	idStr := c.Query("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "id invalido"})
	}
	var req struct {
		Title            string `json:"title"`
		Body             string `json:"body"`
		MetaTemplateName string `json:"meta_template_name"`
		MetaTemplateLang string `json:"meta_template_lang"`
		MetaTemplateVars int    `json:"meta_template_vars"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "JSON invalido"})
	}
	req.Title = strings.TrimSpace(req.Title)
	req.Body = strings.TrimSpace(req.Body)
	req.MetaTemplateName = strings.TrimSpace(req.MetaTemplateName)
	req.MetaTemplateLang = strings.TrimSpace(req.MetaTemplateLang)
	if req.Title == "" || req.Body == "" {
		return c.Status(400).JSON(fiber.Map{"error": "title e body obrigatorios"})
	}
	ok, err := h.repo.UpdateMessageTemplate(ctx, id, domain, req.Title, req.Body,
		req.MetaTemplateName, req.MetaTemplateLang, req.MetaTemplateVars)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	if !ok {
		return c.Status(404).JSON(fiber.Map{"error": "template nao encontrado"})
	}
	// Refresh dropdowns: title pode ter mudado, ou template Nao Oficial
	// virou Oficial (e vice-versa) via edicao.
	h.triggerBPRobotRefresh(domain, "template_update")
	return c.JSON(fiber.Map{"ok": true})
}

// POST /ui/templates/delete?id=...
func (h *handlers) uiTemplatesDelete(c *fiber.Ctx) error {
	ctx := c.Context()
	domain, err := h.resolveDashboardDomain(ctx, c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	idStr := c.Query("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "id invalido"})
	}
	ok, err := h.repo.DeleteMessageTemplate(ctx, id, domain)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	if !ok {
		return c.Status(404).JSON(fiber.Map{"error": "template nao encontrado"})
	}
	// Refresh dropdowns: template deletado precisa sair dos selects.
	h.triggerBPRobotRefresh(domain, "template_delete")
	return c.JSON(fiber.Map{"ok": true})
}

// GET /ui/templates/meta-list?domain=... — chama Meta Graph API e retorna
// templates HSM aprovados da sessao Cloud do tenant. Cliente abre a aba
// "Importar da Meta" e ve quais sao APPROVED.
//
// Pre-requisitos no tenant:
//   - Sessao Cloud configurada (whatsapp_sessions.type='cloud_api')
//   - waba_id preenchido (pediu no formulario de Nova Sessao Cloud)
//   - access_token com scope `whatsapp_business_management`
//
// Retorna erro claro se algum desses faltar.
func (h *handlers) uiTemplatesMetaList(c *fiber.Ctx) error {
	ctx := c.Context()
	domain, err := h.resolveDashboardDomain(ctx, c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	// Acha a primeira sessao Cloud ativa do dominio.
	sessions, err := h.repo.ListSessionsByDomain(ctx, domain)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	var cloud *db.WhatsAppSession
	for _, s := range sessions {
		if s.Type == db.SessionTypeCloudAPI && s.Status == db.SessionActive {
			cloud = s
			break
		}
	}
	if cloud == nil {
		return c.Status(404).JSON(fiber.Map{
			"error": "nenhuma sessao Cloud API ativa neste tenant",
			"hint":  "Conecte uma sessao Cloud em 'Sessoes WhatsApp' antes de importar templates.",
		})
	}
	if cloud.CloudWABAID == "" {
		return c.Status(400).JSON(fiber.Map{
			"error": "sessao Cloud nao tem WABA_ID configurado",
			"hint":  "Edite a sessao em 'Sessoes WhatsApp' e preencha o campo 'WABA ID' (encontrado no Meta Business Manager > WhatsApp Manager).",
		})
	}
	includeAll := c.Query("all") == "1"
	tmpls, err := whatsapp.FetchMetaTemplates(ctx, cloud.CloudWABAID, cloud.CloudAccessToken, includeAll)
	if err != nil {
		// Detecta scope faltando — mensagem clara pro cliente.
		msg := err.Error()
		hint := ""
		if strings.Contains(msg, "permission") || strings.Contains(msg, "scope") || strings.Contains(msg, "OAuthException") {
			hint = "O access_token da sua sessao Cloud nao tem scope 'whatsapp_business_management'. No Meta Business Manager > System Users > seu user, adicione essa permissao e gere um token novo. Depois edite a sessao Cloud aqui com o token novo."
		}
		return c.Status(500).JSON(fiber.Map{
			"error":   msg,
			"hint":    hint,
			"waba_id": cloud.CloudWABAID,
		})
	}
	return c.JSON(fiber.Map{
		"templates": tmpls,
		"waba_id":   cloud.CloudWABAID,
		"count":     len(tmpls),
	})
}

// POST /ui/templates/meta-import — body {templates:[{name,language,vars_count,body_text}, ...]}
// Insere em lote no message_templates com meta_template_* preenchido.
// Se nome ja existe no dominio (pra qualquer template), pula.
func (h *handlers) uiTemplatesMetaImport(c *fiber.Ctx) error {
	ctx := c.Context()
	domain, err := h.resolveDashboardDomain(ctx, c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	var req struct {
		Templates []struct {
			Name      string `json:"name"`
			Language  string `json:"language"`
			VarsCount int    `json:"vars_count"`
			BodyText  string `json:"body_text"`
		} `json:"templates"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "JSON invalido"})
	}
	if len(req.Templates) == 0 {
		return c.Status(400).JSON(fiber.Map{"error": "lista de templates vazia"})
	}

	// Pra evitar duplicatas, lista os ja cadastrados e indexa pelo nome Meta.
	existing, _ := h.repo.ListMessageTemplates(ctx, domain)
	known := map[string]bool{}
	for _, t := range existing {
		if t.MetaTemplateName != "" {
			known[t.MetaTemplateName] = true
		}
	}

	created := 0
	skipped := 0
	var errs []string
	for _, t := range req.Templates {
		t.Name = strings.TrimSpace(t.Name)
		t.Language = strings.TrimSpace(t.Language)
		if t.Name == "" || t.Language == "" {
			skipped++
			continue
		}
		if known[t.Name] {
			skipped++
			continue
		}
		// Titulo derivado do nome Meta (cliente edita depois se quiser).
		title := t.Name
		body := t.BodyText
		if body == "" {
			body = "(template Meta: " + t.Name + " — edite este texto como referencia)"
		}
		// Log antes de inserir pra desambiguar se algum field chega vazio
		// (bug recorrente: import sumir do tab Oficial = meta_template_name
		// gravado em branco).
		h.log.Info("meta-import: inserindo template",
			zap.String("domain", domain),
			zap.String("meta_name", t.Name),
			zap.String("meta_lang", t.Language),
			zap.Int("meta_vars", t.VarsCount),
			zap.Int("body_len", len(body)))
		_, err := h.repo.CreateMessageTemplate(ctx, domain, title, body, "meta-import",
			t.Name, t.Language, t.VarsCount)
		if err != nil {
			h.log.Warn("meta-import: insert falhou",
				zap.String("meta_name", t.Name), zap.Error(err))
			errs = append(errs, t.Name+": "+err.Error())
			continue
		}
		created++
	}

	// Apos importar templates novos, refresh dropdowns dos robots no Bitrix.
	if created > 0 {
		h.triggerBPRobotRefresh(domain, "meta_import")
	}

	return c.JSON(fiber.Map{
		"created": created,
		"skipped": skipped,
		"errors":  errs,
	})
}

// ─── Historico de conversas (aba /dashboard) ──────────────────────────────
// Backend stateless: tudo derivado de whatsapp_sessions + messages no banco.

// GET /ui/history/sessions?domain=... — popula o dropdown da aba Historico
// com TODAS as sessoes do tenant (ativas + desconectadas). Diferente de
// /bitrix/crm/sessions que so retorna ativas — historico tem que mostrar
// sessoes antigas tambem (caso QR -> Cloud, as conversas da QR antiga
// continuam no banco e precisam ser acessiveis).
func (h *handlers) uiHistorySessions(c *fiber.Ctx) error {
	ctx := c.Context()
	domain, err := h.resolveDashboardDomain(ctx, c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	rows, err := h.repo.ListSessionsByDomain(ctx, domain)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	out := make([]fiber.Map, 0, len(rows))
	for _, s := range rows {
		phone := s.Phone
		if phone == "" {
			phone = s.CloudDisplayPhone
		}
		out = append(out, fiber.Map{
			"jid":    s.JID,
			"phone":  phone,
			"type":   s.Type,
			"label":  s.DisplayName,
			"status": s.Status,
		})
	}
	return c.JSON(fiber.Map{"sessions": out, "domain": domain})
}

// GET /ui/history/conversations?session_jid=... — lista de conversas
// (peers) da sessao escolhida, com ultima msg, timestamp e total.
func (h *handlers) uiHistoryConversations(c *fiber.Ctx) error {
	sessionJID := strings.TrimSpace(c.Query("session_jid"))
	if sessionJID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "session_jid obrigatorio"})
	}
	limit := c.QueryInt("limit", 200)
	convs, err := h.repo.ListHistoryConversations(c.Context(), sessionJID, limit)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"conversations": convs, "session_jid": sessionJID})
}

// GET /ui/history/messages?session_jid=...&phone=... — mensagens de uma
// conversa especifica. Sem download de arquivo: midia vira placeholder
// textual (ex.: "[Imagem]", "[Audio]") com legenda se houver.
func (h *handlers) uiHistoryMessages(c *fiber.Ctx) error {
	sessionJID := strings.TrimSpace(c.Query("session_jid"))
	phone := strings.TrimSpace(c.Query("phone"))
	if sessionJID == "" || phone == "" {
		return c.Status(400).JSON(fiber.Map{"error": "session_jid e phone obrigatorios"})
	}
	limit := c.QueryInt("limit", 500)
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	msgs, err := h.repo.GetMessagesByPhone(c.Context(), normalizeWAPhone(phone), limit)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	out := make([]fiber.Map, 0, len(msgs))
	// Inverte: GetMessagesByPhone vem DESC, queremos ASC pra render cronologico
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		text := m.Content
		mt := string(m.MessageType)
		if mt != "" && mt != "text" {
			placeholder := historyMediaPlaceholder(mt)
			if text != "" {
				text = placeholder + " " + text
			} else {
				text = placeholder
			}
		}
		out = append(out, fiber.Map{
			"id":           m.ID.String(),
			"direction":    string(m.Direction),
			"type":         mt,
			"text":         text,
			"author_name":  m.AuthorName,
			"status":       string(m.Status),
			"created_at":   m.CreatedAt,
		})
	}
	return c.JSON(fiber.Map{"messages": out, "count": len(out)})
}

func historyMediaPlaceholder(t string) string {
	switch t {
	case "image":
		return "[Imagem]"
	case "video":
		return "[Video]"
	case "audio":
		return "[Audio]"
	case "document":
		return "[Documento]"
	case "sticker":
		return "[Sticker]"
	default:
		return "[" + t + "]"
	}
}

// GET /bitrix/crm/check-access?domain=...&user_id=...
// Modelo novo: CRM tab e aberto pra todo colaborador interno ativo do portal.
// Externos/bots/desativados sao bloqueados. O que controla o ENVIO e a aba
// "Permissoes por Numero" (allowed_sessions), nao mais o acesso ao tab.
//
// Retorno mantido por compat de JS: {allowed, configured}. Configured fica
// sempre true (nao bloqueia mais por "config inicial pendente").
func (h *handlers) bitrixCRMCheckAccess(c *fiber.Ctx) error {
	domain := strings.TrimSpace(c.Query("domain"))
	userID := strings.TrimSpace(c.Query("user_id"))
	if domain == "" || userID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain e user_id obrigatorios"})
	}
	portal, err := h.repo.GetBitrixPortalByDomain(c.Context(), normalizePortalDomain(domain))
	if err != nil || portal == nil {
		return c.Status(404).JSON(fiber.Map{"error": "portal nao encontrado"})
	}
	creds := h.portalToCreds(portal)
	users, err := h.bitrixClient.GetUserByIDs(c.Context(), creds, []string{userID})
	if err != nil {
		h.log.Error("crm check access: GetUserByIDs failed", zap.Error(err))
		// Falha temporaria do Bitrix nao deve trancar — libera e auditoria
		// via logs detecta abusos. Politica conservadora seria 403; preferimos
		// disponibilidade.
		return c.JSON(fiber.Map{"allowed": true, "configured": true, "warning": "user_lookup_failed"})
	}
	allowed := false
	for _, u := range users {
		if u.ID == userID && u.Active && !u.Extranet && !u.Bot {
			allowed = true
			break
		}
	}
	return c.JSON(fiber.Map{
		"allowed":    allowed,
		"configured": true,
	})
}

// GET /bitrix/crm/master/status?domain=... — retorna info do master atual
// para a UI decidir entre "tela de onboarding", "voce e o master" ou
// "outro user e o master". Cliente decide o que renderizar.
func (h *handlers) bitrixCRMMasterStatus(c *fiber.Ctx) error {
	domain := strings.TrimSpace(c.Query("domain"))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain obrigatorio"})
	}
	portal, err := h.repo.GetBitrixPortalByDomain(c.Context(), normalizePortalDomain(domain))
	if err != nil || portal == nil {
		return c.Status(404).JSON(fiber.Map{"error": "portal nao encontrado"})
	}
	out := fiber.Map{
		"configured":    portal.LegacyAdminUserID != "",
		"master_user_id": portal.LegacyAdminUserID,
	}
	// Se ja tem master, anexa o nome (snapshot) pra UI mostrar
	if portal.LegacyAdminUserID != "" {
		perms, _ := h.repo.ListCrmPermissionsByDomain(c.Context(),
			normalizePortalDomain(domain))
		for _, p := range perms {
			if p.UserID == portal.LegacyAdminUserID {
				out["master_user_name"] = p.UserName
				break
			}
		}
	}
	return c.JSON(out)
}

// POST /bitrix/crm/master/set — body {domain, caller_user_id, new_master_user_id, new_master_name}
//   - Se nao tem master ainda: qualquer interno ativo pode setar (onboarding).
//   - Se tem master: so o master atual pode trocar (caller_user_id == master).
// Em ambos os casos: validamos que caller e new_master sao internos ativos.
func (h *handlers) bitrixCRMMasterSet(c *fiber.Ctx) error {
	var body struct {
		Domain          string `json:"domain"`
		CallerUserID    string `json:"caller_user_id"`
		NewMasterUserID string `json:"new_master_user_id"`
		NewMasterName   string `json:"new_master_name"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "JSON invalido"})
	}
	if body.Domain == "" || body.CallerUserID == "" || body.NewMasterUserID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain, caller_user_id e new_master_user_id obrigatorios"})
	}
	ctx := c.Context()
	domainKey := normalizePortalDomain(body.Domain)

	portal, err := h.repo.GetBitrixPortalByDomain(ctx, domainKey)
	if err != nil || portal == nil {
		return c.Status(404).JSON(fiber.Map{"error": "portal nao encontrado"})
	}
	creds := h.portalToCreds(portal)

	// Politica de validacao:
	//   - new_master SEMPRE precisa ser interno ativo no Bitrix.
	//   - caller so precisa ser interno ativo se ja existe master configurado
	//     (caso de transferencia — pra garantir que o caller e' realmente o
	//     master, nao um user_id qualquer). No onboarding inicial (portal sem
	//     master), o caller pode ser o admin via /dashboard (sem identidade
	//     Bitrix) — autorizacao vem do proprio acesso ao /dashboard.
	users, err := h.bitrixClient.GetUserByIDs(ctx, creds, []string{body.CallerUserID, body.NewMasterUserID})
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "lookup user Bitrix falhou: " + err.Error()})
	}
	isInternalActive := func(id string) (bool, string) {
		for _, u := range users {
			if u.ID == id {
				if u.Active && !u.Extranet && !u.Bot {
					full := strings.TrimSpace(u.Name + " " + u.LastName)
					if full == "" {
						full = "User #" + u.ID
					}
					return true, full
				}
				return false, ""
			}
		}
		return false, ""
	}
	if portal.LegacyAdminUserID != "" {
		if ok, _ := isInternalActive(body.CallerUserID); !ok {
			return c.Status(403).JSON(fiber.Map{"error": "caller nao e um colaborador interno ativo"})
		}
	}
	newOK, newName := isInternalActive(body.NewMasterUserID)
	if !newOK {
		return c.Status(400).JSON(fiber.Map{"error": "novo master precisa ser um colaborador interno ativo"})
	}
	if body.NewMasterName == "" {
		body.NewMasterName = newName
	}

	if err := h.repo.SetMasterUser(ctx, domainKey, body.CallerUserID, body.NewMasterUserID, body.NewMasterName); err != nil {
		if err == db.ErrNotMaster {
			return c.Status(403).JSON(fiber.Map{"error": "apenas o usuario master atual pode transferir o controle"})
		}
		h.log.Error("crm master set failed", zap.Error(err))
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.log.Info("crm master set",
		zap.String("domain", domainKey),
		zap.String("caller", body.CallerUserID),
		zap.String("new_master", body.NewMasterUserID))
	return c.JSON(fiber.Map{"ok": true, "master_user_id": body.NewMasterUserID, "master_user_name": body.NewMasterName})
}

// GET /bitrix/crm/allowed-sessions?domain=...&user_id=...
// Retorna a lista de session_jids que esse usuario pode usar pra enviar.
// CRM tab usa pra montar o seletor de numero — sem fila aqui, mostra tudo
// que esta autorizado (independente de estar ativo na hora; ate sessoes
// desconectadas voltam, com badge).
func (h *handlers) bitrixCRMAllowedSessions(c *fiber.Ctx) error {
	domain := strings.TrimSpace(c.Query("domain"))
	userID := strings.TrimSpace(c.Query("user_id"))
	if domain == "" || userID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain e user_id obrigatorios"})
	}
	key := normalizeDomainKey(domain)
	jids, err := h.repo.ListUserAllowedSessions(c.Context(), key, userID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"sessions": jids, "count": len(jids)})
}

// GET /bitrix/crm/entity?domain=...&entity_type=contact|lead|deal&entity_id=...
// Retorna { phone, name } do contato/lead/deal para o iframe popular o formulário.
func (h *handlers) bitrixCRMEntity(c *fiber.Ctx) error {
	domain := c.Query("domain")
	entityType := strings.ToLower(c.Query("entity_type", "contact"))
	entityID := c.Query("entity_id")

	if domain == "" || entityID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "domain e entity_id são obrigatórios"})
	}

	portal, err := h.repo.GetBitrixPortalByDomain(c.Context(), normalizePortalDomain(domain))
	if err != nil || portal == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "portal não encontrado: " + domain})
	}
	creds := h.portalToCreds(portal)

	var raw json.RawMessage
	switch entityType {
	case "lead":
		raw, err = h.bitrixClient.GetLead(c.Context(), creds, entityID)
	case "deal":
		raw, err = h.bitrixClient.GetDeal(c.Context(), creds, entityID)
	default:
		raw, err = h.bitrixClient.GetContact(c.Context(), creds, entityID)
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	// Extrai nome e telefone do objeto retornado
	var obj map[string]json.RawMessage
	if e := json.Unmarshal(raw, &obj); e != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "parse error"})
	}

	name := jsonStr(obj, "NAME") + " " + jsonStr(obj, "LAST_NAME")
	name = strings.TrimSpace(name)
	if name == "" {
		name = jsonStr(obj, "TITLE")
	}

	// PHONE é array de objetos [{VALUE: "55...", VALUE_TYPE: "WORK"}, ...]
	phone := extractPhone(obj)

	// Sessões WA disponíveis
	sessions := h.waManager.ListSessions()

	return c.JSON(fiber.Map{
		"name":     name,
		"phone":    phone,
		"sessions": sessions,
	})
}

// POST /bitrix/crm/send
// Body: { "domain", "entity_type", "entity_id", "phone", "session_jid", "message", "line_id", "operator_name", "user_id" }
//
// Guard: o user_id precisa ter session_jid liberado em crm_user_permissions
// (a aba "Permissoes por Numero" no /dashboard). Sem libera o envio retorna
// 403. Compat: linhas legacy com session_jid='' (pre-migration 018) valem
// como wildcard.
func (h *handlers) bitrixCRMSend(c *fiber.Ctx) error {
	var body struct {
		Domain       string `json:"domain"`
		EntityType   string `json:"entity_type"`
		EntityID     string `json:"entity_id"`
		Phone        string `json:"phone"`
		SessionJID   string `json:"session_jid"`
		Message      string `json:"message"`
		LineID       int    `json:"line_id"`
		OperatorName string `json:"operator_name"`
		UserID       string `json:"user_id"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if body.Domain == "" || body.Phone == "" || body.SessionJID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "domain, phone e session_jid são obrigatórios"})
	}
	if body.Message == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "message é obrigatório"})
	}

	// Permission guard — operador so envia se a sessao escolhida esta liberada
	// pra ele. user_id obrigatorio nesse modelo novo. Se vier vazio (JS antigo
	// que ainda nao foi atualizado), bloqueia com mensagem explicita.
	if body.UserID == "" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "user_id ausente — atualize a aba do CRM (Ctrl+Shift+R)",
		})
	}
	domainKey := normalizeDomainKey(body.Domain)
	ok, err := h.repo.IsSessionAllowed(c.Context(), domainKey, body.UserID, body.SessionJID)
	if err != nil {
		h.log.Error("crm send: permission check failed", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if !ok {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "voce nao tem permissao pra enviar com este numero. Peca para um admin liberar.",
		})
	}

	portal, err := h.repo.GetBitrixPortalByDomain(c.Context(), normalizePortalDomain(body.Domain))
	if err != nil || portal == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "portal não encontrado"})
	}
	creds := h.portalToCreds(portal)

	phone := normalizeWAPhone(body.Phone)
	toJID := phone + "@s.whatsapp.net"

	connectorID := portal.ConnectorID
	if connectorID == "" {
		connectorID = "whatsapp_uc_v2"
	}
	lineID := body.LineID
	if lineID == 0 {
		lineID = portal.OpenLineID
	}
	if lineID == 0 {
		lineID = 1
	}

	operatorName := body.OperatorName
	if operatorName == "" {
		operatorName = "UC Talk"
	}

	// 1. Busca o chat_id do Open Lines vinculado a este contato/lead/deal.
	//    Necessário para registrar a mensagem no Open Lines existente (não criar paralela).
	chatID := ""
	if body.EntityID != "" {
		bxEntityType := strings.ToUpper(body.EntityType)
		chatID, _ = h.bitrixClient.GetCRMChatLastID(c.Context(), creds, bxEntityType, body.EntityID)
		if chatID == "" {
			if chatsRaw, e := h.bitrixClient.GetCRMChats(c.Context(), creds, bxEntityType, body.EntityID); e == nil {
				chatID = extractChatID(chatsRaw)
			}
		}
	}

	// 2. Se há chat no Open Lines, registra a msg lá. O Bitrix roteia para o
	//    connector (ONIMCONNECTORMESSAGEADD) que enfileira o OutboundJob via
	//    bitrixConnectorEvent — então NÃO enfileiramos direto aqui (evita duplicar).
	//    Se não há chat ainda, enfileira direto pelo WhatsApp como fallback.
	if chatID != "" {
		if _, sendErr := h.bitrixClient.SendOperatorMessage(c.Context(), creds, chatID, body.Message); sendErr != nil {
			h.log.Warn("crm send: SendOperatorMessage failed, falling back to direct WA send",
				zap.String("chat_id", chatID), zap.Error(sendErr))
			// Fallback: se falhou no Open Lines, envia direto pelo WA para não perder a msg
			textWithPrefix := fmt.Sprintf("*%s:*\n%s", operatorName, body.Message)
			job := &queue.OutboundJob{
				SessionJID: body.SessionJID, ToJID: toJID, Text: textWithPrefix,
				BitrixConnector: connectorID, BitrixLine: lineID, OperatorName: operatorName,
			}
			if err := h.q.PushOutbound(c.Context(), job); err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "falha ao enfileirar: " + err.Error()})
			}
			return c.JSON(fiber.Map{"status": "queued", "to_jid": toJID, "chat_id": chatID})
		}
		h.log.Info("crm send: registered in Open Lines (Bitrix roteia para WA via connector)",
			zap.String("chat_id", chatID), zap.String("operator", operatorName))
		return c.JSON(fiber.Map{"status": "queued", "to_jid": toJID, "chat_id": chatID, "via": "openlines"})
	}

	// 3. Sem chat ainda — primeira mensagem do contato. Envia direto pelo WhatsApp
	//    com prefixo formatado igual ao Open Lines.
	textWithPrefix := fmt.Sprintf("*%s:*\n%s", operatorName, body.Message)
	job := &queue.OutboundJob{
		SessionJID:      body.SessionJID,
		ToJID:           toJID,
		Text:            textWithPrefix,
		BitrixConnector: connectorID,
		BitrixLine:      lineID,
		OperatorName:    operatorName,
	}
	if err := h.q.PushOutbound(c.Context(), job); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "falha ao enfileirar mensagem: " + err.Error()})
	}

	h.log.Info("crm send: no chat — sent direct to WhatsApp with prefix",
		zap.String("to_jid", toJID),
		zap.String("operator", operatorName),
	)
	return c.JSON(fiber.Map{"status": "queued", "to_jid": toJID, "via": "direct"})
}

// POST /bitrix/crm/upload — recebe arquivo multipart e enfileira envio via WA
// Form fields: domain, phone, session_jid, user_id + file (multipart)
// Mesmo guard de permissao do bitrixCRMSend.
func (h *handlers) bitrixCRMUpload(c *fiber.Ctx) error {
	domain     := c.FormValue("domain")
	phone      := c.FormValue("phone")
	sessionJID := c.FormValue("session_jid")
	caption    := c.FormValue("caption") // texto opcional junto ao arquivo
	userID     := c.FormValue("user_id")

	if domain == "" || phone == "" || sessionJID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "domain, phone e session_jid são obrigatórios"})
	}
	if userID == "" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "user_id ausente — atualize a aba do CRM (Ctrl+Shift+R)",
		})
	}
	ok, err := h.repo.IsSessionAllowed(c.Context(), normalizeDomainKey(domain), userID, sessionJID)
	if err != nil {
		h.log.Error("crm upload: permission check failed", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if !ok {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "voce nao tem permissao pra enviar arquivos com este numero. Peca para um admin liberar.",
		})
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "arquivo obrigatório"})
	}

	portal, err := h.repo.GetBitrixPortalByDomain(c.Context(), normalizePortalDomain(domain))
	if err != nil || portal == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "portal não encontrado"})
	}

	phone = normalizeWAPhone(phone)
	toJID := phone + "@s.whatsapp.net"

	connectorID := portal.ConnectorID
	if connectorID == "" {
		connectorID = "whatsapp_uc_v2"
	}
	lineID := portal.OpenLineID
	if lineID == 0 {
		lineID = 1
	}

	// Lê bytes do arquivo
	f, err := fileHeader.Open()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "erro ao ler arquivo"})
	}
	defer f.Close()

	// Detecta MIME pelo Content-Type do campo ou pelo nome
	mime := fileHeader.Header.Get("Content-Type")
	if mime == "" || mime == "application/octet-stream" {
		mime = guessMime(fileHeader.Filename)
	}

	job := &queue.OutboundJob{
		SessionJID:      sessionJID,
		ToJID:           toJID,
		Text:            caption,
		FileName:        fileHeader.Filename,
		FileMime:        mime,
		BitrixConnector: connectorID,
		BitrixLine:      lineID,
	}

	// Para arquivos pequenos (<= 64 MB) embute os bytes na fila via MediaURL data URI
	const maxEmbed = 64 << 20
	if fileHeader.Size <= maxEmbed {
		buf := make([]byte, fileHeader.Size)
		if _, rerr := f.Read(buf); rerr == nil {
			job.MediaURL = "data:" + mime + ";base64," + b64Encode(buf)
		}
	}

	if err := h.q.PushOutbound(c.Context(), job); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	h.log.Info("crm upload queued",
		zap.String("file", fileHeader.Filename),
		zap.String("to", toJID),
		zap.String("session", sessionJID),
	)
	return c.JSON(fiber.Map{"status": "queued", "file": fileHeader.Filename})
}

// GET /bitrix/crm/history?domain=...&entity_type=contact&entity_id=...&phone=...&limit=80
// Busca histórico do banco local (fonte primária) + fallback Bitrix API.
func (h *handlers) bitrixCRMHistory(c *fiber.Ctx) error {
	domain     := c.Query("domain")
	entityType := strings.ToLower(c.Query("entity_type", "contact"))
	entityID   := c.Query("entity_id")
	phone      := c.Query("phone") // telefone já conhecido pelo frontend

	if domain == "" || entityID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "domain e entity_id são obrigatórios"})
	}

	// Default 200, max 1000. Conversas longas precisam de cap maior — antes
	// limitava a 200 e usuario via historico cortado.
	limit := 200
	if l := c.QueryInt("limit", 200); l > 0 && l <= 1000 {
		limit = l
	}

	// ── Fonte 1: banco local ──────────────────────────────────────────────
	if phone != "" {
		phoneNorm := normalizeWAPhone(phone)
		localMsgs, dbErr := h.repo.GetMessagesByPhone(c.Context(), phoneNorm, limit)
		h.log.Info("crm history: local db query",
			zap.String("phone_raw", phone),
			zap.String("phone_norm", phoneNorm),
			zap.Int("count", len(localMsgs)),
			zap.Error(dbErr),
		)
		if dbErr == nil && len(localMsgs) > 0 {
			return c.JSON(fiber.Map{
				"messages": localMsgsToCRM(localMsgs),
				"count":    len(localMsgs),
				"source":   "local",
			})
		}
		// Se houver erro no banco, loga e faz fallback para Bitrix (nunca retorna erro ao cliente)
		if dbErr != nil {
			h.log.Error("crm history: local db error, falling back to bitrix", zap.String("phone", phoneNorm), zap.Error(dbErr))
		}
	}

	// ── Fonte 2: Bitrix API ───────────────────────────────────────────────
	portal, err := h.repo.GetBitrixPortalByDomain(c.Context(), normalizePortalDomain(domain))
	if err != nil || portal == nil {
		return c.JSON(fiber.Map{"messages": []interface{}{}, "count": 0})
	}
	creds := h.portalToCreds(portal)
	bxEntityType := strings.ToUpper(entityType)

	// Tenta getLastId primeiro, depois crm.chat.get
	chatID, _ := h.bitrixClient.GetCRMChatLastID(c.Context(), creds, bxEntityType, entityID)
	if chatID == "" {
		if chatsRaw, e := h.bitrixClient.GetCRMChats(c.Context(), creds, bxEntityType, entityID); e == nil {
			chatID = extractChatID(chatsRaw)
		}
	}
	h.log.Info("crm history: bitrix chat_id", zap.String("chat_id", chatID), zap.String("entity", entityID))

	if chatID == "" {
		h.log.Warn("crm history: no chat_id found in bitrix", zap.String("entity_id", entityID))
		return c.JSON(fiber.Map{"messages": []interface{}{}, "count": 0, "source": "none"})
	}

	msgsRaw, err := h.bitrixClient.GetSessionHistory(c.Context(), creds, chatID, limit)
	if err != nil {
		h.log.Warn("crm history: GetSessionHistory failed, trying GetChatMessages", zap.String("chat_id", chatID), zap.Error(err))
		msgsRaw, err = h.bitrixClient.GetChatMessages(c.Context(), creds, chatID, limit)
		if err != nil {
			h.log.Error("crm history: both history methods failed", zap.String("chat_id", chatID), zap.Error(err))
			return c.JSON(fiber.Map{"messages": []interface{}{}, "count": 0, "source": "error"})
		}
	}

	msgs := parseSessionHistory(msgsRaw, portal.ConnectorID)
	if len(msgs) == 0 {
		msgs = parseBitrixMessages(msgsRaw, portal.ConnectorID)
	}

	h.log.Info("crm history: from bitrix", zap.Int("count", len(msgs)))
	return c.JSON(fiber.Map{"messages": msgs, "count": len(msgs), "source": "bitrix", "chat_id": chatID})
}

// localMsgsToCRM converte []db.Message para o formato crmMessage do frontend.
// Preenche session_jid/session_phone/kind com base no lado "nosso" da msg:
//   - outbound: from_jid (a sessao enviou)
//   - inbound:  to_jid   (a sessao recebeu)
func localMsgsToCRM(msgs []db.Message) []crmMessage {
	out := make([]crmMessage, 0, len(msgs))
	for _, m := range msgs {
		msgType := string(m.MessageType)
		if msgType == "" {
			msgType = "text"
		}
		var ourJID string
		if m.Direction == db.DirOutbound {
			ourJID = m.FromJID
		} else {
			ourJID = m.ToJID
		}
		kind, phone := classifySessionJID(ourJID)
		out = append(out, crmMessage{
			ID:           m.WAMessageID,
			Direction:    string(m.Direction),
			Type:         msgType,
			Content:      m.Content,
			MediaURL:     m.MediaURL,
			MediaMime:    m.MediaMime,
			AuthorName:   m.AuthorName,
			Status:       string(m.Status),
			CreatedAt:    m.CreatedAt.Format("2006-01-02T15:04:05Z"),
			SessionJID:   ourJID,
			SessionPhone: phone,
			Kind:         kind,
		})
	}
	return out
}

// classifySessionJID extrai (kind, phone) de um session_jid do nosso lado.
// Aceita formatos:
//   - "cloud:1160607470462388@s.whatsapp.net" -> ("cloud", "1160607470462388")
//   - "cloud@s.whatsapp.net"                  -> ("cloud", "")        (lixo historico)
//   - "5519910001772:88@s.whatsapp.net"       -> ("qr", "5519910001772")
//   - "5519910001772@s.whatsapp.net"          -> ("qr", "5519910001772")
//   - ""                                       -> ("", "")
func classifySessionJID(jid string) (kind, phone string) {
	if jid == "" {
		return "", ""
	}
	if strings.HasPrefix(jid, "cloud:") {
		// cloud:<id>@s.whatsapp.net
		rest := strings.TrimPrefix(jid, "cloud:")
		if at := strings.Index(rest, "@"); at != -1 {
			rest = rest[:at]
		}
		return "cloud", rest
	}
	if strings.HasPrefix(jid, "cloud@") {
		return "cloud", "" // lixo do bug stripDeviceSuffix antigo
	}
	// QR: pode ter device suffix ":NN"
	at := strings.Index(jid, "@")
	if at == -1 {
		return "qr", jid
	}
	base := jid[:at]
	if colon := strings.Index(base, ":"); colon != -1 {
		base = base[:colon]
	}
	return "qr", base
}

// extractChatID tenta extrair o CHAT_ID de várias estruturas possíveis de resposta.
func extractChatID(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "false" {
		return ""
	}
	// Tenta objeto direto: {"CHAT_ID": 123}
	var obj struct {
		ChatID interface{} `json:"CHAT_ID"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.ChatID != nil {
		return fmt.Sprintf("%v", obj.ChatID)
	}
	// Tenta array: [{"CHAT_ID": 123}]
	var arr []struct {
		ChatID interface{} `json:"CHAT_ID"`
	}
	if json.Unmarshal(raw, &arr) == nil && len(arr) > 0 && arr[0].ChatID != nil {
		return fmt.Sprintf("%v", arr[0].ChatID)
	}
	// Tenta result wrapper: {"result": {"CHAT_ID": 123}}
	var wrapped struct {
		Result struct {
			ChatID interface{} `json:"CHAT_ID"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &wrapped) == nil && wrapped.Result.ChatID != nil {
		return fmt.Sprintf("%v", wrapped.Result.ChatID)
	}
	return ""
}

// extractChatIDFromRecent percorre im.recent.list procurando um chat cujo título/nome contém o telefone.
func extractChatIDFromRecent(raw json.RawMessage, phone string) string {
	var items []struct {
		ID    interface{} `json:"ID"`
		Title string      `json:"TITLE"`
		Name  string      `json:"NAME"`
		Type  string      `json:"TYPE"`
	}
	// Tenta array direto
	if json.Unmarshal(raw, &items) != nil {
		// Tenta {result: [...]}
		var w struct {
			Result []struct {
				ID    interface{} `json:"ID"`
				Title string      `json:"TITLE"`
				Name  string      `json:"NAME"`
				Type  string      `json:"TYPE"`
			} `json:"result"`
		}
		if json.Unmarshal(raw, &w) == nil {
			for _, it := range w.Result {
				if strings.Contains(it.Title, phone) || strings.Contains(it.Name, phone) {
					return fmt.Sprintf("%v", it.ID)
				}
			}
		}
		return ""
	}
	for _, it := range items {
		if strings.Contains(it.Title, phone) || strings.Contains(it.Name, phone) {
			return fmt.Sprintf("%v", it.ID)
		}
	}
	return ""
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// parseSessionHistory converte a resposta de imopenlines.session.history.get para []crmMessage.
// Resposta: {"result": {"chatId":"X","message":{"ID":{"id":"X","senderid":"X","date":"...","text":"..."}},"users":{...}}}
func parseSessionHistory(raw json.RawMessage, connectorID string) []crmMessage {
	// Desempacota result wrapper
	var wrapper struct {
		Result json.RawMessage `json:"result"`
	}
	data := raw
	if json.Unmarshal(raw, &wrapper) == nil && len(wrapper.Result) > 0 {
		data = wrapper.Result
	}

	var resp struct {
		ChatID    interface{}                `json:"chatId"`
		SessionID interface{}                `json:"sessionId"`
		Message   map[string]struct {
			ID       interface{} `json:"id"`
			SenderID interface{} `json:"senderid"`
			Date     string      `json:"date"`
			Text     string      `json:"text"`
			Params   struct {
				ConnectorMID string `json:"CONNECTOR_MID"`
			} `json:"params"`
		} `json:"message"`
		Users map[string]struct {
			Name string `json:"name"`
		} `json:"users"`
	}

	if err := json.Unmarshal(data, &resp); err != nil || len(resp.Message) == 0 {
		return nil
	}

	// Coleta e ordena por ID numérico
	type msgEntry struct {
		numID int64
		msg   crmMessage
	}
	entries := make([]msgEntry, 0, len(resp.Message))
	for _, m := range resp.Message {
		senderID := fmt.Sprintf("%v", m.SenderID)
		direction := "outbound"
		if m.Params.ConnectorMID != "" {
			direction = "inbound"
		}
		authorName := ""
		if u, ok := resp.Users[senderID]; ok {
			authorName = u.Name
		}
		numID, _ := strconv.ParseInt(fmt.Sprintf("%v", m.ID), 10, 64)
		entries = append(entries, msgEntry{
			numID: numID,
			msg: crmMessage{
				ID:         fmt.Sprintf("%v", m.ID),
				Direction:  direction,
				Type:       "text",
				Content:    m.Text,
				AuthorID:   senderID,
				AuthorName: authorName,
				Status:     "delivered",
				CreatedAt:  m.Date,
			},
		})
	}
	// Ordena por ID crescente (cronológico)
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j].numID < entries[j-1].numID; j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}
	out := make([]crmMessage, len(entries))
	for i, e := range entries {
		out[i] = e.msg
	}
	return out
}

type crmMessage struct {
	ID         string `json:"id"`
	Direction  string `json:"direction"` // inbound | outbound
	Type       string `json:"type"`
	Content    string `json:"content"`
	MediaURL   string `json:"media_url,omitempty"`
	MediaMime  string `json:"media_mime,omitempty"`
	AuthorID   string `json:"author_id,omitempty"`
	AuthorName string `json:"author_name,omitempty"`
	Status     string `json:"status"`
	CreatedAt  string `json:"created_at"`
	// Vinculo da conexao usada — permite separar chats por sessao quando o
	// mesmo contato falou com varios numeros do mesmo portal.
	SessionJID   string `json:"session_jid,omitempty"`   // ex: "cloud:1160...@s.whatsapp.net" ou "5519910001772@s.whatsapp.net"
	SessionPhone string `json:"session_phone,omitempty"` // numero E.164 da sessao (nosso lado)
	Kind         string `json:"kind,omitempty"`          // "cloud" | "qr"
}

// parseBitrixMessages converte a resposta do im.dialog.messages.get para []crmMessage.
// O Bitrix retorna: {"messages": [{ID, AUTHOR_ID, DATE, MESSAGE, ATTACH, ...}], "users": {...}}
func parseBitrixMessages(raw json.RawMessage, connectorID string) []crmMessage {
	var resp struct {
		Messages []struct {
			ID       interface{} `json:"ID"`
			AuthorID interface{} `json:"AUTHOR_ID"`
			Date     string      `json:"DATE"`
			Message  string      `json:"MESSAGE"`
			Attach   interface{} `json:"ATTACH"`
			IsSystem interface{} `json:"IS_SYSTEM"`
			Params   struct {
				ConnectorMID string `json:"CONNECTOR_MID"`
			} `json:"PARAMS"`
		} `json:"messages"`
		Users map[string]struct {
			Name string `json:"name"`
		} `json:"users"`
	}

	if err := json.Unmarshal(raw, &resp); err != nil {
		// Tenta via result wrapper
		var wrapper struct {
			Result json.RawMessage `json:"result"`
		}
		if json.Unmarshal(raw, &wrapper) == nil {
			json.Unmarshal(wrapper.Result, &resp)
		}
	}

	out := make([]crmMessage, 0, len(resp.Messages))
	for _, m := range resp.Messages {
		// Pula mensagens de sistema
		if fmt.Sprintf("%v", m.IsSystem) == "true" || fmt.Sprintf("%v", m.IsSystem) == "1" {
			continue
		}

		authorID := fmt.Sprintf("%v", m.AuthorID)
		// Mensagens com CONNECTOR_MID são as que vieram do WhatsApp (inbound do cliente)
		// Mensagens dos operadores (users do Bitrix) são outbound
		direction := "outbound"
		if m.Params.ConnectorMID != "" {
			direction = "inbound"
		}

		authorName := ""
		if u, ok := resp.Users[authorID]; ok {
			authorName = u.Name
		}

		// Detecta mídia no ATTACH
		mediaURL, mediaMime, msgType := "", "", "text"
		if m.Attach != nil {
			attachJSON, _ := json.Marshal(m.Attach)
			var attaches []struct {
				Type  string `json:"type"`
				Link  string `json:"link"`
				Value string `json:"value"`
			}
			if json.Unmarshal(attachJSON, &attaches) == nil {
				for _, a := range attaches {
					if a.Type == "image" || a.Type == "file" || a.Type == "video" || a.Type == "audio" {
						mediaURL = a.Link
						if a.Type == "image" { mediaMime = "image/jpeg"; msgType = "image" }
						if a.Type == "video" { mediaMime = "video/mp4";  msgType = "video" }
						if a.Type == "audio" { mediaMime = "audio/ogg";  msgType = "audio" }
						if a.Type == "file"  { msgType = "document" }
						break
					}
				}
			}
		}

		out = append(out, crmMessage{
			ID:         fmt.Sprintf("%v", m.ID),
			Direction:  direction,
			Type:       msgType,
			Content:    m.Message,
			MediaURL:   mediaURL,
			MediaMime:  mediaMime,
			AuthorID:   authorID,
			AuthorName: authorName,
			Status:     "delivered",
			CreatedAt:  m.Date,
		})
	}
	return out
}

// GET /bitrix/crm/debug?phone=5519987717792 — diagnóstico: estado do banco e mensagens salvas
func (h *handlers) bitrixCRMDebug(c *fiber.Ctx) error {
	phone := normalizeWAPhone(c.Query("phone", ""))

	stats, err := h.repo.DebugMessageStats(c.Context(), phone)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(stats)
}


// GET /bitrix/crm/sessions?domain=... — lista sessões WA do tenant para o
// seletor do CRM tab. Le do banco (status='active' + bitrix_accounts.domain)
// para incluir Cloud API junto de QR — antes lia so de h.waManager que e
// whatsmeow puro e ignorava Cloud, deixando tenants Cloud como "Desconectado".
//
// Sem ?domain= cai no comportamento antigo (so QR conectadas em memoria).
func (h *handlers) bitrixCRMSessions(c *fiber.Ctx) error {
	domain := strings.TrimSpace(c.Query("domain"))
	if domain == "" {
		sessions := h.waManager.ListSessions()
		return c.JSON(fiber.Map{"sessions": sessions, "count": len(sessions)})
	}

	rows, err := h.repo.ListActiveSessionsByDomain(c.Context(), normalizePortalDomain(domain))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	out := make([]fiber.Map, 0, len(rows))
	for _, s := range rows {
		phone := s.Phone
		if phone == "" {
			phone = s.CloudDisplayPhone
		}
		if phone == "" {
			// fallback final: extrai do JID (parte antes de @, strip "cloud:" e ":NN")
			j := strings.TrimPrefix(s.JID, "cloud:")
			if at := strings.IndexByte(j, '@'); at >= 0 {
				j = j[:at]
			}
			if col := strings.IndexByte(j, ':'); col >= 0 {
				j = j[:col]
			}
			phone = j
		}
		out = append(out, fiber.Map{
			"jid":   s.JID,
			"phone": phone,
			"type":  s.Type,
			"label": s.DisplayName,
		})
	}
	return c.JSON(fiber.Map{"sessions": out, "count": len(out)})
}

// GET /bitrix/crm/lines?domain=... — lista Open Lines do portal
func (h *handlers) bitrixCRMLines(c *fiber.Ctx) error {
	domain := c.Query("domain")
	if domain == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "domain obrigatório"})
	}
	portal, err := h.repo.GetBitrixPortalByDomain(c.Context(), normalizePortalDomain(domain))
	if err != nil || portal == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "portal não encontrado"})
	}
	creds := h.portalToCreds(portal)
	raw, err := h.bitrixClient.ListOpenLines(c.Context(), creds)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Type("json").Send(raw)
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func jsonStr(obj map[string]json.RawMessage, key string) string {
	v, ok := obj[key]
	if !ok {
		return ""
	}
	var s string
	if json.Unmarshal(v, &s) == nil {
		return s
	}
	return ""
}

func extractPhone(obj map[string]json.RawMessage) string {
	v, ok := obj["PHONE"]
	if !ok {
		return ""
	}
	var phones []struct {
		Value     string `json:"VALUE"`
		ValueType string `json:"VALUE_TYPE"`
	}
	if json.Unmarshal(v, &phones) != nil || len(phones) == 0 {
		return ""
	}
	return phones[0].Value
}

func b64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

func guessMime(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	m := map[string]string{
		".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png",
		".gif": "image/gif", ".webp": "image/webp",
		".mp4": "video/mp4", ".mov": "video/quicktime", ".avi": "video/x-msvideo",
		".mp3": "audio/mpeg", ".ogg": "audio/ogg", ".wav": "audio/wav", ".m4a": "audio/mp4",
		".pdf": "application/pdf",
		".doc": "application/msword",
		".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		".xls": "application/vnd.ms-excel",
		".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		".zip": "application/zip",
	}
	if v, ok := m[ext]; ok {
		return v
	}
	return "application/octet-stream"
}

func normalizeWAPhone(phone string) string {
	var out strings.Builder
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			out.WriteRune(r)
		}
	}
	return out.String()
}

// ─── HTML da aba CRM ─────────────────────────────────────────────────────────

// portalToCreds já está definido em partner.go

var crmTabHTML = `<!DOCTYPE html>
<html lang="pt-BR">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>UC Talk</title>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link href="https://fonts.googleapis.com/css2?family=Plus+Jakarta+Sans:wght@400;500;600;700&display=swap" rel="stylesheet">
<style>
*{box-sizing:border-box;margin:0;padding:0}
html,body{height:100%;overflow:hidden}
body{font-family:'Plus Jakarta Sans',sans-serif;background:#1a2234;color:#e2e8f0;font-size:13px;display:flex;flex-direction:column;height:100%}

/* ══ TOPO: operador + seletor de número ══ */
.topbar{background:#252f3e;border-bottom:1px solid #2d3a4e;padding:8px 14px;display:flex;align-items:center;gap:10px;flex-shrink:0;min-height:52px}
.op-avatar{width:32px;height:32px;border-radius:50%;background:#334155;display:flex;align-items:center;justify-content:center;font-size:13px;font-weight:700;color:#94a3b8;flex-shrink:0;text-transform:uppercase}
.op-info{flex:1;min-width:0}
.op-name{font-size:13px;font-weight:600;color:#f1f5f9;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.op-email{font-size:10px;color:#64748b;margin-top:1px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.wa-selector{display:flex;align-items:center;gap:6px;background:#1a2436;border:1px solid #334155;border-radius:8px;padding:4px 10px;cursor:pointer;flex-shrink:0;position:relative}
.wa-selector-icon{width:18px;height:18px;background:#25D366;border-radius:50%;display:flex;align-items:center;justify-content:center;flex-shrink:0}
.wa-selector-icon svg{width:11px;height:11px}
.wa-selector-label{font-size:12px;font-weight:600;color:#e2e8f0;max-width:130px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.wa-selector-arrow{color:#64748b;font-size:10px;margin-left:2px}
.wa-dropdown{position:absolute;top:calc(100% + 6px);right:0;background:#252f3e;border:1px solid #334155;border-radius:10px;min-width:220px;box-shadow:0 8px 24px rgba(0,0,0,.4);z-index:100;overflow:hidden;display:none}
.wa-dropdown.open{display:block}
.wa-drop-item{display:flex;align-items:center;gap:10px;padding:10px 14px;cursor:pointer;transition:background .12s}
.wa-drop-item:hover{background:#334155}
.wa-drop-item.active{background:#1a3a2a}
.wa-drop-num{font-size:13px;font-weight:600;color:#f1f5f9}
.wa-drop-jid{font-size:10px;color:#64748b;margin-top:1px}
.wa-dot{width:8px;height:8px;border-radius:50%;background:#25D366;flex-shrink:0}
.logo-badge{display:flex;align-items:center;gap:5px;color:#25D366;font-weight:700;font-size:12px;margin-left:4px;flex-shrink:0}
.logo-badge svg{width:18px;height:18px}

/* ══ LAYOUT PRINCIPAL: dois painéis ══ */
.main{flex:1;display:flex;overflow:hidden}

/* ── Painel esquerdo: conversas ── */
.sidebar{width:260px;flex-shrink:0;background:#1e2736;border-right:1px solid #2d3a4e;display:flex;flex-direction:column;overflow:hidden}
.sidebar-header{padding:10px 12px;border-bottom:1px solid #2d3a4e;flex-shrink:0}
.sidebar-title{font-size:12px;font-weight:700;color:#94a3b8;text-transform:uppercase;letter-spacing:.06em;margin-bottom:8px}
.search-box{display:flex;align-items:center;gap:6px;background:#252f3e;border:1px solid #2d3a4e;border-radius:8px;padding:6px 10px}
.search-box svg{color:#64748b;flex-shrink:0}
.search-box input{background:none;border:none;outline:none;color:#f1f5f9;font-size:12px;font-family:inherit;width:100%}
.search-box input::placeholder{color:#475569}
.conv-list{flex:1;overflow-y:auto;padding:4px 0}
.conv-list::-webkit-scrollbar{width:3px}
.conv-list::-webkit-scrollbar-thumb{background:#334155;border-radius:3px}
.conv-item{display:flex;align-items:center;gap:10px;padding:9px 12px;cursor:pointer;transition:background .12s;border-left:3px solid transparent}
.conv-item:hover{background:#252f3e}
.conv-item.active{background:#1a3a2a;border-left-color:#25D366}
.conv-avatar{width:34px;height:34px;border-radius:50%;background:#334155;display:flex;align-items:center;justify-content:center;font-size:12px;font-weight:700;color:#94a3b8;flex-shrink:0;text-transform:uppercase}
.conv-body{flex:1;min-width:0}
.conv-name{font-size:13px;font-weight:600;color:#f1f5f9;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.conv-preview{font-size:11px;color:#64748b;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;margin-top:1px}
.conv-meta{display:flex;flex-direction:column;align-items:flex-end;gap:3px;flex-shrink:0}
.conv-time{font-size:10px;color:#475569}
.conv-badge{background:#25D366;color:#fff;font-size:9px;font-weight:700;border-radius:99px;padding:1px 5px;min-width:16px;text-align:center}
.conv-empty{padding:20px;text-align:center;color:#475569;font-size:12px}

/* ── Painel direito: chat ── */
.chat-panel{flex:1;display:flex;flex-direction:column;overflow:hidden;background:#111827}

/* cabeçalho do contato */
.chat-header{background:#1e2736;border-bottom:1px solid #2d3a4e;padding:10px 16px;display:flex;align-items:center;gap:10px;flex-shrink:0}
.chat-hdr-avatar{width:36px;height:36px;border-radius:50%;background:#334155;display:flex;align-items:center;justify-content:center;font-size:14px;font-weight:700;color:#94a3b8;text-transform:uppercase;flex-shrink:0}
.chat-hdr-info{flex:1;min-width:0}
.chat-hdr-name{font-size:14px;font-weight:700;color:#f1f5f9}
.chat-hdr-phone{font-size:11px;color:#64748b;margin-top:1px}
.btn-icon{background:none;border:none;cursor:pointer;color:#64748b;padding:4px;border-radius:6px;transition:color .15s,background .15s;display:flex;align-items:center}
.btn-icon:hover{color:#e2e8f0;background:#334155}

/* tabs de vinculos (quando o mesmo contato falou em varios numeros) */
.session-tabs{display:flex;gap:0;border-bottom:1px solid #334155;background:#0f172a;padding:0 12px;overflow-x:auto;flex-shrink:0}
.session-tabs::-webkit-scrollbar{height:3px}
.session-tabs::-webkit-scrollbar-thumb{background:#334155;border-radius:3px}
.session-tab{padding:8px 14px;background:transparent;border:0;border-bottom:2px solid transparent;color:#64748b;font-size:11.5px;font-weight:600;cursor:pointer;white-space:nowrap;display:flex;align-items:center;gap:6px;font-family:inherit;letter-spacing:.02em}
.session-tab:hover:not(.active){color:#cbd5e1}
.session-tab.active{color:#e2e8f0;border-bottom-color:#3b82f6}
.session-tab .badge-kind{font-size:9px;padding:1px 5px;border-radius:6px;font-weight:700;letter-spacing:.04em}
.session-tab .badge-kind.cloud{background:rgba(59,130,246,.2);color:#60a5fa}
.session-tab .badge-kind.qr{background:rgba(37,211,102,.18);color:#25D366}
.session-tab .count{font-size:10px;color:#475569;font-weight:500}

/* área de mensagens */
.chat-body{flex:1;overflow-y:auto;padding:14px 16px;display:flex;flex-direction:column;gap:4px}
.chat-body::-webkit-scrollbar{width:4px}
.chat-body::-webkit-scrollbar-thumb{background:#334155;border-radius:4px}
.chat-placeholder{flex:1;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:12px;color:#475569;text-align:center;padding:20px}
.chat-placeholder svg{opacity:.3}
.chat-placeholder p{font-size:13px}
.chat-placeholder .sub{font-size:11px;color:#334155}

.day-div{text-align:center;margin:8px 0}
.day-div span{background:#1e2736;color:#64748b;font-size:10px;font-weight:600;padding:3px 10px;border-radius:99px}

.bw{display:flex;flex-direction:column;max-width:72%}
.bw.out{align-self:flex-end;align-items:flex-end}
.bw.in{align-self:flex-start;align-items:flex-start}
.bubble{padding:8px 12px;border-radius:14px;font-size:13px;line-height:1.5;word-break:break-word}
.bubble.out{background:#005c4b;color:#e2e8f0;border-bottom-right-radius:3px}
.bubble.in{background:#1e2736;color:#e2e8f0;border-bottom-left-radius:3px;border:1px solid #2d3a4e}
.bmeta{display:flex;align-items:center;gap:3px;margin-top:2px;padding:0 2px}
.btime{font-size:10px;color:#64748b}
.bst{font-size:11px;line-height:1}
.bst.sent{color:#94a3b8}.bst.delivered{color:#94a3b8}.bst.failed{color:#f87171}
.bmedia{display:flex;align-items:center;gap:5px;color:#94a3b8;font-size:11px;font-style:italic;margin-bottom:2px}

/* compositor */
.composer{background:#1e2736;border-top:1px solid #2d3a4e;padding:8px 12px;display:flex;flex-direction:column;gap:6px;flex-shrink:0}
.composer-row{display:flex;gap:8px;align-items:flex-end}
.composer textarea{flex:1;background:#252f3e;border:1px solid #334155;border-radius:12px;padding:9px 12px;color:#f1f5f9;font-size:13px;font-family:inherit;outline:none;resize:none;max-height:120px;min-height:38px;line-height:1.4;transition:border-color .2s}
.composer textarea:focus{border-color:#25D366}
.composer textarea::placeholder{color:#475569}
.composer textarea:disabled{opacity:.4}
.btn-attach{background:none;border:1px solid #334155;border-radius:50%;width:38px;height:38px;display:flex;align-items:center;justify-content:center;cursor:pointer;flex-shrink:0;color:#64748b;transition:all .15s}
.btn-attach:hover{border-color:#25D366;color:#25D366}
.btn-attach:disabled{opacity:.3;cursor:not-allowed}
.btn-send{background:#25D366;border:none;border-radius:50%;width:38px;height:38px;display:flex;align-items:center;justify-content:center;cursor:pointer;flex-shrink:0;transition:opacity .15s}
.btn-send:hover{opacity:.85}
.btn-send:disabled{opacity:.4;cursor:not-allowed}
.file-preview{display:none;align-items:center;gap:8px;background:#252f3e;border:1px solid #334155;border-radius:10px;padding:7px 10px;font-size:12px;color:#94a3b8}
.file-preview.show{display:flex}
.file-preview-name{flex:1;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;color:#e2e8f0}
.file-preview-rm{background:none;border:none;color:#64748b;cursor:pointer;padding:0;display:flex;align-items:center}
.file-preview-rm:hover{color:#f87171}
/* spinner */
.spinner{width:22px;height:22px;border:2px solid #2d3a4e;border-top-color:#25D366;border-radius:50%;animation:spin .7s linear infinite}
@keyframes spin{to{transform:rotate(360deg)}}

/* alert inline */
.inline-alert{padding:7px 12px;border-radius:8px;font-size:11px;margin:4px 14px;flex-shrink:0;display:none}
.inline-alert.error{background:#450a0a;color:#f87171;border:1px solid #7f1d1d;display:block}
.inline-alert.success{background:#14532d;color:#4ade80;display:block}
</style>
</head>
<body>

<!-- ══ TOPO: operador + seletor de número WA ══ -->
<div class="topbar">
  <div class="op-avatar" id="op-initials">?</div>
  <div class="op-info">
    <div class="op-name" id="op-name">Carregando...</div>
    <div class="op-email" id="op-email"></div>
  </div>
  <div class="wa-selector" id="wa-selector" onclick="toggleWADropdown()">
    <div class="wa-selector-icon">
      <svg viewBox="0 0 24 24" fill="white"><path d="M12 2C6.48 2 2 6.48 2 12c0 1.82.49 3.53 1.34 5L2 22l5.15-1.32A10 10 0 1012 2z"/></svg>
    </div>
    <span class="wa-selector-label" id="wa-sel-label">Carregando...</span>
    <span class="wa-selector-arrow">▾</span>
    <div class="wa-dropdown" id="wa-dropdown"></div>
  </div>
  <div class="logo-badge">
    <svg viewBox="0 0 24 24" fill="#25D366"><path d="M12 2C6.48 2 2 6.48 2 12c0 1.82.49 3.53 1.34 5L2 22l5.15-1.32A10 10 0 1012 2z"/></svg>
    UC Talk
  </div>
</div>

<!-- ══ CORPO: sidebar + chat ══ -->
<div class="main">

  <!-- ── Sidebar: lista de conversas ── -->
  <div class="sidebar">
    <div class="sidebar-header">
      <div class="sidebar-title">Conversas</div>
      <div class="search-box">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
        <input type="text" id="conv-search" placeholder="Pesquisar..." oninput="filterConvs()">
      </div>
    </div>
    <div class="conv-list" id="conv-list">
      <div style="padding:16px;text-align:center"><div class="spinner" style="margin:auto"></div></div>
    </div>
  </div>

  <!-- ── Painel de chat ── -->
  <div class="chat-panel">

    <!-- cabeçalho do contato ativo -->
    <div class="chat-header">
      <div class="chat-hdr-avatar" id="chat-hdr-avatar">?</div>
      <div class="chat-hdr-info">
        <div class="chat-hdr-name" id="chat-hdr-name">Selecione uma conversa</div>
        <div class="chat-hdr-phone" id="chat-hdr-phone"></div>
      </div>
      <button class="btn-icon" title="Atualizar" onclick="reloadChat()">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="23 4 23 10 17 10"/><polyline points="1 20 1 14 7 14"/><path d="M3.51 9a9 9 0 0114.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0020.49 15"/></svg>
      </button>
    </div>

    <!-- tabs por vinculo (oculto quando so 1 sessao) -->
    <div class="session-tabs" id="session-tabs" style="display:none"></div>

    <!-- mensagens -->
    <div class="chat-body" id="chat-body">
      <div class="chat-placeholder">
        <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M21 15a2 2 0 01-2 2H7l-4 4V5a2 2 0 012-2h14a2 2 0 012 2z"/></svg>
        <p>Selecione uma conversa<br>ou inicie uma nova</p>
      </div>
    </div>

    <!-- alerta inline -->
    <div class="inline-alert" id="inline-alert"></div>

    <!-- compositor -->
    <div class="composer">
      <!-- preview do arquivo selecionado -->
      <div class="file-preview" id="file-preview">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>
        <span class="file-preview-name" id="file-preview-name"></span>
        <button class="file-preview-rm" onclick="clearFile()" title="Remover arquivo">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
        </button>
      </div>
      <!-- Popup de templates (aparece acima do compositor) -->
      <div id="tpl-popup" style="display:none;position:absolute;bottom:64px;left:14px;right:14px;background:#0f172a;border:1px solid #334155;border-radius:10px;box-shadow:0 -8px 24px rgba(0,0,0,.5);max-height:260px;overflow-y:auto;z-index:20;">
        <div style="padding:8px 12px;display:flex;justify-content:space-between;align-items:center;border-bottom:1px solid rgba(255,255,255,.06);">
          <div style="font-size:11px;font-weight:700;color:#94a3b8;text-transform:uppercase;letter-spacing:.06em;">Templates</div>
          <button onclick="fecharTplPopup()" style="background:none;border:0;color:#64748b;cursor:pointer;font-size:14px;">✕</button>
        </div>
        <div id="tpl-popup-list">
          <div style="padding:18px;text-align:center;color:#475569;font-size:12px;">Carregando...</div>
        </div>
      </div>
      <div class="composer-row" style="position:relative;">
        <input type="file" id="file-input" style="display:none" onchange="onFileSelected(this)">
        <button class="btn-attach" id="btn-tpl" onclick="abrirTplPopup()" title="Templates de mensagem" disabled>
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="8" y1="13" x2="16" y2="13"/><line x1="8" y1="17" x2="13" y2="17"/></svg>
        </button>
        <button class="btn-attach" id="btn-attach" onclick="document.getElementById('file-input').click()" title="Anexar arquivo" disabled>
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21.44 11.05l-9.19 9.19a6 6 0 01-8.49-8.49l9.19-9.19a4 4 0 015.66 5.66l-9.2 9.19a2 2 0 01-2.83-2.83l8.49-8.48"/></svg>
        </button>
        <textarea id="msg-input" placeholder="Mensagem..." rows="1" disabled
          onkeydown="onKey(event)" oninput="autoResize(this)"></textarea>
        <button class="btn-send" id="btn-send" onclick="sendOrUpload()" title="Enviar" disabled>
          <svg width="18" height="18" viewBox="0 0 24 24" fill="white"><path d="M2 21l21-9L2 3v7l15 2-15 2z"/></svg>
        </button>
      </div>
    </div>

  </div>
</div>

<script src="https://api.bitrix24.com/api/v1/"></script>
<script>
var _domain = '', _entityType = 'contact', _entityId = '', _userID = '';
var _allowedSessions = null; // Set de session_jids liberados pra este user (null=ainda carregando)
var _contactName = '', _contactPhone = '', _contactInitials = '?';
var _baseUrl = window.location.origin;
var _sessions = [];
var _activeSession = '';
var _pollTimer = null;
var _lastMsgId = '';
var _allConvs = [];
var _placementRaw = null;

// ── Inicialização ────────────────────────────────────────────────────────
function init() {
  BX24.init(function() {
    var p = BX24.placement.info();

    // Salva raw para debug
    _placementRaw = p;

    // Tenta extrair entity_id de todas as formas que o Bitrix pode enviar
    var opts = p.options || {};

    // 1. CRM_CONTACT_DETAIL_TAB → opts.ID (maiúsculo)
    // 2. CRM_CONTACT_DETAIL_TAB → opts.id (minúsculo)
    // 3. opts.entityTypeName + opts.id (formato antigo)
    // 4. p.ID direto
    _entityId = String(opts.ID || opts.id || opts.ENTITY_ID || p.ID || '').replace(/\D/g,'');

    // entityTypeName pode vir como "contact", "CONTACT", "CRM_CONTACT" etc.
    var rawType = (opts.entityTypeName || opts.ENTITY_TYPE_NAME || opts.CRM_ENTITY_TYPE || p.placement || '').toLowerCase();
    if      (rawType.indexOf('lead')    >= 0) _entityType = 'lead';
    else if (rawType.indexOf('deal')    >= 0) _entityType = 'deal';
    else if (rawType.indexOf('contact') >= 0) _entityType = 'contact';
    else _entityType = 'contact'; // default

    // Domain
    _domain = (BX24.getDomain ? BX24.getDomain() : '') || opts.domain || p.domain || '';

    // Mostra debug discreto na sidebar se entity_id estiver vazio
    if (!_entityId) {
      document.getElementById('conv-list').innerHTML =
        '<div class="conv-empty" style="font-size:10px;color:#334155;text-align:left;padding:10px 12px;word-break:break-all">'
        + 'placement: ' + (p.placement||'—') + '<br>'
        + 'options: ' + JSON.stringify(opts).slice(0,200)
        + '</div>';
    }

    // Info do operador logado via BX24.js — depois disso, checa permissao
    BX24.callMethod('profile', {}, function(res) {
      var u = res.data() || {};
      var name  = [u.NAME, u.LAST_NAME].filter(Boolean).join(' ') || u.ID || 'Operador';
      var email = u.EMAIL || '';
      var userID = String(u.ID || '');
      _userID = userID;
      document.getElementById('op-name').textContent     = name;
      document.getElementById('op-email').textContent    = email;
      document.getElementById('op-initials').textContent = initials(name);

      if (!userID || !_domain) {
        showAccessDenied('Não foi possível identificar o usuário');
        return;
      }

      // Modelo novo: check-access valida apenas que o user e' interno ativo.
      // O que controla envio e' /bitrix/crm/allowed-sessions, carregado em
      // paralelo — JS filtra o dropdown com isso.
      var checkUrl = _baseUrl + '/bitrix/crm/check-access'
        + '?domain=' + encodeURIComponent(_domain)
        + '&user_id=' + encodeURIComponent(userID);
      var allowedUrl = _baseUrl + '/bitrix/crm/allowed-sessions'
        + '?domain=' + encodeURIComponent(_domain)
        + '&user_id=' + encodeURIComponent(userID);

      // CRM tab e' aberto pra todo interno ativo. Onboarding do master e
      // configuracao de permissoes ficam no /dashboard e no /admin — nao
      // travam o operador aqui. Se o user nao tem nenhum numero liberado,
      // a UI mostra "Sem permissao" no seletor de numero (loadSessions).
      Promise.all([
        fetch(checkUrl).then(function(r){ return r.json(); }),
        fetch(allowedUrl).then(function(r){ return r.json(); }),
      ]).then(function(arr) {
        var access = arr[0] || {};
        var allowed = arr[1] || {};
        if (!access.allowed) {
          showAccessDenied('Apenas colaboradores internos ativos podem acessar o UC Talk.');
          return;
        }
        var list = allowed.sessions || [];
        var set = {};
        for (var i = 0; i < list.length; i++) set[list[i]] = true;
        _allowedSessions = set;
        loadSessions();
        loadEntity();
      }).catch(function(err) {
        showAccessDenied('Erro ao verificar permissões: ' + (err && err.message || err));
      });
    });
  });
}

// Substitui a UI inteira por pagina amigavel de acesso negado.
function showAccessDenied(msg) {
  var html = ''
    + '<div style="display:flex;flex-direction:column;align-items:center;justify-content:center;height:100vh;padding:32px;text-align:center;background:#0f172a;color:#e2e8f0;font-family:-apple-system,system-ui,sans-serif">'
    +   '<div style="width:88px;height:88px;border-radius:50%;background:rgba(239,68,68,.12);display:flex;align-items:center;justify-content:center;margin-bottom:18px">'
    +     '<svg width="44" height="44" viewBox="0 0 24 24" fill="none" stroke="#ef4444" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>'
    +   '</div>'
    +   '<h2 style="margin:0 0 12px;font-size:18px;font-weight:700">Acesso negado</h2>'
    +   '<p style="max-width:480px;color:#94a3b8;line-height:1.6;font-size:14px;margin:0 0 24px">' + msg + '</p>'
    +   '<div style="font-size:11px;color:#475569">UC Talk &middot; suporte@uctechnology.com.br</div>'
    + '</div>';
  document.body.innerHTML = html;
}

// ── Operador ─────────────────────────────────────────────────────────────
function initials(name) {
  var parts = name.trim().split(/\s+/);
  if (parts.length >= 2) return (parts[0][0] + parts[parts.length-1][0]).toUpperCase();
  return name.slice(0,2).toUpperCase();
}

// ── Sessões WhatsApp ──────────────────────────────────────────────────────
function loadSessions() {
  var url = _baseUrl + '/bitrix/crm/sessions' + (_domain ? ('?domain=' + enc(_domain)) : '');
  fetch(url).then(function(r){ return r.json(); }).then(function(d) {
    var raw = (d.sessions || []).map(function(s) {
      if (typeof s === 'string') {
        var num = s.split('@')[0];
        if (num.indexOf(':') !== -1) num = num.split(':')[0];
        return {jid: s, phone: num, type: 'qr', label: ''};
      }
      return {jid: s.jid, phone: s.phone || '', type: s.type || 'qr', label: s.label || ''};
    });
    // Filtra pelas sessoes que o operador pode usar. Se _allowedSessions nao
    // carregou (erro de rede), preferimos NAO listar nada — operador nao
    // envia sem permissao explicita.
    var allowed = _allowedSessions || {};
    _sessions = raw.filter(function(s){ return allowed[s.jid]; });
    renderWADropdown();
  });
}

function renderWADropdown() {
  var dd = document.getElementById('wa-dropdown');
  var label = document.getElementById('wa-sel-label');
  dd.innerHTML = '';
  if (!_sessions.length) {
    var msg = (_allowedSessions && Object.keys(_allowedSessions).length === 0)
      ? 'Você não tem nenhum número liberado.<br><span style="color:#475569">Peça para um admin liberar na aba Permissões.</span>'
      : 'Nenhuma sessão conectada';
    dd.innerHTML = '<div style="padding:12px 14px;font-size:12px;color:#64748b;line-height:1.5">' + msg + '</div>';
    label.textContent = 'Sem permissão';
    document.getElementById('wa-selector').style.borderColor = '#7f1d1d';
    return;
  }
  _sessions.forEach(function(s) {
    var el = document.createElement('div');
    el.className = 'wa-drop-item' + (s.jid === _activeSession ? ' active' : '');
    var badge = s.type === 'cloud_api'
      ? '<span style="font-size:9px;background:rgba(96,165,250,.18);color:#60a5fa;padding:1px 5px;border-radius:3px;margin-left:6px;vertical-align:1px;">CLOUD</span>'
      : '';
    var sub = s.label || s.jid;
    el.innerHTML = '<div class="wa-dot"></div><div><div class="wa-drop-num">+' + s.phone + badge + '</div><div class="wa-drop-jid">' + sub + '</div></div>';
    el.onclick = function(e) { e.stopPropagation(); selectSession(s.jid); };
    dd.appendChild(el);
  });
  if (!_activeSession && _sessions.length) selectSession(_sessions[0].jid);
}

function selectSession(jid) {
  _activeSession = jid;
  var s = _sessions.find(function(x){ return x.jid === jid; });
  var lbl = s ? ('+' + s.phone + (s.type === 'cloud_api' ? ' (Cloud)' : '')) : jid;
  document.getElementById('wa-sel-label').textContent = lbl;
  document.getElementById('wa-selector').style.borderColor = '';
  document.getElementById('wa-dropdown').classList.remove('open');
  // re-render checkmarks
  var items = document.querySelectorAll('.wa-drop-item');
  items.forEach(function(el, i) { el.className = 'wa-drop-item' + (_sessions[i] && _sessions[i].jid === jid ? ' active' : ''); });
}

function toggleWADropdown() {
  document.getElementById('wa-dropdown').classList.toggle('open');
}
document.addEventListener('click', function(e) {
  if (!document.getElementById('wa-selector').contains(e.target))
    document.getElementById('wa-dropdown').classList.remove('open');
});

// ── Entidade CRM (contato/lead/deal) ─────────────────────────────────────
function loadEntity() {
  // Mostra spinner na sidebar enquanto carrega
  document.getElementById('conv-list').innerHTML = '<div style="padding:16px;text-align:center"><div class="spinner" style="margin:auto"></div></div>';

  if (!_entityId) {
    document.getElementById('conv-list').innerHTML = '<div class="conv-empty">Abra um Contato,<br>Lead ou Deal para<br>ver o histórico.</div>';
    return;
  }
  var url = _baseUrl + '/bitrix/crm/entity?domain=' + enc(_domain) + '&entity_type=' + _entityType + '&entity_id=' + _entityId;
  fetch(url).then(function(r){ return r.json(); }).then(function(d) {
    _contactName     = d.name  || 'Contato';
    _contactPhone    = d.phone || '';
    _contactInitials = initials(_contactName);

    if (!_contactPhone) {
      document.getElementById('conv-list').innerHTML = '<div class="conv-empty">Nenhum telefone<br>cadastrado neste contato.</div>';
      document.getElementById('chat-hdr-name').textContent  = _contactName;
      document.getElementById('chat-hdr-avatar').textContent = _contactInitials;
      return;
    }

    // Adiciona o contato na lista e abre o chat imediatamente
    _allConvs = [{ name: _contactName, phone: _contactPhone, preview: _contactPhone, time: '', unread: 0, active: true }];
    renderConvList();
    openChat(_contactName, _contactPhone);
  }).catch(function() {
    document.getElementById('conv-list').innerHTML = '<div class="conv-empty" style="color:#f87171">Erro ao carregar contato.</div>';
  });
}

// ── Lista de conversas ────────────────────────────────────────────────────
function renderConvList() {
  var list = document.getElementById('conv-list');
  var q = (document.getElementById('conv-search').value || '').toLowerCase();
  var items = _allConvs.filter(function(c){ return !q || c.name.toLowerCase().includes(q) || c.phone.includes(q); });
  if (!items.length) {
    list.innerHTML = '<div class="conv-empty">Nenhuma conversa encontrada</div>';
    return;
  }
  list.innerHTML = items.map(function(c) {
    var av = initials(c.name);
    var isActive = c.active || c.phone === _contactPhone;
    var badge = c.unread ? '<span class="conv-badge">' + c.unread + '</span>' : '';
    return '<div class="conv-item' + (isActive ? ' active' : '') + '" data-phone="' + esc(c.phone) + '" onclick="openChat(' + JSON.stringify(c.name) + ',' + JSON.stringify(c.phone) + ')">'
      + '<div class="conv-avatar">' + av + '</div>'
      + '<div class="conv-body"><div class="conv-name">' + esc(c.name) + '</div>'
      + '<div class="conv-preview">' + esc(c.preview || c.phone) + '</div></div>'
      + '<div class="conv-meta"><span class="conv-time">' + (c.time||'') + '</span>' + badge + '</div>'
      + '</div>';
  }).join('');
}

function filterConvs() { renderConvList(); }

// ── Abrir chat ────────────────────────────────────────────────────────────
function openChat(name, phone) {
  _contactName  = name;
  _contactPhone = phone;
  // Reseta cache de sessoes ao trocar de contato pra nao manter aba antiga ativa
  _msgsBySession = {};
  _activeSessionKey = '';
  document.getElementById('session-tabs').style.display = 'none';
  document.getElementById('chat-hdr-name').textContent   = name;
  document.getElementById('chat-hdr-phone').textContent  = phone ? ('📱 ' + phone) : '';
  document.getElementById('chat-hdr-avatar').textContent = initials(name);
  document.getElementById('msg-input').disabled  = false;
  document.getElementById('btn-send').disabled   = false;
  document.getElementById('btn-attach').disabled = false;
  document.getElementById('btn-tpl').disabled    = false;
  hideAlert();
  loadHistory();
  // marca conversa ativa na sidebar
  document.querySelectorAll('.conv-item').forEach(function(el){ el.classList.remove('active'); });
  var items = document.querySelectorAll('.conv-item');
  items.forEach(function(el){ if (el.dataset.phone === phone) el.classList.add('active'); });
}

function reloadChat() { if (_contactPhone) loadHistory(); }

// ── Histórico (banco local → fallback Bitrix) ─────────────────────────────
function loadHistory() {
  if (!_entityId || !_domain) return;
  document.getElementById('chat-body').innerHTML = '<div class="chat-placeholder"><div class="spinner"></div></div>';
  var url = _baseUrl + '/bitrix/crm/history?domain=' + enc(_domain)
          + '&entity_type=' + _entityType + '&entity_id=' + _entityId
          + '&phone=' + enc(_contactPhone)
          + '&limit=500';
  fetch(url)
    .then(function(r){ return r.json(); })
    .then(function(d) { renderHistory(d.messages || [], d.chat_id || ''); })
    .catch(function() {
      document.getElementById('chat-body').innerHTML = '<div class="chat-placeholder"><p>Erro ao carregar histórico.</p></div>';
    });
  if (_pollTimer) clearInterval(_pollTimer);
  _pollTimer = setInterval(pollHistory, 5000);
}

function pollHistory() {
  if (!_entityId || !_domain) return;
  var url = _baseUrl + '/bitrix/crm/history?domain=' + enc(_domain)
          + '&entity_type=' + _entityType + '&entity_id=' + _entityId
          + '&phone=' + enc(_contactPhone)
          + '&limit=20';
  fetch(url)
    .then(function(r){ return r.json(); })
    .then(function(d) {
      var msgs = d.messages || [];
      if (!msgs.length) return;
      var lastId = msgs[msgs.length-1].id;
      if (lastId !== _lastMsgId) loadHistory();
    }).catch(function(){});
}

// Cache: msgs do contato atual, agrupadas por session_phone (lado nosso).
// Permite trocar de tab sem refetch.
var _msgsBySession = {}; // { "554220181520": [...], "5519910001772": [...] }
var _activeSessionKey = ''; // chave de _msgsBySession atualmente exibida

function renderHistory(msgs, chatID) {
  var body = document.getElementById('chat-body');
  var tabsEl = document.getElementById('session-tabs');
  if (!msgs.length) {
    tabsEl.style.display = 'none';
    body.innerHTML = '<div class="chat-placeholder"><svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M21 15a2 2 0 01-2 2H7l-4 4V5a2 2 0 012-2h14a2 2 0 012 2z"/></svg><p>Nenhuma mensagem ainda</p><span class="sub">Envie a primeira mensagem abaixo</span></div>';
    _msgsBySession = {};
    _activeSessionKey = '';
    return;
  }

  // ── Agrupa mensagens por (kind + phone) ──
  // Chave usada: 'cloud:<phone>' ou 'qr:<phone>'. Se phone vazio, usa 'unknown'.
  var groups = {}; // key -> { kind, phone, msgs[], lastTime }
  msgs.forEach(function(m) {
    var kind = m.kind || 'qr';
    var phone = m.session_phone || '';
    var key = kind + ':' + (phone || 'unknown');
    if (!groups[key]) groups[key] = { kind: kind, phone: phone, key: key, msgs: [], lastTime: 0 };
    groups[key].msgs.push(m);
    var t = parseDate(m.created_at).getTime();
    if (t > groups[key].lastTime) groups[key].lastTime = t;
  });
  _msgsBySession = groups;

  // ── Renderiza tabs (so se houver >1 sessao) ──
  var keys = Object.keys(groups);
  keys.sort(function(a, b) { return groups[b].lastTime - groups[a].lastTime; }); // mais recente primeiro

  // mantem aba ativa se ainda existe; senao usa a mais recente
  if (!_activeSessionKey || !groups[_activeSessionKey]) {
    _activeSessionKey = keys[0];
  }

  if (keys.length > 1) {
    var tabsHtml = '';
    keys.forEach(function(k) {
      var g = groups[k];
      var isActive = (k === _activeSessionKey);
      var kindLabel = g.kind === 'cloud' ? 'OFICIAL' : 'MULTI-DEVICE';
      var phoneLabel = g.phone ? '+' + g.phone : 'sem número';
      tabsHtml += '<button class="session-tab' + (isActive ? ' active' : '') + '" onclick="switchSessionTab(\'' + esc(k) + '\')">'
                + '<span class="badge-kind ' + g.kind + '">' + kindLabel + '</span>'
                + '<span>' + esc(phoneLabel) + '</span>'
                + '<span class="count">(' + g.msgs.length + ')</span>'
                + '</button>';
    });
    tabsEl.innerHTML = tabsHtml;
    tabsEl.style.display = 'flex';
  } else {
    tabsEl.style.display = 'none';
  }

  // ── Renderiza msgs da aba ativa ──
  renderSessionMessages();

  // Atualiza preview na sidebar usando a msg mais recente GLOBAL
  if (msgs.length) _lastMsgId = msgs[msgs.length-1].id;
  var last = msgs[msgs.length-1];
  var idx = _allConvs.findIndex(function(c){ return c.phone === _contactPhone; });
  if (idx >= 0) {
    _allConvs[idx].preview = last.content || mediaLabel(last.type);
    _allConvs[idx].time    = last.created_at ? parseDate(last.created_at).toLocaleTimeString('pt-BR',{hour:'2-digit',minute:'2-digit'}) : '';
    renderConvList();
  }
}

function switchSessionTab(key) {
  if (!_msgsBySession[key]) return;
  _activeSessionKey = key;
  // marca tab ativa
  document.querySelectorAll('.session-tab').forEach(function(el) {
    el.classList.remove('active');
  });
  // re-renderiza ambos os elementos
  var tabsEl = document.getElementById('session-tabs');
  Array.prototype.forEach.call(tabsEl.children, function(btn) {
    if (btn.getAttribute('onclick') && btn.getAttribute('onclick').indexOf(key) !== -1) {
      btn.classList.add('active');
    }
  });
  renderSessionMessages();
}

function renderSessionMessages() {
  var body = document.getElementById('chat-body');
  var group = _msgsBySession[_activeSessionKey];
  if (!group || !group.msgs.length) {
    body.innerHTML = '<div class="chat-placeholder"><p>Nenhuma mensagem neste vínculo</p></div>';
    return;
  }
  var html = '', lastDay = '';
  group.msgs.forEach(function(m) {
    var dt = parseDate(m.created_at);
    var day = dt.toLocaleDateString('pt-BR',{day:'2-digit',month:'2-digit',year:'numeric'});
    if (day !== lastDay) { html += '<div class="day-div"><span>' + day + '</span></div>'; lastDay = day; }
    var isOut = m.direction === 'outbound';
    var time  = dt.toLocaleTimeString('pt-BR',{hour:'2-digit',minute:'2-digit'});
    var st = isOut ? '<span class="bst delivered">✓✓</span>' : '';
    var content = '';
    if (m.type && m.type !== 'text') {
      var icon = mediaIcon(m.type);
      if (m.media_url) {
        content = '<a href="' + esc(m.media_url) + '" target="_blank" style="display:flex;align-items:center;gap:5px;color:#60a5fa;text-decoration:none;">' + icon + ' ' + mediaLabel(m.type) + '</a>';
      } else {
        content = '<div class="bmedia">' + icon + ' ' + mediaLabel(m.type) + '</div>';
      }
      if (m.content) content += '<div style="margin-top:3px">' + esc(m.content) + '</div>';
    } else {
      content = esc(m.content || '');
    }
    var authorLabel = (isOut && m.author_name) ? '<div style="font-size:10px;color:#4ade80;margin-bottom:2px;font-weight:600">' + esc(m.author_name) + '</div>' : '';
    html += '<div class="bw ' + (isOut?'out':'in') + '"><div class="bubble ' + (isOut?'out':'in') + '">'
          + authorLabel + content
          + '<div class="bmeta"><span class="btime">' + time + '</span>' + st + '</div>'
          + '</div></div>';
  });
  body.innerHTML = html;
  body.scrollTop = body.scrollHeight;
}

function parseDate(s) {
  if (!s) return new Date();
  // Formato Bitrix: "DD.MM.YYYY HH:MM:SS"
  var m = s.match(/^(\d{2})\.(\d{2})\.(\d{4})\s+(\d{2}):(\d{2}):(\d{2})$/);
  if (m) return new Date(m[3]+'-'+m[2]+'-'+m[1]+'T'+m[4]+':'+m[5]+':'+m[6]);
  // Formato ISO (banco local): "2024-01-15T14:30:00Z"
  var d = new Date(s);
  if (!isNaN(d.getTime()) && d.getFullYear() > 2000) return d;
  return new Date();
}

// ── Arquivo ───────────────────────────────────────────────────────────────
var _pendingFile = null;

function onFileSelected(input) {
  if (!input.files || !input.files[0]) return;
  _pendingFile = input.files[0];
  document.getElementById('file-preview-name').textContent = _pendingFile.name;
  document.getElementById('file-preview').classList.add('show');
  document.getElementById('msg-input').placeholder = 'Legenda (opcional)...';
}

function clearFile() {
  _pendingFile = null;
  document.getElementById('file-input').value = '';
  document.getElementById('file-preview').classList.remove('show');
  document.getElementById('msg-input').placeholder = 'Mensagem...';
}

// ── Envio (texto ou arquivo) ───────────────────────────────────────────────
function sendOrUpload() {
  if (_pendingFile) { uploadFile(); } else { sendMsg(); }
}

function sendMsg() {
  var msg = document.getElementById('msg-input').value.trim();
  if (!_contactPhone) { showAlert('error','Selecione um contato.'); return; }
  if (!_activeSession){ showAlert('error','Selecione um número WhatsApp no topo.'); return; }
  if (!msg) return;

  setBtnLoading(true);
  fetch(_baseUrl + '/bitrix/crm/send', {
    method:'POST', headers:{'Content-Type':'application/json'},
    body: JSON.stringify({ domain:_domain, entity_type:_entityType, entity_id:_entityId,
                           phone:_contactPhone, session_jid:_activeSession, message:msg,
                           user_id: _userID,
                           operator_name: document.getElementById('op-name').textContent || 'UC Talk' })
  })
  .then(function(r){ return r.json(); })
  .then(function(d) {
    setBtnLoading(false);
    if (d.status === 'queued') {
      document.getElementById('msg-input').value = '';
      autoResize(document.getElementById('msg-input'));
      hideAlert();
      appendOptimistic(msg, null);
    } else {
      showAlert('error', d.error || 'Erro ao enviar.');
    }
  })
  .catch(function(e){ setBtnLoading(false); showAlert('error','Falha: ' + e); });
}

function uploadFile() {
  if (!_pendingFile) return;
  if (!_contactPhone) { showAlert('error','Selecione um contato.'); return; }
  if (!_activeSession){ showAlert('error','Selecione um número WhatsApp no topo.'); return; }

  var caption = document.getElementById('msg-input').value.trim();
  var fd = new FormData();
  fd.append('domain',      _domain);
  fd.append('phone',       _contactPhone);
  fd.append('session_jid', _activeSession);
  fd.append('user_id',     _userID);
  fd.append('caption',     caption);
  fd.append('file',        _pendingFile, _pendingFile.name);

  setBtnLoading(true);
  fetch(_baseUrl + '/bitrix/crm/upload', { method:'POST', body: fd })
  .then(function(r){ return r.json(); })
  .then(function(d) {
    setBtnLoading(false);
    if (d.status === 'queued') {
      appendOptimistic(caption || _pendingFile.name, _pendingFile.name);
      clearFile();
      document.getElementById('msg-input').value = '';
      autoResize(document.getElementById('msg-input'));
      hideAlert();
    } else {
      showAlert('error', d.error || 'Erro ao enviar arquivo.');
    }
  })
  .catch(function(e){ setBtnLoading(false); showAlert('error','Falha: ' + e); });
}

function setBtnLoading(on) {
  document.getElementById('btn-send').disabled   = on;
  document.getElementById('btn-attach').disabled = on;
  document.getElementById('btn-tpl').disabled    = on;
}

function appendOptimistic(text, filename) {
  var body = document.getElementById('chat-body');
  var ph = body.querySelector('.chat-placeholder');
  if (ph) ph.remove();
  var now  = new Date();
  var time = now.toLocaleTimeString('pt-BR',{hour:'2-digit',minute:'2-digit'});
  var content = filename
    ? '<div class="bmedia">📎 ' + esc(filename) + '</div>' + (text ? '<div>' + esc(text) + '</div>' : '')
    : esc(text);
  var el = document.createElement('div');
  el.className = 'bw out';
  el.innerHTML = '<div class="bubble out">' + content + '<div class="bmeta"><span class="btime">' + time + '</span><span class="bst sent">✓</span></div></div>';
  body.appendChild(el);
  body.scrollTop = body.scrollHeight;
}

// ── Helpers ───────────────────────────────────────────────────────────────
function onKey(e) { if (e.key==='Enter' && !e.shiftKey){ e.preventDefault(); sendMsg(); } }
function autoResize(el){ el.style.height='auto'; el.style.height=Math.min(el.scrollHeight,120)+'px'; }
function enc(s){ return encodeURIComponent(s); }
function esc(s){ return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/\n/g,'<br>'); }
function showAlert(t,m){ var el=document.getElementById('inline-alert'); el.className='inline-alert '+t; el.textContent=m; el.style.display='block'; }
function hideAlert(){ var el=document.getElementById('inline-alert'); el.style.display='none'; }

function mediaIcon(t){
  var i={image:'🖼',video:'▶',audio:'🎵',document:'📄',sticker:'🙂'};
  return i[t]||'📎';
}
function mediaLabel(t){
  var l={image:'Imagem',video:'Vídeo',audio:'Áudio',document:'Documento',sticker:'Sticker'};
  return l[t]||'Arquivo';
}

// ── Templates (quick replies) ────────────────────────────────────────────
var _templates = null;
var _tplCacheTime = 0;

function abrirTplPopup() {
  var pop = document.getElementById('tpl-popup');
  if (!pop) return;
  pop.style.display = 'block';
  // cache 60s — evita refetch a cada abertura
  if (_templates && (Date.now() - _tplCacheTime) < 60000) {
    renderTemplates(_templates);
  } else {
    carregarTemplates();
  }
}

function fecharTplPopup() {
  var pop = document.getElementById('tpl-popup');
  if (pop) pop.style.display = 'none';
}

function carregarTemplates() {
  var list = document.getElementById('tpl-popup-list');
  list.innerHTML = '<div style="padding:18px;text-align:center;color:#475569;font-size:12px;">Carregando...</div>';
  var url = _baseUrl + '/ui/templates/list?domain=' + enc(_domain);
  fetch(url, {credentials:'include'})
    .then(function(r){ return r.json(); })
    .then(function(d){
      var items = (d && (d.templates || d.items)) || [];
      _templates = items;
      _tplCacheTime = Date.now();
      renderTemplates(items);
    })
    .catch(function(e){
      list.innerHTML = '<div style="padding:18px;text-align:center;color:#ef4444;font-size:12px;">Erro: ' + esc(String(e)) + '</div>';
    });
}

function renderTemplates(items) {
  var list = document.getElementById('tpl-popup-list');
  if (!items || !items.length) {
    list.innerHTML = '<div style="padding:18px;text-align:center;color:#64748b;font-size:12px;line-height:1.5;">Nenhum template cadastrado.<br><span style="color:#475569;font-size:11px;">Crie no painel admin (/dashboard).</span></div>';
    return;
  }
  var html = '';
  for (var i = 0; i < items.length; i++) {
    var t = items[i];
    var preview = (t.body || '').replace(/\n/g, ' ').slice(0, 70);
    if ((t.body || '').length > 70) preview += '...';
    // dataset para passar body sem escape issues
    html += '<div class="tpl-item" data-idx="' + i + '" style="padding:10px 12px;border-bottom:1px solid rgba(255,255,255,.04);cursor:pointer;transition:background .15s;" '
         +  'onmouseover="this.style.background=\'rgba(255,255,255,.04)\'" '
         +  'onmouseout="this.style.background=\'transparent\'" '
         +  'onclick="inserirTemplateByIdx(' + i + ')">'
         +    '<div style="font-size:12px;font-weight:600;color:#e2e8f0;margin-bottom:2px;">' + esc(t.title || '(sem título)') + '</div>'
         +    '<div style="font-size:11px;color:#64748b;line-height:1.4;">' + esc(preview) + '</div>'
         +  '</div>';
  }
  list.innerHTML = html;
}

function inserirTemplateByIdx(idx) {
  if (!_templates || !_templates[idx]) return;
  var t = _templates[idx];
  var input = document.getElementById('msg-input');
  var cur = input.value || '';
  // se ja tem texto, adiciona com \n antes; senao substitui
  input.value = cur ? (cur + (cur.endsWith('\n') ? '' : '\n') + t.body) : t.body;
  autoResize(input);
  input.focus();
  // posiciona cursor no fim
  try { input.setSelectionRange(input.value.length, input.value.length); } catch(e){}
  fecharTplPopup();
}

// Fecha popup ao clicar fora
document.addEventListener('click', function(e){
  var pop = document.getElementById('tpl-popup');
  var btn = document.getElementById('btn-tpl');
  if (!pop || pop.style.display === 'none') return;
  if (pop.contains(e.target) || (btn && btn.contains(e.target))) return;
  fecharTplPopup();
});

init();
</script>
</body>
</html>`

// GET/POST /bitrix-app — pagina servida no LEFT_MENU placement do Bitrix24.
// Mostra UI completa (lista de conversas + chat) para usuarios liberados.
// Checa permissao via mesma rota /bitrix/crm/check-access do CRM tab.
//
// Quando Bitrix carrega o iframe via POST, manda auth[domain] + auth[application_token].
// Usamos isso pra setar cookie tenant (assinado HMAC) que autentica as
// chamadas /ui/* feitas pelo JS interno depois.
func (h *handlers) bitrixAppMenu(c *fiber.Ctx) error {
	c.Set("Cache-Control", "no-store, no-cache, must-revalidate")
	c.Set("Pragma", "no-cache")
	c.Set("Expires", "0")
	h.maybeSetTenantCookieFromBitrixPost(c)
	return c.Type("html").SendString(bitrixAppMenuHTML)
}

// maybeSetTenantCookieFromBitrixPost: se o request e POST com auth[domain]
// + auth[application_token] validos, seta cookie tenant. Best-effort:
// silencioso em GET ou auth invalido. Usado nos handlers de pagina HTML
// (iframe entry points) pra preparar o cookie antes das chamadas /ui/*.
func (h *handlers) maybeSetTenantCookieFromBitrixPost(c *fiber.Ctx) {
	if c.Method() != fiber.MethodPost {
		return
	}
	appToken := c.FormValue("auth[application_token]")
	domainRaw := c.FormValue("auth[domain]")
	if appToken == "" || domainRaw == "" {
		return
	}
	if _, err := h.validateBitrixAppToken(c.Context(), domainRaw, appToken); err != nil {
		return
	}
	tenantExpires := time.Now().Add(tenantCookieTTL)
	// CHIPS partitioned cookie pra funcionar em iframe cross-site (Bitrix).
	setPartitionedCookie(c, tenantCookieName,
		signTenantCookie(h.cfg.App.Secret, normalizePortalDomain(domainRaw), tenantExpires),
		tenantExpires,
		strings.HasPrefix(h.cfg.App.PublicURL, "https://"))
}

// HTML simples que carrega BX24, valida o user_id contra check-access e
// embute o CRM tab via iframe (com modo "lista geral", sem entity_id).
const bitrixAppMenuHTML = `<!DOCTYPE html>
<html lang="pt-br">
<head>
<meta charset="utf-8">
<title>UC Talk</title>
<style>
  *{box-sizing:border-box}
  html,body{margin:0;padding:0;height:100%;background:#0f172a;color:#e2e8f0;font-family:-apple-system,system-ui,sans-serif}
  #wrap{height:100vh;display:flex;align-items:center;justify-content:center;padding:24px;text-align:center}
  .denied{max-width:480px}
  .denied .icon{width:88px;height:88px;border-radius:50%;background:rgba(239,68,68,.12);display:flex;align-items:center;justify-content:center;margin:0 auto 18px}
  .denied h2{margin:0 0 12px;font-size:18px;font-weight:700}
  .denied p{color:#94a3b8;line-height:1.6;font-size:14px;margin:0 0 24px}
  .denied .ft{font-size:11px;color:#475569}
  iframe{width:100%;height:100vh;border:0;display:block}
</style>
</head>
<body>
<div id="wrap"><div style="color:#64748b;font-size:14px">Carregando...</div></div>
<script src="https://api.bitrix24.com/api/v1/"></script>
<script>
var _baseUrl = window.location.origin;
BX24.init(function() {
  var domain = (BX24.getDomain ? BX24.getDomain() : '') || '';
  BX24.callMethod('profile', {}, function(res) {
    var u = res.data() || {};
    var userID = String(u.ID || '');
    if (!userID || !domain) {
      showDenied('Não foi possível identificar o usuário.');
      return;
    }
    // HANDSHAKE: garante que o backend tem cookie tenant assinado e
    // trial criado pra esse dominio. Usa BX24.getAuth() pra pegar token
    // valido e fazer POST /bitrix/auth (mesma rota usada por /bitrix-connect).
    // Idempotente: ja' salvo passa direto.
    var auth = BX24.getAuth ? BX24.getAuth() : null;
    var handshake;
    if (auth && auth.access_token) {
      handshake = fetch(_baseUrl + '/bitrix/auth', {
        method: 'POST',
        headers: {'Content-Type':'application/json'},
        credentials: 'include',
        body: JSON.stringify({
          domain: domain,
          access_token: auth.access_token,
          refresh_token: auth.refresh_token || '',
          expires_in: auth.expires_in || 3600,
          member_id: auth.member_id || '',
          user_id: userID  // pra auto-vincular master se ainda nao tem
        })
      }).then(function(r){ return r.json(); }).catch(function(){ return null; });
    } else {
      handshake = Promise.resolve(null);
    }
    // Espera handshake + check-access + master.status em paralelo.
    Promise.all([
      handshake,
      fetch(_baseUrl + '/bitrix/crm/check-access?domain=' + encodeURIComponent(domain) + '&user_id=' + encodeURIComponent(userID)).then(function(r){ return r.json(); }),
      fetch(_baseUrl + '/bitrix/crm/master/status?domain=' + encodeURIComponent(domain)).then(function(r){ return r.json(); }),
    ]).then(function(arr) {
      arr.shift(); // remove handshake result, mantem [access, master]
      var access = arr[0] || {};
      var master = arr[1] || {};
      if (!access.allowed) {
        showDenied('Apenas colaboradores internos ativos podem acessar o UC Talk.');
        return;
      }
      if (!master.configured) {
        showDenied('O usuário master ainda não foi definido neste portal. Solicite à UC Technology para configurar o master inicial.');
        return;
      }
      if (master.master_user_id !== userID) {
        var nm = master.master_user_name || ('User #' + master.master_user_id);
        showDenied('Apenas o usuário master (' + nm + ') pode acessar o painel administrativo do UC Talk. Para enviar mensagens, abra o UC Talk dentro de um Contato, Lead ou Deal.');
        return;
      }
      // Master autenticado — carrega o /dashboard com portal+user_id ja'
      // preenchidos. Cache buster (v=<timestamp>) garante que iframes do
      // Bitrix nao sirvam HTML antigo mesmo com no-store no header — Bitrix
      // tem CDN intermediario agressivo em alguns regions.
      var v = Date.now();
      document.getElementById('wrap').outerHTML =
        '<iframe src="' + _baseUrl + '/dashboard?portal=' + encodeURIComponent(domain)
        + '&user_id=' + encodeURIComponent(userID)
        + '&v=' + v + '"></iframe>';
    }).catch(function(err){
      showDenied('Erro ao verificar permissões: ' + (err && err.message || err));
    });
  });
});

function showDenied(msg) {
  document.getElementById('wrap').innerHTML =
    '<div class="denied">'
    + '<div class="icon"><svg width="44" height="44" viewBox="0 0 24 24" fill="none" stroke="#ef4444" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg></div>'
    + '<h2>Acesso negado</h2>'
    + '<p>' + msg + '</p>'
    + '<div class="ft">UC Talk &middot; suporte@uctechnology.com.br</div>'
    + '</div>';
}
</script>
</body>
</html>`

// placementRegisterLocks: 1 mutex por domain pra serializar chamadas
// concorrentes a RegisterPlacementsForPortal. Sem isso, duas goroutines
// (partner.go install + handlers.go callback) podem listar placements
// simultaneamente, ambas nao verem nada, e ambas bindar — gerando
// duplicata no menu lateral.
var placementRegisterLocks sync.Map // map[domain]*sync.Mutex

func placementLockFor(domain string) *sync.Mutex {
	if m, ok := placementRegisterLocks.Load(domain); ok {
		return m.(*sync.Mutex)
	}
	m, _ := placementRegisterLocks.LoadOrStore(domain, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// RegisterPlacementsForPortal registra placement.bind para os 3 tipos de
// entidade CRM e para o menu lateral esquerdo. IDEMPOTENTE: lista placements
// ja' existentes e faz unbind dos duplicados (mesma PLACEMENT + HANDLER)
// antes de bindar de novo. Sem isso, multipla chamada (install + ONAPPINSTALL
// callback) cria N duplicatas de "UC Talk" no menu lateral.
//
// SAFE PARA RACES: usa mutex por domain pra serializar chamadas
// concorrentes. Cenario: install + callback do Bitrix disparam em paralelo,
// ambos chamavam RegisterPlacements ao mesmo tempo, ambos viam estado vazio
// e bindavam, criando 2x "UC Talk" no menu. Mutex resolve.
//
// Chamado após instalar ou ativar o connector.
func (h *handlers) RegisterPlacementsForPortal(ctx context.Context, domain string, creds bitrix.TenantCreds) {
	// Serializa chamadas concorrentes pro mesmo domain.
	lock := placementLockFor(domain)
	lock.Lock()
	defer lock.Unlock()

	base := h.cfg.App.BaseURL()
	tabURL := base + "/bitrix/crm/tab"
	// NOTA IMPORTANTE — NAO REGISTRAR LEFT_MENU via placement.bind.
	//
	// O Bitrix24 ja' renderiza AUTOMATICAMENTE um item no menu lateral
	// baseado na "Main Application URL" configurada em vendors.bitrix24.com.
	// Esse item nao e' visivel via placement.list nem removivel via
	// placement.unbind — e' gerenciado por outro subsistema do Bitrix.
	//
	// Se chamarmos placement.bind LEFT_MENU em paralelo, o Bitrix CRIA UM
	// SEGUNDO ITEM no menu lateral, resultando em 2 "UC Talk" duplicados
	// que NAO ha como limpar via REST (placement.list nao mostra o do
	// Bitrix automatico, placement.unbind nao remove ele).
	//
	// Fonte oficial: apidocs.bitrix24.com/api-reference/widgets/left-menu.html
	// > "unlike the integration of the slider with the main URL of the
	// > application in the left menu, all other widgets are integrated
	// > differently — using the placement.bind method"
	//
	// Conclusao: o LEFT_MENU e' a UNICA excecao — vem do app card, NAO
	// de placement.bind. Removemos esse bind. CRM tabs (Contact/Lead/Deal)
	// continuam via placement.bind normal.

	type pl struct {
		Name    string
		Handler string
	}
	wanted := []pl{
		{"CRM_CONTACT_DETAIL_TAB", tabURL},
		{"CRM_LEAD_DETAIL_TAB", tabURL},
		{"CRM_DEAL_DETAIL_TAB", tabURL},
	}

	// BLINDAGEM ANTI-MENU-DUPLICADO — unbind preventivo de LEFT_MENU.
	//
	// Versoes ANTIGAS do app registravam placement.bind LEFT_MENU (removido
	// depois). Esse placement ficou GRAVADO no portal e nao sai com
	// desinstalacao/reinstalacao — vira orfao que duplica o menu (1 do Bitrix
	// automatico + 1 do placement orfao). Como HOJE nao registramos LEFT_MENU,
	// qualquer LEFT_MENU presente e' lixo legado. Removemos incondicionalmente
	// (handler vazio -> Bitrix apaga TODOS os LEFT_MENU do nosso app).
	// Idempotente: se nao houver nenhum, o erro "not found" e' benigno.
	if err := h.bitrixClient.UnbindPlacement(ctx, creds, "LEFT_MENU", ""); err != nil {
		h.log.Debug("placement.unbind LEFT_MENU preventivo (benigno se nao existia)",
			zap.String("domain", domain), zap.Error(err))
	} else {
		h.log.Info("placement.unbind LEFT_MENU orfao removido",
			zap.String("domain", domain))
	}

	// Lista placements ja registrados. Se a chamada falhar (token expirado,
	// app reinstalado, etc), seguimos pro bind direto — bitrix vai falhar
	// com ALREADY_INSTALLED ou aceitar criando duplicata. Best-effort.
	existing, listErr := h.bitrixClient.ListPlacements(ctx, creds)
	if listErr != nil {
		h.log.Warn("placement.list falhou; bind sem dedup",
			zap.String("domain", domain), zap.Error(listErr))
	}

	// Unbind tudo que ja existe pros placements/handlers que queremos.
	// Idempotente: dedup de duplicatas legadas antes de rebindar limpo.
	for _, w := range wanted {
		for _, e := range existing {
			eName, _ := e["placement"].(string)
			eHandler, _ := e["handler"].(string)
			if eName == w.Name && eHandler == w.Handler {
				if err := h.bitrixClient.UnbindPlacement(ctx, creds, eName, eHandler); err != nil {
					h.log.Warn("placement.unbind (dedup) falhou",
						zap.String("placement", eName),
						zap.String("domain", domain),
						zap.Error(err))
				}
			}
		}
	}

	// Bind limpo — agora so' 1 placement por nome+handler.
	for _, w := range wanted {
		if err := h.bitrixClient.BindPlacement(ctx, creds, w.Name, w.Handler, "UC Talk"); err != nil {
			h.log.Warn("placement.bind falhou",
				zap.String("placement", w.Name),
				zap.String("domain", domain),
				zap.Error(err))
		} else {
			h.log.Info("placement.bind ok",
				zap.String("placement", w.Name),
				zap.String("domain", domain))
		}
	}
}
