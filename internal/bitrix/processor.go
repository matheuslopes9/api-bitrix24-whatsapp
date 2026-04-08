package bitrix

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/queue"
	"go.uber.org/zap"
)

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
	if text == "" {
		text = "[" + job.MessageType + "]"
	}

	msg := ConnectorMessage{
		User: ConnectorUser{
			ID:    job.FromJID,
			Name:  job.FromName,
			Phone: job.FromPhone,
		},
		Message: ConnectorMsgBody{
			ID:   job.MessageID,
			Text: text,
		},
		Chat: ConnectorChat{
			ID: job.FromJID, // usa o JID como ID estável do chat externo
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

	// 5. Marca como entregue no banco
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
