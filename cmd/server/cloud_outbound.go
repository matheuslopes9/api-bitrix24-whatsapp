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
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/bitrix"
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
	bitrixClient *bitrix.Client,
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
		if fileName == "" {
			fileName = "file"
		}
		mimeOriginal := fileMime
		// Bitrix as vezes envia mime "application/octet-stream" generico para
		// arquivos de video/audio. A Meta Cloud API rejeita esse mime.
		// Tenta inferir pelo nome do arquivo (extensao).
		if fileMime == "" || fileMime == "application/octet-stream" {
			if guessed := mimeFromFileName(fileName); guessed != "" {
				fileMime = guessed
			} else if guessed := mimeFromMagic(fileData); guessed != "" {
				fileMime = guessed
			} else {
				fileMime = "application/octet-stream"
			}
		}
		log.Info("cloud outbound: sending file",
			zap.String("session_jid", job.SessionJID),
			zap.String("file_name", fileName),
			zap.String("mime_original", mimeOriginal),
			zap.String("mime_resolved", fileMime),
			zap.Int("size", len(fileData)),
		)
		waID, err = cloudMgr.SendDocument(c, job.SessionJID, toPhone, fileData, fileMime, fileName, "")
	} else {
		waID, err = cloudMgr.SendText(c, job.SessionJID, toPhone, job.Text)
	}

	if err != nil {
		errStr := err.Error()

		// Tamanho excede limite Meta (100MB) — não tem como enviar.
		// Avisa o operador e não tenta retry.
		if strings.Contains(errStr, "status 413") {
			metrics.MessagesFailed.Inc()
			log.Error("cloud outbound: file too large",
				zap.String("file_name", fileName), zap.Int("size", len(fileData)))
			aviso := fmt.Sprintf(
				"⚠️ O arquivo *%s* (%.1f MB) é muito grande. "+
					"O WhatsApp Business API limita arquivos a 100 MB. "+
					"Compacte ou divida o arquivo antes de enviar.",
				fileName, float64(len(fileData))/(1024*1024),
			)
			_, _ = cloudMgr.SendText(c, job.SessionJID, toPhone, aviso)
			return nil
		}

		// Tipo não suportado — TENTA NOVAMENTE como application/pdf (que está
		// na lista oficial da Meta). O WhatsApp usa o filename para identificar
		// o tipo — o cliente recebe o arquivo com o nome correto e a extensão
		// original, e ao baixar abre normalmente. Mesma estratégia que clients
		// como Wassenger / outros gateways usam para "qualquer arquivo".
		if strings.Contains(errStr, "Param file must be a file with one of the following types") ||
			strings.Contains(errStr, "(#100)") {
			log.Info("cloud outbound: retrying as application/pdf (force document)",
				zap.String("file_name", fileName),
				zap.String("mime_rejected", fileMime))
			waID, err = cloudMgr.SendDocument(c, job.SessionJID, toPhone, fileData,
				"application/pdf", fileName, "document")
			if err == nil {
				log.Info("cloud outbound: sent as document fallback",
					zap.String("file_name", fileName), zap.String("wa_id", waID))
				// Continua para o save no banco / delivery (segue o fluxo normal abaixo)
			} else {
				metrics.MessagesFailed.Inc()
				log.Error("cloud outbound: fallback as document also failed",
					zap.String("file_name", fileName), zap.Error(err))
				aviso := fmt.Sprintf(
					"⚠️ O arquivo *%s* não pôde ser enviado pelo WhatsApp Business API. "+
						"Tente enviar pelo WhatsApp QR code, ou compacte como ZIP.",
					fileName,
				)
				_, _ = cloudMgr.SendText(c, job.SessionJID, toPhone, aviso)
				return nil
			}
		} else {
			metrics.MessagesFailed.Inc()
			log.Error("cloud outbound send failed",
				zap.String("session_jid", job.SessionJID),
				zap.String("to_phone", toPhone),
				zap.Error(err),
			)
			return err
		}
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

	// Confirma delivery ao Bitrix para parar o spinner na mensagem do operador
	// (mesma lógica do worker whatsmeow). O Bitrix precisa receber o
	// imconnector.send.status.delivery para marcar a msg como "Enviada ✓".
	if job.BitrixConnector != "" && job.BitrixImMsgID != "" {
		log.Info("cloud outbound delivery: confirming",
			zap.String("connector", job.BitrixConnector),
			zap.String("im_msg_id", job.BitrixImMsgID),
			zap.String("wa_id", waID),
		)
		go func() {
			bgCtx := context.Background()
			acct, err := repo.GetBitrixAccountByJID(bgCtx, job.SessionJID)
			if err != nil {
				log.Warn("cloud outbound delivery: bitrix account not found",
					zap.String("session", job.SessionJID), zap.Error(err))
				return
			}
			creds := bitrix.TenantCreds{
				Domain:       acct.Domain,
				ClientID:     acct.ClientID,
				ClientSecret: acct.ClientSecret,
				RedirectURI:  acct.RedirectURI,
			}
			if err := bitrixClient.ConnectorSetOutboundDelivery(
				bgCtx, creds,
				job.BitrixConnector,
				job.BitrixLine,
				job.BitrixImChatID,
				job.BitrixImMsgID,
				waID,
				job.BitrixChatExtID,
			); err != nil {
				log.Warn("cloud outbound delivery confirmation failed", zap.Error(err))
			}
		}()
	} else {
		log.Warn("cloud outbound delivery: skipped (missing connector or msg_id)",
			zap.String("connector", job.BitrixConnector),
			zap.String("im_msg_id", job.BitrixImMsgID),
		)
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

// mimeFromMagic detecta o MIME pelos primeiros bytes do arquivo.
// Fallback usado quando nem o MIME do Bitrix nem a extensão do nome
// conseguem identificar o tipo. Cobre os formatos aceitos pela
// Meta Cloud API (vídeo, áudio, imagem, PDF).
func mimeFromMagic(data []byte) string {
	if len(data) < 12 {
		return ""
	}
	// MP4 / 3GPP: bytes 4-7 = "ftyp", depois um identificador de marca
	if string(data[4:8]) == "ftyp" {
		brand := string(data[8:12])
		switch brand {
		case "3gp4", "3gp5", "3gp6", "3ge6", "3ge7", "3gg6":
			return "video/3gpp"
		default:
			// isom, mp42, M4V, M4A, qt etc.
			if brand == "M4A " || brand == "M4B " {
				return "audio/mp4"
			}
			return "video/mp4"
		}
	}
	// JPEG: FF D8 FF
	if data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "image/jpeg"
	}
	// PNG: 89 50 4E 47 0D 0A 1A 0A
	if data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 {
		return "image/png"
	}
	// WEBP: RIFF....WEBP
	if string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "image/webp"
	}
	// PDF: 25 50 44 46 ("%PDF")
	if string(data[0:4]) == "%PDF" {
		return "application/pdf"
	}
	// MP3 (ID3 tag): "ID3"
	if string(data[0:3]) == "ID3" {
		return "audio/mpeg"
	}
	// MP3 (frame sync): FF Fx
	if data[0] == 0xFF && (data[1]&0xE0) == 0xE0 {
		return "audio/mpeg"
	}
	// OGG: "OggS"
	if string(data[0:4]) == "OggS" {
		return "audio/ogg"
	}
	return ""
}

