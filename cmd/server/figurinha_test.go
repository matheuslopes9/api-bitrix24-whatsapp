package main

import "testing"

// O adesivo enviado pelo operador chegava no WhatsApp como FOTO — com bolha,
// sombra e horario dentro, em vez do adesivo solto e transparente. Quem recebe
// percebe na hora que do outro lado nao tem uma pessoa no WhatsApp, que e'
// justamente o que o conector existe pra esconder.
//
// A causa era ordem de switch: image/webp casa com "image/" e caia no
// SendImage. O teste de figurinha precisa vir ANTES, e precisa acertar os dois
// lados — adesivo que vira foto era o sintoma; foto que vira adesivo seria o
// oposto, e pior, porque figurinha nao aceita legenda.
func TestReconheceFigurinha(t *testing.T) {
	casos := []struct {
		mime, nome string
		quer       bool
		porque     string
	}{
		{"image/webp", "sticker.webp", true, "o caso normal"},
		{"image/webp", "", true, "mime manda, nome nao importa"},
		{"IMAGE/WEBP", "x.webp", true, "mime em caixa alta ainda e' webp"},
		{"image/webp; charset=binary", "x", true, "mime com parametro continua webp"},
		{"", "adesivo.webp", true, "sem mime, o nome e' o unico sinal que sobra"},
		{"", "ADESIVO.WEBP", true, "extensao em caixa alta"},

		{"image/jpeg", "foto.jpg", false, "foto comum"},
		{"image/png", "print.png", false, "png nao e' figurinha"},
		{"image/jpeg", "foto.webp", false, "mime explicito ganha do nome"},
		{"application/pdf", "doc.pdf", false, "documento"},
		{"audio/ogg", "audio.ogg", false, "audio"},
		{"", "planilha.xlsx", false, "sem mime e sem webp"},
		{"", "", false, "nada identificavel"},
	}
	for _, c := range casos {
		if got := ehFigurinha(c.mime, c.nome); got != c.quer {
			t.Errorf("ehFigurinha(%q, %q) = %v, esperado %v — %s",
				c.mime, c.nome, got, c.quer, c.porque)
		}
	}
}
