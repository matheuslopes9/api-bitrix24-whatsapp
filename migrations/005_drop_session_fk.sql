-- Remove FKs de session_id que quebram quando o JID muda de device suffix.
-- session_id continua existindo como referência soft — sem constraint de integridade.
ALTER TABLE contact_mapping DROP CONSTRAINT IF EXISTS contact_mapping_session_id_fkey;
ALTER TABLE messages        DROP CONSTRAINT IF EXISTS messages_session_id_fkey;

-- Garante que UpsertSession com mesmo JID atualiza o ID também (evita UUID duplicado em memória vs banco).
-- Recria o upsert para incluir id no ON CONFLICT UPDATE.
