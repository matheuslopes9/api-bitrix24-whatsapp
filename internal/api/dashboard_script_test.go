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

// scriptDoPainel devolve o JS embutido no HTML do dashboard.
func scriptDoPainel(t *testing.T) string {
	t.Helper()
	ini := strings.Index(dashboardHTML, "\n<script>\n")
	fim := strings.LastIndex(dashboardHTML, "\n</script>")
	if ini < 0 || fim < 0 || fim <= ini {
		t.Fatal("nao achei o bloco <script> do painel — este teste precisa ser reapontado")
	}
	return dashboardHTML[ini:fim]
}

// Um onclick montado por concatenacao precisa de \' para fechar a string JS de
// fora e abrir o argumento de dentro. Com ” no lugar de \', o JS vira
// `permGestor(” + x + ”)` — string vazia concatenada, e o parser quebra no
// token seguinte. Foi exatamente o que aconteceu.
func TestOnclickMontadoNaoPerdeuAEscapa(t *testing.T) {
	js := scriptDoPainel(t)
	// Captura `onclick="funcao(` seguido do que vier ate' a aspa.
	re := regexp.MustCompile(`onclick="[A-Za-z_][A-Za-z0-9_]*\((''|\\')`)
	achados := re.FindAllStringSubmatch(js, -1)
	// Sem isto o teste passaria por nao encontrar nada — e um teste que nao
	// olha para nada da' a mesma sensacao de seguranca que um que olha.
	if len(achados) < 5 {
		t.Fatalf("so' %d onclick montado encontrado; o padrao mudou e o teste virou vazio", len(achados))
	}
	for _, m := range achados {
		if m[1] == "''" {
			t.Errorf("onclick montado sem escape: %q — a barra se perdeu e o JS nao parseia", m[0])
		}
	}
}

// String JS aberta com ' nao pode conter quebra de linha de verdade: JavaScript
// sem template literal nao permite, e o arquivo inteiro deixa de carregar. O
// caso real foi um \n de confirm() que virou newline literal.
func TestConfirmEAlertNaoTemQuebraDeLinhaCrua(t *testing.T) {
	js := scriptDoPainel(t)
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
	js := scriptDoPainel(t)
	re := regexp.MustCompile(`onclick="(perm[A-Za-z0-9_]*)\(`)
	vistas := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(js, -1) {
		vistas[m[1]] = true
	}
	if len(vistas) == 0 {
		t.Fatal("nenhum onclick de permissoes encontrado — o teste virou vazio")
	}
	for fn := range vistas {
		if !strings.Contains(js, "function "+fn+"(") {
			t.Errorf("onclick chama %s(), que nao existe no script", fn)
		}
	}
}
