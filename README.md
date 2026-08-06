# UC Talk — Conector WhatsApp ↔ Bitrix24

Conector multi-tenant entre WhatsApp e o Contact Center do Bitrix24, com
painel administrativo, cobrança recorrente (PIX + Boleto via Itaú), planos
configuráveis, cupons e auditoria. Escrito em Go, deploy no EasyPanel.

> **Documentação profunda:** os aprendizados detalhados (arquitetura,
> integrações, pendências, PIX Itaú) ficam em [`docs/aprendizados/`](docs/aprendizados).
> Este README é o mapa geral.

---

## Stack

| Componente | Tecnologia |
|---|---|
| Linguagem | Go 1.25 |
| WhatsApp | whatsmeow |
| HTTP | Fiber v2 |
| Banco de dados | PostgreSQL (pgx/pgxpool) |
| Cache / Filas | Redis |
| Persistência de sessões WA | SQLite (um arquivo por número) |
| Pagamentos | **Itaú** — Recebimentos PIX (mTLS) + Boleto Cash Management V2 |
| Logs | zap |
| Métricas | Prometheus |
| Deploy | EasyPanel + Docker |

---

## O que o sistema faz

1. **Conecta WhatsApp ao Bitrix24** — mensagens do cliente no WhatsApp chegam
   no Contact Center; respostas do operador voltam pro WhatsApp. Multi-tenant:
   N números WA × N portais Bitrix.
2. **Cobra pelo uso** — cada portal (tenant) tem um plano. Novos clientes
   entram em **Trial**; depois assinam **Básico** ou **Pro** pagando por
   **PIX** ou **Boleto** direto no Itaú. Pagamento confirmado libera o plano
   automaticamente (webhook).
3. **Administra tudo num painel** (`/admin`) — tenants, planos, cupons,
   gateway de pagamento, auditoria e ferramentas de diagnóstico.

---

## Arquitetura geral

```
WhatsApp ──► Manager ──► Redis Queue ──► Worker Pool ──► Bitrix24 REST API
               │                                              │
               │◄──────────────────────────────────────────────┘
                     (resposta do operador via webhook Bitrix)

Cliente paga PIX/Boleto ──► Itaú ──► webhook /billing/itau ──► libera plano
```

### Componentes principais

| Componente | Arquivo | Função |
|---|---|---|
| **Manager** | `internal/whatsapp/manager.go` | N sessões WhatsApp em goroutines independentes. |
| **Processor** | `internal/bitrix/processor.go` | Converte mensagem WA em chamada REST ao Bitrix24. |
| **Bitrix Client** | `internal/bitrix/client.go` | REST Bitrix24 com OAuth2 + refresh automático. |
| **Queue / Workers** | `internal/queue/` | Filas Redis `queue:inbound`/`queue:outbound` com retry. |
| **Watchdog** | `internal/watchdog/watchdog.go` | Reconecta sessões automaticamente. |
| **API** | `internal/api/` | Handlers Fiber: dashboard, admin, webhooks, billing. |
| **Itaú** | `internal/itau/` | Cliente PIX (`itau.go`) + Boleto (`boleto.go`) + webhook (`webhook.go`). |
| **Repository** | `internal/db/repository.go` | Acesso ao PostgreSQL. Migrations em `internal/db/db.go`. |

---

## Billing (Itaú)

Todo o pagamento é **direto no Itaú** (MaxiPago foi aposentado).

| Recurso | Como funciona |
|---|---|
| **PIX** | Recebimentos PIX (`pix-pj.api.itau.com`). mTLS + OAuth `client_credentials` (credenciais no *body*, não Basic). O CN do certificado == Client ID. |
| **Boleto** | Cash Management V2 (`api.itau.com.br/cash_management/v2`). Carteira 109, "nosso número" crescente/único (contador atômico no banco). |
| **Webhook** | O Itaú chama `/billing/itau` (aceita também `/billing/itau/pix`) quando um PIX é pago. Reconcilia por `txid`, marca a cobrança paga (idempotente) e ativa o plano. |
| **Certificado** | Montado no volume em `/app/certs/itau.crt` + `itau.key`. **Nunca** versionado (está no `.gitignore`). O boot NÃO testa o cert — use o botão *"Testar PIX (R$1)"* no painel. |

> Guia completo de setup Itaú: [`docs/aprendizados/07-integracao-pix-itau.md`](docs/aprendizados/07-integracao-pix-itau.md).

### Planos

Três planos, definidos em `plan_definitions` (editáveis na aba **Planos**):

