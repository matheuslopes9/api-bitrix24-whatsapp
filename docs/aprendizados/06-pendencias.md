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
- [x] `/ui/bitrix/*`, histórico e permissões aceitavam outro portal/número.
- [x] `/bitrix/partner/link` transferia número por prefixo; `bp/send`
      disparava por número de outro portal.

- [x] **Resposta de operador forjada.** Em `observar`, bastava omitir
      `auth[domain]` (ou saber o domínio do cliente) para enviar pelo número
      dele — **confirmado no homolog**: um POST anônimo saiu pelo WhatsApp.
      Agora sem domínio é recusado sempre, e a origem precisa de prova
      (`application_token` ou `access_token` conferido no portal).
- [x] Nome do operador (`operator_name`) vinha da tela — qualquer um assinava
      como outra pessoa. Agora vem do `/rest/profile` e viaja no cookie de
      usuário assinado.
- [x] `/webhook/cloud/:id` pulava a assinatura sem `CloudAppSecret` — bastava o
      UUID da sessão para injetar "mensagem de cliente". Agora recusa, e o App
      Secret é obrigatório ao criar a sessão Cloud.
- [x] `APP_SECRET` vazio liberava `/wa/*` e `/stats/*` e assinava cookies com
      chave vazia. Vazio agora é fatal no boot; curto (<16) apenas avisa —
      travar por tamanho derrubaria um ambiente que hoje funciona.

**Nada em aberto nesta frente.** O desenho resultante está em
[08-isolamento-e-identidade.md](08-isolamento-e-identidade.md).

---

## 🟢 A aba do UC Talk no card — NUNCA houve bug *(encerrado 30/09)*

Investiguei isto por dois dias e reportei como defeito aberto. **Estava errado.**

A aba sempre esteve lá, no menu **"Mais"** do card — junto com o Whatcrm e o
Wazzup, que são os outros apps de WhatsApp do portal. O card do contato tem
**onze** abas antes dela (Geral, Negócios, Orçamentos, Fluxos de trabalho,
Dependências, Histórico, Faturas, Admissão, Banco de Talentos, Renovação
Conectores, Adendo), e o Bitrix recolhe o excedente no overflow.

Clicada, ela abre completa: cabeçalho com o nome do operador e o status verde,
lista de conversas com o telefone do contato resolvido, e a caixa de envio.

### Por que demorei tanto para ver

Todas as quatro hipóteses que eliminei estavam corretas — e **nenhuma era a
pergunta certa**. Eu perguntava "por que o vínculo não funciona?", quando o
vínculo funcionava. A pergunta certa era "onde o Bitrix põe uma aba quando não
cabe na barra?".

O erro concreto: minhas buscas no DOM procuravam o texto "UC Talk" na página, e
o conteúdo do menu "Mais" **só e renderizado quando o menu e' aberto**. Ausente
do DOM não significava ausente do produto. Conclui "não existe" a partir de
"não encontrei", que são coisas diferentes.

O que resolveu foi trocar a heurística de DOM pelo snapshot de acessibilidade,
que lista os botões por referência estável. Com ele, abrir o menu foi um clique.

### A lição

Antes de declarar que algo não existe, vale conferir se a ferramenta de busca
alcança o lugar onde a coisa estaria. Um `grep` que não entra no menu fechado
responde sobre o `grep`, não sobre o produto.

---

## Já ELIMINADO (medido em 28/09)

- [x] **Cache/sessão por usuário** — testado com **dois usuários diferentes**
      (id 38 e id 356), sessões separadas, logout e login limpos. Mesma ausência
      nos dois, em Contato **e** Negócio.
- [x] **Handler inacessível** — `GET` e `POST` em `/bitrix/crm/tab` respondem
      **200 com 54KB de HTML**.
- [x] **Código de placement não suportado** — `CRM_CONTACT_DETAIL_TAB` está no
      `placement.list` deste portal.
- [x] **Vínculo incompleto** — o `placement.get` traz o registro inteiro e
      correto: `title: "UC Talk"`, `langAll` com TITLE no idioma do portal
      (gerado pelo próprio Bitrix a partir do `TITLE` que enviamos),
      `userId: 0` (vale para todos os usuários).

### O que sobra

O problema não está no que o app registra — isso está correto em todos os
campos inspecionáveis. Sobra o lado Bitrix:

- [ ] a versão **on-premise** deste portal pode não renderizar aba de aplicativo
      no card do CRM em uso;
