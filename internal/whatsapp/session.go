package whatsapp

import (
	"github.com/google/uuid"
	"go.mau.fi/whatsmeow"
)

// Session representa uma conexão ativa com o WhatsApp.
type Session struct {
	ID     uuid.UUID
	JID    string
	Phone  string
	Client *whatsmeow.Client
	dbPath string
}
