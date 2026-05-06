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
			id           = EXCLUDED.id,
			display_name = EXCLUDED.display_name,
			status       = EXCLUDED.status,
			session_file = EXCLUDED.session_file,
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

// ListAllSessions retorna todas as sessões (ativas e desconectadas), exceto banidas.
// Usada pelo watchdog para tentar reconectar sessões que caíram.
func (r *Repository) ListAllSessions(ctx context.Context) ([]WhatsAppSession, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, jid, phone, display_name, status, session_file, created_at, last_seen
		 FROM whatsapp_sessions WHERE status != 'banned'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []WhatsAppSession
	for rows.Next() {
		var s WhatsAppSession
		if err := rows.Scan(&s.ID, &s.JID, &s.Phone, &s.DisplayName, &s.Status, &s.SessionFile, &s.CreatedAt, &s.LastSeen); err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}

func (r *Repository) UpdateSessionStatus(ctx context.Context, jid string, status SessionStatus) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE whatsapp_sessions SET status = $1, last_seen = NOW() WHERE jid = $2`,
		status, jid)
	return err
}

func (r *Repository) DeleteSession(ctx context.Context, jid string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM whatsapp_sessions WHERE jid = $1`, jid)
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

func (r *Repository) GetContactByWAJID(ctx context.Context, jid string) (*ContactMapping, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, wa_jid, wa_phone, wa_name, bitrix_entity, bitrix_id, bitrix_chat_id, session_id, created_at, updated_at
		FROM contact_mapping WHERE wa_jid = $1 ORDER BY created_at DESC LIMIT 1`, jid)

	var c ContactMapping
	err := row.Scan(&c.ID, &c.WAJID, &c.WAPhone, &c.WAName, &c.BitrixEntity, &c.BitrixID, &c.BitrixChatID, &c.SessionID, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *Repository) GetSessionByID(ctx context.Context, id uuid.UUID) (*WhatsAppSession, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT id, jid, phone, display_name, status, session_file, created_at, last_seen
		 FROM whatsapp_sessions WHERE id = $1`, id)

	var s WhatsAppSession
	err := row.Scan(&s.ID, &s.JID, &s.Phone, &s.DisplayName, &s.Status, &s.SessionFile, &s.CreatedAt, &s.LastSeen)
	if err != nil {
		return nil, err
	}
	return &s, nil
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
		INSERT INTO messages (id, wa_message_id, session_id, contact_id, from_jid, to_jid, author_name,
		                      direction, message_type, content, media_url, media_mime, media_size, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (wa_message_id) DO UPDATE SET
			from_jid    = EXCLUDED.from_jid,
			to_jid      = EXCLUDED.to_jid,
			author_name = EXCLUDED.author_name,
			status      = EXCLUDED.status
	`, m.ID, m.WAMessageID, m.SessionID, m.ContactID, m.FromJID, m.ToJID, m.AuthorName,
		m.Direction, m.MessageType, m.Content,
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

// DeleteOldMessages remove mensagens com mais de retentionDays dias.
// Retorna o número de registros deletados.
func (r *Repository) DeleteOldMessages(ctx context.Context, retentionDays int) (int64, error) {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM messages WHERE created_at < NOW() - ($1 || ' days')::INTERVAL`,
		retentionDays)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// GetRecentMessages retorna as N mensagens mais recentes do banco, sem filtro de telefone.
