# WhatsApp ↔ Bitrix24 Connector

Conector entre WhatsApp e Bitrix24 Contact Center, suportando múltiplas sessões simultâneas (multi-tenant).

## Stack

| Componente | Tecnologia |
|---|---|
| Linguagem | Go 1.25 |
| WhatsApp | whatsmeow v0.0.0-20260327181659 |
| HTTP | Fiber v2 |
| Banco de dados | PostgreSQL (pgxpool) |
| Cache / Filas | Redis |
| Persistência de sessões WA | SQLite (um arquivo por número) |
| Métricas | Prometheus |
| Deploy | EasyPanel + Docker |

---

## Arquitetura geral

```
WhatsApp ──────► Manager ──────► Redis Queue ──────► Worker Pool ──────► Bitrix24 REST API
                   │                                                            │
                   │◄───────────────────────────────────────────────────────────┘
                         (resposta do operador via webhook POST /bitrix/connector/event)
```

### Componentes principais

| Componente | Arquivo | Função |
|---|---|---|
| **Manager** | `internal/whatsapp/manager.go` | Gerencia N sessões WhatsApp em goroutines independentes. Mantém mapa `sessions[jid]` em memória. |
| **Processor** | `internal/bitrix/processor.go` | Converte InboundJob em chamada REST ao Bitrix24 (imconnector.send.messages + upload de mídia). |
| **Client** | `internal/bitrix/client.go` | Cliente HTTP para a API REST do Bitrix24. Gerencia token OAuth2 com refresh automático. |
| **Queue** | `internal/queue/queue.go` | Filas Redis `queue:inbound` e `queue:outbound` com retry exponencial. |
| **Worker Pool** | `internal/queue/worker.go` | 20 goroutines paralelas consumindo as filas. Mutex por JID de destino (outbound). |
| **Watchdog** | `internal/watchdog/watchdog.go` | Verifica sessões a cada 30s e reconecta automaticamente. |
| **API** | `internal/api/` | Handlers HTTP (Fiber). Dashboard, UI de gestão, webhooks Bitrix, API pública. |
| **Repository** | `internal/db/repository.go` | Acesso ao PostgreSQL (sessões, tokens, contas, mensagens, contatos). |

---

## Fluxo 1: WhatsApp → Bitrix24 (FUNCIONANDO)

```
1. Cliente envia mensagem no WhatsApp
2. whatsmeow dispara evento *events.Message
3. buildEventHandler (manager.go) recebe o evento
4. buildMessageHandler (main.go) é chamado via m.onMsg(sess.ID, sess.JID, evt)
5. Mensagem salva no banco (status: received)
6. Mídia baixada (imagem, vídeo, doc, áudio, vCard, sticker)
7. InboundJob empurrado para Redis queue:inbound
8. Worker consome o job:
   a. GetBitrixAccountByJID → busca credenciais pelo session_jid
   b. UploadToDisk (se mídia) → disk.storage.uploadfile (base64 JSON)
   c. imconnector.send.messages → entrega ao Contact Center do Bitrix
   d. imconnector.send.status.delivery → confirma entrega (remove spinner)
9. Mensagem aparece na conversa do operador no Bitrix24
```

**Campos críticos no banco:**
- `bitrix_accounts.session_jid` — chave do match, normalizado sem device suffix via `SPLIT_PART(jid, ':', 1)`
- `bitrix_accounts.connector_id` — ex: `whatsapp_uc`
- `bitrix_accounts.open_line_id` — ID da Open Line configurada no Bitrix24
- `bitrix_accounts.domain` — ex: `https://uctdemo.bitrix24.com`

---

## Fluxo 2: Bitrix24 → WhatsApp (EM INVESTIGAÇÃO)

