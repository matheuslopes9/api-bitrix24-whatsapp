package whatsapp

import (
	"encoding/binary"
	"testing"
)

// Audio do operador chegava como arquivo anexado em vez de mensagem de voz.
// Sao dois detalhes e os dois precisam estar certos: o PTT, que diz "isto e'
// voz", e a DURACAO — com Seconds=0 a bolha aparece "0:00", tecnicamente uma
// mensagem de voz e visivelmente quebrada.

func TestSoOggOpusViraMensagemDeVoz(t *testing.T) {
	voz := []string{"audio/ogg", "audio/ogg; codecs=opus", "AUDIO/OGG", "audio/opus"}
	for _, m := range voz {
		if !EhMensagemDeVoz(m) {
			t.Errorf("%q deveria virar mensagem de voz", m)
		}
	}
	// Marcar estes como voz gera bolha que parte dos aparelhos nao toca —
	// pior que o anexo, porque falha depois de parecer que ia funcionar.
	anexo := []string{"audio/mpeg", "audio/mp4", "audio/wav", "audio/x-m4a", "video/mp4", "", "application/pdf"}
	for _, m := range anexo {
		if EhMensagemDeVoz(m) {
			t.Errorf("%q NAO pode ir como voz — o WhatsApp so' reproduz Ogg/Opus na bolha", m)
		}
	}
}

// paginaOgg monta uma pagina Ogg minima, para o teste nao depender de arquivo.
func paginaOgg(granule uint64, corpo []byte) []byte {
	p := make([]byte, 27)
	copy(p, "OggS")
	binary.LittleEndian.PutUint64(p[6:14], granule)
	// Segmenta o corpo em lacunas de 255, como manda o formato.
	var tabela []byte
	resto := len(corpo)
	for resto >= 255 {
		tabela = append(tabela, 255)
		resto -= 255
	}
	tabela = append(tabela, byte(resto))
	p[26] = byte(len(tabela))
	return append(append(p, tabela...), corpo...)
}

func opusHead(preSkip uint16) []byte {
	c := make([]byte, 19)
	copy(c, "OpusHead")
	c[8] = 1 // versao
	c[9] = 1 // canais
	binary.LittleEndian.PutUint16(c[10:12], preSkip)
	return c
}

func TestDuracaoSaiDoGranuleDaUltimaPagina(t *testing.T) {
	// 3 segundos a 48 kHz, mais o pre-skip que nao toca.
	const preSkip = 312
	arquivo := append(paginaOgg(0, opusHead(preSkip)),
		paginaOgg(3*taxaOpus+preSkip, []byte("audio"))...)

	d, ok := DuracaoOggOpus(arquivo)
	if !ok {
		t.Fatal("nao leu a duracao — a bolha apareceria 0:00")
	}
	if d != 3 {
		t.Errorf("duracao = %d, esperado 3 (o pre-skip precisa ser descontado)", d)
	}
}

// Audio de menos de 1 segundo existe — um "oi", um riso. Mostrar 0:00 faria a
// mensagem parecer vazia.
func TestAudioCurtoNaoFicaZero(t *testing.T) {
	arquivo := append(paginaOgg(0, opusHead(0)),
		paginaOgg(taxaOpus/2, []byte("x"))...) // meio segundo
	if d, ok := DuracaoOggOpus(arquivo); !ok || d != 1 {
		t.Errorf("duracao = %d (ok=%v), esperado 1", d, ok)
	}
}

// Qualquer coisa que nao seja Ogg valido devolve "nao sei", e quem chama
// manda 0 — como ja' fazia. Duracao inventada seria pior que ausente.
func TestArquivoInvalidoNaoInventaDuracao(t *testing.T) {
	casos := map[string][]byte{
		"vazio":           nil,
		"curto demais":    []byte("Ogg"),
		"nao e' ogg":      []byte("ID3\x04isto e' um mp3 qualquer...."),
		"tabela truncada": append(append([]byte("OggS"), make([]byte, 22)...), 200),
		"so' cabecalho":   paginaOgg(0, opusHead(312)),
	}
	for nome, dados := range casos {
		if d, ok := DuracaoOggOpus(dados); ok {
			t.Errorf("%s: inventou duracao %d", nome, d)
		}
	}
}

// Granule -1 marca pagina sem amostras completas e nao serve de total. Usar
// esse valor daria uma duracao astronomica.
func TestGranuleInvalidoEhIgnorado(t *testing.T) {
	arquivo := append(paginaOgg(0, opusHead(0)), paginaOgg(2*taxaOpus, []byte("a"))...)
	arquivo = append(arquivo, paginaOgg(^uint64(0), []byte("b"))...)
	if d, ok := DuracaoOggOpus(arquivo); !ok || d != 2 {
		t.Errorf("duracao = %d (ok=%v), esperado 2 — a pagina -1 deveria ser ignorada", d, ok)
	}
}
