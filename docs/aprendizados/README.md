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
| [03-integracao-maxipago.md](03-integracao-maxipago.md) | Gateway MaxiPago: IP allowlist, processorIDs, postback (boleto) |
| [07-integracao-pix-itau.md](07-integracao-pix-itau.md) | PIX direto Itaú (mTLS + OAuth), webhook, certificado, config env |
| [04-integracao-bitrix.md](04-integracao-bitrix.md) | Bitrix24: imconnector, robôs BizProc, placement/menu, OAuth |
| [05-integracao-whatsapp.md](05-integracao-whatsapp.md) | whatsmeow, device suffix, persistência de sessão, Cloud API Meta |
| [06-pendencias.md](06-pendencias.md) | O que falta, próximos passos, riscos de segurança pra produção |

## Convenção

Cada bug/decisão segue o padrão **Sintoma → Causa-raiz → Fix → Lição**, para
que a lição sobreviva mesmo depois que o código mudar.

## Como atualizar o grafo depois de editar estes docs

```bash
graphify . --update    # re-extrai só o que mudou e atualiza graphify-out/
```