```
1. Operador responde no Contact Center do Bitrix24
2. Bitrix24 DEVERIA chamar POST /bitrix/connector/event (webhook)
3. Handler extrai texto/arquivo, monta OutboundJob
4. OutboundJob empurrado para Redis queue:outbound
5. Worker consome:
   a. SendTyping → "digitando..." por 1.5–4s
   b. waManager.Send / SendDocument / SendAudio
   c. imconnector.send.status.delivery (confirma outbound)
6. Mensagem chega ao cliente no WhatsApp
```

**PROBLEMA ATUAL:** O passo 2 não acontece. O Bitrix24 não chama `POST /bitrix/connector/event` quando o operador responde. O endpoint existe e está registrado corretamente, mas o Bitrix nunca aciona o webhook.

### O que já foi tentado e NÃO funcionou:

| Tentativa | Resultado |
|---|---|
| `event.bind` com `ONIMCONNECTORMESSAGEADD` | Bitrix aceita o bind (`true`) mas nunca chama o endpoint |
| `imconnector.register` com `LINES.MESSAGES_HANDLER` | Quebrou o inbound (WA→Bitrix parou de funcionar) |
| `imconnector.connector.data.set` com `DATA.SEND_MESSAGE` após activate | Quebrou o inbound |
| `imconnector.connector.data.set` com `DATA.CONFIG.send_message` antes do activate | Quebrou o inbound |

### O que o concorrente (Whatcrm) faz diferente:
Nos logs do Bitrix24, o app "WhatsApp por Whatcrm" chama `imconnector.connector.data.set` e depois **recebe** `ONIMCONNECTORMESSAGEADD`. A nossa app também chama `imconnector.connector.data.set`, mas o inbound quebra toda vez que adicionamos essa chamada ao fluxo de ativação.

### Hipóteses em aberto:
1. O `imconnector.connector.data.set` precisa de um formato específico ainda não descoberto
2. O Whatcrm usa um fluxo de instalação completamente diferente (via Marketplace, não app local)
3. O connector precisa estar registrado com o `PLACEMENT_HANDLER` apontando para uma URL que retorna HTML específico
4. O `event.bind` funciona mas o evento só é disparado para apps do Marketplace, não apps locais

### Estado atual do código (seguro — não quebra o inbound):
```
register → activate → event.bind (ONIMCONNECTORMESSAGEADD)
```
O `imconnector.connector.data.set` foi removido do fluxo de ativação.

---

## Dashboard (`/dashboard`)

Interface web de gestão acessível sem autenticação (protegida por proxy interno).

### Seções

**Painel** — visão geral em tempo real:
- Contador de sessões WA ativas
- Filas Redis (inbound, outbound, dead)
- Gráfico de mensagens dos últimos 7 dias

**Sessões WhatsApp** — gestão de dispositivos:
- Lista de JIDs conectados com status
- Botão conectar (abre modal de QR scan)
- Botão desconectar (confirmação via modal)
- Auto-refresh a cada 10s

**Filas Bitrix** — gestão de portais e conectores:
- Cards por portal Bitrix24 instalado
- Mostra: domain, connector ID, open line ID, sessão WA vinculada
- Botão "Ativar Conector" — executa: register → activate → event.bind
- Botão "Vincular" — associa sessão WA ao portal
- Botão "Desvincular"

**Relatórios** — estatísticas de mensagens (requer `X-API-Key`).

**Tema claro/escuro** — persistido em `localStorage`.

---

## Endpoints HTTP

### UI pública (sem autenticação)