// mimeFromFileName infere o MIME pela extensão do arquivo.
// Necessário porque o Bitrix às vezes manda "application/octet-stream"
// genérico para arquivos cujo MIME real a Meta Cloud API exige específico.
// Lista alinhada com tipos aceitos pela Meta:
// https://developers.facebook.com/docs/whatsapp/cloud-api/reference/media#supported-media-types
func mimeFromFileName(name string) string {
	lower := strings.ToLower(name)
	dot := strings.LastIndex(lower, ".")
	if dot < 0 {
		return ""
	}
	switch lower[dot:] {
	// Vídeos
	case ".mp4":
		return "video/mp4"
	case ".3gp", ".3gpp":
		return "video/3gpp"
	// Áudios
	case ".mp3":
		return "audio/mpeg"
	case ".aac":
		return "audio/aac"
	case ".m4a":
		return "audio/mp4"
	case ".amr":
		return "audio/amr"
	case ".ogg", ".opus":
		return "audio/ogg"
	case ".wav":
		return "audio/wav"
	// Imagens
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".bmp":
		return "image/bmp"
	// Documentos Office (oficialmente aceitos pela Meta)
	case ".pdf":
		return "application/pdf"
	case ".doc":
		return "application/msword"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".xls":
		return "application/vnd.ms-excel"
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".ppt":
		return "application/vnd.ms-powerpoint"
	case ".pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	case ".txt":
		return "text/plain"
	case ".csv":
		return "text/csv"
	case ".rtf":
		return "application/rtf"
	// Arquivos compactados
	case ".zip":
		return "application/zip"
	case ".rar":
		return "application/vnd.rar"
	case ".7z":
		return "application/x-7z-compressed"
	case ".tar":
		return "application/x-tar"
	case ".gz":
		return "application/gzip"
	// Executáveis / instaladores
	case ".msi":
		return "application/x-msi"
	case ".exe":
		return "application/x-msdownload"
	// Outros (provavelmente serão rejeitados pela Meta — fallback genérico)
	case ".bak":
		return "application/octet-stream"
	case ".json":
		return "application/json"
	case ".xml":
		return "application/xml"
	case ".html", ".htm":
		return "text/html"
	}
	return ""
}

// fetchHTTPBytes faz GET simples — usado pelo cloud_outbound se downloadURL
// estiver em outro escopo. Mantido apenas para garantir que tenhamos um
// helper local (downloadURL já existe em main.go com a mesma assinatura).
var _ = http.Get
var _ io.Reader
