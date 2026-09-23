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

// telefoneLegivel formata o numero pra exibicao: "558199809595" vira
// "+55 81 9980-9595". Usado como ultimo recurso de nome — melhor o cliente
// aparecer pelo numero do que como "Guest".
func telefoneLegivel(fone string) string {
	d := make([]rune, 0, len(fone))
	for _, r := range fone {
		if r >= '0' && r <= '9' {
			d = append(d, r)
		}
	}
	n := string(d)
	if n == "" {
		return ""
	}
	// LID (LinkedID) nao e' telefone. O whatsmeow resolve a maioria via
	// SenderAlt, mas quando nao ha' alt sobra o LID cru — 15+ digitos.
	// Formatar isso como "+1275862..." inventaria um numero que nao existe
	// e o atendente tentaria ligar. Mostra os ultimos digitos so' pra
	// distinguir um contato do outro na lista.
	// A regra de 15 digitos vem da migration 009, que ja' usava esse corte.
	if len(n) > 15 {
		return "Contato WhatsApp (…" + n[len(n)-4:] + ")"
	}
	// Brasil: 55 + DDD(2) + numero(8 ou 9)
	if strings.HasPrefix(n, "55") && (len(n) == 12 || len(n) == 13) {
		ddd := n[2:4]
		resto := n[4:]
		meio := len(resto) - 4
		return "+55 " + ddd + " " + resto[:meio] + "-" + resto[meio:]
	}
	return "+" + n
}

// nomeDoContato decide o nome que vai aparecer no Contact Center.
//
// BUG: antes era so' job.FromName, que e' o PushName do WhatsApp. Esse campo
// vem VAZIO com frequencia — remetente @lid, contato que nunca mandou push
// name, ou store recem-pareado (o cache de contatos nasce vazio). Com nome
// vazio, o Bitrix rotula a conversa como "Guest", e o atendente nao sabe com
// quem esta falando nem consegue diferenciar dois "Guest" na lista.
//
// Ordem: push name do WhatsApp -> nome que ja' gravamos do contato ->
// telefone formatado. So' cai pra vazio se nao houver nem numero.
func nomeDoContato(pushName string, contato *db.ContactMapping, fone string) string {
	if n := strings.TrimSpace(pushName); n != "" {
		return n
	}
	if contato != nil {
		if n := strings.TrimSpace(contato.WAName); n != "" {
			return n
		}
	}
	if n := telefoneLegivel(fone); n != "" {
		return n
	}
	// Ultimo recurso. Nunca devolve vazio: nome vazio faz o Bitrix rotular a
	// conversa como "Guest", e duas conversas "Guest" sao indistinguiveis na
	// lista do atendente. Um rotulo generico ao menos diz que o contato nao
	// se identificou — e a trava em ProcessInbound impede que se chegue aqui
	// por job malformado.
	return "Contato WhatsApp"
}

// primeirosChars corta texto pra log sem estourar a linha.
func primeirosChars(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
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

// markStatus atualiza o status da mensagem no banco LOGANDO a falha.
//
// BUG HISTORICO: estas chamadas eram '_ = p.repo.UpdateMessageStatus(...)'.
// A tabela 'messages' estava sem as colunas error_msg/delivered_at (ver
// migration 043), entao TODO UPDATE de status falhava com SQLSTATE 42703 —
// e o erro descartado fazia isso passar despercebido: a mensagem chegava no
// Contact Center mas nunca marcava entrega no banco, e o painel mostrava
// tudo parado em 'received' sem nenhum sinal de erro nos logs.
//
// Nao retorna erro de proposito: falhar em gravar o status nao deve abortar
// a entrega da mensagem, que ja' aconteceu. Mas TEM que aparecer no log.
func (p *Processor) markStatus(ctx context.Context, waMessageID string, status db.MessageStatus, errMsg string) {
	if err := p.repo.UpdateMessageStatus(ctx, waMessageID, status, errMsg); err != nil {
		p.log.Error("falha ao gravar status da mensagem no banco",
			zap.String("msg_id", waMessageID),
			zap.String("status_pretendido", string(status)),
			zap.Error(err))
	}
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
		p.markStatus(ctx, job.MessageID, db.MsgFailed, "bitrix account not configured")
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
		p.markStatus(ctx, job.MessageID, db.MsgFailed, err.Error())
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
		if strings.TrimSpace(chatName) == "" {
			chatName = "Grupo WhatsApp"
		}
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
		// TRAVA: sem remetente nao existe conversa. Um job que chega aqui sem
		// from_jid E sem from_phone nao e' mensagem de cliente — e' job
		// malformado (ja' aconteceu com job de SAIDA reenfileirado como
		// entrada pela dead queue). Mandar isso pro Contact Center criava um
		// chat sem identidade, que o Bitrix rotula "Guest": o atendente via
		// a propria mensagem dele voltar como cliente novo e anonimo.
		//
		// Falhar aqui e' melhor que criar o chat fantasma: o job fica
		// registrado como falha, com motivo, em vez de sujar a lista de
		// atendimento.
		if strings.TrimSpace(job.FromJID) == "" && strings.TrimSpace(job.FromPhone) == "" {
			p.markStatus(ctx, job.MessageID, db.MsgFailed, "job sem remetente (from_jid e from_phone vazios)")
			p.log.Warn("inbound descartado: job sem remetente",
				zap.String("job_id", job.ID),
				zap.String("session_jid", job.SessionJID),
				zap.String("texto", primeirosChars(job.Text, 60)))
			return fmt.Errorf("job sem remetente: nao da' pra identificar o contato")
		}
		chatExtID = normalizeChatID(job.FromJID)
		chatName = nomeDoContato(job.FromName, contact, job.FromPhone)
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
		p.markStatus(ctx, job.MessageID, db.MsgFailed, err.Error())
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
	p.markStatus(ctx, job.MessageID, db.MsgDelivered, "")

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
		// Grava o nome quando ele MUDA ou quando finalmente chega.
		//
		// Antes isto valia so' pra grupo. Num contato 1-a-1 cujo primeiro
		// PushName veio vazio, o nome nunca mais era preenchido, mesmo que
		// mensagens seguintes trouxessem — o contato ficava "Guest" pra
		// sempre. Nunca sobrescreve nome existente com vazio.
		if name != "" && existing.WAName != name {
			existing.WAName = name
			if existing.WAPhone == "" && phone != "" {
				existing.WAPhone = phone
			}
			_ = p.repo.UpsertContact(ctx, existing)
		}
		return existing, nil
	}
	contact := &db.ContactMapping{
		ID:      uuid.New(),
		WAJID:   jid,
		WAPhone: phone,
		// Sem PushName, guarda o telefone formatado: e' o que vai aparecer
		// pro atendente ate' o WhatsApp informar o nome de perfil.
		WAName:       nomeDoContato(name, nil, phone),
		BitrixEntity: "chat",
		BitrixID:     "", // nunca lido: o vinculo real do chat e o BitrixChatID
		SessionID:    &job.SessionID,
	}
	if err := p.repo.UpsertContact(ctx, contact); err != nil {
		return nil, err
	}
	return contact, nil
}
