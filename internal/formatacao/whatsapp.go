// Package formatacao le a formatacao de texto do WhatsApp (*negrito*,
// _italico_, ~riscado~, `codigo`, ```bloco```, listas e citacao) e a reescreve
// para quem exibe a mensagem do lado de ca.
//
// O parser NAO foi escrito de cabeca: e' um porte do que o proprio WhatsApp
// Web roda (modulos WAWebFormatParse, WAWebFormatMatch, WAWebFormatFilter,
// WAWebFormatParseTree e os *FormatRegex/*FormatMutator, lidos do bundle em
// 13/10/2026). As regras que parecem estranhas sao dele, e os testes conferem
// a saida contra a do WhatsApp Web rodando de verdade (ver whatsapp_test.go):
//
//   - "a*b*c" NAO e' negrito: o marcador precisa de borda (inicio, espaco,
//     pontuacao, simbolo/emoji) do lado de fora;
//   - "*a*b*" e' negrito de "a*b": o primeiro "*" de fechamento nao tem borda
//     depois, entao o WhatsApp segue procurando;
//   - negrito, italico e riscado nao atravessam quebra de linha; o bloco
//     ```...``` atravessa e nada dentro dele e' formatado;
//   - lista numerada so' ate' 2 digitos ("100. x" fica texto).
package formatacao

import "unicode"

// Tipo identifica um no' da arvore.
type Tipo int

const (
	Texto       Tipo = iota // texto puro
	Delimitador             // marcador consumido ("*", "```", "1.", ">")
	Raiz
	Negrito
	Italico
	Riscado
	CodigoBloco  // ```...```
	CodigoLinha  // `...`
	ListaItem    // "* x", "• x" ou "- x"
	Lista        // bloco de itens com marcador
	ListaNumItem // "1. x"
	ListaNum     // bloco de itens numerados
	Citacao      // "> x"
)

// No e' um pedaco do texto. Texto e Delimitador carregam Valor; os demais
// carregam Filhos. Em ListaItem e ListaNumItem, Marcador guarda o que abriu
// o item ("*", "•", "-" ou "1.").
type No struct {
	Tipo     Tipo
	Valor    string
	Marcador string
	Filhos   []*No
}

// Parse quebra a mensagem na mesma arvore que o WhatsApp Web monta.
func Parse(s string) *No {
	rs := []rune(s)
	var matches [][]faixa
	for idx, m := range mutadores {
		fs := m.extrair(rs)
		for i := range fs {
			fs[i].mut = idx
		}
		if len(fs) > 0 {
			matches = append(matches, fs)
		}
	}
	raiz := &No{Tipo: Raiz}
	if len(matches) == 0 {
		raiz.Filhos = []*No{{Tipo: Texto, Valor: s}}
		return raiz
	}
	return montarArvore(rs, filtrar(ordenar(matches)))
}

// faixa e' um match: [ini, fim] do trecho inteiro (com delimitadores) e
// [iniDentro, fimDentro] do conteudo — indices inclusivos, como no WA.
type faixa struct {
	ini, iniDentro, fimDentro, fim int
	mut                            int
}

type mutador struct {
	tipo    Tipo
	grupo   int  // prioridade: menor ganha nos conflitos
	formata bool // false = nada dentro e' formatado (codigo)
	aninha  func(pai Tipo) bool
	extrair func(rs []rune) []faixa
}

