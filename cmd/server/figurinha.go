package main

// figurinha.go — adesivo do Bitrix virando figurinha de verdade no WhatsApp.
//
// O QUE FOI MEDIDO (01/10, enviando um adesivo pelo Contact Center e lendo o
// raw_body do evento):
//
//	files[0][name]  = sticker_4_1.png
//	files[0][type]  = image
//	files[0][mime]  = image/png          <- PNG, nao webp
//	files[0][link]  = .../im/stickers/bitrixReactions/1.png
//	files[0][width] = 512  height = 512
//
// Ou seja: o Bitrix manda adesivo como PNG comum. A primeira versao desta
// correcao assumiu que viria image/webp e nao pegou nada — o adesivo continuou
// chegando como foto. Supor o formato custou um deploy inteiro.
//
// E o WhatsApp so' aceita figurinha em WebP. Entao detectar nao basta: e'
// preciso CONVERTER. Por isso existe um encoder webp aqui.

import (
	"bytes"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"

	"github.com/HugoSmits86/nativewebp"
	_ "golang.org/x/image/webp" // decodificar webp que ja' venha pronto
)

// ehFigurinha decide se o arquivo que veio do Bitrix e' um adesivo.
//
// Tres sinais, do mais forte pro mais fraco:
//
//	image/webp       — ja' e' figurinha, so' repassar
//	.../im/stickers/ — o caminho dos adesivos no Bitrix
//	sticker_*        — o nome que o Bitrix da' ao arquivo
//
// O nome sozinho e' fraco de proposito: quem anexar uma foto chamada
// "sticker_familia.png" nao quer manda-la como figurinha, e figurinha PERDE a
// legenda (o protocolo nao tem o campo). Por isso quem usa isto confere
// tambem se a imagem e' quadrada — ver converterParaFigurinha.
func ehFigurinha(mime, nome, link string) bool {
	if strings.HasPrefix(strings.ToLower(mime), "image/webp") {
		return true
	}
	if strings.Contains(strings.ToLower(link), "/im/stickers/") {
		return true
	}
	n := strings.ToLower(strings.TrimSpace(nome))
	return strings.HasPrefix(n, "sticker_") || strings.HasSuffix(n, ".webp")
}

// ladoMaximoFigurinha: o WhatsApp exibe figurinha num quadrado de 512. Maior
// que isso so' aumenta o arquivo — e figurinha tem limite de tamanho.
const ladoMaximoFigurinha = 512

// converterParaFigurinha devolve os bytes em WebP, prontos pra SendSticker.
//
// Devolve ok=false quando NAO deve virar figurinha. Nesse caso quem chama
// manda como imagem, que e' o comportamento antigo — e preserva a legenda.
//
// Recusa imagem nao-quadrada porque figurinha quadrada e' o unico formato que
// o WhatsApp exibe sem distorcer, e porque a forma e' o que separa um adesivo
// de verdade de uma foto com nome parecido.
func converterParaFigurinha(dados []byte, mime string) ([]byte, bool) {
	if strings.HasPrefix(strings.ToLower(mime), "image/webp") {
		return dados, true // ja' esta' no formato; converter de novo so' perderia qualidade
	}

	img, _, err := image.Decode(bytes.NewReader(dados))
	if err != nil {
		return nil, false
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 || w != h || w > ladoMaximoFigurinha {
		// Nao quadrada, vazia, ou maior que o padrao: trata como imagem comum.
		// Redimensionar aqui seria adivinhar o recorte que a pessoa queria.
		return nil, false
	}

	var buf bytes.Buffer
	if err := nativewebp.Encode(&buf, img, nil); err != nil {
		return nil, false
	}
	return buf.Bytes(), true
}
