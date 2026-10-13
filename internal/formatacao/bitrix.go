package formatacao

import (
	"regexp"
	"strings"
)

// ParaBitrix reescreve a formatacao do WhatsApp no BBCode que o chat do
// Bitrix24 desenha. O que cada codigo vira foi conferido no messenger do
// portal (crm.uctechnology.com.br, 13/10/2026), nao so' na documentacao:
//
//	*negrito*   → [B]   _italico_ → [I]   ~riscado~ → [S]   (aninham entre si)
//	```bloco``` → [CODE]  — no Bitrix e' um bloco com barra lateral, sem
//	                        fonte monoespacada, e sempre quebra a linha
//	`codigo`    → [CODE] so' quando ocupa a linha sozinho (um protocolo, um
//	              cupom); no meio da frase fica com as crases, porque o
//	              [CODE] partiria a frase em tres linhas
//	"> x"       → ">>x"   — a citacao nativa do Bitrix; linhas seguidas viram
//	                        um bloco so'
//	"* x"/"• x" → "• x"     "- x" e "1. x" ficam como estao
//
// Sublinhado nao existe no WhatsApp, entao nunca chega [U].
//
// O texto do cliente nao pode virar BBCode: "[SEND=x]clique[/SEND]" vira um
// botao que manda mensagem, e "[URL=...]texto[/URL]" um link disfarcado na
// tela do operador. Ver neutralizar.
func ParaBitrix(s string) string {
	if !strings.ContainsAny(s, "*_~`>-•[0123456789") {
		return s
	}
	var b strings.Builder
	escreverBitrix(&b, Parse(s).Filhos, true)
	return b.String()
}

// naLinha: os nos sao filhos diretos da raiz, ou seja, nao estao dentro de
// negrito, item de lista ou citacao.
func escreverBitrix(b *strings.Builder, nos []*No, naLinha bool) {
	for i, n := range nos {
		switch n.Tipo {
		case Texto:
			b.WriteString(neutralizar(n.Valor))
		case Delimitador:
		case Negrito:
			envolver(b, "B", n.Filhos)
		case Italico:
			envolver(b, "I", n.Filhos)
		case Riscado:
			envolver(b, "S", n.Filhos)
		case CodigoBloco:
			// O \n logo depois de ``` e logo antes do fechamento sobra como
			// linha vazia dentro do bloco no Bitrix.
			b.WriteString("[CODE]" + neutralizar(strings.Trim(textoPuro(n.Filhos), "\n")) + "[/CODE]")
		case CodigoLinha:
			if naLinha && sozinhoNaLinha(nos, i) {
				b.WriteString("[CODE]" + neutralizar(textoPuro(n.Filhos)) + "[/CODE]")
			} else {
				b.WriteString("`" + neutralizar(textoPuro(n.Filhos)) + "`")
			}
		case ListaItem:
			marcador := n.Marcador
			if marcador == "*" {
				marcador = "•"
			}
			b.WriteString(marcador)
			escreverBitrix(b, n.Filhos, false)
		case ListaNumItem:
			b.WriteString(n.Marcador)
			escreverBitrix(b, n.Filhos, false)
		case Citacao:
			// O conteudo comeca no espaco depois do ">"; o Bitrix mostraria
			// esse espaco na frente do texto citado.
			b.WriteString(">>")
			if len(n.Filhos) > 0 && n.Filhos[0].Tipo == Texto {
				primeiro := *n.Filhos[0]
				primeiro.Valor = strings.TrimLeft(primeiro.Valor, " \t")
				escreverBitrix(b, append([]*No{&primeiro}, n.Filhos[1:]...), false)
			} else {
				escreverBitrix(b, n.Filhos, false)
			}
		default: // Lista, ListaNum
			escreverBitrix(b, n.Filhos, false)
		}
	}
}

func envolver(b *strings.Builder, tag string, filhos []*No) {
	b.WriteString("[" + tag + "]")
	escreverBitrix(b, filhos, false)
	b.WriteString("[/" + tag + "]")
}

// textoPuro junta o texto de um no' de codigo (dentro dele nada e' formatado,
// entao so' ha' Texto).
func textoPuro(nos []*No) string {
	var b strings.Builder
	for _, n := range nos {
		if n.Tipo == Texto {
			b.WriteString(n.Valor)
		} else {
			b.WriteString(textoPuro(n.Filhos))
		}
	}
	return b.String()
}

// sozinhoNaLinha: entre a quebra de linha anterior e a seguinte so' existe o
// codigo (e espacos). Os vizinhos imediatos sao as crases; olha alem delas.
func sozinhoNaLinha(irmaos []*No, i int) bool {
	antes, depois := i-1, i+1
	if antes >= 0 && irmaos[antes].Tipo == Delimitador {
		antes--
	}
	if depois < len(irmaos) && irmaos[depois].Tipo == Delimitador {
		depois++
	}
	if antes >= 0 {
		v := irmaos[antes]
		if v.Tipo != Texto || !strings.HasSuffix(strings.TrimRight(v.Valor, " \t"), "\n") {
			return false
		}
	}
	if depois < len(irmaos) {
		v := irmaos[depois]
		if v.Tipo != Texto || !strings.HasPrefix(strings.TrimLeft(v.Valor, " \t"), "\n") {
			return false
		}
	}
	return true
}

// tagBitrix casa a abertura de qualquer codigo no formato do Bitrix: "[B]",
// "[/URL]", "[URL=", "[IMG SIZE=". Todo codigo dele e' assim, inclusive os que
// ainda nao existem hoje.
var tagBitrix = regexp.MustCompile(`\[(/?[A-Za-z]+[\]= ])`)

// neutralizar poe um espaco de largura zero (U+200B) depois do "[" de tudo que
// pareca codigo do Bitrix. O operador continua vendo "[B]" escrito, e o
// Bitrix deixa de tratar como codigo. Entidade HTML ("&#91;") nao serve: o
// Bitrix decodifica e formata do mesmo jeito (testado no portal).
func neutralizar(s string) string {
	if !strings.Contains(s, "[") {
		return s
	}
	return tagBitrix.ReplaceAllString(s, "[​$1")
}
