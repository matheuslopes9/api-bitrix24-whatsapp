# Aprendizados do Projeto UC Talk

Base de conhecimento do conector **WhatsApp ↔ Bitrix24** (UC Talk). Reúne o que
aprendemos construindo e operando o sistema: bugs resolvidos com causa-raiz,
como cada integração externa funciona de verdade, as decisões de arquitetura e
o que ainda está pendente.

Estes documentos são a fonte que alimenta o grafo de conhecimento (graphify):
mantê-los atualizados mantém o "cérebro" do projeto atualizado.

## Índice

| Documento | Conteúdo |
|---|---|
| [01-arquitetura.md](01-arquitetura.md) | Visão geral, componentes, decisões de design, o God Object `Repository` |
| [02-bugs-resolvidos.md](02-bugs-resolvidos.md) | Troubleshooting: sintoma → causa-raiz → fix de cada problema real |
| [04-integracao-bitrix.md](04-integracao-bitrix.md) | Bitrix24: imconnector, robôs BizProc, placement/menu, OAuth |
| [05-integracao-whatsapp.md](05-integracao-whatsapp.md) | whatsmeow, device suffix, persistência de sessão, Cloud API Meta |
| [06-pendencias.md](06-pendencias.md) | O que falta, próximos passos, riscos de segurança pra produção |
| [07-alertas-email.md](07-alertas-email.md) | Alertas por e-mail: proxy OAuth2, categorias, janelas, template |
| [08-isolamento-e-identidade.md](08-isolamento-e-identidade.md) | Como sabemos quem está pedindo e o que essa pessoa pode ver |
| [09-midia.md](09-midia.md) | Arquivos das conversas: store em disco, entrega e retenção |

> **Procurando como usar as telas?** Isto aqui explica como o sistema funciona
> por dentro. O que fazer na tela está em [`docs/manuais/`](../manuais) — o
> painel do suporte e o guia do cliente.

> Os documentos de MaxiPago e PIX Itaú foram **removidos** junto do módulo de
> cobrança. O porquê da remoção está em
> [`docs/fluxos/05-licenca.md`](../fluxos/05-licenca.md).

## Convenção

Cada bug/decisão segue o padrão **Sintoma → Causa-raiz → Fix → Lição**, para
que a lição sobreviva mesmo depois que o código mudar.

## O que este projeto ensinou, em quatro frases

Quatro lições se repetiram em bugs de origens completamente diferentes. Elas
valem mais que o catálogo:

1. **Falha silenciosa é pior que falha barulhenta.** `encoding/json` aceitando a
   struct errada, `r.json()` sem `r.ok`, `.catch` vazio, `placement.list` no
   lugar de `placement.get` — nenhum gerou erro, todos mentiram na tela ou no
   dado. Três custaram dias.
2. **Diagnóstico errado custa mais que diagnóstico ausente.** Tela dizendo
   "0 abas registradas" com quatro registradas, e "conector quebrado" com o
   conector ativo, mandaram procurar no lugar errado.
3. **Quando o default de uma falha é mostrar demais, inverta o default.** Escopo
   não preenchido tem que devolver vazio, nunca o banco inteiro.
4. **Quem decide é o sistema externo, não a nossa coluna.** `expires_at` local
   dizia que o token valia; o Bitrix dizia que não. A autoridade é de quem
   emite.

## Como atualizar o grafo depois de editar estes docs

```bash
graphify update .    # re-extrai o código (sem LLM) e atualiza graphify-out/
```
