# Bitrix24 imconnector — Documentação Completa

## Fluxo de implementação (4 passos obrigatórios)

1. `imconnector.register` — registra o conector
2. `imconnector.activate` — ativa na open line
3. `imconnector.connector.data.set` — configura dados do canal externo
4. `imconnector.status` — verifica prontidão

## imconnector.register

**PLACEMENT_HANDLER** — URL onde o Bitrix24 abre a interface de configurações
em um slider. É a URL de configuração do conector, NÃO é onde chegam mensagens.

Parâmetros obrigatórios:
- ID (string): identificador único (lowercase, sem pontos)
- NAME (string): nome exibido
- ICON (object): DATA_IMAGE (SVG Data URI), COLOR, SIZE, POSITION
- PLACEMENT_HANDLER (string): URL da interface de configuração (slider)

Parâmetros opcionais:
- ICON_DISABLED, DEL_EXTERNAL_MESSAGES, EDIT_INTERNAL_MESSAGES
- DEL_INTERNAL_MESSAGES, NEWSLETTER, NEED_SYSTEM_MESSAGES
- NEED_SIGNATURE, CHAT_GROUP, COMMENT

## imconnector.activate

Parâmetros:
- CONNECTOR (string): código do conector
- LINE (integer): ID da open line
- ACTIVE (string): "1"/"Y" para ativar, "0" para desativar

## imconnector.connector.data.set

**DATA** contém apenas:
- ID (string): identificador do canal externo
- URL (string): link público do chat no sistema externo
- URL_IM (string): link da interface do operador
- NAME (string): nome do canal

**NÃO existe send_message neste DATA** — confirmado pela doc oficial.

## imconnector.send.messages (WA → Bitrix)

Envia mensagem do cliente externo para o Bitrix.

Parâmetros:
- CONNECTOR, LINE, MESSAGES[]
  - user: {id, name, last_name, phone, skip_phone_validate}
  - message: {id, text, files, date, disable_crm, user_id}
  - chat: {id, name, url}

Resposta: DATA.RESULT[].session.CHAT_ID

## imconnector.send.status.delivery (confirmar entrega outbound)

Confirma que mensagem do operador foi entregue ao sistema externo.
Para o spinner parar no Bitrix.

Parâmetros:
- CONNECTOR, LINE
- MESSAGES[]:
  - im: {chat_id (int), message_id (int)}  ← vêm do ONIMCONNECTORMESSAGEADD
  - message: {id (array de strings), date (unix timestamp int)}
  - chat: {id (string)}

## ONIMCONNECTORMESSAGEADD — Como funciona

O evento dispara quando o operador responde no Contact Center.
- auth_type: 0 = app sem INSTALLED:true → Bitrix NÃO entrega
- auth_type: 1 = app com INSTALLED:true → Bitrix entrega

**Requisito:** event.bind feito com token de app com INSTALLED:true.

## Mecanismo de entrega de mensagens do operador

A documentação NÃO menciona nenhuma outra forma além do event.bind.
O PLACEMENT_HANDLER é apenas para UI de configuração.

O único caminho é:
1. App precisa ter INSTALLED:true no Bitrix
2. event.bind com token desse app → auth_type:1
3. Bitrix entrega ONIMCONNECTORMESSAGEADD para a handler URL

## imconnector.status

Retorna: LINE, CONNECTOR, ERROR, CONFIGURED, STATUS
Sempre passar LINE para resultado correto.
