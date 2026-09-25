// tenant_isolation.go — garante que um cliente só enxergue e opere os
// PRÓPRIOS números de WhatsApp.
//
// O BUG QUE ISTO CORRIGE: os handlers de /ui/sessions liam
// waManager.ListSessions(), que devolve as sessões de TODOS os tenants do
// processo. Consequências, em ordem de gravidade:
//
//   - /ui/sessions/:phone/qr devolvia o QR de pareamento de QUALQUER numero.
//     Quem escaneasse assumia a sessao de WhatsApp do outro cliente;
//   - /ui/sessions/disconnect aceitava qualquer jid, sem checar dono: um
//     cliente derrubava o atendimento do outro e ainda apagava o vinculo
//     dele com o Bitrix;
//   - /ui/sessions e /ui/sessions/status listavam os numeros alheios.
//
// Foi visto em producao: o portal crm.uctechnology.com.br exibindo, com
// botao de desconectar, o numero do teclife.
//
// A FONTE DA VERDADE e' bitrix_accounts, que liga portal -> sessao. A unica
// excecao e' o numero em PAREAMENTO: entre "Conectar WhatsApp" e a conclusao
// do QR ainda nao existe vinculo, entao a intencao fica registrada em
// memoria por quem pediu.
package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/bitrix"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/whatsapp"
	"go.uber.org/zap"
)

// numeroBase reduz qualquer forma de JID a uma chave estavel de comparacao.
//
// QR: tira dominio e device suffix. O suffix muda a cada re-pareamento
// (":1", ":7", ":47"), e se entrasse na chave o cliente perderia acesso ao
// proprio numero toda vez que reconectasse.
//
//	"5581...:7@s.whatsapp.net" -> "5581..."
//
// CLOUD API: o JID e' "cloud:<phone_id>" e o corte no primeiro ':' devolveria
// "cloud" para TODAS elas — toda sessao Cloud casaria com toda sessao Cloud,
// que e' exatamente o vazamento entre clientes que este arquivo corrige.
// Por isso o prefixo e' preservado e o identificador vai inteiro.
// A mesma armadilha esta' documentada em GetBitrixAccountByJID.
//
//	"cloud:123@s.whatsapp.net" -> "cloud:123"
func numeroBase(jid string) string {
	jid = strings.TrimSpace(jid)
	if i := strings.IndexByte(jid, '@'); i > 0 {
		jid = jid[:i]
	}
	if resto, ehCloud := strings.CutPrefix(jid, "cloud:"); ehCloud {
		return "cloud:" + strings.TrimSpace(resto)
	}
	if i := strings.IndexByte(jid, ':'); i > 0 {
		jid = jid[:i]
	}
	return strings.TrimPrefix(jid, "+")
}

// tenantDoPedido devolve o dominio do cliente que fez a chamada.
func (h *handlers) tenantDoPedido(c *fiber.Ctx) (string, error) {
	return h.resolveDashboardDomain(c.Context(), c)
}

// numerosDoTenant devolve os numeros que pertencem ao dominio, incluindo o
// que estiver em pareamento neste momento.
//
// Erro de leitura NAO vira lista vazia silenciosa: quem chama precisa saber
// a diferenca entre "nao tem numero" e "nao consegui verificar". Tratar as
// duas como iguais esconderia falha de banco atras de uma tela vazia.
func (h *handlers) numerosDoTenant(c *fiber.Ctx) (map[string]bool, error) {
	dominio, err := h.tenantDoPedido(c)
	if err != nil {
		return nil, err
	}
	contas, err := h.repo.ListBitrixAccountsByDomain(c.Context(), dominio)
	if err != nil {
		return nil, err
	}
	meus := make(map[string]bool, len(contas)+1)
	for _, a := range contas {
		if n := numeroBase(a.SessionJID); n != "" {
			meus[n] = true
		}
	}
	// Pareamento em curso: ainda sem vinculo em bitrix_accounts.
	h.pareamentos.Range(func(k, v any) bool {
		if fone, ok := k.(string); ok {
			if dono, ok := v.(string); ok && dono == dominio {
				meus[fone] = true
			}
		}
		return true
	})
	return meus, nil
}

