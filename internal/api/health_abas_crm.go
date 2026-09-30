package api

// health_abas_crm.go — a Saude diz se as abas do UC Talk estao vinculadas.
//
// POR QUE EXISTE: registrar as abas do CRM exige usuario ADMINISTRADOR do
// portal. Quando o app e' instalado por um usuario comum, o placement.bind
// falha, as tres abas nao aparecem no card, e o erro vai so' para o log do
// container. Visto em crm.uctechnology.com.br em 25/09.
//
// O modo de falha e' o pior tipo: nada quebra de forma visivel. O WhatsApp
// segue funcionando, as mensagens continuam chegando no Contact Center, e a
// unica coisa que falta e' uma aba que ninguem sabe que deveria existir.
// Fica assim ate' alguem reclamar.
//
// POR QUE SO' AVISA, E NAO CONSERTA: o conserto depende de uma pessoa com
// permissao de admin abrir o app no portal — nada que o servidor possa fazer
// sozinho com o token que ele tem. Um botao de "reparar" aqui falharia
// exatamente nos casos em que e' preciso, que e' pior que nao existir.

import (
	"context"
	"sort"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/bitrix"
)

// abasEsperadas: as tres que crm.go registra em placement.bind. Mantidas em
// sincronia com `wanted` la' — divergir aqui faria a Saude acusar falta de uma
// aba que o app nunca tentou registrar.
var abasEsperadas = map[string]string{
	"CRM_CONTACT_DETAIL_TAB": "Contato",
	"CRM_LEAD_DETAIL_TAB":    "Lead",
	"CRM_DEAL_DETAIL_TAB":    "Negócio",
}

// healthAbasCRM confere quais das tres abas estao de fato vinculadas.
func (h *handlers) healthAbasCRM(ctx context.Context, creds bitrix.TenantCreds) fiber.Map {
	out := fiber.Map{}

	// placement.get, nao placement.list: o primeiro diz o que o app VINCULOU,
	// o segundo so' o catalogo do que ele poderia usar. Trocar um pelo outro
	// fazia a tela relatar "0 abas" num portal com as quatro funcionando —
	// ver bug #24.
	ligados, err := h.bitrixClient.ListBoundPlacements(ctx, creds)
	if err != nil {
		// "Nao consegui perguntar" nao e' "esta' faltando". Acusar falta aqui
		// mandaria o suporte reinstalar abas que talvez estejam perfeitas.
		out["indeterminado"] = true
		out["detalhe"] = "nao deu pra consultar o portal: " + err.Error()
		return out
	}

	presentes := map[string]bool{}
	for _, p := range ligados {
		if nome := nomeDoPlacement(p); nome != "" {
			presentes[nome] = true
		}
	}

	// Veio registro, mas nenhum nome reconhecivel: a resposta mudou de formato.
	// Nesse caso o certo e' dizer "nao sei", nunca "estao faltando" — foi
	// exatamente assim que o bug #24 mandou o suporte re-registrar quatro abas
	// que estavam perfeitas. Errar para o lado do silencio e' pior aqui, mas
	// errar para o lado do alarme falso e' o que ja' aconteceu.
	if len(ligados) > 0 && len(presentes) == 0 {
		out["indeterminado"] = true
		out["detalhe"] = "o portal respondeu, mas nao reconheci o formato da resposta — " +
			"nao da' pra afirmar que as abas estao faltando"
		return out
	}

	var faltando []string
	lista := make([]fiber.Map, 0, len(abasEsperadas))
	for codigo, rotulo := range abasEsperadas {
		ok := presentes[codigo]
		lista = append(lista, fiber.Map{"codigo": codigo, "onde": rotulo, "vinculada": ok})
		if !ok {
			faltando = append(faltando, rotulo)
		}
	}
	sort.Slice(lista, func(i, j int) bool {
		return lista[i]["codigo"].(string) < lista[j]["codigo"].(string)
	})
	sort.Strings(faltando)

	out["abas"] = lista
	out["total_vinculadas"] = len(abasEsperadas) - len(faltando)
	out["total_esperadas"] = len(abasEsperadas)

	if len(faltando) > 0 {
		out["problema"] = "a aba do UC Talk nao esta' no card de " + strings.Join(faltando, ", ") +
			". Registrar aba exige usuario ADMINISTRADOR do portal: se o app foi instalado " +
			"por um usuario comum, o registro falha e nao avisa ninguem."
		out["acao"] = "Peça a um administrador do Bitrix para abrir o UC Talk uma vez — o registro " +
			"acontece sozinho nesse acesso. Se ele já for admin e mesmo assim faltar, use " +
			"Ferramentas → Re-registrar abas."
	}

	// A aba existe mas o cliente jura que nao ve: quase sempre e' o menu
	// "Mais" do card. Custou dois dias em 30/09 — o aviso fica aqui para nao
	// custar de novo.
	if len(faltando) == 0 {
		out["nota"] = "As três abas estão vinculadas. Se o cliente não as encontra, elas estão " +
			"no menu \"Mais\" do card — o Bitrix recolhe as abas que não cabem na barra."
	}
	return out
}

// nomeDoPlacement extrai o codigo do placement tolerando a caixa da chave.
//
// O Bitrix devolve `placement` em alguns metodos e `PLACEMENT` em outros, e a
// caixa ja' mudou entre versoes. Ler so' uma grafia faria a Saude concluir que
// as tres abas sumiram num portal onde nada mudou.
func nomeDoPlacement(p map[string]interface{}) string {
	for chave, v := range p {
		if !strings.EqualFold(chave, "placement") {
			continue
		}
		if nome, ok := v.(string); ok && nome != "" {
			return strings.ToUpper(strings.TrimSpace(nome))
		}
	}
	return ""
}
