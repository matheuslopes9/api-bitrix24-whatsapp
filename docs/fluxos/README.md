# Fluxos do UC Talk

Cada arquivo desta pasta desenha **um fluxo de ponta a ponta**: por onde o dado
passa, qual função roda em cada etapa, e onde o fluxo já quebrou.

Diferente de `docs/aprendizados/`, que guarda *decisões de design* e *bugs já
resolvidos*, aqui o foco é **o caminho que o dado percorre**. Se você precisa
descobrir "onde a mensagem se perde", comece por aqui.

| Fluxo | Arquivo | Resumo |
|---|---|---|
| Mensagem do cliente → Bitrix | [01-inbound.md](01-inbound.md) | WhatsApp → fila → Open Channel |
| Operador → cliente | [02-outbound.md](02-outbound.md) | Bitrix → fila → WhatsApp |
| Ciclo de vida da sessão | [03-sessoes.md](03-sessoes.md) | QR, pareamento, reconexão, watchdog |
| Telas do painel | [04-telas.md](04-telas.md) | O que cada tela lê e onde ela mente |
| Licença e contrato | [05-licenca.md](05-licenca.md) | Benefícios, vigência, pagamentos e avisos |

## Convenção dos diagramas

```
  ┌─ etapa ─┐   função que executa              arquivo:linha
```

Um `⚠` numa etapa marca um ponto onde o fluxo **já quebrou em produção**.
O detalhe do defeito e da correção fica no próprio arquivo do fluxo.

## As três identidades que confundem tudo

Boa parte dos bugs deste sistema sai de confundir três coisas parecidas:

| O quê | Exemplo | Estável? |
|---|---|---|
| **JID completo** | `558196807479:2@s.whatsapp.net` | ❌ o `:2` muda a cada re-pareamento |
| **Número base** | `558196807479` | ✅ é a identidade real da sessão |
| **`phone` digitado** | `5581996807479` | ❌ é texto de formulário, pode estar errado |

**Regra:** casamento de sessão é sempre por **número base**. Em SQL, o
**duplo** split — só por `':'` não funciona em JID sem suffix (devolve a string
inteira com o domínio junto):

```sql
SPLIT_PART(SPLIT_PART(jid,'@',1),':',1)
```

### ⚠ ...menos para Cloud API

O JID da Cloud API **é** `cloud:<phone_number_id>`. O duplo split corta no `:` e
devolve `cloud` para **todas** elas — e aí toda sessão Cloud casa com toda
sessão Cloud. Foi assim que as falhas, sessões e mensagens Cloud de clientes
diferentes viraram um número só (bug #11 em
[../aprendizados/02-bugs-resolvidos.md](../aprendizados/02-bugs-resolvidos.md)).

Consulta nova **não escreve o split na mão**. Use a função que já trata os dois
casos:

| Onde | O que usar |
|---|---|
| SQL | `sqlNumeroBase` ([internal/db/escopo.go](../../internal/db/escopo.go)) |
| Go | `numeroBase` ([internal/api/tenant_isolation.go](../../internal/api/tenant_isolation.go)) |

As duas são a **mesma** decisão em duas linguagens. Se uma mudar, a outra muda
junto — cada cópia solta do split é um vazamento esperando acontecer.
