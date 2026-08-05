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

## Features por plano

O acesso a templates/automação/SMS/relatórios é resolvido por
`resolveTenantFeatures(ctx, domain)`
([internal/api/plan_features.go](../../internal/api/plan_features.go)) a partir da
definição do plano — inclusive no trial (o Trial é um plano separado, code
`trial`, com features próprias configuráveis). Fallback legado quando não há
definição no banco.

## Mídia

Download de mídia (`DownloadMedia`), limpeza de arquivos órfãos de sessão
(`cleanupOrphanSessionFiles`), e limites de QR
([internal/whatsapp/qr_limits.go](../../internal/whatsapp/qr_limits.go)).
