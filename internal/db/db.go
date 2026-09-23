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
		{"000_base_schema", `
			-- Schema base: cria as tabelas que as migrations 006+ assumem que
			-- ja existem. Em bancos ANTIGOS (producao) essas tabelas foram
			-- criadas por migrations 001-005 legadas (removidas do codigo depois
			-- que os bancos ja as tinham). Num banco NOVO/ZERADO elas faltavam,
			-- e a migration 006 quebrava com 'relation "messages" does not exist'.
			-- TODAS com IF NOT EXISTS: no-op seguro em bancos que ja as tem;
			-- cria do zero em bancos novos. As colunas extras (from_jid, type,
			-- cloud_*, author_name, etc) sao adicionadas pelas migrations 006+
			-- via ALTER ... ADD COLUMN IF NOT EXISTS — nao duplicam aqui.

			CREATE TABLE IF NOT EXISTS whatsapp_sessions (
				id            UUID PRIMARY KEY,
				jid           TEXT NOT NULL UNIQUE,
				phone         TEXT NOT NULL DEFAULT '',
				display_name  TEXT NOT NULL DEFAULT '',
				status        TEXT NOT NULL DEFAULT 'disconnected',
				session_file  TEXT,
				last_seen     TIMESTAMPTZ,
				created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE TABLE IF NOT EXISTS contact_mapping (
				id             UUID PRIMARY KEY,
				wa_jid         TEXT NOT NULL,
				wa_phone       TEXT NOT NULL DEFAULT '',
				wa_name        TEXT NOT NULL DEFAULT '',
				bitrix_entity  TEXT NOT NULL DEFAULT '',
				bitrix_id      TEXT NOT NULL DEFAULT '',
				bitrix_chat_id TEXT NOT NULL DEFAULT '',
				session_id     UUID,
				created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				UNIQUE (wa_jid, session_id)
			);

			CREATE TABLE IF NOT EXISTS messages (
				id            UUID PRIMARY KEY,
				wa_message_id TEXT NOT NULL UNIQUE,
				session_id    UUID,
				contact_id    UUID,
				direction     TEXT NOT NULL DEFAULT 'inbound',
				message_type  TEXT NOT NULL DEFAULT 'text',
				content       TEXT NOT NULL DEFAULT '',
				media_url     TEXT NOT NULL DEFAULT '',
				media_mime    TEXT NOT NULL DEFAULT '',
				media_size    BIGINT NOT NULL DEFAULT 0,
				status        TEXT NOT NULL DEFAULT 'received',
				retry_count   INT NOT NULL DEFAULT 0,
				error_msg     TEXT,
				sent_at       TIMESTAMPTZ,
				delivered_at  TIMESTAMPTZ,
				created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE TABLE IF NOT EXISTS bitrix_accounts (
				id           UUID PRIMARY KEY,
				session_jid  TEXT NOT NULL UNIQUE,
				domain       TEXT NOT NULL DEFAULT '',
				client_id    TEXT NOT NULL DEFAULT '',
				client_secret TEXT NOT NULL DEFAULT '',
				open_line_id INTEGER NOT NULL DEFAULT 0,
				connector_id TEXT NOT NULL DEFAULT '',
				redirect_uri TEXT NOT NULL DEFAULT '',
				status       TEXT NOT NULL DEFAULT 'active',
				created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE TABLE IF NOT EXISTS bitrix_portals (
				id            UUID PRIMARY KEY,
				domain        TEXT NOT NULL UNIQUE,
				access_token  TEXT NOT NULL DEFAULT '',
				refresh_token TEXT NOT NULL DEFAULT '',
				expires_at    TIMESTAMPTZ,
				member_id     TEXT NOT NULL DEFAULT '',
				connector_id  TEXT NOT NULL DEFAULT '',
				open_line_id  INTEGER NOT NULL DEFAULT 0,
				created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);

			CREATE TABLE IF NOT EXISTS bitrix_tokens (
				id            UUID PRIMARY KEY,
				domain        TEXT NOT NULL,
				client_id     TEXT NOT NULL DEFAULT '',
				access_token  TEXT NOT NULL DEFAULT '',
				refresh_token TEXT NOT NULL DEFAULT '',
				expires_at    TIMESTAMPTZ,
				scope         TEXT NOT NULL DEFAULT '',
				created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				UNIQUE (domain, client_id)
			);

			CREATE INDEX IF NOT EXISTS idx_messages_created_at ON messages (created_at);
			CREATE INDEX IF NOT EXISTS idx_contact_mapping_wa_jid ON contact_mapping (wa_jid);
			CREATE INDEX IF NOT EXISTS idx_bitrix_accounts_domain ON bitrix_accounts (domain);
		`},
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
		{"023_template_meta_revert", `
			-- NO-OP DESDE 2026-05-18.
			--
			-- Esta migration ORIGINALMENTE droppava meta_template_name/lang/vars
			-- (revertendo 022). Como o sistema NAO tem migration ledger, ela
			-- rodava em TODO startup — e como migration 024 recria as colunas
			-- com DEFAULT '', cada restart do container apagava os valores de
			-- meta_template_* de TODOS os rows. Bug: templates importados da
			-- Meta sumiam da aba Oficial apos o proximo restart.
			--
			-- Migration mantida (nao removida) pra preservar a ordem do slice
			-- e a "historia" das migrations declaradas. So neutralizada.
			SELECT 1;
		`},
		{"022_template_meta", `
			-- (descontinuada) — campos adicionados aqui foram removidos pela 023.
			-- Mantida apenas pra historia da fila de migrations.
			SELECT 1;
		`},
		{"024_template_meta_redo", `
			-- IMPORTANTE: precisa rodar DEPOIS da 023 (que dropa as colunas).
			-- Slice e' percorrido na ORDEM DECLARADA, nao alfabetica — entao
			-- 024 vem depois de 023/022/etc no codigo, recriando as colunas
			-- que a 023 removeu.
			--
			-- Recria os campos meta_template_* em message_templates. Cliente
			-- usa pra automacoes do CRM Bitrix24 (robot/BizProc activity)
			-- onde escolhe entre envio Oficial (Cloud API + template HSM
			-- aprovado pela Meta) e Nao Oficial (texto livre Multi-Device).
			ALTER TABLE message_templates
				ADD COLUMN IF NOT EXISTS meta_template_name TEXT NOT NULL DEFAULT '';
			ALTER TABLE message_templates
				ADD COLUMN IF NOT EXISTS meta_template_lang TEXT NOT NULL DEFAULT '';
			ALTER TABLE message_templates
				ADD COLUMN IF NOT EXISTS meta_template_vars INT NOT NULL DEFAULT 0;
		`},
		{"021_sms_provider", `
			-- Modulo SMS Campaigns: Bitrix Marketing > Campanhas SMS escolhe o
			-- UC Talk como provedor. Bitrix manda POSTs de SMS pra gente, nos
			-- entregamos via WhatsApp e reportamos status de volta. Tudo
			-- isolado em tabela propria — nao toca nada existente.
			CREATE TABLE IF NOT EXISTS bitrix_sms_messages (
				bitrix_message_id   TEXT PRIMARY KEY,           -- id que o Bitrix nos manda
				domain              TEXT NOT NULL,              -- portal Bitrix
				sender_code         TEXT NOT NULL DEFAULT 'uctalk_whatsapp',
				session_jid         TEXT NOT NULL DEFAULT '',   -- sessao WA usada
				to_phone            TEXT NOT NULL,              -- E.164 normalizado
				body                TEXT NOT NULL,
				wa_message_id       TEXT NOT NULL DEFAULT '',   -- id retornado pelo WA
				status              TEXT NOT NULL DEFAULT 'queued', -- queued|sent|delivered|undelivered|failed
				error_msg           TEXT NOT NULL DEFAULT '',
				bindings_json       TEXT NOT NULL DEFAULT '',   -- bindings CRM raw (opcional)
				created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				sent_at             TIMESTAMPTZ,
				status_updated_at   TIMESTAMPTZ
			);
			CREATE INDEX IF NOT EXISTS idx_bsm_domain         ON bitrix_sms_messages (domain);
			CREATE INDEX IF NOT EXISTS idx_bsm_wa_message_id  ON bitrix_sms_messages (wa_message_id) WHERE wa_message_id <> '';
			CREATE INDEX IF NOT EXISTS idx_bsm_status         ON bitrix_sms_messages (status);
			CREATE INDEX IF NOT EXISTS idx_bsm_created_at     ON bitrix_sms_messages (created_at);

			-- Sessao WA padrao do tenant pra disparar campanhas SMS.
			-- Vazio = nao configurado, modulo desativado pra esse tenant.
			ALTER TABLE bitrix_portals
				ADD COLUMN IF NOT EXISTS default_sms_session_jid TEXT NOT NULL DEFAULT '';

			-- Confirmacao do aviso de risco de banimento (modal 1a vez).
			-- false = ainda nao mostrou. Marcamos true quando o tenant aceita.
			ALTER TABLE bitrix_portals
				ADD COLUMN IF NOT EXISTS sms_risk_acknowledged BOOLEAN NOT NULL DEFAULT FALSE;
		`},
		{"020_messages_retention", `
			-- Indice em created_at pra suportar a purga rolling (1 ano) e
			-- todas as queries de Historico que filtram por created_at >.
			CREATE INDEX IF NOT EXISTS idx_messages_created_at ON messages (created_at);

			-- Indices auxiliares pra Historico em escala (matches por JID).
			-- Idempotente — IF NOT EXISTS evita problema em re-runs.
			CREATE INDEX IF NOT EXISTS idx_messages_from_to_created
				ON messages (from_jid, to_jid, created_at DESC);
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
		{"025_portal_application_token", `
			-- application_token do Bitrix24: token que o Bitrix gera pra cada
			-- install/app e envia em TODA chamada server-to-server (event handlers,
			-- BizProc activities, SMS sender callbacks). Persistir no install pra
			-- validar com constant-time-compare nos endpoints publicos
			-- (/bitrix/bp/send, /bitrix/sms/send) — bloqueia atacante anonimo
			-- mandando POSTs com auth[domain] forjado.
			ALTER TABLE bitrix_portals
				ADD COLUMN IF NOT EXISTS application_token TEXT NOT NULL DEFAULT '';
		`},
		{"029_admin_users", `
			-- Usuarios do painel admin (multi-admin com papeis). O login por
			-- env (ADMIN_USER/ADMIN_PASSWORD) continua funcionando como 'root'
			-- de emergencia; estes sao usuarios adicionais persistidos.
			-- role: 'superadmin' (tudo) | 'support' (le tudo, acoes limitadas)
			-- password_hash: bcrypt.
			CREATE TABLE IF NOT EXISTS admin_users (
				id            UUID PRIMARY KEY,
				email         TEXT NOT NULL UNIQUE,
				name          TEXT NOT NULL DEFAULT '',
				password_hash TEXT NOT NULL,
				role          TEXT NOT NULL DEFAULT 'support',
				active        BOOLEAN NOT NULL DEFAULT TRUE,
				last_login_at TIMESTAMPTZ,
				created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				created_by    TEXT NOT NULL DEFAULT ''
			);
		`},
		{"030_admin_audit_log", `
			-- Log de auditoria: toda acao relevante no painel admin.
			CREATE TABLE IF NOT EXISTS admin_audit_log (
				id         BIGSERIAL PRIMARY KEY,
				actor      TEXT NOT NULL DEFAULT '',
				action     TEXT NOT NULL DEFAULT '',
				target     TEXT NOT NULL DEFAULT '',
				detail     TEXT NOT NULL DEFAULT '',
				ip         TEXT NOT NULL DEFAULT '',
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);
			CREATE INDEX IF NOT EXISTS idx_audit_created_at ON admin_audit_log (created_at DESC);
		`},
		{"031_blocked_ips", `
			-- IPs bloqueados persistentes (alem do rate-limit em memoria).
			-- reason: 'manual' | 'brute_force'. active=FALSE = liberado.
			CREATE TABLE IF NOT EXISTS blocked_ips (
				ip          TEXT PRIMARY KEY,
				reason      TEXT NOT NULL DEFAULT 'manual',
				fail_count  INT NOT NULL DEFAULT 0,
				active      BOOLEAN NOT NULL DEFAULT TRUE,
				note        TEXT NOT NULL DEFAULT '',
				created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);
		`},
		{"041b_bitrix_portals_installed_at", `
			-- installed_at e' lido pelo código (admin: lista de portais, ORDER BY
			-- installed_at) mas nunca foi criado em migration — em banco novo
			-- (homolog) a coluna nao existe e o /admin/api/tenants da 500.
			-- Adiciona idempotente; backfill com created_at pros registros antigos.
			ALTER TABLE bitrix_portals
				ADD COLUMN IF NOT EXISTS installed_at TIMESTAMPTZ;
			UPDATE bitrix_portals SET installed_at = created_at WHERE installed_at IS NULL;
			ALTER TABLE bitrix_portals
				ALTER COLUMN installed_at SET DEFAULT NOW();
		`},
		{"043_messages_colunas_faltantes", `
			-- BUG: a tabela 'messages' criada pela migration 000_base_schema e'
			-- uma copia REDUZIDA do migrations/001_init.sql — perdeu 4 colunas.
			-- Os arquivos migrations/*.sql NAO sao executados (so' este array
			-- roda), entao em qualquer banco criado pelo codigo atual essas
			-- colunas simplesmente nao existem. Consequencias:
			--
			--   GetMessagesByPhone  -> SELECT ... retry_count  -> SQLSTATE 42703
			--                          (a aba Historico nao abre a conversa)
			--   GetRecentMessages   -> mesmo erro (simulador/diagnostico)
			--   UpdateMessageStatus -> UPDATE ... error_msg, delivered_at falha.
			--                          O processor descarta o erro com '_ =',
			--                          entao a mensagem NUNCA sai de 'received'
			--                          e nunca marca entrega — falha silenciosa.
			--   IncrementRetry      -> UPDATE ... retry_count  -> retry nunca
			--                          e' contabilizado.
			--
			-- Idempotente (ADD COLUMN IF NOT EXISTS): em bancos antigos que ja'
			-- nasceram do 001_init.sql as colunas ja' existem e isto e' no-op.
			ALTER TABLE messages ADD COLUMN IF NOT EXISTS retry_count  INT NOT NULL DEFAULT 0;
			ALTER TABLE messages ADD COLUMN IF NOT EXISTS error_msg    TEXT;
			ALTER TABLE messages ADD COLUMN IF NOT EXISTS sent_at      TIMESTAMPTZ;
			ALTER TABLE messages ADD COLUMN IF NOT EXISTS delivered_at TIMESTAMPTZ;
		`},
		{"044_sessoes_qr_duplicadas", `
			-- Limpa as linhas orfas que o UpsertSession criou ao longo do tempo.
			--
			-- O upsert e' ON CONFLICT (jid) e o JID de sessao QR carrega o
			-- device suffix do whatsmeow, que muda a cada re-pareamento
			-- (":1" -> ":2"). Com suffix novo o ON CONFLICT nao dispara e o
			-- Postgres INSERE linha nova: sobra a antiga. Efeitos em producao:
			-- numero repetido na tela de Sessoes, 2 chips do mesmo numero em
			-- Permissoes, e o watchdog tentando reconectar a linha orfa —
			-- abrindo um 2o client sobre o mesmo device, o que o WhatsApp
			-- resolve derrubando um dos dois (stream:conflict). Era a origem
			-- do ciclo de desconexao a cada ~30s.
			--
			-- Mantem, por numero base, UMA linha: a de last_seen mais recente
			-- (NULL por ultimo), desempatando por created_at e por jid.
			--
			-- ROW_NUMBER em vez de comparacao de tupla de proposito: last_seen
			-- e' NULLABLE, e "(a,b) > (c,d)" com NULL resulta em NULL (nao em
			-- true/false). Com duas linhas de last_seen NULL — caso comum em
			-- sessao que nunca reconectou — a comparacao nunca seria
			-- verdadeira e NADA seria deletado, deixando a duplicata de pe'
			-- e fazendo o CREATE UNIQUE INDEX abaixo falhar. Migration que
			-- falha aborta o boot inteiro, entao isto precisa ser NULL-safe.
			DELETE FROM whatsapp_sessions
			 WHERE jid IN (
			   SELECT jid FROM (
			     SELECT jid,
			            ROW_NUMBER() OVER (
			              PARTITION BY SPLIT_PART(SPLIT_PART(jid, '@', 1), ':', 1)
			              ORDER BY last_seen DESC NULLS LAST, created_at DESC, jid DESC
			            ) AS rn
			       FROM whatsapp_sessions
			      WHERE jid NOT LIKE 'cloud:%'
			        -- So' deduplica quem tem numero base valido. Um JID
			        -- malformado produziria base vazia e TODOS eles cairiam
			        -- na mesma particao, fazendo a limpeza apagar linhas sem
			        -- nenhuma relacao entre si.
			        AND SPLIT_PART(SPLIT_PART(jid, '@', 1), ':', 1) ~ '^[0-9]+$'
			   ) ranked
			   WHERE rn > 1
			 );

			-- Corrige o campo 'phone' das linhas sobreviventes. Ele guardava a
			-- string que o usuario DIGITOU ao parear, nunca conferida contra o
			-- JID real — foi assim que o mesmo numero apareceu como
			-- "+5581996807479" e "+81996807479" na tela de Permissoes, sendo
			-- 558196807479 o numero de verdade. Quem manda no numero e' o
			-- WhatsApp (o JID do device), nao o formulario.
			UPDATE whatsapp_sessions
			   SET phone = SPLIT_PART(SPLIT_PART(jid, '@', 1), ':', 1)
			 WHERE jid NOT LIKE 'cloud:%'
			   AND SPLIT_PART(SPLIT_PART(jid, '@', 1), ':', 1) ~ '^[0-9]+$'
			   AND phone IS DISTINCT FROM SPLIT_PART(SPLIT_PART(jid, '@', 1), ':', 1);

			-- Indice unico por numero base pras sessoes QR: barra no BANCO a
			-- reintroducao de duplicata, mesmo que algum caminho de codigo
			-- novo esqueca de deduplicar.
			--
			-- Dentro de bloco com EXCEPTION: se por qualquer estado inesperado
			-- ainda houver duplicata, o indice nao e' criado e o boot segue —
			-- em vez de derrubar a aplicacao inteira. O log do Postgres avisa,
			-- e o proximo boot tenta de novo.
			DO $$
			BEGIN
				CREATE UNIQUE INDEX IF NOT EXISTS idx_whatsapp_sessions_numero_base
					ON whatsapp_sessions ((SPLIT_PART(SPLIT_PART(jid, '@', 1), ':', 1)))
					WHERE jid NOT LIKE 'cloud:%'
					  AND SPLIT_PART(SPLIT_PART(jid, '@', 1), ':', 1) ~ '^[0-9]+$';
			EXCEPTION WHEN unique_violation OR duplicate_table THEN
				RAISE WARNING 'idx_whatsapp_sessions_numero_base nao criado (duplicatas remanescentes); boot segue';
			END $$;
		`},
		{"046_licencas", `
			-- MODELO NOVO: instalacao local com licenca manual.
			--
			-- O UC Talk deixou de ser SaaS de marketplace (trial de 7 dias,
			-- planos Basico/Pro, cupons, cobranca online). Agora quem instala
			-- e' a UC Technology, e o cliente paga pelo comercial. O que o
			-- contrato libera fica AQUI, na licenca do proprio cliente.
			--
			-- Por que nao reaproveitar tenant_plans + plan_definitions: o
			-- modelo antigo guardava o CODIGO do plano no tenant e as FLAGS
			-- num catalogo separado. Bastava o catalogo dessincronizar pro
			-- cliente ficar rotulado "Pro" e sem nenhuma feature — foi
			-- exatamente o bug relatado. Sem catalogo intermediario, nao ha'
			-- o que dessincronizar.
			CREATE TABLE IF NOT EXISTS tenant_licenses (
				domain             TEXT PRIMARY KEY,

				-- Beneficios contratados, marcados um a um na ativacao.
				max_sessions       INT     NOT NULL DEFAULT 1,
				feat_cloud_api     BOOLEAN NOT NULL DEFAULT FALSE, -- Cloud API + Templates
				feat_automations   BOOLEAN NOT NULL DEFAULT FALSE, -- robos BizProc
				feat_reports       BOOLEAN NOT NULL DEFAULT FALSE,
				feat_sms           BOOLEAN NOT NULL DEFAULT FALSE, -- oculto na UI por ora

				-- Vigencia. NULL = sem prazo (nao vence).
				-- Vencer NAO bloqueia o app: so' mostra aviso e notifica o
				-- financeiro. Ninguem fica sem atendimento por boleto atrasado.
				valid_until        DATE,

				notes              TEXT NOT NULL DEFAULT '',

				-- Onboarding, herdado de tenant_plans. NAO e' cobranca: sao as
				-- flags de "ja' mostrei as boas-vindas" e "ja' defini o master
				-- automaticamente". Vem junto porque a tabela de origem sera'
				-- removida na 047 e estas duas colunas precisam sobreviver.
				welcome_shown      BOOLEAN NOT NULL DEFAULT FALSE,
				master_auto_set_at TIMESTAMPTZ,

				created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);
			CREATE INDEX IF NOT EXISTS idx_tenant_licenses_valid_until
				ON tenant_licenses (valid_until) WHERE valid_until IS NOT NULL;

			-- Historico de pagamentos, lancado a mao pelo suporte.
			-- paid_at      = quando o cliente pagou (pra conciliar com extrato)
			-- covers_until = ate quando aquele pagamento libera o uso
			-- recorded_by  = quem lancou, pra auditoria
			CREATE TABLE IF NOT EXISTS license_payments (
				id           UUID PRIMARY KEY,
				domain       TEXT   NOT NULL,
				paid_at      DATE   NOT NULL,
				covers_until DATE   NOT NULL,
				amount_cents BIGINT NOT NULL DEFAULT 0,
				method       TEXT   NOT NULL DEFAULT '',
				notes        TEXT   NOT NULL DEFAULT '',
				recorded_by  TEXT   NOT NULL DEFAULT '',
				created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);
			CREATE INDEX IF NOT EXISTS idx_license_payments_domain
				ON license_payments (domain, paid_at DESC);

			-- Avisos ja' enviados pro financeiro. Serve pra NAO repetir o
			-- mesmo aviso todo dia enquanto a licenca segue vencida.
			CREATE TABLE IF NOT EXISTS license_notifications (
				id         BIGSERIAL PRIMARY KEY,
				domain     TEXT NOT NULL,
				kind       TEXT NOT NULL,          -- 'vencendo' | 'vencida'
				ref_date   DATE NOT NULL,          -- valid_until que originou o aviso
				sent_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				UNIQUE (domain, kind, ref_date)
			);

			-- MIGRACAO DOS DADOS — roda ANTES da 047 dropar tenant_plans.
			--
			-- Todo portal instalado ganha licenca. Sem isto, na virada o
			-- cliente ficaria sem linha e perderia os beneficios.
			--
			-- Traduz o plano antigo pro modelo novo, respeitando o que cada
			-- um ja' tinha: quem estava em 'pro' com acesso valido leva os
			-- beneficios; os demais entram no minimo (1 sessao) e o suporte
			-- ajusta conforme contrato. valid_until herda o active_until (ou
			-- o fim do trial), entao ninguem e' marcado vencido a toa.
			--
			-- Tudo dentro de um IF sobre to_regclass: num banco NOVO a tabela
			-- tenant_plans nunca existiu (as migrations que a criavam sairam
			-- do array junto com o modelo SaaS), e um SELECT direto quebraria
			-- o boot com "relation does not exist".
			DO $$
			BEGIN
			IF to_regclass('public.tenant_plans') IS NOT NULL THEN

				INSERT INTO tenant_licenses (
					domain, max_sessions,
					feat_cloud_api, feat_automations, feat_reports,
					valid_until, notes, welcome_shown, master_auto_set_at
				)
				SELECT
					p.domain,
					CASE WHEN tp.plan = 'pro' THEN 10 ELSE 1 END,
					COALESCE(tp.plan = 'pro', FALSE),
					COALESCE(tp.plan = 'pro', FALSE),
					COALESCE(tp.plan = 'pro', FALSE),
					COALESCE(tp.active_until::date, tp.trial_ends_at::date),
					COALESCE(tp.notes, ''),
					COALESCE(tp.welcome_shown, FALSE),
					tp.master_auto_set_at
				  FROM bitrix_portals p
				  LEFT JOIN tenant_plans tp ON tp.domain = p.domain
				 WHERE p.domain <> p.member_id
				ON CONFLICT (domain) DO NOTHING;

				-- Tenants com plano cujo portal ja' sumiu: entram tambem,
				-- pra nao perder historico.
				INSERT INTO tenant_licenses (
					domain, max_sessions,
					feat_cloud_api, feat_automations, feat_reports,
					valid_until, notes, welcome_shown, master_auto_set_at
				)
				SELECT
					tp.domain,
					CASE WHEN tp.plan = 'pro' THEN 10 ELSE 1 END,
					tp.plan = 'pro', tp.plan = 'pro', tp.plan = 'pro',
					COALESCE(tp.active_until::date, tp.trial_ends_at::date),
					COALESCE(tp.notes, ''),
					COALESCE(tp.welcome_shown, FALSE),
					tp.master_auto_set_at
				  FROM tenant_plans tp
				ON CONFLICT (domain) DO NOTHING;

			END IF;

			-- Independente de ter havido modelo antigo: todo portal instalado
			-- precisa de licenca.
			INSERT INTO tenant_licenses (domain)
			SELECT p.domain FROM bitrix_portals p
			 WHERE p.domain <> p.member_id
			ON CONFLICT (domain) DO NOTHING;
			END $$;
		`},
		{"047_remove_billing", `
			-- Remove o modelo SaaS de marketplace. Roda DEPOIS da 046, que ja'
			-- copiou pra tenant_licenses tudo que precisava sobreviver:
			-- os beneficios traduzidos do plano e as duas colunas de
			-- onboarding (welcome_shown, master_auto_set_at) que moravam em
			-- tenant_plans mas nao sao cobranca.
			--
			-- Destrutivo e irreversivel. O historico de cobranca online morre
			-- junto — e' intencional: nao ha' mais cobranca pelo app, o
			-- cliente paga pelo comercial e o lancamento vive em
			-- license_payments.
			DROP TABLE IF EXISTS coupon_redemptions;
			DROP TABLE IF EXISTS coupons;
			DROP TABLE IF EXISTS billing_charges;
			DROP TABLE IF EXISTS boleto_numeracao;
			DROP TABLE IF EXISTS billing_config;
			DROP TABLE IF EXISTS plan_definitions;
			DROP TABLE IF EXISTS tenant_plans;
		`},
		{"048_permissoes_por_numero_base", `
			-- As permissoes de envio guardavam o JID COMPLETO da sessao, com o
			-- device suffix do whatsmeow (":1", ":5"). Esse suffix muda a cada
			-- re-pareamento, e a unique e' (domain, user_id, session_jid) —
			-- entao cada re-pareamento criava LINHA NOVA em vez de atualizar,
			-- e a permissao antiga deixava de casar com a sessao atual.
			--
			-- Resultado observado no teclife: 10 usuarios com 3 permissoes
			-- cada (uma por JID antigo) e NENHUMA valendo pra sessao corrente.
			-- O operador levava 403 "voce nao tem permissao pra enviar com
			-- este numero" estando liberado na tela.
			--
			-- Normaliza pro numero base, que e' a identidade estavel da
			-- sessao. Preserva o wildcard do master ('') e as sessoes Cloud
			-- API ('cloud:...'), onde o identificador inteiro e' que distingue
			-- uma conta oficial da outra.

			-- 1. Apaga as duplicatas ANTES de normalizar, senao a normalizacao
			--    colidiria na unique. Mantem a concedida mais cedo de cada
			--    (dominio, usuario, numero base) — e' a que o cliente
			--    realmente autorizou; as outras sao eco de re-pareamento.
			DELETE FROM crm_user_permissions p
			 WHERE p.session_jid <> ''
			   AND p.session_jid NOT LIKE 'cloud:%'
			   AND EXISTS (
			     SELECT 1 FROM crm_user_permissions q
			      WHERE q.domain  = p.domain
			        AND q.user_id = p.user_id
			        AND q.session_jid <> ''
			        AND q.session_jid NOT LIKE 'cloud:%'
			        AND SPLIT_PART(SPLIT_PART(q.session_jid, '@', 1), ':', 1)
			          = SPLIT_PART(SPLIT_PART(p.session_jid, '@', 1), ':', 1)
			        AND (q.granted_at, q.id) < (p.granted_at, p.id)
			   );

			-- 2. Agora normaliza as sobreviventes.
			UPDATE crm_user_permissions
			   SET session_jid = SPLIT_PART(SPLIT_PART(session_jid, '@', 1), ':', 1)
			 WHERE session_jid <> ''
			   AND session_jid NOT LIKE 'cloud:%'
			   AND session_jid <> SPLIT_PART(SPLIT_PART(session_jid, '@', 1), ':', 1);
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