| Plano | Preço | Papel |
|---|---|---|
| **Trial** | grátis | Concedido automaticamente no install (`is_trial_default`). Não aparece nos cards de compra. |
| **Básico** | R$ 99/mês | 1 sessão, sem features avançadas. |
| **Pro** | R$ 199/mês | Múltiplas sessões + templates, automações, SMS, relatórios. |

As migrations garantem a ordem Trial → Básico → Pro (migration `042`).

---

## Painel Admin (`/admin`)

Protegido por `ADMIN_USER`/`ADMIN_PASSWORD` (cookie assinado com `APP_SECRET`,
HMAC-SHA256). Todas as rotas `/admin/api/*` retornam **401** sem login.

| Aba | O que faz |
|---|---|
| **Visão geral** | KPIs (tenants, em trial, ativos, expirados, receita, sessões) + últimos pagamentos. |
| **Tenants** | Lista de portais com plano, status, conexões, mensagens 24h, token. Ações: ativar Pro/Básico, +7d trial, reativar, suspender, abrir em Ferramentas. Busca + filtros por status/plano. |
| **Planos** | Construtor de planos: preço, features, sessões, trial, formas de pagamento. |
| **Cupons** | Desconto %, valor fixo ou dias extras de trial. |
| **Gateway** | Status (somente leitura) do Itaú + botões de teste real (PIX R$1 / Boleto validação). |
| **Auditoria** | Histórico de mudanças: login, planos, cupons, tenants (ativar/suspender/reativar/pagamento), IP block. |
| **Ferramentas** | Diagnósticos por tenant (placements, master, SMS, BizProc, portal). |

---

## Endpoints principais

### Público / UI (sem auth de admin)

| Método | Rota | Descrição |
|---|---|---|
| `GET` | `/health` | Health check (status + filas). |
| `GET` | `/metrics` | Métricas Prometheus. |
| `GET` | `/dashboard` | Dashboard do tenant (gated: só iframe Bitrix / cookie válido). |
| `GET` | `/welcome`, `/planos`, `/connect` | Telas públicas / onboarding. |
| `POST` | `/billing/itau`, `/billing/itau/pix` | Webhook de PIX pago (Itaú). |

### Bitrix24

| Método | Rota | Descrição |
|---|---|---|
| `GET/POST` | `/bitrix/callback` | Instalação do app local (ONAPPINSTALL). |
| `POST` | `/bitrix/connector/event` | Resposta do operador (ONIMCONNECTORMESSAGEADD). |
| `POST` | `/bitrix/auth` | Token BX24.js do iframe. |
| `GET` | `/bitrix-connect` | Application URL do Partner App. |

### Admin (requer login)

`GET /admin` · `/admin/api/tenants` · `/admin/api/metrics` ·
`/admin/api/plan-defs` · `/admin/api/coupons` · `/admin/api/itau-status` ·
`POST /admin/api/itau-test` · `/admin/api/tenant/plan/*` · `/admin/api/audit`

---

## Banco de dados

Migrations rodam **em todo boot**, na ordem do array em
[`internal/db/db.go`](internal/db/db.go), **sem ledger** — por isso toda
migration é **idempotente** (`ADD COLUMN IF NOT EXISTS`, `ON CONFLICT DO
NOTHING`, `UPDATE` direto).

| Tabela | Descrição |
|---|---|
| `whatsapp_sessions` | Sessões WA — JID, telefone, status, path SQLite. |
| `bitrix_accounts` | Vínculo sessão WA ↔ conta Bitrix — domain, connector, open line. |
| `bitrix_tokens` | Tokens OAuth2 por domain. |
| `bitrix_portals` | Portais instalados (Partner App) — member_id, tokens, `installed_at`. |
| `messages` | Log de mensagens — direção, tipo, status. |
| `tenant_plans` | Plano de cada portal — plan, status, active_until, trial_ends_at. |
| `plan_definitions` | Catálogo de planos (preço, features, trial, pagamento). |
| `billing_charges` | Cobranças geradas — método, valor, txid/nosso-número, pago. |
| `coupons` | Cupons de desconto. |
| `boleto_numeracao` | Contador atômico do "nosso número" (carteira 109). |
| `audit_log` | Trilha de auditoria. |
| `blocked_ips` | Bloqueio de IPs. |

---

## Deploy no EasyPanel

O EasyPanel faz deploy a partir da branch `main` do GitHub
(`matheuslopes9/api-bitrix24-whatsapp`).

> **Importante:** não use `#` em valores de env — o EasyPanel trunca no `#`.

### Variáveis obrigatórias (resumo)

