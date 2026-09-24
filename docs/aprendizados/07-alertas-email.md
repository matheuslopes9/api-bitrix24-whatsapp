# Alertas por e-mail

Por que existem, como o envio funciona, e as decisões que não são óbvias
olhando só o código.

## Por que existem

Em **23/09/2026** o token do Bitrix do teclife venceu às 21:02. A renovação não
passava, e enquanto isso **nenhuma mensagem de cliente chegava no Contact
Center** — elas falhavam, esgotavam as tentativas e morriam na fila.

Ninguém soube até o dia seguinte. A dead queue saiu de 12 para 55 durante a
noite, com mensagem real dentro:

> *"Preciso de auxílio referente boletos em atraso"* — cliente esperando
> resposta desde 21:01.

E a tela de suporte dizia **`estado: ok`** o tempo todo.

O problema não foi falta de dado: o dado estava no banco. Foi que ele só
aparecia para quem fosse olhar. **O sistema precisava procurar o time, não
esperar ser consultado.**

## Arquitetura do envio

```
UC Talk ──SMTP simples──► uctalk_email ──XOAUTH2──► Microsoft 365
(Go)      sem auth        (proxy Python)  OAuth2     smtp.office365.com
```

O UC Talk **não fala com a Microsoft**. Ele manda SMTP puro para o proxy
(`tools/oauth2-email-service`), que resolve o OAuth2 com o Azure AD.

**A razão de existirem dois serviços:** nenhuma credencial do Azure entra no
processo do UC Talk. O `client_secret` do Azure permite enviar e-mail como
`@uctechnology.com.br` — vale para o domínio inteiro, não só para este app.
Mantê-lo fora do container que fala com a internet é contenção de estrago.

### Armadilhas de container

Cinco problemas empilhados atrasaram a subida do proxy, e todos voltam se
alguém recriar o serviço:

| Sintoma | Causa |
|---|---|
| Deploy do app errado | `Build Path` na raiz em vez de `tools/oauth2-email-service` |
| `no such host` | Container nunca subiu — salvar env não faz deploy |
| Reinício a cada 3s | Sonda de saúde batendo no SMTP, que não fala HTTP |
| Sonda na porta errada | `EXPOSE 2526 80` — o orquestrador pega a **primeira** |
| `500 Line too long` | Corpo HTML numa linha só; SMTP limita a 1000 octetos |

O `entrypoint.py` resolve os dois do meio: sobe um endpoint HTTP de saúde na
porta 80 ao lado do proxy, e reinicia o proxy se ele cair. O `EXPOSE` lista
**só a 80** — `EXPOSE` não abre porta, é metadado, mas é o metadado que o
orquestrador usa para decidir onde sondar.

`PROXY_HOST` **tem** que ser `0.0.0.0`. Com `127.0.0.1` o serviço sobe verde e
continua inalcançável — falha silenciosa cara de diagnosticar.

## O que dispara alerta

| Categoria | Condição | Janela padrão |
|---|---|---|
| `token_vencido` | Domínio sem **nenhum** token válido | 6h |
| `sessao_desconectada` | Banco diz ativo, manager não tem conexão viva | 30min |
| `licenca_vencimento` | Vence em ≤7 dias, ou venceu sem pagamento | diário |

### Por que "nenhum token válido", e não "algum token vencido"

Um domínio tem **várias linhas** em `bitrix_tokens` — uma por app que já
autorizou aquele portal. Sobra linha órfã de instalação antiga, vencida para
sempre.

A primeira versão listava qualquer linha vencida, e disparou alerta num cliente
que estava **atendendo normalmente**, com 117 mensagens de entrada no dia.

**Alerta falso é pior que nenhum:** ensina o time a ignorar, que é exatamente o
que o sistema de avisos veio evitar. Hoje só entra domínio cujo token *mais
novo* já venceu.

### Por que Cloud API fica de fora do alerta de sessão

A sessão Cloud API é stateless por HTTPS e **não vive no manager**. Ausência
ali não significa queda — alertaria falso a cada ciclo. Cobrir a Cloud API
exige checar a API da Meta, que é outro mecanismo.

## Deduplicação

`alertas_operacionais` guarda o que já foi enviado. Sem isso, um token vencido
geraria e-mail **a cada 5 minutos**.

