# Arquitetura & Decisões de Design

## O que o UC Talk é

Conector que liga o **WhatsApp** (oficial via Cloud API da Meta, e não-oficial
via whatsmeow/Multi-Device) ao **Bitrix24** (CRM + Open Channels/Linhas Abertas).
Escrito em **Go**, servido por **Fiber**, com **PostgreSQL** (pgx/pgxpool),
**Redis** (filas) e **zap** (logs). Deploy em **EasyPanel** (Docker).

Modelo comercial: **instalação local pela UC Technology**, uma licença por
cliente. Não há trial, planos, cupons nem cobrança online — o cliente paga pelo
comercial, e o contrato vira benefícios em `tenant_licenses`. O modelo antigo
(SaaS de marketplace com trial e gateway) foi removido; o histórico dessa
mudança está em [`docs/fluxos/05-licenca.md`](../fluxos/05-licenca.md).

## Componentes centrais (god nodes do grafo)

O grafo de conhecimento revelou os nós mais conectados — as abstrações que
seguram o sistema:

| Componente | Papel | Arestas no grafo |
|---|---|---|
| `Repository` | Toda a persistência (Postgres) | 147 |
| `Client` (bitrix) | Cliente REST do Bitrix24 | 76 |
| `TenantCreds` | Credenciais por tenant (multi-tenant) | 59 |
| `handlers` | Handlers HTTP (Fiber) | 47 |
| `Manager` (whatsapp) | Gerencia sessões whatsmeow | 46 |

### Pacotes que nasceram depois da medição

| Pacote / arquivo | Papel |
|---|---|
| [internal/media](../../internal/media/store.go) | cópia em disco dos arquivos da conversa — [09-midia.md](09-midia.md) |
| [internal/bitrix/verificar.go](../../internal/bitrix/verificar.go) | prova de identidade no portal + defesa de SSRF |
| [internal/db/escopo.go](../../internal/db/escopo.go) | escopo de relatório por número |
| [internal/whatsapp/ritmo.go](../../internal/whatsapp/ritmo.go) | ritmo de envio por número |
| [internal/db/pareamentos.go](../../internal/db/pareamentos.go) | dono do número em pareamento |
| [internal/bitrix/nome_arquivo.go](../../internal/bitrix/nome_arquivo.go) | nome de arquivo que sobrevive ao Open Lines |

## Decisão: `Repository` é um God Object (dívida técnica conhecida)

**O que é:** o `Repository` conecta praticamente todos os domínios do sistema
(sessões, cloud, licenças, OAuth, permissões, auditoria, templates, alertas...).
Betweenness centrality **0.104** na medição original — de longe a maior ponte
do sistema. A remoção do módulo de cobrança tirou algumas comunidades, mas a
natureza de God Object continua.

**Por que ficou assim:** velocidade. Centralizar todo acesso a dados num único
tipo foi mais rápido do que criar repositórios por domínio. Funcionou bem
enquanto o projeto era pequeno.

**Risco:** conforme cresce, esse arquivo vira gargalo de merge, fica difícil de
testar isoladamente, e qualquer mudança de schema toca tudo.

**Quando refatorar:** se/quando o time crescer ou o arquivo passar de ~alguns
milhares de linhas. Fatiar por domínio: `SessionRepo`, `LicenseRepo`,
`TenantRepo`, `BitrixRepo`. **Não é urgente** — é uma decisão consciente, não um
acidente.

## Decisão: migrations rodam a CADA boot, sem ledger

**O que é:** as migrations vivem num array declarativo em
[internal/db/db.go](../../internal/db/db.go) e **todas rodam a cada inicialização**,
na ordem do array. Não há tabela de controle (`schema_migrations`) marcando o que
já rodou.

**Consequências que viram REGRA:**
1. **Toda migration deve ser idempotente** — `IF NOT EXISTS`, `ON CONFLICT DO
   NOTHING`, `DROP ... IF EXISTS` antes de recriar.
2. **A ordem no array é a ordem de execução** — uma migration não pode depender de
   tabela que só é criada mais abaixo no array.
3. **Toda migration que cria constraint deve primeiro limpar o estado que a
   violaria** — porque o banco pode estar em qualquer estado intermediário de
   versões anteriores já deployadas. (Foi exatamente isso que quebrou a 038 — ver
   [02-bugs-resolvidos.md](02-bugs-resolvidos.md).)