// A ordem e os grupos seguem WAWebFormatConfigurationConversation com
// parseInlineCode, parseLists e parseQuotes ligados. Mencoes, links e emoji
// ficam de fora: nao mudam o texto, so' o desenho.
var mutadores = []mutador{
	{tipo: CodigoBloco, grupo: 0, formata: false, aninha: sempre, extrair: extrairCodigoBloco},
	{tipo: CodigoLinha, grupo: 1, formata: false, aninha: sempre, extrair: extrairDelimitado('`', regraCodigoLinha)},
	{tipo: ListaItem, grupo: 2, formata: true, aninha: soEm(Lista), extrair: extrairItem(marcadorAsterisco)},
	{tipo: ListaItem, grupo: 3, formata: true, aninha: soEm(Lista), extrair: extrairItem(marcadorHifen)},
	{tipo: Lista, grupo: 4, formata: true, aninha: nunca, extrair: extrairLista},
	{tipo: ListaNumItem, grupo: 5, formata: true, aninha: soEm(ListaNum), extrair: extrairItemNum},
	{tipo: ListaNum, grupo: 6, formata: true, aninha: nunca, extrair: extrairListaNum},
	{tipo: Negrito, grupo: 7, formata: true, aninha: sempre, extrair: extrairDelimitado('*', regraNegrito)},
	{tipo: Italico, grupo: 7, formata: true, aninha: sempre, extrair: extrairDelimitado('_', regraComum)},
	{tipo: Riscado, grupo: 7, formata: true, aninha: sempre, extrair: extrairDelimitado('~', regraComum)},
	{tipo: Citacao, grupo: 8, formata: true, aninha: sempre, extrair: extrairCitacao},
}

func sempre(Tipo) bool { return true }
func nunca(Tipo) bool  { return false }
func soEm(t Tipo) func(Tipo) bool {
	return func(pai Tipo) bool { return pai == t }
}

// ── Classes de caractere ────────────────────────────────────────────────

// espaco e' o \s do JavaScript.
func espaco(r rune) bool {
	switch {
	case r >= '\t' && r <= '\r', r == ' ', r == 0xA0, r == 0x1680,
		r >= 0x2000 && r <= 0x200A, r == 0x2028, r == 0x2029,
		r == 0x202F, r == 0x205F, r == 0x3000, r == 0xFEFF:
		return true
	}
	return false
}

// borda: o que pode encostar do lado de fora de *, _, ~ e `. A classe do WA
// e' exatamente pontuacao (P), simbolo (S, inclui emoji), separador (Z),
// formato (Cf) e \t-\r — conferido codepoint a codepoint contra a regex dele.
func borda(r rune) bool {
	return (r >= '\t' && r <= '\r') ||
		unicode.In(r, unicode.P, unicode.S, unicode.Z, unicode.Cf)
}

// fimDeLinha: onde ^ e $ casam numa regex JS com a flag m.
func fimDeLinha(r rune) bool {
	return r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029
}

func inicioDeLinha(rs []rune, i int) bool {
	return i == 0 || fimDeLinha(rs[i-1])
}

func digito(r rune) bool { return r >= '0' && r <= '9' }

// ── Extratores (um por regex do WA) ─────────────────────────────────────

// regraDelim decide se o conteudo pode comecar em rs[i].
type regraDelim func(rs []rune, i int) bool

// Negrito: (?![\s]|⃣|️⃣) — nao abre colado em espaco nem
// vira o "*" do emoji de tecla (*️⃣).
func regraNegrito(rs []rune, i int) bool {
	if i >= len(rs) || espaco(rs[i]) || rs[i] == 0x20E3 {
		return false
	}
	return !(rs[i] == 0xFE0F && i+1 < len(rs) && rs[i+1] == 0x20E3)
}

func regraComum(rs []rune, i int) bool {
	return i < len(rs) && !espaco(rs[i])
}

func regraCodigoLinha(rs []rune, i int) bool {
	return i < len(rs) && !espaco(rs[i]) && rs[i] != '`'
}

// extrairDelimitado porta as regex de negrito/italico/riscado/codigo:
//
//	(?:^|BORDA) D (conteudo sem \n, que comeca e termina sem espaco) D (?=$|BORDA)
//
// com o conteudo preguicoso (fecha no PRIMEIRO D valido) e o ajuste do
// WAWebAdjustDelimitedRange: o match perde o caractere de borda da frente, e
// a busca seguinte recomeca NO delimitador de fechamento — por isso "*a**b*"
// sao dois negritos.
func extrairDelimitado(d rune, regra regraDelim) func([]rune) []faixa {
	return func(rs []rune) []faixa {
		var out []faixa
		p := 0
		for p < len(rs) {
			f, prox, ok := casarDelimitado(rs, p, d, regra)
			if !ok {
				break
			}
			out = append(out, f)
			p = prox
		}
		return out
	}
}

