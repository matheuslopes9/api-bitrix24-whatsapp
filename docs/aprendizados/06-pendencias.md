# Pendências & Próximos Passos

Onde o projeto está, o que falta, e o que precisa ser checado antes e durante a
fase de testes.

---

## 🔴 Segurança — fazer agora

Vários segredos circularam em chat durante o desenvolvimento e a operação.
**Rotacionar todos**, em ordem de dano potencial:

- [ ] **`AZURE_CLIENT_SECRET`** — o mais grave. Permite enviar e-mail como
      `@uctechnology.com.br`; vale para o **domínio inteiro**, não só para este
      app. Rotacionar no Azure AD e atualizar o serviço `uctalk_email`.
- [ ] **`BITRIX_CLIENT_SECRET`** do app instalado no portal do cliente.
      Ao rotacionar, atualizar em **Saúde do cliente → Credenciais do app**
      *antes* de testar, senão volta o `wrong_client`.
- [ ] `APP_SECRET` — assina os cookies de tenant e de admin. Trocar invalida
      as sessões abertas, o que é aceitável.
- [ ] `ADMIN_PASSWORD`, `POSTGRES_PASSWORD`, `REDIS_PASSWORD`.
- [ ] Token GitHub PAT, se ainda existir — revogar em
      github.com/settings/tokens.

**Regra do EasyPanel:** valor de env **não pode conter `#`** — ele trunca ali.

---

## 🔴 Segurança — identidade do tenant (auditoria de 25/09)

A auditoria de rotas achou que **a identidade do cliente não era verificada**.
Corrigido em 25/09:

- [x] `/debug/*`, `/sim/*`, `/bitrix/webhook` e `/bitrix/crm/debug` públicos.
- [x] Relatórios, export e painel mostravam dados de todos os clientes.
- [x] `/bitrix/auth`, `/bitrix/install` e `/bitrix/callback` gravavam token e
      emitiam cookie sem validar — agora o token é conferido em `/rest/profile`
      do próprio portal. Cookie de tenant mudou de assinatura (os antigos,
      possivelmente forjados, deixaram de valer).
- [x] "First-touch" do `application_token` aceitava o primeiro que chegasse.
- [x] `/bitrix/crm/*` confiava em `?domain=`/`user_id`; histórico trazia
      conversas de outros clientes; envio aceitava número alheio.
- [x] `/ui/bitrix/*`, histórico, permissões e SMS aceitavam outro portal/número.
- [x] `/bitrix/partner/link` transferia número por prefixo; `bp/send`
      disparava por número de outro portal.

Ainda aberto:

- [ ] **`CONNECTOR_EVENT_TOKEN=exigir`.** Hoje em `observar`: o domínio do
      evento já é conferido contra o dono do número, mas `application_token`
      divergente só vai pro log. Procurar no log por
      `connector event: application_token nao confere` — sem ocorrência em
      evento legítimo, mudar para `exigir`.
- [ ] Nome do operador (`operator_name`) ainda vem da tela.
- [ ] `/webhook/cloud/:id` pula a assinatura se `CloudAppSecret` estiver vazio.
- [ ] `APP_SECRET` vazio libera `/wa/*` e `/stats/*` — falhar no boot.

---

## 🟡 Bitrix — abas do CRM não registradas

O app instalado por usuário **não administrador** não consegue fazer
`placement.bind`: as abas UC Talk em contato, lead e negócio simplesmente não
aparecem, e a falha só vai para o log. Visto em crm.uctechnology.com.br em
25/09 — registrado à mão com um usuário admin via BX24. Precisa virar aviso na
Saúde do cliente e ação de "registrar abas" que use o usuário logado.

---

## 🔴 Banco de dados — investigar

Em 24/09 o Postgres entrou em **recovery mode** e demorou mais de 10 minutos
sem aceitar conexão, chegando a voltar ao estágio inicial. Isso derruba o
sistema inteiro: sem banco, mensagem de cliente não é entregue.

- [ ] Descobrir **por que** houve desligamento sujo (reinício do host, falta de
      memória, disco cheio). Se foi disco, volta a acontecer.
- [ ] Conferir espaço livre e política de retenção de `messages`
      (migration `020_messages_retention`).
- [ ] Confirmar que existe **backup recente e restaurável** — houve um
      `pg_dump` antes da migração de licenças, mas não há rotina automática.

> Nunca reiniciar o Postgres durante recovery: reinicia o processo do zero e é
> o caminho mais rápido de transformar um susto em perda real.

---

## 🟡 Fase de testes — roteiro

O que exercitar, com o resultado esperado. Cada item que falhar deve virar
issue com o log correspondente.

### Fluxo básico

- [ ] Cliente manda mensagem → chega no Contact Center com **nome correto**
      (não "Guest") e no chat certo.
- [ ] Operador responde pelo Contact Center → chega no WhatsApp do cliente.
- [ ] Operador envia pela **aba do CRM**, a partir de um **contato**.
- [ ] Operador envia pela aba do CRM a partir de um **negócio** — o telefone
      vem do contato vinculado.