// Usado pelo simulador para diagnosticar mensagens com JID inesperado.
func (r *Repository) GetRecentMessages(ctx context.Context, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 30
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, wa_message_id, session_id, contact_id,
		       COALESCE(from_jid,''), COALESCE(to_jid,''), COALESCE(author_name,''),
		       direction, message_type,
		       COALESCE(content,''), COALESCE(media_url,''), COALESCE(media_mime,''),
		       COALESCE(media_size,0),
		       status, retry_count, COALESCE(error_msg,''),
		       sent_at, delivered_at, created_at
		FROM messages
		ORDER BY created_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var msgs []Message
	for rows.Next() {
		var m Message
		var sentAt, deliveredAt *time.Time
		if err := rows.Scan(
			&m.ID, &m.WAMessageID, &m.SessionID, &m.ContactID,
			&m.FromJID, &m.ToJID, &m.AuthorName,
			&m.Direction, &m.MessageType,
			&m.Content, &m.MediaURL, &m.MediaMime,
			&m.MediaSize,
			&m.Status, &m.RetryCount, &m.ErrorMsg,
			&sentAt, &deliveredAt, &m.CreatedAt,
		); err != nil {
			return nil, err
		}
		m.SentAt = sentAt
		m.DeliveredAt = deliveredAt
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// DeleteMessagesByJIDPattern remove mensagens cujo from_jid ou to_jid bate com o pattern LIKE.
// Usado pelo simulador para limpar mensagens de teste.
func (r *Repository) DeleteMessagesByJIDPattern(ctx context.Context, pattern string) (int64, error) {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM messages WHERE from_jid LIKE $1 OR to_jid LIKE $1`, pattern)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// GetMessagesByPhone retorna as últimas N mensagens trocadas com um número de telefone.
// Busca de duas formas:
//   1. JID direto via LIKE "phone@%"  (msgs com @s.whatsapp.net)
//   2. Via contact_mapping: pega todos os wa_jid (incluindo @lid) onde wa_phone = phone,
//      e busca msgs que envolvam qualquer um desses JIDs. Necessário porque o WhatsApp
//      pode usar @lid (LinkedID) em vez de @s.whatsapp.net no Sender — o LID não contém
//      o telefone real, mas o contact_mapping vincula os dois.
func (r *Repository) GetMessagesByPhone(ctx context.Context, phone string, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 50
	}
	pattern := phone + "@%"
	rows, err := r.pool.Query(ctx, `
		SELECT id, wa_message_id, session_id, contact_id,
		       COALESCE(from_jid,''), COALESCE(to_jid,''), COALESCE(author_name,''),
		       direction, message_type,
		       COALESCE(content,''), COALESCE(media_url,''), COALESCE(media_mime,''),
		       COALESCE(media_size,0),
		       status, retry_count, COALESCE(error_msg,''),
		       sent_at, delivered_at, created_at
		FROM messages
		WHERE from_jid LIKE $1
		   OR to_jid   LIKE $1
		   OR from_jid IN (SELECT wa_jid FROM contact_mapping WHERE wa_phone = $2)
		   OR to_jid   IN (SELECT wa_jid FROM contact_mapping WHERE wa_phone = $2)
		   OR contact_id IN (SELECT id FROM contact_mapping WHERE wa_phone = $2)
		ORDER BY created_at DESC
		LIMIT $3
	`, pattern, phone, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var m Message
		// sent_at e delivered_at são nullable no banco — usar *time.Time intermediário
		var sentAt, deliveredAt *time.Time
		if err := rows.Scan(
			&m.ID, &m.WAMessageID, &m.SessionID, &m.ContactID,
			&m.FromJID, &m.ToJID, &m.AuthorName,
			&m.Direction, &m.MessageType,
			&m.Content, &m.MediaURL, &m.MediaMime,
			&m.MediaSize,
			&m.Status, &m.RetryCount, &m.ErrorMsg,
			&sentAt, &deliveredAt, &m.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan message row: %w", err)
		}
		m.SentAt = sentAt
		m.DeliveredAt = deliveredAt
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Inverte para ordem cronológica (mais antigas primeiro)
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

// DebugMessageStats retorna estatísticas do banco para diagnóstico.
func (r *Repository) DebugMessageStats(ctx context.Context, phone string) (map[string]interface{}, error) {
	result := map[string]interface{}{}

	// Migration aplicada?
	var hasCols bool
	r.pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_name='messages' AND column_name='from_jid'
	)`).Scan(&hasCols)
	result["migration_006_applied"] = hasCols

	// Total de mensagens
	var total int64
	r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM messages`).Scan(&total)
	result["total_messages"] = total

	if !hasCols {
		return result, nil
	}

	// Com from_jid preenchido
	var withJID int64
	r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM messages WHERE from_jid != ''`).Scan(&withJID)
	result["messages_with_jid"] = withJID

	// Últimas mensagens do número
	result["phone_searched"] = phone
	if phone != "" {
		pattern := phone + "@%"
		rows, err := r.pool.Query(ctx, `
			SELECT wa_message_id, direction, from_jid, to_jid, content, created_at
			FROM messages WHERE from_jid LIKE $1 OR to_jid LIKE $1
			ORDER BY created_at DESC LIMIT 10`, pattern)
		if err == nil {
			defer rows.Close()
			type row struct {
				ID        string `json:"id"`
				Direction string `json:"direction"`
				FromJID   string `json:"from_jid"`
				ToJID     string `json:"to_jid"`
				Content   string `json:"content"`
				CreatedAt string `json:"created_at"`
			}
			var msgs []row
			for rows.Next() {
				var m row
				var t interface{}
				rows.Scan(&m.ID, &m.Direction, &m.FromJID, &m.ToJID, &m.Content, &t)
				m.CreatedAt = fmt.Sprintf("%v", t)
				msgs = append(msgs, m)
			}
			result["recent_messages"] = msgs
		}
	}
	return result, nil
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

// StatsSessionRow — mensagens agrupadas por sessão (número WA)
type StatsSessionRow struct {
	SessionJID    string  `db:"session_jid"`
	Phone         string  `db:"phone"`
	TotalMessages int64   `db:"total_messages"`
	InboundCount  int64   `db:"inbound_count"`
	OutboundCount int64   `db:"outbound_count"`
	FailedCount   int64   `db:"failed_count"`
}

// StatsTypeRow — mensagens agrupadas por tipo (text, image, audio…)
type StatsTypeRow struct {
	MessageType   string `db:"message_type"`
	TotalMessages int64  `db:"total_messages"`
	InboundCount  int64  `db:"inbound_count"`
	OutboundCount int64  `db:"outbound_count"`
}

// StatsHourRow — volume por hora do dia
type StatsHourRow struct {
	Hour          int   `db:"hour"`
	TotalMessages int64 `db:"total_messages"`
}

// StatsContactRow — top contatos por volume
type StatsContactRow struct {
	WAJID         string `db:"wa_jid"`
	WAPhone       string `db:"wa_phone"`
	WAName        string `db:"wa_name"`
	TotalMessages int64  `db:"total_messages"`
	InboundCount  int64  `db:"inbound_count"`
	OutboundCount int64  `db:"outbound_count"`
}

func (r *Repository) GetStatsBySession(ctx context.Context, days int) ([]StatsSessionRow, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			COALESCE(s.jid, 'desconhecido')    AS session_jid,
			COALESCE(s.phone, 'desconhecido')  AS phone,
			COUNT(*)                           AS total_messages,
			SUM(CASE WHEN m.direction = 'inbound'  THEN 1 ELSE 0 END) AS inbound_count,
			SUM(CASE WHEN m.direction = 'outbound' THEN 1 ELSE 0 END) AS outbound_count,
			SUM(CASE WHEN m.status    = 'failed'   THEN 1 ELSE 0 END) AS failed_count
		FROM messages m
		LEFT JOIN whatsapp_sessions s ON s.id = m.session_id
		WHERE m.created_at >= NOW() - ($1 || ' days')::interval
		GROUP BY s.jid, s.phone
		ORDER BY total_messages DESC
	`, fmt.Sprintf("%d", days))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StatsSessionRow
	for rows.Next() {
		var row StatsSessionRow
		if err := rows.Scan(&row.SessionJID, &row.Phone, &row.TotalMessages, &row.InboundCount, &row.OutboundCount, &row.FailedCount); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

func (r *Repository) GetStatsByType(ctx context.Context, days int) ([]StatsTypeRow, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			message_type,
			COUNT(*)                           AS total_messages,
			SUM(CASE WHEN direction = 'inbound'  THEN 1 ELSE 0 END) AS inbound_count,
			SUM(CASE WHEN direction = 'outbound' THEN 1 ELSE 0 END) AS outbound_count
		FROM messages
		WHERE created_at >= NOW() - ($1 || ' days')::interval
		GROUP BY message_type
		ORDER BY total_messages DESC
	`, fmt.Sprintf("%d", days))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StatsTypeRow
	for rows.Next() {
		var row StatsTypeRow
		if err := rows.Scan(&row.MessageType, &row.TotalMessages, &row.InboundCount, &row.OutboundCount); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

func (r *Repository) GetStatsByHour(ctx context.Context, days int) ([]StatsHourRow, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			EXTRACT(HOUR FROM created_at)::int AS hour,
			COUNT(*)                           AS total_messages
		FROM messages
		WHERE created_at >= NOW() - ($1 || ' days')::interval
		GROUP BY hour
		ORDER BY hour
	`, fmt.Sprintf("%d", days))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StatsHourRow
	for rows.Next() {
		var row StatsHourRow
		if err := rows.Scan(&row.Hour, &row.TotalMessages); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

func (r *Repository) GetTopContacts(ctx context.Context, days, limit int) ([]StatsContactRow, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			COALESCE(c.wa_jid, 'desconhecido')   AS wa_jid,
			COALESCE(c.wa_phone, '')              AS wa_phone,
			COALESCE(c.wa_name, '')               AS wa_name,
			COUNT(*)                              AS total_messages,
			SUM(CASE WHEN m.direction = 'inbound'  THEN 1 ELSE 0 END) AS inbound_count,
			SUM(CASE WHEN m.direction = 'outbound' THEN 1 ELSE 0 END) AS outbound_count
		FROM messages m
		LEFT JOIN contact_mapping c ON c.id = m.contact_id
		WHERE m.created_at >= NOW() - ($1 || ' days')::interval
		GROUP BY c.wa_jid, c.wa_phone, c.wa_name
		ORDER BY total_messages DESC
		LIMIT $2
	`, fmt.Sprintf("%d", days), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StatsContactRow
	for rows.Next() {
		var row StatsContactRow
		if err := rows.Scan(&row.WAJID, &row.WAPhone, &row.WAName, &row.TotalMessages, &row.InboundCount, &row.OutboundCount); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

// ─── Bitrix Tokens ────────────────────────────────────────────────────────

func (r *Repository) UpsertBitrixToken(ctx context.Context, t *BitrixToken) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO bitrix_tokens (id, domain, client_id, access_token, refresh_token, expires_at, scope)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (domain, client_id) DO UPDATE SET
			access_token  = EXCLUDED.access_token,
			refresh_token = EXCLUDED.refresh_token,
			expires_at    = EXCLUDED.expires_at,
			updated_at    = NOW()
	`, t.ID, t.Domain, t.ClientID, t.AccessToken, t.RefreshToken, t.ExpiresAt, t.Scope)
	return err
}

// GetBitrixToken retorna o token para um domain+client_id específico.
// Se client_id for vazio, retorna o token mais recente do domain (compatibilidade).
func (r *Repository) GetBitrixToken(ctx context.Context, domain string) (*BitrixToken, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT id, domain, client_id, access_token, refresh_token, expires_at, scope, created_at, updated_at
		 FROM bitrix_tokens WHERE domain = $1
		 ORDER BY updated_at DESC LIMIT 1`, domain)

	var t BitrixToken
	err := row.Scan(&t.ID, &t.Domain, &t.ClientID, &t.AccessToken, &t.RefreshToken, &t.ExpiresAt, &t.Scope, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// GetBitrixTokenByClientID retorna o token para um domain+client_id específico.
func (r *Repository) GetBitrixTokenByClientID(ctx context.Context, domain, clientID string) (*BitrixToken, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT id, domain, client_id, access_token, refresh_token, expires_at, scope, created_at, updated_at
		 FROM bitrix_tokens WHERE domain = $1 AND client_id = $2`, domain, clientID)

	var t BitrixToken
	err := row.Scan(&t.ID, &t.Domain, &t.ClientID, &t.AccessToken, &t.RefreshToken, &t.ExpiresAt, &t.Scope, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// ─── Bitrix Accounts ──────────────────────────────────────────────────────

func (r *Repository) UpsertBitrixAccount(ctx context.Context, a *BitrixAccount) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO bitrix_accounts
			(id, session_jid, domain, client_id, client_secret, open_line_id, connector_id, redirect_uri, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (session_jid) DO UPDATE SET
			domain        = EXCLUDED.domain,
			client_id     = EXCLUDED.client_id,
			client_secret = EXCLUDED.client_secret,
			open_line_id  = EXCLUDED.open_line_id,
			connector_id  = EXCLUDED.connector_id,
			redirect_uri  = EXCLUDED.redirect_uri,
			status        = EXCLUDED.status,
			updated_at    = NOW()
	`, a.ID, a.SessionJID, a.Domain, a.ClientID, a.ClientSecret,
		a.OpenLineID, a.ConnectorID, a.RedirectURI, a.Status)
	return err
}

func (r *Repository) GetBitrixAccountByJID(ctx context.Context, sessionJID string) (*BitrixAccount, error) {
	// Match pelo número de telefone (parte antes de ':'), ignorando o device suffix que muda a cada reconexão.
	// Ex: "5519910001772:19@s.whatsapp.net" bate em "5519910001772:18@s.whatsapp.net" salvo no banco.
	row := r.pool.QueryRow(ctx, `
		SELECT id, session_jid, domain, client_id, client_secret, open_line_id,
		       connector_id, redirect_uri, status, created_at, updated_at
		FROM bitrix_accounts
		WHERE SPLIT_PART(session_jid, ':', 1) = SPLIT_PART($1, ':', 1)
		ORDER BY updated_at DESC
		LIMIT 1`, sessionJID)

	var a BitrixAccount
	err := row.Scan(&a.ID, &a.SessionJID, &a.Domain, &a.ClientID, &a.ClientSecret,
		&a.OpenLineID, &a.ConnectorID, &a.RedirectURI, &a.Status, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (r *Repository) ListBitrixAccounts(ctx context.Context) ([]*BitrixAccount, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, session_jid, domain, client_id, client_secret, open_line_id,
		       connector_id, redirect_uri, status, created_at, updated_at
		FROM bitrix_accounts ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accounts []*BitrixAccount
	for rows.Next() {
		var a BitrixAccount
		if err := rows.Scan(&a.ID, &a.SessionJID, &a.Domain, &a.ClientID, &a.ClientSecret,
			&a.OpenLineID, &a.ConnectorID, &a.RedirectURI, &a.Status, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		accounts = append(accounts, &a)
	}
	return accounts, nil
}

func (r *Repository) UpdateBitrixAccountStatus(ctx context.Context, sessionJID string, status BitrixAccountStatus) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE bitrix_accounts SET status = $1, updated_at = NOW()
		 WHERE SPLIT_PART(session_jid, ':', 1) = SPLIT_PART($2, ':', 1)`,
		status, sessionJID)
	return err
}

func (r *Repository) DeleteBitrixAccount(ctx context.Context, sessionJID string) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM bitrix_accounts WHERE SPLIT_PART(session_jid, ':', 1) = SPLIT_PART($1, ':', 1)`,
		sessionJID)
	return err
}

// ─── Bitrix Portals (Partner App) ─────────────────────────────────────────

// UpsertBitrixPortal salva ou atualiza os dados de um portal instalado via Marketplace.
func (r *Repository) UpsertBitrixPortal(ctx context.Context, p *BitrixPortal) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO bitrix_portals
			(id, domain, access_token, refresh_token, expires_at, member_id, connector_id, open_line_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (domain) DO UPDATE SET
			access_token  = EXCLUDED.access_token,
			refresh_token = EXCLUDED.refresh_token,
			expires_at    = EXCLUDED.expires_at,
			member_id     = EXCLUDED.member_id,
			connector_id  = COALESCE(NULLIF(EXCLUDED.connector_id,''), bitrix_portals.connector_id),
			open_line_id  = CASE WHEN EXCLUDED.open_line_id > 0 THEN EXCLUDED.open_line_id ELSE bitrix_portals.open_line_id END,
			updated_at    = NOW()
	`, p.ID, p.Domain, p.AccessToken, p.RefreshToken, p.ExpiresAt, p.MemberID, p.ConnectorID, p.OpenLineID)
	return err
}

// GetBitrixPortalByMemberID retorna o portal pelo member_id único do Bitrix.
// Usado para migrar o registro placeholder criado no install (quando domain ainda não era conhecido).
func (r *Repository) GetBitrixPortalByMemberID(ctx context.Context, memberID string) (*BitrixPortal, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, domain, access_token, refresh_token, expires_at, member_id,
		       connector_id, open_line_id, installed_at, updated_at
		FROM bitrix_portals WHERE member_id = $1
		ORDER BY installed_at DESC LIMIT 1`, memberID)

	var p BitrixPortal
	err := row.Scan(&p.ID, &p.Domain, &p.AccessToken, &p.RefreshToken, &p.ExpiresAt,
		&p.MemberID, &p.ConnectorID, &p.OpenLineID, &p.InstalledAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// UpdateBitrixPortalDomain atualiza o domain de um portal identificado pelo member_id.
// Usado quando o domain real chega via BX24.getAuth() após o install.
func (r *Repository) UpdateBitrixPortalDomain(ctx context.Context, memberID, newDomain string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE bitrix_portals SET domain = $1, updated_at = NOW() WHERE member_id = $2`,
		newDomain, memberID)
	return err
}

// GetBitrixPortalByDomain retorna o portal pelo domain (sem https://).
func (r *Repository) GetBitrixPortalByDomain(ctx context.Context, domain string) (*BitrixPortal, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, domain, access_token, refresh_token, expires_at, member_id,
		       connector_id, open_line_id, installed_at, updated_at
		FROM bitrix_portals WHERE domain = $1`, domain)

	var p BitrixPortal
	err := row.Scan(&p.ID, &p.Domain, &p.AccessToken, &p.RefreshToken, &p.ExpiresAt,
		&p.MemberID, &p.ConnectorID, &p.OpenLineID, &p.InstalledAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// ListBitrixPortals retorna todos os portais instalados.
func (r *Repository) ListBitrixPortals(ctx context.Context) ([]*BitrixPortal, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, domain, access_token, refresh_token, expires_at, member_id,
		       connector_id, open_line_id, installed_at, updated_at
		FROM bitrix_portals ORDER BY installed_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var portals []*BitrixPortal
	for rows.Next() {
		var p BitrixPortal
		if err := rows.Scan(&p.ID, &p.Domain, &p.AccessToken, &p.RefreshToken, &p.ExpiresAt,
			&p.MemberID, &p.ConnectorID, &p.OpenLineID, &p.InstalledAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		portals = append(portals, &p)
	}
	return portals, nil
}
