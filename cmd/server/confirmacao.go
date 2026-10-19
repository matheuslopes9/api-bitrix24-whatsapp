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
// Mensagem sem bolha no Bitrix (aba do CRM, robo de automacao) nao tem onde
// avisar: fica so' o log, como antes.
func avisarOperadorDesistencia(bitrixClient *bitrix.Client, repo *db.Repository, log *zap.Logger) func(job *queue.OutboundJob) {
	return func(job *queue.OutboundJob) {
		motivo := job.LastError
		if motivo == "" {
			motivo = "erro desconhecido"
		}
		aviso := "a mensagem não foi entregue no WhatsApp. Motivo: " + motivo
		log.Warn("fila desistiu da mensagem — avisando o operador",
			zap.String("job_id", job.ID), zap.String("para", job.ToJID), zap.String("motivo", motivo))
		ctx := context.Background()
		if whatsapp.IsCloudJID(job.SessionJID) {
			notifyOperatorError(ctx, bitrixClient, repo, log, job, aviso)
			return
		}
		notifyQROperatorError(ctx, bitrixClient, repo, log, job, aviso)
	}
}
