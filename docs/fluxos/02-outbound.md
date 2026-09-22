# Fluxo 2 — Operador responde o cliente

O caminho de uma mensagem digitada pelo operador no Bitrix até chegar no
WhatsApp do cliente.

## O caminho

```
 operador digita na Linha Aberta do Bitrix
          │
          ▼
 Bitrix dispara ONIMCONNECTORMESSAGEADD → webhook do UC Talk
          │   (form-encoded; o mesmo endpoint também recebe JSON)
          ▼
 handler monta o OutboundJob                    internal/api/handlers.go
          │   q.PushOutbound(job)
          ▼
 fila Redis "queue:outbound"                        queue/queue.go:17
          │   worker pool (BLPop)
          ▼
 ┌──────────────────────────────────┐
 │ é sessão Cloud API?              │  IsCloudJID(job.SessionJID)
 └──────────────────────────────────┘
      │ sim                      │ não
      ▼                          ▼
 handleCloudOutbound        caminho whatsmeow (QR)
 (Graph API da Meta)             │
 cmd/server/cloud_outbound.go    ├─ SendTyping — mostra "digitando..."
                                 │   antes de enviar, pra não parecer robô
                                 │
                                 ├─ resolve o arquivo (FileURL do Bitrix ou
                                 │   MediaURL base64), com pre-check de
                                 │   tamanho ANTES do download
                                 │
                                 ├─ waManager.Send → whatsmeow
                                 │
                                 ├─ InsertMessage (direction=outbound)
                                 │   from_jid = sessão, SEM device suffix
                                 │   to_jid   = destinatário; se for @lid,
                                 │              resolve pro telefone real via
                                 │              lid_phone_map
                                 │
                                 └─ ConnectorSetOutboundDelivery
                                     para o spinner na mensagem do operador
```

## Os dois identificadores do destinatário

O WhatsApp Multi-Device às vezes entrega o remetente como **`@lid`**
(LinkedID) em vez do telefone. São a mesma pessoa, com endereços diferentes.

```
 msg inbound com Sender @lid  →  UpsertLIDPhoneMap(lid, telefone)
                                 popula o lid_phone_map
                                            │
 msg outbound pra um @lid      →  GetPhoneByLID(lid)
                                 grava to_jid como telefone real
                                 fallback: contact_mapping
```

Sem essa resolução, a aba CRM não acha a mensagem ao buscar pelo número — a
conversa parece vazia mesmo tendo mensagens no banco.

## ⚠ Pontos sensíveis

### Confirmação de entrega depende de dois campos do job

```go
if job.BitrixConnector != "" && job.BitrixImMsgID != "" { /* confirma */ }
else { log.Warn("outbound delivery: skipped (missing connector or msg_id)") }
```

Se algum dos dois vier vazio, a mensagem **é enviada normalmente** mas o
spinner no Bitrix nunca para — o operador acha que falhou. Procure
`outbound delivery: skipped` no log.

### `im.message.add` não reabre Open Line encerrada

Por isso o caminho de envio abandonou `GetCRMChatLastID`/`SendOperatorMessage`.
Hoje é um caminho único: **envia direto no WhatsApp + espelha via
`PushInbound`**. O imconnector é unidirecional (cliente → openline), então
mensagem de saída só aparece na timeline se for espelhada como se fosse
inbound.

### Arquivo grande

O pre-check usa `files[0][size]` que o próprio webhook do Bitrix manda,
bloqueando **antes** do download. Sem isso, o worker baixava o arquivo inteiro
pra descobrir que estourava o limite — e os retries geravam `GOAWAY` do
servidor Bitrix.

## Envio pela aba CRM e pelo robô BizProc

Dois outros produtores da mesma fila outbound:

| Origem | Particularidade |
|---|---|
| Aba CRM do contato | abre chat na Linha Aberta com rótulo "Mensagem enviada Externamente (nome do usuário)" |
| Robô BizProc | espelha na Linha Aberta com prefixo `🤖 *Automação:*` |

Ambos passam pelo gate de permissão: o operador só envia por um número que o
usuário master liberou pra ele (ver [04-telas.md](04-telas.md)).
