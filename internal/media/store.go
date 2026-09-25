// Package media guarda os arquivos das conversas para que a aba do CRM
// consiga exibi-los.
//
// O PORQUE: o arquivo recebido ia direto pro Drive do Bitrix e era anexado
// ao chat do Open Lines. Nada ficava do nosso lado — messages.media_url
// nunca era preenchido —, entao a aba do CRM (negocio, contato, lead) so'
// conseguia mostrar o rotulo "Imagem" ou "Documento", sem a imagem, sem
// player e sem download.
//
// Layout em disco: <raiz>/AAAA/MM/DD/<id>/<nome original>. A data na frente
// deixa a retencao barata (apaga o dia inteiro), e o id isola arquivos de
// mesmo nome. A referencia gravada no banco e' o caminho relativo com o
// prefixo "local:"; o nome original sai do proprio caminho, sem precisar de
// coluna nova.
package media

import (
	"fmt"
	"mime"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

const (
	// PrefixoLocal marca media_url de arquivo guardado por este pacote.
	PrefixoLocal = "local:"
	// PrefixoGrande marca arquivo que passou do limite e nao foi guardado.
	// O nome vem junto pra tela ainda mostrar o que era.
	PrefixoGrande = "grande:"
)

// Store grava e le arquivos numa raiz de disco.
type Store struct {
	raiz     string
	maxBytes int64
}

// NewStore cria a raiz se preciso. maxBytes <= 0 desliga o limite.
func NewStore(raiz string, maxBytes int64) (*Store, error) {
	if raiz == "" {
		return nil, fmt.Errorf("media: raiz vazia")
	}
	if err := os.MkdirAll(raiz, 0o750); err != nil {
		return nil, fmt.Errorf("media: criar raiz: %w", err)
	}
	return &Store{raiz: raiz, maxBytes: maxBytes}, nil
}

// Salvar grava o arquivo e devolve a referencia pra messages.media_url.
//
// Nunca falha a ponto de derrubar a entrega da mensagem: quem chama so'
// perde a exibicao no CRM. Arquivo acima do limite volta como
// PrefixoGrande + nome, sem gravar nada.
func (s *Store) Salvar(nome, mimeType string, dados []byte, quando time.Time) (string, error) {
	if s == nil || len(dados) == 0 {
		return "", nil
	}
	nome = NomeSeguro(nome, mimeType)
	if s.maxBytes > 0 && int64(len(dados)) > s.maxBytes {
		return PrefixoGrande + nome, nil
	}
	rel := path.Join(quando.Format("2006/01/02"), uuid.NewString(), nome)
	abs := filepath.Join(s.raiz, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, dados, 0o640); err != nil {
		return "", err
	}
	return PrefixoLocal + rel, nil
}

// Caminho resolve uma referencia "local:" pro arquivo em disco.
//
// Recusa qualquer coisa que saia da raiz: a referencia vem do banco, mas e'
// ela que decide qual arquivo o servidor le — um ".." aqui viraria leitura
// arbitraria do disco.
func (s *Store) Caminho(ref string) (string, error) {
	rel, ok := strings.CutPrefix(ref, PrefixoLocal)
	if !ok || rel == "" {
		return "", fmt.Errorf("media: referencia nao local")
	}
	limpo := path.Clean("/" + rel)[1:]
	if limpo == "" || limpo != rel {
		return "", fmt.Errorf("media: referencia invalida")
	}
	abs := filepath.Join(s.raiz, filepath.FromSlash(limpo))
	if !strings.HasPrefix(abs, filepath.Clean(s.raiz)+string(filepath.Separator)) {
		return "", fmt.Errorf("media: fora da raiz")
	}
	return abs, nil
}

// NomeDaRef devolve o nome original guardado na referencia.
func NomeDaRef(ref string) string {
	if n, ok := strings.CutPrefix(ref, PrefixoGrande); ok {
		return n
	}
	if rel, ok := strings.CutPrefix(ref, PrefixoLocal); ok {
		return path.Base(rel)
	}
	return ""
}

// ApagarAntigos remove os dias mais velhos que a retencao. Devolve quantos
// dias (diretorios) foram apagados.
func (s *Store) ApagarAntigos(retencaoDias int, agora time.Time) (int, error) {
	if s == nil || retencaoDias <= 0 {
		return 0, nil
	}
	limite := agora.AddDate(0, 0, -retencaoDias)
	dias, err := filepath.Glob(filepath.Join(s.raiz, "[0-9][0-9][0-9][0-9]", "[0-9][0-9]", "[0-9][0-9]"))
	if err != nil {
		return 0, err
	}
	apagados := 0
	for _, d := range dias {
		rel, _ := filepath.Rel(s.raiz, d)
		dia, err := time.ParseInLocation("2006/01/02", filepath.ToSlash(rel), agora.Location())
		if err != nil || !dia.Before(limite) {
			continue
		}
		if err := os.RemoveAll(d); err == nil {
			apagados++
		}
	}
	return apagados, nil
}

// NomeSeguro limpa o nome pra virar componente de caminho e garante uma
// extensao coerente com o tipo real.
//
// A extensao importa: o audio recebido era sempre gravado como "audio.ogg",
// e um MP3 chegava ao Bitrix como ".ogg" — o player nao toca.
func NomeSeguro(nome, mimeType string) string {
	nome = strings.TrimSpace(filepath.Base(strings.ReplaceAll(nome, "\\", "/")))
	nome = strings.Map(func(r rune) rune {
		switch {
		case r == '/' || r == '\\' || r == 0:
			return '_'
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, nome)
	if nome == "" || nome == "." || nome == ".." {
		nome = "arquivo"
	}
	if len(nome) > 180 {
		ext := filepath.Ext(nome)
		if len(ext) > 16 {
			ext = ""
		}
		nome = nome[:180-len(ext)] + ext
	}
	if filepath.Ext(nome) == "" {
		nome += ExtensaoDoMime(mimeType)
	}
	return nome
}

// ExtensaoDoMime devolve a extensao usual do tipo, ou "" se nao souber.
//
// A tabela propria vem antes da do sistema porque mime.ExtensionsByType
// depende do SO do container e costuma devolver a primeira de uma lista em
// ordem alfabetica (".jfif" pra JPEG, ".m2a" pra MPEG).
func ExtensaoDoMime(mimeType string) string {
	base, _, _ := mime.ParseMediaType(mimeType)
	if base == "" {
		base = strings.TrimSpace(strings.SplitN(mimeType, ";", 2)[0])
	}
	conhecidos := map[string]string{
		"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp", "image/gif": ".gif",
		"audio/ogg": ".ogg", "audio/mpeg": ".mp3", "audio/mp4": ".m4a", "audio/aac": ".aac",
		"audio/amr": ".amr", "audio/wav": ".wav", "audio/x-wav": ".wav",
		"video/mp4": ".mp4", "video/3gpp": ".3gp", "video/quicktime": ".mov", "video/webm": ".webm",
		"application/pdf": ".pdf", "text/plain": ".txt", "text/vcard": ".vcf",
	}
	if e, ok := conhecidos[strings.ToLower(base)]; ok {
		return e
	}
	if exts, _ := mime.ExtensionsByType(base); len(exts) > 0 {
		return exts[0]
	}
	return ""
}

// TrocarExtensao ajusta a extensao de um nome GENERICO ("audio.ogg",
// "video.mp4") pro tipo real. Nome escolhido pelo usuario nao e' tocado.
func TrocarExtensao(nome, mimeType string) string {
	ext := ExtensaoDoMime(mimeType)
	if ext == "" {
		return nome
	}
	return strings.TrimSuffix(nome, filepath.Ext(nome)) + ext
}
