package main

// confirmacao.go — o que o operador ve no Bitrix depois de mandar a mensagem.
//
// Dois buracos que existiam:
//
//  1. "Entregue" cedo demais. O Bitrix tem uma confirmacao so'
//     (imconnector.send.status.delivery), e ela tambem marca a mensagem como
//     LIDA pelo cliente. A gente mandava assim que o servidor do WhatsApp
//     aceitava — antes do celular do cliente receber. Agora espera o recibo
//     do aparelho. Ver queue.ConfirmacaoPendente.
//
//  2. Falha muda. Quando a fila desistia de uma mensagem (dead queue), o
//     operador nao ficava sabendo. Agora a bolha dele vira o aviso de falha.

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/bitrix"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/queue"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/whatsapp"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"go.uber.org/zap"
)

// confirmarEntregaNoBitrix manda o "entregue" da mensagem do operador.
func confirmarEntregaNoBitrix(ctx context.Context, bitrixClient *bitrix.Client, repo *db.Repository, log *zap.Logger, waID string, c queue.ConfirmacaoPendente) {
	acct, err := repo.GetBitrixAccountByJID(ctx, c.SessionJID)
	if err != nil {
		log.Warn("confirmacao de entrega: conta bitrix nao encontrada",
			zap.String("session", c.SessionJID), zap.Error(err))
		return
	}
	creds := bitrix.TenantCreds{
		Domain:       acct.Domain,
		ClientID:     acct.ClientID,
		ClientSecret: acct.ClientSecret,
		RedirectURI:  acct.RedirectURI,
	}
	if err := bitrixClient.ConnectorSetOutboundDelivery(ctx, creds,
		c.Connector, c.Line, c.ImChatID, c.ImMsgID, waID, c.ChatExtID); err != nil {
		log.Warn("confirmacao de entrega no bitrix falhou", zap.String("wa_id", waID), zap.Error(err))
		return
	}
	log.Info("mensagem do operador entregue no celular do cliente — confirmado no bitrix",
		zap.String("wa_id", waID), zap.String("im_msg_id", c.ImMsgID))
}

// tratarRecibo aplica os recibos que o celular do cliente devolve.
//
// So' conta recibo do DESTINATARIO: entregue, lido e audio ouvido. Os demais
// tipos (read-self, sender, retry...) vem dos nossos proprios aparelhos ou do
// protocolo e nao dizem nada sobre o cliente ter recebido.
func tratarRecibo(bitrixClient *bitrix.Client, repo *db.Repository, q *queue.Queue, log *zap.Logger) whatsapp.ReceiptHandler {
	return func(sessionJID string, evt *events.Receipt) {
		if evt.IsFromMe {
			return
		}
		var status db.MessageStatus
		switch evt.Type {
		case types.ReceiptTypeDelivered:
			status = db.MsgDelivered
		case types.ReceiptTypeRead, types.ReceiptTypePlayed:
			status = db.MsgRead
		default:
			return
		}
		go semPanico(log, "recibo de entrega", func() {
			ctx := context.Background()
			for _, id := range evt.MessageIDs {
				if err := repo.AvancarStatusSaida(ctx, id, status); err != nil {
					log.Warn("recibo: status da mensagem nao atualizado", zap.String("wa_id", id), zap.Error(err))
				}
				pend, err := q.ReciboChegou(ctx, id)
				if err != nil {
					log.Warn("recibo: falha ao consultar confirmacao pendente", zap.String("wa_id", id), zap.Error(err))
					continue
				}
				if pend != nil {
					confirmarEntregaNoBitrix(ctx, bitrixClient, repo, log, id, *pend)
				}
			}
		})
	}
}

// avisarOperadorDesistencia transforma a bolha do operador no aviso de falha
// quando a fila desiste da mensagem.
//
// Toda falha tambem vai pro historico, com o motivo: e' por ali que quem
// mandou pela aba UC Talk fica sabendo — la' a mensagem aparecia na hora e
// sumia sem explicacao. Mensagem do robo de automacao nao tem tela; fica o
// historico e o log.
func avisarOperadorDesistencia(bitrixClient *bitrix.Client, repo *db.Repository, log *zap.Logger) func(job *queue.OutboundJob) {
	return func(job *queue.OutboundJob) {
		motivo := job.LastError
		if motivo == "" {
			motivo = "erro desconhecido"
		}
		motivoTecnico := motivo
		motivo = motivoParaOperador(motivo)
		aviso := "a mensagem não foi entregue no WhatsApp. Motivo: " + motivo
		log.Warn("fila desistiu da mensagem — avisando o operador",
			zap.String("job_id", job.ID), zap.String("para", job.ToJID), zap.String("motivo", motivoTecnico))
		ctx := context.Background()
		registrarFalhaNoHistorico(ctx, repo, log, job, motivo)
		if whatsapp.IsCloudJID(job.SessionJID) {
			notifyOperatorError(ctx, bitrixClient, repo, log, job, aviso)
			return
		}
		notifyQROperatorError(ctx, bitrixClient, repo, log, job, aviso)
	}
}

