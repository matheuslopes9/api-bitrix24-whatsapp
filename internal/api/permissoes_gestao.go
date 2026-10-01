package api

// permissoes_gestao.go — quem pode liberar e restringir numeros de um portal.
//
// O PROBLEMA QUE ISTO RESOLVE: existia um master unico por portal, e so' ele
// mexia em permissao. A tela oferecia um unico botao — "Transferir controle" —
// que entregava o master inteiro pra outra pessoa. Para destravar um colega que
// nao conseguia responder pela aba do CRM, o master tinha que abrir mao do
// controle. Quem nao era master via a mensagem "apenas o usuario master atual
// pode transferir o controle" e parava ali.
//
// Alem de incomodo, era fragil: o master sai de ferias, muda de area ou deixa a
// empresa e ninguem mais mexe em permissao nenhuma.
//
// AGORA SAO TRES CAMINHOS, nesta ordem de confiabilidade:
//
//	1. Administrador do portal Bitrix — gestor por direito, conferido na hora
//	   no proprio portal. Nao ha' o que cadastrar, e perder o cargo no Bitrix
//	   tira o poder aqui no mesmo instante.
//	2. Gestor nomeado — para quem precisa gerenciar sem ser admin do Bitrix.
//	3. O master historico — mantido para nao quebrar quem ja' dependia dele.

import (
	"context"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"
)

// timeoutPapelBitrix: isto roda dentro do iframe do Bitrix, com o usuario
// esperando. Uma consulta de papel que demora nao pode segurar a tela.
const timeoutPapelBitrix = 4 * time.Second

// ehGestor diz se o usuario pode mexer nas permissoes deste portal.
//
// Devolve tambem o MOTIVO, porque a tela precisa poder explicar: "voce gerencia
// por ser administrador do Bitrix" e "voce foi nomeado gestor" levam a
// conversas diferentes quando alguem pergunta por que perdeu o acesso.
func (h *handlers) ehGestor(ctx context.Context, domain, userID string) (bool, string) {
	if userID == "" {
		return false, ""
	}
	chave := normalizeDomainKey(domain)

	// Gestor nomeado primeiro: é uma consulta local e barata. Perguntar ao
	// Bitrix antes seria pagar uma ida à rede para quem já está na tabela.
	if ok, err := h.repo.EhGestorNomeado(ctx, chave, userID); err == nil && ok {
		return true, "gestor"
	}

	portal, err := h.repo.GetBitrixPortalByDomain(ctx, normalizePortalDomain(domain))
	if err == nil && portal != nil && portal.LegacyAdminUserID == userID {
		return true, "master"
	}

	// Admin do portal, conferido no Bitrix. Fica por ultimo porque e' o unico
	// que depende da rede.
	if portal != nil && h.bitrixClient != nil {
		cctx, cancel := context.WithTimeout(ctx, timeoutPapelBitrix)
		defer cancel()
		users, uerr := h.bitrixClient.GetUserByIDs(cctx, h.portalToCreds(portal), []string{userID})
		if uerr != nil {
			// Nao da' pra saber: NAO promove. Em caso de duvida sobre
			// autorizacao, a resposta segura e' nao.
			h.log.Warn("permissoes: nao consegui conferir se o usuario e' admin do Bitrix",
				zap.String("domain", chave), zap.String("user_id", userID), zap.Error(uerr))
			return false, ""
		}
		for _, u := range users {
			if u.ID == userID && u.IsAdmin {
				return true, "admin_bitrix"
			}
		}
	}
	return false, ""
}

// exigirGestor é o guard das rotas de gestão. Devolve o user_id do chamador.
func (h *handlers) exigirGestor(c *fiber.Ctx, domain string) (string, error) {
	udom, uid, _, temIdentidade := verifyUserCookie(h.cfg.App.Secret, c.Cookies(userCookieName))
	if !temIdentidade || uid == "" || !strings.EqualFold(udom, normalizePortalDomain(domain)) {
		return "", c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error":  "nao da' pra confirmar quem esta' pedindo — reabra o UC Talk pelo menu do Bitrix",
			"codigo": "sem_identidade",
		})
	}
	if ok, _ := h.ehGestor(c.Context(), domain, uid); !ok {
		return "", c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "voce nao pode alterar permissoes deste portal. " +
				"Quem pode: administradores do Bitrix24 e os gestores nomeados aqui.",
			"codigo": "nao_gestor",
		})
	}
	return uid, nil
}

