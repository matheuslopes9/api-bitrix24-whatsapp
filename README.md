# UC Talk — Conector WhatsApp ↔ Bitrix24

Conector multi-tenant entre WhatsApp e o Contact Center do Bitrix24, com
painel de suporte, controle de licença por cliente e alertas por e-mail.
Escrito em Go, deploy no EasyPanel.

> **Documentação por tema:** [`docs/fluxos/`](docs/fluxos) descreve cada fluxo
> ponta a ponta; [`docs/aprendizados/`](docs/aprendizados) guarda o porquê das
> decisões e os bugs que custaram caro. Este README é o mapa geral.

---

## O que o sistema faz

1. **Liga WhatsApp ao Bitrix24.** Mensagem do cliente chega no Contact Center;
   resposta do operador volta pro WhatsApp. Multi-tenant: N números × N portais.
2. **Dá ao suporte uma tela de estado.** Token, conexões, mensagens, fila e
   licença de cada cliente num lugar só — sem abrir log de container nem rodar
   SQL na mão.
3. **Avisa quando algo para de atender.** Token vencido, número caído e licença
   vencendo viram e-mail pro time.

### O que ele NÃO faz mais

Cobrança, planos, cupons, trial e gateway de pagamento **foram removidos**. O
app não vai ao marketplace do Bitrix: quem instala é a UC Technology, direto no
portal do cliente, e o cliente paga pelo comercial. O que ficou no lugar é uma
**licença por cliente**, registrada à mão, com os benefícios do contrato e o
histórico de pagamentos.

---

## Stack

| Componente | Tecnologia |
|---|---|
| Linguagem | Go 1.26 |
| WhatsApp | whatsmeow (multi-device, via QR) |
| WhatsApp oficial | Meta Cloud API (opcional, por licença) |
| HTTP | Fiber v2 |
| Banco | PostgreSQL (pgx/pgxpool) |
| Filas | Redis |
| Sessões WA | SQLite — um arquivo por número |
| E-mail | SMTP → proxy OAuth2 → Microsoft 365 |
| Logs | zap |
| Métricas | Prometheus |
| Deploy | EasyPanel + Docker |

---

## Arquitetura

```
WhatsApp ──► Manager ──► Redis queue:inbound  ──► Workers ──► Bitrix24 REST
                                                                    │
WhatsApp ◄── Manager ◄── Redis queue:outbound ◄── Workers ◄─────────┘
                                                   (webhook do operador)

Alertas ──► SMTP simples ──► uctalk_email (proxy OAuth2) ──► Microsoft 365
```

### Componentes

| Componente | Onde | Função |
|---|---|---|
| **Manager** | `internal/whatsapp/manager.go` | N sessões WhatsApp, cada uma em goroutine própria. |
| **Processor** | `internal/bitrix/processor.go` | Traduz mensagem WA em chamada ao Contact Center. |
| **Bitrix Client** | `internal/bitrix/client.go` | REST do Bitrix24 com OAuth2 e refresh serializado. |
| **Queue / Workers** | `internal/queue/` | `queue:inbound`, `queue:outbound`, `queue:dead`, com retry e reprocesso. |
| **Watchdog** | `internal/watchdog/` | Reconecta sessão que caiu. |
| **Alertas** | `internal/api/alertas.go` | Verifica token e conexões a cada 5min e avisa por e-mail. |
| **E-mail** | `internal/email/` | Envio e o template de alerta da UC Technology. |
| **Repository** | `internal/db/repository.go` | PostgreSQL. Migrations em `internal/db/db.go`. |

---

## Licença por cliente

Substituiu planos e cobrança. Cada portal tem uma linha em `tenant_licenses`
com o que o **contrato** dá — não um pacote fechado:

| Benefício | Efeito |
|---|---|
| Números WhatsApp | Quantas sessões o cliente pode parear. |
| Cloud API + Templates | Libera WhatsApp oficial e templates HSM. |
| Automações | Robôs BizProc. |
| Relatórios | Aba de relatórios no painel do cliente. |

Pagamento é registrado pelo suporte (`license_payments`) e **estende** a
vigência. O financeiro não tem login: é avisado por e-mail.

**Licença vencida avisa, não bloqueia.** Ninguém fica sem atendimento por
boleto atrasado — decisão de projeto, não esquecimento.

---

## Painel de suporte (`/admin`)