// podeOperarNumero decide se o chamador pode ver ou mexer neste numero.
func (h *handlers) podeOperarNumero(c *fiber.Ctx, jidOuFone string) (bool, error) {
	n := numeroBase(jidOuFone)
	if n == "" {
		return false, nil
	}
	meus, err := h.numerosDoTenant(c)
	if err != nil {
		return false, err
	}
	return meus[n], nil
}

// registrarPareamento marca que este dominio pediu o pareamento do numero,
// para que ele possa buscar o proprio QR antes de o vinculo existir.
func (h *handlers) registrarPareamento(dominio, fone string) {
	if dominio == "" || fone == "" {
		return
	}
	h.pareamentos.Store(numeroBase(fone), dominio)
}

// esquecerPareamento limpa a intencao quando ela deixa de ser necessaria.
func (h *handlers) esquecerPareamento(fone string) {
	h.pareamentos.Delete(numeroBase(fone))
}

// filtrarSessoesDoTenant reduz uma lista de JIDs aos que sao do chamador.
func (h *handlers) filtrarSessoesDoTenant(c *fiber.Ctx, jids []string) ([]string, error) {
	meus, err := h.numerosDoTenant(c)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(jids))
	for _, j := range jids {
		if meus[numeroBase(j)] {
			out = append(out, j)
		}
	}
	return out, nil
}

// sessoesQRDoTenant e' o atalho usado pelos handlers de /ui/sessions.
func (h *handlers) sessoesQRDoTenant(c *fiber.Ctx) ([]string, error) {
	return h.filtrarSessoesDoTenant(c, h.waManager.ListSessions())
}

// localEscopoGlobal marca o pedido que pode ver relatorio de todos os
// clientes. So' o grupo /stats (X-API-Key da UC Technology) liga isso.
const localEscopoGlobal = "escopo_global"

// marcarEscopoGlobal e' o middleware que liga localEscopoGlobal.
func marcarEscopoGlobal(c *fiber.Ctx) error {
	c.Locals(localEscopoGlobal, true)
	return c.Next()
}

// escopoRelatorio decide de quais numeros um relatorio pode contar.
//
// Os mesmos handlers servem /stats/* (global, X-API-Key) e /ui/stats/*
// (cliente). Antes os dois devolviam o banco inteiro: o painel de um cliente
// listava os numeros, os volumes e os CONTATOS — nome e telefone de quem
// conversou — dos outros clientes.
//
// Sem marca de global, o escopo e' sempre o do tenant. Se o tenant nao puder
// ser determinado, falha: relatorio de todo mundo nunca e' o padrao.
func (h *handlers) escopoRelatorio(c *fiber.Ctx) (db.EscopoNumeros, error) {
	if global, _ := c.Locals(localEscopoGlobal).(bool); global {
		return db.EscopoNumeros{Todos: true}, nil
	}
	meus, err := h.numerosDoTenant(c)
	if err != nil {
		return db.EscopoNumeros{}, err
	}
	numeros := make([]string, 0, len(meus))
	for n := range meus {
		numeros = append(numeros, n)
	}
	return db.EscopoNumeros{Numeros: numeros}, nil
}

// aceitaEscopo transforma o escopo em filtro de sessao (para a fila Redis).
func aceitaEscopo(e db.EscopoNumeros) func(string) bool {
	if e.Todos {
		return func(string) bool { return true }
	}
	meus := make(map[string]bool, len(e.Numeros))
	for _, n := range e.Numeros {
		meus[n] = true
	}
	return func(jid string) bool { return meus[numeroBase(jid)] }
}

