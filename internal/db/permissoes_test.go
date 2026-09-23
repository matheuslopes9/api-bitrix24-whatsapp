package db

import "testing"

// A normalizacao do session_jid decide quem consegue ENVIAR mensagem. Errar
// pra um lado bloqueia operador liberado (o bug relatado); errar pro outro
// libera quem nao deveria — inclusive entre contas Cloud diferentes.
func TestNormalizarSessionJID(t *testing.T) {
	casos := []struct{ entrada, querido, porque string }{
		{"558196807479:5@s.whatsapp.net", "558196807479", "tira device suffix e dominio"},
		{"558196807479:1@s.whatsapp.net", "558196807479", "suffix antigo da no mesmo numero"},
		{"558196807479@s.whatsapp.net", "558196807479", "sem suffix tambem normaliza"},
		{"558196807479", "558196807479", "ja normalizado nao muda"},
		{"", "", "wildcard do master preservado"},
		{"   ", "", "espaco conta como wildcard"},
		{"cloud:123456@s.whatsapp.net", "cloud:123456@s.whatsapp.net", "Cloud fica intacta"},
		{"cloud:999@x", "cloud:999@x", "Cloud com outro id fica intacta"},
	}
	for _, c := range casos {
		if got := normalizarSessionJID(c.entrada); got != c.querido {
			t.Errorf("normalizarSessionJID(%q) = %q, queria %q (%s)",
				c.entrada, got, c.querido, c.porque)
		}
	}
}

// Duas contas Cloud DIFERENTES nao podem colapsar no mesmo identificador —
// seria dar a permissao de uma conta oficial para outra.
func TestCloudNaoColapsa(t *testing.T) {
	a := normalizarSessionJID("cloud:111@s.whatsapp.net")
	b := normalizarSessionJID("cloud:222@s.whatsapp.net")
	if a == b {
		t.Fatalf("duas contas Cloud colapsaram em %q", a)
	}
}

// Dois device suffixes do MESMO numero precisam colapsar — e' exatamente o
// que faz a permissao sobreviver ao re-pareamento.
func TestMesmoNumeroColapsa(t *testing.T) {
	a := normalizarSessionJID("558196807479:1@s.whatsapp.net")
	b := normalizarSessionJID("558196807479:5@s.whatsapp.net")
	if a != b {
		t.Fatalf("mesmo numero nao colapsou: %q vs %q", a, b)
	}
}
