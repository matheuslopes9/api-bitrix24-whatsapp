package whatsapp

import (
	"os"
	"testing"
)

// Os outros testes montam o Ogg na mao. Este le o arquivo que foi REALMENTE
// enviado pelo Contact Center em 01/10 e entregue ao WhatsApp — o mesmo
// caminho que o operador usa. Teste sintetico prova a logica; este prova que
// a logica casa com o que o Bitrix entrega de verdade.
//
// Pula quando o arquivo nao esta' na maquina: nao vale travar o build de
// quem nao tem a pasta de testes.
func TestDuracaoDoArquivoRealEnviadoPeloBitrix(t *testing.T) {
	const caminho = `D:\teste arquivos\File.ogg`
	dados, err := os.ReadFile(caminho)
	if err != nil {
		t.Skipf("arquivo de teste indisponivel nesta maquina: %v", err)
	}
	if !EhMensagemDeVoz("audio/ogg") {
		t.Fatal("audio/ogg deixou de ser mensagem de voz")
	}
	d, ok := DuracaoOggOpus(dados)
	if !ok {
		t.Fatalf("nao li a duracao do arquivo real (%d bytes) — a bolha apareceria 0:00", len(dados))
	}
	if d == 0 || d > 600 {
		t.Errorf("duracao implausivel: %ds", d)
	}
	t.Logf("arquivo real: %d bytes, duracao lida = %ds", len(dados), d)
}
