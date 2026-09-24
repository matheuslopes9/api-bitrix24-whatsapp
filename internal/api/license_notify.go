// license_notify.go — aviso de vencimento de licenca para o financeiro.
//
// O financeiro nao usa o sistema: quem lanca pagamento e' o suporte. O que
// o financeiro precisa e' ser avisado de quem esta pra vencer e de quem
// venceu sem pagamento, pra cobrar.
package api

import (
	"context"
	"fmt"
	"time"

	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/email"
	"go.uber.org/zap"
)

// Quantos dias antes do vencimento o aviso sai.
const diasAvisoVencimento = 7

// Tipos de aviso. Viram chave em license_notifications junto com a data de
// vencimento, o que impede reenviar o mesmo aviso todo dia.
const (
	avisoVencendo = "vencendo"
	avisoVencida  = "vencida"
)

// LicenseNotifier entrega o aviso. A UC Technology ja' tem um sistema de
// envio proprio; a interface existe pra encaixar esse sistema sem mexer no
// job que decide QUEM avisar e QUANDO.
type LicenseNotifier interface {
	LicencaVencendo(ctx context.Context, lic *db.TenantLicense, diasRestantes int) error
	LicencaVencida(ctx context.Context, lic *db.TenantLicense, diasVencida int) error
}

// notificadorLog e' a implementacao padrao enquanto o envio real nao esta
// ligado. Registra no log com nivel WARN — aparece na aba "Logs ao vivo" do
// admin, entao a informacao nao se perde: da' pra agir olhando o painel.
type notificadorLog struct{ log *zap.Logger }

func (n notificadorLog) LicencaVencendo(_ context.Context, lic *db.TenantLicense, dias int) error {
	n.log.Warn("licenca vencendo — avisar o financeiro",
		zap.String("cliente", lic.Domain),
		zap.Int("dias_restantes", dias),
		zap.String("vence_em", lic.ValidUntil.Format("2006-01-02")))
	return nil
}

func (n notificadorLog) LicencaVencida(_ context.Context, lic *db.TenantLicense, dias int) error {
	n.log.Warn("licenca VENCIDA sem pagamento — avisar o financeiro",
		zap.String("cliente", lic.Domain),
		zap.Int("dias_vencida", dias),
		zap.String("venceu_em", lic.ValidUntil.Format("2006-01-02")))
	return nil
}

// notificadorEmail entrega o aviso na caixa do financeiro. Reaproveita o
// mesmo corpo dos alertas operacionais pra que todo e-mail do UC Talk tenha
// a mesma cara — quem recebe reconhece de relance.
type notificadorEmail struct {
	rem     *email.Remetente
	log     *zap.Logger
	baseURL string
}

func (n notificadorEmail) LicencaVencendo(ctx context.Context, lic *db.TenantLicense, dias int) error {
	corpo := email.Renderizar(email.Alerta{
		Categoria: email.CatLicenca,
		Titulo:    "Licença vence em breve — " + lic.Domain,
		CorpoHTML: fmt.Sprintf("<p>A licença de <b>%s</b> vence em <b>%d dia(s)</b> (%s).</p>"+
			"<p>Vencer <b>não</b> bloqueia o atendimento — o cliente continua sendo atendido "+
			"normalmente. O aviso é para a cobrança não passar batida.</p>",
			lic.Domain, dias, lic.ValidUntil.Format("02/01/2006")),
		Contexto: []email.LinhaContexto{
			{Rotulo: "Cliente", Valor: lic.Domain},
			{Rotulo: "Vencimento", Valor: lic.ValidUntil.Format("02/01/2006")},
			{Rotulo: "Dias restantes", Valor: fmt.Sprintf("%d", dias)},
		},
		Acao:    "Confirme a renovação com o cliente e registre o pagamento em Licenças assim que entrar.",
		BaseURL: n.baseURL,
	})
	return n.rem.Enviar(ctx, "[UC Talk] Licenca vencendo — "+lic.Domain, corpo)
}

