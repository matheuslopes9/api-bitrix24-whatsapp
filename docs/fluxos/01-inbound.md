# Fluxo 1 — Mensagem do cliente chega no Bitrix

O caminho que uma mensagem percorre desde o celular do cliente até aparecer na
Linha Aberta (Open Channel) do Bitrix24.

## O caminho

```
 cliente manda msg no WhatsApp
          │
          ▼
 ┌────────────────────────┐
 │ whatsmeow events.Message│  buildEventHandler          manager.go:1042
 └────────────────────────┘
          │  m.onMsg(sess.ID, sess.JID, evt)
          ▼
 ┌────────────────────────┐
 │ buildMessageHandler     │  monta o InboundJob          cmd/server/main.go
 │  · baixa mídia          │
 │  · resolve @lid → fone  │
 │  · InsertMessage        │
 └────────────────────────┘
          │  q.PushInbound(job)
          ▼
 ┌────────────────────────┐
 │ fila Redis "queue:inbound"│                            queue/queue.go:17
 └────────────────────────┘
          │  worker pool (BLPop)
          ▼
 ┌────────────────────────┐
 │ Processor.ProcessInbound│                       bitrix/processor.go:80
 └────────────────────────┘
          │
          ├─ 1. GetBitrixAccountByJID(job.SessionJID)   ⚠ ver "Onde quebra" (a)
          │     descobre domínio, open_line_id, connector_id do tenant
          │
          ├─ 2. ensureContact(job)
          │     cria/acha o contato no CRM e o contact_mapping local
          │
          ├─ 3. monta o chat_id do Open Channel
          │     1-a-1 → JID do remetente   |   grupo → JID do grupo
          │     normalizeChatID tira o device suffix       processor.go:15
          │
          ├─ 4. ConnectorSendMessage → imconnector.send.messages
          │     ESTE é o passo que faz a msg aparecer no Contact Center
          │
          ├─ 5. UpsertContact grava o chat_id devolvido
          │
          ├─ 6. ConnectorSetDelivery → confirma recebimento pro Bitrix
          │
          └─ 7. markStatus(delivered)                  ⚠ ver "Onde quebra" (b)
```

## Onde quebra

### (a) A conta Bitrix "não é encontrada" e a mensagem morre

`GetBitrixAccountByJID` casa a sessão do job com a linha de `bitrix_accounts`.
Se não achar, `ProcessInbound` aborta no passo 1 — a mensagem **nunca chega no
Contact Center**, e o log mostra `bitrix account not found`.

Duas causas já vistas:

1. **Match por `SPLIT_PART(session_jid, ':', 1)`** (corrigido). Só funciona
   quando o JID *tem* device suffix. Num JID sem suffix o split devolve a
   string inteira, `558196807479@s.whatsapp.net`, que nunca casa com o lado
   que tem suffix. Agora é o duplo split (`'@'` e depois `':'`).

2. **Vínculo apontando pra sessão errada.** Com duas linhas do mesmo número em
   `whatsapp_sessions` (ver [03-sessoes.md](03-sessoes.md)), o
   `bitrix_accounts` era re-vinculado a cada reconexão — e a mensagem podia
   chegar pela sessão que não era a vinculada no momento.

**Como diagnosticar:** procure `bitrix account not found` no log. Se aparecer,
compare o `session_jid` logado com o que está em `bitrix_accounts`:

```sql
SELECT session_jid, domain, open_line_id, connector_id, status, updated_at
  FROM bitrix_accounts
 WHERE SPLIT_PART(SPLIT_PART(session_jid,'@',1),':',1) = '558196807479';
```

Deve haver **uma** linha. Mais de uma, ou nenhuma, é o problema.

### (b) A mensagem chega mas nunca marca entrega

A tabela `messages` nasceu sem as colunas `retry_count`, `error_msg`,
`sent_at` e `delivered_at` — o `CREATE TABLE` do array Go
(`000_base_schema`) era uma cópia reduzida do `migrations/001_init.sql`, que
não é executado (ver
[migrations/README.md](../../migrations/README.md)).

Com isso `UpdateMessageStatus` falhava **sempre**, com `SQLSTATE 42703`. E as
quatro chamadas no processor descartavam o erro com `_ =`, então nada aparecia
no log: a mensagem era entregue no Contact Center, mas ficava eternamente em
`received` no banco.

Corrigido pela migration `043` + `Processor.markStatus`, que loga a falha.

### (c) `im.message.add` não reabre sessão encerrada

Regra de ouro herdada de `docs/aprendizados/04-integracao-bitrix.md`: o
imconnector é **unidirecional** (cliente → openline). Para injetar uma
mensagem de *saída* na timeline, é preciso espelhá-la como inbound via
`PushInbound`. `im.message.add` **não** reabre uma Open Line já fechada.

## Como testar o fluxo inteiro

1. Mande uma mensagem do celular pro número conectado.
2. No log, a sequência esperada é:
   ```
   message event received        (whatsmeow recebeu)
   ProcessInbound called         (saiu da fila)
   bitrix account found          (tenant resolvido)
   calling set delivery
   set delivery ok
   inbound delivered to contact center
   ```
3. Qualquer parada antes de `inbound delivered` aponta o passo que falhou.
