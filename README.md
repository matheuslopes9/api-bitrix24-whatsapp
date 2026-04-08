# WhatsApp ↔ Bitrix24 Connector

Conector de alta performance entre WhatsApp e Bitrix24 Open Lines, capaz de gerenciar até **1.000 conversas simultâneas**.

## Stack

| Componente | Tecnologia |
|---|---|
| Linguagem | Go 1.23 |
| WhatsApp | whatsmeow (protocolo nativo) |
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
- **Bitrix24**: integração via Local Application com OAuth2 + Open Lines

## Endpoints

### WhatsApp

| Método | Rota | Auth | Descrição |
|---|---|---|---|
| `POST` | `/wa/sessions` | Bearer | Inicia nova sessão (retorna imediatamente) |
| `GET` | `/wa/sessions` | Bearer | Lista sessões ativas |
| `GET` | `/wa/sessions/:phone/qr` | Bearer | Polling para obter QR code |
| `DELETE` | `/wa/sessions/:jid` | Bearer | Remove sessão |
| `POST` | `/wa/send` | Bearer | Envia mensagem de texto |

### Bitrix24

| Método | Rota | Descrição |
|---|---|---|
| `GET` | `/bitrix/oauth/start` | Inicia fluxo OAuth2 |
| `GET/POST` | `/bitrix/callback` | Callback de instalação do app |
| `POST` | `/bitrix/webhook` | Recebe eventos do Bitrix24 |

### Sistema

| Método | Rota | Descrição |
|---|---|---|
| `GET` | `/health` | Health check |
| `GET` | `/metrics` | Métricas Prometheus |

## Autenticação da API

Todas as rotas `/wa/*` exigem header:
```
Authorization: Bearer <APP_SECRET>
```

## Fluxo de Conexão WhatsApp

1. `POST /wa/sessions` com `{"phone": "5519910001772"}` — retorna `{"status": "connecting", "qr_url": "..."}`
2. Fazer polling em `GET /wa/sessions/5519910001772/qr` a cada 2s
3. Quando `status == "ready"`, exibir o `qr` (string Base64 ou texto) para escanear
4. Após escaneamento, a sessão fica ativa e o QR some

## Deploy no EasyPanel

### Pré-requisitos

- Projeto EasyPanel criado (ex: `integracao-mosca`)
- Serviço **PostgreSQL** criado no projeto
- Serviço **Redis** criado no projeto
- Repositório GitHub: `matheuslopes9/api-bitrix24-whatsapp`

### Passo a passo

1. Criar serviço **App** no EasyPanel apontando para o repo GitHub
2. Build command: automático via `Dockerfile`
3. Configurar variáveis de ambiente (ver seção abaixo)
4. Rodar migrations manualmente no PostgreSQL:
   ```sql
   -- conectar via EasyPanel terminal no serviço PostgreSQL
   psql -U admin -d whatsapp-bitrix -f /path/to/migrations/001_init.sql
   ```
5. Acessar `https://<dominio-easypanel>/bitrix/oauth/start` para autenticar no Bitrix24

### Variáveis de ambiente

```env
APP_PORT=3000
APP_ENV=production
APP_SECRET=<string-forte>

POSTGRES_HOST=<nome-servico-easypanel>
POSTGRES_PORT=5432
POSTGRES_USER=admin
POSTGRES_PASSWORD=<senha>
POSTGRES_DB=whatsapp-bitrix
POSTGRES_SSLMODE=disable
POSTGRES_MAX_OPEN_CONNS=50
POSTGRES_MAX_IDLE_CONNS=10

REDIS_HOST=<nome-servico-easypanel>
REDIS_PORT=6379
REDIS_PASSWORD=<senha>
REDIS_DB=0

WA_SESSIONS_DIR=./sessions
WA_MEDIA_DIR=./media
WA_LOG_LEVEL=INFO

BITRIX_DOMAIN=https://suaempresa.bitrix24.com
BITRIX_CLIENT_ID=<client-id>
BITRIX_CLIENT_SECRET=<client-secret>
BITRIX_REDIRECT_URI=https://<dominio-easypanel>/bitrix/callback

QUEUE_WORKERS=20
QUEUE_MAX_RETRY=5
QUEUE_RETRY_BASE_DELAY_MS=1000

WATCHDOG_PING_INTERVAL_SECS=30
```

## Desenvolvimento local

### Requisitos
- Go 1.23+
- Docker + Docker Compose
- CGO habilitado (necessário para sqlite3)

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
| `contact_mapping` | Mapeamento JID WhatsApp ↔ Contact ID Bitrix |
| `messages` | Log de mensagens trocadas |
| `bitrix_tokens` | Tokens OAuth2 do Bitrix24 |
| `event_log` | Log de eventos do sistema |

## Estrutura do projeto

```
.
├── cmd/server/          # Entrypoint
├── internal/
│   ├── api/             # HTTP handlers + rotas (Fiber)
│   ├── bitrix/          # Cliente Bitrix24 + processador de eventos
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

## Status atual

- [x] Autenticação Bitrix24 (ONAPPINSTALL recebido com sucesso)
- [x] Gerenciamento de sessões WhatsApp (async QR)
- [x] Fila Redis com worker pool
- [x] Watchdog de reconexão
- [x] Métricas Prometheus
- [x] Deploy EasyPanel configurado
- [ ] Escaneamento QR testado em produção
- [ ] Open Lines ID configurado
- [ ] Testes end-to-end WA → Bitrix e Bitrix → WA