func (n notificadorEmail) LicencaVencida(ctx context.Context, lic *db.TenantLicense, dias int) error {
	corpo := email.Renderizar(email.Alerta{
		Categoria: email.CatLicenca,
		Titulo:    "Licença VENCIDA sem pagamento — " + lic.Domain,
		CorpoHTML: fmt.Sprintf("<p>A licença de <b>%s</b> venceu em <b>%s</b> — há %d dia(s) — "+
			"e nenhum pagamento novo foi registrado.</p>"+
			"<p>O atendimento <b>não</b> foi interrompido, por decisão de projeto: "+
			"ninguém fica sem atendimento por boleto atrasado.</p>",
			lic.Domain, lic.ValidUntil.Format("02/01/2006"), dias),
		Contexto: []email.LinhaContexto{
			{Rotulo: "Cliente", Valor: lic.Domain},
			{Rotulo: "Venceu em", Valor: lic.ValidUntil.Format("02/01/2006")},
			{Rotulo: "Dias em atraso", Valor: fmt.Sprintf("%d", dias)},
		},
		Acao:    "Acione o comercial para cobrança e registre o pagamento em Licenças quando entrar.",
		BaseURL: n.baseURL,
	})
	return n.rem.Enviar(ctx, "[UC Talk] Licenca VENCIDA — "+lic.Domain, corpo)
}

// IniciarAvisosDeLicenca roda a verificacao uma vez por dia.
//
// Usa notificadorLog enquanto o sistema de envio da empresa nao esta
// plugado. Trocar a implementacao nao exige tocar nesta funcao.
func (h *handlers) IniciarAvisosDeLicenca(ctx context.Context) {
	// O e-mail e' o canal que o financeiro realmente le — ele nao tem login
	// no painel, foi decisao de projeto. Sem envio configurado cai no log,
	// que ao menos aparece na aba "Logs ao vivo".
	// A escolha do canal e' feita A CADA RODADA, nao no boot: a configuracao
	// vive no banco e pode mudar pela tela de Alertas sem reiniciar o app.
	escolherCanal := func(ctx context.Context) LicenseNotifier {
		cfg, err := h.repo.GetConfigAlertas(ctx)
		if err != nil || cfg == nil || !cfg.LicencaAtivo || !cfg.Configurado() {
			// Sem envio (ou aviso desligado na tela): cai no log, que ao menos
			// aparece em "Logs ao vivo" — a informacao nao se perde.
			return notificadorLog{log: h.log}
		}
		return notificadorEmail{rem: remetenteDaConfig(cfg), log: h.log, baseURL: h.cfg.App.BaseURL()}
	}
	go func() {
		// Espera o boot assentar (migrations, carga de sessoes) antes da
		// primeira rodada.
		time.Sleep(2 * time.Minute)
		for {
			if ctx.Err() != nil {
				return
			}
			h.verificarVencimentos(ctx, escolherCanal(ctx))
			time.Sleep(24 * time.Hour)
		}
	}()
	h.log.Info("avisos de licenca ativos",
		zap.Int("dias_de_antecedencia", diasAvisoVencimento))
}

// verificarVencimentos percorre as licencas com prazo e dispara o aviso
// devido — no maximo UM por cliente, por tipo, por data de vencimento.
func (h *handlers) verificarVencimentos(ctx context.Context, n LicenseNotifier) {
	lics, err := h.repo.ListLicensesExpiringOrExpired(ctx, diasAvisoVencimento)
	if err != nil {
		h.log.Error("avisos de licenca: consulta falhou", zap.Error(err))
		return
	}
	var vencendo, vencidas int
	for _, lic := range lics {
		dias, temPrazo := lic.DaysUntilExpiry()
		if !temPrazo {
			continue
		}

		tipo := avisoVencendo
		if lic.Expired() {
			tipo = avisoVencida
		}

		// A data de vencimento entra na chave de deduplicacao de proposito:
		// quando o suporte registra um pagamento e a vigencia avanca, o
		// proximo ciclo tem uma ref_date nova e o aviso volta a ser
		// permitido. Sem isso, um cliente avisado uma vez nunca mais seria.
		novo, err := h.repo.MarkNotificationSent(ctx, lic.Domain, tipo, *lic.ValidUntil)
		if err != nil {
			h.log.Warn("avisos de licenca: nao consegui registrar o envio",
				zap.String("cliente", lic.Domain), zap.Error(err))
			continue
		}
		if !novo {
			continue // ja' avisado para este vencimento
		}

		if tipo == avisoVencida {
			if err := n.LicencaVencida(ctx, lic, -dias); err != nil {
				h.log.Error("avisos de licenca: envio falhou",
					zap.String("cliente", lic.Domain), zap.Error(err))
			}
			vencidas++
		} else {
			if err := n.LicencaVencendo(ctx, lic, dias); err != nil {
				h.log.Error("avisos de licenca: envio falhou",
					zap.String("cliente", lic.Domain), zap.Error(err))
			}
			vencendo++
		}
	}
	if vencendo > 0 || vencidas > 0 {
		h.log.Info("avisos de licenca: rodada concluida",
			zap.Int("vencendo", vencendo), zap.Int("vencidas", vencidas))
	}
}
