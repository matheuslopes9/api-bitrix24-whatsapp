# Pendências & Próximos Passos

Estado em que o projeto está parado, o que falta, e os riscos a tratar antes de
produção.

## 🔴 Segurança — ANTES de produção (crítico)

Vários segredos foram expostos durante o desenvolvimento (chat, envs de
homolog). **Rotacionar TODOS antes de ir a produção** e nunca commitar:

- [ ] `ADMIN_PASSWORD` (estava `admin`)
- [ ] `POSTGRES_PASSWORD`
- [ ] `BITRIX_CLIENT_SECRET`
- [ ] `APP_SECRET`
- [ ] MaxiPago `MerchantKey`
- [ ] Credenciais do Portal Sandbox MaxiPago
- [ ] Senha do servidor de faturamento (Itaú) — foi exposta; trocar
      independentemente

**Nota:** o `.env` e `CREDENCIAIS.md` **não** devem conter segredos reais em
commits. Manter em `.gitignore` e usar env do EasyPanel.

**Regra do EasyPanel:** senhas **não podem conter `#`** — o EasyPanel interpreta
como comentário e trunca o valor.

## 🟡 MaxiPago (pagamentos)

- [x] IP `187.110.174.122/32` no allowlist (**feito**)
- [ ] **Testar boleto** — admin → Gateway → Testar gateway. Com o IP liberado,
      deve sair do `DECLINED (1)`. Este teste valida a integração de ponta.
- [ ] **Credencial PIX do Itaú** — Chave PIX + Client ID + Token, da aplicação
      **PIX Recebimentos** (escopo `cob.write`). Processo bancário com o gerente
      Itaú. Credencial de outro sistema (faturamento/boleto) não serve.
- [ ] Registrar URL de postback no portal MaxiPago:
      `https://uctalk-homolog-connector.omva7z.easypanel.host/billing/maxipago/postback`
- [ ] Confirmar URL do **SmartPage** (checkout hospedado) com o suporte — destrava
      pagamento por cartão de forma PCI-safe.

**Caminho de venda que já funciona sem essas pendências:** boleto (assim que o
teste passar).

## 🟡 Bitrix (ambiente de teste)

- [ ] Registrar o app no Bitrix de **teste/homologação** (deixado para depois —
      "essa parte do bitrix vamos ver depois"). Produção já está no ar para a
      empresa.

## 🟢 Evoluções de automação (não implementadas)

Três melhorias de automação idealizadas mas nunca construídas:

- [ ] Robô "**aguardar resposta do cliente**" (pausa o fluxo até o cliente
      responder)
- [ ] **Retorno de status** para o workflow (o robô devolve resultado ao BizProc)
- [ ] **Templates de fluxo prontos** (fluxos pré-montados para o cliente usar)

## 🟢 Dívida técnica (ver [01-arquitetura.md](01-arquitetura.md))

- [ ] `Repository` é um God Object (147 arestas, ponte de 15 comunidades).
      Refatorar por domínio quando o projeto/time crescer. Não urgente.
- [ ] Chart.js minificado embutido em `internal/api/assets/` infla o grafo de
      conhecimento (~15 comunidades de ruído). Considerar servir via CDN ou marcar
      como ignorado no graphify.

## Ferramentas de desenvolvimento instaladas (Protocolo Desenvolvedor)

- [x] **graphify** — mapa mental do projeto (`graphify-out/`)
- [x] **playwright-mcp** — QA de front (Chromium baixado)
- [x] **chrome-devtools-mcp** — DevTools do Chrome
- [ ] **glyph** — busca por símbolos. **Não instalado**: falta compilador C na
      máquina (tree-sitter usa cgo). Instalar MSYS2/MinGW e recompilar, ou seguir
      sem — o graphify cobre parte do que ele faria.

Config dos MCPs em [.mcp.json](../../.mcp.json).

## Como manter esta base viva

Depois de resolver qualquer item aqui, ou aprender algo novo, edite o doc do tema
e rode:
```bash
graphify . --update
```
para o grafo de conhecimento refletir o novo estado.
