# Bugs Resolvidos — Troubleshooting com Causa-Raiz

Cada entrada: **Sintoma → Causa-raiz → Fix → Lição**. Ordenado pelos que mais
consumiram tempo / mais provavelmente voltam a morder.

---

## 1. QR Code caindo a cada deploy (3 camadas)

**Sintoma:** depois de todo deploy no EasyPanel, a sessão WhatsApp aparecia como
"Desconectada" e exigia escanear o QR de novo. Recorrente, frustrante.

**Causa-raiz — eram TRÊS problemas empilhados:**

1. **Handler de `LoggedOut` apagava o `.db` em falhas transitórias.** Um
   `stream:error` momentâneo era tratado como logout real, e o código deletava os
   arquivos de sessão.
2. **`Reconnect` retornava `nil` para entradas "zumbi"** — quando o registro
   existia no mapa mas `!IsConnected()`, ele não reconectava.
3. **RAIZ REAL:** a env `WA_SESSIONS_DIR` estava setada como `./sessions`
   (efêmero, dentro do container) em vez de `/app/sessions` (volume persistente).
   A cada deploy, o container novo nascia sem os arquivos `.db`.

**Fix:**
1. Distinguir logout real de falha transitória em
   [internal/whatsapp/manager.go](../../internal/whatsapp/manager.go):
   ```go
   realLogout := true // stream:error (OnConnect==false) trata como logout real
   if evt.OnConnect { realLogout = evt.Reason.IsLoggedOut() }
   if !realLogout { /* mantém .db, marca Disconnected, retorna */ }
   ```
2. No `Reconnect`: se a entrada existe mas `!IsConnected()`, `Disconnect` + remove
   do mapa, depois reconecta.
