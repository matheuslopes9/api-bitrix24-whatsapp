package bitrix

import "testing"

func TestRotuloPessoa(t *testing.T) {
	casos := []struct{ nome, fone, quer string }{
		{"Rafael uctech", "5519991234567", "Rafael uctech (+55 19 99123-4567)"},
		{"  Ester  ", "551998874034", "Ester (+55 19 9887-4034)"},
		{"Ester", "", "Ester"},
		{"", "5519991234567", "+55 19 99123-4567"},
		{"", "", ""},
	}
	for _, c := range casos {
		if got := RotuloPessoa(c.nome, c.fone); got != c.quer {
			t.Errorf("RotuloPessoa(%q, %q) = %q, queria %q", c.nome, c.fone, got, c.quer)
		}
	}
}

func TestTrocarMencoes(t *testing.T) {
	rotulos := map[string]string{
		"98681319620715": "Rafael uctech (+55 19 99123-4567)",
		"123":            "Curto",
	}
	casos := []struct{ entra, quer string }{
		// o caso do print: o LID no lugar da pessoa marcada
		{"@98681319620715 testando marcação", "@Rafael uctech (+55 19 99123-4567) testando marcação"},
		{"oi @98681319620715, tudo bem?", "oi @Rafael uctech (+55 19 99123-4567), tudo bem?"},
		{"@123 e @1234", "@Curto e @1234"}, // numero mais longo nao e' a mesma pessoa
		{"@999 nao marcado", "@999 nao marcado"},
		{"sem mencao", "sem mencao"},
		{"@", "@"},
	}
	for _, c := range casos {
		if got := TrocarMencoes(c.entra, rotulos); got != c.quer {
			t.Errorf("TrocarMencoes(%q) = %q, queria %q", c.entra, got, c.quer)
		}
	}
}
