package db

import (
	"context"

	"github.com/google/uuid"
)

// MidiaDaMensagem e' o minimo pra entregar o arquivo de uma mensagem e
// decidir se quem pede pode ve-lo.
type MidiaDaMensagem struct {
	Direction MessageDirection
	FromJID   string
	ToJID     string
	MediaURL  string
	MediaMime string
	MediaSize int64
}

// NossoJID e' o numero do cliente UC Talk na conversa — o que decide de
// quem e' a mensagem.
func (m *MidiaDaMensagem) NossoJID() string {
	if m.Direction == DirOutbound {
		return m.FromJID
	}
	return m.ToJID
}

// GetMidiaDaMensagem busca a midia pelo id (UUID) da mensagem.
func (r *Repository) GetMidiaDaMensagem(ctx context.Context, id uuid.UUID) (*MidiaDaMensagem, error) {
	var m MidiaDaMensagem
	err := r.pool.QueryRow(ctx, `
		SELECT direction, COALESCE(from_jid,''), COALESCE(to_jid,''),
		       COALESCE(media_url,''), COALESCE(media_mime,''), COALESCE(media_size,0)
		  FROM messages WHERE id = $1`, id).
		Scan(&m.Direction, &m.FromJID, &m.ToJID, &m.MediaURL, &m.MediaMime, &m.MediaSize)
	if err != nil {
		return nil, err
	}
	return &m, nil
}
