package main

// Caminho exclusivo do QR Code (whatsmeow) para tratar erros outbound —
// mantido separado do equivalente Cloud (cloud_outbound.go::notifyOperatorError)
// para que cada modo evolua de forma independente.

import (
	"context"

	"github.com/uctechnology/api-bitrix24-whatsapp/internal/bitrix"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/queue"
	"go.uber.org/zap"
)

// notifyQROperatorError marca uma mensagem outbound (modo QR) como FALHA
// no Bitrix Open Lines. Usado quando o operador tenta enviar arquivo
// > 2 GB via whatsmeow — apaga a bolha original e injeta uma msg de
// sistema explicando o motivo, sem enviar nada ao cliente real.
func notifyQROperatorError(
	ctx context.Context,
	bitrixClient *bitrix.Client,
	repo *db.Repository,
	log *zap.Logger,
	job *queue.OutboundJob,
	errorMsg string,
) {
	if job.BitrixConnector == "" || job.BitrixImMsgID == "" {
		log.Warn("notifyQROperatorError: skip — missing connector or im_msg_id",
			zap.String("connector", job.BitrixConnector),
			zap.String("im_msg_id", job.BitrixImMsgID))
		return
	}
	go func() {
		bgCtx := context.Background()
		acct, err := repo.GetBitrixAccountByJID(bgCtx, job.SessionJID)
		if err != nil {
			log.Warn("notifyQROperatorError: bitrix account not found",
				zap.String("session", job.SessionJID), zap.Error(err))
			return
		}
		creds := bitrix.TenantCreds{
			Domain:       acct.Domain,
			ClientID:     acct.ClientID,
			ClientSecret: acct.ClientSecret,
			RedirectURI:  acct.RedirectURI,
		}
		if err := bitrixClient.ConnectorSetOutboundError(
			bgCtx, creds,
			job.BitrixConnector,
			job.BitrixLine,
			job.BitrixImChatID,
			job.BitrixImMsgID,
			job.BitrixChatExtID,
			errorMsg,
		); err != nil {
			log.Warn("notifyQROperatorError: failed to mark error",
				zap.Error(err), zap.String("error_msg", errorMsg))
		} else {
			log.Info("notifyQROperatorError: msg marked as failed in Bitrix",
				zap.String("error_msg", errorMsg))
		}
	}()
}
