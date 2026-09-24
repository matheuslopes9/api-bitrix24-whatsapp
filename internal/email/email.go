// Package email envia e-mail pelo proxy OAuth2 da UC Technology.
//
// ARQUITETURA (nao inventada aqui — e' a que a empresa ja' usa em producao):
//
//	este app --SMTP simples--> oauth2_smtp_proxy --XOAUTH2--> Microsoft 365
//
// O proxy (tools/oauth2-email-service) resolve o OAuth2 com o Azure AD e
// escuta SMTP sem autenticacao em 127.0.0.1:2525. Quem envia so' precisa
// falar SMTP puro — nenhuma credencial da Microsoft entra neste processo, o
// que e' justamente a razao do proxy existir.
//
// Em container, o proxy precisa ser alcancavel pela rede: aponte SMTP_HOST
// pro servico dele (ex.: "uctalk-email") em vez de 127.0.0.1.
package email

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Config vem do ambiente. Nomes iguais aos do .env do proxy, de proposito:
// quem ja' opera o servico de e-mail reconhece as chaves.
type Config struct {
	Host          string   // SMTP_HOST — onde o proxy escuta
	Port          int      // SMTP_PORT
	From          string   // EMAIL_SENDER — aparece no From:
	ReplyTo       string   // EMAIL_REPLY_TO
	Destinatarios []string // ALERT_RECIPIENTS, separados por virgula
}

// Configurado diz se da' pra enviar. Sem isso o chamador nao tem como
// distinguir "nao enviei porque falhou" de "nao enviei porque nao ha' para
// onde" — e as duas coisas pedem acoes diferentes.
func (c Config) Configurado() bool {
	return c.Host != "" && c.Port > 0 && c.From != "" && len(c.Destinatarios) > 0
}

type Remetente struct {
	cfg Config
}

func Novo(cfg Config) *Remetente { return &Remetente{cfg: cfg} }

func (r *Remetente) Configurado() bool       { return r.cfg.Configurado() }
func (r *Remetente) Destinatarios() []string { return r.cfg.Destinatarios }

