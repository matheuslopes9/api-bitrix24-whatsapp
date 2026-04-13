-- ─── Portais Bitrix24 — Partner App Global ─────────────────────────────────
-- Registra cada portal que instalou o app via Bitrix24 Marketplace.
-- Diferente de bitrix_accounts (que vincula WA + Bitrix via UI admin),
-- esta tabela é preenchida automaticamente no install handler do Partner App.
CREATE TABLE IF NOT EXISTS bitrix_portals (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    domain        TEXT NOT NULL UNIQUE,        -- ex: "empresa.bitrix24.com.br" (sem https://)
    access_token  TEXT NOT NULL DEFAULT '',
    refresh_token TEXT NOT NULL DEFAULT '',
    expires_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    member_id     TEXT NOT NULL DEFAULT '',    -- identificador único do portal no Bitrix
    connector_id  TEXT NOT NULL DEFAULT 'whatsapp_uc',
    open_line_id  INT  NOT NULL DEFAULT 0,     -- 0 = não configurado ainda
    installed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_bitrix_portals_domain    ON bitrix_portals (domain);
CREATE INDEX IF NOT EXISTS idx_bitrix_portals_member_id ON bitrix_portals (member_id);
