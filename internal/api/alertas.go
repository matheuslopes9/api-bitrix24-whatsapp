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
				semPanico(h.log, "alertas", func() { h.verificarAlertas(ctx) })
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
		avisoID, ok, err := h.repo.DeveAvisar(ctx, alertaTokenVencido, p.Domain, p.Domain, janela)
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
			_ = h.repo.DesfazerAviso(ctx, avisoID) // tenta de novo no proximo ciclo
			continue
		}
		h.log.Info("alerta enviado: token vencido", zap.String("dominio", p.Domain))
	}
}

func (h *handlers) alertarSessoesCaidas(ctx context.Context, rem *email.Remetente, janela time.Duration) {
	if h.waManager == nil {
		return
	}
	vivos := map[string]bool{}
	for _, s := range h.waManager.ConnectedSessions() {
		vivos[s.Phone] = true
	}
	// Desvinculado nao e' "caiu": o socket esta' aberto e vai continuar
	// aberto. Esperar reconectar e' esperar o que nao vai acontecer, entao o
	// aviso precisa dizer outra coisa.
	semVinculo := map[string]bool{}
	for _, sv := range h.waManager.SessoesComSocketSemLogin() {
		semVinculo[sv.Phone] = true
	}

	// QUEM DEVERIA ESTAR ATENDENDO: toda sessao que continua no banco.
	//
	// BUG que isto corrige: a lista vinha so' das sessoes com status
	// 'active' — e o proprio sistema grava 'disconnected' no instante em que
	// a conexao cai (evento de queda, watchdog, pausa de reconexao). O numero
	// que caiu sumia da lista antes do alerta olhar, e so' o caso
	// "desvinculado" (socket de pe, status ainda ativo) chegava a avisar.
	//
	// Desconectar de proposito pela tela APAGA a sessao do banco, entao o que
	// sobra aqui e' numero que deveria estar no ar — a mesma lista que o
	// watchdog tenta reconectar.
	sessoes, err := h.repo.ListAllSessions(ctx)
	if err != nil {
		h.log.Warn("alertas: falha ao listar sessoes", zap.Error(err))
		return
	}
	foraDoAr := map[string]bool{}
	for _, s := range sessoes {
		// Sessao Cloud API nao vive no manager (e' HTTPS stateless): ausencia
		// ali nao significa queda, e alertar pelo mesmo criterio geraria
		// alarme falso a cada ciclo.
		if string(s.Type) == "cloud_api" {
			continue
		}
		numero := somenteNumero(s.JID)
		if numero == "" || vivos[numero] || foraDoAr[numero] {
			continue
		}
		foraDoAr[numero] = true

		// Numero sem vinculo com nenhum portal nao atende ninguem: nao ha'
		// cliente a avisar nem acao a tomar.
		acct, aerr := h.repo.GetBitrixAccountByJID(ctx, s.JID)
		if aerr != nil || acct == nil || acct.Domain == "" {
			continue
		}
		dominio := acct.Domain

		// Carencia: so' avisa se o numero ja' estava fora do ar no ciclo
		// anterior. Queda de segundos — deploy, reconexao do watchdog — volta
		// sozinha e nao e' caso pra e-mail.
		visto, _ := h.quedaVista.LoadOrStore(numero, time.Now())
		desde := visto.(time.Time)
		if time.Since(desde) < carenciaQueda {
			continue
		}

		avisoID, ok, derr := h.repo.DeveAvisar(ctx, alertaSessaoCaiu, numero, dominio, janela)
		if derr != nil || !ok {
			continue
		}
		titulo, explicacao, acao, assunto := textoDaQueda(numero, semVinculo[numero], h.waManager)
		corpo := h.montarAlerta(
			email.CatSessaoCaiu,
			titulo,
			explicacao,
			acao,
			[]email.LinhaContexto{
				{Rotulo: "Cliente", Valor: dominio},
				{Rotulo: "Número", Valor: numero},
				{Rotulo: "Fora do ar desde", Valor: desde.Format("02/01/2006 15:04")},
			})
		if err := rem.Enviar(ctx, assunto, corpo); err != nil {
			h.log.Error("alertas: falha ao enviar aviso de sessao",
				zap.String("numero", numero), zap.Error(err))
			_ = h.repo.DesfazerAviso(ctx, avisoID) // tenta de novo no proximo ciclo
			continue
		}
		h.log.Info("alerta enviado: sessao caiu",
			zap.String("numero", numero), zap.String("dominio", dominio),
			zap.Bool("desvinculada", semVinculo[numero]))
	}
	// Quem voltou sai da contagem: a proxima queda recomeca a carencia.
	h.quedaVista.Range(func(k, _ any) bool {
		if !foraDoAr[k.(string)] {
			h.quedaVista.Delete(k)
		}
		return true
	})
}

