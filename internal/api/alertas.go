// alertas.go — avisa o time por e-mail quando algo para de atender.
//
// POR QUE EXISTE: em 23/09 o token do Bitrix de um cliente venceu as 21:02 e
// ninguem soube ate' o dia seguinte. Nesse intervalo 43 mensagens de clientes
// reais morreram na fila, e a tela de suporte dizia "ok". O problema nao foi
// falta de dado — foi que o dado so' aparecia pra quem fosse olhar.
package api

import (
	"context"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/email"
	"go.uber.org/zap"
)

const (
	alertaTokenVencido = "token_vencido"
	alertaSessaoCaiu   = "sessao_desconectada"

	intervaloVerificacao = 5 * time.Minute
)

// remetenteDaConfig monta o enviador a partir da configuracao do BANCO.
//
// A config e' lida a cada ciclo, de proposito: trocar destinatario ou
// desligar um alerta nao pode exigir reiniciar o app — reiniciar derruba o
// atendimento de todos os clientes por causa de uma mudanca de e-mail.
func remetenteDaConfig(c *db.ConfigAlertas) *email.Remetente {
	return email.Novo(email.Config{
		Host:          c.SMTPHost,
		Port:          c.SMTPPort,
		From:          c.EmailSender,
		ReplyTo:       c.EmailReplyTo,
		Destinatarios: c.ListaDestinatarios(),
	})
}

// semearConfigDoAmbiente copia as envs pra dentro do banco na PRIMEIRA vez.
//
// Instalacao nova nasce funcionando com o que ja' esta' no ambiente, e dali
// em diante a fonte da verdade e' a tela. Nunca sobrescreve o que foi salvo
// pelo painel: se o campo ja' tem valor, o ambiente nao manda mais.
func (h *handlers) semearConfigDoAmbiente(ctx context.Context) {
	c, err := h.repo.GetConfigAlertas(ctx)
	if err != nil || c == nil {
		return
	}
	if c.SMTPHost != "" || c.EmailSender != "" || c.Destinatarios != "" {
		return // ja' configurado pelo painel
	}
	if h.cfg.Email.SMTPHost == "" && h.cfg.Email.Sender == "" && len(h.cfg.Email.Destinatarios) == 0 {
		return // nao ha nada no ambiente pra semear
	}
	c.SMTPHost = h.cfg.Email.SMTPHost
	if h.cfg.Email.SMTPPort > 0 {
		c.SMTPPort = h.cfg.Email.SMTPPort
	}
	c.EmailSender = h.cfg.Email.Sender
	c.EmailReplyTo = h.cfg.Email.ReplyTo
	c.Destinatarios = strings.Join(h.cfg.Email.Destinatarios, ", ")
	if err := h.repo.SalvarConfigAlertas(ctx, c, "ambiente (primeira carga)"); err != nil {
		h.log.Warn("alertas: falha ao semear config do ambiente", zap.Error(err))
		return
	}
	h.log.Info("alertas: config inicial copiada do ambiente para o banco",
		zap.String("smtp_host", c.SMTPHost), zap.String("destinatarios", c.Destinatarios))
}

// IniciarAlertas roda a verificacao periodicamente ate' o contexto encerrar.
func (h *handlers) IniciarAlertas(ctx context.Context) {
	h.semearConfigDoAmbiente(ctx)
	go func() {
		t := time.NewTicker(intervaloVerificacao)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				h.verificarAlertas(ctx)
			}
		}
	}()
}

func (h *handlers) verificarAlertas(pai context.Context) {
	ctx, cancel := context.WithTimeout(pai, 60*time.Second)
	defer cancel()

	cfg, err := h.repo.GetConfigAlertas(ctx)
	if err != nil || cfg == nil {
		return
	}
	if !cfg.Configurado() {
		return // nada configurado: silencio proposital, nao erro
	}
	rem := remetenteDaConfig(cfg)

	if cfg.TokenAtivo {
		h.alertarTokensVencidos(ctx, rem, time.Duration(cfg.TokenJanelaH)*time.Hour)
	}
	if cfg.SessaoAtivo {
		h.alertarSessoesCaidas(ctx, rem, time.Duration(cfg.SessaoJanelaMin)*time.Minute)
	}
	_ = h.repo.LimparAlertasAntigos(ctx)
}

func (h *handlers) alertarTokensVencidos(ctx context.Context, rem *email.Remetente, janela time.Duration) {
	portais, err := h.repo.ListarTokensVencidos(ctx)
	if err != nil {
		h.log.Warn("alertas: falha ao listar tokens vencidos", zap.Error(err))
		return
	}
	for _, p := range portais {
		ok, err := h.repo.DeveAvisar(ctx, alertaTokenVencido, p.Domain, p.Domain, janela)
		if err != nil || !ok {
			continue
		}
		horas := int(time.Since(p.ExpiresAt).Hours())
		corpo := corpoAlerta(
			"Token do Bitrix24 vencido",
			p.Domain,
			fmt.Sprintf("O token venceu em %s (ha %d hora(s)) e a renovacao nao esta passando.",
				p.ExpiresAt.Format("02/01/2006 15:04"), horas),
			"Enquanto nao renovar, NENHUMA mensagem do cliente chega no Contact Center — elas ficam presas na fila.",
			[]string{
				"Abrir Saude do cliente no painel e conferir o bloco Token",
				"Se client_id/client_secret estiverem vazios, cadastrar em Credenciais do app",
				"Depois de renovar, usar Reentregar mensagens para escoar a fila",
			})
		if err := rem.Enviar(ctx, "[UC Talk] Token vencido — "+p.Domain, corpo); err != nil {
			h.log.Error("alertas: falha ao enviar aviso de token",
				zap.String("dominio", p.Domain), zap.Error(err))
			continue
		}
		h.log.Info("alerta enviado: token vencido", zap.String("dominio", p.Domain))
	}
}

