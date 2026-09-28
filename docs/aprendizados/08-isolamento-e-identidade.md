# Isolamento entre clientes & identidade do tenant

Como o UC Talk sabe **quem está pedindo** e **o que essa pessoa pode ver**.

Este documento nasceu da auditoria de 25/09, feita no homolog com um portal
real. O resultado curto: o sistema tinha isolamento entre clientes, mas ele
dependia de um cookie que qualquer pessoa conseguia emitir. Tudo abaixo foi
corrigido — está aqui para que a decisão sobreviva ao código.

---

## O princípio: a prova de identidade é o próprio portal

Antes, `POST /bitrix/auth` recebia `{domain, access_token}`, gravava o token por
cima do que existia e emitia o cookie de tenant. **Nada conferia o token.** O
comentário no código dizia "o token em si é a prova" — não era. Mandar
`{domain: <vítima>, access_token: "x"}` devolvia o cookie da vítima (que abre
todo o `/ui/*`) e ainda derrubava a integração dela, porque o token real era
substituído por lixo. `/bitrix/install` e `/bitrix/callback` tinham o mesmo
buraco, e o install ainda trocava o `application_token` — o que autentica
`bp/send` e o cookie da aba do CRM.

Hoje, `bitrix.VerificarToken`
([internal/bitrix/verificar.go](../../internal/bitrix/verificar.go)) chama
`/rest/profile` **no domínio informado, com o token informado**. Só o portal
verdadeiro aceita um token dele. A resposta devolve `IdentidadeBitrix`:
domínio, `user_id`, se é admin, e o **nome de cadastro** — que passou a ser o
que assina as mensagens do operador.

### O risco que isso cria: SSRF

Aceitar um domínio de quem chama e fazer uma requisição para ele é, por
construção, um SSRF. Três camadas seguram isso:

| Camada | O que recusa |
|---|---|
| `DominioSeguro` | IP literal, `localhost`, nomes sem ponto |
| Dialer | IP interno **já resolvido** (anti DNS rebinding) |
| `BITRIX_DOMINIOS_REDE_INTERNA` | allowlist explícita, para portal on-premise |

A segunda camada é a que importa: validar o nome não basta, porque o DNS pode
responder uma coisa na validação e outra na conexão.

---

## Os dois cookies

| Cookie | Emitido por | Contém | Protege |
|---|---|---|---|
| tenant | `/bitrix/auth`, após verificação | domínio do portal | todo `/ui/*` |
| `uctalk_user` | idem | `user_id` e nome do Bitrix | `/bitrix/crm/*` |

Ambos são HMAC-assinados com `APP_SECRET`. O cookie de tenant **mudou de
assinatura** na correção: os emitidos antes — possivelmente forjados — deixaram
de valer, e quem estava logado refaz o handshake sozinho ao abrir o app.

A consequência prática mais importante: **`/bitrix/crm/*` deixou de confiar no
que a tela manda**. Antes recebiam `?domain=` e `user_id` do chamador; hoje os
dois vêm do cookie e **sobrescrevem** o que veio no corpo. A aba do CRM faz o
handshake antes de qualquer outra chamada.

---

## `escoparAoTenant` — o middleware que prende a requisição ao portal

Aplicado a `/ui/bitrix/*` (contas, filas, vínculos, ativação), histórico e
permissões. A regra é simples e vale para os dois eixos:

- `domain`/`portal` de outro portal → **403**
- `session_jid`/`jid` de outro portal → **403**

Fecha, entre outras coisas: a lista de contas que era a de **todos** os
clientes; `bp/send` disparando por número alheio; e `partner/link`, que casava
o número por **prefixo** — `phone="5"` pegava a primeira sessão que existisse.

---

## `EscopoNumeros` — relatório nenhum é global por acidente

`/ui/overview`, `/ui/stats/*` e o export liam o banco inteiro. O portal
`crm.uctechnology.com.br`, que não tinha número próprio, exibia como
"conectado" o número de outro cliente e listava 65 contatos dele, com nome e
telefone.

