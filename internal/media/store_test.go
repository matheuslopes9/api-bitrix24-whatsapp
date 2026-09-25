package media

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSalvarEReler(t *testing.T) {
	s, err := NewStore(t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	quando := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	ref, err := s.Salvar("Relatorio & Contas.pdf", "application/pdf", []byte("%PDF"), quando)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ref, PrefixoLocal+"2026/09/25/") {
		t.Fatalf("ref inesperada: %s", ref)
	}
	// O nome original — com "&" — tem que sobreviver: o Bitrix trocava por "v".
	if got := NomeDaRef(ref); got != "Relatorio & Contas.pdf" {
		t.Fatalf("nome perdido: %q", got)
	}
	p, err := s.Caminho(ref)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "%PDF" {
		t.Fatalf("conteudo errado: %q", b)
	}
}

// A referencia decide qual arquivo o servidor le. Qualquer uma que escape
// da raiz seria leitura arbitraria do disco do container.
func TestCaminhoRecusaSairDaRaiz(t *testing.T) {
	s, _ := NewStore(t.TempDir(), 0)
	for _, ref := range []string{
		"local:../../etc/passwd",
		"local:2026/09/25/../../../../etc/passwd",
		"local:/etc/passwd",
		"local:",
		"grande:x.pdf",
		"https://exemplo.com/x.png",
		"local:2026/09/25/id/./x.pdf",
	} {
		if _, err := s.Caminho(ref); err == nil {
			t.Errorf("aceitou %q", ref)
		}
	}
}

func TestAcimaDoLimiteNaoGrava(t *testing.T) {
	raiz := t.TempDir()
	s, _ := NewStore(raiz, 3)
	ref, err := s.Salvar("backup.bak", "", []byte("1234"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if ref != PrefixoGrande+"backup.bak" {
		t.Fatalf("ref inesperada: %s", ref)
	}
	if entradas, _ := os.ReadDir(raiz); len(entradas) != 0 {
		t.Fatal("gravou arquivo acima do limite")
	}
}

// Era o bug visto no teste: MP3 chegava ao Bitrix como "audio.ogg".
func TestTrocarExtensaoDoAudioGenerico(t *testing.T) {
	casos := map[string]string{
		"audio/mpeg":             "audio.mp3",
		"audio/ogg; codecs=opus": "audio.ogg",
		"audio/mp4":              "audio.m4a",
		"":                       "audio.ogg", // tipo desconhecido: mantem
	}
	for mimeType, esperado := range casos {
		if got := TrocarExtensao("audio.ogg", mimeType); got != esperado {
			t.Errorf("TrocarExtensao(audio.ogg, %q) = %q, esperado %q", mimeType, got, esperado)
		}
	}
}

func TestNomeSeguro(t *testing.T) {
	casos := map[[2]string]string{
		{"../../segredo.txt", ""}:                   "segredo.txt",
		{"a\\b\\c.pdf", ""}:                         "c.pdf",
		{"", "image/jpeg"}:                          "arquivo.jpg",
		{"sem-extensao", "application/pdf"}:         "sem-extensao.pdf",
		{"linha\nquebrada.txt", ""}:                 "linhaquebrada.txt",
		{"PSE-SystemLog-83.21.0.117-beta1.tar", ""}: "PSE-SystemLog-83.21.0.117-beta1.tar",
	}
	for in, esperado := range casos {
		if got := NomeSeguro(in[0], in[1]); got != esperado {
			t.Errorf("NomeSeguro(%q, %q) = %q, esperado %q", in[0], in[1], got, esperado)
		}
	}
	if n := NomeSeguro(strings.Repeat("a", 300)+".pdf", ""); len(n) > 180 || !strings.HasSuffix(n, ".pdf") {
		t.Errorf("nome longo mal cortado: %d %q", len(n), n[len(n)-8:])
	}
}

func TestApagarAntigosRespeitaRetencao(t *testing.T) {
	raiz := t.TempDir()
	s, _ := NewStore(raiz, 0)
	agora := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	velho, _ := s.Salvar("v.txt", "", []byte("x"), agora.AddDate(0, 0, -100))
	novo, _ := s.Salvar("n.txt", "", []byte("x"), agora.AddDate(0, 0, -10))
	if n, err := s.ApagarAntigos(90, agora); err != nil || n != 1 {
		t.Fatalf("apagou %d dias (err %v), esperado 1", n, err)
	}
	pv, _ := s.Caminho(velho)
	pn, _ := s.Caminho(novo)
	if _, err := os.Stat(pv); !os.IsNotExist(err) {
		t.Error("arquivo velho continua")
	}
	if _, err := os.Stat(pn); err != nil {
		t.Error("arquivo novo sumiu")
	}
	if _, err := os.Stat(filepath.Dir(pn)); err != nil {
		t.Error("diretorio do dia novo sumiu")
	}
}