| Método | Rota | Descrição |
|---|---|---|
| `GET` | `/dashboard` | Dashboard principal |
| `GET` | `/connect` | Página legada de conexão WA |
| `GET` | `/ui/overview` | JSON: sessões ativas + status das filas |
| `GET` | `/ui/sessions` | JSON: lista de JIDs ativos |
| `POST` | `/ui/sessions` | Inicia nova sessão WA (body: `{"phone":"551999..."}`) |
| `GET` | `/ui/sessions/:phone/qr` | Polling do QR code (base64 ou vazio) |
| `DELETE` | `/ui/sessions/remove?jid=` | Desconecta e remove sessão |
| `GET` | `/ui/bitrix/queues` | Lista portais Bitrix24 instalados |
| `PUT` | `/ui/bitrix/queues` | Atualiza open_line_id de um portal |
| `POST` | `/ui/bitrix/queues/link` | Vincula sessão WA a um portal |
| `DELETE` | `/ui/bitrix/queues/link` | Remove vínculo sessão WA ↔ portal |
| `POST` | `/ui/bitrix/queues/activate` | Executa register + activate + event.bind |
| `GET` | `/ui/bitrix/accounts` | Lista contas Bitrix24 (modo legado) |
| `POST` | `/ui/bitrix/accounts` | Cria conta Bitrix24 (modo legado) |
| `DELETE` | `/ui/bitrix/accounts` | Remove conta Bitrix24 |

### WhatsApp API (requer `X-API-Key`)

| Método | Rota | Descrição |
|---|---|---|
| `POST` | `/wa/sessions` | Inicia sessão |
| `GET` | `/wa/sessions` | Lista sessões |
| `GET` | `/wa/sessions/:phone/qr` | Polling QR |
| `DELETE` | `/wa/sessions/:jid` | Remove sessão |
| `POST` | `/wa/send` | Envia mensagem de texto direto |

### Bitrix24 webhooks (sem autenticação)

| Método | Rota | Descrição |
|---|---|---|
| `GET/POST` | `/bitrix/callback` | Callback de instalação do app local (ONAPPINSTALL) |
| `POST` | `/bitrix/connector/event` | **Webhook esperado para respostas do operador** (ONIMCONNECTORMESSAGEADD) |
| `POST` | `/bitrix/install` | Installer URL do Partner App (Marketplace) |
| `POST` | `/bitrix/auth` | Token BX24.js enviado pela página /bitrix-connect |
| `POST` | `/bitrix/partner/link` | Vincula sessão WA ao portal após QR scan (Partner App) |
| `GET` | `/bitrix-connect` | Application URL do Partner App (iframe no Bitrix24) |
| `GET` | `/bitrix/oauth/start` | Info de autenticação OAuth2 |

### Sistema

| Método | Rota | Descrição |
|---|---|---|
| `GET` | `/health` | Health check |
| `GET` | `/metrics` | Métricas Prometheus |
| `GET` | `/stats/daily` | Estatísticas diárias (requer `X-API-Key`) |
| `GET` | `/stats/queues` | Status das filas (requer `X-API-Key`) |

---

## Banco de dados

Tabelas (migration: `migrations/001_init.sql`):

| Tabela | Descrição |
|---|---|
| `whatsapp_sessions` | Sessões WA — JID, telefone, status (`active`/`disconnected`/`banned`), path do SQLite |
| `bitrix_accounts` | Vinculação sessão WA ↔ conta Bitrix24 — domain, client_id/secret, open_line_id, connector_id |
| `bitrix_tokens` | Tokens OAuth2 por domain — access_token, refresh_token, expires_at |
| `bitrix_portals` | Portais instalados via Partner App (Marketplace) — member_id, tokens, connector, open_line |
| `contact_mapping` | Mapeamento JID WhatsApp ↔ chat_id Bitrix24 (normalizado sem device suffix) |
| `messages` | Log de todas as mensagens trocadas — direção, tipo, status, conteúdo |

---

## Tipos de mídia suportados

### WA → Bitrix24

| Tipo | Comportamento |
|---|---|
| Texto | Enviado diretamente |
| Imagem (jpg/png) | Download + upload para Bitrix Disk + attachment |
| Vídeo (mp4) | Download + upload para Bitrix Disk + attachment |
| Documento (pdf, xlsx, etc) | Download + upload para Bitrix Disk + attachment |
| Áudio PTT (voice note) | Upload como `voice.ogg` + attachment |
| Áudio pré-gravado | Tenta download com fallback `DownloadAny`; se falhar, `[Áudio]` |
| Contato (vCard) | Upload como `.vcf` + attachment |
| Sticker | Upload como `.webp` + attachment |

