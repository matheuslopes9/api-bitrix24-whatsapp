-- Adiciona from_jid e to_jid na tabela messages para busca por número sem depender de contact_id.
-- from_jid: JID de quem enviou (ex: 5519987717792@s.whatsapp.net)
-- to_jid:   JID de quem recebeu
-- Ambos são TEXT nullable — retrocompatível com registros antigos.
ALTER TABLE messages ADD COLUMN IF NOT EXISTS from_jid TEXT DEFAULT '';
ALTER TABLE messages ADD COLUMN IF NOT EXISTS to_jid   TEXT DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_messages_from_jid ON messages (from_jid);
CREATE INDEX IF NOT EXISTS idx_messages_to_jid   ON messages (to_jid);