**Por que assim:** simplicidade de deploy no EasyPanel — sobe o container, o
schema se garante sozinho. O custo é a disciplina de idempotência.

## Decisão: multi-tenant por domínio Bitrix

Cada portal Bitrix (ex: `crm.uctechnology.com.br`) é um tenant. A identidade do
tenant é o **domínio**, propagado em cookie HMAC-assinado — e o token que emite
esse cookie é conferido no próprio portal antes de valer. Sessões WhatsApp,
licença, relatórios e permissões são todos escopados por domínio.

**Detalhe sensível:** o casamento de sessões WhatsApp por domínio tolera o
**device suffix** do whatsmeow (`:66` → `:67`), que muda a cada re-pareamento —
ver [05-integracao-whatsapp.md](05-integracao-whatsapp.md).

## Decisão: o valor zero de um escopo não enxerga nada

`EscopoNumeros` ([internal/db/escopo.go](../../internal/db/escopo.go)) decide de
quais números um relatório pode contar mensagens. A escolha que vale registrar
não é o filtro em si, e sim o **default**:

> Escopo não preenchido → resultado **vazio**. Nunca o banco inteiro.

Antes, esquecer o filtro devolvia tudo — e foi assim que um portal sem número
próprio passou a exibir o número e os 65 contatos de outro cliente. Com o valor
zero fechado, o mesmo esquecimento produz uma tela vazia: um bug visível e
inofensivo, em vez de um vazamento silencioso. Visão global exige pedir
(`Todos=true`), e só o `/stats` autenticado por `X-API-Key` pede.

Vale como regra geral do projeto: **quando o default de uma falha é mostrar
demais, inverta o default.**

## Decisão: limite de taxa pertence ao recurso, não ao caminho

Havia dois controles de ritmo de envio — um na fila, por destinatário, e outro
no robô de automação. Eles não se somavam: somavam as **taxas** no mesmo
aparelho de WhatsApp. Hoje existe um só
([internal/whatsapp/ritmo.go](../../internal/whatsapp/ritmo.go)), por **número**,
compartilhado por todos os caminhos de envio.

A espera devolve o job ao fim da fila em vez de dormir dentro do worker — senão
uma rajada num número ocupa os 20 workers e trava o envio de todos os outros.

## Decisão: filas Redis para inbound/outbound

Mensagens entram e saem por filas Redis processadas por worker pools. Isola picos
de tráfego e permite drenagem graciosa no shutdown (até 30s). O `main()` é o
ponto único de orquestração — inicializa config, Postgres, Redis, Bitrix client,
WhatsApp manager e watchdog, nessa ordem.

## Ambientes

| Ambiente | Projeto EasyPanel | Domínio |
|---|---|---|
| Produção | `integracao-mosca` | `integracao-mosca-whatsapp-connector.omva7z.easypanel.host` |
| Homologação | (novo) | `uctalk-homolog-connector.omva7z.easypanel.host` |

Os dois rodam no mesmo host EasyPanel (`omva7z`), portanto compartilham o mesmo
**IP de saída** (`187.110.174.122`) — relevante para allowlist de serviço externo.

## Segurança (mecanismos usados)

> O **modelo** de identidade e isolamento entre clientes está em
> [08-isolamento-e-identidade.md](08-isolamento-e-identidade.md). Abaixo só os
> mecanismos.

- **bcrypt** para senhas de admin
- **HMAC-SHA256** para os três cookies de sessão (tenant, usuário e admin),
  assinados com `APP_SECRET` — vazio, o app não sobe
- **`subtle.ConstantTimeCompare`** para comparação de segredos (evita timing attack)
- **IP real atrás do proxy** EasyPanel/Traefik via header, com fallback pro RemoteIP
- **Defesa de SSRF** ao verificar um portal informado por quem chama: recusa IP
  literal e `localhost`, e também IP interno **já resolvido** (anti DNS rebinding)
- Página de **IPs bloqueados** no admin (liberar/manter bloqueio)
- Headers de segurança (`X-Frame-Options: DENY`) **só no `/admin`** — o resto roda
  em iframe do Bitrix e headers restritivos quebrariam o embed.
