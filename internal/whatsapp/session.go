package whatsapp

import (
	"github.com/google/uuid"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
)

// Session representa uma conexão ativa com o WhatsApp.
type Session struct {
	ID     uuid.UUID
	JID    string
	Phone  string
	Client *whatsmeow.Client
	dbPath string

	// container e' o store SQLite que originou o Client. Precisa ser
	// guardado pra poder ser FECHADO no teardown: cada connectSession abria
	// um sqlstore.New novo e nunca fechava o anterior, vazando o handle do
	// SQLite (e o pool de conexoes) a cada reconexao do watchdog.
	container *sqlstore.Container
}

// close encerra a sessão por completo: desconecta o WebSocket, remove os
// event handlers e fecha o store SQLite.
//
// Sem isso, o cliente antigo continuava vivo depois de ser removido do mapa
// do Manager — com os event handlers ainda registrados. Dois clientes vivos
// sobre o MESMO device fazem o WhatsApp derrubar um deles (stream:conflict),
// o que produzia o ciclo de desconexão/reconexão a cada ~30s.
func (s *Session) close() {
	if s == nil {
		return
	}
	if s.Client != nil {
		s.Client.RemoveEventHandlers()
		s.Client.Disconnect()
	}
	if s.container != nil {
		_ = s.container.Close()
	}
}
