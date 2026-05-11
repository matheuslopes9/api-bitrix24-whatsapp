package db

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool *pgxpool.Pool
}

// Pool expõe o pgxpool para diagnostico (usado pelo painel admin).
func (r *Repository) Pool() *pgxpool.Pool {
	return r.pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// ─── Sessions ──────────────────────────────────────────────────────────────

// sessionColumns lista todas as colunas escalares de whatsapp_sessions na ordem
// usada pelos helpers scanSession*. session_file é nullable (NULL para Cloud API).
const sessionColumns = `id, jid, phone, display_name, status,
		COALESCE(session_file, '') AS session_file,
		COALESCE(type, 'qr') AS type,
		COALESCE(cloud_phone_number_id, '') AS cloud_phone_number_id,
		COALESCE(cloud_waba_id, '') AS cloud_waba_id,
		COALESCE(cloud_access_token, '') AS cloud_access_token,
		COALESCE(cloud_verify_token, '') AS cloud_verify_token,
		COALESCE(cloud_app_secret, '') AS cloud_app_secret,
		COALESCE(cloud_display_phone, '') AS cloud_display_phone,
		created_at, last_seen`

// scanSessionRow lê uma linha da tabela whatsapp_sessions com todas as colunas.
type sessionRowScanner interface {
	Scan(dest ...any) error
}

func scanSession(r sessionRowScanner, s *WhatsAppSession) error {
	return r.Scan(
		&s.ID, &s.JID, &s.Phone, &s.DisplayName, &s.Status,
		&s.SessionFile, &s.Type,
		&s.CloudPhoneNumberID, &s.CloudWABAID, &s.CloudAccessToken,
		&s.CloudVerifyToken, &s.CloudAppSecret, &s.CloudDisplayPhone,
		&s.CreatedAt, &s.LastSeen,
	)
}

func (r *Repository) UpsertSession(ctx context.Context, s *WhatsAppSession) error {
	if s.Type == "" {
		s.Type = SessionTypeQR
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO whatsapp_sessions
			(id, jid, phone, display_name, status, session_file, type,
			 cloud_phone_number_id, cloud_waba_id, cloud_access_token,
			 cloud_verify_token, cloud_app_secret, cloud_display_phone)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT (jid) DO UPDATE SET
			id                    = EXCLUDED.id,
			display_name          = EXCLUDED.display_name,
			status                = EXCLUDED.status,
			session_file          = EXCLUDED.session_file,
			type                  = EXCLUDED.type,
			cloud_phone_number_id = EXCLUDED.cloud_phone_number_id,
			cloud_waba_id         = EXCLUDED.cloud_waba_id,
			cloud_access_token    = EXCLUDED.cloud_access_token,
			cloud_verify_token    = EXCLUDED.cloud_verify_token,
			cloud_app_secret      = EXCLUDED.cloud_app_secret,
			cloud_display_phone   = EXCLUDED.cloud_display_phone,
			last_seen             = NOW()
	`,
		s.ID, s.JID, s.Phone, s.DisplayName, s.Status, s.SessionFile, s.Type,
		s.CloudPhoneNumberID, s.CloudWABAID, s.CloudAccessToken,
		s.CloudVerifyToken, s.CloudAppSecret, s.CloudDisplayPhone,
	)
	return err
}

func (r *Repository) GetSessionByJID(ctx context.Context, jid string) (*WhatsAppSession, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT `+sessionColumns+` FROM whatsapp_sessions WHERE jid = $1`, jid)
	var s WhatsAppSession
	if err := scanSession(row, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// GetSessionByCloudPhoneID localiza uma sessão Cloud API pelo phone_number_id.
func (r *Repository) GetSessionByCloudPhoneID(ctx context.Context, phoneID string) (*WhatsAppSession, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT `+sessionColumns+` FROM whatsapp_sessions
		 WHERE type = 'cloud_api' AND cloud_phone_number_id = $1`, phoneID)
	var s WhatsAppSession
	if err := scanSession(row, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *Repository) ListActiveSessions(ctx context.Context) ([]*WhatsAppSession, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+sessionColumns+` FROM whatsapp_sessions WHERE status = 'active'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []*WhatsAppSession
	for rows.Next() {
		var s WhatsAppSession
		if err := scanSession(rows, &s); err != nil {
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
		`SELECT `+sessionColumns+` FROM whatsapp_sessions WHERE status != 'banned'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []WhatsAppSession
	for rows.Next() {
		var s WhatsAppSession
		if err := scanSession(rows, &s); err != nil {
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
		`SELECT `+sessionColumns+` FROM whatsapp_sessions WHERE id = $1`, id)
	var s WhatsAppSession
	if err := scanSession(row, &s); err != nil {
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

// UpsertLIDPhoneMap registra (ou atualiza) o mapeamento LID -> telefone.
// Chamado quando recebemos uma msg do cliente onde Sender é @lid mas
// SenderAlt tem o JID com telefone real.
func (r *Repository) UpsertLIDPhoneMap(ctx context.Context, lidJID, phoneJID, phone string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO lid_phone_map (lid_jid, phone_jid, phone, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (lid_jid) DO UPDATE SET
			phone_jid  = EXCLUDED.phone_jid,
			phone      = EXCLUDED.phone,
			updated_at = NOW()
	`, lidJID, phoneJID, phone)
	return err
}

// RawQueryLIDMap retorna todos os mapeamentos LID -> telefone (para diagnóstico).
func (r *Repository) RawQueryLIDMap(ctx context.Context) ([]map[string]interface{}, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT lid_jid, phone_jid, phone, updated_at
		  FROM lid_phone_map
		 ORDER BY updated_at DESC
		 LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]interface{}{}
	for rows.Next() {
		var lid, phoneJID, phone string
		var updated time.Time
		if err := rows.Scan(&lid, &phoneJID, &phone, &updated); err != nil {
			return nil, err
		}
		out = append(out, map[string]interface{}{
			"lid_jid":    lid,
			"phone_jid":  phoneJID,
			"phone":      phone,
			"updated_at": updated.Format("2006-01-02 15:04:05"),
		})
	}
	return out, rows.Err()
}

// GetPhoneByLID retorna o telefone real associado a um JID @lid.
// Retorna ("", nil) se não há mapeamento (não é erro).
func (r *Repository) GetPhoneByLID(ctx context.Context, lidJID string) (string, error) {
	var phone string
	err := r.pool.QueryRow(ctx,
		`SELECT phone FROM lid_phone_map WHERE lid_jid = $1`, lidJID).Scan(&phone)
	if err != nil {
		// pgx retorna pgx.ErrNoRows se não achou — tratamos como vazio sem erro
		if err.Error() == "no rows in result set" {
			return "", nil
		}
		return "", err
	}
	return phone, nil
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
	Date            time.Time `db:"date"              json:"date"`
	TotalMessages   int64     `db:"total_messages"    json:"total_messages"`
	InboundCount    int64     `db:"inbound_count"     json:"inbound_count"`
	OutboundCount   int64     `db:"outbound_count"    json:"outbound_count"`
	AvgResponseSecs float64   `db:"avg_response_secs" json:"avg_response_secs"`
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
	SessionJID    string `db:"session_jid"    json:"session_jid"`
	Phone         string `db:"phone"          json:"phone"`
	TotalMessages int64  `db:"total_messages" json:"total_messages"`
	InboundCount  int64  `db:"inbound_count"  json:"inbound_count"`
	OutboundCount int64  `db:"outbound_count" json:"outbound_count"`
	FailedCount   int64  `db:"failed_count"   json:"failed_count"`
}

// StatsTypeRow — mensagens agrupadas por tipo (text, image, audio…)
type StatsTypeRow struct {
	MessageType   string `db:"message_type"   json:"message_type"`
	TotalMessages int64  `db:"total_messages" json:"total_messages"`
	InboundCount  int64  `db:"inbound_count"  json:"inbound_count"`
	OutboundCount int64  `db:"outbound_count" json:"outbound_count"`
}

// StatsHourRow — volume por hora do dia
type StatsHourRow struct {
	Hour          int   `db:"hour"           json:"hour"`
	TotalMessages int64 `db:"total_messages" json:"total_messages"`
}

// StatsContactRow — top contatos por volume
type StatsContactRow struct {
	WAJID         string `db:"wa_jid"         json:"wa_jid"`
	WAPhone       string `db:"wa_phone"       json:"wa_phone"`
	WAName        string `db:"wa_name"        json:"wa_name"`
	TotalMessages int64  `db:"total_messages" json:"total_messages"`
	InboundCount  int64  `db:"inbound_count"  json:"inbound_count"`
	OutboundCount int64  `db:"outbound_count" json:"outbound_count"`
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
	// Match estratégico:
	//  - Cloud API ("cloud:<phone_id>@..."): match EXATO pelo session_jid completo,
	//    pois SPLIT_PART(...,':',1) retorna "cloud" para todas as sessões Cloud
	//    e causaria colisão entre múltiplas contas oficiais.
	//  - QR (whatsmeow): match pelo número antes de ':' (tolera device suffix
	//    que muda a cada reconexão, ex: ":19" vs ":18").
	if strings.HasPrefix(sessionJID, "cloud:") {
		row := r.pool.QueryRow(ctx, `
			SELECT id, session_jid, domain, client_id, client_secret, open_line_id,
			       connector_id, redirect_uri, status, created_at, updated_at
			FROM bitrix_accounts
			WHERE session_jid = $1
			ORDER BY updated_at DESC
			LIMIT 1`, sessionJID)
		var a BitrixAccount
		if err := row.Scan(&a.ID, &a.SessionJID, &a.Domain, &a.ClientID, &a.ClientSecret,
			&a.OpenLineID, &a.ConnectorID, &a.RedirectURI, &a.Status, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		return &a, nil
	}

	row := r.pool.QueryRow(ctx, `
		SELECT id, session_jid, domain, client_id, client_secret, open_line_id,
		       connector_id, redirect_uri, status, created_at, updated_at
		FROM bitrix_accounts
		WHERE session_jid NOT LIKE 'cloud:%'
		  AND SPLIT_PART(session_jid, ':', 1) = SPLIT_PART($1, ':', 1)
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

// GetBitrixAccountByConnectorID localiza a sessão dona de um connector_id no Bitrix.
// Usado pelo caminho Cloud no bitrixConnectorEvent: quando o evento chega com
// connector="wa_cloud_<phone_id>", esta função retorna o bitrix_account com o
// SessionJID Cloud correspondente. Lookup exato — sem ambiguidade entre sessões.
func (r *Repository) GetBitrixAccountByConnectorID(ctx context.Context, connectorID string) (*BitrixAccount, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, session_jid, domain, client_id, client_secret, open_line_id,
		       connector_id, redirect_uri, status, created_at, updated_at
		FROM bitrix_accounts
		WHERE connector_id = $1
		ORDER BY updated_at DESC
		LIMIT 1`, connectorID)
	var a BitrixAccount
	if err := row.Scan(&a.ID, &a.SessionJID, &a.Domain, &a.ClientID, &a.ClientSecret,
		&a.OpenLineID, &a.ConnectorID, &a.RedirectURI, &a.Status, &a.CreatedAt, &a.UpdatedAt); err != nil {
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
	if strings.HasPrefix(sessionJID, "cloud:") {
		_, err := r.pool.Exec(ctx,
			`UPDATE bitrix_accounts SET status = $1, updated_at = NOW()
			 WHERE session_jid = $2`,
			status, sessionJID)
		return err
	}
	_, err := r.pool.Exec(ctx,
		`UPDATE bitrix_accounts SET status = $1, updated_at = NOW()
		 WHERE session_jid NOT LIKE 'cloud:%'
		   AND SPLIT_PART(session_jid, ':', 1) = SPLIT_PART($2, ':', 1)`,
		status, sessionJID)
	return err
}

func (r *Repository) DeleteBitrixAccount(ctx context.Context, sessionJID string) error {
	if strings.HasPrefix(sessionJID, "cloud:") {
		_, err := r.pool.Exec(ctx,
			`DELETE FROM bitrix_accounts WHERE session_jid = $1`, sessionJID)
		return err
	}
	_, err := r.pool.Exec(ctx,
		`DELETE FROM bitrix_accounts
		 WHERE session_jid NOT LIKE 'cloud:%'
		   AND SPLIT_PART(session_jid, ':', 1) = SPLIT_PART($1, ':', 1)`,
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

// DomainSessionCounts retorna QR e Cloud por dominio em UMA query — usado
// pelo painel admin para evitar N+1 sobre N portais.
type DomainSessionCounts struct {
	Domain string
	QR     int
	Cloud  int
}

// AllDomainSessionCounts agrupa sessoes WA por bitrix_account.domain
// (QR = jid sem prefixo "cloud:", Cloud = jid com prefixo). Uma unica query.
//
// O JOIN normaliza:
//   - device suffix (":NN@") em jid (whatsmeow renumera ao reconectar)
//   - protocolo/www/trailing slash em domain (bitrix_accounts grava
//     "https://x" enquanto bitrix_portals grava "x" — formatos diferentes)
//
// A chave do map retornado é o domain JÁ NORMALIZADO (lowercase, sem proto)
// — o caller (admin) também normaliza p.Domain antes de buscar.
func (r *Repository) AllDomainSessionCounts(ctx context.Context) (map[string]DomainSessionCounts, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT LOWER(REGEXP_REPLACE(ba.domain, '^https?://(www\.)?', '')) AS d,
			COUNT(*) FILTER (WHERE REGEXP_REPLACE(s.jid, ':[0-9]+@', '@') NOT LIKE 'cloud:%') AS qr_count,
			COUNT(*) FILTER (WHERE REGEXP_REPLACE(s.jid, ':[0-9]+@', '@') LIKE 'cloud:%') AS cloud_count
		FROM bitrix_accounts ba
		JOIN whatsapp_sessions s
		  ON REGEXP_REPLACE(s.jid, ':[0-9]+@', '@') = REGEXP_REPLACE(ba.session_jid, ':[0-9]+@', '@')
		GROUP BY d`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]DomainSessionCounts{}
	for rows.Next() {
		var d DomainSessionCounts
		if err := rows.Scan(&d.Domain, &d.QR, &d.Cloud); err != nil {
			return nil, err
		}
		out[d.Domain] = d
	}
	return out, rows.Err()
}

// DomainMessageCounts agrupa contagem de msgs por dominio desde `since`.
type DomainMessageCounts struct {
	Domain   string
	Inbound  int
	Outbound int
}

// AllDomainTokenExpiry retorna o expires_at do token MAIS RECENTE de cada
// dominio em bitrix_tokens. Esta tabela é atualizada a cada refresh (TTL 1h
// do access_token Bitrix24), entao reflete o estado real da autenticacao.
// Diferente de bitrix_portals.expires_at, que só é atualizado no install
// inicial e fica desatualizado depois.
//
// Chave do map normalizada (sem https://, sem www., lowercase) — mesmo
// formato usado por AllDomainSessionCounts/AllDomainMessageCounts.
func (r *Repository) AllDomainTokenExpiry(ctx context.Context) (map[string]time.Time, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT LOWER(REGEXP_REPLACE(domain, '^https?://(www\.)?', '')) AS d,
		       MAX(expires_at) AS exp
		FROM bitrix_tokens
		GROUP BY d`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var domain string
		var exp time.Time
		if err := rows.Scan(&domain, &exp); err != nil {
			return nil, err
		}
		out[domain] = exp
	}
	return out, rows.Err()
}

// AllDomainMessageCounts retorna inbound/outbound de TODOS os dominios em UMA query.
//
// JOIN/agrupamento normaliza jid (device suffix) e domain (protocolo/www) —
// mesma justificativa de AllDomainSessionCounts.
func (r *Repository) AllDomainMessageCounts(ctx context.Context, since time.Time) (map[string]DomainMessageCounts, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT LOWER(REGEXP_REPLACE(ba.domain, '^https?://(www\.)?', '')) AS d,
			COUNT(*) FILTER (WHERE m.direction = 'inbound') AS inbound,
			COUNT(*) FILTER (WHERE m.direction = 'outbound') AS outbound
		FROM messages m
		JOIN whatsapp_sessions s ON s.id = m.session_id
		JOIN bitrix_accounts ba
		  ON REGEXP_REPLACE(s.jid, ':[0-9]+@', '@') = REGEXP_REPLACE(ba.session_jid, ':[0-9]+@', '@')
		WHERE m.created_at >= $1
		GROUP BY d`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]DomainMessageCounts{}
	for rows.Next() {
		var d DomainMessageCounts
		if err := rows.Scan(&d.Domain, &d.Inbound, &d.Outbound); err != nil {
			return nil, err
		}
		out[d.Domain] = d
	}
	return out, rows.Err()
}

// CountSessionsByDomain — versão por-domínio (mantida para uso pontual).
func (r *Repository) CountSessionsByDomain(ctx context.Context, domain string) (qr, cloud int, err error) {
	row := r.pool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE s.jid NOT LIKE 'cloud:%') AS qr_count,
			COUNT(*) FILTER (WHERE s.jid LIKE 'cloud:%') AS cloud_count
		FROM bitrix_accounts ba
		JOIN whatsapp_sessions s ON s.jid = ba.session_jid
		WHERE ba.domain = $1`, domain)
	err = row.Scan(&qr, &cloud)
	return
}

// CountMessagesByDomain — versão por-domínio (mantida para uso pontual).
func (r *Repository) CountMessagesByDomain(ctx context.Context, domain string, since time.Time) (inbound, outbound int, err error) {
	row := r.pool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE m.direction = 'inbound') AS inbound,
			COUNT(*) FILTER (WHERE m.direction = 'outbound') AS outbound
		FROM messages m
		JOIN whatsapp_sessions s ON s.id = m.session_id
		JOIN bitrix_accounts ba ON ba.session_jid = s.jid
		WHERE ba.domain = $1 AND m.created_at >= $2`, domain, since)
	err = row.Scan(&inbound, &outbound)
	return
}
