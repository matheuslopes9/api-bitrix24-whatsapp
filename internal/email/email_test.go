package email

import (
	"encoding/base64"
	"strings"
	"testing"
)

func remetenteDeTeste() *Remetente {
	return Novo(Config{
		Host: "proxy", Port: 2526,
		From: "noreply@empresa.com.br", ReplyTo: "suporte@empresa.com.br",
		Destinatarios: []string{"a@empresa.com.br", "b@empresa.com.br"},
	})
}

// O corpo HTML sai numa linha so'. SMTP limita linha a 1000 octetos e o
// servidor recusava com 500 "Line too long" — o alerta nunca saia, com o
// proxy funcionando. Este teste e' o que impede isso de voltar.
func TestNenhumaLinhaPassaDoLimiteSMTP(t *testing.T) {
	corpo := `<div style="font-family:-apple-system,Segoe UI,Roboto,sans-serif;max-width:560px">` +
		strings.Repeat(`<p style="margin:0 0 14px;font-size:14px;line-height:1.6">Token do Bitrix24 vencido em 23/09 as 21:02 e a renovacao nao esta passando.</p>`, 30) +
		`</div>`

	msg := remetenteDeTeste().montar("[UC Talk] Token vencido — teclife.bitrix24.com.br", corpo)

	for i, linha := range strings.Split(msg, "\r\n") {
		// 998 + CRLF = 1000 (RFC 5321 4.5.3.1.6).
		if len(linha) > 998 {
			t.Fatalf("linha %d tem %d caracteres — o servidor recusa acima de 998", i+1, len(linha))
		}
	}
}

// Quebrar a linha nao pode custar o conteudo: o que chega tem que ser
// exatamente o que foi escrito, acento incluso.
func TestCorpoChegaIntacto(t *testing.T) {
	corpo := `<p>Número 5588981859136 não está conectado — açaí, coração, ão</p>`
	msg := remetenteDeTeste().montar("assunto", corpo)

	i := strings.Index(msg, "\r\n\r\n")
	if i < 0 {
		t.Fatal("mensagem sem separacao entre cabecalho e corpo")
	}
	codificado := strings.ReplaceAll(msg[i+4:], "\r\n", "")
	bruto, err := base64.StdEncoding.DecodeString(codificado)
	if err != nil {
		t.Fatalf("corpo nao decodifica: %v", err)
	}
	if string(bruto) != corpo {
		t.Fatalf("corpo alterado no caminho:\nquerido: %q\nveio:    %q", corpo, string(bruto))
	}
}

// Sem declarar base64 no cabecalho, o leitor mostraria o texto codificado.
func TestCabecalhoDeclaraACodificacao(t *testing.T) {
	msg := remetenteDeTeste().montar("assunto", "<p>oi</p>")
	for _, esperado := range []string{
		"Content-Transfer-Encoding: base64",
		"Content-Type: text/html; charset=UTF-8",
		"MIME-Version: 1.0",
		"Reply-To: suporte@empresa.com.br",
		"To: a@empresa.com.br, b@empresa.com.br",
	} {
		if !strings.Contains(msg, esperado) {
			t.Errorf("cabecalho sem %q", esperado)
		}
	}
}

// Assunto com acento nao pode ir cru: alguns leitores trocam o caractere.
func TestAssuntoComAcentoEhCodificado(t *testing.T) {
	msg := remetenteDeTeste().montar("Número desconectado — atenção", "<p>oi</p>")
	linha := ""
	for _, l := range strings.Split(msg, "\r\n") {
		if strings.HasPrefix(l, "Subject:") {
			linha = l
			break
		}
	}
	if linha == "" {
		t.Fatal("mensagem sem Subject")
	}
	if strings.Contains(linha, "Número") {
		t.Errorf("assunto foi cru, sem codificacao MIME: %q", linha)
	}
	if !strings.Contains(linha, "=?") {
		t.Errorf("assunto deveria vir codificado (=?UTF-8?...): %q", linha)
	}
}

func TestConfiguradoExigeOEssencial(t *testing.T) {
	completo := Config{Host: "p", Port: 2526, From: "a@b.c", Destinatarios: []string{"d@e.f"}}
	if !completo.Configurado() {
		t.Error("config completa deveria valer")
	}
	semHost := completo
	semHost.Host = ""
	semDest := completo
	semDest.Destinatarios = nil
	semPorta := completo
	semPorta.Port = 0
	for nome, c := range map[string]Config{"sem host": semHost, "sem destinatario": semDest, "sem porta": semPorta} {
		if c.Configurado() {
			t.Errorf("%s NAO deveria contar como configurado — senao o alerta falha em silencio", nome)
		}
	}
}
