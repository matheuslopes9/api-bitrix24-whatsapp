package whatsapp

import "testing"

// A Meta trata figurinha como tipo proprio ("sticker"), nao como imagem. Com
// image/webp caindo em "image", o adesivo do operador chegava como foto — com
// bolha e horario dentro. Mesmo sintoma que o caminho QR tinha, e a mesma
// causa: ordem de switch, porque image/webp casa com os dois testes.
func TestInferMediaTypeSeparaFigurinhaDeImagem(t *testing.T) {
	casos := map[string]string{
		"image/webp":               "sticker",
		"IMAGE/WEBP":               "sticker",
		"image/webp; codecs=vp8":   "sticker",
		"image/jpeg":               "image",
		"image/png":                "image",
		"audio/ogg; codecs=opus":   "audio",
		"audio/mpeg":               "audio",
		"video/mp4":                "video",
		"application/pdf":          "document",
		"application/octet-stream": "document",
	}
	for mime, quer := range casos {
		if got := inferMediaType(mime); got != quer {
			t.Errorf("inferMediaType(%q) = %q, esperado %q", mime, got, quer)
		}
	}
}
