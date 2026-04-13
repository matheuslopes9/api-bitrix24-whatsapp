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
- **Worker Pool**: 20 workers paralelos com retry exponencial (até 5 tentativas); mensagens para o mesmo JID são serializadas (mutex por JID)
- **Watchdog**: verifica saúde das sessões a cada 30s e reconecta automaticamente
- **Bitrix24**: integração via `imconnector` (Contact Center / Open Lines); mensagens chegam como conversa no operador

## Fluxo de mensagem (WA → Bitrix24)

```
WA mensagem chega (texto, imagem, doc, vídeo, áudio, vCard, sticker)
      │
      ▼
Manager (whatsmeow event handler)
      │  salva no banco (status: received)
      │  baixa mídia (com fallback DownloadAny + MediaDocument)
      ▼
Redis Queue (queue:inbound)
      │
      ▼
Worker Pool (20 goroutines)
      │  UploadToDisk → disk.storage.uploadfile (base64 JSON, nome único com timestamp)
      │  imconnector.send.messages com files[] ou texto
      │  imconnector.send.status.delivery (para parar o spinner)
      ▼
Bitrix24 Contact Center (conversa aberta para o operador)
```

## Fluxo de resposta (Bitrix24 → WA)

```
Operador responde no Contact Center
      │  ONIMCONNECTORMESSAGEADD webhook → POST /bitrix/connector/event
      ▼
Handler extrai texto/arquivo e monta OutboundJob
      │
      ▼
Redis Queue (queue:outbound)
      │
      ▼
Worker Pool (serializado por JID de destino)
      │  SendTyping → ChatPresenceComposing ("digitando...") por 1.5–4s
      │  waManager.Send (texto) ou waManager.SendDocument (arquivo) ou waManager.SendAudio (mp3)
      │  imconnector.send.status.delivery (confirma delivery outbound)
      ▼
WhatsApp (mensagem entregue ao cliente com indicador de digitação)
```

## Dashboard

O sistema inclui uma interface web acessível em `/dashboard` com as seguintes seções:

### Painel
- Visão geral de sessões ativas, mensagens nas filas e taxa de erro
- Cards de sessões WhatsApp conectadas
- Cards de Workers e Redis (status de filas em tempo real)
- Gráfico de mensagens dos últimos 7 dias

### Sessões WhatsApp
- Lista de dispositivos conectados com status em tempo real
- Botão de desconexão com confirmação via modal
- Atualização automática a cada 10s

### Integrações Bitrix24
- Cards com detalhes de cada conta Bitrix24 vinculada a uma sessão WA
- Botão **Editar** — abre modal com dados pré-preenchidos para alterar configurações
- Botão **Excluir** — confirmação via modal antes de remover
- Botão **Adicionar Integração** — modal para criar nova vinculação sessão WA ↔ Bitrix24
- Formulário com campos: sessão WA, domain, Client ID, Client Secret, Redirect URI, Open Line ID, Connector ID

### Relatórios
- Gráficos de volume diário (inbound vs outbound)
- Tabela de estatísticas dos últimos 7/30 dias

### Tema claro/escuro
- Alternância via ícone de sol/lua no menu lateral
- Persistido em `localStorage` — mantido entre sessões do navegador
- Todos os gráficos se adaptam automaticamente às cores do tema ativo

## Endpoints

### UI (sem autenticação)

| Método | Rota | Descrição |
|---|---|---|
| `GET` | `/connect` | Página de conexão WhatsApp (QR scan) |
| `GET` | `/dashboard` | Dashboard principal |
| `POST` | `/ui/sessions` | Inicia nova sessão |
| `GET` | `/ui/sessions/:phone/qr` | Polling do QR code |
| `GET` | `/ui/sessions` | Lista sessões ativas |
| `DELETE` | `/ui/sessions/remove?jid=` | Desconecta sessão (via query param) |
| `GET` | `/ui/overview` | Visão geral de sessões e filas |
| `POST` | `/ui/bitrix/accounts` | Cria/atualiza conta Bitrix24 |
| `GET` | `/ui/bitrix/accounts` | Lista contas Bitrix24 cadastradas |
| `DELETE` | `/ui/bitrix/accounts` | Remove conta Bitrix24 |

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
| `POST` | `/bitrix/connector/event` | Recebe ONIMCONNECTORMESSAGEADD (resposta do operador) |

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