// Enviar manda um e-mail HTML. Respeita o prazo do contexto: envio de alerta
// nao pode segurar o job que o disparou.
func (r *Remetente) Enviar(ctx context.Context, assunto, corpoHTML string) error {
	if !r.cfg.Configurado() {
		return fmt.Errorf("envio de e-mail nao configurado (defina SMTP_HOST, EMAIL_SENDER e ALERT_RECIPIENTS)")
	}

	msg := r.montar(assunto, corpoHTML)
	endereco := net.JoinHostPort(r.cfg.Host, fmt.Sprint(r.cfg.Port))

	prazo := 20 * time.Second
	if lim, ok := ctx.Deadline(); ok {
		if d := time.Until(lim); d < prazo {
			prazo = d
		}
	}
	if prazo <= 0 {
		return fmt.Errorf("prazo esgotado antes de enviar")
	}

	conn, err := net.DialTimeout("tcp", endereco, prazo)
	if err != nil {
		return fmt.Errorf("proxy de e-mail inalcancavel em %s: %w", endereco, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(prazo))

	cli, err := smtp.NewClient(conn, r.cfg.Host)
	if err != nil {
		return fmt.Errorf("handshake SMTP: %w", err)
	}
	defer func() { _ = cli.Quit() }()

	// Sem Auth de proposito: o proxy aceita sem autenticacao e ele' que faz
	// o XOAUTH2 com a Microsoft.
	if err := cli.Mail(r.cfg.From); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	for _, para := range r.cfg.Destinatarios {
		if err := cli.Rcpt(para); err != nil {
			return fmt.Errorf("RCPT TO %s: %w", para, err)
		}
	}
	w, err := cli.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return fmt.Errorf("escrevendo corpo: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("fechando corpo: %w", err)
	}
	return nil
}

func (r *Remetente) montar(assunto, corpoHTML string) string {
	var b strings.Builder

	// "UC Technology" e nao "UC Talk": e' o remetente que o servico de e-mail
	// da empresa ja' usa (send_email.py). Quem recebe reconhece a origem, e
	// filtro/regra de caixa que ja' exista continua valendo.
	b.WriteString("From: UC Technology <" + r.cfg.From + ">\r\n")
	b.WriteString("To: " + strings.Join(r.cfg.Destinatarios, ", ") + "\r\n")
	if r.cfg.ReplyTo != "" {
		b.WriteString("Reply-To: " + r.cfg.ReplyTo + "\r\n")
	}
	// Assunto codificado: ele carrega acento e o dominio do cliente. Sem isto
	// chega com caractere trocado em alguns leitores.
	b.WriteString("Subject: " + mime.QEncoding.Encode("UTF-8", assunto) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")

	// multipart/alternative com texto puro ANTES do HTML, mesma estrutura do
	// send_email.py. Duas razoes praticas:
	//
	//   - leitor que bloqueia HTML (ou notificacao de celular, ou relogio)
	//     mostra o texto em vez de nada. Alerta que chega ilegivel as 3h da
	//     manha e' alerta perdido;
	//   - mensagem so'-HTML pontua pior em filtro de spam, e alerta na caixa
	//     de lixo e' o mesmo que alerta nao enviado.
	//
	// A ordem importa: pelo RFC 2046 o leitor escolhe a ULTIMA parte que sabe
	// exibir, entao o HTML vem por ultimo pra ser o preferido.
	fronteira := fronteiraMIME()
	b.WriteString("Content-Type: multipart/alternative; boundary=\"" + fronteira + "\"\r\n")
	b.WriteString("\r\n")

	b.WriteString("--" + fronteira + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	b.WriteString(base64Quebrado(textoDoHTML(corpoHTML)))

	b.WriteString("--" + fronteira + "\r\n")
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	b.WriteString(base64Quebrado(corpoHTML))

	b.WriteString("--" + fronteira + "--\r\n")
	return b.String()
}

// fronteiraMIME devolve um separador que nao pode aparecer no conteudo.
// Usa tempo + aleatorio: fronteira repetida entre duas mensagens nao quebra
// nada, mas fronteira que COLIDE com o corpo corta o e-mail ao meio.
func fronteiraMIME() string {
	var n [12]byte
	if _, err := rand.Read(n[:]); err != nil {
		// Sem aleatoriedade ainda da' pra gerar algo unico o bastante: o
		// conteudo e' HTML de alerta, nao texto arbitrario do usuario.
		return fmt.Sprintf("uctalk-%d", time.Now().UnixNano())
	}
	return "uctalk-" + hex.EncodeToString(n[:])
}

// textoDoHTML monta a versao em texto puro a partir do HTML.
//
// Nao e' um conversor de HTML de uso geral — e' suficiente para os alertas,
// que sao gerados por corpoAlerta e tem estrutura conhecida: blocos, titulos
// e uma lista de passos. Fecha bloco vira quebra de linha, item de lista
// ganha marcador, e o resto e' texto.
func textoDoHTML(h string) string {
	// Quebra onde o HTML quebra visualmente, antes de tirar as tags.
	subs := strings.NewReplacer(
		"</div>", "\n", "</p>", "\n", "</h1>", "\n", "</h2>", "\n",
		"</h3>", "\n", "</ol>", "\n", "</ul>", "\n", "<br>", "\n",
		"<br/>", "\n", "<br />", "\n", "<li>", "  - ",
	)
	t := subs.Replace(h)

	var b strings.Builder
	dentroDeTag := false
	for _, r := range t {
		switch {
		case r == '<':
			dentroDeTag = true
		case r == '>':
			dentroDeTag = false
		case !dentroDeTag:
			b.WriteRune(r)
		}
	}
	t = html.UnescapeString(b.String())

	// Colapsa as linhas vazias que sobram das tags aninhadas.
	var linhas []string
	for _, l := range strings.Split(t, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			linhas = append(linhas, l)
		}
	}
	return strings.Join(linhas, "\n") + "\n"
}

// base64Quebrado codifica o corpo e quebra em linhas de 76 caracteres.
//
// POR QUE: o corpo HTML sai numa linha so', e SMTP limita linha a 1000
// octetos (RFC 5321 4.5.3.1.6). O servidor recusava com
//
//	500 "Line too long (see RFC5321 4.5.3.1.6)"
//
// e o e-mail nunca saia — com o proxy funcionando perfeitamente.
//
// base64 resolve as duas coisas de uma vez: garante o limite de linha e
// entrega acento intacto, sem depender do servidor tratar UTF-8 cru. 76 e' a
// largura classica de MIME (RFC 2045), com folga larga sobre o limite.
func base64Quebrado(s string) string {
	cod := base64.StdEncoding.EncodeToString([]byte(s))
	const largura = 76
	var b strings.Builder
	for i := 0; i < len(cod); i += largura {
		fim := i + largura
		if fim > len(cod) {
			fim = len(cod)
		}
		b.WriteString(cod[i:fim])
		b.WriteString("\r\n")
	}
	return b.String()
}
