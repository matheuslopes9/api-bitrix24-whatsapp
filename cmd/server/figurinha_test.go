package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// A PRIMEIRA versao desta correcao assumiu que o Bitrix mandava adesivo em
// image/webp. Nao mandava. Enviei um adesivo de verdade pelo Contact Center e
// li o raw_body do evento:
//
//	files[0][name]  = sticker_4_1.png
//	files[0][mime]  = image/png
//	files[0][link]  = .../im/stickers/bitrixReactions/1.png
//	files[0][width] = 512  height = 512
//
// PNG. A deteccao por webp nao pegava nada, e o adesivo continuou chegando
// como foto depois de um deploy inteiro. Os casos abaixo sao essa medicao,
// nao uma suposicao minha.

func TestReconheceAdesivoRealDoBitrix(t *testing.T) {
	// Exatamente o que o portal enviou.
	if !ehFigurinha("image/png", "sticker_4_1.png",
		"https://crm.uctechnology.com.br/bitrix/images/im/stickers/bitrixReactions/1.png") {
		t.Fatal("o adesivo real do Bitrix nao foi reconhecido — ele volta a chegar como foto")
	}
}

func TestReconheceFigurinha(t *testing.T) {
	casos := []struct {
		mime, nome, link string
		quer             bool
		porque           string
	}{
		{"image/webp", "x.webp", "", true, "ja' e' figurinha"},
		{"IMAGE/WEBP", "x", "", true, "mime em caixa alta"},
		{"image/png", "1.png", "https://portal/bitrix/images/im/stickers/animals/3.png", true, "o caminho denuncia o adesivo"},
		{"image/png", "sticker_9_2.png", "", true, "o nome que o Bitrix da'"},
		{"", "algo.webp", "", true, "sem mime, a extensao resolve"},

		{"image/jpeg", "foto.jpg", "https://portal/upload/foto.jpg", false, "foto comum"},
		{"image/png", "print.png", "", false, "png fora da pasta de adesivos"},
		{"application/pdf", "doc.pdf", "", false, "documento"},
		{"audio/ogg", "File.ogg", "", false, "audio — medido no mesmo teste"},
		{"", "", "", false, "nada identificavel"},
	}
	for _, c := range casos {
		if got := ehFigurinha(c.mime, c.nome, c.link); got != c.quer {
			t.Errorf("ehFigurinha(%q,%q,%q) = %v, esperado %v — %s",
				c.mime, c.nome, c.link, got, c.quer, c.porque)
		}
	}
}

func pngDeTeste(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 120, 255})
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// Detectar sem converter nao serve pra nada: o WhatsApp so' aceita WebP, e um
// PNG enviado como StickerMessage e' recusado ou chega quebrado.
func TestConverteAdesivoQuadradoParaWebP(t *testing.T) {
	webp, ok := converterParaFigurinha(pngDeTeste(512, 512), "image/png")
	if !ok {
		t.Fatal("adesivo 512x512 nao converteu — continuaria chegando como foto")
	}
	// Assinatura do container: "RIFF" .... "WEBP".
	if len(webp) < 12 || string(webp[0:4]) != "RIFF" || string(webp[8:12]) != "WEBP" {
		t.Errorf("a saida nao e' um arquivo WebP valido (%d bytes, cabecalho %q)",
			len(webp), string(webp[:min(12, len(webp))]))
	}
}

// Imagem nao-quadrada NAO vira figurinha: o WhatsApp distorceria, e figurinha
// perde a legenda (o protocolo nao tem o campo). Melhor seguir como imagem.
func TestNaoConverteImagemQueNaoEhAdesivo(t *testing.T) {
	casos := []struct {
		dados  []byte
		mime   string
		porque string
	}{
		{pngDeTeste(800, 600), "image/png", "retangular"},
		{pngDeTeste(1024, 1024), "image/png", "quadrada porem maior que 512"},
		{[]byte("isto nao e' imagem"), "image/png", "bytes invalidos"},
		{nil, "image/png", "vazio"},
	}
	for _, c := range casos {
		if _, ok := converterParaFigurinha(c.dados, c.mime); ok {
			t.Errorf("converteu o que nao devia (%s) — a legenda seria perdida", c.porque)
		}
	}
}

// WebP que ja' chega pronto passa direto. Reconverter so' perderia qualidade.
func TestWebPPassaSemReconverter(t *testing.T) {
	original := []byte("RIFF....WEBPfake")
	saida, ok := converterParaFigurinha(original, "image/webp")
	if !ok || !bytes.Equal(saida, original) {
		t.Error("webp pronto foi alterado no caminho")
	}
}
