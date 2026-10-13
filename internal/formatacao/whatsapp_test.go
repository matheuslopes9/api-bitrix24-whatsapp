package formatacao

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// testdata/whatsapp_web.json tem pares [entrada, arvore] gerados pelo parser
// do PROPRIO WhatsApp Web (WAWebFormatParse com a configuracao de conversa),
// rodado no navegador em 13/10/2026: 62 casos escritos a mao e 6000
// aleatorios com *, _, ~, `, ```, quebras de linha, listas, citacao e emoji.
//
// A arvore vem na notacao <b>..</b>, <i>, <s>, <code>, <icode>, <ul>, <ol>,
// <li*> (item com * ou •), <li-> (item com -), <li#> (item numerado) e <q>;
// delimitadores somem e \n vira ⏎.
func TestParseIgualAoWhatsAppWeb(t *testing.T) {
	raw, err := os.ReadFile("testdata/whatsapp_web.json")
	if err != nil {
		t.Fatal(err)
	}
	var casos [][2]string
	if err := json.Unmarshal(raw, &casos); err != nil {
		t.Fatal(err)
	}
	falhas := 0
	for _, c := range casos {
		got := notacao(Parse(c[0]))
		if got != c[1] {
			falhas++
			if falhas <= 15 {
				t.Errorf("entrada %q\n  whatsapp: %s\n  nosso:    %s", c[0], c[1], got)
			}
		}
	}
	if falhas > 0 {
		t.Errorf("%d de %d casos divergem do WhatsApp Web", falhas, len(casos))
	}
}

func notacao(raiz *No) string {
	var b strings.Builder
	var anda func(nos []*No)
	anda = func(nos []*No) {
		for i, n := range nos {
			var tag string
			switch n.Tipo {
			case Texto:
				b.WriteString(strings.ReplaceAll(n.Valor, "\n", "⏎"))
				continue
			case Delimitador:
				continue
			case Negrito:
				tag = "b"
			case Italico:
				tag = "i"
			case Riscado:
				tag = "s"
			case CodigoBloco:
				tag = "code"
			case CodigoLinha:
				tag = "icode"
			case Lista:
				tag = "ul"
			case ListaNum:
				tag = "ol"
			case ListaNumItem:
				tag = "li#"
			case Citacao:
				tag = "q"
			case ListaItem:
				tag = "li*"
				if i > 0 && nos[i-1].Tipo == Delimitador && nos[i-1].Valor == "-" {
					tag = "li-"
				}
			}
			b.WriteString("<" + tag + ">")
			anda(n.Filhos)
			b.WriteString("</" + tag + ">")
		}
	}
	anda(raiz.Filhos)
	return b.String()
}