A decisão e o registro acontecem na **mesma query** (`DeveAvisar`). Se fossem
separados, um erro no meio do envio deixaria a porta aberta para reenviar em
loop. No pior caso perde-se *um* aviso — melhor que inundar a caixa de todo
mundo.

Janela zero é recusada no formulário pelo mesmo motivo.

## Configuração vive no banco, não no ambiente

`config_alertas` (linha única) guarda servidor, remetente, destinatários, quais
alertas e as janelas. O job relê **a cada ciclo**.

**Por quê:** trocar um destinatário exigia editar variável e reiniciar — ou
seja, derrubar o atendimento de todos os clientes por causa de um e-mail. E de
dentro do painel não dava para ver se o alerta estava ligado nem para quem ia.

As envs continuam valendo como **carga inicial**: instalação nova nasce
funcionando com o que já está no ambiente, e dali em diante a tela manda.
Nunca sobrescreve o que foi salvo pelo painel.

## O e-mail

Usa o template da UC Technology (`internal/email/template.go`), portado do
`alert_email_template.py` do backend de ferramentas. Alerta do UC Talk chega
com a mesma cara dos outros alertas da plataforma.

**A categoria decide** o setor da faixa, o selo e a rota do botão. É o que
evita o alerta de WhatsApp chegar carimbado como "Licenciamento". Categoria
desconhecida cai num padrão seguro em vez de quebrar — alerta que não sai por
causa de rótulo é pior que alerta com rótulo genérico.

**O botão aponta para o painel do UC Talk**, não para o de ferramentas: quem
recebe precisa chegar na tela que resolve aquele problema.

### Escolhas de marcação que parecem antiquadas

São o que faz o e-mail chegar igual no Outlook, Gmail e celular. Os testes as
travam para ninguém "modernizar" por engano:

- **Layout em `<table>`** — o Outlook renderiza com o motor do Word, que ignora
  flex/grid e empilha tudo numa coluna.
- **Estilo inline em cada elemento** — vários clientes descartam o `<style>` do
  cabeçalho.
- **Fundo escuro em toda célula** — cliente que força tema claro repinta o que
  estiver sem cor explícita, e o texto claro sumiria.
- **Link em texto embaixo do botão** — se imagem ou botão forem bloqueados,
  ainda dá para copiar.

### Corpo em base64, multipart

Duas correções que vieram de falha real:

**`500 Line too long`** — o corpo HTML sai numa linha só e o SMTP limita linha
a 1000 octetos (RFC 5321 4.5.3.1.6). `Content-Transfer-Encoding: base64`,
quebrado em 76 colunas, resolve isso e ainda entrega acento intacto.

**`multipart/alternative`** com texto puro antes do HTML, mesma estrutura do
`send_email.py`. Leitor que bloqueia HTML, notificação de celular e relógio
mostram o texto em vez de nada — alerta ilegível às 3h da manhã é alerta
perdido. E mensagem só-HTML pontua pior em filtro de spam.

## A logo

Servida pelo **próprio app** (`/assets/logo-email.png`), não por
`ferramentas.uctechnology.com.br` como faz o template original.

Alerta chega justamente quando algo está quebrado; a hora de descobrir que a
imagem depende de outro serviço no ar não pode ser essa. E em homolog o e-mail
deixa de puxar imagem de produção.

> **Cuidado:** já existe um `assets/logo.png`, que é a marca do **UC Talk** no
> dashboard e no favicon do cliente. São arquivos diferentes. Sobrescrever um
> pelo outro troca a identidade do produto na tela do cliente final.

## O que aprendi com isso

**Observabilidade primeiro.** Os três commits que adicionaram diagnóstico —
motivo da falha gravado no job, profundidade das filas, `client_id` lado a lado
— transformaram investigações de meio dia em leituras de cinco minutos. Antes
deles, cada problema virava uma sequência de hipóteses testadas por deploy.

**Estado tem que refletir se dá para usar, não se está preenchido.** A tela
dizia `ok` porque os dois campos do token tinham conteúdo. O token estava
vencido há 16 horas.

**Alerta sem "o que fazer" é ruído.** Quem recebe às 3h da manhã precisa do
próximo passo escrito, não de um diagnóstico para interpretar.