## Multi-tenant (múltiplas contas Bitrix24)

O sistema suporta múltiplas sessões WhatsApp, cada uma vinculada a uma conta Bitrix24 diferente:

- Tabela `bitrix_accounts` mapeia `session_jid` → credenciais Bitrix24 (domain, client_id, client_secret, open_line_id, connector_id)
- O `bitrix.Client` é stateless — recebe `TenantCreds` por chamada
- Matching por prefixo de telefone via `SPLIT_PART(session_jid, ':', 1)`, ignorando o device suffix (`:18`, `:19`) que muda a cada reconexão
- Gerenciado pela UI no menu **Integrações Bitrix24**

## Tipos de mídia suportados (WA → Bitrix)

| Tipo | Comportamento |
|---|---|
| Texto | Enviado diretamente como mensagem |
| Imagem (jpg/png) | Upload para Bitrix Disk + attachment |
| Vídeo (mp4) | Upload para Bitrix Disk + attachment |
| Documento (pdf, xlsx, etc) | Upload para Bitrix Disk + attachment |
| Áudio gravado no WA (PTT) | Upload como voice.ogg + attachment |
| Áudio pré-gravado | Tenta download; fallback `[Áudio]` se HMAC inválido |
| Contato (vCard) | Upload como .vcf + attachment |
| Sticker | Upload como .webp + attachment |

> **Nota sobre áudio:** `invalid media hmac` ocorre quando a MediaKey do áudio não está disponível na sessão atual (áudios de outros dispositivos). É uma limitação criptográfica do WhatsApp — não tem solução.

## Tipos de mídia suportados (Bitrix → WA)

| Tipo MIME | Comportamento |
|---|---|
| `audio/mpeg` (mp3) | Enviado como áudio nativo WA (com botão play) |
| `audio/wav` | Enviado como documento (WA não suporta wav como áudio) |
| `audio/ogg` | Enviado como documento |
| `video/webm` | Enviado como documento |
| Imagens, vídeos, docs | Enviado como documento |

## Indicador de digitação (outbound)

Antes de cada mensagem enviada pelo operador ao cliente WA, o sistema:
1. Envia `ChatPresenceComposing` ("digitando...") ao destinatário
2. Aguarda 1.5–4s proporcional ao tamanho do texto (com jitter aleatório)
3. Envia `ChatPresencePaused`
4. Envia a mensagem

Mensagens para o mesmo número são serializadas (mutex por JID) — nunca chegam em paralelo.

## Renovação automática de token Bitrix24

O token OAuth2 expira a cada 1 hora. O sistema renova automaticamente:
- Toda chamada REST verifica se o token expira em menos de 60s
- Se sim, faz POST para `https://oauth.bitrix.info/oauth/token/` com o `refresh_token`
- O novo token é salvo no PostgreSQL sob o domain da config
- Nenhuma intervenção manual é necessária

## Fluxo de Conexão WhatsApp (via UI)

1. Acessar `https://<dominio>/connect`
2. Digitar o número no formato `5519910001772`
3. Clicar em **Conectar** e aguardar o QR code aparecer
4. Escanear com o WhatsApp do celular (⋮ → Aparelhos conectados → Conectar um aparelho)
5. Sessão ativa aparece na lista "Dispositivos conectados"

## Fluxo de Autenticação Bitrix24 (app local)

