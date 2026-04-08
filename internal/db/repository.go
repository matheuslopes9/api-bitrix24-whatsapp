package db

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// ─── Sessions ──────────────────────────────────────────────────────────────

func (r *Repository) UpsertSession(ctx context.Context, s *WhatsAppSession) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO whatsapp_sessions (id, jid, phone, display_name, status, session_file)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (jid) DO UPDATE SET
			display_name = EXCLUDED.display_name,
			status       = EXCLUDED.status,
			last_seen    = NOW()
	`, s.ID, s.JID, s.Phone, s.DisplayName, s.Status, s.SessionFile)
	return err
}

func (r *Repository) GetSessionByJID(ctx context.Context, jid string) (*WhatsAppSession, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT id, jid, phone, display_name, status, session_file, created_at, last_seen
		 FROM whatsapp_sessions WHERE jid = $1`, jid)

	var s WhatsAppSession
	err := row.Scan(&s.ID, &s.JID, &s.Phone, &s.DisplayName, &s.Status, &s.SessionFile, &s.CreatedAt, &s.LastSeen)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *Repository) ListActiveSessions(ctx context.Context) ([]*WhatsAppSession, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, jid, phone, display_name, status, session_file, created_at, last_seen
		 FROM whatsapp_sessions WHERE status = 'active'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []*WhatsAppSession
	for rows.Next() {
		var s WhatsAppSession
		if err := rows.Scan(&s.ID, &s.JID, &s.Phone, &s.DisplayName, &s.Status, &s.SessionFile, &s.CreatedAt, &s.LastSeen); err != nil {
			return nil, err
		}
		sessions = append(sessions, &s)
	}
	return sessions, nil
}

func (r *Repository) UpdateSessionStatus(ctx context.Context, jid string, status SessionStatus) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE whatsapp_sessions SET status = $1, last_seen = NOW() WHERE jid = $2`,
		status, jid)
	return err
}

// ─── Contact Mapping ───────────────────────────────────────────────────────

func (r *Repository) UpsertContact(ctx context.Context, c *ContactMapping) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO contact_mapping (id, wa_jid, wa_phone, wa_name, bitrix_entity, bitrix_id, bitrix_chat_id, session_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (wa_jid, session_id) DO UPDATE SET
			wa_name       = EXCLUDED.wa_name,
			bitrix_chat_id= EXCLUDED.bitrix_chat_id,
			updated_at    = NOW()
	`, c.ID, c.WAJID, c.WAPhone, c.WAName, c.BitrixEntity, c.BitrixID, c.BitrixChatID, c.SessionID)
	return err
}

func (r *Repository) GetContactByJID(ctx context.Context, jid string, sessionID uuid.UUID) (*ContactMapping, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, wa_jid, wa_phone, wa_name, bitrix_entity, bitrix_id, bitrix_chat_id, session_id, created_at, updated_at
		FROM contact_mapping WHERE wa_jid = $1 AND session_id = $2`, jid, sessionID)

	var c ContactMapping
	err := row.Scan(&c.ID, &c.WAJID, &c.WAPhone, &c.WAName, &c.BitrixEntity, &c.BitrixID, &c.BitrixChatID, &c.SessionID, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ─── Messages ──────────────────────────────────────────────────────────────

func (r *Repository) InsertMessage(ctx context.Context, m *Message) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO messages (id, wa_message_id, session_id, contact_id, direction, message_type, content,
		                      media_url, media_mime, media_size, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (wa_message_id) DO NOTHING
	`, m.ID, m.WAMessageID, m.SessionID, m.ContactID, m.Direction, m.MessageType, m.Content,
		m.MediaURL, m.MediaMime, m.MediaSize, m.Status)
	return err
}

func (r *Repository) UpdateMessageStatus(ctx context.Context, waMessageID string, status MessageStatus, errMsg string) error {
	var deliveredAt *time.Time
	if status == MsgDelivered {
		now := time.Now()
		deliveredAt = &now
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE messages SET status = $1, error_msg = $2, delivered_at = $3
		WHERE wa_message_id = $4`,
		status, errMsg, deliveredAt, waMessageID)
	return err
}

func (r *Repository) IncrementRetry(ctx context.Context, waMessageID string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE messages SET retry_count = retry_count + 1 WHERE wa_message_id = $1`, waMessageID)
	return err
}

// ─── Relatórios ───────────────────────────────────────────────────────────

type StatsRow struct {
	Date            time.Time `db:"date"`
	TotalMessages   int64     `db:"total_messages"`
	InboundCount    int64     `db:"inbound_count"`
	OutboundCount   int64     `db:"outbound_count"`
	AvgResponseSecs float64   `db:"avg_response_secs"`
}

func (r *Repository) GetDailyStats(ctx context.Context, days int) ([]StatsRow, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			DATE(created_at)            AS date,
			COUNT(*)                    AS total_messages,
			SUM(CASE WHEN direction = 'inbound'  THEN 1 ELSE 0 END) AS inbound_count,
			SUM(CASE WHEN direction = 'outbound' THEN 1 ELSE 0 END) AS outbound_count,
			0::float8                   AS avg_response_secs
		FROM messages
		WHERE created_at >= NOW() - ($1 || ' days')::interval
		GROUP BY DATE(created_at)
		ORDER BY date DESC
	`, fmt.Sprintf("%d", days))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats []StatsRow
	for rows.Next() {
		var s StatsRow
		if err := rows.Scan(&s.Date, &s.TotalMessages, &s.InboundCount, &s.OutboundCount, &s.AvgResponseSecs); err != nil {
			return nil, err
		}
		stats = append(stats, s)
	}
	return stats, nil
}

// ─── Bitrix Tokens ────────────────────────────────────────────────────────

func (r *Repository) UpsertBitrixToken(ctx context.Context, t *BitrixToken) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO bitrix_tokens (id, domain, access_token, refresh_token, expires_at, scope)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (domain) DO UPDATE SET
			access_token  = EXCLUDED.access_token,
			refresh_token = EXCLUDED.refresh_token,
			expires_at    = EXCLUDED.expires_at,
			updated_at    = NOW()
	`, t.ID, t.Domain, t.AccessToken, t.RefreshToken, t.ExpiresAt, t.Scope)
	return err
}

func (r *Repository) GetBitrixToken(ctx context.Context, domain string) (*BitrixToken, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT id, domain, access_token, refresh_token, expires_at, scope, created_at, updated_at
		 FROM bitrix_tokens WHERE domain = $1`, domain)

	var t BitrixToken
	err := row.Scan(&t.ID, &t.Domain, &t.AccessToken, &t.RefreshToken, &t.ExpiresAt, &t.Scope, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
