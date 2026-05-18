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

// SessionType indica se a sessão é QR Code (whatsmeow) ou Cloud API (Meta).
type SessionType string

const (
	SessionTypeQR       SessionType = "qr"
	SessionTypeCloudAPI SessionType = "cloud_api"
)

type WhatsAppSession struct {
	ID          uuid.UUID     `db:"id"`
	JID         string        `db:"jid"`
	Phone       string        `db:"phone"`
	DisplayName string        `db:"display_name"`
	Status      SessionStatus `db:"status"`
	SessionFile string        `db:"session_file"`
	Type        SessionType   `db:"type"`
	// Credenciais Cloud API (vazias para sessões QR)
	CloudPhoneNumberID string `db:"cloud_phone_number_id"`
	CloudWABAID        string `db:"cloud_waba_id"`
	CloudAccessToken   string `db:"cloud_access_token"`
	CloudVerifyToken   string `db:"cloud_verify_token"`
	CloudAppSecret     string `db:"cloud_app_secret"`
	CloudDisplayPhone  string `db:"cloud_display_phone"`
	CreatedAt          time.Time  `db:"created_at"`
	LastSeen           *time.Time `db:"last_seen"`
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
	FromJID     string           `db:"from_jid"`    // JID de quem enviou
	ToJID       string           `db:"to_jid"`      // JID de quem recebeu
	AuthorName  string           `db:"author_name"` // nome do operador (outbound) ou do contato (inbound)
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
// TenantPlan representa o plano/billing de um tenant (domain Bitrix).
// Default no install: plan=basic, status=trial, trial_ends_at=NOW+7d.
type TenantPlan struct {
	Domain       string     `db:"domain"`
	Plan         string     `db:"plan"`           // 'basic' | 'pro'
	Status       string     `db:"status"`         // 'trial' | 'active' | 'expired' | 'suspended'
	TrialEndsAt  *time.Time `db:"trial_ends_at"`  // NULL = sem trial (Pro pago)
	ActiveUntil  *time.Time `db:"active_until"`   // NULL = vitalicio
	CreatedAt    time.Time  `db:"created_at"`
	UpdatedAt    time.Time  `db:"updated_at"`
	Notes        string     `db:"notes"`
}

// IsAccessAllowed retorna true se o tenant tem acesso a usar o app.
// false = expirou trial sem upgrade, ou suspended manual.
func (p *TenantPlan) IsAccessAllowed() bool {
	if p == nil {
		return false
	}
	now := time.Now()
	switch p.Status {
	case "active":
		return p.ActiveUntil == nil || p.ActiveUntil.After(now)
	case "trial":
		return p.TrialEndsAt != nil && p.TrialEndsAt.After(now)
	}
	return false
}

// HasProFeatures retorna true se o plano libera features avancadas
// (Cloud API, templates HSM, SMS Campaigns, multi-sessao, relatorios).
// Trial = basico (config do cliente), so 'active'+'pro' tem Pro.
func (p *TenantPlan) HasProFeatures() bool {
	if p == nil {
		return false
	}
	return p.Plan == "pro" && p.IsAccessAllowed()
}

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
	// LegacyAdminUserID: user_id Bitrix de quem instalou o app. Esse usuario
	// recebe permission wildcard automatica no install e e o unico que pode
	// gerenciar permissoes dos outros. Troca via /admin/api/legacy-requests.
	LegacyAdminUserID string `db:"legacy_admin_user_id"`
	// DefaultSMSSessionJID: sessao WA escolhida pelo master pra rotear
	// campanhas SMS. Vazio = modulo SMS Campaigns desativado.
	DefaultSMSSessionJID string `db:"default_sms_session_jid"`
	// SMSRiskAcknowledged: master confirmou o aviso de risco de banimento.
	SMSRiskAcknowledged bool `db:"sms_risk_acknowledged"`
	// ApplicationToken: token que o Bitrix gera por install e envia em todos
	// os POSTs server-to-server (handlers de event, BizProc activities, SMS
	// sender callbacks). Usado pra validar autenticidade de chamadas em
	// /bitrix/bp/send e /bitrix/sms/send com constant-time compare.
	ApplicationToken string `db:"application_token"`
}

// BitrixSMSMessage representa um envio de SMS via Bitrix Marketing > Campanhas SMS
// que e' roteado pelo nosso provedor para WhatsApp. Cada linha = 1 destinatario.
type BitrixSMSMessage struct {
	BitrixMessageID  string     `db:"bitrix_message_id"`
	Domain           string     `db:"domain"`
	SenderCode       string     `db:"sender_code"`
	SessionJID       string     `db:"session_jid"`
	ToPhone          string     `db:"to_phone"`
	Body             string     `db:"body"`
	WAMessageID      string     `db:"wa_message_id"`
	Status           string     `db:"status"`
	ErrorMsg         string     `db:"error_msg"`
	BindingsJSON     string     `db:"bindings_json"`
	CreatedAt        time.Time  `db:"created_at"`
	SentAt           *time.Time `db:"sent_at"`
	StatusUpdatedAt  *time.Time `db:"status_updated_at"`
}