// POST /ui/permissions/restrict — liga/desliga o controle de um numero.
//
// Body: {"session_jid":"...","restrito":true}
func (h *handlers) uiPermissionsRestrict(c *fiber.Ctx) error {
	ctx := c.Context()
	domain, err := h.resolveDashboardDomain(ctx, c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	uid, resp := h.exigirGestor(c, domain)
	if resp != nil {
		return resp
	}
	var req struct {
		SessionJID string `json:"session_jid"`
		Restrito   bool   `json:"restrito"`
	}
	if perr := c.BodyParser(&req); perr != nil || strings.TrimSpace(req.SessionJID) == "" {
		return c.Status(400).JSON(fiber.Map{"error": "session_jid obrigatorio"})
	}
	// O numero tem que ser deste portal. Sem isto, um gestor restringiria o
	// numero de outro cliente — e a tela de permissoes viraria uma forma de
	// desligar o atendimento alheio.
	if ok, r := h.exigirNumeroDoPortal(c, req.SessionJID); !ok {
		return r
	}

	chave := normalizeDomainKey(domain)
	if err := h.repo.DefinirRestricao(ctx, chave, req.SessionJID, req.Restrito, uid); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.log.Info("permissoes: restricao de numero alterada",
		zap.String("domain", chave), zap.String("session_jid", req.SessionJID),
		zap.Bool("restrito", req.Restrito), zap.String("por", uid))

	msg := "número liberado para todos os colaboradores"
	if req.Restrito {
		msg = "número restrito — agora só quem estiver na lista envia por ele"
	}
	return c.JSON(fiber.Map{"ok": true, "restrito": req.Restrito, "mensagem": msg})
}

// POST /ui/permissions/manager — nomeia ou remove um gestor.
//
// Body: {"user_id":"N","user_name":"X","gestor":true}
func (h *handlers) uiPermissionsManager(c *fiber.Ctx) error {
	ctx := c.Context()
	domain, err := h.resolveDashboardDomain(ctx, c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	uid, resp := h.exigirGestor(c, domain)
	if resp != nil {
		return resp
	}
	var req struct {
		UserID   string `json:"user_id"`
		UserName string `json:"user_name"`
		Gestor   bool   `json:"gestor"`
	}
	if perr := c.BodyParser(&req); perr != nil || strings.TrimSpace(req.UserID) == "" {
		return c.Status(400).JSON(fiber.Map{"error": "user_id obrigatorio"})
	}
	chave := normalizeDomainKey(domain)

	if req.Gestor {
		if err := h.repo.AdicionarGestor(ctx, chave, req.UserID, req.UserName, uid); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		h.log.Info("permissoes: gestor nomeado",
			zap.String("domain", chave), zap.String("user_id", req.UserID), zap.String("por", uid))
		return c.JSON(fiber.Map{"ok": true, "gestor": true})
	}

	// Remover a si mesmo e' permitido, mas nao sem aviso: se o ultimo gestor
	// nomeado sair e o portal nao tiver admin do Bitrix usando o app, volta o
	// problema que este arquivo existe pra resolver.
	if _, err := h.repo.RemoverGestor(ctx, chave, req.UserID); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.log.Info("permissoes: gestor removido",
		zap.String("domain", chave), zap.String("user_id", req.UserID), zap.String("por", uid))

	aviso := ""
	if req.UserID == uid {
		aviso = "você removeu a si mesmo. Se não for administrador do Bitrix24, " +
			"não poderá mais alterar permissões."
	}
	return c.JSON(fiber.Map{"ok": true, "gestor": false, "aviso": aviso})
}

// gestoresDoPortal monta a lista para a tela, com o master junto.
func (h *handlers) gestoresDoPortal(ctx context.Context, domain string) []fiber.Map {
	chave := normalizeDomainKey(domain)
	nomeados, _ := h.repo.ListarGestores(ctx, chave)

	out := make([]fiber.Map, 0, len(nomeados)+1)
	jaListado := map[string]bool{}
	for _, g := range nomeados {
		out = append(out, fiber.Map{"user_id": g.UserID, "user_name": g.UserName, "origem": "gestor"})
		jaListado[g.UserID] = true
	}
	// O master continua gerenciando mesmo sem estar na tabela; some-lo aqui
	// faria a tela mostrar uma lista que nao corresponde a quem pode agir.
	if p, err := h.repo.GetBitrixPortalByDomain(ctx, normalizePortalDomain(domain)); err == nil &&
		p != nil && p.LegacyAdminUserID != "" && !jaListado[p.LegacyAdminUserID] {
		out = append(out, fiber.Map{
			"user_id": p.LegacyAdminUserID, "user_name": "", "origem": "master",
		})
	}
	return out
}