// garanteWhatsApp evita nil deref em instalacao sem manager (teste/dev).
func (h *handlers) garanteWhatsApp() *whatsapp.Manager { return h.waManager }

// modoTokenDoEvento controla a prova de origem dos eventos do Contact Center
// (CONNECTOR_EVENT_TOKEN = "exigir" | "observar"). Padrao: exigir.
//
// A primeira versao comecava em "observar" pra nao cortar resposta de
// operador caso o application_token gravado fosse o do Partner App e o
// evento viesse do app local. Mas "observar" deixava o furo aberto: quem
// soubesse o dominio de um cliente enviava pelo numero dele — confirmado
// no homolog em 25/09. Agora a prova aceita tambem o access_token do evento,
// conferido no proprio portal, entao a divergencia de application_token nao
// corta atendimento. "observar" fica so' como valvula de emergencia.
func modoTokenDoEvento() string {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("CONNECTOR_EVENT_TOKEN")), "observar") {
		return "observar"
	}
	return "exigir"
}

// tokensDeEventoConfirmados guarda access_tokens ja' conferidos no portal,
// pra nao chamar o Bitrix a cada resposta de operador. Token do Bitrix vale
// 1h; o cache vale menos que isso.
var tokensDeEventoConfirmados sync.Map // sha256(dominio|token) -> validade

const validadeTokenDeEvento = 30 * time.Minute

// eventoTemProvaDoPortal: o evento prova que veio do portal se o
// application_token bate OU se o access_token e' aceito pelo proprio portal.
func (h *handlers) eventoTemProvaDoPortal(c *fiber.Ctx, dominio string) (bool, string) {
	accessTok := strings.TrimSpace(c.FormValue("auth[access_token]"))
	if _, err := h.validateBitrixAppToken(c.Context(), dominio, c.FormValue("auth[application_token]"), accessTok); err == nil {
		return true, "application_token"
	}
	if accessTok == "" {
		return false, "sem access_token e application_token nao confere"
	}
	soma := sha256.Sum256([]byte(dominio + "|" + accessTok))
	chave := hex.EncodeToString(soma[:])
	if v, ok := tokensDeEventoConfirmados.Load(chave); ok {
		if ate, _ := v.(time.Time); time.Now().Before(ate) {
			return true, "access_token (cache)"
		}
		tokensDeEventoConfirmados.Delete(chave)
	}
	ident, err := bitrix.VerificarToken(c.Context(), dominio, accessTok)
	if err != nil || ident.Dominio != dominio {
		return false, "access_token recusado pelo portal"
	}
	tokensDeEventoConfirmados.Store(chave, time.Now().Add(validadeTokenDeEvento))
	return true, "access_token"
}

// eventoPodeUsarSessao decide se um evento de resposta do operador pode
// sair pelo numero sessionJID.
//
// O BURACO: /bitrix/connector/event nao conferia nada. Um POST forjado com o
// conector ou o chat de outro cliente enfileirava texto livre saindo do
// WhatsApp dele. Aqui:
//   - o dominio do evento TEM que ser o dono do numero. Isso vale sempre:
//     evento legitimo vem do portal que tem o conector, nunca de outro;
//   - a origem tem que ser provada (eventoTemProvaDoPortal).
func (h *handlers) eventoPodeUsarSessao(c *fiber.Ctx, sessionJID string) bool {
	dominio := normalizePortalDomain(c.FormValue("auth[domain]"))
	exigir := modoTokenDoEvento() == "exigir"
	if dominio == "" {
		// Recusado SEMPRE, em qualquer modo. A primeira versao deixava
		// passar em "observar" — e bastava OMITIR auth[domain] pra pular a
		// checagem de dono: no homolog, um POST anonimo com so' conector,
		// chat e texto saiu de verdade pelo WhatsApp (25/09). Evento
		// legitimo do Bitrix sempre traz auth[domain].
		h.log.Error("connector event: sem auth[domain] — descartado",
			zap.String("session_jid", sessionJID))
		return false
	}
	contas, err := h.repo.ListBitrixAccountsByDomain(c.Context(), dominio)
	if err != nil {
		h.log.Error("connector event: falha ao ler contas do portal — descartado",
			zap.String("domain", dominio), zap.Error(err))
		return false
	}
	alvo := numeroBase(sessionJID)
	dono := false
	for _, a := range contas {
		if numeroBase(a.SessionJID) == alvo {
			dono = true
			break
		}
	}
	if !dono {
		h.log.Error("connector event: numero NAO pertence ao portal do evento — descartado",
			zap.String("domain", dominio), zap.String("session_jid", sessionJID))
		return false
	}
	ok, via := h.eventoTemProvaDoPortal(c, dominio)
	if !ok {
		h.log.Error("connector event: sem prova de origem — descartado",
			zap.String("domain", dominio), zap.String("session_jid", sessionJID),
			zap.String("motivo", via), zap.String("modo", modoTokenDoEvento()))
		return !exigir
	}
	h.log.Info("connector event: origem confirmada", zap.String("domain", dominio), zap.String("via", via))
	return true
}

