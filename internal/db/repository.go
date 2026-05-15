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

// ListActiveSessionsByDomain retorna sessões ativas (qr + cloud) vinculadas
// ao domain via bitrix_accounts. Usada pelo CRM tab para o seletor de sessoes
// — antes ListSessions() do manager so devolvia whatsmeow, deixando tenants
// Cloud sempre como "Desconectado".
func (r *Repository) ListActiveSessionsByDomain(ctx context.Context, domain string) ([]*WhatsAppSession, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+sessionColumns+`
		  FROM whatsapp_sessions ws
		 WHERE ws.status = 'active'
		   AND ws.jid IN (SELECT session_jid FROM bitrix_accounts WHERE domain = $1)
		 ORDER BY ws.last_seen DESC`, domain)
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

// ListSessionsByDomain retorna sessoes "conhecidas" pelo dominio,
// incluindo as antigas/desconectadas. Usada pelo Historico no /dashboard.
//
// Duas fontes (UNION):
//   1. Sessoes atualmente vinculadas via bitrix_accounts.domain (caminho
//      normal — sessoes que o tenant operou recentemente).
//   2. Sessoes que aparecem em messages historicamente — capturadas via
//      INTERSECT entre os JIDs distintos das msgs e as bitrix_accounts
//      ja vistas pra esse dominio em qualquer momento (snapshot historico
//      nao existe, entao filtramos por sessoes cujo JID aparece em msgs
//      com session_id apontando pra uma sessao "do dominio em algum
//      momento" — mas como nao temos esse log, ficamos com (1) + heuristic
//      adicional via JIDs de mensagens cuja session_id == sessoes da (1)
//      OU cujo JID == session_jid de (1)).
//
// Simplificacao: pra cobrir QR -> Cloud no mesmo dominio sem complicar
// schema, listamos TODAS sessoes nao banidas se o dominio tem ao menos
// 1 bitrix_account. O tenant ve sessoes alheias so se o bitrix_account
// foi compartilhado — risco aceito em troca de funcionalidade. Multi
// tenant isolation ja se ergue via permissions (so super-admin acessa
// /dashboard).
func (r *Repository) ListSessionsByDomain(ctx context.Context, domain string) ([]*WhatsAppSession, error) {
	// Confirma que dominio existe em bitrix_accounts (sanity)
	var hasDomain bool
	if err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM bitrix_accounts WHERE domain = $1)`,
		domain).Scan(&hasDomain); err != nil {
		return nil, err
	}
	if !hasDomain {
		return nil, nil
	}

	// Tradicional: sessoes ainda existentes em whatsapp_sessions (nao banidas).
	// Histórico precisa funcionar mesmo se o registro de sessao foi deletado
	// (master desconectou + admin limpou). Pra cobrir esse caso, retornamos
	// tambem "sessoes sombra" — JIDs unicos que aparecem em messages mas
	// nao existem mais em whatsapp_sessions. ON DELETE SET NULL ja preserva
	// as msgs (FK), e aqui derivamos a sessao apenas pra alimentar o dropdown.
	rows, err := r.pool.Query(ctx, `
		WITH real AS (
			SELECT `+sessionColumns+`
			  FROM whatsapp_sessions ws
			 WHERE ws.status <> 'banned'
		),
		shadow_jids AS (
			-- JIDs unicos que aparecem em messages mas NAO em whatsapp_sessions.
			-- Limita a 30d retroativo pra nao ressuscitar lixo antigo.
			SELECT DISTINCT j AS jid
			  FROM (
				SELECT from_jid AS j FROM messages
				 WHERE direction = 'outbound'
				   AND created_at > NOW() - INTERVAL '1 year'
				UNION
				SELECT to_jid   AS j FROM messages
				 WHERE direction = 'inbound'
				   AND created_at > NOW() - INTERVAL '1 year'
			  ) all_jids
			 WHERE j LIKE '%@%'
			   AND j NOT IN (SELECT jid FROM whatsapp_sessions)
			   AND j NOT LIKE '%@lid'
		),
		shadow AS (
			-- Mesma ordem/forma de sessionColumns, apenas com valores derivados
			-- do JID. Status='disconnected' marca como "sessao sombra" no UI.
			SELECT
				gen_random_uuid()         AS id,
				sj.jid                    AS jid,
				CASE
					WHEN sj.jid LIKE 'cloud:%' THEN ''
					ELSE SPLIT_PART(SPLIT_PART(sj.jid, '@', 1), ':', 1)
				END                       AS phone,
				''                        AS display_name,
				'disconnected'::text      AS status,
				''                        AS session_file,
				CASE
					WHEN sj.jid LIKE 'cloud:%' THEN 'cloud_api'
					ELSE 'qr'
				END                       AS type,
				''                        AS cloud_phone_number_id,
				''                        AS cloud_waba_id,
				''                        AS cloud_access_token,
				''                        AS cloud_verify_token,
				''                        AS cloud_app_secret,
				''                        AS cloud_display_phone,
				NOW()                     AS created_at,
				NOW()                     AS last_seen
			FROM shadow_jids sj
		)
		SELECT * FROM (
			SELECT * FROM real
			UNION ALL
			SELECT * FROM shadow
		) combined
		ORDER BY (combined.status = 'active') DESC, combined.last_seen DESC`)
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

// DeleteSessionsByPhone remove TODAS as rows whatsapp_sessions cujo phone bate,
// independente do device suffix (devices reconectados criam rows novas).
// Usado pelo Disconnect para evitar lixo persistente.
func (r *Repository) DeleteSessionsByPhone(ctx context.Context, phone string) error {
	if phone == "" {
		return nil
	}
	_, err := r.pool.Exec(ctx, `DELETE FROM whatsapp_sessions WHERE phone = $1`, phone)
	return err
}

// DeleteSessionByJID — alias para uso explicito por callers que ja sabem o jid exato.
func (r *Repository) DeleteSessionByJID(ctx context.Context, jid string) error {
	return r.DeleteSession(ctx, jid)
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
// Usa make_interval (tipo-safe) — versoes novas do pgx nao convertem int -> text
// automaticamente, entao "$1 || ' days'" falha com "cannot find encode plan".
func (r *Repository) DeleteOldMessages(ctx context.Context, retentionDays int) (int64, error) {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM messages WHERE created_at < NOW() - make_interval(days => $1)`,
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

