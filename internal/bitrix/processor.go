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
//
// Grupos: quando job.IsGroup=true, usa GroupJID como chat key (1 chat
// unico por grupo no Open Channel, nao 1 por participante) e prefixa
// o texto com "*Nome do remetente:* texto" pra atendente distinguir
// quem mandou. Contato CRM e' criado com WAName="Grupo XYZ".
func (p *Processor) ProcessInbound(ctx context.Context, job *queue.InboundJob) error {
	p.log.Info("ProcessInbound called",
		zap.String("session_jid", job.SessionJID),
		zap.String("from", job.FromJID),
		zap.String("msg_id", job.MessageID),
		zap.String("text", job.Text),
		zap.String("type", job.MessageType),
		zap.Bool("is_group", job.IsGroup),
		zap.String("group_jid", job.GroupJID),
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

	// 3. Decide chat_id, nome e telefone que vao pro Open Channel.
	//
	// Grupo: chat_id = JID do grupo (todos do grupo caem no mesmo chat).
	//        Nome = nome do grupo. Phone = "" (grupo nao tem telefone).
	//        Texto = "*Remetente:* mensagem" pra atendente distinguir.
	// 1-a-1: chat_id = JID do remetente (default antigo). Nome/Phone do
	//        proprio sender.
	var chatExtID, chatName, chatPhone, msgText string
	if job.IsGroup && job.GroupJID != "" {
		chatExtID = normalizeChatID(job.GroupJID)
		chatName = job.GroupName
		chatPhone = "" // grupo nao tem telefone individual
		sender := job.FromName
		if sender == "" {
			sender = job.FromPhone
		}
		if sender == "" {
			sender = "Membro"
		}
		if job.Text != "" {
			msgText = "*" + sender + ":* " + job.Text
		} else {
			msgText = "*" + sender + ":* [" + job.MessageType + "]"
		}
	} else {
		chatExtID = normalizeChatID(job.FromJID)
		chatName = job.FromName
		chatPhone = job.FromPhone
		msgText = job.Text
	}

	msgBody := ConnectorMsgBody{ID: job.MessageID, Text: msgText}

	// Anexa mídia se disponível
	if len(job.MediaData) > 0 && job.MediaName != "" {
		_, downloadURL, err := p.client.UploadToDisk(ctx, creds, job.MediaName, job.MediaData)
		if err != nil {
			p.log.Warn("upload media to disk failed, sending text only",
				zap.String("file", job.MediaName), zap.Error(err))
			if msgBody.Text == "" {
				msgBody.Text = "📎 Arquivo recebido: " + job.MediaName + "\n⚠️ Não foi possível transferir o arquivo (pode ser muito grande para o Bitrix24 ou o upload expirou)."
			}
		} else {
			msgBody.Files = []ConnectorFile{{Name: job.MediaName, URL: downloadURL}}
			p.log.Info("media uploaded to disk", zap.String("file", job.MediaName), zap.String("url", downloadURL))
		}
	} else if msgBody.Text == "" {
		msgBody.Text = "[" + job.MessageType + "]"
	}

	msg := ConnectorMessage{
		User:    ConnectorUser{ID: chatExtID, Name: chatName, Phone: chatPhone},
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
	// Grupo: contato representa o GRUPO inteiro, identificado pelo GroupJID.
	// Nao cria contato por participante.
	var jid, name, phone string
	if job.IsGroup && job.GroupJID != "" {
		jid = normalizeChatID(job.GroupJID)
		name = job.GroupName
		phone = "" // grupo nao tem telefone proprio
	} else {
		jid = normalizeChatID(job.FromJID)
		name = job.FromName
		phone = job.FromPhone
	}

	existing, err := p.repo.GetContactByJID(ctx, jid, job.SessionID)
	if err == nil {
		// Atualiza nome do grupo se mudou (grupo renomeado no WhatsApp).
		if job.IsGroup && name != "" && existing.WAName != name {
			existing.WAName = name
			_ = p.repo.UpsertContact(ctx, existing)
		}
		return existing, nil
	}
	contact := &db.ContactMapping{
		ID:           uuid.New(),
		WAJID:        jid,
		WAPhone:      phone,
		WAName:       name,
		BitrixEntity: "chat",
		BitrixID:     0,
		SessionID:    &job.SessionID,
	}
	if err := p.repo.UpsertContact(ctx, contact); err != nil {
		return nil, err
	}
	return contact, nil
}