### Bitrix24 → WA

| MIME | Comportamento |
|---|---|
| `audio/mpeg` (mp3) | `SendAudio` — aparece como áudio nativo com botão play |
| Outros áudios (wav, ogg) | `SendDocument` — WA não suporta esses formatos como áudio |
| Imagens, vídeos, docs | `SendDocument` |
| Texto | `Send` — mensagem de texto simples |

---

## Multi-tenant

Cada sessão WhatsApp é vinculada a um portal Bitrix24 diferente:

- Tabela `bitrix_accounts`: `session_jid` → credenciais Bitrix24
- O JID é normalizado: `5519910001772:27@s.whatsapp.net` → chave `5519910001772` (ignora device suffix que muda a cada reconexão)
- Query SQL usa `SPLIT_PART(session_jid, ':', 1)` para match robusto
- Tabela `bitrix_portals`: portais instalados via Marketplace (Partner App), com fluxo OAuth2 próprio

---

## Deploy no EasyPanel

### Variáveis de ambiente obrigatórias

> **Importante:** Não use `#` em valores — o EasyPanel interpreta como comentário e trunca.

```env
APP_PORT=3000
APP_ENV=production
APP_SECRET=<string-forte-sem-hash>
APP_BASE_URL=https://<dominio-easypanel>   ← OBRIGATÓRIO para webhooks Bitrix funcionarem

POSTGRES_HOST=<nome-servico>
POSTGRES_PORT=5432
POSTGRES_USER=admin
POSTGRES_PASSWORD=<senha>
POSTGRES_DB=whatsapp-bitrix
POSTGRES_SSLMODE=disable

REDIS_HOST=<nome-servico>
REDIS_PORT=6379
REDIS_PASSWORD=<senha>
REDIS_DB=0

WA_SESSIONS_DIR=./sessions
QUEUE_WORKERS=20
QUEUE_MAX_RETRY=5
QUEUE_RETRY_BASE_DELAY_MS=1000
WATCHDOG_PING_INTERVAL_SECS=30
```

### Passos de setup

1. Criar serviço App no EasyPanel apontando para `matheuslopes9/api-bitrix24-whatsapp`
2. Configurar variáveis de ambiente (incluindo `APP_BASE_URL`)
3. Rodar migrations no PostgreSQL: `psql -U admin -d whatsapp-bitrix` → colar `migrations/001_init.sql`
4. Acessar `/dashboard` → **Sessões WhatsApp** → conectar número via QR
5. No Bitrix24: instalar app local com Handler Path `https://<dominio>/bitrix/callback`, escopos: `crm, im, imopenlines, imconnector, disk, contact_center`
6. Em `/dashboard` → **Filas Bitrix** → vincular sessão WA ao portal e clicar **Ativar Conector**

---

## Problemas conhecidos

| Problema | Causa | Status |
|---|---|---|
| Bitrix→WA não funciona | Bitrix não chama `POST /bitrix/connector/event` | **EM INVESTIGAÇÃO** |
| `imconnector.connector.data.set` quebra o inbound | Formato/timing incorreto corrompe estado do connector | Removido do fluxo — investigar formato correto |
| `invalid media hmac` no áudio | MediaKey indisponível para áudios de outros dispositivos | Limitação criptográfica do WA — fallback para `[Áudio]` |
| Device suffix muda a cada reconexão | WA muda `:27` → `:28` → `:29` a cada novo par | `SPLIT_PART` na query resolve |
| `#` em variáveis trunca no EasyPanel | EasyPanel interpreta `#` como comentário | Não usar `#` em senhas/secrets |
| SQLite "readonly database" no logout | Volume de sessões montado como read-only | Warning ignorável — desconexão funciona |
| `@` no JID truncado em path routing | Fiber trata `@` como separador | Usa query param `?jid=` em vez de path param |