1. No Bitrix24: **Aplicações → Criar app local**
2. Configurar Handler Path: `https://<dominio>/bitrix/callback`
3. Conceder escopos: `crm`, `imopenlines`, `im`, `imconnector`, `disk`
4. Ao instalar, o Bitrix24 chama automaticamente `POST /bitrix/callback` com o token
5. Token salvo no banco — renovação automática via refresh_token (sem reinstalar nunca mais)
6. O app registra automaticamente: `imconnector.register` + `imconnector.activate` + `event.bind ONIMCONNECTORMESSAGEADD`

## Fluxo de Vinculação Bitrix24 ↔ WhatsApp (via Dashboard)

1. Acessar `/dashboard` → menu **Integrações Bitrix24**
2. Clicar em **Adicionar Integração**
3. Preencher: sessão WA, domain Bitrix24, Client ID, Client Secret, Redirect URI, Open Line ID, Connector ID
4. Clicar em **Salvar** — aparece card com as informações da integração
5. Para editar: clicar em **Editar** no card → modal com dados pré-preenchidos
6. Para remover: clicar em **Excluir** → confirmação via modal

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
6. Acessar `https://<dominio>/dashboard` → **Integrações Bitrix24** para vincular a conta
7. Instalar o app local no Bitrix24 — token e connector são configurados automaticamente via `/bitrix/callback`

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

QUEUE_WORKERS=20
QUEUE_MAX_RETRY=5
QUEUE_RETRY_BASE_DELAY_MS=1000

WATCHDOG_PING_INTERVAL_SECS=30
```

> **Nota:** As credenciais Bitrix24 (domain, client_id, client_secret, open_line_id, connector_id) são configuradas diretamente pela UI em `/dashboard` → **Integrações Bitrix24**, não por variáveis de ambiente.

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
| `contact_mapping` | Mapeamento JID WhatsApp ↔ chat ID Bitrix24 (normalizado sem device part) |
| `messages` | Log de mensagens trocadas |
| `bitrix_tokens` | Tokens OAuth2 do Bitrix24 com renovação automática |
| `bitrix_accounts` | Contas Bitrix24 vinculadas a cada sessão WA (multi-tenant) |
| `event_log` | Log de eventos do sistema |

## Estrutura do projeto

```
.
├── cmd/server/          # Entrypoint
├── internal/
│   ├── api/             # HTTP handlers + rotas (Fiber)
│   │   ├── server.go    # Setup do app Fiber e rotas
│   │   ├── dashboard.go # Dashboard UI (HTML/CSS/JS inline)
│   │   ├── assets.go    # Servir assets estáticos embarcados
│   │   ├── assets/      # chart.js, logo.png, logo_uc.png
│   │   ├── ui.go        # Handlers da UI /connect e /ui/*
│   │   └── handlers.go  # Handlers da API /wa/* e /bitrix/*
│   ├── bitrix/          # Cliente Bitrix24 + processador de mensagens
│   │   ├── client.go    # Chamadas REST ao Bitrix24 (OAuth2 + disk + imconnector)
│   │   └── processor.go # Lógica WA→Bitrix (ensureContact + upload + entrega)
│   ├── config/          # Configuração via viper/env
│   ├── db/              # Repositório PostgreSQL
│   ├── queue/           # Filas Redis + worker pool (mutex por JID)
│   ├── telemetry/       # Métricas Prometheus
│   ├── watchdog/        # Monitoramento de sessões
│   └── whatsapp/        # Manager de sessões whatsmeow + typing indicator
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
| `DISK_OBJ_22000` | Nome de arquivo duplicado no Bitrix Disk | `uniqueFileName` adiciona timestamp ao nome (ex: `voice_20260410_120000.ogg`) |
| Chat duplicado no Bitrix | JID com `:47@lid` vs `@lid` cria sessões diferentes | `normalizeChatID` remove device part antes de enviar ao connector |
| `invalid media hmac` no áudio | MediaKey indisponível para áudios de outros dispositivos | Fallback para `[Áudio]` — limitação criptográfica do WA |
| `ERROR_ARGUMENT` no disk upload | `fileContent` deve ser `[fileName, base64]` em JSON | `UploadToDisk` usa JSON com base64, não multipart |
| Spinner no Contact Center | `imconnector.send.status.delivery` não era chamado | Chamado após cada envio (inbound e outbound) |
| Token expirado a cada 1h | Refresh enviava para domain errado + salvava com domain errado | Endpoint fixo `oauth.bitrix.info`; sempre salva com `cfg.Domain` |
| Mensagens outbound em paralelo | 20 workers disparando para o mesmo JID simultaneamente | Mutex por JID no worker pool |
| wav/ogg como áudio WA | WA rejeita wav e ogg como AudioMessage | Apenas `audio/mpeg` vai como áudio nativo; demais como documento |
| Inbound não chega ao Bitrix após reconexão | JID device suffix muda a cada reconexão (`:18`→`:19`), causando falha no match exato | `SPLIT_PART(session_jid, ':', 1)` em todas as queries de `bitrix_accounts` |
| Botão Editar do card não abre modal | `JSON.stringify` dentro de `onclick=""` gera aspas duplas que quebram o atributo HTML | `data-acct='...'` + `JSON.parse(this.dataset.acct)` no handler JS |
| `@` no JID truncado no path routing | Fiber trata `@` como separador em parâmetros de rota | Desconexão usa query param `?jid=encodeURIComponent(jid)` em vez de path param |
| Tema claro: texto invisível nos cards | Estilos inline com cores hardcoded não são sobrescritos por classes CSS | Seletores CSS `[style*="color:#e2e8f0"]` com `!important` |
| Tema claro: linhas do gráfico invisíveis | Cor de grid hardcoded como `rgba(255,255,255,.04)` (branco sobre branco) | Funções `chartGridColor()`, `chartTickColor()`, `chartLegendColor()` com valores por tema |

