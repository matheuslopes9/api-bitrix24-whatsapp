# Fluxo 3 — Ciclo de vida da sessão WhatsApp

Como uma sessão QR nasce, se mantém viva, e como esse ciclo produziu o loop de
reconexão de 30 segundos visto em produção.

## Pareamento (sessão nova)

```
 usuário digita o número na tela "Sessões WhatsApp"
          │
          ▼
 AddSession(phone)                                     manager.go:335
          │   dbPath = <WA_SESSIONS_DIR>/<phone>.db      ⚠ nome vem do INPUT
          ▼
 initSession(phone, dbPath)  — goroutine
          │
          ├─ sqlstore.New(sqlite, dbPath)
          ├─ container.GetFirstDevice()
          └─ client.Store.ID == nil?
                 │
                 ├── sim → connectWithQR
                 │        events.QR          → guarda o código, a UI faz poll
                 │        events.PairSuccess → JID real do device chega AQUI
                 │                             UpsertSession(jid, phone)
                 │        events.Connected   → status = active
                 │
                 └── não → client.Connect() e segue como sessão existente
```

**O JID real só existe depois do `PairSuccess`.** Antes disso o sistema só tem
a string que o usuário digitou — e era ela que ia parar em
`whatsapp_sessions.phone` sem nunca ser conferida.

## Boot do processo

```
 main() → waManager.LoadAll(ctx)                        manager.go:295
          │
          ├─ conta arquivos .db em WA_SESSIONS_DIR
          │  (warn se o banco tem sessões QR e o diretório está vazio →
          │   volume não persistente no EasyPanel)
          │
          └─ para cada linha de whatsapp_sessions:
                 connectSession(&s)
```

## Manutenção: o watchdog

```
 a cada WATCHDOG_PING_INTERVAL (default 30s)            watchdog.go:54
          │
          └─ para cada linha de ListAllSessions():
                 │
                 ├─ tipo cloud_api? → conta como viva e pula
                 │  (Cloud é stateless via HTTPS, não tem WebSocket)
                 │
                 ├─ Ping(s.JID) verdadeiro? → viva, pula
                 │
                 └─ falso → Reconnect(&s)
                             │
                             ├─ já conectada de verdade → no-op
                             ├─ entrada zumbi → close() + remove do mapa
                             └─ connectSession()
```

## ⚠ O loop de 30 segundos

Sintoma no log de produção, repetindo indefinidamente:

```
09:57:09 [WARN] session disconnected
09:57:39 [WARN] session not responding, attempting reconnect
09:57:39 [WARN] reconnect: entrada zumbi no mapa — limpando pra reconectar
09:57:39 [INFO] watchdog reconnected session
09:58:10 [WARN] session disconnected          ← 31s depois, de novo
```

### A cadeia causal

Tudo sai de **uma** confusão: tratar o JID como identidade estável da sessão.
Ele não é — o device suffix (`:1`, `:2`) muda a cada re-pareamento.

```
1. usuário re-pareia o número
       │
       ▼
2. UpsertSession usa ON CONFLICT (jid).
   Suffix novo ⇒ o conflito não dispara ⇒ INSERT de linha NOVA.
   A linha antiga fica órfã no banco pra sempre.
       │
       ▼
3. agora o banco tem 2 linhas do mesmo número: ":1" e ":2"
   → a tela de Sessões lista o número 2x
   → a tela de Permissões oferece 2 chips do mesmo número
       │
       ▼
4. o watchdog varre as DUAS. O mapa em memória está indexado pelo JID
   REAL do device (":2"), mas ele consulta pela linha do banco (":1"):

       Ping(":1") → map miss → false, SEMPRE
       │
       ▼
5. "session not responding" → Reconnect(":1") → connectSession abre um
   SEGUNDO client whatsmeow sobre o MESMO device
       │
       ▼
6. o WhatsApp não aceita duas conexões no mesmo device: derruba uma
   (stream:conflict) → "session disconnected"
       │
       ▼
7. 30s depois o watchdog roda de novo. Volta ao passo 4. Para sempre.
```

Agravante: `connectSession` abria um `sqlstore` novo a cada chamada e **nunca
fechava o anterior**. O client velho continuava vivo, com os event handlers
ainda registrados, e o handle do SQLite vazado.

### O que foi corrigido

| Ponto | Antes | Depois |
|---|---|---|
| `UpsertSession` | `ON CONFLICT (jid)` cria linha nova | remove as outras linhas do mesmo número base antes de inserir |
| `Ping` | igualdade exata de JID | resolve por número base (`resolveSession`) |
| `Reconnect` | busca no mapa por JID exato | idem, por número base |
| `connectSession` | abre client sempre | recusa 2º client se o número já tem sessão viva |
| `Session` | sem teardown | `close()`: Disconnect + RemoveEventHandlers + container.Close |
| `whatsapp_sessions.phone` | string digitada no formulário | derivado do JID do device |
| banco | sem restrição | índice único por número base (migration 044) |

## Logout: real ou transitório

`events.LoggedOut` dispara tanto por logout de verdade quanto por falha
momentânea de conexão. Apagar o `.db` no caso transitório força o cliente a
escanear o QR de novo sem necessidade.

```go
realLogout := true // stream:error (OnConnect==false) ⇒ logout real
if evt.OnConnect { realLogout = evt.Reason.IsLoggedOut() }
if !realLogout { /* NÃO apaga .db; marca Disconnected; retorna */ }
```

## Persistência exige volume

Os `.db` do whatsmeow **têm** que ficar em volume persistente.
`WA_SESSIONS_DIR` deve apontar pra `/app/sessions` (volume no EasyPanel),
nunca pra `./sessions` — que é efêmero e some a cada deploy, derrubando todas
as sessões pareadas.

## Queries de diagnóstico

```sql
-- Existe número duplicado? Deve voltar VAZIO.
SELECT SPLIT_PART(SPLIT_PART(jid,'@',1),':',1) AS numero,
       COUNT(*), ARRAY_AGG(jid), ARRAY_AGG(phone)
  FROM whatsapp_sessions
 WHERE jid NOT LIKE 'cloud:%'
 GROUP BY 1 HAVING COUNT(*) > 1;

-- O campo phone bate com o JID? As duas colunas devem ser iguais.
SELECT jid, phone, SPLIT_PART(SPLIT_PART(jid,'@',1),':',1) AS numero_real
  FROM whatsapp_sessions WHERE jid NOT LIKE 'cloud:%';
```
