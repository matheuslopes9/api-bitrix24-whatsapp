package db

import (
	"time"

	"github.com/google/uuid"
)

type SessionStatus string

const (
	SessionActive       SessionStatus = "active"
	SessionDisconnected SessionStatus = "disconnected"
	SessionBanned       SessionStatus = "banned"
)

type WhatsAppSession struct {
	ID          uuid.UUID     `db:"id"`
	JID         string        `db:"jid"`
	Phone       string        `db:"phone"`
	DisplayName string        `db:"display_name"`
	Status      SessionStatus `db:"status"`
	SessionFile string        `db:"session_file"`
	CreatedAt   time.Time     `db:"created_at"`
	LastSeen    *time.Time    `db:"last_seen"`
}

type ContactMapping struct {
	ID            uuid.UUID  `db:"id"`
	WAJID         string     `db:"wa_jid"`
	WAPhone       string     `db:"wa_phone"`
	WAName        string     `db:"wa_name"`
	BitrixEntity  string     `db:"bitrix_entity"`
	BitrixID      int64      `db:"bitrix_id"`
	BitrixChatID  string     `db:"bitrix_chat_id"`
	SessionID     *uuid.UUID `db:"session_id"`
	CreatedAt     time.Time  `db:"created_at"`
	UpdatedAt     time.Time  `db:"updated_at"`
}

type MessageDirection string
type MessageType string
type MessageStatus string

const (
	DirInbound  MessageDirection = "inbound"
	DirOutbound MessageDirection = "outbound"

	MsgTypeText     MessageType = "text"
	MsgTypeImage    MessageType = "image"
	MsgTypeAudio    MessageType = "audio"
	MsgTypeDocument MessageType = "document"
	MsgTypeVideo    MessageType = "video"
	MsgTypeSticker  MessageType = "sticker"

	MsgReceived  MessageStatus = "received"
	MsgQueued    MessageStatus = "queued"
	MsgDelivered MessageStatus = "delivered"
	MsgFailed    MessageStatus = "failed"
)

type Message struct {
	ID          uuid.UUID        `db:"id"`
	WAMessageID string           `db:"wa_message_id"`
	SessionID   *uuid.UUID       `db:"session_id"`
	ContactID   *uuid.UUID       `db:"contact_id"`
	Direction   MessageDirection `db:"direction"`
	MessageType MessageType      `db:"message_type"`
	Content     string           `db:"content"`
	MediaURL    string           `db:"media_url"`
	MediaMime   string           `db:"media_mime"`
	MediaSize   int64            `db:"media_size"`
	Status      MessageStatus    `db:"status"`
	RetryCount  int              `db:"retry_count"`
	ErrorMsg    string           `db:"error_msg"`
	SentAt      *time.Time       `db:"sent_at"`
	DeliveredAt *time.Time       `db:"delivered_at"`
	CreatedAt   time.Time        `db:"created_at"`
}

type BitrixToken struct {
	ID           uuid.UUID `db:"id"`
	Domain       string    `db:"domain"`
	ClientID     string    `db:"client_id"`  // separa tokens do Local App e Partner App
	AccessToken  string    `db:"access_token"`
	RefreshToken string    `db:"refresh_token"`
	ExpiresAt    time.Time `db:"expires_at"`
	Scope        string    `db:"scope"`
	CreatedAt    time.Time `db:"created_at"`
	UpdatedAt    time.Time `db:"updated_at"`
}

type BitrixAccountStatus string

const (
	BitrixAccountPending BitrixAccountStatus = "pending"
	BitrixAccountActive  BitrixAccountStatus = "active"
)

// BitrixAccount vincula uma sessão WhatsApp a uma conta Bitrix24 específica.
// Permite multi-tenancy: cada número WA tem seu próprio portal Bitrix.
type BitrixAccount struct {
	ID           uuid.UUID           `db:"id"`
	SessionJID   string              `db:"session_jid"`
	Domain       string              `db:"domain"`
	ClientID     string              `db:"client_id"`
	ClientSecret string              `db:"client_secret"`
	OpenLineID   int                 `db:"open_line_id"`
	ConnectorID  string              `db:"connector_id"`
	RedirectURI  string              `db:"redirect_uri"`
	Status       BitrixAccountStatus `db:"status"`
	CreatedAt    time.Time           `db:"created_at"`
	UpdatedAt    time.Time           `db:"updated_at"`
}

// BitrixPortal representa um portal Bitrix24 que instalou o app via Marketplace.
// Preenchido automaticamente pelo installation handler (POST /bitrix/install).
// Independente de BitrixAccount — não requer configuração manual pelo admin.
type BitrixPortal struct {
	ID           uuid.UUID `db:"id"`
	Domain       string    `db:"domain"`       // ex: "empresa.bitrix24.com.br"
	AccessToken  string    `db:"access_token"`
	RefreshToken string    `db:"refresh_token"`
	ExpiresAt    time.Time `db:"expires_at"`
	MemberID     string    `db:"member_id"`    // identificador único do portal
	ConnectorID  string    `db:"connector_id"`
	OpenLineID   int       `db:"open_line_id"` // 0 = não configurado
	InstalledAt  time.Time `db:"installed_at"`
	UpdatedAt    time.Time `db:"updated_at"`
}
