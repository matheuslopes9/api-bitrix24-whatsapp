-- ─── Contas Bitrix24 vinculadas a sessões WhatsApp ────────────────────────────
-- Cada sessão WA pode ter exatamente uma conta Bitrix24 vinculada.
-- O token OAuth fica em bitrix_tokens indexado por domain.
CREATE TABLE IF NOT EXISTS bitrix_accounts (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_jid   TEXT NOT NULL UNIQUE,  -- JID do número WA (FK lógica para whatsapp_sessions.jid)
    domain        TEXT NOT NULL,         -- ex: "https://empresa.bitrix24.com.br"
    client_id     TEXT NOT NULL,
    client_secret TEXT NOT NULL,
    open_line_id  INT  NOT NULL DEFAULT 1,
    connector_id  TEXT NOT NULL DEFAULT 'whatsapp_uc',
    redirect_uri  TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'pending',  -- pending | active
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_bitrix_accounts_jid    ON bitrix_accounts (session_jid);
CREATE INDEX IF NOT EXISTS idx_bitrix_accounts_domain ON bitrix_accounts (domain);
