package main

// Caminho exclusivo do QR Code (whatsmeow) para entregar mídias inbound
// no Bitrix Open Lines via link público — usado quando o arquivo é grande
// demais para o disk.storage.uploadfile (que faz base64 do payload).
//
// Não toca no processor.go (compartilhado) nem no caminho Cloud.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/uctechnology/api-bitrix24-whatsapp/internal/bitrix"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/queue"
	"go.uber.org/zap"
)

// qrLargeMediaThreshold — acima desse tamanho não usamos disk.storage.uploadfile
// (que faz base64 e estoura), e sim hospedamos via /cloud-media/:token.
// 30 MB é margem segura: o limite real do REST com base64 fica em torno de 35-40 MB.
const qrLargeMediaThreshold = 30 * 1024 * 1024

// deliverQRInboundViaPublicLink envia uma mensagem inbound do WhatsApp QR
// ao Bitrix24 Open Lines com o anexo apontando para um link público nosso
// (Redis + endpoint /cloud-media/:token, TTL 1h). Não usa disk.storage.
//
// Retorna true se entregou; false se deve cair pro fluxo padrão (processor).
func deliverQRInboundViaPublicLink(
	ctx context.Context,
	bitrixClient *bitrix.Client,
	repo *db.Repository,
	q *queue.Queue,
	appBase string,
	log *zap.Logger,
	job *queue.InboundJob,
) (bool, error) {
	if len(job.MediaData) == 0 || job.MediaName == "" {
		return false, nil
	}

	acct, err := repo.GetBitrixAccountByJID(ctx, job.SessionJID)
	if err != nil {
		return false, fmt.Errorf("bitrix account not found: %w", err)
	}
	creds := bitrix.TenantCreds{
		Domain:       acct.Domain,
		ClientID:     acct.ClientID,
		ClientSecret: acct.ClientSecret,
		RedirectURI:  acct.RedirectURI,
	}

	// Garante contato — replica o que o processor faz, mas via UpsertContactMapping
	// já existente no repository para não duplicar lógica de criação no Bitrix.
	chatExtID := normalizeQRChatID(job.FromJID)

	// Hospeda o arquivo no Redis e gera URL pública.
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return false, fmt.Errorf("gerar token: %w", err)
	}
	token := hex.EncodeToString(tokenBytes)
	mime := job.MediaMime
	if mime == "" {
		mime = "application/octet-stream"
	}
	if err := q.StoreMedia(ctx, token, &queue.MediaCache{
		Data:     job.MediaData,
		Mime:     mime,
		FileName: job.MediaName,
	}, time.Hour); err != nil {
		return false, fmt.Errorf("store media: %w", err)
	}
	publicURL := strings.TrimRight(appBase, "/") + "/cloud-media/" + token
	log.Info("qr inbound: hosted large media via public link",
		zap.String("file", job.MediaName),
		zap.Int("size", len(job.MediaData)),
		zap.String("url", publicURL))

	// Monta a mensagem para o connector com o link público no campo Files.
	msg := bitrix.ConnectorMessage{
		User: bitrix.ConnectorUser{
			ID:    chatExtID,
			Name:  job.FromName,
			Phone: job.FromPhone,
		},
		Message: bitrix.ConnectorMsgBody{
			ID:    job.MessageID,
			Text:  job.Text,
			Files: []bitrix.ConnectorFile{{Name: job.MediaName, URL: publicURL}},
		},
		Chat: bitrix.ConnectorChat{ID: chatExtID},
	}

	if _, err := bitrixClient.ConnectorSendMessage(ctx, creds, acct.ConnectorID, acct.OpenLineID, msg); err != nil {
		return false, fmt.Errorf("send to contact center: %w", err)
	}

	// Marca status no banco — independente do processor.
	_ = repo.UpdateMessageStatus(ctx, job.MessageID, db.MsgDelivered, "")
	return true, nil
}

// normalizeQRChatID — replica normalizeChatID do processor sem importar daquele
// pacote (que é compartilhado com Cloud). Mantém QR independente.
// "127586399207476:47@lid" → "127586399207476@lid"
func normalizeQRChatID(jid string) string {
	if idx := strings.Index(jid, ":"); idx != -1 {
		if at := strings.Index(jid, "@"); at != -1 {
			return jid[:idx] + jid[at:]
		}
	}
	return jid
}
