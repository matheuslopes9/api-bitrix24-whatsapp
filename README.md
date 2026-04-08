# WhatsApp ↔ Bitrix24 Connector

Conector de alta performance entre WhatsApp e Bitrix24, capaz de gerenciar múltiplas sessões simultâneas.

## Stack

| Componente | Tecnologia |
|---|---|
| Linguagem | Go 1.25 |
| WhatsApp | whatsmeow v0.0.0-20260327181659-02ec817e7cf4 |
| HTTP | Fiber v2 |
| Banco de dados | PostgreSQL (pgxpool) |
| Cache / Filas | Redis |
| Persistência de sessões WA | SQLite (por número) |
| Métricas | Prometheus |
| Deploy | EasyPanel + Docker |

## Arquitetura

```
WhatsApp ──► Manager ──► Queue (Redis) ──► Worker Pool ──► Bitrix24 API
                │                                              │
                └──────────────────────────────────────────────┘
                              (resposta do agente)
```

- **Manager**: gerencia múltiplas sessões WhatsApp (`whatsmeow`) em goroutines independentes
- **Queue**: fila Redis separada para mensagens inbound (WA→Bitrix) e outbound (Bitrix→WA)
- **Worker Pool**: 20 workers paralelos com retry exponencial (até 5 tentativas)
- **Watchdog**: verifica saúde das sessões a cada 30s e reconecta automaticamente
- **Bitrix24**: integração via Local Application com OAuth2; mensagens entregues como atividades em Leads do CRM

## Fluxo de mensagem (WA → Bitrix24)

```
WA mensagem chega
      │
      ▼
Manager (whatsmeow event handler)
      │  salva no banco (status: received)
      ▼
Redis Queue (queue:inbound)
      │
      ▼
Worker Pool (20 goroutines)
      │  FindOrCreateLead por telefone
      │  AddLeadComment com texto da mensagem
      ▼
Bitrix24 CRM Lead (status: delivered)
```

## Endpoints

### UI (sem autenticação)

| Método | Rota | Descrição |
|---|---|---|
| `GET` | `/connect` | Página de conexão WhatsApp (QR scan) |
| `POST` | `/ui/sessions` | Inicia nova sessão |
| `GET` | `/ui/sessions/:phone/qr` | Polling do QR code |
| `GET` | `/ui/sessions` | Lista sessões ativas |
| `DELETE` | `/ui/sessions/:jid` | Desconecta sessão |

### WhatsApp (requer `X-API-Key`)

| Método | Rota | Descrição |
|---|---|---|
| `POST` | `/wa/sessions` | Inicia nova sessão |
| `GET` | `/wa/sessions` | Lista sessões ativas |
| `GET` | `/wa/sessions/:phone/qr` | Polling para obter QR code |
| `DELETE` | `/wa/sessions/:jid` | Remove sessão |
| `POST` | `/wa/send` | Envia mensagem de texto |

### Bitrix24

| Método | Rota | Descrição |
|---|---|---|
| `GET` | `/bitrix/oauth/start` | Info sobre autenticação (app local) |
| `GET/POST` | `/bitrix/callback` | Callback de instalação do app (ONAPPINSTALL) |
| `POST` | `/bitrix/webhook` | Recebe eventos do Bitrix24 (resposta do operador) |

### Sistema

| Método | Rota | Descrição |
|---|---|---|
| `GET` | `/health` | Health check |
| `GET` | `/metrics` | Métricas Prometheus |
| `GET` | `/stats/daily` | Estatísticas diárias (requer `X-API-Key`) |
| `GET` | `/stats/queues` | Status das filas (requer `X-API-Key`) |

## Autenticação da API

Rotas `/wa/*` e `/stats/*` exigem header:
```
X-API-Key: <APP_SECRET>
```

## Fluxo de Conexão WhatsApp (via UI)

1. Acessar `https://<dominio>/connect`
2. Digitar o número no formato `5519910001772`
3. Clicar em **Conectar** e aguardar o QR code aparecer
4. Escanear com o WhatsApp do celular (⋮ → Aparelhos conectados → Conectar um aparelho)
5. Sessão ativa aparece na lista "Dispositivos conectados"

## Fluxo de Autenticação Bitrix24 (app local)

1. No Bitrix24: **Aplicações → Criar app local**
2. Configurar Handler Path: `https://<dominio>/bitrix/callback`
3. Conceder escopos: `crm`, `imopenlines`, `imconnector`
4. Ao instalar, o Bitrix24 chama automaticamente `POST /bitrix/callback` com o token
5. Token salvo no banco — renovação automática via refresh_token

## Deploy no EasyPanel

### Pré-requisitos

- Projeto EasyPanel criado (ex: `integracao-mosca`)
- Serviço **PostgreSQL** criado no projeto
- Serviço **Redis** criado no projeto
- Repositório GitHub: `matheuslopes9/api-bitrix24-whatsapp`

### Passo a passo

1. Criar serviço **App** no EasyPanel apontando para o repo GitHub
2. Build automático via `Dockerfile`
3. Configurar variáveis de ambiente (ver seção abaixo)
4. Rodar migrations no terminal do PostgreSQL (EasyPanel):
   ```bash
   psql -U admin -d whatsapp-bitrix
   ```
   Colar o conteúdo de `migrations/001_init.sql`
