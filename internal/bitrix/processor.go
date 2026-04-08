package bitrix

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/queue"
	"go.uber.org/zap"
)

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

// ProcessInbound entrega uma mensagem do WhatsApp no Bitrix24.
func (p *Processor) ProcessInbound(ctx context.Context, job *queue.InboundJob) error {
	// 1. Garante que existe um mapeamento contato ↔ bitrix
	contact, err := p.ensureContact(ctx, job)
	if err != nil {
		_ = p.repo.UpdateMessageStatus(ctx, job.MessageID, db.MsgFailed, err.Error())
		return fmt.Errorf("ensure contact: %w", err)
	}

	// 2. Envia mensagem para o Open Lines (apenas texto por enquanto)
	text := job.Text
	if text == "" {
		text = "[" + job.MessageType + "]"
	}
	if err := p.client.SendMessage(ctx, contact.BitrixID, text); err != nil {
		_ = p.repo.UpdateMessageStatus(ctx, job.MessageID, db.MsgFailed, err.Error())
		return fmt.Errorf("send to bitrix: %w", err)
	}

	// 3. Marca como entregue no banco
	_ = p.repo.UpdateMessageStatus(ctx, job.MessageID, db.MsgDelivered, "")

	p.log.Info("inbound delivered to bitrix",
		zap.String("from", job.FromPhone),
		zap.String("type", job.MessageType),
		zap.Int64("bitrix_session", contact.BitrixID))
	return nil
}

// ensureContact garante que temos um lead e uma sessão de chat no Bitrix para este contato.
func (p *Processor) ensureContact(ctx context.Context, job *queue.InboundJob) (*db.ContactMapping, error) {
	existing, err := p.repo.GetContactByJID(ctx, job.FromJID, job.SessionID)
	if err == nil {
		return existing, nil
	}

	// Abre (ou recupera) uma sessão no Open Lines
	sessionID, err := p.client.OpenChatSession(ctx, p.lineID, job.FromPhone, job.FromName, "")
	if err != nil {
		// Fallback: cria um Lead no CRM
		leadID, err2 := p.client.FindOrCreateLead(ctx, job.FromPhone, job.FromName)
		if err2 != nil {
			return nil, fmt.Errorf("open chat: %v | create lead: %w", err, err2)
		}
		sessionID = leadID
	}

	contact := &db.ContactMapping{
		ID:           uuid.New(),
		WAJID:        job.FromJID,
		WAPhone:      job.FromPhone,
		WAName:       job.FromName,
		BitrixEntity: "openlines",
		BitrixID:     sessionID,
		SessionID:    &job.SessionID,
	}

	if err := p.repo.UpsertContact(ctx, contact); err != nil {
		return nil, err
	}
	return contact, nil
}
