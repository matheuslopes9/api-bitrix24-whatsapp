-- ─── Sessões WhatsApp ──────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS whatsapp_sessions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    jid         TEXT NOT NULL UNIQUE,          -- JID do número (ex: 5511999999999@s.whatsapp.net)
    phone       TEXT NOT NULL,                 -- Número legível
    display_name TEXT,
    status      TEXT NOT NULL DEFAULT 'active', -- active | disconnected | banned
    session_file TEXT NOT NULL,                -- Caminho do arquivo .db da sessão
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen   TIMESTAMPTZ
);

-- ─── Mapeamento WhatsApp ↔ Bitrix24 ────────────────────────────────────────
CREATE TABLE IF NOT EXISTS contact_mapping (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    wa_jid          TEXT NOT NULL,
    wa_phone        TEXT NOT NULL,
    wa_name         TEXT,
    bitrix_entity   TEXT NOT NULL DEFAULT 'lead', -- lead | deal | contact
    bitrix_id       BIGINT NOT NULL,
    bitrix_chat_id  TEXT,
    session_id      UUID REFERENCES whatsapp_sessions(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (wa_jid, session_id)
);

-- ─── Histórico de Mensagens ────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS messages (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    wa_message_id   TEXT NOT NULL UNIQUE,       -- ID interno do WhatsApp
    session_id      UUID REFERENCES whatsapp_sessions(id) ON DELETE SET NULL,
    contact_id      UUID REFERENCES contact_mapping(id) ON DELETE SET NULL,
    direction       TEXT NOT NULL,              -- inbound | outbound
    message_type    TEXT NOT NULL DEFAULT 'text', -- text | image | audio | document | video | sticker
    content         TEXT,
    media_url       TEXT,
    media_mime      TEXT,
    media_size      BIGINT,
    status          TEXT NOT NULL DEFAULT 'received', -- received | queued | delivered | failed
    retry_count     INT NOT NULL DEFAULT 0,
    error_msg       TEXT,
    sent_at         TIMESTAMPTZ,
    delivered_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ─── Tokens OAuth2 Bitrix24 ───────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS bitrix_tokens (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    domain          TEXT NOT NULL UNIQUE,
    access_token    TEXT NOT NULL,
    refresh_token   TEXT NOT NULL,
    expires_at      TIMESTAMPTZ NOT NULL,
    scope           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ─── Log de Eventos (auditoria) ───────────────────────────────────────────
CREATE TABLE IF NOT EXISTS event_log (
    id          BIGSERIAL PRIMARY KEY,
    event_type  TEXT NOT NULL,
    payload     JSONB,
    source      TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ─── Índices ──────────────────────────────────────────────────────────────
CREATE INDEX idx_messages_session   ON messages (session_id);
CREATE INDEX idx_messages_contact   ON messages (contact_id);
CREATE INDEX idx_messages_status    ON messages (status);
CREATE INDEX idx_messages_created   ON messages (created_at DESC);
CREATE INDEX idx_contact_jid        ON contact_mapping (wa_jid);
CREATE INDEX idx_contact_bitrix     ON contact_mapping (bitrix_id);
CREATE INDEX idx_event_log_type     ON event_log (event_type);
CREATE INDEX idx_event_log_created  ON event_log (created_at DESC);