func casarDelimitado(rs []rune, p int, d rune, regra regraDelim) (faixa, int, bool) {
	for s := p; s < len(rs); s++ {
		// (?:^|BORDA): no inicio tenta ^ primeiro; senao consome uma borda.
		var aberturas []int
		if s == 0 && rs[0] == d {
			aberturas = append(aberturas, 0)
		}
		if borda(rs[s]) && s+1 < len(rs) && rs[s+1] == d {
			aberturas = append(aberturas, s+1)
		}
		for _, o := range aberturas {
			if !regra(rs, o+1) {
				continue
			}
			for k := o + 1; k < len(rs) && rs[k] != '\n'; k++ {
				if k == o+1 || rs[k] != d || espaco(rs[k-1]) {
					continue
				}
				if k+1 < len(rs) && !borda(rs[k+1]) {
					continue
				}
				conteudo := string(rs[o+1 : k])
				inteiro := string(rs[o : k+1])
				ini := o + indexRunas(inteiro, conteudo)
				f := faixa{ini: o, iniDentro: ini, fimDentro: ini + len([]rune(conteudo)) - 1, fim: k}
				return f, k, true
			}
		}
	}
	return faixa{}, 0, false
}

// indexRunas e' o String.prototype.indexOf, em runas. O WA calcula o inicio
// do conteudo assim (WAWebExtractRangeFromMatch), e o porte repete isso.
func indexRunas(s, sub string) int {
	a, b := []rune(s), []rune(sub)
	for i := 0; i+len(b) <= len(a); i++ {
		igual := true
		for j := range b {
			if a[i+j] != b[j] {
				igual = false
				break
			}
		}
		if igual {
			return i
		}
	}
	return -1
}

// extrairCodigoBloco: /```([\s\S]*?\S[\s\S]*?)```/g
func extrairCodigoBloco(rs []rune) []faixa {
	var out []faixa
	tres := func(i int) bool { return i+2 < len(rs) && rs[i] == '`' && rs[i+1] == '`' && rs[i+2] == '`' }
	for s := 0; s < len(rs); s++ {
		if !tres(s) {
			continue
		}
		j := s + 3
		for j < len(rs) && espaco(rs[j]) {
			j++
		}
		k := -1
		for i := j + 1; i < len(rs); i++ {
			if tres(i) {
				k = i
				break
			}
		}
		if j >= len(rs) || k < 0 {
			continue
		}
		conteudo := string(rs[s+3 : k])
		ini := s + indexRunas(string(rs[s:k+3]), conteudo)
		out = append(out, faixa{ini: s, iniDentro: ini, fimDentro: ini + (k - s - 3) - 1, fim: k + 2})
		s = k + 2
	}
	return out
}

// fimDaLinha devolve o indice do proximo \n (ou len).
func fimDaLinha(rs []rune, i int) int {
	for i < len(rs) && rs[i] != '\n' {
		i++
	}
	return i
}

func marcadorAsterisco(r rune) bool { return r == '*' || r == 0x2022 }
func marcadorHifen(r rune) bool     { return r == '-' }
func marcadorLista(r rune) bool     { return r == '*' || r == '-' || r == 0x2022 }

// itemNaLinha: MARCADOR " " (?! +) [^\n]+ a partir de i. Devolve o fim da
// linha (exclusivo).
func itemNaLinha(rs []rune, i int, marcador func(rune) bool, exigeSemEspaco bool) (int, bool) {
	if i+2 >= len(rs) || !marcador(rs[i]) || rs[i+1] != ' ' || rs[i+2] == '\n' {
		return 0, false
	}
	if exigeSemEspaco && rs[i+2] == ' ' {
		return 0, false
	}
	return fimDaLinha(rs, i+2), true
}

// extrairItem: /^M( (?! +)[^\n]+$)/gm
func extrairItem(marcador func(rune) bool) func([]rune) []faixa {
	return func(rs []rune) []faixa {
		var out []faixa
		for s := 0; s < len(rs); s++ {
			if !inicioDeLinha(rs, s) {
				continue
			}
			fim, ok := itemNaLinha(rs, s, marcador, true)
			if !ok {
				continue
			}
			out = append(out, faixa{ini: s, iniDentro: s + 1, fimDentro: fim - 1, fim: fim - 1})
			s = fim - 1
		}
		return out
	}
}