```env
APP_PORT=3000
APP_ENV=production
APP_SECRET=<string-forte>
APP_BASE_URL=https://<dominio>          # OBRIGATÓRIO p/ webhooks Bitrix
ADMIN_USER=<usuario-admin>
ADMIN_PASSWORD=<senha-forte>

POSTGRES_HOST=... POSTGRES_PORT=5432 POSTGRES_USER=... POSTGRES_PASSWORD=...
POSTGRES_DB=... POSTGRES_SSLMODE=disable
REDIS_HOST=... REDIS_PORT=6379 REDIS_PASSWORD=...

# Itaú (billing). O SECRET vem do .env do servidor — nunca colar em chat/git.
ITAU_CLIENT_ID=...
ITAU_CLIENT_SECRET=...
ITAU_CHAVE_PIX=...
ITAU_ENV=producao                       # ou sandbox
ITAU_CERT_PATH=/app/certs/itau.crt      # montar no volume
ITAU_KEY_PATH=/app/certs/itau.key
ITAU_AGENCIA=... ITAU_CONTA=... ITAU_CONTA_DAC=... ITAU_CARTEIRA=109
```

### Setup

1. Serviço App no EasyPanel apontando pro repo, branch `main`.
2. Configurar as envs (incluindo `APP_BASE_URL` e as `ITAU_*`).
3. Subir o certificado Itaú (`itau.crt` + `itau.key`) no volume `/app/certs`.
4. Deploy. As migrations rodam sozinhas no boot.
5. No Itaú: cadastrar o webhook `https://<dominio>/billing/itau` (**sem** `/pix`).
6. No painel `/admin` → **Gateway** → *Testar PIX (R$1)* pra validar o cert.

---

## Testes

### Smoke test (valida um deploy no ar)

Roda **da sua máquina** contra a URL pública — não precisa de login admin.
Ver [`scripts/smoke/README.md`](scripts/smoke/README.md).

```powershell
./scripts/smoke/smoke.ps1          # Windows
```
```bash
./scripts/smoke/smoke.sh           # Linux/macOS/CI
```

Cobre: health, rotas públicas, proteção do admin (401/302) e robustez do
webhook Itaú. Sai com código 1 se algo falhar.

### Stress test (carga do webhook Bitrix)

Ver [`scripts/stress_test/README.md`](scripts/stress_test/README.md).

---

## Estrutura do projeto

```
.
├── cmd/server/main.go              # Entrypoint — wiring dos componentes
├── internal/
│   ├── api/                        # Handlers Fiber
│   │   ├── server.go               # Rotas
│   │   ├── admin.go / admin_html.go / admin_platform.go  # Painel admin
│   │   ├── billing.go / billing_itau.go                  # Checkout + webhook
│   │   ├── gateway_itau_admin.go   # Status + teste do gateway Itaú
│   │   ├── plan_admin.go           # Planos + ações de tenant
│   │   ├── dashboard.go            # Dashboard do tenant
│   │   └── partner.go              # Partner App (Marketplace)
│   ├── itau/                       # PIX + Boleto Itaú (mTLS)
│   ├── bitrix/                     # REST Bitrix24
│   ├── db/                         # Pool, models, repository, migrations
│   ├── queue/                      # Redis + worker pool
│   ├── whatsapp/                   # Sessões whatsmeow
│   ├── watchdog/ · telemetry/ · config/
├── docs/aprendizados/              # Documentação profunda por tema
├── scripts/smoke/                  # Smoke test (ps1 + sh)
├── scripts/stress_test/            # Stress test do webhook
└── Dockerfile · docker-compose.yml
```

---

## Segurança — regras do projeto

- **Certificados** (`*.crt`, `*.key`, `*.pfx`) e a pasta `certs/` **nunca** vão
  pro git. Já estão no `.gitignore`.
- **Secrets** (`ITAU_CLIENT_SECRET`, `APP_SECRET`, senhas de banco) vêm das
  envs do servidor — nunca colados em chat, commit ou log.
- O painel admin é o único com headers restritivos (`X-Frame-Options: DENY`);
  o resto roda em iframe Bitrix.

---

## Status

| Área | Status |
|---|---|
| WA → Bitrix24 (texto + mídias) | ✅ Funcionando |
| Bitrix24 → WA (resposta do operador) | ✅ Funcionando |
| Multi-tenant (N sessões × N portais) | ✅ Funcionando |
| Painel admin (tenants, planos, cupons, auditoria) | ✅ Funcionando |
| Billing PIX + Boleto (Itaú) | ✅ Implementado — validar pagamento real em homolog |
| Smoke test | ✅ 18/18 no homolog |
| Testes automatizados (unit) | ❌ Não implementado |
