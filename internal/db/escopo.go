package db

import "fmt"

// EscopoNumeros diz de quais numeros de WhatsApp uma consulta de relatorio
// pode contar mensagens.
//
// O VALOR ZERO NAO ENXERGA NADA. De proposito: os relatorios eram globais e
// um cliente via os numeros, os volumes e os CONTATOS dos outros. Quem
// esquecer de preencher o escopo recebe relatorio vazio, nunca o de todo
// mundo. Visao global exige pedir explicitamente com Todos=true.
type EscopoNumeros struct {
	// Todos libera todos os numeros. So' para quem ja' e' global por
	// natureza (X-API-Key da UC Technology).
	Todos bool
	// Numeros na forma de numeroBase: "5581..." para QR, "cloud:<id>" para
	// Cloud API. Sem device suffix nem dominio.
	Numeros []string
}

// sqlNumeroBase devolve a expressao SQL que reduz um JID ao mesmo formato de
// EscopoNumeros.Numeros. Espelha numeroBase (internal/api/tenant_isolation.go):
//
//	"5581...:7@s.whatsapp.net" -> "5581..."
//	"cloud:123@s.whatsapp.net" -> "cloud:123"   (o prefixo NAO pode cair:
//	                                              sem ele toda sessao Cloud
//	                                              viraria "cloud" e casaria
//	                                              com todas as outras)
func sqlNumeroBase(expr string) string {
	semDominio := fmt.Sprintf("SPLIT_PART(%s, '@', 1)", expr)
	return fmt.Sprintf(
		"(CASE WHEN %[1]s LIKE 'cloud:%%' THEN %[1]s ELSE LTRIM(SPLIT_PART(%[1]s, ':', 1), '+') END)",
		semDominio)
}

// sqlNossoJID e' o JID do NOSSO lado da conversa: quem enviou, na saida;
// quem recebeu, na entrada.
const sqlNossoJID = "(CASE WHEN direction = 'outbound' THEN from_jid ELSE to_jid END)"

// filtroEscopo monta o trecho de WHERE que aplica o escopo. pTodos e
// pNumeros sao as posicoes dos parametros ($n) na query.
func filtroEscopo(pTodos, pNumeros int) string {
	return fmt.Sprintf("($%d::bool OR %s = ANY($%d::text[]))", pTodos, sqlNumeroBase(sqlNossoJID), pNumeros)
}
