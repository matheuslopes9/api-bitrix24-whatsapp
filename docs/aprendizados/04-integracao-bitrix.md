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

### ⚠️ `im.message.add` não reabre sessão fechada

`im.message.add` **não** reabre uma sessão de Open Line já encerrada. Por isso o
caminho de envio abandonou `GetCRMChatLastID`/`SendOperatorMessage` e passou a usar
um caminho único: envia direto no WhatsApp + espelha via `PushInbound`. Ver bug #6
em [02-bugs-resolvidos.md](02-bugs-resolvidos.md).

## Placement / LEFT_MENU (o menu do app)

### ⚠️ LEFT_MENU vem do MANIFESTO, não de placement

O item de menu à esquerda é renderizado automaticamente a partir do **manifesto do
app**, não de um `placement.bind`. Confundir os dois gerou o menu duplicado.

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
`EnsureTenantTrial` é chamado no auth para criar o trial do tenant no primeiro
acesso (com salvaguarda no dashboard caso a corrida falhe).

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
