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

	// 1. Busca a conta Bitrix vinculada à sessão WA.
	// Tenta primeiro em bitrix_accounts (app local), depois em bitrix_portals (Partner App).
	var (
		domain      string
		connectorID string
		openLineID  int
		creds       TenantCreds
	)

	acct, err := p.repo.GetBitrixAccountByJID(ctx, job.SessionJID)
	if err == nil {
		// App local — usa as credenciais da conta
		domain      = acct.Domain
		connectorID = acct.ConnectorID
		openLineID  = acct.OpenLineID
		creds = TenantCreds{
			Domain:       acct.Domain,
			ClientID:     acct.ClientID,
			ClientSecret: acct.ClientSecret,
			RedirectURI:  acct.RedirectURI,
		}
		p.log.Info("bitrix account found (local app)",
			zap.String("domain", domain),
			zap.Int("open_line_id", openLineID),
		)
	} else {
		// Fallback: Partner App — pega o primeiro portal disponível
		portals, pErr := p.repo.ListBitrixPortals(ctx)
		if pErr != nil || len(portals) == 0 {
			p.log.Error("no bitrix account or portal found",
				zap.String("session_jid", job.SessionJID),
				zap.Error(err),
			)
			_ = p.repo.UpdateMessageStatus(ctx, job.MessageID, db.MsgFailed, "no bitrix account configured")
			return fmt.Errorf("no bitrix account or portal for session %s: %w", job.SessionJID, err)
		}
		portal := portals[0]
		domain      = portal.Domain
		connectorID = portal.ConnectorID
		if connectorID == "" {
			connectorID = "whatsapp_uc_v2"
		}
		openLineID = portal.OpenLineID
		if openLineID == 0 {
			openLineID = 1
		}
		creds = TenantCreds{Domain: portal.Domain}
		p.log.Info("bitrix portal found (partner app)",
			zap.String("domain", domain),
			zap.Int("open_line_id", openLineID),
			zap.String("connector_id", connectorID),
		)
	}
	_ = domain // usado nos logs abaixo

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
				msgBody.Text = "📎 Arquivo recebido: " + job.MediaName + "\n⚠️ Não foi possível transferir o arquivo (pode ser muito grande para o Bitrix24 ou o upload expirou)."
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
	chatID, err := p.client.ConnectorSendMessage(ctx, creds, connectorID, openLineID, msg)
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
	p.log.Info("calling set delivery", zap.String("msg_id", job.MessageID), zap.Int("line", openLineID))
	if err := p.client.ConnectorSetDelivery(ctx, creds, connectorID, openLineID, job.MessageID); err != nil {
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
		zap.String("bitrix_domain", domain))
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
