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
		corpo := h.montarAlerta(
			email.CatTokenVencido,
			"Token do Bitrix24 vencido — "+p.Domain,
			"<p>A renovação do token de <b>"+p.Domain+"</b> não está passando.</p>"+
				"<p><b>Enquanto não renovar, nenhuma mensagem do cliente chega no Contact Center</b> — "+
				"elas ficam presas na fila.</p>",
			"Abra Saúde do cliente e confira o bloco Token. Se client_id/client_secret estiverem vazios, "+
				"cadastre em Credenciais do app. Depois de renovar, use Reentregar mensagens para escoar a fila.",
			[]email.LinhaContexto{
				{Rotulo: "Cliente", Valor: p.Domain},
				{Rotulo: "Venceu em", Valor: p.ExpiresAt.Format("02/01/2006 15:04")},
				{Rotulo: "Tempo vencido", Valor: fmt.Sprintf("%d hora(s)", horas)},
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
		cliente := naoVazio(dominio, "cliente não identificado")
		corpo := h.montarAlerta(
			email.CatSessaoCaiu,
			"Número de WhatsApp desconectado — "+numero,
			"<p>O número <b>"+numero+"</b> consta como ativo no sistema, mas não há conexão viva com o WhatsApp.</p>"+
				"<p><b>Enquanto estiver caído, o cliente não recebe nem envia mensagem por este número.</b></p>",
			"Abra Saúde do cliente e confira o bloco Sessões WhatsApp. "+
				"Se não reconectar sozinho, pareie de novo em Conectar WhatsApp.",
			[]email.LinhaContexto{
				{Rotulo: "Cliente", Valor: cliente},
				{Rotulo: "Número", Valor: numero},
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

// montarAlerta usa o template da UC Technology (internal/email/template.go),
// o mesmo que o backend de ferramentas ja' manda. Alerta do UC Talk chega com
// a cara dos outros alertas da plataforma, e quem esta' de plantao reconhece
// de relance em vez de ter que ler pra descobrir a origem.
func (h *handlers) montarAlerta(categoria, titulo, corpoHTML, acao string, ctx []email.LinhaContexto) string {
	return email.Renderizar(email.Alerta{
		Categoria: categoria,
		Titulo:    titulo,
		CorpoHTML: corpoHTML,
		Contexto:  ctx,
		Acao:      acao,
		BaseURL:   h.cfg.App.BaseURL(),
	})
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