func (h *handlers) alertarSessoesCaidas(ctx context.Context, rem *email.Remetente, janela time.Duration) {
	if h.waManager == nil {
		return
	}
	// O banco diz quem DEVERIA estar atendendo; o manager diz quem esta'
	// mesmo. A diferenca e' o numero que caiu.
	vivos := map[string]bool{}
	for _, s := range h.waManager.ConnectedSessions() {
		vivos[s.Phone] = true
	}
	sessoes, err := h.repo.ListActiveSessions(ctx)
	if err != nil {
		h.log.Warn("alertas: falha ao listar sessoes", zap.Error(err))
		return
	}
	for _, s := range sessoes {
		// Sessao Cloud API nao vive no manager (e' HTTPS stateless): ausencia
		// ali nao significa queda, e alertar pelo mesmo criterio geraria
		// alarme falso a cada ciclo.
		if string(s.Type) == "cloud_api" {
			continue
		}
		numero := somenteNumero(s.JID)
		if numero == "" || vivos[numero] {
			continue
		}
		dominio := ""
		if acct, aerr := h.repo.GetBitrixAccountByJID(ctx, s.JID); aerr == nil && acct != nil {
			dominio = acct.Domain
		}
		ok, derr := h.repo.DeveAvisar(ctx, alertaSessaoCaiu, numero, dominio, janela)
		if derr != nil || !ok {
			continue
		}
		corpo := corpoAlerta(
			"Numero de WhatsApp desconectado",
			naoVazio(dominio, "cliente nao identificado"),
			fmt.Sprintf("O numero %s consta como ativo no sistema, mas nao ha conexao viva com o WhatsApp.", numero),
			"Enquanto estiver caido, o cliente nao recebe nem envia mensagem por este numero.",
			[]string{
				"Abrir Saude do cliente e conferir o bloco Sessoes WhatsApp",
				"Se nao reconectar sozinho, parear de novo em Conectar WhatsApp",
			})
		if err := rem.Enviar(ctx, "[UC Talk] Numero desconectado — "+numero, corpo); err != nil {
			h.log.Error("alertas: falha ao enviar aviso de sessao",
				zap.String("numero", numero), zap.Error(err))
			continue
		}
		h.log.Info("alerta enviado: sessao caiu",
			zap.String("numero", numero), zap.String("dominio", dominio))
	}
}

// corpoAlerta monta um e-mail que diz, nesta ordem: o que houve, quem foi
// afetado, qual a consequencia e o que fazer. Alerta sem "o que fazer" vira
// ruido — quem recebe as 3h da manha precisa do proximo passo escrito.
func corpoAlerta(titulo, cliente, oQueHouve, consequencia string, passos []string) string {
	var b strings.Builder
	b.WriteString(`<div style="font-family:-apple-system,Segoe UI,Roboto,sans-serif;max-width:560px;color:#0f172a">`)
	b.WriteString(`<div style="background:#0f172a;color:#fff;padding:16px 20px;border-radius:12px 12px 0 0">`)
	b.WriteString(`<div style="font-size:12px;opacity:.7;letter-spacing:.5px">UC TALK &middot; ALERTA</div>`)
	b.WriteString(`<div style="font-size:18px;font-weight:700;margin-top:2px">` + html.EscapeString(titulo) + `</div>`)
	b.WriteString(`</div>`)
	b.WriteString(`<div style="border:1px solid #e2e8f0;border-top:0;border-radius:0 0 12px 12px;padding:20px">`)
	b.WriteString(`<p style="margin:0 0 4px;font-size:13px;color:#64748b">Cliente</p>`)
	b.WriteString(`<p style="margin:0 0 16px;font-size:15px;font-weight:600">` + html.EscapeString(cliente) + `</p>`)
	b.WriteString(`<p style="margin:0 0 14px;font-size:14px;line-height:1.6">` + html.EscapeString(oQueHouve) + `</p>`)
	b.WriteString(`<p style="margin:0 0 18px;padding:12px 14px;background:#fef2f2;border-left:3px solid #ef4444;font-size:14px;line-height:1.6;color:#991b1b">` + html.EscapeString(consequencia) + `</p>`)
	if len(passos) > 0 {
		b.WriteString(`<p style="margin:0 0 6px;font-size:13px;color:#64748b">O que fazer</p><ol style="margin:0;padding-left:20px;font-size:14px;line-height:1.8">`)
		for _, p := range passos {
			b.WriteString(`<li>` + html.EscapeString(p) + `</li>`)
		}
		b.WriteString(`</ol>`)
	}
	b.WriteString(`<p style="margin:18px 0 0;font-size:12px;color:#94a3b8">Enviado automaticamente pelo UC Talk em ` +
		time.Now().Format("02/01/2006 15:04") + `.</p>`)
	b.WriteString(`</div></div>`)
	return b.String()
}

func naoVazio(v, alternativa string) string {
	if strings.TrimSpace(v) == "" {
		return alternativa
	}
	return v
}

// somenteNumero extrai o numero base do JID, sem device suffix nem dominio.
func somenteNumero(jid string) string {
	if i := strings.IndexByte(jid, '@'); i > 0 {
		jid = jid[:i]
	}
	if i := strings.IndexByte(jid, ':'); i > 0 {
		jid = jid[:i]
	}
	return jid
}
