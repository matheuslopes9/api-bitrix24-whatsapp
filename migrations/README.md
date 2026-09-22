# ⚠️ ESTES ARQUIVOS NÃO SÃO EXECUTADOS

Os `.sql` desta pasta são **legado histórico**. Nada no código lê este
diretório — não há `embed`, `os.ReadDir` nem runner de `.sql`.

**A única fonte de schema em execução** é o array `migrations` em
[internal/db/db.go](../internal/db/db.go), que roda inteiro a cada boot.

## Por que isso importa

Essa duplicidade já causou um bug em produção. O `CREATE TABLE messages` do
array Go (migration `005`) foi escrito como uma cópia do
[001_init.sql](001_init.sql) e **perdeu 4 colunas** no caminho:
`retry_count`, `error_msg`, `sent_at`, `delivered_at`.

Como só o array Go roda, qualquer banco criado pelo código atual nasceu sem
essas colunas — e todo `SELECT`/`UPDATE` que as referenciava quebrava com
`SQLSTATE 42703`. A aba Histórico não abria conversa e `UpdateMessageStatus`
falhava silenciosamente (o erro era descartado com `_ =`), então mensagem
nenhuma chegava a marcar entrega.

Corrigido pela migration `043_messages_colunas_faltantes`.

## Regra

Ao alterar schema, mexa **somente** em `internal/db/db.go`. Se for consultar
um `.sql` daqui como referência, lembre que ele pode divergir do que roda de
verdade — confira contra o array Go antes de confiar.

As regras de escrita de migration (idempotência obrigatória, ordem do array,
limpeza antes de constraint) estão em
[docs/aprendizados/01-arquitetura.md](../docs/aprendizados/01-arquitetura.md).