5. Acessar `https://<dominio>/connect` para conectar o WhatsApp
6. Instalar o app local no Bitrix24 — token é salvo automaticamente via `/bitrix/callback`

### Variáveis de ambiente

> **Atenção:** Não use `#` em senhas/secrets — o EasyPanel interpreta como comentário e trunca o valor.

```env
APP_PORT=3000
APP_ENV=production
APP_SECRET=<string-forte-sem-hash>

POSTGRES_HOST=<nome-servico-easypanel>
POSTGRES_PORT=5432
POSTGRES_USER=admin
POSTGRES_PASSWORD=<senha-sem-hash>
POSTGRES_DB=whatsapp-bitrix
POSTGRES_SSLMODE=disable
POSTGRES_MAX_OPEN_CONNS=50
POSTGRES_MAX_IDLE_CONNS=10

REDIS_HOST=<nome-servico-easypanel>
REDIS_PORT=6379
REDIS_PASSWORD=<senha-sem-hash>
REDIS_DB=0

WA_SESSIONS_DIR=./sessions
WA_MEDIA_DIR=./media
WA_LOG_LEVEL=INFO

BITRIX_DOMAIN=https://suaempresa.bitrix24.com
BITRIX_CLIENT_ID=<client-id>
BITRIX_CLIENT_SECRET=<client-secret>
BITRIX_REDIRECT_URI=https://<dominio-easypanel>/bitrix/callback
BITRIX_OPEN_LINE_ID=218

QUEUE_WORKERS=20
QUEUE_MAX_RETRY=5
QUEUE_RETRY_BASE_DELAY_MS=1000

WATCHDOG_PING_INTERVAL_SECS=30
```

## Desenvolvimento local

### Requisitos
- Go 1.25+
- Docker + Docker Compose
- CGO habilitado (necessário para sqlite3)
- gcc / musl-dev instalados

### Subir infraestrutura local
```bash
docker compose up -d postgres redis
```

### Rodar o servidor
```bash
cp .env.example .env
# editar .env com suas credenciais
go run ./cmd/server
```

### Build Docker
```bash
docker build -t wa-bitrix-connector .
```

## Banco de dados

Tabelas criadas pela migration `migrations/001_init.sql`:

| Tabela | Descrição |
|---|---|
| `whatsapp_sessions` | Sessões WA ativas (JID, telefone, status) |
| `contact_mapping` | Mapeamento JID WhatsApp ↔ Lead ID Bitrix24 |
| `messages` | Log de mensagens trocadas |
| `bitrix_tokens` | Tokens OAuth2 do Bitrix24 |
| `event_log` | Log de eventos do sistema |

## Estrutura do projeto

```
.
├── cmd/server/          # Entrypoint
├── internal/
│   ├── api/             # HTTP handlers + rotas (Fiber)
│   │   ├── server.go    # Setup do app Fiber e rotas
│   │   ├── ui.go        # Handlers da UI /connect
│   │   └── handlers.go  # Handlers da API /wa/* e /bitrix/*
│   ├── bitrix/          # Cliente Bitrix24 + processador de mensagens
│   │   ├── client.go    # Chamadas REST ao Bitrix24 (OAuth2 + CRM + imconnector)
│   │   └── processor.go # Lógica WA→Bitrix (ensureContact + entrega)
│   ├── config/          # Configuração via viper/env
│   ├── db/              # Repositório PostgreSQL
│   ├── queue/           # Filas Redis + worker pool
│   ├── telemetry/       # Métricas Prometheus
│   ├── watchdog/        # Monitoramento de sessões
│   └── whatsapp/        # Manager de sessões whatsmeow
├── migrations/          # SQL migrations
├── Dockerfile
├── docker-compose.yml
└── .env.example
```

## Problemas conhecidos / armadilhas

| Problema | Causa | Solução |
|---|---|---|
| `get token: no rows` | Domain do callback vem sem `https://` | `SaveToken` sempre usa `cfg.Domain` |
| `sessions loaded count:0` | `ListActiveSessions` não retorna desconectadas | `LoadAll` usa `ListAllSessions` |
| PairSuccess sem log (deadlock) | `AddEventHandler` dentro de event handler | Usa `go client.AddEventHandler(...)` |
| `session file not found` | SQLite deletado no redeploy, registro permanece no DB | `connectSession` verifica se arquivo existe antes de abrir |
| Variáveis truncadas no EasyPanel | `#` no valor interpretado como comentário | Nunca usar `#` em senhas/secrets |

## Status atual

- [x] Deploy EasyPanel configurado e rodando
- [x] PostgreSQL e Redis conectados
- [x] Gerenciamento de sessões WhatsApp (async QR via goroutine)
- [x] UI `/connect` com QR scan, lista de dispositivos e botão desconectar
- [x] WhatsApp conectado via QR scan em produção
- [x] Fila Redis com worker pool (20 workers, retry exponencial)
- [x] Watchdog de reconexão automática (intervalo 30s)
- [x] Métricas Prometheus
- [x] Autenticação Bitrix24 via app local (token salvo automaticamente no install)
- [x] Fluxo WA → Bitrix24: mensagens chegam como atividade no Lead do CRM
- [ ] Fluxo WA → Bitrix24 via Contact Center (Open Lines / imconnector)
- [ ] Fluxo Bitrix24 → WA (resposta do operador)
- [ ] Testes end-to-end
