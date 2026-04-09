package bitrix

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/queue"
	"go.uber.org/zap"
)

// normalizeChatID remove o device part do JID para garantir consistência no Bitrix.
// "127586399207476:47@lid" → "127586399207476@lid"
// "5511999999999@s.whatsapp.net" → mantém como está
func normalizeChatID(jid string) string {
	if idx := strings.Index(jid, ":"); idx != -1 {
		if at := strings.Index(jid, "@"); at != -1 {
			return jid[:idx] + jid[at:]
		}
	}
	return jid
}

const connectorID = "whatsapp_uc"

// Processor implementa a lógica de negócio: inbound WA → Bitrix, outbound Bitrix → WA.
type Processor struct {
	client *Client
	repo   *db.Repository
	log    *zap.Logger
	lineID int // ID do Open Lines no Bitrix24
}

func NewProcessor(client *Client, repo *db.Repository, log *zap.Logger, lineID int) *Processor {
	return &Processor{client: client, repo: repo, log: log, lineID: lineID}
}

// ProcessInbound entrega uma mensagem do WhatsApp no Bitrix24 Contact Center.
func (p *Processor) ProcessInbound(ctx context.Context, job *queue.InboundJob) error {
	// 1. Garante que existe um mapeamento contato ↔ bitrix
	contact, err := p.ensureContact(ctx, job)
	if err != nil {
		_ = p.repo.UpdateMessageStatus(ctx, job.MessageID, db.MsgFailed, err.Error())
		return fmt.Errorf("ensure contact: %w", err)
	}

	// 2. Monta a mensagem para o connector
	text := job.Text
	msgBody := ConnectorMsgBody{ID: job.MessageID, Text: text}

	// Anexa mídia se disponível — faz upload para o Bitrix disk primeiro
	if len(job.MediaData) > 0 && job.MediaName != "" {
		_, downloadURL, err := p.client.UploadToDisk(ctx, job.MediaName, job.MediaData)
		if err != nil {
			p.log.Warn("upload media to disk failed, sending text only",
				zap.String("file", job.MediaName), zap.Error(err))
			if text == "" {
				msgBody.Text = "[" + job.MediaName + "]"
			}
		} else {
			msgBody.Files = []ConnectorFile{{Name: job.MediaName, URL: downloadURL}}
			p.log.Info("media uploaded to disk", zap.String("file", job.MediaName), zap.String("url", downloadURL))
		}
	} else if text == "" {
		msgBody.Text = "[" + job.MessageType + "]"
	}

	// Normaliza o chat ID: remove device part (:47) para garantir consistência.
	// O Bitrix usa chat.ID como chave de conversa — JIDs com e sem device part
	// criam sessões duplicadas. Usamos sempre "user@domain" sem o device.
	chatExtID := normalizeChatID(job.FromJID)

	msg := ConnectorMessage{
		User: ConnectorUser{
			ID:    chatExtID,
			Name:  job.FromName,
			Phone: job.FromPhone,
		},
		Message: msgBody,
		Chat: ConnectorChat{
			ID: chatExtID,
		},
	}

	// 3. Envia ao Contact Center
	chatID, err := p.client.ConnectorSendMessage(ctx, connectorID, p.lineID, msg)
	if err != nil {
		_ = p.repo.UpdateMessageStatus(ctx, job.MessageID, db.MsgFailed, err.Error())
		return fmt.Errorf("send to contact center: %w", err)
	}

	// 4. Atualiza o chat_id no mapeamento (para future replies do operador)
	if chatID != "" && chatID != "<nil>" && chatID != "0" {
		contact.BitrixChatID = chatID
		_ = p.repo.UpsertContact(ctx, contact)
	}

	// 5. Confirma entrega da mensagem do cliente ao Bitrix (para parar o spinner)
	p.log.Info("calling set delivery", zap.String("msg_id", job.MessageID), zap.Int("line", p.lineID))
	if err := p.client.ConnectorSetDelivery(ctx, connectorID, p.lineID, job.MessageID); err != nil {
		p.log.Warn("set delivery status failed", zap.String("msg_id", job.MessageID), zap.Error(err))
	} else {
		p.log.Info("set delivery ok", zap.String("msg_id", job.MessageID))
	}

	// 6. Marca como entregue no banco
	_ = p.repo.UpdateMessageStatus(ctx, job.MessageID, db.MsgDelivered, "")

	p.log.Info("inbound delivered to contact center",
		zap.String("from", job.FromPhone),
		zap.String("type", job.MessageType),
		zap.String("chat_id", chatID))
	return nil
}

// ensureContact garante que temos um mapeamento para este contato.
func (p *Processor) ensureContact(ctx context.Context, job *queue.InboundJob) (*db.ContactMapping, error) {
	existing, err := p.repo.GetContactByJID(ctx, job.FromJID, job.SessionID)
	if err == nil {
		return existing, nil
	}

	contact := &db.ContactMapping{
		ID:           uuid.New(),
		WAJID:        job.FromJID,
		WAPhone:      job.FromPhone,
		WAName:       job.FromName,
		BitrixEntity: "chat",
		BitrixID:     0,
		SessionID:    &job.SessionID,
	}

	if err := p.repo.UpsertContact(ctx, contact); err != nil {
		return nil, err
	}
	return contact, nil
}
