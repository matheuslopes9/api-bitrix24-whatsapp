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
		{"011_cloud_api_session", `
			-- Suporte a sessões WhatsApp Cloud API (Meta) ao lado de QR Code.
			-- type: 'qr' (whatsmeow) | 'cloud_api' (Meta Graph)
			ALTER TABLE whatsapp_sessions
				ADD COLUMN IF NOT EXISTS type TEXT NOT NULL DEFAULT 'qr';
			ALTER TABLE whatsapp_sessions
				ADD COLUMN IF NOT EXISTS cloud_phone_number_id TEXT NOT NULL DEFAULT '';
			ALTER TABLE whatsapp_sessions
				ADD COLUMN IF NOT EXISTS cloud_waba_id TEXT NOT NULL DEFAULT '';
			ALTER TABLE whatsapp_sessions
				ADD COLUMN IF NOT EXISTS cloud_access_token TEXT NOT NULL DEFAULT '';
			ALTER TABLE whatsapp_sessions
				ADD COLUMN IF NOT EXISTS cloud_verify_token TEXT NOT NULL DEFAULT '';
			ALTER TABLE whatsapp_sessions
				ADD COLUMN IF NOT EXISTS cloud_app_secret TEXT NOT NULL DEFAULT '';
			ALTER TABLE whatsapp_sessions
				ADD COLUMN IF NOT EXISTS cloud_display_phone TEXT NOT NULL DEFAULT '';
			-- session_file só faz sentido para QR — cloud_api não tem arquivo SQLite
			ALTER TABLE whatsapp_sessions
				ALTER COLUMN session_file DROP NOT NULL;
			CREATE INDEX IF NOT EXISTS idx_whatsapp_sessions_type ON whatsapp_sessions (type);
			CREATE INDEX IF NOT EXISTS idx_whatsapp_sessions_cloud_phone_id
				ON whatsapp_sessions (cloud_phone_number_id) WHERE cloud_phone_number_id <> '';
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
		{"012_cloud_connector_per_session", `
			-- Cada sessão Cloud API precisa do próprio connector_id no Bitrix
			-- (formato: "wa_cloud_<phone_number_id>"). Sessões Cloud existentes
			-- foram criadas com connector_id genérico ("whatsapp_uc_v2") e por
			-- isso colidiam entre si. Atualiza os bitrix_accounts existentes
			-- consultando whatsapp_sessions pelo session_jid.
			UPDATE bitrix_accounts ba
			   SET connector_id = 'wa_cloud_' || ws.cloud_phone_number_id,
			       updated_at   = NOW()
			  FROM whatsapp_sessions ws
			 WHERE ba.session_jid = ws.jid
			   AND ws.type = 'cloud_api'
			   AND ws.cloud_phone_number_id <> ''
			   AND ba.connector_id NOT LIKE 'wa_cloud_%';
		`},
		{"013_reactivate_cloud_sessions", `
			-- Reativa sessões Cloud API que ficaram 'disconnected' por engano:
			-- o watchdog antigo tentava reconectar como whatsmeow ("session file not
			-- found") e marcava as sessões Cloud como desconectadas a cada ciclo.
			-- Como sessões Cloud são stateless via HTTPS (token + phone_number_id
			-- bastam), basta reativar no banco para que voltem a operar.
			UPDATE whatsapp_sessions
			   SET status = 'active', last_seen = NOW()
			 WHERE type = 'cloud_api'
			   AND status = 'disconnected'
			   AND cloud_phone_number_id <> ''
			   AND cloud_access_token <> '';
		`},
		{"014_qr_connector_per_session", `
			-- Mesma ideia para sessões QR: cada uma ganha "wa_qr_<telefone>".
			-- O telefone é extraído do session_jid removendo ":NN" (device suffix)
			-- e "@s.whatsapp.net". Isso permite que múltiplos números QR
			-- coexistam no mesmo portal sem colidir no connector.
			UPDATE bitrix_accounts ba
			   SET connector_id = 'wa_qr_' || SPLIT_PART(SPLIT_PART(ba.session_jid, '@', 1), ':', 1),
			       updated_at   = NOW()
			 WHERE ba.session_jid NOT LIKE 'cloud:%'
			   AND ba.session_jid <> ''
			   AND ba.connector_id NOT LIKE 'wa_qr_%'
			   AND ba.connector_id NOT LIKE 'wa_cloud_%';
		`},
		{"015_crm_user_permissions", `
			-- Controle de acesso ao CRM tab por usuario Bitrix.
			-- Modelo estrito: se nenhum row existe pra um dominio, NINGUEM acessa.
			-- Para liberar, super-admin insere (domain, user_id).
			CREATE TABLE IF NOT EXISTS crm_user_permissions (
				id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
				domain      TEXT NOT NULL,
				user_id     TEXT NOT NULL,
				user_name   TEXT NOT NULL DEFAULT '',
				granted_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				granted_by  TEXT NOT NULL DEFAULT 'super-admin',
				UNIQUE (domain, user_id)
			);
			CREATE INDEX IF NOT EXISTS idx_crm_user_permissions_domain ON crm_user_permissions (domain);
		`},
		{"017_normalize_bitrix_accounts_domain", `
			-- Normaliza bitrix_accounts.domain pra bater com bitrix_portals.domain
			-- (que e gravado via normalizePortalDomain — sem https:// e lowercase).
			-- Caminhos antigos (partner.go, handlers.go) gravavam "https://<dom>",
			-- entao queries como ListActiveSessionsByDomain nunca matchavam e o
			-- CRM tab mostrava "Desconectado" mesmo com sessao ativa.
			UPDATE bitrix_accounts
			   SET domain = LOWER(REGEXP_REPLACE(domain, '^https?://', ''))
			 WHERE domain ~* '^https?://' OR domain <> LOWER(domain);
			-- Trailing slash tambem (defensivo)
			UPDATE bitrix_accounts
			   SET domain = RTRIM(domain, '/')
			 WHERE domain LIKE '%/';
		`},
		{"016_message_templates", `
			-- Templates / quick replies — mensagens pre-formatadas que o
			-- atendente pode inserir no compositor com 1 clique.
			--
			-- Scope: por dominio (portal Bitrix). Nao distingue por usuario
			-- (qualquer atendente liberado ve e usa). Se precisar de templates
			-- privados por usuario, basta filtrar por user_id no futuro.
			CREATE TABLE IF NOT EXISTS message_templates (
				id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
				domain      TEXT NOT NULL,
				title       TEXT NOT NULL,         -- nome curto pra atendente identificar
				body        TEXT NOT NULL,         -- texto da mensagem (pode ter \n)
				created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				created_by  TEXT NOT NULL DEFAULT '',  -- user_id Bitrix de quem criou
				updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);
			CREATE INDEX IF NOT EXISTS idx_message_templates_domain ON message_templates (domain);
		`},
		{"019_master_user", `
			-- "Master user" por tenant: o usuario Bitrix que controla quem pode
			-- usar quais numeros. Escolhido pelo proprio cliente na primeira
			-- abertura do app (tela de onboarding). So o master atual pode
			-- transferir o controle para outro usuario.
			--
			-- Vazio = onboarding pendente. Backend bloqueia grant/revoke ate
			-- alguem ser escolhido. UI mostra tela de "Escolha o usuario master".
			ALTER TABLE bitrix_portals
				ADD COLUMN IF NOT EXISTS legacy_admin_user_id TEXT NOT NULL DEFAULT '';
		`},
		{"018_session_permissions", `
			-- Mudanca de modelo: ate aqui crm_user_permissions controlava QUEM
			-- acessava o CRM tab. Novo comportamento: CRM tab e aberto pra todo
			-- colaborador interno ativo; o que essa tabela controla agora e
			-- QUAIS NUMEROS (session_jid) o operador pode usar pra enviar.
			--
			-- Estrategia: adiciona coluna session_jid. Linhas legadas (com
			-- session_jid='') ficam como wildcard — interpretadas pelo backend
			-- como "esse user esta liberado pra qualquer sessao do dominio".
			-- Novos grants viram com session_jid especifico.
			ALTER TABLE crm_user_permissions
				ADD COLUMN IF NOT EXISTS session_jid TEXT NOT NULL DEFAULT '';

			-- Recria a UNIQUE incluindo session_jid (so se a antiga ainda existe).
			-- A constraint antiga era UNIQUE(domain, user_id).
			DO $$
			BEGIN
				IF EXISTS (
					SELECT 1 FROM pg_constraint
					WHERE conname = 'crm_user_permissions_domain_user_id_key'
				) THEN
					ALTER TABLE crm_user_permissions
						DROP CONSTRAINT crm_user_permissions_domain_user_id_key;
				END IF;
			END $$;

			-- Nova UNIQUE: (domain, user_id, session_jid). Permite varias linhas
			-- pro mesmo user (1 por sessao liberada).
			DO $$
			BEGIN
				IF NOT EXISTS (
					SELECT 1 FROM pg_constraint
					WHERE conname = 'crm_user_permissions_domain_user_id_session_jid_key'
				) THEN
					ALTER TABLE crm_user_permissions
						ADD CONSTRAINT crm_user_permissions_domain_user_id_session_jid_key
						UNIQUE (domain, user_id, session_jid);
				END IF;
			END $$;

			CREATE INDEX IF NOT EXISTS idx_crm_user_permissions_user
				ON crm_user_permissions (domain, user_id);
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
