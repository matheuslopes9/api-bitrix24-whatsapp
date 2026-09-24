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

// parteDecodificada acha a parte do multipart pelo Content-Type e devolve o
// conteudo ja' fora do base64.
func parteDecodificada(t *testing.T, msg, tipo string) string {
	t.Helper()
	i := strings.Index(msg, "Content-Type: "+tipo)
	if i < 0 {
		t.Fatalf("mensagem sem parte %s", tipo)
	}
	resto := msg[i:]
	j := strings.Index(resto, "\r\n\r\n")
	if j < 0 {
		t.Fatalf("parte %s sem corpo", tipo)
	}
	corpo := resto[j+4:]
	// Vai ate' a proxima fronteira.
	if k := strings.Index(corpo, "\r\n--"); k >= 0 {
		corpo = corpo[:k]
	}
	bruto, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(corpo, "\r\n", ""))
	if err != nil {
		t.Fatalf("parte %s nao decodifica: %v", tipo, err)
	}
	return string(bruto)
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

	if veio := parteDecodificada(t, msg, "text/html"); veio != corpo {
		t.Fatalf("corpo alterado no caminho:\nquerido: %q\nveio:    %q", corpo, veio)
	}
}

// Estrutura igual a do send_email.py da empresa: multipart/alternative, com
// texto puro antes do HTML e remetente "UC Technology".
func TestEstruturaMultipartComoOServicoDaEmpresa(t *testing.T) {
	msg := remetenteDeTeste().montar("assunto", "<div><p>Token vencido</p></div>")

	if !strings.Contains(msg, "From: UC Technology <") {
		t.Error("remetente deveria ser 'UC Technology', igual ao send_email.py")
	}
	if !strings.Contains(msg, "Content-Type: multipart/alternative; boundary=") {
		t.Fatal("faltou o multipart/alternative")
	}
	if !strings.Contains(msg, "Content-Type: text/plain; charset=UTF-8") {
		t.Error("faltou a parte em texto puro")
	}
	if !strings.Contains(msg, "Content-Type: text/html; charset=UTF-8") {
		t.Error("faltou a parte em HTML")
	}
	// RFC 2046: o leitor escolhe a ULTIMA parte que sabe exibir. HTML por
	// ultimo, senao todo mundo veria o texto puro.
	if strings.Index(msg, "text/plain") > strings.Index(msg, "text/html") {
		t.Error("texto puro tem que vir ANTES do HTML")
	}
	if !strings.Contains(msg, "MIME-Version: 1.0") {
		t.Error("faltou MIME-Version")
	}
	if !strings.Contains(msg, "Reply-To: suporte@empresa.com.br") {
		t.Error("faltou Reply-To")
	}
	if !strings.Contains(msg, "To: a@empresa.com.br, b@empresa.com.br") {
		t.Error("destinatarios deveriam ir juntos no To")
	}
}

// Fronteira que aparecesse no conteudo cortaria o e-mail ao meio.
func TestFronteiraFechaCorretamente(t *testing.T) {
	msg := remetenteDeTeste().montar("assunto", "<p>corpo qualquer</p>")
	i := strings.Index(msg, `boundary="`)
	if i < 0 {
		t.Fatal("sem boundary")
	}
	resto := msg[i+len(`boundary="`):]
	fronteira := resto[:strings.Index(resto, `"`)]

	// Tres vezes: abre o texto, abre o html, fecha.
	if n := strings.Count(msg, "--"+fronteira); n != 3 {
		t.Fatalf("fronteira aparece %d vezes, esperava 3 (duas aberturas + fechamento)", n)
	}
	if !strings.HasSuffix(strings.TrimRight(msg, "\r\n"), "--"+fronteira+"--") {
		t.Error("mensagem nao termina com o fechamento da fronteira")
	}
}

// Duas mensagens nao podem usar a mesma fronteira por acaso previsivel.
func TestFronteiraVariaEntreMensagens(t *testing.T) {
	r := remetenteDeTeste()
	if r.montar("a", "<p>x</p>") == r.montar("a", "<p>x</p>") {
		t.Error("a fronteira deveria variar a cada mensagem")
	}
}

// Quem le com HTML bloqueado, ou na notificacao do celular, tem que receber
// algo legivel — alerta ilegivel as 3h da manha e' alerta perdido.
func TestVersaoEmTextoEhLegivel(t *testing.T) {
	corpo := `<div><p>Token do Bitrix24 vencido</p>` +
		`<p style="color:red">Nenhuma mensagem chega no Contact Center.</p>` +
		`<ol><li>Abrir Saude do cliente</li><li>Cadastrar credenciais</li></ol></div>`

	txt := textoDoHTML(corpo)

	for _, esperado := range []string{
		"Token do Bitrix24 vencido",
		"Nenhuma mensagem chega no Contact Center.",
		"Abrir Saude do cliente",
		"Cadastrar credenciais",
	} {
		if !strings.Contains(txt, esperado) {
			t.Errorf("texto puro perdeu %q:\n%s", esperado, txt)
		}
	}
	if strings.Contains(txt, "<") || strings.Contains(txt, "style=") {
		t.Errorf("sobrou marcacao no texto puro:\n%s", txt)
	}
	if strings.Contains(txt, "\n\n\n") {
		t.Errorf("linhas vazias em excesso:\n%q", txt)
	}
}

// Entidade HTML tem que virar o caractere de verdade no texto puro.
func TestTextoPuroDesfazEntidades(t *testing.T) {
	if txt := textoDoHTML("<p>UC TALK &middot; ALERTA &amp; aviso</p>"); !strings.Contains(txt, "· ALERTA & aviso") {
		t.Errorf("entidades nao foram desfeitas: %q", txt)
	}
}

// Sem declarar base64 nas partes, o leitor mostraria o texto codificado.
func TestPartesDeclaramBase64(t *testing.T) {
	msg := remetenteDeTeste().montar("assunto", "<p>oi</p>")
	if n := strings.Count(msg, "Content-Transfer-Encoding: base64"); n != 2 {
		t.Errorf("as duas partes deveriam declarar base64, encontrei %d", n)
	}
}

// Assunto com acento nao pode ir cru: alguns leitores trocam o caractere.
func TestAssuntoComAcentoEhCodificado(t *testing.T) {
	msg := remetenteDeTeste().montar("Número desconectado — atenção", "<p>oi</p>")
	var linha string
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
	for nome, c := range map[string]Config{
		"sem host":         semHost,
		"sem destinatario": semDest,
		"sem porta":        semPorta,
	} {
		if c.Configurado() {
			t.Errorf("%s NAO deveria contar como configurado — senao o alerta falha em silencio", nome)
		}
	}
}
