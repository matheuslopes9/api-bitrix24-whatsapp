// conector_reconciliacao.go — mantem o conector de cada cliente ativo sozinho.
//
// POR QUE EXISTE: conector inativo e' a falha mais silenciosa deste sistema.
// O `imconnector.send.messages` recusa, a mensagem do cliente morre na fila, e
// NAO aparece erro em lugar nenhum que alguem olhe. Descobrimos em 28/09 que o
// portal do teclife estava assim — ninguem sabia, e ninguem saberia.
//
// Isso nao escala: com dez clientes, ninguem vai notar que o setimo parou. O
// alerta existente cobre token vencido e numero caido; conector inativo passava
// por baixo dos dois, porque token e numero continuam perfeitos.
//
// POR QUE CONSERTA EM VEZ DE AVISAR: o reparo e' deterministico e idempotente —
// register, activate, data.set, na ordem que a documentacao do Bitrix exige.
// Rodou duas vezes em 28/09 (crm e teclife) e resolveu nas duas. Acordar uma
// pessoa para clicar um botao cujo resultado e' previsivel e' desperdicio; o
// aviso fica para quando o conserto NAO resolve — ai' sim precisa de gente.
package api

import (
	"context"
	"encoding/json"
	"time"

	"github.com/uctechnology/api-bitrix24-whatsapp/internal/bitrix"
	"go.uber.org/zap"
)

// intervaloReconciliacao: conector nao cai sozinho o tempo todo — ele cai em
// evento (troca de linha, reinstalacao, activate solto). De hora em hora pega
// isso rapido sem martelar o Bitrix, que tem limite por metodo.
const intervaloReconciliacao = 1 * time.Hour

// IniciarReconciliacaoConector liga a checagem periodica.
func (h *handlers) IniciarReconciliacaoConector(ctx context.Context) {
	if h.repo == nil || h.bitrixClient == nil {
		return
	}
	go func() {
		// O boot precisa assentar: token pode estar renovando e sessao
		// carregando. Checar agora so' geraria republicacao desnecessaria.
		time.Sleep(5 * time.Minute)
		t := time.NewTicker(intervaloReconciliacao)
		defer t.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			semPanico(h.log, "reconciliacao do conector", func() { h.reconciliarConectores(ctx) })
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	h.log.Info("reconciliacao do conector ativa", zap.Duration("intervalo", intervaloReconciliacao))
}

// reconciliarConectores percorre os vinculos e reativa o que estiver fora.
func (h *handlers) reconciliarConectores(ctx context.Context) {
	portais, err := h.repo.ListBitrixPortals(ctx)
	if err != nil {
		h.log.Warn("reconciliacao: falha ao listar portais", zap.Error(err))
		return
	}
	for _, p := range portais {
		contas, err := h.repo.ListBitrixAccountsByDomain(ctx, normalizePortalDomain(p.Domain))
		if err != nil {
			continue
		}
		creds := h.portalToCreds(p)
		for _, a := range contas {
			// Sem linha nao da' pra checar: o imconnector.status responde
			// CONFIGURED=false para LINE=0 qualquer que seja o conector, e
			// republicar sem linha nao tem onde pousar.
			if a.ConnectorID == "" || a.OpenLineID <= 0 {
				continue
			}
			h.reconciliarUm(ctx, creds, a.Domain, a.ConnectorID, a.SessionJID, a.OpenLineID)
		}
	}
}

// reconciliarUm checa um vinculo e, se estiver fora, republica e confere.
func (h *handlers) reconciliarUm(ctx context.Context, creds bitrix.TenantCreds, dominio, connectorID, sessionJID string, linha int) {
	campos := []zap.Field{
		zap.String("domain", dominio),
		zap.String("connector_id", connectorID),
		zap.Int("line", linha),
	}

	ativo, entendeu := h.conectorAtivo(ctx, creds, connectorID, linha)
	if !entendeu {
		// Nao deu pra saber (token vencido, Bitrix fora, resposta estranha).
		// Republicar as cegas martelaria o Bitrix a cada hora sem motivo: o
		// alerta de token ja' cobre a causa mais provavel.
		h.log.Info("reconciliacao: estado do conector indeterminado — pulando", campos...)
		return
	}
	if ativo {
		return // o caso normal, e o silencioso de proposito
	}

	h.log.Warn("reconciliacao: conector INATIVO — republicando", campos...)
	_, nome, _ := h.connectorDaSessao(ctx, sessionJID)
	h.publicarConector(ctx, creds, connectorID, nome, linha, "reconciliacao")

	// Confere: sem isto, um conserto que falhou vira silencio igual ao
	// problema que ele deveria resolver.
	if ok, entendeu := h.conectorAtivo(ctx, creds, connectorID, linha); entendeu && ok {
		h.log.Info("reconciliacao: conector reativado sozinho", campos...)
		return
	}
	h.log.Error("reconciliacao: conector SEGUE INATIVO apos republicar — precisa de gente",
		append(campos, zap.String("acao", "abra Saude do cliente e rode Testar conexao"))...)
}

// conectorAtivo devolve (ativo, entendeu). O segundo valor separa "esta' fora"
// de "nao consegui saber" — tratar os dois como iguais faria o job republicar a
// cada hora num portal cujo token esta' vencido.
func (h *handlers) conectorAtivo(ctx context.Context, creds bitrix.TenantCreds, connectorID string, linha int) (bool, bool) {
	raw, err := h.bitrixClient.GetConnectorStatus(ctx, creds, connectorID, linha)
	if err != nil {
		return false, false
	}
	var s statusConector
	if jerr := json.Unmarshal(raw, &s); jerr != nil || s.Connector == "" {
		return false, false
	}
	return s.Status, true
}
