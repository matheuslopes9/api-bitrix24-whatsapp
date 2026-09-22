# Fluxo 4 — As telas do painel

O que cada tela lê, e onde ela mostrava coisa diferente da realidade.

## Mapa rápido

| Tela | Endpoint que popula | Fonte |
|---|---|---|
| Painel (admin) | `/admin/api/tenants` | `AllDomainSessionCounts` + `AllDomainMessageCounts` |
| Sessões WhatsApp | `/ui/sessions` | `ListSessionsByDomain` |
| Permissões | `/ui/history/sessions` + `/ui/permissions/all-users` | `ListSessionsByDomain` + usuários do Bitrix |
| Histórico | `/ui/history/{sessions,conversations,messages}` | `ListHistoryConversations`, `GetMessagesByPhoneForSession` |
| Templates / Relatórios | gate por benefício contratado | `resolveTenantFeatures(ctx, domain)` |
| Licenças (admin) | `/admin/api/licenses` | `ListLicenses` |
| Saúde do cliente (admin) | `/admin/api/tenant/health` | agrega portal, token, sessões, mensagens e licença |

## ⚠ As telas discordavam entre si

Este era o sintoma mais visível: **o mesmo número aparecia de formas
diferentes em cada tela.**

```
 Painel admin        →  "1 QR"
 Sessões WhatsApp    →  +558196807479  (2x, jids :1 e :2)
 Permissões          →  +5581996807479  e  +81996807479
 Histórico           →  +5581996807479
```

Três leituras diferentes do mesmo número. A causa:

1. **O banco realmente tinha 2 linhas** pro mesmo número — `UpsertSession` com
   `ON CONFLICT (jid)` inseria linha nova a cada troca de device suffix (ver
   [03-sessoes.md](03-sessoes.md)).

2. **O painel admin deduplicava, as outras telas não.**
   `AllDomainSessionCounts` normaliza o JID com
   `REGEXP_REPLACE(jid, ':[0-9]+@', '@')` e faz `DISTINCT ON` — por isso
   contava "1 QR" corretamente. `ListSessionsByDomain` devolvia as duas linhas
   cruas, então Sessões e Permissões mostravam duas.

3. **A tela de Permissões mostra `phone`, as outras mostram o JID.**
   O `phone` guardava a string **digitada no formulário** de pareamento,
   nunca conferida contra o JID real. Um número digitado como
   `5581996807479` (com o 9 extra) e outro como `81996807479` (sem o código
   do país) produziram os dois rótulos errados — sendo que o número de
   verdade, o do JID, é `558196807479`.

Corrigido em três frentes: o `phone` passou a ser derivado do JID, a
migration `044` limpou as linhas órfãs e corrigiu os phones divergentes, e um
índice único por número base impede a reintrodução.

## Tela: Histórico de Conversas

```
 seleciona a sessão no dropdown
     GET /ui/history/sessions?domain=…
     → ListSessionsByDomain (ativas E desconectadas)
       inclui sessões antigas de propósito: numa migração QR → Cloud,
       as conversas da sessão QR antiga continuam no banco
          │
          ▼
 lista de conversas (peers)
     GET /ui/history/conversations?session_jid=…
     → ListHistoryConversations — JÁ escopado por sessão:
       FK session_id, com fallback por JID literal tolerante a suffix
          │
          ▼
 abre uma conversa
     GET /ui/history/messages?session_jid=…&phone=…
     → GetMessagesByPhoneForSession
```

### ⚠ Dois defeitos corrigidos aqui

**Erro `column "retry_count" does not exist`.** A conversa não abria. A tabela
`messages` nasceu sem `retry_count`, `error_msg`, `sent_at` e `delivered_at`,
porque o `CREATE TABLE` do array Go é uma cópia reduzida do
`migrations/001_init.sql`, que não é executado. A lista de conversas
funcionava (o `InsertMessage` só usa colunas que existem), mas abrir qualquer
uma estourava `SQLSTATE 42703`. Corrigido pela migration `043`.