// registrarFalhaNoHistorico grava a mensagem que nao saiu, com status
// "failed" e o motivo, pra aba UC Talk mostrar com ❌.
func registrarFalhaNoHistorico(ctx context.Context, repo *db.Repository, log *zap.Logger, job *queue.OutboundJob, motivo string) {
	conteudo := job.Text
	if conteudo == "" && job.FileName != "" {
		conteudo = "📎 " + job.FileName
	}
	agora := time.Now()
	waID := "falha-" + job.ID // a mensagem nunca ganhou ID do WhatsApp
	msg := &db.Message{
		ID:          uuid.New(),
		WAMessageID: waID,
		SessionID:   sessaoParaBanco(ctx, repo, log, job.SessionJID),
		FromJID:     stripDeviceSuffix(job.SessionJID),
		ToJID:       destinoParaBanco(ctx, repo, log, job.ToJID),
		AuthorName:  job.OperatorName,
		Direction:   db.DirOutbound,
		MessageType: db.MsgTypeText,
		Content:     conteudo,
		Status:      db.MsgFailed,
		SentAt:      &agora,
	}
	if err := repo.InsertMessage(ctx, msg); err != nil {
		log.Warn("falha nao gravada no historico", zap.String("job_id", job.ID), zap.Error(err))
		return
	}
	// InsertMessage nao grava error_msg; o motivo entra aqui.
	_ = repo.UpdateMessageStatus(ctx, waID, db.MsgFailed, motivo)
}

// destinoParaBanco: o JID do destinatario como o historico guarda — telefone,
// nao LID, senao a aba do CRM (que busca pelo telefone) nao acha a mensagem.
func destinoParaBanco(ctx context.Context, repo *db.Repository, log *zap.Logger, toJID string) string {
	jid := stripDeviceSuffix(toJID)
	if !strings.HasSuffix(jid, "@lid") {
		return jid
	}
	if phone, err := repo.GetPhoneByLID(ctx, jid); err == nil && phone != "" {
		log.Info("outbound: resolved @lid via lid_phone_map", zap.String("lid", jid), zap.String("phone", phone))
		return phone + "@s.whatsapp.net"
	}
	// Fallback: contact_mapping (caso lid_phone_map ainda esteja vazio)
	if contact, err := repo.GetContactByWAJID(ctx, jid); err == nil && contact.WAPhone != "" {
		return contact.WAPhone + "@s.whatsapp.net"
	}
	return jid
}

// sessaoParaBanco: session_id precisa ser gravado — a tela de saude conta
// mensagem por tenant com JOIN em whatsapp_sessions, entao linha de saida sem
// session_id some da contagem ("saida_24h" aparecia 0 com envio
// funcionando). Best-effort: sem sessao, grava sem.
func sessaoParaBanco(ctx context.Context, repo *db.Repository, log *zap.Logger, sessionJID string) *uuid.UUID {
	sess, err := repo.GetSessionByJIDTolerante(ctx, sessionJID)
	if err == nil && sess != nil {
		return &sess.ID
	}
	if err != nil {
		log.Warn("outbound: sessao nao encontrada para gravar session_id",
			zap.String("session_jid", sessionJID), zap.Error(err))
	}
	return nil
}

// motivoParaOperador traduz os motivos de falha que o operador consegue
// resolver. O texto tecnico continua no log.
//
// "no LID found ... from server": o WhatsApp exige o identificador interno
// (LID) pra mandar mensagem 1:1, e o servidor dele nao devolveu nenhum pro
// numero — o que, na pratica, e' numero sem conta no WhatsApp. O sistema ja'
// testou as formas com e sem o 9 antes de chegar aqui.
func motivoParaOperador(motivo string) string {
	t := strings.ToLower(motivo)
	switch {
	case strings.Contains(t, "no lid found"),
		strings.Contains(t, "nao esta no whatsapp"),
		strings.Contains(t, "is not on whatsapp"):
		return "o WhatsApp não reconhece este número (testado com e sem o 9). Confira o telefone do contato."
	case strings.Contains(t, "session not found"):
		return "o número de WhatsApp da empresa está desconectado."
	}
	return motivo
}