[`EscopoNumeros`](../../internal/db/escopo.go) resolve isso com uma escolha de
design que vale registrar:

> **O valor zero não enxerga nada.**

Um handler que esquecer de preencher o escopo mostra tela vazia — nunca o banco
inteiro. Visão global precisa ser pedida explicitamente (`Todos=true`), e só o
`/stats` autenticado por `X-API-Key` pede.

O filtro é pelo **nosso** lado da conversa: quem enviou na saída, quem recebeu
na entrada (`sqlNossoJID`). O número do cliente final não escopa nada — ele
pode falar com vários portais.

---

## Prova de origem no evento do conector

`POST /bitrix/connector/event` é como a resposta do operador chega até nós. Na
bateria de testes de 25/09, **um POST anônimo saiu de verdade pelo WhatsApp** —
só com conector, chat e texto, sem nenhuma autenticação.

Duas falhas somadas em `eventoPodeUsarSessao`:

1. sem `auth[domain]`, o modo permissivo deixava passar;
2. **com** `auth[domain]` preenchido a checagem de dono passava — e o domínio de
   um cliente não é segredo. O `application_token` divergente só ia para o log.

Hoje a prova é obrigatória (`CONNECTOR_EVENT_TOKEN=exigir`, o padrão) e aceita
dois caminhos:

- `application_token` igual ao gravado; **ou**
- o `access_token` do próprio evento, aceito pelo portal em `/rest/profile`.

O segundo caminho existe por um motivo concreto: o evento vem do **app local**,
e o token gravado pode ser o do **Partner App**. Era exatamente por isso que o
padrão tinha ficado em `observar` — exigir sem esse segundo caminho cortaria
todo o atendimento. Tokens conferidos ficam 30min em cache.

`observar` continua existindo como válvula de emergência, e está documentado
como inseguro.

---

## Rotas que não tinham autenticação nenhuma

Achado no mesmo teste, sem login, de fora:

| Rota | O que dava para fazer |
|---|---|
| `/debug/bitrix-call` | executar **qualquer** método REST no Bitrix de qualquer cliente |
| `/debug/rebind-event` | redirecionar a resposta do operador para uma URL própria |
| `/sim/history` | ler a dead queue com o conteúdo das mensagens e o histórico de qualquer telefone — confirmado com `curl` |
| `/bitrix/webhook` | enviar texto livre pelo WhatsApp de qualquer cliente (legado, sem nenhum evento apontando para ele) |

Todas exigem login de admin agora.

**Lição:** rota de diagnóstico é rota de produção. Ela nasce "temporária", fica,
e é a que tem os poderes mais amplos — justamente porque foi feita para
investigar.

---

## `APP_SECRET`

Assina os três cookies. Vazio, os cookies eram assinados com chave vazia e
`/wa/*` e `/stats/*` ficavam abertos.

- **vazio** → o app **não sobe** (`log.Fatal`);
- **curto** (menos de 16 caracteres) → sobe com aviso.

A distinção é deliberada: travar o boot por tamanho derrubaria um ambiente que
hoje funciona.

---

## Onde isso vive no código

| Arquivo | Papel |
|---|---|
| [internal/bitrix/verificar.go](../../internal/bitrix/verificar.go) | `VerificarToken`, `DominioSeguro`, dialer anti-rebinding |
| [internal/api/tenant_isolation.go](../../internal/api/tenant_isolation.go) | `numeroBase`, `escoparAoTenant`, `DonoDoNumero`, prova do evento |
| [internal/api/crm_identidade.go](../../internal/api/crm_identidade.go) | cookie de usuário, nome do operador |
| [internal/db/escopo.go](../../internal/db/escopo.go) | `EscopoNumeros`, `sqlNumeroBase`, `sqlNossoJID` |
| [internal/db/pareamentos.go](../../internal/db/pareamentos.go) | dono do número em pareamento (migration `052`) |
