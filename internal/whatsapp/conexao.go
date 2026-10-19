package whatsapp

// conexao.go — o que fazer quando o WhatsApp recusa ou toma a conexao.
//
// O whatsmeow NAO reconecta sozinho nestes casos; quem reconectava era o
// watchdog, a cada 30s, sem saber o motivo. Em tres deles isso so' piora:
//
//   - StreamReplaced: outro processo conectou com a MESMA sessao (deploy que
//     deixou o container antigo vivo, duas instancias). Reconectar toma a
//     sessao de volta, o outro faz o mesmo, e as mensagens se alternam entre
//     os dois — sai por um, o recibo chega no outro.
//   - ClientOutdated (405): a versao do whatsmeow ficou velha pro protocolo.
//     Reconectar da' 405 de novo pra sempre, e refazer o QR tambem nao
//     resolve: o remedio e' atualizar a dependencia e fazer deploy.
//   - TemporaryBan: o WhatsApp restringiu o numero por um tempo que ele
//     mesmo informa. Insistir antes disso so' prolonga a restricao.
//
// Nos tres, a reconexao fica pausada pelo tempo certo e o log diz o motivo.

import (
	"context"
	"time"

	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"go.mau.fi/whatsmeow/types/events"
	"go.uber.org/zap"
)

const (
	// Tempo pro deploy terminar e o processo antigo morrer. Se a outra
	// instancia continuar viva, a sessao alterna a cada 10 min, nao a cada
	// 30s — e o log de erro aparece a cada vez.
	pausaSessaoTomada = 10 * time.Minute
	// Ninguem atualiza a dependencia em menos que isso; e o 405 so' some
	// com deploy, que reinicia o processo e zera a pausa.
	pausaClienteDesatualizado = 30 * time.Minute
	// Banimento sem prazo informado: espera um pouco antes de testar de novo.
	pausaBanimentoSemPrazo = 15 * time.Minute
)

type pausaReconexao struct {
	ate    time.Time
	motivo string
}

// ReconexaoPausada diz se o watchdog deve deixar esta sessao quieta agora.
func (m *Manager) ReconexaoPausada(jid string) (bool, string, time.Duration) {
	v, ok := m.pausas.Load(extractPhoneFromJID(jid))
	if !ok {
		return false, "", 0
	}
	p := v.(pausaReconexao)
	falta := time.Until(p.ate)
	if falta <= 0 {
		m.pausas.Delete(extractPhoneFromJID(jid))
		return false, "", 0
	}
	return true, p.motivo, falta
}

func (m *Manager) pausarReconexao(sess *Session, por time.Duration, motivo string) {
	m.pausas.Store(extractPhoneFromJID(sess.JID), pausaReconexao{ate: time.Now().Add(por), motivo: motivo})
	_ = m.repo.UpdateSessionStatus(context.Background(), sess.JID, db.SessionDisconnected)
}

// tratarFalhaDeConexao cuida dos eventos acima. Devolve false se o evento
// nao e' dele.
func (m *Manager) tratarFalhaDeConexao(sess *Session, rawEvt interface{}) bool {
	switch evt := rawEvt.(type) {
	case *events.StreamReplaced:
		m.log.Error("sessao tomada por OUTRA conexao com a mesma sessao — confira se ha' dois processos/containers rodando; reconexao pausada",
			zap.String("jid", sess.JID), zap.Duration("pausa", pausaSessaoTomada))
		m.pausarReconexao(sess, pausaSessaoTomada, "sessao tomada por outra conexao (StreamReplaced)")
	case *events.ClientOutdated:
		m.log.Error("WhatsApp recusou a conexao: cliente desatualizado (405). Atualize go.mau.fi/whatsmeow e faca deploy — NAO refaca o QR, nao resolve",
			zap.String("jid", sess.JID), zap.Duration("pausa", pausaClienteDesatualizado))
		m.pausarReconexao(sess, pausaClienteDesatualizado, "whatsmeow desatualizado (405)")
	case *events.TemporaryBan:
		pausa := evt.Expire
		if pausa <= 0 {
			pausa = pausaBanimentoSemPrazo
		}
		m.log.Error("WhatsApp restringiu o numero temporariamente — reconexao pausada ate' o prazo informado",
			zap.String("jid", sess.JID), zap.String("motivo", evt.Code.String()),
			zap.Duration("expira_em", evt.Expire))
		m.pausarReconexao(sess, pausa, "restricao temporaria do WhatsApp: "+evt.Code.String())
	case *events.ConnectFailure:
		// Motivo que o whatsmeow nao conhece: ele ja' desistiu de reconectar.
		// Sem pausa — o watchdog tenta de novo —, mas agora com o motivo no log.
		m.log.Error("WhatsApp recusou a conexao",
			zap.String("jid", sess.JID), zap.Int("motivo", int(evt.Reason)),
			zap.String("mensagem", evt.Message))
		_ = m.repo.UpdateSessionStatus(context.Background(), sess.JID, db.SessionDisconnected)
	default:
		return false
	}
	return true
}

// ReceiptHandler recebe os recibos (entregue, lido) das mensagens enviadas.
type ReceiptHandler func(sessionJID string, evt *events.Receipt)

// SetReceiptHandler registra quem trata os recibos de entrega e leitura.
func (m *Manager) SetReceiptHandler(h ReceiptHandler) { m.onRecibo = h }
