package api

import (
	"regexp"
	"strings"
	"testing"
)

// O painel do cliente carrega ~2.900 linhas de JavaScript dentro de uma string
// Go. Erro de sintaxe ali nao quebra o build, nao quebra teste nenhum, e nao
// aparece no log do servidor: a pagina responde 200 e simplesmente nao
// funciona. O operador ve uma tela morta.
//
// Estes testes cobrem a classe de erro que de fato acontece ao editar JS dentro
// de string Go: a barra de escape se perder no caminho. Aconteceu tres vezes
// numa unica sessao.
//
// NAO RENOMEIE este arquivo para *_js_test.go: "js" e' um GOOS valido
// (WebAssembly), entao o Go trata o sufixo como restricao de plataforma e
// IGNORA o arquivo em qualquer build normal. Os testes somem da lista sem erro
// nenhum — `go test` responde "ok" e nao roda nada. Aconteceu aqui.

// paineis sao os dois HTMLs com JavaScript embutido. Os dois correm o mesmo
// risco, e o do admin e' onde o suporte trabalha: uma tela morta la' significa
// ninguem conseguindo diagnosticar cliente nenhum.
func paineis(t *testing.T) map[string]string {
	t.Helper()
	fontes := map[string]string{"painel do cliente": dashboardHTML, "painel admin": adminHomeHTML}
	out := map[string]string{}
	for nome, html := range fontes {
		i := strings.Index(html, "\n<script>\n")
		f := strings.LastIndex(html, "\n</script>")
		if i < 0 || f < 0 || f <= i {
			t.Fatalf("nao achei o bloco <script> do %s — este teste precisa ser reapontado", nome)
		}
		out[nome] = html[i:f]
	}
	return out
}

// Um onclick montado por concatenacao precisa de \' para fechar a string JS de
// fora e abrir o argumento de dentro. Com ” no lugar de \', o JS vira
// `permGestor(” + x + ”)` — string vazia concatenada, e o parser quebra no
// token seguinte. Foi exatamente o que aconteceu.
func TestOnclickMontadoNaoPerdeuAEscapa(t *testing.T) {
	for nome, js := range paineis(t) {
		verificarOnclick(t, nome, js)
	}
}

func verificarOnclick(t *testing.T, nome, js string) {
	t.Helper()
	// Captura `onclick="funcao(` seguido do que vier ate' a aspa.
	re := regexp.MustCompile(`onclick="[A-Za-z_][A-Za-z0-9_]*\((''|\\')`)
	achados := re.FindAllStringSubmatch(js, -1)
	// Sem isto o teste passaria por nao encontrar nada — e um teste que nao
	// olha para nada da' a mesma sensacao de seguranca que um que olha.
	if len(achados) < 5 {
		t.Fatalf("%s: so' %d onclick montado encontrado; o padrao mudou e o teste virou vazio", nome, len(achados))
	}
	for _, m := range achados {
		if m[1] == "''" {
			t.Errorf("%s: onclick montado sem escape: %q — a barra se perdeu e o JS nao parseia", nome, m[0])
		}
	}
}

// String JS aberta com ' nao pode conter quebra de linha de verdade: JavaScript
// sem template literal nao permite, e o arquivo inteiro deixa de carregar. O
// caso real foi um \n de confirm() que virou newline literal.
func TestConfirmEAlertNaoTemQuebraDeLinhaCrua(t *testing.T) {
	for nome, js := range paineis(t) {
		verificarStringsDeDialogo(t, nome, js)
	}
}

func verificarStringsDeDialogo(t *testing.T, nome, js string) {
	t.Helper()
	for _, chamada := range []string{"confirm('", "alert('", "toast('"} {
		pos := 0
		for {
			i := strings.Index(js[pos:], chamada)
			if i < 0 {
				break
			}
			abre := pos + i + len(chamada)
			// Onde a string literal termina: a primeira ' nao escapada.
			fim := abre
			for fim < len(js) {
				if js[fim] == '\\' {
					fim += 2
					continue
				}
				if js[fim] == '\'' {
					break
				}
				fim++
			}
			if strings.ContainsRune(js[abre:min(fim, len(js))], '\n') {
				trecho := js[abre:min(abre+60, len(js))]
				t.Errorf("%s com quebra de linha crua na string: %q — o \\n virou newline de verdade",
					chamada, trecho)
			}
			pos = abre
		}
	}
}

// As funcoes que a tela de permissoes chama por onclick precisam existir. Um
// onclick apontando pra funcao inexistente so' falha quando alguem clica — e o
// erro fica no console do navegador, onde ninguem olha.
func TestFuncoesChamadasPorOnclickExistem(t *testing.T) {
	for nome, js := range paineis(t) {
		verificarFuncoesExistem(t, nome, js)
	}
}

func verificarFuncoesExistem(t *testing.T, nome, js string) {
	t.Helper()
	re := regexp.MustCompile(`onclick="([a-zA-Z_][A-Za-z0-9_]*)\(`)
	// onclick aceita JS inline de verdade — `onclick="if(x) y()"` e' valido e
	// nao e' chamada de funcao nossa. Sem esta lista o teste acusaria que a
	// "funcao if" nao existe, que e' ruido puro.
	palavraChave := map[string]bool{
		"if": true, "for": true, "while": true, "switch": true, "return": true,
		"typeof": true, "new": true, "delete": true, "void": true, "catch": true,
		"function": true, "do": true,
	}
	vistas := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(js, -1) {
		if palavraChave[m[1]] {
			continue
		}
		vistas[m[1]] = true
	}
	if len(vistas) == 0 {
		t.Fatalf("%s: nenhum onclick encontrado — o teste virou vazio", nome)
	}
	for fn := range vistas {
		if !strings.Contains(js, "function "+fn+"(") {
			t.Errorf("%s: onclick chama %s(), que nao existe no script", nome, fn)
		}
	}
}