- [ ] Mídia nos dois sentidos: imagem, áudio, documento.

### 9º dígito

- [ ] Enviar para contato cujo WhatsApp existe **sem** o 9, com o CRM
      guardando **com** o 9. Deve entregar, e o cliente deve conseguir
      **responder** (o log mostra `enviado_para` terminando em
      `@s.whatsapp.net`, nunca `@lid`).
- [ ] A resposta cai na **mesma conversa**, não numa nova.
- [ ] Enviar para número que não existe no WhatsApp → falha **na primeira
      tentativa**, com motivo legível, sem gastar 6 retentativas.

### Permissões

- [ ] A aba de permissões lista **todos** os funcionários ativos do portal.
- [ ] Operador sem permissão no número não consegue enviar.
- [ ] Usuário desativado no Bitrix **não** aparece.

### Licença

- [ ] Marcar Cloud API → a aba Templates aparece no painel do cliente.
      Desmarcar → some.
- [ ] Reduzir o número de sessões abaixo do usado → o painel avisa.
- [ ] Pôr `valid_until` no passado → aviso no painel, **e o atendimento
      continua funcionando** (vencida avisa, não bloqueia).

### Alertas

- [ ] **Enviar teste** em Alertas → e-mail chega no padrão da empresa.
- [ ] Desconectar um número de teste → em até 5min chega o alerta
      "Número desconectado", com o cliente e o que fazer.
- [ ] O mesmo alerta **não** se repete dentro da janela configurada.
- [ ] Alterar destinatário na tela → vale **sem reiniciar** o app.

### Resiliência

- [ ] Derrubar e subir o `connector` → as sessões reconectam sozinhas.
- [ ] Fila presa → o reprocesso automático devolve em até 15min, e o botão
      manual funciona a qualquer momento.
- [ ] Token vencido → alerta chega, e após cadastrar credencial a renovação
      volta sozinha.

---

## 🟡 Bitrix — resolver com o cliente

- [ ] **Reinstalar o app por um administrador.** Hoje o token age como o
      usuário que instalou (`ADMIN=false`), e registro fora do alcance dele
      volta vazio — foi o caso do negócio 12313. A aba do CRM contorna lendo
      pelo `BX24` do usuário logado, mas o backend não tem esse recurso.
      Reinstalar **muda** `client_id`/`client_secret`: atualizar em Credenciais
      do app antes de testar.
- [ ] Avaliar conceder o escopo **`user`** ao app. Sem ele, `user.get` é
      recusado. A listagem atual pela estrutura da empresa é rápida e completa,
      então isso deixou de ser urgente.
- [ ] **Conector duplicado:** `bitrix_accounts.connector_id` é
      `wa_qr_<numero>` e `bitrix_portals.connector_id` é `whatsapp_uc_v2`. Os
      dois estão registrados e ativos, então não quebra — mas são duas fontes
      para a mesma coisa e vão divergir de novo.
- [ ] Há outros conectores de WhatsApp ativos no portal do teclife (`WhatCrm`).
      Dois sistemas pareando o mesmo número brigam entre si.

---

## 🟢 Evoluções não construídas

- [ ] Robô "**aguardar resposta do cliente**" (pausa o fluxo até o cliente
      responder).
- [ ] **Retorno de status** para o workflow BizProc.
- [ ] **Templates de fluxo prontos** para o cliente usar.
- [ ] Alerta de desconexão para **Cloud API** — exige checar a API da Meta,
      já que a sessão oficial não vive no manager.
- [ ] Limpeza de linhas órfãs em `bitrix_tokens` (sem `access_token`). Não
      atrapalha mais desde que a leitura passou a escolher o token utilizável.

---

## 🟢 Dívida técnica

- [ ] **`Repository` é um God Object.** Fatiar por domínio (`SessionRepo`,
      `LicenseRepo`, `MessageRepo`) quando o time crescer. Não urgente.
- [ ] **JS dentro de string Go.** `admin_html.go` e `crm.go` somam milhares de
      linhas que o `go build` não enxerga. A checagem com `node --check` está
      documentada no README e já pegou três erros que iriam a produção — mas o
      certo é extrair para arquivo servido, e aí o próprio compilador de front
      valida.
- [ ] **Chart.js embutido** em `internal/api/assets/` infla o binário e o grafo
      de conhecimento.
- [ ] **`migrations/*.sql` é código morto** — as migrations que valem estão no
      array de `internal/db/db.go`. Manter a pasta só confunde; ver
      [`migrations/README.md`](../../migrations/README.md).

---

## Como manter esta base viva

Depois de resolver qualquer item aqui, ou aprender algo que custou tempo,
edite o doc do tema. A convenção é **Sintoma → Causa-raiz → Fix → Lição**, para
que a lição sobreviva mesmo depois que o código mudar.
