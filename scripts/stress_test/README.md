# Stress test — webhook do Bitrix24

Dispara N POSTs em paralelo no `/bitrix/connector/event` simulando
operadores enviando mensagens ao mesmo tempo. Mede latência (p50/p95/p99),
taxa de sucesso e throughput.

## Pré-requisitos

1. Servidor rodando localmente (`go run ./cmd/server` ou via Docker).
2. Pelo menos um `bitrix_account` cadastrado e um connector ativo na linha
   alvo — senão o handler vai rejeitar com "no session found".

## Como rodar (cenários)

### 50 conversas, 1 msg cada (carga padrão)

```bash
go run ./scripts/stress_test \
  -url http://localhost:3000/bitrix/connector/event \
  -connector wa_cloud_1160607470462388 \
  -line 220 \
  -concurrent 50
```

### Ramp: 200 conversas, 5 msgs cada (carga maior)

```bash
go run ./scripts/stress_test \
  -concurrent 200 \
  -msgs-per-conv 5
```

### Validar throughput sustentado: 50 conversas, 20 msgs

```bash
go run ./scripts/stress_test \
  -concurrent 50 \
  -msgs-per-conv 20
```

## Flags

| Flag | Default | Descrição |
|---|---|---|
| `-url` | `http://localhost:3000/bitrix/connector/event` | Endpoint a testar |
| `-connector` | `wa_cloud_1160607470462388` | Connector ID que existe no banco |
| `-line` | `220` | Open Line ID |
| `-concurrent` | `50` | Conversas paralelas |
| `-msgs-per-conv` | `1` | Msgs sequenciais por conversa |
| `-timeout` | `30` | Timeout HTTP por request (s) |
| `-im-chat-base` | `9000` | Base para im.chat_id (cada conv += offset) |

## Interpretação

- **Sucesso < 95%**: workers não aguentam. Suba `cfg.Queue.Workers`.
- **p99 > 1s no localhost**: gargalo no banco, Redis ou Bitrix Client.
- **Erros de rede**: provavelmente fila Redis cheia ou Fiber rejeitando.
- **Throughput baixo (< 50 req/s)**: handler está síncrono em algo que
  poderia ser assíncrono.

## Aviso

Cada request enfileira um `OutboundJob` real. Se houver sessão Cloud/QR
conectada de verdade, o worker vai tentar enviar via Meta/whatsmeow para
os números fictícios (5519900XXXX). Esses envios vão falhar e poluir logs.
**Recomendado rodar em ambiente local sem sessão WA conectada**, focando
apenas no caminho até o `PushOutbound`.