// extrairLista: o bloco inteiro de itens seguidos, mais o \n final (grupo 0).
func extrairLista(rs []rune) []faixa {
	var out []faixa
	for s := 0; s < len(rs); s++ {
		if !inicioDeLinha(rs, s) {
			continue
		}
		fim, ok := itemNaLinha(rs, s, marcadorLista, true)
		if !ok {
			continue
		}
		for fim < len(rs) {
			prox, ok := itemNaLinha(rs, fim+1, marcadorLista, true)
			if !ok {
				break
			}
			fim = prox
		}
		if fim < len(rs) {
			fim++ // (?:\n|$) consome o \n
		}
		out = append(out, faixa{ini: s, iniDentro: s, fimDentro: fim - 1, fim: fim - 1})
		s = fim - 1
	}
	return out
}

// numeroNaLinha: (\d{1,2})\. " " a partir de i; devolve onde comeca o " ".
func numeroNaLinha(rs []rune, i int) (int, bool) {
	n := 0
	for n < 2 && i+n < len(rs) && digito(rs[i+n]) {
		n++
	}
	// \d{1,2} com backtracking: tenta 2 digitos, depois 1.
	for ; n >= 1; n-- {
		if i+n+1 < len(rs) && rs[i+n] == '.' && rs[i+n+1] == ' ' {
			return i + n + 1, true
		}
	}
	return 0, false
}

// itemNumNaLinha: (\d{1,2})\. (?! +)[^\n]+ — o primeiro item exige o
// (?! +); os seguintes, na regex do bloco, nao.
func itemNumNaLinha(rs []rune, i int, exigeSemEspaco bool) (espacoEm, fim int, ok bool) {
	e, ok := numeroNaLinha(rs, i)
	if !ok || e+1 >= len(rs) || rs[e+1] == '\n' {
		return 0, 0, false
	}
	if exigeSemEspaco && rs[e+1] == ' ' {
		return 0, 0, false
	}
	return e, fimDaLinha(rs, e+1), true
}

// extrairItemNum: /^(\d{1,2}\.)( (?! +)[^\n]+$)/gm, conteudo = grupo 2.
func extrairItemNum(rs []rune) []faixa {
	var out []faixa
	for s := 0; s < len(rs); s++ {
		if !inicioDeLinha(rs, s) {
			continue
		}
		e, fim, ok := itemNumNaLinha(rs, s, true)
		if !ok {
			continue
		}
		conteudo := string(rs[e:fim])
		ini := s + indexRunas(string(rs[s:fim]), conteudo)
		out = append(out, faixa{ini: s, iniDentro: ini, fimDentro: ini + (fim - e) - 1, fim: fim - 1})
		s = fim - 1
	}
	return out
}

func extrairListaNum(rs []rune) []faixa {
	var out []faixa
	for s := 0; s < len(rs); s++ {
		if !inicioDeLinha(rs, s) {
			continue
		}
		_, fim, ok := itemNumNaLinha(rs, s, true)
		if !ok {
			continue
		}
		for fim < len(rs) {
			_, prox, ok := itemNumNaLinha(rs, fim+1, false)
			if !ok {
				break
			}
			fim = prox
		}
		if fim < len(rs) {
			fim++
		}
		out = append(out, faixa{ini: s, iniDentro: s, fimDentro: fim - 1, fim: fim - 1})
		s = fim - 1
	}
	return out
}

// extrairCitacao: /^>([^\S\n](?! +)[^\n]*$(?:\n|$))/gm — ">" + UM espaco.
func extrairCitacao(rs []rune) []faixa {
	var out []faixa
	for s := 0; s < len(rs); s++ {
		if !inicioDeLinha(rs, s) || rs[s] != '>' || s+1 >= len(rs) {
			continue
		}
		if !espaco(rs[s+1]) || rs[s+1] == '\n' || (s+2 < len(rs) && rs[s+2] == ' ') {
			continue
		}
		fim := fimDaLinha(rs, s+1)
		if fim < len(rs) {
			fim++
		}
		out = append(out, faixa{ini: s, iniDentro: s + 1, fimDentro: fim - 1, fim: fim - 1})
		s = fim - 1
	}
	return out
}

