# Mídia das conversas

Como os arquivos trocados no WhatsApp chegam à aba do CRM.

---

## O problema

A aba UC Talk (negócio, contato, lead) mostrava apenas o rótulo `Imagem` ou
`Documento`. Sem miniatura, sem player, sem download.

A causa é que **nada ficava do nosso lado**: o arquivo recebido ia direto para o
Drive do Bitrix e era anexado ao chat do Open Lines. A coluna
`messages.media_url` nunca era preenchida, então a aba não tinha o que exibir.

O volume `/app/media` já estava declarado no `Dockerfile` — e sem uso.

---

## O desenho

[internal/media/store.go](../../internal/media/store.go) guarda uma cópia
nossa. Layout em disco:

```
<raiz>/AAAA/MM/DD/<id>/<nome original>
```

Duas decisões dentro desse caminho:

- **a data na frente** deixa a retenção barata: apagar um dia é apagar um
  diretório, sem varrer o banco;
- **o id isola** arquivos de mesmo nome, que são comuns
  (`documento.pdf`, `IMG-0001.jpg`).

A referência gravada no banco é o caminho relativo com o prefixo `local:`. O
nome original sai do próprio caminho — **sem coluna nova**.

| Prefixo em `media_url` | Significado |
|---|---|
| `local:` | arquivo guardado por nós, exibível na aba |
| `grande:` | passou do limite e não foi guardado; o nome vem junto para a tela ainda dizer o que era |

Arquivo grande continua indo para o Bitrix normalmente. O limite só decide se
guardamos **nossa** cópia.

---

## Configuração

| Env | Padrão | O que é |
|---|---|---|
| `WA_MEDIA_DIR` | `/app/media` | raiz em disco (**precisa ser volume**) |
| `MEDIA_MAX_MB` | `64` | acima disso, não guarda a cópia local |
| `MEDIA_RETENTION_DAYS` | `90` | por quanto tempo o dia fica no disco |

> Mesma armadilha de `WA_SESSIONS_DIR`: se apontar para um caminho que não é
> volume, tudo evapora a cada deploy — e aí o sintoma volta a ser exatamente o
> rótulo "Imagem" sem imagem.

---

## Entrega: `/ui/media/:id`

A rota **confere o dono do número** antes de servir. Não é um caminho público:
arquivo de conversa é dado de cliente, e servir por id sem checagem seria
reabrir, na mão, o vazamento que a auditoria fechou (ver
[08-isolamento-e-identidade.md](08-isolamento-e-identidade.md)).

Na tela:

- **imagem** inline;
- **áudio e vídeo** com player — `http.ServeContent`, que dá suporte a `Range` e
  portanto permite buscar no meio do arquivo sem baixar tudo;
- **documento** com nome, tamanho e download.

---

## Nome do arquivo no Open Lines

Assunto relacionado e não óbvio: o Bitrix guarda apenas os **últimos 50
caracteres** do nome e troca `&` por uma letra qualquer. Quem corta o nome somos
nós, pelo fim, preservando começo e extensão
([internal/bitrix/nome_arquivo.go](../../internal/bitrix/nome_arquivo.go)). Ver
bug #14 em [02-bugs-resolvidos.md](02-bugs-resolvidos.md).
