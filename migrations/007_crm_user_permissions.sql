-- ─── Permissões de acesso ao CRM tab ────────────────────────────────────────
-- Controla quais usuários Bitrix24 (user_id) podem abrir o iframe de CRM
-- (rotas /bitrix/crm/tab) para cada portal/dominio.
--
-- Modelo: estrito por padrão — se nao existir nenhuma linha para o dominio,
-- ninguem tem acesso. Para liberar, super-admin insere (domain, user_id).
-- Quando existe pelo menos 1 linha de um dominio, so quem esta na lista
-- pode acessar (modo whitelist).
CREATE TABLE IF NOT EXISTS crm_user_permissions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    domain      TEXT NOT NULL,                 -- bitrix_portals.domain normalizado (sem https://)
    user_id     TEXT NOT NULL,                 -- user_id do Bitrix (numerico mas pode ser string)
    user_name   TEXT NOT NULL DEFAULT '',      -- nome legivel (snapshot na hora de liberar)
    granted_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    granted_by  TEXT NOT NULL DEFAULT 'super-admin',
    UNIQUE (domain, user_id)
);

CREATE INDEX IF NOT EXISTS idx_crm_user_permissions_domain ON crm_user_permissions (domain);