// ── Motor (WAWebFormatMatch / Filter / ParseTree) ───────────────────────

// ordenar intercala os matches de todos os mutadores do inicio MAIOR para o
// menor; no empate, o mutador de indice menor vem antes.
func ordenar(listas [][]faixa) []faixa {
	pos := make([]int, len(listas))
	for i := range listas {
		pos[i] = len(listas[i]) - 1
	}
	var out []faixa
	for len(listas) > 0 {
		v, maior := 0, -1
		for i := range listas {
			if listas[i][pos[i]].ini > maior {
				v, maior = i, listas[i][pos[i]].ini
			}
		}
		out = append(out, listas[v][pos[v]])
		pos[v]--
		if pos[v] < 0 {
			listas = append(listas[:v], listas[v+1:]...)
			pos = append(pos[:v], pos[v+1:]...)
		}
	}
	return out
}

// filtrar resolve sobreposicoes: quem cruza a fronteira de outro cai, a nao
// ser que tenha prioridade maior; nada entra dentro de codigo.
func filtrar(e []faixa) []faixa {
	if len(e) == 1 {
		return e
	}
	var n []faixa
	for len(e) > 0 {
		r := e[len(e)-1]
		e = e[:len(e)-1]
		u := mutadores[r.mut]
		aceita := true
		for p := len(n) - 1; p >= 0; p-- {
			ac := n[p]
			if r.ini > ac.fim {
				continue
			}
			c := mutadores[ac.mut]
			if r.fim > ac.fimDentro {
				if u.grupo < c.grupo {
					n = append(n[:p], n[p+1:]...)
					continue
				}
				aceita = false
				break
			}
			if !u.aninha(c.tipo) && u.grupo < c.grupo {
				n = append(n[:p], n[p+1:]...)
				continue
			}
			if !c.formata {
				aceita = false
				break
			}
		}
		if aceita {
			n = append(n, r)
		}
	}
	return n
}

type aberto struct {
	no  *No
	f   faixa
	pai *aberto
}

func montarArvore(rs []rune, fs []faixa) *No {
	raiz := &No{Tipo: Raiz}
	topo := &aberto{no: raiz}
	sub := func(a, b int) string { return string(rs[a:b]) }
	a := 0
	fechar := func(i *aberto) {
		l, e := i.f.fimDentro, i.f.fim
		if a <= l {
			i.no.Filhos = append(i.no.Filhos, &No{Tipo: Texto, Valor: sub(a, l+1)})
		}
		a = e + 1
		if l != e {
			i.pai.no.Filhos = append(i.pai.no.Filhos, &No{Tipo: Delimitador, Valor: sub(l+1, a)})
		}
	}
	for _, c := range fs {
		d, m := c.ini, c.iniDentro
		for topo.pai != nil && d > topo.f.fimDentro {
			fechar(topo)
			topo = topo.pai
		}
		if a < d {
			topo.no.Filhos = append(topo.no.Filhos, &No{Tipo: Texto, Valor: sub(a, d)})
		}
		a = m
		if d != m {
			topo.no.Filhos = append(topo.no.Filhos, &No{Tipo: Delimitador, Valor: sub(d, m)})
		}
		no := &No{Tipo: mutadores[c.mut].tipo}
		if no.Tipo == ListaItem || no.Tipo == ListaNumItem {
			no.Marcador = sub(d, m)
		}
		topo.no.Filhos = append(topo.no.Filhos, no)
		topo = &aberto{no: no, f: c, pai: topo}
	}
	for topo.pai != nil {
		fechar(topo)
		topo = topo.pai
	}
	if a != len(rs) {
		raiz.Filhos = append(raiz.Filhos, &No{Tipo: Texto, Valor: sub(a, len(rs))})
	}
	return raiz
}
