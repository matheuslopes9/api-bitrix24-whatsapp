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
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/whatsapp"
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