| Aba | Para quê |
|---|---|
| **Visão geral** | Números conectados vs ativos, mensagens por direção, falhas, filas, últimas mensagens de todos os clientes e alertas do que exige ação agora. |
| **Tenants** | Portais instalados, com licença, conexões e token. |
| **Consumo** | Mensagens por período contra o contratado, com vigência. |
| **Saúde do cliente** | Estado real de um cliente: Bitrix, sessões, mensagens, fila e licença. Ações: testar conexão, parear WhatsApp, reentregar fila, cadastrar credenciais do app, ver como o cliente vê. |
| **Licenças** | Benefícios contratados, vigência e pagamentos. |
| **Alertas** | Para onde enviar, o que avisar, de quanto em quanto tempo, e o histórico do que saiu. |
| **Sistema / Logs** | Processo em tempo real e stream de log. |
| **Minha conta** | Troca da própria senha. |
| **Usuários admin / IPs / Auditoria** | Quem acessa, bloqueio por IP, trilha de ações. |
| **Preview do app** | Como o cliente vê o UC Talk, por cliente ou em modo demonstração. |
| **Ferramentas** | Reparo pontual — cada ação diz quando usar. |

---

## Alertas por e-mail

O app **não fala com a Microsoft**: manda SMTP simples para o proxy
(`tools/oauth2-email-service`), que resolve o OAuth2 com o Azure AD. Nenhuma
credencial do Azure entra no processo do UC Talk — é a razão de existirem dois
serviços.

| Alerta | Quando | Janela padrão |
|---|---|---|
| Token do Bitrix vencido | O token não renova; nenhuma mensagem chega no Contact Center. | 6h |
| Número desconectado | O banco diz ativo, mas não há conexão viva. | 30min |
| Licença vencendo/vencida | Aviso pro financeiro. | diário |

Tudo configurável na aba **Alertas**, sem reiniciar: a configuração vive em
`config_alertas` e o job relê a cada ciclo. As variáveis de ambiente valem
apenas como carga inicial.

O e-mail usa o template da UC Technology (`internal/email/template.go`), o
mesmo layout do backend de ferramentas.

> Setup do serviço de e-mail: [`tools/oauth2-email-service/README.md`](tools/oauth2-email-service/README.md).

---

## Banco de dados

Migrations rodam **em todo boot**, na ordem do array em
[`internal/db/db.go`](internal/db/db.go), **sem ledger**. Por isso toda
migration precisa ser idempotente (`IF NOT EXISTS`, `ON CONFLICT DO NOTHING`).

> A pasta `migrations/*.sql` é **código morto** — ver [`migrations/README.md`](migrations/README.md).

| Tabela | Descrição |
|---|---|
| `whatsapp_sessions` | Sessões WA — JID, telefone, status, arquivo SQLite. |
| `bitrix_accounts` | Vínculo sessão ↔ portal: domínio, conector, linha aberta, credenciais OAuth. |
| `bitrix_tokens` | Tokens OAuth2 por domínio e `client_id`. |
| `bitrix_portals` | Portais instalados. |
| `messages` | Log de mensagens — direção, tipo, status, erro. |
| `contact_mapping` | Contato WA ↔ chat do Bitrix. |
| `lid_phone_map` | LID do WhatsApp ↔ telefone real. |
| `tenant_licenses` | Benefícios contratados e vigência. |
| `license_payments` | Pagamentos registrados pelo suporte. |
| `license_notifications` | Avisos de vencimento já enviados. |
| `config_alertas` | Configuração dos alertas (linha única). |
| `alertas_operacionais` | Alertas enviados — evita repetir o mesmo aviso. |
| `crm_user_permissions` | Quem pode enviar por qual número. |
| `message_templates` | Respostas prontas do operador. |
| `admin_users` · `admin_audit_log` · `blocked_ips` | Acesso ao painel e trilha. |

---

## Endpoints principais

### Público

| Rota | Descrição |
|---|---|
| `GET /health` | Status + profundidade das filas. |
| `GET /metrics` | Prometheus. |
| `GET /dashboard` | Painel do cliente (iframe Bitrix ou cookie válido). |
| `GET /assets/logo-email.png` | Logo dos alertas — buscada pelo cliente de e-mail. |

### Bitrix24

| Rota | Descrição |
|---|---|
| `GET/POST /bitrix/callback` | Instalação do app. |
| `POST /bitrix/connector/event` | Resposta do operador. |
| `POST /bitrix/auth` | Token do BX24.js. |
| `GET /bitrix/crm/tab` | Aba no contato, lead e negócio. |

### Admin (exige login)

`/admin/api/tenants` · `/metrics` · `/usage` · `/mensagens-recentes` ·
`/licenses` · `/license` · `/tenant/health` · `/tenant/credenciais` ·
`/tenant/reprocessar-fila` · `/alertas/config` · `/alertas/teste` ·
`/alertas/historico` · `/me/password` · `/audit`

---

## Deploy

Dois serviços no mesmo projeto do EasyPanel, para compartilharem a rede interna.

