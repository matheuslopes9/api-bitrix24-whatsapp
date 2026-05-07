package main

// handleCloudOutbound processa OutboundJob para sessões Cloud API (Meta).
// Caminho paralelo ao worker whatsmeow — não interfere no fluxo QR.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/queue"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/telemetry"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/whatsapp"
	"go.uber.org/zap"
	"io"
)

func handleCloudOutbound(
	c context.Context,
	cloudMgr *whatsapp.CloudManager,
	repo *db.Repository,
	log *zap.Logger,
	metrics *telemetry.Metrics,
	job *queue.OutboundJob,
) error {
	// Resolve o telefone do destinatário a partir do ToJID.
	// ToJID pode vir como "5519999999@s.whatsapp.net", "phone@s.whatsapp.net" ou só dígitos.
	toPhone := stripJIDToPhone(job.ToJID)
	if toPhone == "" {
		metrics.MessagesFailed.Inc()
		return fmt.Errorf("cloud outbound: to_jid vazio ou inválido: %s", job.ToJID)
	}

	// Resolve dados do arquivo (FileURL do Bitrix ou MediaURL data: URI)
	var fileData []byte
	fileMime := job.FileMime
	fileName := job.FileName

	if job.FileURL != "" {
		var dlErr error
		fileData, dlErr = downloadURL(job.FileURL)
		if dlErr != nil {
			metrics.MessagesFailed.Inc()
			return fmt.Errorf("cloud outbound: download file: %w", dlErr)
		}
	} else if strings.HasPrefix(job.MediaURL, "data:") {
		fileData, fileMime = decodeDataURI(job.MediaURL)
	}

	// Pequeno delay simulando "digitando" — Cloud API não tem typing indicator,
	// então só damos um pacing entre msgs para reduzir risco de rate limit.
	time.Sleep(800 * time.Millisecond)

	var waID string
	var err error
	if len(fileData) > 0 {
		if fileMime == "" {
			fileMime = "application/octet-stream"
		}
		if fileName == "" {
			fileName = "file"
		}
		waID, err = cloudMgr.SendDocument(c, job.SessionJID, toPhone, fileData, fileMime, fileName, "")
	} else {
		waID, err = cloudMgr.SendText(c, job.SessionJID, toPhone, job.Text)
	}

	if err != nil {
		metrics.MessagesFailed.Inc()
		log.Error("cloud outbound send failed",
			zap.String("session_jid", job.SessionJID),
			zap.String("to_phone", toPhone),
			zap.Error(err),
		)
		return err
	}

	// Salva no banco (mesmo padrão do whatsmeow path).
	msgType := db.MsgTypeText
	if len(fileData) > 0 {
		msgType = db.MsgTypeDocument
	}
	now := time.Now()
	outMsg := &db.Message{
		ID:          uuid.New(),
		WAMessageID: waID,
		FromJID:     job.SessionJID,
		ToJID:       toPhone + "@s.whatsapp.net",
		AuthorName:  job.OperatorName,
		Direction:   db.DirOutbound,
		MessageType: msgType,
		Content:     job.Text,
		MediaMime:   fileMime,
		Status:      db.MsgDelivered,
		SentAt:      &now,
	}
	if err := repo.InsertMessage(c, outMsg); err != nil {
		log.Warn("cloud outbound: insert message failed",
			zap.String("wa_id", waID), zap.Error(err))
	}

	log.Info("cloud outbound sent",
		zap.String("to", toPhone),
		zap.String("wa_id", waID),
		zap.String("session", job.SessionJID),
	)
	return nil
}

// stripJIDToPhone extrai apenas os dígitos do telefone de um JID.
// "5519987717792@s.whatsapp.net" -> "5519987717792"
// "+55 (19) 98771-7792" -> "5519987717792"
func stripJIDToPhone(jid string) string {
	if at := strings.Index(jid, "@"); at != -1 {
		jid = jid[:at]
	}
	if colon := strings.Index(jid, ":"); colon != -1 {
		jid = jid[:colon]
	}
	out := make([]byte, 0, len(jid))
	for _, ch := range jid {
		if ch >= '0' && ch <= '9' {
			out = append(out, byte(ch))
		}
	}
	return string(out)
}

// fetchHTTPBytes faz GET simples — usado pelo cloud_outbound se downloadURL
// estiver em outro escopo. Mantido apenas para garantir que tenhamos um
// helper local (downloadURL já existe em main.go com a mesma assinatura).
var _ = http.Get
var _ io.Reader