3. Corrigir a env no EasyPanel para `WA_SESSIONS_DIR=/app/sessions` apontando pro
   volume persistente. O default no código já é `/app/sessions`
   ([config.go:125](../../internal/config/config.go#L125)) — o problema era a env
   sobrescrevendo com um path efêmero.

**Lição:** quando algo "se perde no deploy", o primeiro suspeito é **persistência
efêmera** (volume não montado / path errado), não a lógica da aplicação. Há um
warn defensivo no manager: "banco tem sessões QR mas /app/sessions está vazio".

---

## 2. Crash loop — migration 038 (duplicate key no índice de trial)

**Sintoma:** app em crash loop no homolog. Boot morria com:
```
migration 038_plan_trial: duplicate key value violates unique constraint
"idx_plan_trial_default" (SQLSTATE 23505)
```
Ninguém conseguia acessar a página.

**Causa-raiz:** a versão anteriormente deployada da 038 marcava o plano `basic`
com `is_trial_default=TRUE`. A versão nova insere um plano `trial` separado,
**também** marcado TRUE. Com **duas linhas** marcadas, o
`CREATE UNIQUE INDEX ... WHERE is_trial_default` colidia ao ser criado. E como
migrations rodam a cada boot, o app nunca subia.

**Fix (ordem à prova de falha):**
```sql
ALTER TABLE plan_definitions ADD COLUMN IF NOT EXISTS trial_days INT NOT NULL DEFAULT 0;
ALTER TABLE plan_definitions ADD COLUMN IF NOT EXISTS is_trial_default BOOLEAN NOT NULL DEFAULT FALSE;
DROP INDEX IF EXISTS idx_plan_trial_default;          -- 1. limpa o índice ANTES de qualquer write
INSERT INTO plan_definitions (...) VALUES ('trial',...) ON CONFLICT (code) DO NOTHING;
UPDATE plan_definitions SET is_trial_default = FALSE WHERE is_trial_default AND code <> 'trial';  -- 2. remove duplicatas
UPDATE plan_definitions SET is_trial_default = TRUE WHERE code='trial'
  AND NOT EXISTS (SELECT 1 FROM plan_definitions WHERE is_trial_default);  -- 3. garante exatamente 1
CREATE UNIQUE INDEX IF NOT EXISTS idx_plan_trial_default
  ON plan_definitions ((true)) WHERE is_trial_default;   -- 4. índice sobre CONSTANTE: no máx. 1 linha total
```

**Lição:** (reforça a regra da arquitetura) **toda migration que cria constraint
deve primeiro limpar o estado que a violaria** — `DROP INDEX` antes, normalizar os
dados, e só então `CREATE INDEX`. Indexar `((true))` com filtro parcial força "no
máximo 1 linha no total", que é a semântica de "um único plano trial padrão".

---

## 3. Menu UC Talk duplicado no Bitrix (preso, não saía)

**Sintoma:** dois itens de menu "UC Talk" no Bitrix. Um funcionava, o outro dava
"Acesso negado / Application not found". Persistia **mesmo após desinstalar** o app.

**Causa-raiz:** um `placement` órfão de uma versão antiga do código, que ficava
"preso" porque a desinstalação não limpava o binding.

**Fix:** unbind preventivo em `RegisterPlacementsForPortal` — antes de registrar,
remove qualquer placement órfão. Um "force-unbind" manual limpou o que já estava
preso; depois disso sobrou apenas 1 menu.

**Lição:** o LEFT_MENU do Bitrix é renderizado a partir do **manifesto do app**,
não de placement. Bindings de placement de versões antigas sobrevivem à
desinstalação — sempre limpe antes de registrar.

---

## 4. Dropdown de sessão/template vazio na automação (robô BizProc)

**Sintoma:** ao configurar o robô, os dropdowns de sessão e template vinham vazios.

**Causa-raiz:** o SQL de casamento de sessões usava **match exato** do JID, mas o
whatsmeow muda o **device suffix** (`:66` → `:67`) a cada re-pareamento. O JID
salvo não batia mais com o atual.

**Fix:** casamento tolerante ao suffix em `ListSessionsByDomain` e afins —
comparar pela parte base do número, ignorando o suffix:
```sql
SPLIT_PART(SPLIT_PART(ws.jid,'@',1),':',1) IN (...)
```
Mais um fallback em memória.

**Lição:** nunca compare JID do whatsmeow por igualdade exata — normalize
removendo o device suffix primeiro.

---

## 5. Campo de mensagem não aparecia no robô "Não Oficial"

**Sintoma:** mesmo após limpar cookie, recriar automação e dar refresh, o campo de
texto livre para digitar a mensagem não renderizava no editor do Bitrix.

**Causa-raiz:** o campo estava declarado com `"Type": "text"`. A API do Bitrix
**aceita** `text`, mas o editor de robôs **não renderiza** esse tipo.

**Fix:** trocar para `"Type": "string"` em
[internal/api/bp_robot.go](../../internal/api/bp_robot.go). Além disso, foi
removido o check de `DefaultSMSSessionJID` que silenciosamente descartava todos os
envios.

**Lição:** no editor de robôs do Bitrix, use `"string"` para campo de texto — não
`"text"`. E cuidado com checks que retornam 200 mas descartam a ação (falha
silenciosa).

---

## 6. Mensagem do robô/CRM não aparecia no Open Channel

**Sintoma:** a mensagem enviada só aparecia na Linha Aberta **quando o cliente
respondia** — não no momento do envio.

**Causa-raiz:** `im.message.add` **não reabre** sessões de Open Line já fechadas.
O caminho antigo (`GetCRMChatLastID` / `SendOperatorMessage`) dependia de sessão
aberta.

**Fix:** caminho único em [internal/api/crm.go](../../internal/api/crm.go) —
sempre envia direto pro WhatsApp E espelha na Linha Aberta via `PushInbound` (o
imconnector, que é o único caminho cliente→openline). Prefixos:
`📤 *Mensagem enviada externamente (%s):*` e `🤖 *Automação:*`.

**Lição:** o imconnector só funciona no sentido cliente→openline. Para "injetar"
uma mensagem de saída na timeline da Linha Aberta, espelhe-a como se fosse inbound
via `PushInbound`.

---

## 7. Homolog não subia — `relation "messages" does not exist`

**Sintoma:** ambiente de homologação novo (banco vazio) falhava no boot com
`relation "messages" does not exist`.

**Causa-raiz:** as migrations começavam na `006`, assumindo que as tabelas base
vinham das migrations `001`-`005`, que haviam sido removidas. Em banco vazio, não
havia a tabela `messages`.

**Fix:** criada a migration `000_base_schema` que cria o schema base, garantindo
que um banco zerado tenha as tabelas antes das migrations incrementais.

**Lição:** um ambiente novo com banco vazio é o teste de fogo das migrations.
Sempre garanta que o array de migrations constrói o schema **do zero**, não só
"a partir de onde a produção está".

---

## 8. `git push` reportava sucesso mas commits não chegavam

**Sintoma:** o push dizia OK, mas o commit não aparecia no GitHub / no deploy.

**Fix / prática adotada:** sempre verificar com `git fetch` + comparar
`HEAD` vs `FETCH_HEAD` (ou `REMOTO`) após o push. Só considerar "enviado" quando
os hashes batem.

**Lição:** não confie no exit-code do push isoladamente — confirme que o remoto
recebeu.

---

## 9. Docker Hub 429 (rate limit) no build

**Sintoma:** build falhava puxando imagens base com HTTP 429.

**Fix:** trocar as imagens base no `Dockerfile` para o mirror público da AWS ECR:
`public.ecr.aws/docker/library/*`.

**Lição:** em CI/build sem login no Docker Hub, use um mirror para evitar o rate
limit de pulls anônimos.

---

## 10. Histórico da aba do CRM vinha incompleto ou vazio

**Sintoma:** abrir a aba UC Talk num contato mostrava poucas mensagens, ou
nenhuma, mesmo com conversa longa no WhatsApp.

**Causa-raiz:** a consulta trazia as últimas 200 mensagens **daquele telefone** e
só depois, em Go, descartava as que não eram do portal. Um contato que também
conversa com outro cliente UC Talk enchia as 200 linhas com mensagens alheias —
que eram então descartadas, deixando o histórico deste portal curto ou vazio. O
filtro rodava **depois** do `LIMIT`.

**Fix:** `GetMessagesByPhoneNoEscopo` filtra dentro da consulta, antes do
`LIMIT`.

**Lição:** filtro de autorização aplicado depois do `LIMIT` não é só lento — ele
**muda o resultado**. Quando o filtro define o que o usuário pode ver, ele
pertence ao `WHERE`, nunca ao laço que lê o retorno.

---

## 11. Cloud API de todos os clientes somada como se fosse uma só

**Sintoma:** cliente com WhatsApp Oficial via números, falhas e volumes que não
eram dele.

**Causa-raiz:** `CountFailedMessagesByDomain` e `GetTenantUsage` reduziam o JID
cortando no primeiro `:`. Para QR isso remove o device suffix e funciona; para
Cloud API o JID **é** `cloud:<phone_id>`, então toda sessão Cloud do sistema
virava a mesma chave: `"cloud"`.

**Fix:** as duas passaram a usar `sqlNumeroBase`
([internal/db/escopo.go](../../internal/db/escopo.go)), que preserva o prefixo
`cloud:`. As outras ~15 consultas com o mesmo corte já tinham a guarda
`NOT LIKE 'cloud:%'` — conferidas uma a uma.

**Lição:** a mesma normalização escrita duas vezes vira duas regras. `numeroBase`
(Go) e `sqlNumeroBase` (SQL) são a mesma decisão em duas linguagens, e cada cópia
solta é um vazamento esperando acontecer.

---

## 12. Rajada de envio pelo mesmo número

**Sintoma:** risco de banimento — vinte mensagens saindo pelo mesmo WhatsApp no
mesmo segundo.

**Causa-raiz:** a fila de saída tem 20 workers e serializava apenas por
**destinatário**. Pelo mesmo número, 20 mensagens para 20 contatos saíam juntas.
Pior: o robô de automação tinha um controle de ritmo próprio
(`wa_send_gate`) que não conversava com a fila, então automação e atendimento
somavam as taxas no mesmo aparelho.

**Fix:** [internal/whatsapp/ritmo.go](../../internal/whatsapp/ritmo.go) — um
controle **por número** (sem device suffix), usado por todos os caminhos de
envio. Entre um envio e o próximo passa ao menos o tempo de escrever a próxima
mensagem: `2s + 100ms por caractere`, teto de `12s`. Quem responde um cliente de
vez em quando não espera nada. Cloud API fica de fora — é oficial, a Meta
controla a taxa.

**Lição:** dois controles de taxa para o mesmo recurso não se somam, se anulam.
O limite pertence ao recurso escasso (o número), não a cada caminho que o usa.

---

## 13. Fila presa quando uma rajada chegava

**Sintoma:** uma sequência grande de mensagens para um número travava o envio de
**todos** os outros.

**Causa-raiz:** a espera do ritmo acontecia dentro do worker. Uma rajada num
número só ocupava os 20 workers, todos dormindo.

**Fix:** número ocupado devolve o job para o fim da fila (`RPush`/`BLPop`, FIFO)
**sem contar como tentativa** — o worker fica livre imediatamente.

**Lição:** esperar dentro do worker transforma limite de taxa em indisponibilidade.
Devolver para a fila custa uma volta a mais e mantém o resto andando.

---

## 14. Nome de arquivo chegava cortado no começo no Open Lines

**Sintoma:** `PSE-SystemLog-83.21.0.117-beta1-download-relatorio.tar` aparecia no
Contact Center como `beta1-download-relatorio.tar` — sem o começo, que é o que
identifica o arquivo.

**Causa-raiz:** o Bitrix guarda apenas os **últimos 50 caracteres** do nome, e
ainda troca `&` por uma letra qualquer.

**Fix:** [internal/bitrix/nome_arquivo.go](../../internal/bitrix/nome_arquivo.go)
— `NomeParaBitrix` corta **no fim**, preservando o começo e a extensão, e troca
`&` por `e`. O arquivo em si não muda; só o rótulo enviado.

**Lição:** quando um sistema externo trunca, ele trunca do lado errado. Cortar
antes, do lado certo, é a única forma de escolher o que sobrevive.

---

## 15. Dono do número em pareamento se perdia no restart

**Sintoma:** depois de um deploy, um número que estava sendo pareado ficava sem
dono e o QR não voltava para quem tinha pedido.

**Causa-raiz:** entre "Conectar WhatsApp" e a leitura do QR ainda não existe
vínculo em `bitrix_accounts`, então a intenção vivia num `sync.Map` em memória —
que morre no restart.

**Fix:** tabela `pareamentos` (migration `052`), retenção de 30 dias
([internal/db/pareamentos.go](../../internal/db/pareamentos.go)). No caminho,
fechou-se um furo que já existia: pedir o pareamento de um número de **outro
portal** registrava quem pediu como dono. Agora é `403` (`DonoDoNumero`).

**Lição:** estado que decide permissão não pode viver só em memória. Um deploy
não deveria ser capaz de transferir a posse de um recurso.

---

## 16. Abas do CRM não apareciam quando o app era instalado por não-admin

**Sintoma:** as abas UC Talk em contato, lead e negócio simplesmente não
apareciam. A falha só ia para o log.

**Causa-raiz:** `placement.bind` feito pelo servidor usa o token do app, que
carrega as permissões de **quem instalou**. Instalado por usuário não
administrador, o Bitrix recusa o registro.

**Fix:** `/bitrix-connect` e o menu do app registram as abas faltantes usando o
**usuário logado** quando ele é admin (até 3s, nunca segura o painel).

**Lição:** no Bitrix, o que o app pode fazer é o que **quem instalou** podia. Um
app instalado por usuário comum é permanentemente limitado — vale conferir isso
antes de culpar o código.

---

## 17. Dois dias sem receber mensagem, por um campo vazio

**Sintoma:** `crm.uctechnology.com.br` parou de receber em 26/09 11:16. As seis
últimas mensagens falharam e **nada mais passou por dois dias**. O painel do
cliente não acusava nada de anormal.

**Causa-raiz:** nenhuma credencial OAuth cadastrada — nem em
`BITRIX_CLIENT_ID`/`BITRIX_CLIENT_SECRET`, nem na conta. Sem elas o token não
renova. O padrão que isso cria é traiçoeiro: **toda vez que alguém abre o app no
Bitrix chega um token novo, válido por 1 hora**, e tudo funciona. Passada a
hora, para tudo — até alguém abrir de novo. Parece intermitência; é um ciclo.

**Fix:** credenciais cadastradas. A mensagem de erro dizia isso desde o começo
(`client_id do token="", da config=""`), e foi lida como "ou um ou outro" quando
era **os dois**.

**Lição:** um sistema que se recupera sozinho ao ser aberto esconde a falha. O
alerta tem que disparar no *token que não renova*, não no *sintoma que some*.

---

## 18. O botão de reparo destruía a credencial antes de usá-la

**Sintoma:** com o token recém-renovado e funcionando, "Forçar
register+activate" respondia `expired_token` nos cinco passos.

**Causa-raiz:** a ordem dos passos. `save_token` vinha primeiro e gravava
`portal.AccessToken` — o token guardado em `bitrix_portals` no último install,
já vencido — **por cima** do token bom em `bitrix_tokens`.

**Fix:** `semearTokenDoPortal` só grava quando não há token utilizável, e nunca
grava um já vencido. O motivo aparece no diagnóstico em vez de virar `ok`.

**Lição:** a ferramenta de reparo é a que roda no pior momento, no estado mais
frágil. Ela precisa de mais cuidado que o caminho normal, não menos.

---

## 19. `expired_token` sem volta

**Sintoma:** a tela de Saúde dizia "token ok, válido até 14:04" enquanto **toda**
chamada real respondia `expired_token`.

**Causa-raiz:** o cliente só renova quando o `expires_at` **gravado** já passou.
Mas quem invalida o token é o Bitrix, por conta própria — basta uma nova
autorização ou um refresh feito noutro lugar. O banco seguia confiante e
nenhuma renovação era tentada.

**Fix:** na primeira recusa por token inválido, renovação forçada e uma nova
tentativa. Se o `refresh_token` também morreu, o erro que sobe já diz o que
fazer.

**Lição:** validade local é palpite. A autoridade é quem emite — e a resposta
dele vale mais que a nossa coluna.

---

## 20. O "Preview do app" nunca funcionou

**Sintoma:** a tela que o suporte usa para ver o app como o cliente vê aparecia
vazia, em Demonstração **e** com cliente selecionado.

**Causa-raiz:** o painel lê o portal de `?portal=`; o preview montava a URL com
`&domain=`. Dentro do iframe `PORTAL` ficava vazio, `apiUrl()` não anexava
parâmetro e todo `/ui/*` respondia "tenant não identificado".

**Fix:** o painel aceita os dois. Trocar só o produtor quebraria a aba do CRM,
que lê `domain`.

**Lição:** dois nomes para a mesma coisa em pontas diferentes é bug garantido —
e silencioso, porque cada lado está "certo" isoladamente.

---

## 21. A tela escrevia `undefined` para o usuário

**Sintoma:** "undefined ativas" no card e "undefined sessão(ões) ativa(s)" no
rodapé do painel do cliente.

**Causa-raiz:** `fetch().then(r => r.json())` **sem checar `r.ok`**. Num 403 o
corpo é `{"error":...}`, `d.active_sessions` vem `undefined` e vai direto para o
DOM. E o `.catch` no fim estava **vazio**: qualquer falha sumia e o painel
seguia exibindo número velho como se fosse atual.

**Fix:** guarda de `r.ok` + verificação de tipo, e o catch passa a mostrar
`--` com o motivo.

**Lição:** `r.json()` sem `r.ok` é o `catch {}` do front — transforma erro em
dado. E estado desconhecido tem que **parecer** desconhecido.

---

## 22. A Saúde acusava conector quebrado com o conector ativo

**Sintoma:** depois de republicar o conector, os cinco passos responderam `ok`,
o `imconnector.status` respondeu `STATUS: true` — e a tela continuou dizendo
"registrado, mas nao ativo+configurado".

**Causa-raiz:** `GetConnectorStatus` devolve o conteúdo de `result` **já
desembrulhado** pelo `client.call()`. O parse procurava `st.Result.STATUS`. O
`encoding/json` aceita sem reclamar — campo ausente vira zero value — então os
três booleanos viravam `false` **em silêncio**.

**Fix:** parse na raiz, extraído para função testável, com teste sobre a
resposta real copiada do homolog e outro que **recusa** a forma com envelope.

**Lição:** é a segunda vez que `encoding/json` aceita a struct errada e some com
o problema (ver #3 do ciclo da fila morta). Parse de resposta externa merece
teste com a carga real — o compilador não ajuda aqui, e o log fica limpo.

---

## 23. Pânico em tarefa de fundo derrubava o app inteiro

**Sintoma:** nenhum ainda — achado em varredura. Vale registrar antes de custar.

**Causa-raiz:** 16 goroutines de fundo, **zero `recover()`**. Em Go, pânico em
goroutine não sobe para o chamador: mata o **processo**. O `recover` do Fiber só
cobre o que roda dentro de um handler HTTP.

O pior ponto eram os workers da fila — por onde passa **toda** mensagem de
cliente, com parse de JSON, acesso a mapa, download de mídia e chamada ao
Bitrix. Uma única mensagem malformada tiraria o connector do ar para todos os
clientes.

**Fix:** pânico no processamento vira erro comum e o job segue para retry/fila
morta; os três jobs perpétuos (alertas, reprocesso, licença) têm a contenção por
**iteração**, para que uma volta ruim não mate o laço.

**Lição:** `go func()` sem `recover` é um crash global esperando entrada
estranha. O raio de alcance não é a goroutine — é o processo.

---

## 24. "0 abas registradas" com quatro registradas

**Sintoma:** a tela de Ferramentas informava que nenhum placement estava
registrado no portal — reforçando a suspeita de que as abas do CRM não tinham
subido.

**Causa-raiz:** `placement.list` e `placement.get` respondem perguntas
diferentes. O primeiro devolve o **catálogo** do que o app *pode* usar (só os
códigos); o segundo, o que o app **de fato vinculou**, com handler. A tela usava
o primeiro.

O próprio código já documentava o sintoma — *"versão nova do Bitrix retorna só
os nomes (catálogo de placements disponíveis) … Devolve vazio"*. Notaram que a
resposta mudou e trocaram por vazio, em vez de trocar pelo método que responde à
pergunta certa.

**Fix:** `ListBoundPlacements` usando `placement.get`. `ListPlacements` continua
para quem quer o catálogo.

**Lição:** diagnóstico errado custa mais que diagnóstico ausente. A tela mandava
o suporte registrar o que já estava registrado — e escondeu, por dias, o
problema real (a aba está vinculada e mesmo assim não aparece).
