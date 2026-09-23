package whatsapp

import (
	"strings"
	"testing"
)

// O 9o digito e' a causa de mensagem ativa que nao entrega: o CRM guarda
// todo mundo COM o 9 (heranca da importacao do Wazzup), mas parte das
// contas de WhatsApp so' existe SEM ele. Estes testes fixam quais pares de
// candidatos sao oferecidos ao IsOnWhatsApp — quem decide qual vale e' o
// servidor, nao esta funcao.
func TestVariantesNonoDigito(t *testing.T) {
	casos := []struct {
		nome    string
		entrada string
		quer    []string
	}{
		{"celular com 9 gera variante sem 9", "5581999887766", []string{"5581999887766", "558199887766"}},
		{"celular sem 9 gera variante com 9", "558199887766", []string{"558199887766", "5581999887766"}},
		{"ida e volta sao simetricas", "5511987654321", []string{"5511987654321", "551187654321"}},
		{"fixo nao ganha 9o digito", "558133224455", []string{"558133224455"}},
		{"fixo em 2 nao ganha 9o digito", "558122334455", []string{"558122334455"}},
		{"13 digitos que nao comeca com 9 no assinante fica so'", "5581899887766", []string{"5581899887766"}},
		{"numero de fora do Brasil passa intacto", "351912345678", []string{"351912345678"}},
		{"tamanho inesperado nao inventa variante", "55819988776655", []string{"55819988776655"}},
		{"nao numerico nao gera variante", "55819988776a", []string{"55819988776a"}},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			got := variantesNonoDigito(c.entrada)
			if len(got) != len(c.quer) {
				t.Fatalf("%s: esperava %v, veio %v", c.entrada, c.quer, got)
			}
			for i := range got {
				if got[i] != c.quer[i] {
					t.Fatalf("%s: esperava %v, veio %v", c.entrada, c.quer, got)
				}
			}
		})
	}
}

// O numero como o CRM mandou tem que ser sempre o PRIMEIRO candidato. E'
// essa ordem que garante que a correcao so' pode ajudar quem falha hoje:
// se o original estiver no WhatsApp, e' ele que e' escolhido, e o envio que
// ja' funcionava nao muda de destino.
func TestOriginalEhSempreOPrimeiroCandidato(t *testing.T) {
	for _, n := range []string{"5581999887766", "558199887766", "558133224455", "351912345678"} {
		if got := variantesNonoDigito(n); got[0] != n {
			t.Fatalf("%s: primeiro candidato deveria ser o proprio numero, veio %q", n, got[0])
		}
	}
}

// Query volta do servidor com "+" (e as vezes sufixo), mas o candidato que
// montamos e' so' digito. Sem normalizar, o casamento falha e o envio cai
// no JID original — exatamente o bug que se quer evitar.
func TestApenasDigitos(t *testing.T) {
	casos := map[string]string{
		"+5581999887766":      "5581999887766",
		"5581999887766":       "5581999887766",
		"+55 81 99988-7766":   "5581999887766",
		"+5581999887766@c.us": "5581999887766",
		"":                    "",
	}
	for entrada, quer := range casos {
		if got := apenasDigitos(entrada); got != quer {
			t.Fatalf("apenasDigitos(%q) = %q, queria %q", entrada, got, quer)
		}
	}
}

// Guarda contra regressao silenciosa: toda variante gerada tem que diferir
// do original em exatamente um digito "9" de posicao 5.
func TestVarianteDifereApenasPeloNove(t *testing.T) {
	for _, n := range []string{"5581999887766", "558199887766", "5511987654321"} {
		v := variantesNonoDigito(n)
		if len(v) != 2 {
			continue
		}
		maior, menor := v[0], v[1]
		if len(menor) > len(maior) {
			maior, menor = menor, maior
		}
		if len(maior)-len(menor) != 1 {
			t.Fatalf("%s: variantes deveriam diferir em 1 digito: %v", n, v)
		}
		if maior[4] != '9' {
			t.Fatalf("%s: o digito extra deveria ser o 9 na posicao 5: %v", n, v)
		}
		if maior[:4]+maior[5:] != menor {
			t.Fatalf("%s: tirar o 9 do maior deveria dar o menor: %v", n, v)
		}
		if !strings.HasPrefix(maior, "55") {
			t.Fatalf("%s: variante perdeu o codigo do pais: %v", n, v)
		}
	}
}
