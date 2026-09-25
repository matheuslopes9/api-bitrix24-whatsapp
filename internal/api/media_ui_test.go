package api

import "testing"

// O arquivo vem do cliente final e e' servido da nossa origem. Tipo que o
// navegador executa (html, svg, js) aberto inline rodaria script com o
// cookie do operador — tem que sair como download, sempre.
func TestTipoParaServirSoAbreTiposSeguros(t *testing.T) {
	casos := []struct {
		mime   string
		tipo   string
		inline bool
	}{
		{"image/jpeg", "image/jpeg", true},
		{"audio/ogg; codecs=opus", "audio/ogg", true},
		{"AUDIO/MPEG", "audio/mpeg", true},
		{"video/mp4", "video/mp4", true},
		{"application/pdf", "application/pdf", true},
		{"text/html", "application/octet-stream", false},
		{"image/svg+xml", "application/octet-stream", false},
		{"application/javascript", "application/octet-stream", false},
		{"text/html; charset=utf-8", "application/octet-stream", false},
		{"", "application/octet-stream", false},
		{"application/x-msdownload", "application/octet-stream", false},
	}
	for _, c := range casos {
		tipo, inline := tipoParaServir(c.mime)
		if tipo != c.tipo || inline != c.inline {
			t.Errorf("tipoParaServir(%q) = (%q, %v), esperado (%q, %v)", c.mime, tipo, inline, c.tipo, c.inline)
		}
	}
}
