# Integração Bitrix24

O UC Talk é um app de marketplace/partner do Bitrix24. Integra com CRM, Open
Channels (Linhas Abertas) e automação (BizProc).

## imconnector (Linhas Abertas / Open Channels)

O conector liga o WhatsApp a uma Linha Aberta do Bitrix. Ver
[documentation/bitrix24-imconnector.md](../../documentation/bitrix24-imconnector.md).

**Métodos REST envolvidos:** register / activate / data.set / status /
send.messages / send.status.delivery. Evento chave: `ONIMCONNECTORMESSAGEADD`.

### ⚠️ Regra de ouro: imconnector é UNIDIRECIONAL

O imconnector só carrega o sentido **cliente → openline** (mensagem que o cliente
manda entra na Linha Aberta). Para "injetar" uma mensagem de **saída** (enviada
pelo operador/robô/CRM) na timeline da Linha Aberta, é preciso **espelhá-la como
se fosse inbound** via `PushInbound`.

### ⚠️ A ORDEM é register → activate → data.set

A [documentação do `imconnector.activate`](https://apidocs.bitrix24.com/api-reference/imopenlines/imconnector/imconnector-activate.html)
diz, com todas as letras:

> *"The connector settings specified by the `imconnector.connector.data.set`
> method are **deleted along with the status record**. After enabling the
> connector again, pass the settings once more."*

Ou seja: **o `activate` apaga os dados do conector**. Chamar `data.set` antes
dele é jogar a configuração fora.

O efeito é silêncio, não erro. Pelo
[`imconnector.status`](https://apidocs.bitrix24.com/api-reference/imopenlines/imconnector/imconnector-status.html),
`CONFIGURED` só é `true` quando o conector está *registrado, conectado e ativo ao
mesmo tempo*, e `STATUS` só é `true` quando `CONFIGURED` é `true`. **Só com
`STATUS: true` a linha aceita mensagem.** As três chamadas retornam sucesso e o
conector fica inútil.

`imconnector.status` devolve exatamente cinco campos — `LINE`, `CONNECTOR`,
`ERROR`, `CONFIGURED`, `STATUS`. Dois detalhes que custam tempo:

- **`LINE` ausente vira `0`**, e aí a resposta é `CONFIGURED: false` sempre,
  independente do conector. Checar sem linha só produz falso alarme.
- **`ERROR: true`** significa que o Bitrix marcou o conector como inoperante.
  Isso se limpa chamando `activate` de novo — não é o mesmo que "não ativado".

> ⚠️ **O código ainda faz na ordem errada** (`register → data.set → activate`)
> em seis pontos. Ver a pendência do conector em
> [06-pendencias.md](06-pendencias.md).

### ⚠️ Um conector por Número, e duas fontes para ele

Cada sessão WhatsApp tem o seu conector: `wa_qr_<telefone>` ou
`wa_cloud_<phone_number_id>`. Compartilhar um só faria as mensagens de todos os
números do portal desaguarem no mesmo canal. Reregistrar o mesmo ID **atualiza**,
não duplica — então publicar de novo é seguro.

O problema é que existem **duas fontes** para o mesmo dado:
`bitrix_accounts.connector_id` (por número) e `bitrix_portals.connector_id`
(genérico, `whatsapp_uc_v2`). Quem entrega a mensagem usa o primeiro
([processor.go](../../internal/bitrix/processor.go)); vários caminhos de
registro e a confirmação de entrega usam o segundo.

### ⚠️ `im.message.add` não reabre sessão fechada

`im.message.add` **não** reabre uma sessão de Open Line já encerrada. Por isso o
caminho de envio abandonou `GetCRMChatLastID`/`SendOperatorMessage` e passou a usar
um caminho único: envia direto no WhatsApp + espelha via `PushInbound`. Ver bug #6
em [02-bugs-resolvidos.md](02-bugs-resolvidos.md).

## Placement / LEFT_MENU (o menu do app)

### ⚠️ LEFT_MENU vem do MANIFESTO, não de placement

O item de menu à esquerda é renderizado automaticamente a partir do **manifesto do
app**, não de um `placement.bind`. Confundir os dois gerou o menu duplicado.

### ⚠️ O app só pode o que QUEM INSTALOU podia

`placement.bind` feito pelo servidor usa o token do app, que carrega as
permissões do usuário que instalou. Instalado por **não administrador**, o
Bitrix recusa o registro e as abas simplesmente não aparecem — a falha só vai
para o log. Contorno atual: `/bitrix-connect` e o menu do app registram as abas
faltantes com o **usuário logado**, quando ele é admin.

A mesma limitação explica registro de CRM que volta vazio para o backend e
aparece normalmente na aba (que lê pelo `BX24` do usuário logado).

### ⚠️ Bindings órfãos sobrevivem à desinstalação

Placements registrados por versões antigas do código **não são removidos** quando o
app é desinstalado — ficam "presos". Solução: **unbind preventivo** em
`RegisterPlacementsForPortal` antes de registrar. Ver bug #3.

## Robôs BizProc (Automação)

Robôs registrados via `bizproc.robot.add` com `USE_SUBSCRIPTION`. Dois robôs:
Oficial (template) e Não Oficial (texto livre).

### ⚠️ Campo de texto usa `"string"`, não `"text"`

No editor de robôs do Bitrix, um campo declarado como `"Type": "text"` é aceito
pela API mas **não renderiza** no editor. Use `"Type": "string"`:
```go
"message_text": map[string]interface{}{
    "Name": map[string]string{"en":"Message","pt-BR":"Mensagem"},
    "Type": "string",  // NÃO "text"
    "Required": "Y", "Multiple": "N",
}
```
O robô Não Oficial ficou como **texto livre** (suporta variáveis do CRM Bitrix), e
espelha a mensagem na Linha Aberta com prefixo `🤖 *Automação:*`. Ver bug #5.

## OAuth & Webhooks

Fluxo OAuth padrão do Bitrix (partner/marketplace). O `main()` inicializa o
`bitrix.Client`. Rotas relevantes: `/bitrix/auth`, callback OAuth, webhooks.

**Todo token recebido é conferido no próprio portal** (`/rest/profile`) antes de
ser gravado ou de virar cookie — ver
[08-isolamento-e-identidade.md](08-isolamento-e-identidade.md). O evento
`ONIMCONNECTORMESSAGEADD` exige prova de origem (`CONNECTOR_EVENT_TOKEN`,
padrão `exigir`).

Detalhe que explica muita coisa: **o evento chega pelo app local, mas o token
gravado pode ser o do Partner App.** Por isso a prova aceita dois caminhos
(`application_token` gravado **ou** `access_token` do evento aceito pelo portal).

**Rate limiting:** há um `RateLimiter`/`MethodLimiter` dedicado
([internal/bitrix/ratelimit.go](../../internal/bitrix/ratelimit.go)) porque o
Bitrix impõe limites de chamadas por método.

## CRM

- Envio pela aba CRM do contato → abre chat na Linha Aberta com rótulo
  "Mensagem enviada Externamente (nome do usuário)" + a primeira mensagem.
- Permissões de acesso a sessões por usuário do CRM (`crm_user_permissions`).
- Contexto do partner: o app é publicado no Vendor Bitrix
  (`vendors.bitrix24.com`).

## Detalhes de registro do app

- Checkbox "Add custom page and menu item" controla se a página/menu customizado
  são adicionados.
- Redirect URI configurado no app aponta para
  `.../bitrix/callback` do domínio EasyPanel.
- `event.bind` requer `INSTALLED:true` no contexto correto.