### 1. `connector` — o app

Build a partir da raiz do repositório, branch `main`.

```env
APP_PORT=3000
APP_ENV=production
APP_SECRET=<string-forte>
APP_BASE_URL=https://<dominio>/          # usado em webhooks e nos e-mails
ADMIN_USER=<usuario>
ADMIN_PASSWORD=<senha-forte>

POSTGRES_HOST=... POSTGRES_PORT=5432 POSTGRES_USER=... POSTGRES_PASSWORD=...
POSTGRES_DB=... POSTGRES_SSLMODE=disable
REDIS_HOST=... REDIS_PORT=6379 REDIS_PASSWORD=...

BITRIX_REDIRECT_URI=https://<dominio>/bitrix/callback
```

`BITRIX_CLIENT_ID` e `BITRIX_CLIENT_SECRET` são **opcionais**: o app é
instalado por cliente, e cada portal tem o seu. Cadastre em **Saúde do
cliente → Credenciais do app**. A env global só serve quando há um app único.

As de e-mail (`SMTP_HOST`, `EMAIL_SENDER`, `ALERT_RECIPIENTS`) também são
opcionais — valem como carga inicial da aba **Alertas**.

**Volumes** — os dois precisam ser persistentes:

| Mount path | Para quê |
|---|---|
| `/app/sessions` | Pareamento do WhatsApp. Sem volume, todo deploy pede QR de novo. |
| `/app/media` | Arquivos das conversas, exibidos na aba do CRM. Sem volume, somem a cada deploy. |

Arquivos: `MEDIA_MAX_MB` (padrão 64 — acima disso o arquivo vai só pro Bitrix)
e `MEDIA_RETENTION_DAYS` (padrão 90 — depois disso a mensagem fica, o arquivo não).

Segurança dos eventos do Bitrix:

| Variável | Padrão | Para quê |
|---|---|---|
| `CONNECTOR_EVENT_TOKEN` | `observar` | `exigir` recusa resposta de operador cujo `application_token` não confere. Em `observar` só registra no log — mude para `exigir` depois de ver o log limpo (o evento vem do app local, e o token gravado pode ser o do Partner App). |
| `BITRIX_DOMINIOS_REDE_INTERNA` | vazio | Portais on-premise que resolvem para IP interno. Sem isso a verificação de token recusa o endereço (proteção contra SSRF) e o portal fica sem login. Separados por vírgula. |

### 2. `uctalk_email` — o proxy de e-mail

Mesmo repositório, com **Build Path `tools/oauth2-email-service`**.

```env
AZURE_TENANT_ID=... AZURE_CLIENT_ID=... AZURE_CLIENT_SECRET=...
SENDER_EMAIL=<conta com licença de envio>
EMAIL_SENDER=<endereço que aparece no From>
PROXY_HOST=0.0.0.0                       # 127.0.0.1 o torna inalcançável
PROXY_PORT=2526
```

**Não publique domínio** nesse serviço: o proxy aceita e-mail sem autenticação
e só deve ser alcançado pela rede interna.

> **`#` em valor de env:** o EasyPanel trunca no `#`. Evite.

---

## Testes

```bash
go build ./... && go vet ./... && go test ./...
```

| Pacote | Cobre |
|---|---|
| `internal/bitrix` | Nome do contato, listagem de usuários, fila da linha. |
| `internal/db` | Normalização de permissões por número. |
| `internal/queue` | Direção do job na dead queue, limite de taxa, falha permanente. |
| `internal/whatsapp` | Variantes do 9º dígito. |
| `internal/email` | Limite de linha SMTP, multipart, template e categorias. |

### JavaScript dentro do Go

O painel e a aba do CRM têm milhares de linhas de JS dentro de string Go — o
`go build` **não** as enxerga, e um erro de sintaxe derruba a tela inteira sem
aviso. Antes de subir mudança de UI:

```bash
python - <<'PY'
import io,re
s=io.open('internal/api/admin_html.go',encoding='utf-8').read()
js='\n;\n'.join(re.findall(r'<script>(.*?)</script>', s, re.S))
io.open('/tmp/check.js','w',encoding='utf-8').write(js)
ch=set(re.findall(r'onclick="([A-Za-z_$][\w$]*)\(', s))
de=set(re.findall(r'function\s+([A-Za-z_$][\w$]*)\s*\(', js))
print('handlers sem definicao:', sorted(ch-de) or 'nenhum')
PY
node --check /tmp/check.js
```

Isso já pegou três erros que teriam ido a produção, incluindo uma função
apagada por engano junto de um bloco substituído.

### Smoke test

```bash
./scripts/smoke/smoke.sh      # ou smoke.ps1 no Windows
```

---

## Estrutura

