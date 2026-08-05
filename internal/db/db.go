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
		{"026_tenant_plans", `
			-- Sistema de planos / trial / billing. Um row por dominio (portal
			-- Bitrix24). Default: cria com trial 7 dias no install do app.
			--
			-- plan: 'basic' (limitado, default trial) | 'pro' (full features)
			-- status: 'trial' | 'active' | 'expired' | 'suspended'
			-- trial_ends_at: NULL para Pro pago; futuro para trial em andamento
			-- active_until: NULL = vitalicio (Pro pago manual); futuro = renovacao
			CREATE TABLE IF NOT EXISTS tenant_plans (
				domain          TEXT PRIMARY KEY,
				plan            TEXT NOT NULL DEFAULT 'basic',
				status          TEXT NOT NULL DEFAULT 'trial',
				trial_ends_at   TIMESTAMPTZ,
				active_until    TIMESTAMPTZ,
				created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				notes           TEXT NOT NULL DEFAULT ''
			);
			CREATE INDEX IF NOT EXISTS idx_tenant_plans_status ON tenant_plans (status);
		`},
		{"027_tenant_plans_onboarding", `
			-- Onboarding state pra controlar se mostramos welcome screen e
			-- quando o master foi auto-setado.
			--
			-- welcome_shown: TRUE = user ja' viu a tela de boas-vindas e
			-- clicou 'Continuar pro App'. Dashboard skip o /welcome.
			-- master_auto_set_at: timestamp em que o backend setou o master
			-- automaticamente no install (NULL = setado manual ou ainda nao).
			ALTER TABLE tenant_plans
				ADD COLUMN IF NOT EXISTS welcome_shown BOOLEAN NOT NULL DEFAULT FALSE;
			ALTER TABLE tenant_plans
				ADD COLUMN IF NOT EXISTS master_auto_set_at TIMESTAMPTZ;
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
		{"034_billing_config", `
			-- Config do gateway de pagamento (maxiPago) editavel pela UI admin,
			-- em vez de depender so' de env var. Linha unica (id=1). Valores
			-- vazios caem no fallback do env. merchant_key e' sensivel — nunca
			-- retornado pro front (so' um flag "configurado").
			CREATE TABLE IF NOT EXISTS billing_config (
				id                INT PRIMARY KEY DEFAULT 1,
				provider          TEXT NOT NULL DEFAULT 'maxipago',
				environment       TEXT NOT NULL DEFAULT 'sandbox',   -- sandbox | production
				merchant_id       TEXT NOT NULL DEFAULT '',
				merchant_key      TEXT NOT NULL DEFAULT '',
				processor_boleto  TEXT NOT NULL DEFAULT '12',
				processor_pix     TEXT NOT NULL DEFAULT '206',
				processor_card    TEXT NOT NULL DEFAULT '1',
				activate_days     INT NOT NULL DEFAULT 30,
				enabled           BOOLEAN NOT NULL DEFAULT FALSE,
				updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				CONSTRAINT billing_config_single CHECK (id = 1)
			);
			-- Seed com as credenciais de SANDBOX ja conhecidas, pra tela vir
			-- preenchida e o admin so' revisar/habilitar. enabled=FALSE por
			-- seguranca: o admin liga conscientemente na tela Gateway.
			-- ON CONFLICT DO NOTHING: nunca sobrescreve o que o admin ja salvou.
			INSERT INTO billing_config
				(id, provider, environment, merchant_id, merchant_key,
				 processor_boleto, processor_pix, processor_card, activate_days, enabled)
			VALUES
				(1, 'maxipago', 'sandbox', '39041', 'e0kpdthr975n3xcv1r9lfg80',
				 '12', '206', '1', 30, FALSE)
			ON CONFLICT (id) DO NOTHING;
		`},
		{"037_coupons", `
			-- Cupons de desconto configuraveis pelo admin.
			-- kind: 'percent' (desconto %) | 'amount' (desconto fixo em centavos)
			--     | 'trial_days' (estende o trial em N dias, sem cobranca)
			-- plan_code vazio = vale pra qualquer plano.
			-- max_uses 0 = ilimitado. expires_at NULL = sem validade.
			CREATE TABLE IF NOT EXISTS coupons (
				code        TEXT PRIMARY KEY,
				description TEXT NOT NULL DEFAULT '',
				kind        TEXT NOT NULL DEFAULT 'percent',
				value       INT NOT NULL DEFAULT 0,  -- % | centavos | dias
				plan_code   TEXT NOT NULL DEFAULT '',
				max_uses    INT NOT NULL DEFAULT 0,
				used_count  INT NOT NULL DEFAULT 0,
				active      BOOLEAN NOT NULL DEFAULT TRUE,
				expires_at  TIMESTAMPTZ,
				created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				created_by  TEXT NOT NULL DEFAULT ''
			);
			-- Registro de uso (auditoria + evita reuso pelo mesmo tenant).
			CREATE TABLE IF NOT EXISTS coupon_redemptions (
				id           BIGSERIAL PRIMARY KEY,
				code         TEXT NOT NULL,
				domain       TEXT NOT NULL,
				plan_code    TEXT NOT NULL DEFAULT '',
				discount_cents BIGINT NOT NULL DEFAULT 0,
				trial_days_added INT NOT NULL DEFAULT 0,
				redeemed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);
			CREATE INDEX IF NOT EXISTS idx_coupon_redemptions_code ON coupon_redemptions (code);
			CREATE UNIQUE INDEX IF NOT EXISTS idx_coupon_once_per_domain
				ON coupon_redemptions (code, domain);
		`},
		{"036_trial_days_config", `
			-- Dias de trial configuraveis pela UI admin (antes fixo em 7).
			-- Fica em billing_config por ser config global do produto.
			ALTER TABLE billing_config
				ADD COLUMN IF NOT EXISTS trial_days INT NOT NULL DEFAULT 7;
		`},
		{"035_billing_config_seed", `
			-- Preenche as credenciais de sandbox SE a config ainda estiver
			-- vazia. Cobre quem ja rodou a 034 antes deste seed existir (a
			-- linha foi criada em branco). Nunca sobrescreve config existente:
			-- so' age quando merchant_id esta vazio.
			UPDATE billing_config
			   SET merchant_id  = '39041',
			       merchant_key = 'e0kpdthr975n3xcv1r9lfg80',
			       environment  = COALESCE(NULLIF(environment,''), 'sandbox'),
			       processor_boleto = COALESCE(NULLIF(processor_boleto,''), '12'),
			       processor_pix    = COALESCE(NULLIF(processor_pix,''), '206'),
			       processor_card   = COALESCE(NULLIF(processor_card,''), '1'),
			       updated_at   = NOW()
			 WHERE id = 1 AND COALESCE(merchant_id,'') = '';
		`},
		{"033_plan_definitions", `
			-- Construtor de planos: cada plano e' uma linha configuravel pela
			-- UI admin (preco + quais features libera + limite de sessoes).
			-- code: identificador estavel referenciado por tenant_plans.plan.
			-- As flags substituem o gating hardcoded (HasProFeatures etc).
			CREATE TABLE IF NOT EXISTS plan_definitions (
				code             TEXT PRIMARY KEY,
				name             TEXT NOT NULL DEFAULT '',
				description      TEXT NOT NULL DEFAULT '',
				price_cents      BIGINT NOT NULL DEFAULT 0,
				max_sessions     INT NOT NULL DEFAULT 1,
				feat_templates   BOOLEAN NOT NULL DEFAULT FALSE,  -- templates + Cloud API Meta
				feat_automations BOOLEAN NOT NULL DEFAULT FALSE,  -- robots BizProc
				feat_sms         BOOLEAN NOT NULL DEFAULT FALSE,  -- campanhas SMS
				feat_reports     BOOLEAN NOT NULL DEFAULT FALSE,  -- relatorios + historico longo
				is_pro           BOOLEAN NOT NULL DEFAULT FALSE,  -- rotulo "pro" (compat gating antigo)
				active           BOOLEAN NOT NULL DEFAULT TRUE,   -- aparece pro cliente assinar
				sort_order       INT NOT NULL DEFAULT 0,
				created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);
			-- Seed dos 2 planos atuais preservando o comportamento hardcoded:
			-- basic = so conexao; pro = tudo. Idempotente (ON CONFLICT nada).
			INSERT INTO plan_definitions
				(code, name, description, price_cents, max_sessions,
				 feat_templates, feat_automations, feat_sms, feat_reports, is_pro, active, sort_order)
			VALUES
				('basic','Básico','Conecte o WhatsApp ao Bitrix24 e atenda pelo CRM.',
				 9900, 1, FALSE, FALSE, FALSE, FALSE, FALSE, TRUE, 1),
				('pro','Pro','Automação, campanhas, múltiplos números e relatórios completos.',
				 19900, 10, TRUE, TRUE, TRUE, TRUE, TRUE, TRUE, 2)
			ON CONFLICT (code) DO NOTHING;
		`},
		{"038_plan_trial", `
			-- ORDEM IMPORTA: roda DEPOIS da 033 (que cria plan_definitions).
			--
			-- O TRIAL e' um PLANO SEPARADO na lista (code 'trial'), com preco
			-- zero e suas proprias features. is_trial_default marca qual plano
			-- os novos tenants recebem; trial_days e' a duracao.
			ALTER TABLE plan_definitions
				ADD COLUMN IF NOT EXISTS trial_days INT NOT NULL DEFAULT 0;
			ALTER TABLE plan_definitions
				ADD COLUMN IF NOT EXISTS is_trial_default BOOLEAN NOT NULL DEFAULT FALSE;

			-- Remove o indice ANTES de qualquer INSERT/UPDATE. A versao
			-- anterior desta migration criava um indice sobre a COLUNA, que
			-- colide quando 2 planos ficam marcados — e travava o boot com
			-- "duplicate key". Dropar primeiro deixa os ajustes rodarem.
			DROP INDEX IF EXISTS idx_plan_trial_default;

			-- Cria o plano Trial. active=FALSE porque ele NAO e' assinavel —
			-- e' concedido automaticamente no install, nao aparece nos cards
			-- de compra do cliente. Features iguais ao Basico por padrao;
			-- o admin edita na aba Planos.
			INSERT INTO plan_definitions
				(code, name, description, price_cents, max_sessions,
				 feat_templates, feat_automations, feat_sms, feat_reports,
				 is_pro, active, sort_order, trial_days, is_trial_default)
			VALUES
				('trial','Trial','Período de teste gratuito para novos clientes.',
				 0, 1, FALSE, FALSE, FALSE, FALSE, FALSE, FALSE, 0, 7, TRUE)
			ON CONFLICT (code) DO NOTHING;

			-- Corrige estado inconsistente de deploys anteriores: se mais de
			-- um plano ficou marcado como trial, mantem so' o 'trial'.
			UPDATE plan_definitions SET is_trial_default = FALSE
			 WHERE is_trial_default AND code <> 'trial';

			-- Se ninguem estiver marcado (ex: alguem desmarcou), garante o
			-- 'trial' como default.
			UPDATE plan_definitions SET is_trial_default = TRUE
			 WHERE code = 'trial'
			   AND NOT EXISTS (SELECT 1 FROM plan_definitions WHERE is_trial_default);

			-- No maximo 1 plano marcado como default de trial.
			-- Indexa uma CONSTANTE com filtro parcial: so' as linhas com
			-- is_trial_default=TRUE entram no indice, e todas mapeiam pro
			-- mesmo valor — logo, so' 1 linha pode existir. E' a regra que
			-- queremos, sem colidir na criacao (ja limpamos duplicados acima).
			CREATE UNIQUE INDEX IF NOT EXISTS idx_plan_trial_default
				ON plan_definitions ((true)) WHERE is_trial_default;
		`},
		{"039_migrate_trial_tenants", `
			-- Tenants que estao em trial ainda apontam pro plano 'basic'
			-- (comportamento antigo). Move pro plano 'trial' pra ficar
			-- coerente com a nova modelagem. So' mexe em quem esta em trial.
			UPDATE tenant_plans
			   SET plan = 'trial', updated_at = NOW()
			 WHERE status = 'trial' AND plan = 'basic'
			   AND EXISTS (SELECT 1 FROM plan_definitions WHERE code = 'trial');
		`},
		{"032_plan_cancellation", `
			-- Cancelamento agendado (padrao SaaS): cliente cancela mas usa ate
			-- o fim do periodo pago (active_until). cancel_at_period_end=TRUE
			-- marca que NAO deve renovar. cancelled_at = quando cancelou.
			-- O acesso continua liberado ate active_until (IsAccessAllowed nao
			-- muda). Um job/checagem no vencimento move pra expired.
			ALTER TABLE tenant_plans
				ADD COLUMN IF NOT EXISTS cancel_at_period_end BOOLEAN NOT NULL DEFAULT FALSE;
			ALTER TABLE tenant_plans
				ADD COLUMN IF NOT EXISTS cancelled_at TIMESTAMPTZ;
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
		{"028_billing_charges", `
			-- Cobrancas geradas via gateway maxiPago (boleto/cartao).
			-- 1 row por tentativa de cobranca. reference_num e' a chave que
			-- amarra o postback do maxiPago de volta ao tenant.
			--
			-- status: 'pending' (gerada, aguardando pagto) | 'paid' |
			--         'failed' | 'cancelled'
			CREATE TABLE IF NOT EXISTS billing_charges (
				id                 UUID PRIMARY KEY,
				domain             TEXT NOT NULL,
				plan               TEXT NOT NULL DEFAULT 'pro',
				method             TEXT NOT NULL DEFAULT 'boleto',
				amount_cents       BIGINT NOT NULL,
				reference_num      TEXT NOT NULL UNIQUE,
				mp_order_id        TEXT NOT NULL DEFAULT '',
				mp_transaction_id  TEXT NOT NULL DEFAULT '',
				boleto_url         TEXT NOT NULL DEFAULT '',
				status             TEXT NOT NULL DEFAULT 'pending',
				raw_response       TEXT NOT NULL DEFAULT '',
				created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				paid_at            TIMESTAMPTZ
			);
			CREATE INDEX IF NOT EXISTS idx_billing_charges_domain ON billing_charges (domain);
			CREATE INDEX IF NOT EXISTS idx_billing_charges_status ON billing_charges (status);
		`},
		{"040_plan_payment_methods", `
			-- Formas de pagamento aceitas POR PLANO (configuravel no admin).
			-- Default: boleto ligado (ja funciona), PIX ligado por padrao tambem
			-- — o checkout so' oferece PIX se o gateway estiver configurado, entao
			-- ligar aqui e' seguro. O admin desliga por plano se quiser.
			ALTER TABLE plan_definitions
				ADD COLUMN IF NOT EXISTS accept_boleto BOOLEAN NOT NULL DEFAULT TRUE;
			ALTER TABLE plan_definitions
				ADD COLUMN IF NOT EXISTS accept_pix BOOLEAN NOT NULL DEFAULT TRUE;
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