// carenciaQueda: quanto tempo o numero precisa estar fora do ar pra virar
// alerta. Pouco menos que o ciclo de 5 min, pra disparar na segunda leitura.
const carenciaQueda = 4 * time.Minute

// pausaConsultavel e' o pedaco do manager que o texto do alerta precisa —
// interface pra o teste nao depender de uma conexao de WhatsApp.
type pausaConsultavel interface {
	ReconexaoPausada(jid string) (bool, string, time.Duration)
}

// textoDaQueda escreve o aviso conforme o MOTIVO, porque cada um pede uma
// acao diferente — e alerta sem "o que fazer" e' ruido.
func textoDaQueda(numero string, desvinculado bool, wa pausaConsultavel) (titulo, explicacao, acao, assunto string) {
	impacto := "<p><b>Enquanto estiver fora do ar, o cliente não recebe nem envia mensagem por este número.</b></p>"
	if desvinculado {
		return "Número de WhatsApp DESVINCULADO — " + numero,
			"<p>O aparelho do número <b>" + numero + "</b> foi removido em " +
				"<i>Aparelhos conectados</i> no WhatsApp — pelo cliente ou por troca de celular.</p>" + impacto +
				"<p>Este caso <b>não se resolve sozinho</b>: a conexão continua de pé, o que falta é a autorização do aparelho.</p>",
			"Peça ao cliente para abrir Conectar WhatsApp e ler o QR Code de novo. Reiniciar o serviço não resolve.",
			"[UC Talk] Numero DESVINCULADO (precisa ler o QR) — " + numero
	}
	// JID completo: a pausa e' guardada pelo numero extraido do JID, e um
	// numero puro nao casa (extractPhoneFromJID exige o "@").
	if pausada, motivo, falta := wa.ReconexaoPausada(numero + "@s.whatsapp.net"); pausada {
		base := "<p>O WhatsApp recusou a conexão do número <b>" + numero + "</b>: " + motivo + ".</p>" + impacto
		switch {
		case strings.Contains(motivo, "405"):
			return "WhatsApp recusou a conexão: versão desatualizada — " + numero,
				base + "<p>A biblioteca do WhatsApp usada pelo UC Talk ficou antiga para o protocolo atual. " +
					"Isso afeta <b>todos</b> os números conectados, não só este.</p>",
				"Atualize a dependência go.mau.fi/whatsmeow e faça deploy. NÃO peça ao cliente para ler o QR de novo: não resolve.",
				"[UC Talk] URGENTE: versao do WhatsApp desatualizada — " + numero
		case strings.Contains(motivo, "restricao"):
			return "Número restrito temporariamente pelo WhatsApp — " + numero,
				base + fmt.Sprintf("<p>O próprio WhatsApp informou o prazo; o sistema tenta de novo em %s.</p>", falta.Round(time.Minute)),
				"Aguarde o prazo e evite envios em massa por este número. Se a restrição se repetir, revise o volume de envio do cliente.",
				"[UC Talk] Numero restrito pelo WhatsApp — " + numero
		case strings.Contains(motivo, "outra conexao"):
			return "Sessão tomada por outra conexão — " + numero,
				base + "<p>Outro processo conectou com a mesma sessão. Normalmente é um deploy que deixou o container antigo vivo.</p>",
				"Confira no EasyPanel se há dois containers do UC Talk rodando e pare o antigo.",
				"[UC Talk] Sessao tomada por outro processo — " + numero
		}
		return "Número de WhatsApp desconectado — " + numero, base,
			"Abra Saúde do cliente e confira o bloco Sessões WhatsApp.",
			"[UC Talk] Numero desconectado — " + numero
	}
	return "Número de WhatsApp desconectado — " + numero,
		"<p>O número <b>" + numero + "</b> deveria estar atendendo, mas não há conexão viva com o WhatsApp há alguns minutos.</p>" + impacto,
		"Abra Saúde do cliente e confira o bloco Sessões WhatsApp. Se não reconectar sozinho, pareie de novo em Conectar WhatsApp.",
		"[UC Talk] Numero desconectado — " + numero
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
