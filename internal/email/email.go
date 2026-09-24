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
	"fmt"
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
	b.WriteString("From: UC Talk <" + r.cfg.From + ">\r\n")
	b.WriteString("To: " + strings.Join(r.cfg.Destinatarios, ", ") + "\r\n")
	if r.cfg.ReplyTo != "" {
		b.WriteString("Reply-To: " + r.cfg.ReplyTo + "\r\n")
	}
	b.WriteString("Subject: " + assunto + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("\r\n")
	b.WriteString(corpoHTML)
	return b.String()
}
