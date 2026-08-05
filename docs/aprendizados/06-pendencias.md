# Pendências & Próximos Passos

Estado em que o projeto está parado, o que falta, e os riscos a tratar antes de
produção.

## 🔴 Segurança — ANTES de produção (crítico)

Vários segredos foram expostos durante o desenvolvimento (chat, envs de
homolog). **Rotacionar TODOS antes de ir a produção** e nunca commitar:

- [ ] `ADMIN_PASSWORD`
- [ ] `POSTGRES_PASSWORD`, `REDIS_PASSWORD`
- [ ] `BITRIX_CLIENT_SECRET`
- [ ] `APP_SECRET`
- [ ] `ITAU_CLIENT_SECRET` — foi manuseado no setup; rotacionar via gerente antes de prod
- [ ] Token GitHub PAT exposto no chat — **revogar** em github.com/settings/tokens
- [ ] Senha do servidor de faturamento — exposta no chat; trocar independentemente

**Nota:** o `.env` e os certificados (`itau.crt`/`itau.key`) **nunca** vão pro
git — estão no `.gitignore` (`faturamento/`, `*.key`, `*.crt`, `certs/`). Usar
env do EasyPanel + volume pros certificados.

**Regra do EasyPanel:** senhas **não podem conter `#`** — o EasyPanel interpreta
como comentário e trunca o valor.

## 🟢 Pagamentos — Itaú (PIX + Boleto)

**Estado:** código completo e commitado. Checkout usa Itaú direto pros dois
métodos. MaxiPago aposentada (código morto, limpeza na fase 2).

### O que falta pra funcionar em homologação
- [x] Cliente PIX + Boleto Itaú implementados
- [x] Certificado da empresa já existe (`faturamento/app/certs/`, válido 2027)
- [x] Produto PIX habilitado pelo gerente
- [ ] **Copiar** `itau.crt`/`itau.key` pro volume `/app/certs/` do EasyPanel
- [ ] **Setar** as envs `ITAU_*` (secret vem de `faturamento/app/.env`)
- [ ] **Cadastrar webhook** no Itaú: `https://SEU-DOMINIO/billing/itau` (SEM `/pix`)
- [ ] **Validar mTLS do webhook** atrás do Traefik/EasyPanel (pode precisar ajuste)
- [ ] Confirmar **DNS de homologação** com o gerente (pra `ITAU_ENV=sandbox`);
      sem ele, teste roda em `producao` — **PIX/boleto reais** (usar R$1 e
      `ITAU_ETAPA=validacao` pro boleto não emitir de verdade)

### ⚠️ Sobre testar em homologação
Não há sandbox Itaú configurado (falta o DNS). Então:
- **Boleto:** `ITAU_ETAPA=validacao` → valida mas não emite real. Seguro.
- **PIX:** `ITAU_ENV=producao` → **cobrança real**. Testar com R$1, QR não circula.

Ver setup completo em [07-integracao-pix-itau.md](07-integracao-pix-itau.md).

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