**Vazamento entre sessões e tenants.** O handler recebia `session_jid` mas
chamava `GetMessagesByPhone(phone)`, que casa por peer em **toda** a tabela
`messages` — sem filtro de sessão nem de tenant. Se dois tenants já falaram
com o mesmo número, a conversa misturava mensagens dos dois. Agora usa
`GetMessagesByPhoneForSession`.

**Ordem invertida.** A query ordena `DESC`, o scan já inverte pra `ASC`, e o
handler invertia **de novo** — o chat renderizava a mensagem mais nova no
topo.

## Tela: Saúde do cliente (admin)

Nasceu do jeito errado de diagnosticar. Durante toda esta semana, descobrir
por que o app de um cliente estava quebrado exigiu abrir o log do container e
rodar SQL na mão — foi assim que apareceram o token do Bitrix zerado, a sessão
duplicada brigando e o ciclo de reconexão de 30s.

Os dados já existiam, espalhados por 11 rotas de diagnóstico.
`GET /admin/api/tenant/health?domain=` junta tudo num request, em quatro
blocos, cada um marcando o problema em vermelho quando encontra:

| Bloco | O que revela |
|---|---|
| Conexão Bitrix | token ausente/corrompido, Linha Aberta não vinculada, conector sem sessão |
| Sessões WhatsApp | cruza o status do **banco** com o que está **vivo em memória** — a divergência costuma ser o próprio problema |
| Mensagens | volume 24h e falhas em 7 dias (só existem desde a migration 043) |
| Licença | benefícios, vigência e pagamentos registrados |

O cruzamento banco × memória é o ponto: `whatsapp_sessions.status` atrasa em
relação à realidade, e "banco diz ativa mas não há conexão viva" é exatamente
o sintoma que o cliente reporta como "o número caiu".

## Tela: Permissões por Número

Modelo de autorização em duas camadas:

```
 acesso à aba CRM     →  todo colaborador interno ATIVO do portal
                         (externos, bots e desativados são bloqueados)

 envio por um número  →  só se o número estiver em allowed_sessions
                         do usuário — é o que esta tela controla
```

Quem altera é apenas o **usuário master** do portal. Sem nenhum número
liberado, o operador vê o histórico mas não envia.

O chip de cada número é montado como `'+' + (s.phone || s.jid) + tipo` — por
isso o `phone` errado aparecia aqui e em nenhuma outra tela.

## Tela: Sessões WhatsApp

Lista as sessões QR (whatsmeow) e Cloud API (Meta) do tenant. O botão
"Atualizar status" refaz a leitura; o status vem de `whatsapp_sessions.status`,
que o watchdog e os event handlers do whatsmeow mantêm.

**Cuidado ao ler esta tela:** o status no banco pode estar defasado em relação
à conexão real em memória. Logo após um deploy é comum ver `Desconectada`
enquanto a sessão já voltou. A fonte confiável do que está realmente vivo é
`Manager.ConnectedSessions()`, que lê o estado em memória — é ela que alimenta
o dropdown do robô BizProc justamente por isso.

## Tela: Painel (admin de tenants)

Um card por portal Bitrix. Agrega numa query só:

- **Conexões** — `AllDomainSessionCounts`, filtrando `status = 'active'`
- **Msgs 24h** — inbound/outbound por domínio
- **Token** — `valid` / `expiring` / `expired`, calculado sobre o
  `expires_at` do `bitrix_tokens` **mais 30 dias** do refresh token. O access
  token vive ~1h e é renovado automaticamente; só é `expired` de verdade se
  passou de 30 dias sem renovar, sinal de que o refresh também morreu e o app
  precisa ser reinstalado.
- **Plano** — Trial / Básico / Pro, com dias restantes do trial

Portais com `domain == member_id` são escondidos: são placeholders criados
pelo install do Marketplace antes de o domínio real chegar pelo
`BX24.getAuth` no iframe.