// HistoryConversation representa uma linha do menu "Historico" no dashboard:
// uma conversa = um peer (telefone) trocando msgs com uma sessao especifica.
type HistoryConversation struct {
	Phone        string    `json:"phone"`
	LastMessage  string    `json:"last_message"`
	LastAt       time.Time `json:"last_at"`
	LastDir      string    `json:"last_direction"`
	LastType     string    `json:"last_message_type"`
	Total        int       `json:"total"`
}

// ListHistoryConversations agrupa msgs de uma sessao por "peer" (o JID
// oposto ao session_jid em cada mensagem) e retorna a ultima msg, data e
// total. Usada pela aba Historico do /dashboard.
//
// Lookup hibrido — via session_id (FK pra whatsapp_sessions.id) E via
// match flexivel de from_jid/to_jid. Necessario porque:
//   - Algumas msgs antigas tem session_id NULL.
//   - JIDs Cloud no banco aparecem em 3 formas: "cloud:<id>@s.whatsapp.net"
//     (formato novo), "cloud@s.whatsapp.net" (bug stripDeviceSuffix antigo)
//     e "<id>@s.whatsapp.net" (legado da v1).
//   - JIDs QR podem ter ":NN" device suffix ou nao.
//
// peer_phone = o numero do cliente (sempre do lado "@s.whatsapp.net" oposto).
func (r *Repository) ListHistoryConversations(ctx context.Context, sessionJID string, limit int) ([]HistoryConversation, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := r.pool.Query(ctx, `
		WITH sess AS (
			SELECT id, jid FROM whatsapp_sessions WHERE jid = $1 LIMIT 1
		),
		peers AS (
			SELECT
				CASE WHEN direction = 'outbound' THEN to_jid ELSE from_jid END AS peer_jid,
				content,
				message_type,
				direction,
				created_at
			FROM messages m
			WHERE
				-- 1) match por FK session_id (caminho mais confiavel)
				(m.session_id IS NOT NULL AND m.session_id = (SELECT id FROM sess))
				OR
				-- 2) match por JID literal (cobre legado sem session_id)
				(direction = 'outbound' AND (
					m.from_jid = $1
					OR REGEXP_REPLACE(m.from_jid, ':[0-9]+@', '@') = REGEXP_REPLACE($1, ':[0-9]+@', '@')
				))
				OR
				(direction = 'inbound'  AND (
					m.to_jid   = $1
					OR REGEXP_REPLACE(m.to_jid,   ':[0-9]+@', '@') = REGEXP_REPLACE($1, ':[0-9]+@', '@')
				))
		),
		peers_norm AS (
			SELECT
				SPLIT_PART(SPLIT_PART(peer_jid, '@', 1), ':', 1) AS phone,
				content,
				message_type,
				direction,
				created_at,
				ROW_NUMBER() OVER (
					PARTITION BY SPLIT_PART(SPLIT_PART(peer_jid, '@', 1), ':', 1)
					ORDER BY created_at DESC
				) AS rn,
				COUNT(*) OVER (
					PARTITION BY SPLIT_PART(SPLIT_PART(peer_jid, '@', 1), ':', 1)
				) AS total
			FROM peers
			WHERE peer_jid LIKE '%@%'
			  AND peer_jid NOT LIKE 'cloud%@%'  -- nao listar a propria sessao como "conversa"
		)
		SELECT phone, COALESCE(content,''), message_type, direction, created_at, total
		  FROM peers_norm
		 WHERE rn = 1
		   AND phone <> ''
		   AND phone ~ '^[0-9]+$'
		 ORDER BY created_at DESC
		 LIMIT $2
	`, sessionJID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]HistoryConversation, 0, 64)
	for rows.Next() {
		var c HistoryConversation
		if err := rows.Scan(&c.Phone, &c.LastMessage, &c.LastType, &c.LastDir, &c.LastAt, &c.Total); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
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
	Kind          string `db:"kind"           json:"kind"` // "cloud" ou "qr"
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
	// Agrupa pelo telefone do "nosso" lado:
	//   - outbound: from_jid (a sessao enviou)
	//   - inbound:  to_jid   (a sessao recebeu)
	// Strip do device suffix (":NN@") e do prefixo "cloud:" para que
	// reconexoes QR/Cloud nao quebrem o agrupamento. Quando whatsapp_sessions
	// ainda existe, pega phone/jid dela; senão extrai do digit da chave.
	rows, err := r.pool.Query(ctx, `
		WITH msg_raw AS (
			-- JID original (com prefixo cloud: e device suffix) — usado para
			-- detectar Kind. Versao normalizada serve so para agrupar.
			SELECT
				CASE WHEN direction = 'outbound' THEN from_jid ELSE to_jid END AS our_jid,
				direction,
				status
			FROM messages
			WHERE created_at >= NOW() - ($1 || ' days')::interval
			  AND CASE WHEN direction = 'outbound' THEN from_jid ELSE to_jid END IS NOT NULL
			  AND CASE WHEN direction = 'outbound' THEN from_jid ELSE to_jid END != ''
		),
		msg_norm AS (
			SELECT
				-- Bug historico: stripDeviceSuffix() removia o ":" do prefixo
				-- "cloud:" e gravava "cloud@s.whatsapp.net" no banco. Tratamos
				-- esse caso aqui como sinonimo de Cloud (sem id especifico).
				CASE
					WHEN our_jid = 'cloud@s.whatsapp.net' THEN 'cloud-legacy'
					WHEN our_jid LIKE 'cloud:%' THEN
						REGEXP_REPLACE(REPLACE(our_jid, 'cloud:', ''), ':[0-9]+@', '@')
					ELSE REGEXP_REPLACE(our_jid, ':[0-9]+@', '@')
				END AS norm_jid,
				CASE
					WHEN our_jid LIKE 'cloud:%' OR our_jid = 'cloud@s.whatsapp.net' THEN 'cloud'
					ELSE 'qr'
				END AS kind,
				direction,
				status
			FROM msg_raw
			WHERE our_jid LIKE '%@%'
		)
		SELECT
			COALESCE(NULLIF(MAX(s.jid), ''), m.norm_jid)                        AS session_jid,
			-- phone preferencialmente do banco (se numerico). Senao, para
			-- linha 'cloud-legacy' tenta pegar a unica sessao Cloud ativa;
			-- para QR, usa SPLIT_PART do JID puro.
			COALESCE(
				NULLIF(MAX(s.phone) FILTER (WHERE s.phone ~ '^[0-9]+$'), ''),
				CASE WHEN MAX(m.kind) = 'cloud' THEN (
					SELECT phone FROM whatsapp_sessions
					WHERE type = 'cloud_api' AND status = 'active' AND phone ~ '^[0-9]+$'
					ORDER BY created_at DESC LIMIT 1
				) END,
				NULLIF(SPLIT_PART(m.norm_jid, '@', 1), 'cloud-legacy'),
				SPLIT_PART(m.norm_jid, '@', 1)
			)                                                                   AS phone,
			MAX(m.kind)                                                         AS kind,
			COUNT(*)                                                            AS total_messages,
			SUM(CASE WHEN m.direction = 'inbound'  THEN 1 ELSE 0 END)           AS inbound_count,
			SUM(CASE WHEN m.direction = 'outbound' THEN 1 ELSE 0 END)           AS outbound_count,
			SUM(CASE WHEN m.status    = 'failed'   THEN 1 ELSE 0 END)           AS failed_count
		FROM msg_norm m
		LEFT JOIN whatsapp_sessions s
			ON (m.norm_jid <> 'cloud-legacy'
				AND REGEXP_REPLACE(REPLACE(s.jid, 'cloud:', ''), ':[0-9]+@', '@') = m.norm_jid)
		WHERE m.norm_jid <> ''
		GROUP BY m.norm_jid
		ORDER BY total_messages DESC
	`, fmt.Sprintf("%d", days))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StatsSessionRow
	for rows.Next() {
		var row StatsSessionRow
		if err := rows.Scan(&row.SessionJID, &row.Phone, &row.Kind, &row.TotalMessages, &row.InboundCount, &row.OutboundCount, &row.FailedCount); err != nil {
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
	// Agrupa pelo telefone do contato (o "outro lado"):
	//   - outbound: to_jid   (nos enviamos PARA ele)
	//   - inbound:  from_jid (ele enviou PARA nos)
	// Strip device suffix e prefixo cloud:. Quando contact_mapping existe,
	// pega name/phone dele; senão exibe o numero extraído do JID normalizado.
	rows, err := r.pool.Query(ctx, `
		WITH msg_norm AS (
			SELECT
				REGEXP_REPLACE(
					REPLACE(
						CASE WHEN direction = 'outbound' THEN to_jid ELSE from_jid END,
						'cloud:', ''),
					':[0-9]+@', '@') AS norm_jid,
				direction
			FROM messages
			WHERE created_at >= NOW() - ($1 || ' days')::interval
			  AND CASE WHEN direction = 'outbound' THEN to_jid ELSE from_jid END IS NOT NULL
			  AND CASE WHEN direction = 'outbound' THEN to_jid ELSE from_jid END != ''
		)
		SELECT
			m.norm_jid                                              AS wa_jid,
			COALESCE(
				NULLIF(MAX(c.wa_phone), ''),
				NULLIF(MAX(c.wa_phone), 'cloud'),
				SPLIT_PART(m.norm_jid, '@', 1)
			)                                                       AS wa_phone,
			COALESCE(MAX(c.wa_name), '')                            AS wa_name,
			COUNT(*)                                                AS total_messages,
			SUM(CASE WHEN m.direction = 'inbound'  THEN 1 ELSE 0 END) AS inbound_count,
			SUM(CASE WHEN m.direction = 'outbound' THEN 1 ELSE 0 END) AS outbound_count
		FROM msg_norm m
		LEFT JOIN contact_mapping c
			ON REGEXP_REPLACE(REPLACE(c.wa_jid, 'cloud:', ''), ':[0-9]+@', '@') = m.norm_jid
		WHERE m.norm_jid <> '' AND m.norm_jid LIKE '%@%'
		GROUP BY m.norm_jid
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
		       connector_id, open_line_id, installed_at, updated_at,
		       COALESCE(legacy_admin_user_id, ''),
		       COALESCE(default_sms_session_jid, ''),
		       COALESCE(sms_risk_acknowledged, FALSE)
		FROM bitrix_portals WHERE member_id = $1
		ORDER BY installed_at DESC LIMIT 1`, memberID)

	var p BitrixPortal
	err := row.Scan(&p.ID, &p.Domain, &p.AccessToken, &p.RefreshToken, &p.ExpiresAt,
		&p.MemberID, &p.ConnectorID, &p.OpenLineID, &p.InstalledAt, &p.UpdatedAt,
		&p.LegacyAdminUserID, &p.DefaultSMSSessionJID, &p.SMSRiskAcknowledged)
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
		       connector_id, open_line_id, installed_at, updated_at,
		       COALESCE(legacy_admin_user_id, ''),
		       COALESCE(default_sms_session_jid, ''),
		       COALESCE(sms_risk_acknowledged, FALSE)
		FROM bitrix_portals WHERE domain = $1`, domain)

	var p BitrixPortal
	err := row.Scan(&p.ID, &p.Domain, &p.AccessToken, &p.RefreshToken, &p.ExpiresAt,
		&p.MemberID, &p.ConnectorID, &p.OpenLineID, &p.InstalledAt, &p.UpdatedAt,
		&p.LegacyAdminUserID, &p.DefaultSMSSessionJID, &p.SMSRiskAcknowledged)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// SetMasterUser define quem e' o "master user" do tenant. Politica:
//   - Se ja existe master (legacy_admin_user_id <> ''), so pode trocar se
//     callerUserID == master atual. Caso contrario retorna ErrNotMaster.
//   - Se nao existe, qualquer caller pode set (onboarding inicial).
//   - Ao salvar o master, grant wildcard automatico em crm_user_permissions
//     pra ele (session_jid='') — eh quem pode liberar outros.
func (r *Repository) SetMasterUser(ctx context.Context, domain, callerUserID, newMasterUserID, newMasterName string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var current string
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(legacy_admin_user_id, '') FROM bitrix_portals WHERE domain = $1`,
		domain).Scan(&current); err != nil {
		return fmt.Errorf("portal lookup: %w", err)
	}
	if current != "" && current != callerUserID {
		return ErrNotMaster
	}
	if _, err := tx.Exec(ctx,
		`UPDATE bitrix_portals SET legacy_admin_user_id = $1, updated_at = NOW() WHERE domain = $2`,
		newMasterUserID, domain); err != nil {
		return err
	}
	// Garantir wildcard pro novo master (libera tudo). Idempotente.
	if _, err := tx.Exec(ctx, `
		INSERT INTO crm_user_permissions (id, domain, user_id, user_name, session_jid, granted_by)
		VALUES (gen_random_uuid(), $1, $2, $3, '', 'master-onboarding')
		ON CONFLICT (domain, user_id, session_jid) DO UPDATE SET user_name = EXCLUDED.user_name`,
		domain, newMasterUserID, newMasterName); err != nil {
		return err
	}
	// Se houve troca de master, revogar wildcard do antigo (mantem grants
	// especificos se ele tinha). Politica simples: ex-master vira user normal.
	if current != "" && current != newMasterUserID {
		if _, err := tx.Exec(ctx,
			`DELETE FROM crm_user_permissions WHERE domain = $1 AND user_id = $2 AND session_jid = ''`,
			domain, current); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// SetMasterUserForce: variante de SetMasterUser SEM checagem de "caller
// precisa ser master atual". Usado pelo /admin master (super-admin) que
// tem bypass total — atende casos onde o cliente perdeu acesso ao master
// antigo. Mesmo efeito colateral: revoga wildcard do master antigo se
// houve troca, grant wildcard pro novo.
func (r *Repository) SetMasterUserForce(ctx context.Context, domain, newMasterUserID, newMasterName string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var current string
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(legacy_admin_user_id, '') FROM bitrix_portals WHERE domain = $1`,
		domain).Scan(&current); err != nil {
		return fmt.Errorf("portal lookup: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE bitrix_portals SET legacy_admin_user_id = $1, updated_at = NOW() WHERE domain = $2`,
		newMasterUserID, domain); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO crm_user_permissions (id, domain, user_id, user_name, session_jid, granted_by)
		VALUES (gen_random_uuid(), $1, $2, $3, '', 'super-admin')
		ON CONFLICT (domain, user_id, session_jid) DO UPDATE SET user_name = EXCLUDED.user_name`,
		domain, newMasterUserID, newMasterName); err != nil {
		return err
	}
	if current != "" && current != newMasterUserID {
		if _, err := tx.Exec(ctx,
			`DELETE FROM crm_user_permissions WHERE domain = $1 AND user_id = $2 AND session_jid = ''`,
			domain, current); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ListBitrixPortals retorna todos os portais instalados.
func (r *Repository) ListBitrixPortals(ctx context.Context) ([]*BitrixPortal, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, domain, access_token, refresh_token, expires_at, member_id,
		       connector_id, open_line_id, installed_at, updated_at,
		       COALESCE(legacy_admin_user_id, ''),
		       COALESCE(default_sms_session_jid, ''),
		       COALESCE(sms_risk_acknowledged, FALSE)
		FROM bitrix_portals ORDER BY installed_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var portals []*BitrixPortal
	for rows.Next() {
		var p BitrixPortal
		if err := rows.Scan(&p.ID, &p.Domain, &p.AccessToken, &p.RefreshToken, &p.ExpiresAt,
			&p.MemberID, &p.ConnectorID, &p.OpenLineID, &p.InstalledAt, &p.UpdatedAt,
			&p.LegacyAdminUserID, &p.DefaultSMSSessionJID, &p.SMSRiskAcknowledged); err != nil {
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
	// Filtra sessoes ATIVAS — sessoes banned/disconnected ficam no banco como
	// historico (whatsmeow gera uma row nova por device suffix a cada
	// reconexao). Sem o filtro, o painel conta todas e infla a contagem.
	// Usamos DISTINCT ON pelo jid normalizado para garantir 1 contagem por
	// numero/sessao logica, mesmo que tenham varios devices ativos.
	rows, err := r.pool.Query(ctx, `
		WITH active_sessions AS (
			SELECT DISTINCT ON (REGEXP_REPLACE(jid, ':[0-9]+@', '@'))
			       REGEXP_REPLACE(jid, ':[0-9]+@', '@') AS norm_jid
			FROM whatsapp_sessions
			WHERE status = 'active'
		)
		SELECT LOWER(REGEXP_REPLACE(ba.domain, '^https?://(www\.)?', '')) AS d,
			COUNT(*) FILTER (WHERE s.norm_jid NOT LIKE 'cloud:%') AS qr_count,
			COUNT(*) FILTER (WHERE s.norm_jid LIKE 'cloud:%') AS cloud_count
		FROM bitrix_accounts ba
		JOIN active_sessions s
		  ON s.norm_jid = REGEXP_REPLACE(ba.session_jid, ':[0-9]+@', '@')
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

// DeleteBannedSessions remove rows com status='banned' (sessoes antigas que
// sobraram a cada reconexao QR — whatsmeow cria uma row nova por device
// suffix). Mantem 'active' e 'disconnected'. Retorna quantas removeu.
func (r *Repository) DeleteBannedSessions(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM whatsapp_sessions WHERE status = 'banned'`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// DeletePlaceholderPortals remove portais com domain == member_id (placeholders
// nao migrados do install Partner App). Retorna quantos removeu.
func (r *Repository) DeletePlaceholderPortals(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM bitrix_portals WHERE domain = member_id`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// DeleteLegacyMessages remove mensagens "lixo" do banco:
//   - from_jid OU to_jid vazio/NULL (msgs antigas sem JID — pré-correções)
//   - from_jid = 'cloud@s.whatsapp.net' (bug historico do stripDeviceSuffix
//     que truncava 'cloud:1160...' para 'cloud')
// Retorna contagem de linhas removidas.
func (r *Repository) DeleteLegacyMessages(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM messages
		WHERE from_jid IS NULL OR from_jid = ''
		   OR to_jid IS NULL OR to_jid = ''
		   OR from_jid = 'cloud@s.whatsapp.net'
		   OR to_jid = 'cloud@s.whatsapp.net'`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// DeleteLegacyMessagesByDomain remove mensagens "lixo" SO daquele dominio Bitrix.
// Filtra messages pela JID da sessao usada (whatsapp_sessions.id = m.session_id
// → bitrix_accounts.session_jid → bitrix_accounts.domain). Mensagens com
// session_id NULL ou orfaa nao sao afetadas (use DeleteLegacyMessages global
// pra essas — sao do tempo pre-validacao).
func (r *Repository) DeleteLegacyMessagesByDomain(ctx context.Context, domain string) (int64, error) {
	// Normaliza pra comparar sem se importar com https:// / www.
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM messages
		WHERE id IN (
			SELECT m.id FROM messages m
			JOIN whatsapp_sessions s ON s.id = m.session_id
			JOIN bitrix_accounts ba
			  ON REGEXP_REPLACE(REPLACE(s.jid, 'cloud:', ''), ':[0-9]+@', '@')
			   = REGEXP_REPLACE(REPLACE(ba.session_jid, 'cloud:', ''), ':[0-9]+@', '@')
			WHERE LOWER(REGEXP_REPLACE(ba.domain, '^https?://(www\.)?', '')) = $1
			  AND (
				   m.from_jid IS NULL OR m.from_jid = ''
				OR m.to_jid IS NULL OR m.to_jid = ''
				OR m.from_jid = 'cloud@s.whatsapp.net'
				OR m.to_jid = 'cloud@s.whatsapp.net'
			  )
		)`, domain)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ─── CRM User Permissions ────────────────────────────────────────────────

// CrmUserPermission representa um usuário Bitrix24 liberado para acessar o CRM tab
// de um dominio especifico.
// ErrNotMaster: caller tentou setar master mas nao e o master atual.
// Tratado pelos handlers como 403.
var ErrNotMaster = fmt.Errorf("not_master")

// CrmUserPermission representa "user X pode usar session Y no portal Z"
// (model novo). session_jid vazio = wildcard legacy (compatibilidade com
// linhas pre-migration 018, equivale a "qualquer sessao do dominio").
type CrmUserPermission struct {
	ID         uuid.UUID `db:"id"           json:"id"`
	Domain     string    `db:"domain"       json:"domain"`
	UserID     string    `db:"user_id"      json:"user_id"`
	UserName   string    `db:"user_name"    json:"user_name"`
	SessionJID string    `db:"session_jid"  json:"session_jid"`
	GrantedAt  time.Time `db:"granted_at"   json:"granted_at"`
	GrantedBy  string    `db:"granted_by"   json:"granted_by"`
}

// ListCrmPermissionsByDomain retorna todas as linhas de permissao do
// dominio — uma linha por (user, session_jid). Mesmo user pode aparecer
// varias vezes (uma vez por sessao liberada). Linhas legadas tem
// session_jid='' = wildcard.
func (r *Repository) ListCrmPermissionsByDomain(ctx context.Context, domain string) ([]*CrmUserPermission, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, domain, user_id, user_name, session_jid, granted_at, granted_by
		FROM crm_user_permissions
		WHERE domain = $1
		ORDER BY user_name, session_jid`, domain)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*CrmUserPermission
	for rows.Next() {
		var p CrmUserPermission
		if err := rows.Scan(&p.ID, &p.Domain, &p.UserID, &p.UserName, &p.SessionJID, &p.GrantedAt, &p.GrantedBy); err != nil {
			return nil, err
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

// GrantSessionPermission libera UM usuario pra usar UMA sessao (numero
// especifico). Idempotente — refresh do user_name a cada grant.
func (r *Repository) GrantSessionPermission(ctx context.Context, domain, userID, userName, sessionJID, grantedBy string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO crm_user_permissions (id, domain, user_id, user_name, session_jid, granted_by)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5)
		ON CONFLICT (domain, user_id, session_jid) DO UPDATE SET user_name = EXCLUDED.user_name`,
		domain, userID, userName, sessionJID, grantedBy)
	return err
}

// RevokeSessionPermission remove o vinculo (user, sessao) no dominio.
// Retorna true se algo foi removido.
func (r *Repository) RevokeSessionPermission(ctx context.Context, domain, userID, sessionJID string) (bool, error) {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM crm_user_permissions
		  WHERE domain = $1 AND user_id = $2 AND session_jid = $3`,
		domain, userID, sessionJID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ListUserAllowedSessions retorna as session_jids que esse user pode usar
// no dominio. Linha legada com session_jid='' funciona como wildcard:
// libera qualquer sessao ativa do dominio (mantem compat sem migrate manual).
func (r *Repository) ListUserAllowedSessions(ctx context.Context, domain, userID string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT session_jid
		  FROM crm_user_permissions
		 WHERE domain = $1 AND user_id = $2`, domain, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hasWildcard bool
	var specific []string
	for rows.Next() {
		var jid string
		if err := rows.Scan(&jid); err != nil {
			return nil, err
		}
		if jid == "" {
			hasWildcard = true
		} else {
			specific = append(specific, jid)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if hasWildcard {
		// Wildcard = todas as sessoes ativas do dominio
		all, err := r.ListActiveSessionsByDomain(ctx, domain)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(all))
		for _, s := range all {
			out = append(out, s.JID)
		}
		return out, nil
	}
	return specific, nil
}

// IsSessionAllowed: o user pode enviar com esta sessao?
// Match exato + match wildcard (session_jid='').
func (r *Repository) IsSessionAllowed(ctx context.Context, domain, userID, sessionJID string) (bool, error) {
	var n int
	err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM crm_user_permissions
		 WHERE domain = $1
		   AND user_id = $2
		   AND (session_jid = $3 OR session_jid = '')`,
		domain, userID, sessionJID).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ListPhonesByDomain retorna os telefones QR (nao-Cloud) associados a um dominio
// Bitrix. Usado para limpeza de session files por tenant.
func (r *Repository) ListPhonesByDomain(ctx context.Context, domain string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT s.phone
		FROM bitrix_accounts ba
		JOIN whatsapp_sessions s
		  ON REGEXP_REPLACE(REPLACE(s.jid, 'cloud:', ''), ':[0-9]+@', '@')
		   = REGEXP_REPLACE(REPLACE(ba.session_jid, 'cloud:', ''), ':[0-9]+@', '@')
		WHERE LOWER(REGEXP_REPLACE(ba.domain, '^https?://(www\.)?', '')) = $1
		  AND s.type != 'cloud_api'
		  AND s.phone IS NOT NULL AND s.phone <> ''`, domain)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var phones []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		phones = append(phones, p)
	}
	return phones, rows.Err()
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

// ─── Message Templates ───────────────────────────────────────────────────

// MessageTemplate representa uma mensagem pre-formatada para uso rapido
// pelo atendente no CRM tab.
type MessageTemplate struct {
	ID        uuid.UUID `db:"id"          json:"id"`
	Domain    string    `db:"domain"      json:"domain"`
	Title     string    `db:"title"       json:"title"`
	Body      string    `db:"body"        json:"body"`
	CreatedAt time.Time `db:"created_at"  json:"created_at"`
	CreatedBy string    `db:"created_by"  json:"created_by"`
	UpdatedAt time.Time `db:"updated_at"  json:"updated_at"`
	// MetaTemplateName: se preenchido, template aponta pra um template
	// aprovado no Meta Business Manager. Robot pode escolher modo
	// "Oficial" e enviar via Cloud API com esse template HSM.
	MetaTemplateName string `db:"meta_template_name"  json:"meta_template_name"`
	// MetaTemplateLang: language code (ex: pt_BR, en_US).
	MetaTemplateLang string `db:"meta_template_lang"  json:"meta_template_lang"`
	// MetaTemplateVars: numero de variaveis {{1}}, {{2}}, ... do template.
	MetaTemplateVars int `db:"meta_template_vars"  json:"meta_template_vars"`
}

// ListMessageTemplates retorna todos os templates do domain, ordenados por titulo.
func (r *Repository) ListMessageTemplates(ctx context.Context, domain string) ([]*MessageTemplate, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, domain, title, body, created_at, created_by, updated_at,
		       COALESCE(meta_template_name, ''),
		       COALESCE(meta_template_lang, ''),
		       COALESCE(meta_template_vars, 0)
		FROM message_templates
		WHERE domain = $1
		ORDER BY title ASC`, domain)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*MessageTemplate
	for rows.Next() {
		var t MessageTemplate
		if err := rows.Scan(&t.ID, &t.Domain, &t.Title, &t.Body, &t.CreatedAt, &t.CreatedBy, &t.UpdatedAt,
			&t.MetaTemplateName, &t.MetaTemplateLang, &t.MetaTemplateVars); err != nil {
			return nil, err
		}
		out = append(out, &t)
	}
	return out, rows.Err()
}

// GetMessageTemplateByID retorna 1 template do dominio. Usado pelo robot
// quando precisa resolver nome+lang+vars antes de enviar via Cloud API.
func (r *Repository) GetMessageTemplateByID(ctx context.Context, id uuid.UUID, domain string) (*MessageTemplate, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, domain, title, body, created_at, created_by, updated_at,
		       COALESCE(meta_template_name, ''),
		       COALESCE(meta_template_lang, ''),
		       COALESCE(meta_template_vars, 0)
		FROM message_templates
		WHERE id = $1 AND domain = $2`, id, domain)
	var t MessageTemplate
	if err := row.Scan(&t.ID, &t.Domain, &t.Title, &t.Body, &t.CreatedAt, &t.CreatedBy, &t.UpdatedAt,
		&t.MetaTemplateName, &t.MetaTemplateLang, &t.MetaTemplateVars); err != nil {
		return nil, err
	}
	return &t, nil
}

// CreateMessageTemplate insere um novo template. Retorna o ID gerado.
func (r *Repository) CreateMessageTemplate(ctx context.Context, domain, title, body, createdBy, metaName, metaLang string, metaVars int) (uuid.UUID, error) {
	id := uuid.New()
	_, err := r.pool.Exec(ctx, `
		INSERT INTO message_templates (id, domain, title, body, created_by,
			meta_template_name, meta_template_lang, meta_template_vars)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		id, domain, title, body, createdBy, metaName, metaLang, metaVars)
	return id, err
}

// UpdateMessageTemplate altera title/body/meta_* de um template (validando dominio).
func (r *Repository) UpdateMessageTemplate(ctx context.Context, id uuid.UUID, domain, title, body, metaName, metaLang string, metaVars int) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE message_templates
		SET title = $1, body = $2,
		    meta_template_name = $5, meta_template_lang = $6, meta_template_vars = $7,
		    updated_at = NOW()
		WHERE id = $3 AND domain = $4`,
		title, body, id, domain, metaName, metaLang, metaVars)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// DeleteMessageTemplate remove um template (validando domain).
func (r *Repository) DeleteMessageTemplate(ctx context.Context, id uuid.UUID, domain string) (bool, error) {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM message_templates WHERE id = $1 AND domain = $2`, id, domain)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ─── SMS Provider (Marketing > Campanhas SMS via WhatsApp) ────────────────
// Modulo isolado: nao toca em messages/sessions existentes.

// InsertSMSMessage cria registro de uma mensagem SMS recebida do Bitrix.
// Idempotente: se bitrix_message_id ja existe (retry do Bitrix), nao falha.
func (r *Repository) InsertSMSMessage(ctx context.Context, m *BitrixSMSMessage) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO bitrix_sms_messages
			(bitrix_message_id, domain, sender_code, session_jid, to_phone, body,
			 bindings_json, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'queued')
		ON CONFLICT (bitrix_message_id) DO NOTHING`,
		m.BitrixMessageID, m.Domain, m.SenderCode, m.SessionJID,
		m.ToPhone, m.Body, m.BindingsJSON)
	return err
}

// UpdateSMSMessageSent marca como enviado ao WA e guarda wa_message_id.
func (r *Repository) UpdateSMSMessageSent(ctx context.Context, bitrixMsgID, waMsgID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE bitrix_sms_messages
		   SET wa_message_id = $2, status = 'sent', sent_at = NOW(),
		       status_updated_at = NOW()
		 WHERE bitrix_message_id = $1`, bitrixMsgID, waMsgID)
	return err
}

// UpdateSMSMessageStatus seta status final + opcional error_msg.
// Status validos: queued|sent|delivered|undelivered|failed.
func (r *Repository) UpdateSMSMessageStatus(ctx context.Context, bitrixMsgID, status, errorMsg string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE bitrix_sms_messages
		   SET status = $2, error_msg = $3, status_updated_at = NOW()
		 WHERE bitrix_message_id = $1`, bitrixMsgID, status, errorMsg)
	return err
}

// GetSMSMessageByWAID busca o registro SMS pelo wa_message_id (delivery
// receipt do WhatsApp Cloud/Multi-Device chega com esse id).
func (r *Repository) GetSMSMessageByWAID(ctx context.Context, waMsgID string) (*BitrixSMSMessage, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT bitrix_message_id, domain, sender_code, session_jid, to_phone, body,
		       COALESCE(wa_message_id,''), status, COALESCE(error_msg,''),
		       COALESCE(bindings_json,''), created_at, sent_at, status_updated_at
		  FROM bitrix_sms_messages WHERE wa_message_id = $1 LIMIT 1`, waMsgID)
	var m BitrixSMSMessage
	if err := row.Scan(&m.BitrixMessageID, &m.Domain, &m.SenderCode, &m.SessionJID,
		&m.ToPhone, &m.Body, &m.WAMessageID, &m.Status, &m.ErrorMsg,
		&m.BindingsJSON, &m.CreatedAt, &m.SentAt, &m.StatusUpdatedAt); err != nil {
		return nil, err
	}
	return &m, nil
}

// ListSMSMessagesByDomain pagina as ultimas N msgs pra UI do dashboard.
func (r *Repository) ListSMSMessagesByDomain(ctx context.Context, domain string, limit int) ([]*BitrixSMSMessage, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `
		SELECT bitrix_message_id, domain, sender_code, session_jid, to_phone, body,
		       COALESCE(wa_message_id,''), status, COALESCE(error_msg,''),
		       COALESCE(bindings_json,''), created_at, sent_at, status_updated_at
		  FROM bitrix_sms_messages
		 WHERE domain = $1
		 ORDER BY created_at DESC
		 LIMIT $2`, domain, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*BitrixSMSMessage
	for rows.Next() {
		var m BitrixSMSMessage
		if err := rows.Scan(&m.BitrixMessageID, &m.Domain, &m.SenderCode, &m.SessionJID,
			&m.ToPhone, &m.Body, &m.WAMessageID, &m.Status, &m.ErrorMsg,
			&m.BindingsJSON, &m.CreatedAt, &m.SentAt, &m.StatusUpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

// DeleteOldSMSMessages apaga linhas de bitrix_sms_messages com mais de
// retentionDays dias. Politica de retencao do modulo SMS Campaigns.
func (r *Repository) DeleteOldSMSMessages(ctx context.Context, retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		retentionDays = 30
	}
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM bitrix_sms_messages WHERE created_at < NOW() - make_interval(days => $1)`,
		retentionDays)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// SetDefaultSMSSession define a sessao WA padrao do tenant pra campanhas SMS.
// Vazio = desativado.
func (r *Repository) SetDefaultSMSSession(ctx context.Context, domain, sessionJID string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE bitrix_portals SET default_sms_session_jid = $1, updated_at = NOW() WHERE domain = $2`,
		sessionJID, domain)
	return err
}

// AckSMSRisk marca que o tenant ja viu e aceitou o aviso de risco de
// banimento por uso massivo do WhatsApp. Modal nao reaparece.
func (r *Repository) AckSMSRisk(ctx context.Context, domain string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE bitrix_portals SET sms_risk_acknowledged = TRUE, updated_at = NOW() WHERE domain = $1`,
		domain)
	return err
}
