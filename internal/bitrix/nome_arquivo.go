package bitrix

import (
	"path/filepath"
	"strings"
)

// maxNomeArquivoBitrix: o Open Lines guarda so' os ULTIMOS 50 caracteres do
// nome do arquivo anexado. Visto no homolog em 25/09:
//
//	"PSE-SystemLog-83.21.0.117-beta1-download-20260331195609-QbCLXR9X2G0A9WkY.tar"
//	chegou como "beta1-download-20260331195609-QbCLXR9X2G0A9WkY.tar"
//
// — o comeco, que e' o que identifica o arquivo, sumia.
const maxNomeArquivoBitrix = 50

// NomeParaBitrix prepara o nome do arquivo pro anexo do Open Lines.
//
//   - Corta NO FIM do nome (antes da extensao), com "…", pra caber nos 50
//     caracteres sem o Bitrix cortar o comeco.
//   - Troca "&" por "e": o Bitrix trocava o "&" por uma letra qualquer
//     ("Benchmark & Compare" chegou como "Benchmark v Compare" e, noutro
//     envio, "Benchmark b Compare").
//
// O arquivo em si nao muda — so' o nome que aparece no chat.
func NomeParaBitrix(nome string) string {
	nome = strings.TrimSpace(strings.ReplaceAll(nome, "&", "e"))
	r := []rune(nome)
	if len(r) <= maxNomeArquivoBitrix {
		return nome
	}
	ext := []rune(filepath.Ext(nome))
	if len(ext) > 10 {
		ext = nil // "extensao" absurda: trata tudo como nome
	}
	base := r[:len(r)-len(ext)]
	cabe := maxNomeArquivoBitrix - len(ext) - 1 // 1 = "…"
	return string(base[:cabe]) + "…" + string(ext)
}
