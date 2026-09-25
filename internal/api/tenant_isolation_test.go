package api

import (
	"testing"

	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
)

// numeroBase e' a CHAVE de comparacao do isolamento entre clientes. Se ela
// normalizar errado, o efeito e' um dos dois desastres:
//
//   - permissiva demais: o cliente A opera o numero do cliente B;
//   - restritiva demais: o cliente nao enxerga o proprio numero e o produto
//     parece quebrado.
//
// Por isso ela e' testada com as formas reais que circulam no sistema.
func TestNumeroBaseNormalizaTodasAsFormas(t *testing.T) {
	casos := map[string]string{
		// Forma completa, com device suffix — a que vem do whatsmeow.
		"558196807479:7@s.whatsapp.net": "558196807479",
		// Device suffix muda a cada re-pareamento e NAO pode virar numero novo.
		"558196807479:1@s.whatsapp.net":  "558196807479",
		"558196807479:47@s.whatsapp.net": "558196807479",
		// Sem device suffix.
		"558196807479@s.whatsapp.net": "558196807479",
		// Só o número, como vem do path de /ui/sessions/:phone/qr.
		"558196807479": "558196807479",
		// Com "+", como o usuário digita.
		"+558196807479": "558196807479",
		// Espaço em volta, de campo de formulário.
		"  558196807479  ": "558196807479",
		// Vazio continua vazio — e vazio nunca casa com ninguém.
		"": "",
	}
	for entrada, querido := range casos {
		if got := numeroBase(entrada); got != querido {
			t.Errorf("numeroBase(%q) = %q, queria %q", entrada, got, querido)
		}
	}
}

// O device suffix e' a armadilha historica deste sistema: ele muda a cada
// re-pareamento. Se ele entrasse na chave, o cliente perderia acesso ao
// proprio numero toda vez que reconectasse.
func TestDeviceSuffixNaoMudaAChave(t *testing.T) {
	base := numeroBase("558196807479@s.whatsapp.net")
	for _, suf := range []string{":1", ":5", ":7", ":47", ":100"} {
		jid := "558196807479" + suf + "@s.whatsapp.net"
		if got := numeroBase(jid); got != base {
			t.Fatalf("%s virou %q, deveria ser %q — o cliente perderia o proprio numero ao re-parear",
				jid, got, base)
		}
	}
}

// Numeros diferentes NAO podem colidir: colisao aqui deixa um cliente operar
// o numero do outro, que e' exatamente o vazamento que isto corrige.
func TestNumerosDiferentesNaoColidem(t *testing.T) {
	a := numeroBase("558196807479:7@s.whatsapp.net")
	b := numeroBase("558195098320:2@s.whatsapp.net")
	if a == b {
		t.Fatalf("numeros distintos colidiram em %q", a)
	}
	// Prefixo comum nao pode ser tratado como igual.
	c := numeroBase("5581968074@s.whatsapp.net")
	if c == a {
		t.Fatalf("numero mais curto colidiu com %q", a)
	}
}

// Cloud API usa "cloud:<phone_id>" como JID. Nao pode virar string vazia,
// senao toda sessao Cloud casaria com toda sessao Cloud.
func TestJIDCloudNaoViraVazio(t *testing.T) {
	got := numeroBase("cloud:123456789@s.whatsapp.net")
	if got == "" {
		t.Fatal("JID de Cloud API virou vazio — todas casariam entre si")
	}
	outro := numeroBase("cloud:987654321@s.whatsapp.net")
	if got == outro {
		t.Fatalf("dois phone_id distintos colidiram em %q", got)
	}
}

// O escopo vazio de relatorio NAO pode enxergar nada. E' a garantia de que
// um handler que esqueca de preencher o escopo mostre tela vazia — e nao o
// banco inteiro, que foi o bug dos relatorios e do /ui/overview.
func TestEscopoVazioNaoAceitaNenhumNumero(t *testing.T) {
	aceita := aceitaEscopo(db.EscopoNumeros{})
	for _, jid := range []string{"558196807479:7@s.whatsapp.net", "cloud:123", "", "cloud@s.whatsapp.net"} {
		if aceita(jid) {
			t.Errorf("escopo vazio aceitou %q", jid)
		}
	}
}

func TestEscopoDoTenantSoAceitaOsProprios(t *testing.T) {
	aceita := aceitaEscopo(db.EscopoNumeros{Numeros: []string{"5519987717792", "cloud:111"}})
	casos := map[string]bool{
		"5519987717792:7@s.whatsapp.net":  true, // device suffix nao importa
		"5519987717792:44@s.whatsapp.net": true,
		"558196807479:7@s.whatsapp.net":   false, // numero de outro cliente
		"cloud:111":                       true,
		"cloud:222":                       false, // outra Cloud API nao casa so' por ser Cloud
		"cloud@s.whatsapp.net":            false, // legado sem id: nao da' pra dizer de quem e'
	}
	for jid, esperado := range casos {
		if got := aceita(jid); got != esperado {
			t.Errorf("aceita(%q) = %v, esperado %v", jid, got, esperado)
		}
	}
}

func TestEscopoGlobalAceitaTudo(t *testing.T) {
	aceita := aceitaEscopo(db.EscopoNumeros{Todos: true})
	if !aceita("558196807479:7@s.whatsapp.net") || !aceita("cloud:1") {
		t.Fatal("escopo global recusou numero")
	}
}
