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
func normalizeChatID(jid string) string {
	if idx := strings.Index(jid, ":"); idx != -1 {
		if at := strings.Index(jid, "@"); at != -1 {
			return jid[:idx] + jid[at:]
		}
	}
	return jid
}

// Processor implementa a lógica de negócio: inbound WA → Bitrix, outbound Bitrix → WA.
// É multi-tenant: busca a BitrixAccount vinculada ao sessionJID de cada job.
type Processor struct {
	client *Client
	repo   *db.Repository
	log    *zap.Logger
}

func NewProcessor(client *Client, repo *db.Repository, log *zap.Logger) *Processor {
	return &Processor{client: client, repo: repo, log: log}
}

// ProcessInbound entrega uma mensagem do WhatsApp no Bitrix24 Contact Center.
func (p *Processor) ProcessInbound(ctx context.Context, job *queue.InboundJob) error {
	p.log.Info("ProcessInbound called",
		zap.String("session_jid", job.SessionJID),
		zap.String("from", job.FromJID),
		zap.String("msg_id", job.MessageID),
		zap.String("text", job.Text),
		zap.String("type", job.MessageType),
	)

	// 1. Busca a conta Bitrix vinculada à sessão WA
	acct, err := p.repo.GetBitrixAccountByJID(ctx, job.SessionJID)
	if err != nil {
		p.log.Error("bitrix account not found",
			zap.String("session_jid", job.SessionJID),
			zap.Error(err),
		)
		_ = p.repo.UpdateMessageStatus(ctx, job.MessageID, db.MsgFailed, "bitrix account not configured")
		return fmt.Errorf("bitrix account not found for session %s: %w", job.SessionJID, err)
	}

	p.log.Info("bitrix account found",
		zap.String("domain", acct.Domain),
		zap.Int("open_line_id", acct.OpenLineID),
		zap.String("connector_id", acct.ConnectorID),
	)

	creds := TenantCreds{
		Domain:       acct.Domain,
		ClientID:     acct.ClientID,
		ClientSecret: acct.ClientSecret,
		RedirectURI:  acct.RedirectURI,
	}

	// 2. Garante que existe um mapeamento contato ↔ bitrix
	contact, err := p.ensureContact(ctx, job)
	if err != nil {
		_ = p.repo.UpdateMessageStatus(ctx, job.MessageID, db.MsgFailed, err.Error())
		return fmt.Errorf("ensure contact: %w", err)
	}

	// 3. Monta a mensagem para o connector
	msgBody := ConnectorMsgBody{ID: job.MessageID, Text: job.Text}

	// Anexa mídia se disponível
	if len(job.MediaData) > 0 && job.MediaName != "" {
		_, downloadURL, err := p.client.UploadToDisk(ctx, creds, job.MediaName, job.MediaData)
		if err != nil {
			p.log.Warn("upload media to disk failed, sending text only",
				zap.String("file", job.MediaName), zap.Error(err))
			if job.Text == "" {
				msgBody.Text = "[" + job.MediaName + "]"
			}
		} else {
			msgBody.Files = []ConnectorFile{{Name: job.MediaName, URL: downloadURL}}
			p.log.Info("media uploaded to disk", zap.String("file", job.MediaName), zap.String("url", downloadURL))
		}
	} else if job.Text == "" {
		msgBody.Text = "[" + job.MessageType + "]"
	}

	chatExtID := normalizeChatID(job.FromJID)

	msg := ConnectorMessage{
		User:    ConnectorUser{ID: chatExtID, Name: job.FromName, Phone: job.FromPhone},
		Message: msgBody,
		Chat:    ConnectorChat{ID: chatExtID},
	}

	// 4. Envia ao Contact Center
	chatID, err := p.client.ConnectorSendMessage(ctx, creds, acct.ConnectorID, acct.OpenLineID, msg)
	if err != nil {
		_ = p.repo.UpdateMessageStatus(ctx, job.MessageID, db.MsgFailed, err.Error())
		return fmt.Errorf("send to contact center: %w", err)
	}

	// 5. Atualiza o chat_id no mapeamento
	if chatID != "" && chatID != "<nil>" && chatID != "0" {
		contact.BitrixChatID = chatID
		_ = p.repo.UpsertContact(ctx, contact)
	}

	// 6. Confirma entrega da mensagem ao Bitrix
	p.log.Info("calling set delivery", zap.String("msg_id", job.MessageID), zap.Int("line", acct.OpenLineID))
	if err := p.client.ConnectorSetDelivery(ctx, creds, acct.ConnectorID, acct.OpenLineID, job.MessageID); err != nil {
		p.log.Warn("set delivery status failed", zap.String("msg_id", job.MessageID), zap.Error(err))
	} else {
		p.log.Info("set delivery ok", zap.String("msg_id", job.MessageID))
	}

	// 7. Marca como entregue no banco
	_ = p.repo.UpdateMessageStatus(ctx, job.MessageID, db.MsgDelivered, "")

	p.log.Info("inbound delivered to contact center",
		zap.String("from", job.FromPhone),
		zap.String("type", job.MessageType),
		zap.String("chat_id", chatID),
		zap.String("bitrix_domain", acct.Domain))
	return nil
}

func (p *Processor) ensureContact(ctx context.Context, job *queue.InboundJob) (*db.ContactMapping, error) {
	normalizedJID := normalizeChatID(job.FromJID)
	existing, err := p.repo.GetContactByJID(ctx, normalizedJID, job.SessionID)
	if err == nil {
		return existing, nil
	}
	contact := &db.ContactMapping{
		ID:           uuid.New(),
		WAJID:        normalizedJID,
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
