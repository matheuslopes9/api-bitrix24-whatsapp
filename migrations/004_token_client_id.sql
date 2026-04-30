-- Adiciona client_id à tabela bitrix_tokens para separar tokens do Local App
-- e do Partner App que compartilham o mesmo domain mas têm client_ids diferentes.
-- A chave única passa de (domain) para (domain, client_id).

ALTER TABLE bitrix_tokens ADD COLUMN IF NOT EXISTS client_id TEXT NOT NULL DEFAULT '';

-- Remove constraint antiga e cria nova com (domain, client_id)
ALTER TABLE bitrix_tokens DROP CONSTRAINT IF EXISTS bitrix_tokens_domain_key;
ALTER TABLE bitrix_tokens ADD CONSTRAINT bitrix_tokens_domain_client_id_key UNIQUE (domain, client_id);