## Status atual

- [x] Deploy EasyPanel configurado e rodando
- [x] PostgreSQL e Redis conectados
- [x] Gerenciamento de sessões WhatsApp (async QR via goroutine)
- [x] UI `/connect` com QR scan, lista de dispositivos e botão desconectar
- [x] Dashboard completo em `/dashboard` com Painel, Sessões, Integrações e Relatórios
- [x] WhatsApp conectado via QR scan em produção
- [x] Fila Redis com worker pool (20 workers, retry exponencial)
- [x] Watchdog de reconexão automática (intervalo 30s)
- [x] Métricas Prometheus
- [x] Autenticação Bitrix24 via app local (token salvo automaticamente no install)
- [x] Renovação automática de token OAuth2 (refresh sem reinstalar)
- [x] Fluxo WA → Bitrix24 via Contact Center (imconnector) — texto, imagem, vídeo, doc, vCard, sticker
- [x] Fluxo Bitrix24 → WA — texto, documentos e áudio mp3 nativo
- [x] Spinner do Contact Center eliminado (delivery confirmation inbound e outbound)
- [x] Upload de mídia para Bitrix Disk (base64 JSON, nome único, Shared Drive)
- [x] Normalização de JID para evitar chat duplicado no Bitrix
- [x] Suporte a vCard (contato) e sticker
- [x] Indicador de digitação ("digitando...") no WA antes de cada mensagem outbound
- [x] Serialização por JID — mensagens para o mesmo número nunca chegam em paralelo
- [x] Multi-tenant — múltiplas sessões WA com contas Bitrix24 independentes
- [x] Gerenciamento de integrações Bitrix24 via UI (criar/editar/excluir com cards e modais)
- [x] Matching de JID por prefixo de telefone (imune a mudança de device suffix na reconexão)
- [x] Tema claro/escuro com persistência em localStorage
- [x] Assets estáticos embarcados no binário (logo, favicon, chart.js)
- [ ] Testes end-to-end automatizados