// escoparAoTenant prende um pedido /ui/* ao portal do cookie.
//
// O BURACO: as rotas de contas, filas e vinculos (/ui/bitrix/*) e algumas
// de configuracao liam domain/portal e session_jid/jid da query ou do JSON,
// ignorando o cookie. Um cliente listava as contas e member_ids de todos,
// vinculava o numero de outro cliente ao proprio portal (as mensagens dele
// passavam a chegar aqui) ou desvinculava o de qualquer um.
//
// Regras, pra quem entrou com cookie de tenant:
//   - domain/portal diferente do cookie -> 403; ausente -> preenchido;
//   - session_jid/jid tem que ser numero do portal (ou em pareamento).
// Super-admin passa direto: o painel dele opera qualquer cliente.
func (h *handlers) escoparAoTenant(c *fiber.Ctx) error {
	if src, _ := c.Locals("auth_source").(string); src != "tenant" {
		return c.Next()
	}
	dominio, _ := c.Locals("tenant_domain").(string)
	if dominio == "" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "portal nao identificado"})
	}
	outro := func(v string) bool {
		v = normalizePortalDomain(v)
		return v != "" && v != dominio
	}
	numeros := []string{}

	args := c.Request().URI().QueryArgs()
	for _, k := range []string{"domain", "portal"} {
		if outro(string(args.Peek(k))) {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "portal diferente da sessao"})
		}
	}
	args.Set("domain", dominio)
	args.Set("portal", dominio)
	for _, k := range []string{"session_jid", "jid"} {
		if v := string(args.Peek(k)); v != "" {
			numeros = append(numeros, v)
		}
	}

	if strings.Contains(string(c.Request().Header.ContentType()), "application/json") && len(c.Body()) > 0 {
		var corpo map[string]any
		if err := json.Unmarshal(c.Body(), &corpo); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON invalido"})
		}
		for _, k := range []string{"domain", "portal"} {
			if v, _ := corpo[k].(string); outro(v) {
				return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "portal diferente da sessao"})
			}
		}
		if _, tem := corpo["domain"]; tem {
			corpo["domain"] = dominio
		}
		if _, tem := corpo["portal"]; tem {
			corpo["portal"] = dominio
		}
		for _, k := range []string{"session_jid", "jid"} {
			if v, _ := corpo[k].(string); v != "" {
				numeros = append(numeros, v)
			}
		}
		if novo, err := json.Marshal(corpo); err == nil {
			c.Request().SetBody(novo)
		}
	}

	for _, n := range numeros {
		pode, err := h.podeOperarNumero(c, n)
		if err != nil {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error()})
		}
		if !pode {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "este numero nao pertence a este portal"})
		}
	}
	return c.Next()
}
