# Integração WhatsApp

Duas vias: **não-oficial** via whatsmeow (WhatsApp Multi-Device) e **oficial** via
Cloud API da Meta.

## whatsmeow (não-oficial, Multi-Device)

Gerenciado pelo `Manager`
([internal/whatsapp/manager.go](../../internal/whatsapp/manager.go)). Cada sessão
tem um arquivo `.db` (store do whatsmeow) persistido em disco.

### ⚠️ Device suffix muda a cada re-pareamento

O JID do whatsmeow inclui um **device suffix** (`...:66@...`) que **muda a cada
re-pareamento** (`:66` → `:67`). Isso quebra qualquer casamento por igualdade
exata de JID.

**Regra:** ao casar sessões (por domínio, por número), normalize removendo o
suffix:
```sql
SPLIT_PART(SPLIT_PART(ws.jid,'@',1),':',1)
```
Ver bug #4 em [02-bugs-resolvidos.md](02-bugs-resolvidos.md). Existe
`RebindBitrixAccountJID` para religar a conta ao novo JID após re-pareamento.

### ⚠️ Distinguir logout real de falha transitória

O evento `events.LoggedOut` do whatsmeow pode disparar tanto por logout real
quanto por falha momentânea de conexão (`stream:error`). Apagar o `.db` numa falha
transitória força re-scan do QR.

**Regra:**
```go
realLogout := true // stream:error (OnConnect==false) => logout real
if evt.OnConnect { realLogout = evt.Reason.IsLoggedOut() }
if !realLogout { /* NÃO apaga .db; marca Disconnected; retorna */ }
```
Só apaga a sessão quando `Reason.IsLoggedOut()` é verdadeiro. Ver bug #1.

### ⚠️ Persistência de sessão exige VOLUME

Os arquivos `.db` **têm** que ficar num volume persistente. `WA_SESSIONS_DIR` deve
apontar para `/app/sessions` (volume no EasyPanel), **nunca** para `./sessions`
(efêmero, apaga a cada deploy). Default no código é `/app/sessions`
([config.go:125](../../internal/config/config.go#L125)). Há um warn defensivo se o
banco tem sessões QR mas o diretório está vazio.

### Reconnect / zombies

Se uma entrada existe no mapa mas `!IsConnected()` (zumbi), o `Reconnect` deve
`Disconnect` + remover do mapa antes de reconectar — senão retorna `nil` e a
sessão nunca volta.

### ⚠️ Três recusas em que reconectar só piora

O whatsmeow **não** reconecta sozinho nestes casos: quem insistia era o
watchdog, a cada 30s. Agora ele pausa
([conexao.go](../../internal/whatsapp/conexao.go)):

| Evento | O que é | Pausa |
|---|---|---|
| `StreamReplaced` | outro processo conectou com a mesma sessão | 10 min — reconectar toma a sessão de volta e os dois ficam se alternando |
| `ClientOutdated` (405) | whatsmeow velho para o protocolo | 30 min — **refazer o QR não resolve**; atualize a dependência e faça deploy |
| `TemporaryBan` | número restrito pelo WhatsApp | o prazo que o próprio evento informa |

### ⚠️ `SendMessage` voltar não é o cliente receber

O retorno só diz que o **servidor** do WhatsApp aceitou. O celular do cliente
confirma depois, por `events.Receipt` (entregue, lido, ouvido). A saída nasce
`sent` e avança com o recibo, nunca para trás
([confirmacao.go](../../cmd/server/confirmacao.go)).

### Eventos de sessão

`SessionConnectHandler` / `fireSessionConnect` / `ConnectedSessions()` /
`ResolveSessionInfo()` permitem reagir a conexões (ex: auto-refresh da UI quando
uma sessão conecta).

## Cloud API (oficial, Meta)

Implementada em [internal/whatsapp/cloud.go](../../internal/whatsapp/cloud.go).
Sessões cloud usam prefixo `cloud:` no JID (por isso o casamento por número exclui
`ws.jid LIKE 'cloud:%'`). Suporta **templates da Meta**
(`fetchMetaTemplates`, contagem de variáveis, escaping). É uma feature de plano
(gate por `Templates` nas features do tenant).

### ⚠️ A versão da Graph API vence

Cada versão sai de linha cerca de 2 anos depois da seguinte
([changelog](https://developers.facebook.com/docs/graph-api/changelog)).
Vencida, a Meta **não** recusa a chamada: redireciona para a versão mais antiga
ainda ativa. Funciona, mas com o comportamento de uma versão que ninguém
escolheu — e sem nenhum erro avisando.

Em 21/10/2026 o código estava em `v20.0` (fora de linha desde 24/09/2026) e a
listagem de templates fixa em `v18.0` (desde 26/01/2026). Hoje há **uma** fonte
(`VersaoGraphAPI`, padrão `v25.0`, vale até 29/07/2028), trocável por
`META_GRAPH_API_VERSION` sem deploy.

## Ritmo de envio (limite por número)

Todo envio pelo mesmo número passa por
[internal/whatsapp/ritmo.go](../../internal/whatsapp/ritmo.go): entre um envio e
o próximo passa ao menos o tempo de **escrever** a próxima mensagem
(`2s + 100ms por caractere`, teto de `12s`).

O controle é **por número, sem device suffix** — com o suffix, o mesmo aparelho
teria dois controles depois de cada re-pareamento. Vale para a fila do operador,
a aba do CRM e o robô de automação, pelo mesmo motivo de sempre: o recurso
escasso é o número, não o caminho que o usa.

**Cloud API fica de fora** — é oficial e a Meta controla a taxa.

Ver bugs #12 e #13 em [02-bugs-resolvidos.md](02-bugs-resolvidos.md).

## Features por licença

O acesso a templates/automação/relatórios é resolvido por
`resolveTenantFeatures(ctx, domain)`
([internal/api/license_features.go](../../internal/api/license_features.go)), que
lê os benefícios direto de `tenant_licenses` — sem catálogo intermediário para
dessincronizar. Ver [`docs/fluxos/05-licenca.md`](../fluxos/05-licenca.md).

## Mídia

Download de mídia (`DownloadMedia`), limpeza de arquivos órfãos de sessão
(`cleanupOrphanSessionFiles`), e limites de QR
([internal/whatsapp/qr_limits.go](../../internal/whatsapp/qr_limits.go)).

A cópia que a aba do CRM exibe é guardada por
[internal/media/store.go](../../internal/media/store.go) — ver
[09-midia.md](09-midia.md).
