package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/config"
	"go.uber.org/zap"
)

func NewPool(ctx context.Context, cfg *config.PostgresConfig, log *zap.Logger) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.URL())
	if err != nil {
		return nil, fmt.Errorf("parse postgres config: %w", err)
	}

	poolCfg.MaxConns = int32(cfg.MaxOpenConns)
	poolCfg.MinConns = int32(cfg.MaxIdleConns)
	poolCfg.MaxConnLifetime = 30 * time.Minute
	poolCfg.MaxConnIdleTime = 5 * time.Minute
	poolCfg.HealthCheckPeriod = 1 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	log.Info("PostgreSQL connected", zap.String("host", cfg.Host), zap.String("db", cfg.DB))

	if err := runMigrations(ctx, pool, log); err != nil {
		return nil, fmt.Errorf("migrations failed: %w", err)
	}

	return pool, nil
}

// runMigrations aplica migrações incrementais idempotentes.
// Cada migração usa IF NOT EXISTS / IF EXISTS — seguro rodar múltiplas vezes.
func runMigrations(ctx context.Context, pool *pgxpool.Pool, log *zap.Logger) error {
	migrations := []struct {
		name string
		sql  string
	}{
		{"006_messages_jid", `
			ALTER TABLE messages ADD COLUMN IF NOT EXISTS from_jid TEXT NOT NULL DEFAULT '';
			ALTER TABLE messages ADD COLUMN IF NOT EXISTS to_jid   TEXT NOT NULL DEFAULT '';
			CREATE INDEX IF NOT EXISTS idx_messages_from_jid ON messages (from_jid);
			CREATE INDEX IF NOT EXISTS idx_messages_to_jid   ON messages (to_jid);
		`},
		{"007_messages_author", `
			ALTER TABLE messages ADD COLUMN IF NOT EXISTS author_name TEXT NOT NULL DEFAULT '';
		`},
		{"008_strip_device_suffix", `
			-- Normaliza JIDs existentes: remove ":NN" entre o número e o "@"
			-- "5519987717792:48@s.whatsapp.net" -> "5519987717792@s.whatsapp.net"
			-- "127586399207476:48@lid"          -> "127586399207476@lid"
			UPDATE messages
			   SET from_jid = REGEXP_REPLACE(from_jid, ':[0-9]+@', '@')
			 WHERE from_jid ~ ':[0-9]+@';
			UPDATE messages
			   SET to_jid = REGEXP_REPLACE(to_jid, ':[0-9]+@', '@')
			 WHERE to_jid ~ ':[0-9]+@';
			UPDATE contact_mapping
			   SET wa_jid = REGEXP_REPLACE(wa_jid, ':[0-9]+@', '@')
			 WHERE wa_jid ~ ':[0-9]+@';
		`},
		{"009_drop_lid_contact_mapping", `
			-- Remove contact_mapping entries cujo wa_phone é um LID em vez de telefone.
			-- LIDs costumam ter 15+ dígitos (ex: 127586399207476).
			-- Telefones brasileiros tem 12-13 dígitos (55 + DDD + numero).
			-- Quando o cliente mandar a próxima msg, ensureContact cria de novo com
			-- o telefone real (via SenderAlt do whatsmeow).
			DELETE FROM contact_mapping
			 WHERE wa_jid LIKE '%@lid'
			   AND LENGTH(wa_phone) > 14;
		`},
		{"010_lid_phone_map", `
			-- Tabela dedicada de mapeamento LID -> telefone.
			-- Populada toda vez que recebemos uma msg do cliente onde Sender é @lid
			-- e SenderAlt tem o telefone real (whatsmeow nos dá os dois).
			-- Consultada na hora de salvar msgs outbound (operador -> cliente)
			-- para resolver o @lid em telefone real e gravar to_jid corretamente.
			CREATE TABLE IF NOT EXISTS lid_phone_map (
				lid_jid    TEXT PRIMARY KEY,         -- ex: "127586399207476@lid"
				phone_jid  TEXT NOT NULL,            -- ex: "5519987717792@s.whatsapp.net"
				phone      TEXT NOT NULL,            -- ex: "5519987717792"
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);
			CREATE INDEX IF NOT EXISTS idx_lid_phone_map_phone ON lid_phone_map (phone);
		`},
	}

	for _, m := range migrations {
		if _, err := pool.Exec(ctx, m.sql); err != nil {
			return fmt.Errorf("migration %s: %w", m.name, err)
		}
		log.Info("migration applied", zap.String("name", m.name))
	}
	return nil
}