- [ ] configuração ou cache de nível de portal (não de usuário).

Próximo passo exige o lado Bitrix: log de eventos do portal, ou o painel
administrativo — fora do alcance da investigação remota feita até aqui.

> A seção anterior dava isto como **resolvido** em 25/09 (registro com o usuário
> logado). O registro de fato funciona — o que não funciona é a exibição. São
> problemas diferentes, e o segundo seguia escondido atrás da tela de
> Ferramentas, que dizia "0 abas registradas" (ver bug #24).

### O que já foi resolvido nesta frente



O app instalado por usuário **não administrador** não consegue fazer
`placement.bind`: as abas UC Talk em contato, lead e negócio não apareciam, e a
falha só ia para o log. Visto em crm.uctechnology.com.br em 25/09.

- [x] `/bitrix-connect` e o menu do app registram as abas faltantes com o
      **usuário logado** quando ele é admin (até 3s, nunca segura o painel).
- [ ] Falta o **aviso na Saúde do cliente** quando as abas não estão
      registradas e o usuário logado também não é admin — hoje isso ainda passa
      despercebido até alguém reclamar que a aba sumiu.

---

## 🟢 Conector não ficava ativo na Linha Aberta *(resolvido e VALIDADO no ar, 28/09)*

> Confirmado no homolog: depois de republicar, o `imconnector.status` respondeu
> `{"ERROR":false,"CONFIGURED":true,"STATUS":true}` e a fila zerou (6 mensagens
> presas entregues, 0 falhas em 7 dias).
>
> **Portal já quebrado não se conserta no boot** — nada reativa retroativamente.
> Para cada um: Saúde do cliente → "Forçar register+activate".

**Sintoma medido:** a tela de Saúde mostrava `Conector wa_qr_558196807479 — falhou`.
As três chamadas retornavam sucesso e o conector ficava inútil — sem
`STATUS: true` a linha não aceita mensagem, e **mensagem de cliente sumia**.

- [x] **Ordem invertida em SETE pontos** (eram seis no levantamento inicial). O
      `activate` apaga os dados do conector, então `data.set` antes dele era
      jogar a configuração fora. A sequência correta vive num lugar só:
      `publicarConector` em
      [internal/api/connector_setup.go](../../internal/api/connector_setup.go).
- [x] **O sétimo ponto era o pior:** `uiUpdateBitrixQueue` chamava `activate`
      **sozinho**, sem `data.set` depois. Trocar a Linha Aberta pelo painel
      *desativava* o conector — a ação de configurar era a que parava de receber.
- [x] **`connector_id` divergente.** `partner link` gravava o genérico
      `whatsapp_uc_v2`; a migration `014` reescrevia no boot seguinte e o banco
      passava a apontar pra um conector nunca registrado. Regra única em
      `connectorDaSessao`, com teste travando o formato contra o da migration.
- [x] **Confirmação de entrega no conector errado** em `crm.go` — agora
      `conectorDeEnvio` resolve pelo vínculo da sessão.
- [x] `connectorID := "whatsapp_uc"` e `lineID = 218` **chumbados** no callback
      de install — toda instalação nova ganhava um canal fantasma.
- [x] A tela de Saúde lia `ERROR`/`CONFIGURED` de verdade e não checa mais com
      `LINE=0` (que faz o Bitrix responder `CONFIGURED: false` sempre).

---

## 🟢 Aba do CRM — envio de arquivo era mais fraco que o de texto *(resolvido 28/09)*

- [x] **Não resolvia o 9º dígito.** Para contato cujo WhatsApp só existe *sem* o
      9, mandar texto funcionava e mandar arquivo falhava — assimetria que
      ninguém adivinharia.
- [x] **Não espelhava no Open Channel.** O arquivo saa para o cliente e não
      aparecia no Contact Center: nem o próprio atendente via o que mandou.

---

## 🟢 Permissões — qualquer usuário virava master *(resolvido 28/09)*

`/ui/permissions/grant` e `/revoke` liam `caller_user_id` **do corpo**. A tela
preenchia com o `?user_id=` da URL, e havia um campo **"Atuar como master"**
onde dá pra digitar qualquer id — com a conferência só no navegador.

Na prática: qualquer usuário do portal digitava o id do master (que a própria
tela exibe) e liberava pra si **qualquer número do portal**, anulando a
permissão por número inteira. Não atravessa clientes — o cookie de tenant segura.

- [x] O caller agora vem do cookie de usuário assinado, nunca do corpo. Mesma
      correção que o `master/set` recebeu em 25/09; estas duas ficaram de fora.
- [x] O caminho do menu do app passou a emitir o cookie de usuário também
      (antes só o de tenant: sabíamos *qual portal*, não *quem*), com teto de 3s
      pra não segurar o iframe.
- [x] `revoke` ganhou o `escoparAoTenant` que só o `grant` tinha.

---

## 🟡 Credencial do Bitrix: duas fontes, e a que quase ninguém usa

Descoberto rastreando a queda de dois dias em 28/09.

- **`portalToCreds`** lê `BITRIX_CLIENT_ID`/`BITRIX_CLIENT_SECRET` do **ambiente**
  — usada por **~40 chamadas** em 7 arquivos.
- **`localCredsForDomain`** lê a credencial gravada na **conta** (o que a tela
  "Credenciais do app" preenche) — usada por **3 chamadas**.

Cadastrar pela tela dá a impressão de resolver e alcança quase nada. Hoje
funciona porque a env está preenchida, mas a armadilha continua armada.

- [ ] `portalToCreds` cair para a credencial da conta quando a env estiver
      vazia. Enquanto isso, **a env é a fonte que vale** — a mesma para todos os
      clientes.

---

## 🟡 WhatsApp — Status (`status@broadcast`) sem filtro

Não há nenhum filtro para `status@broadcast` no caminho de entrada. Atualizações
de Status dos contatos entram como mensagem normal: criam contato e vão para o
Contact Center.

- [ ] Filtrar na entrada. Não houve enxurrada até agora, mas é questão de o
      número conectado ter uma agenda ativa.

---

## 🟢 Tarefas de fundo sem `recover` *(resolvido 28/09)*

16 goroutines, zero `recover()`. Em Go, pânico em goroutine mata o **processo**
— o `recover` do Fiber só cobre handler HTTP.

- [x] Workers da fila: pânico vira erro e o job segue para retry/fila morta.
      Era o pior ponto — passa **toda** mensagem de cliente por ali.
- [x] Os três jobs perpétuos (alertas, reprocesso, licença): contenção por
      iteração, para que uma volta ruim não mate o laço.
- [ ] Sobram 4 goroutines em `cmd/server/main.go` (limpeza, boot) sem proteção.
      Risco menor — fazem pouco — mas o mesmo raio de alcance.

---

## 🔴 Banco de dados — investigar

Em 24/09 o Postgres entrou em **recovery mode** e demorou mais de 10 minutos
sem aceitar conexão, chegando a voltar ao estágio inicial. Isso derruba o
sistema inteiro: sem banco, mensagem de cliente não é entregue.

- [ ] Descobrir **por que** houve desligamento sujo (reinício do host, falta de
      memória, disco cheio). Se foi disco, volta a acontecer.
- [x] Política de retenção de `messages`: **já existe e roda** —
      `DeleteOldMessages` é chamada diariamente ([cmd/server/main.go](../../cmd/server/main.go)),
      365 dias rolling. A pendência anterior estava desatualizada.
- [ ] Conferir espaço livre em disco no servidor.
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
- [ ] O arquivo **aparece na aba do CRM**: imagem inline, áudio/vídeo com
      player, documento com nome, tamanho e download (não só o rótulo).
- [ ] Arquivo com nome longo chega no Contact Center com o **começo e a
      extensão** preservados.

### Isolamento entre clientes

- [ ] Com dois portais instalados, o painel e os relatórios de cada um mostram
      **só os próprios** números, contatos e volumes.
- [ ] `/ui/media/:id` de arquivo de outro portal → recusado.
- [ ] `POST /bitrix/connector/event` sem prova de origem → recusado (não sai
      nada pelo WhatsApp).
- [ ] `/debug/*` e `/sim/*` sem login → recusados.
- [ ] Pedir pareamento de número de outro portal → 403.

### Ritmo de envio

- [ ] Disparar várias mensagens seguidas pelo mesmo número → saem espaçadas,
      no ritmo de digitação, e **não** travam o envio dos outros números.
- [ ] Uma mensagem isolada sai **sem espera**.

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
- [ ] **Conector duplicado.** A suposição de que "os dois estão ativos, então
      não quebra" **estava errada** — medido na tela de Saúde. Ver a seção
      *Conector não fica ativo na Linha Aberta*, acima.
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