```
.
├── cmd/server/main.go           # Entrypoint e wiring
├── internal/
│   ├── api/                     # Handlers Fiber
│   │   ├── server.go            # Rotas
│   │   ├── admin_html.go        # Painel de suporte (HTML + JS)
│   │   ├── tenant_health.go     # Saúde do cliente
│   │   ├── alertas.go           # Job de alertas
│   │   ├── alertas_admin.go     # Configuração dos alertas
│   │   ├── reprocesso.go        # Reprocesso automático da fila
│   │   ├── conta.go             # Senha do admin e teste de e-mail
│   │   ├── license_admin.go     # Licenças e pagamentos
│   │   ├── crm.go               # Aba do CRM
│   │   └── dashboard.go         # Painel do cliente
│   ├── email/                   # Envio + template de alerta
│   ├── bitrix/ · whatsapp/ · queue/ · db/ · watchdog/ · config/
├── docs/fluxos/                 # Cada fluxo ponta a ponta
├── docs/aprendizados/           # Decisões e bugs que custaram caro
├── tools/oauth2-email-service/  # Proxy SMTP OAuth2 (serviço separado)
└── Dockerfile
```

---

## Segurança

- **Segredos vêm do ambiente ou do banco** — nunca de commit, chat ou log. Isso
  inclui `client_secret` do Bitrix e do Azure, `APP_SECRET` e senhas de banco.
- O `client_secret` do Bitrix **aparece** na tela de credenciais, com
  mostrar/ocultar: o suporte precisa conferir, e regravar no escuro a cada
  dúvida derruba a renovação do token. Não vai para log nem para a tela de
  saúde, que mostra apenas se existe.
- **QR de pareamento é renderizado no próprio app** (`rsc.io/qr`). Já foi
  enviado a um serviço externo de imagem — era vazamento de segredo de
  pareamento.
- `tools/**/.env`, `tools/*.msg` e certificados estão no `.gitignore`.
- O painel admin é o único com `X-Frame-Options: DENY`; o resto roda em iframe.
- `/debug/*`, `/sim/*` e `/bitrix/webhook` exigem login do admin. Eram
  públicos: permitiam chamar qualquer método REST no Bitrix de qualquer
  cliente e enviar WhatsApp por qualquer número.
- Relatórios e o painel do cliente são **filtrados pelo portal**
  (`internal/db/escopo.go`). O escopo vazio não enxerga nada — relatório de
  todos os clientes só com `X-API-Key`.
- **Identidade do cliente é conferida no próprio Bitrix.** `/bitrix/auth`,
  `/bitrix/install` e `/bitrix/callback` chamam `/rest/profile` no domínio
  informado com o token recebido (`internal/bitrix/verificar.go`) antes de
  gravar token ou emitir cookie. O cookie de tenant e o de usuário
  (`uctalk_user`) só nascem daí.
- `/bitrix/crm/*` exige os dois cookies e ignora `domain`/`user_id` enviados
  pela tela (`crm_identidade.go`). As rotas `/ui/bitrix/*`, histórico,
  e permissões são presas ao portal do cookie (`escoparAoTenant`), e
  envio só sai por número do próprio portal.
- Arquivos das conversas (`/ui/media/:id`) só saem para o portal dono do
  número, e só abrem no navegador tipos seguros (imagem, áudio, vídeo, PDF).
  O resto vai como download: o arquivo vem do cliente final e um `.html`
  aberto na nossa origem rodaria script com o cookie do operador.

---

## Estado atual

| Área | Situação |
|---|---|
| WA → Bitrix (texto e mídia) | Funcionando |
| Bitrix → WA | Funcionando |
| Multi-tenant | Funcionando |
| Painel de suporte | Funcionando |
| Licenças e pagamentos | Funcionando |
| Alertas por e-mail | Funcionando |
| Permissões por número | Funcionando — lista pela estrutura da empresa |
| Testes automatizados | Parcial — ver tabela acima |
| WhatsApp oficial (Cloud API) | Implementado, pouco exercitado |

### Limites conhecidos

- **`user.get` não é concedido** ao app no portal do cliente. A listagem de
  usuários usa a estrutura da empresa (`mobile.intranet.departments.get` +
  `im.department.employees.get`), que é rápida e completa. Se o portal não
  tiver o quadro montado, cai numa sondagem por IDs, lenta e parcial.
- **O token do app age como quem instalou.** Registro fora do alcance desse
  usuário volta vazio do Bitrix. Na aba do CRM isso é contornado lendo pelo
  `BX24` do usuário logado; no backend, não.
- **Alerta de sessão não cobre Cloud API.** Ela é stateless por HTTPS e não
  vive no manager, então ausência ali não significa queda.