---

## Estrutura do projeto

```
.
├── cmd/server/main.go          # Entrypoint — wiring de todos os componentes
├── internal/
│   ├── api/
│   │   ├── server.go           # Rotas Fiber
│   │   ├── handlers.go         # /wa/*, /bitrix/*, /health
│   │   ├── ui.go               # /ui/*, /connect
│   │   ├── dashboard.go        # HTML/CSS/JS do dashboard
│   │   ├── partner.go          # Fluxo Partner App (Marketplace)
│   │   └── assets.go           # Servir logo, favicon, chart.js
│   ├── bitrix/
│   │   ├── client.go           # REST Bitrix24: OAuth2, imconnector, disk
│   │   └── processor.go        # Lógica WA→Bitrix: contact + upload + entrega
│   ├── config/config.go        # Viper — lê variáveis de ambiente
│   ├── db/
│   │   ├── db.go               # pgxpool connection
│   │   ├── models.go           # Structs das tabelas
│   │   └── repository.go       # Queries SQL
│   ├── queue/
│   │   ├── queue.go            # Redis RPUSH/BLPOP + retry + dead queue
│   │   └── worker.go           # Worker pool — 20 goroutines inbound + 20 outbound
│   ├── telemetry/metrics.go    # Contadores Prometheus
│   ├── watchdog/watchdog.go    # Reconexão automática de sessões
│   └── whatsapp/
│       ├── manager.go          # Sessões whatsmeow: QR, connect, disconnect, send
│       └── session.go          # Struct Session
├── migrations/001_init.sql     # Schema PostgreSQL completo
├── Dockerfile
└── docker-compose.yml
```

---

## Status atual (14/04/2026)

| Funcionalidade | Status |
|---|---|
| Deploy EasyPanel + Docker | ✅ Funcionando |
| PostgreSQL + Redis conectados | ✅ Funcionando |
| Sessões WhatsApp (QR scan, reconexão, watchdog) | ✅ Funcionando |
| Dashboard UI (sessões, filas, relatórios, tema claro/escuro) | ✅ Funcionando |
| **WA → Bitrix24** (texto, imagem, vídeo, doc, áudio, vCard, sticker) | ✅ Funcionando |
| Upload de mídia para Bitrix Disk | ✅ Funcionando |
| Renovação automática de token OAuth2 | ✅ Funcionando |
| Delivery confirmation (spinner removido) | ✅ Funcionando |
| Multi-tenant (N sessões WA × N portais Bitrix) | ✅ Funcionando |
| Partner App (Marketplace) — instalação e vinculação | ✅ Funcionando |
| Indicador de digitação ("digitando...") antes de mensagens outbound | ✅ Implementado |
| **Bitrix24 → WA** (operador responde no Contact Center) | ❌ **Não funcionando** |
| Testes automatizados | ❌ Não implementado |

### Próximo passo: Bitrix→WA

O endpoint `POST /bitrix/connector/event` existe e processa corretamente quando chamado manualmente (via curl). O problema é que o **Bitrix24 não chama esse endpoint** quando o operador responde.

Para avançar nessa investigação sem risco de quebrar o inbound, as opções são:

1. **Verificar via API do Bitrix** qual handler está registrado atualmente: `GET /rest/imconnector.list` ou inspecionar via Admin → Logs de REST
2. **Testar manualmente** o endpoint: `curl -X POST https://<dominio>/bitrix/connector/event -d 'data[CONNECTOR]=whatsapp_uc&data[LINE]=218&data[MESSAGES][0][message][text]=Oi&data[MESSAGES][0][chat][id]=127586399207476@lid&data[MESSAGES][0][message][user_id]=1558'`
3. **Consultar fórum Bitrix24** sobre como apps locais (não Marketplace) recebem `ONIMCONNECTORMESSAGEADD`
4. **Inspecionar um app Whatcrm** para ver o payload exato do `imconnector.connector.data.set` que eles usam
