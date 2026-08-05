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
