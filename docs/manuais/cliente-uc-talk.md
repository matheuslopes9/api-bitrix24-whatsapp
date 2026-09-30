# UC Talk — Guia de uso

Para quem usa o UC Talk no Bitrix24 da sua empresa.

O UC Talk liga o **WhatsApp** ao **Bitrix24**. A conversa que o cliente manda
pelo WhatsApp chega no Contact Center como qualquer outro atendimento, e a
resposta do atendente volta para o WhatsApp dele. Do lado do cliente final não
muda nada: ele continua conversando pelo WhatsApp normal.

---

## 1. Conectar o número

1. Abra o **UC Talk** no menu do seu Bitrix24.
2. Vá em **Sessões WhatsApp**.
3. Clique em **Conectar WhatsApp**. Vai aparecer um QR Code.
4. No celular do número que vai atender: **WhatsApp → ⋮ → Aparelhos conectados
   → Conectar um aparelho**.
5. Aponte a câmera para o QR Code da tela.

Pronto. Em alguns segundos o número aparece como **online** e já começa a
receber.

> **Importante:** use o celular do número da empresa, não o pessoal do
> atendente. O UC Talk atende pelo número que for pareado aqui.

### O que NÃO fazer

Não remova o UC Talk de **Aparelhos conectados** no celular. Isso desliga o
atendimento na hora: o portal continua parecendo normal, mas **nenhuma mensagem
entra nem sai** até alguém ler o QR Code de novo.

Se isso acontecer, a tela de Sessões vai mostrar **desvinculado** com a
instrução do que fazer.

---

## 2. Onde as conversas aparecem

### No Contact Center

Toda mensagem recebida vira um atendimento na **Linha Aberta** configurada para
o seu número. É ali que o time atende no dia a dia, com a fila e a distribuição
que o Bitrix já faz para os outros canais.

### No card do contato, lead ou negócio

Existe uma aba **UC Talk** dentro do card, com o histórico daquela pessoa e uma
caixa para responder sem sair do CRM.

> **Não está achando a aba?** Ela costuma estar no menu **"Mais"** do card. O
> Bitrix mostra só as abas que cabem na barra e recolhe o resto ali — em cards
> com muitos campos, a aba do UC Talk quase sempre fica nesse menu.

---

## 3. As telas do painel

| Tela | Para quê |
|---|---|
| **Painel** | Visão do dia: números conectados e movimento de mensagens |
| **Sessões WhatsApp** | Conectar, reconectar e acompanhar cada número |
| **Filas Bitrix** | Qual Linha Aberta recebe cada número |
| **Permissões** | Quem da equipe pode usar cada número |
| **Templates** | Mensagens aprovadas pela Meta (só com Cloud API no contrato) |
| **Histórico** | Conversas anteriores (só com Relatórios no contrato) |
| **Relatórios** | Volume e desempenho do atendimento (só com Relatórios no contrato) |

Abas que não aparecem no seu menu são benefícios que não estão no contrato —
fale com o comercial da UC Technology.

---

## 4. Permissões por número

Se a empresa tem mais de um número, dá para controlar quem usa cada um em
**Permissões**.

Existe um **usuário master**, que é quem administra as permissões. Ele libera ou
remove o acesso de cada pessoa a cada número. Quem não tem acesso a um número
simplesmente não consegue enviar por ele.

Isso é útil quando setores diferentes atendem por números diferentes e você não
quer que um veja a conversa do outro.

---

## 5. O que o UC Talk entende do WhatsApp

Chega no Bitrix, do jeito que o cliente mandou:

- Texto, foto, vídeo, áudio e documento
- Figurinha e **reação** (o emoji que o cliente põe na mensagem)
- Localização, inclusive localização em tempo real
- Contato compartilhado
- Resposta citando uma mensagem anterior
- Mensagem **editada** e mensagem **apagada** pelo cliente
- Enquete, com o voto identificado por opção

Alguns tipos mais raros — pedido de catálogo, convite de grupo, resposta de
botão — ainda não viram mensagem no Bitrix, mas ficam registrados. Se precisar
de algum deles, fale com o suporte.

---

## 6. Problemas comuns

### O número aparece como "offline"

A conexão caiu — queda de internet, celular desligado, reinício do serviço. **O
sistema reconecta sozinho.** Espere alguns minutos. Se continuar, chame o
suporte.

### O número aparece como "desvinculado"

O aparelho foi removido em *Aparelhos conectados* no WhatsApp. Isso **não se
resolve sozinho**: vá em **Sessões WhatsApp → Conectar WhatsApp** e leia o QR
Code de novo.

### Parei de receber mensagens e o número está online

Quase sempre é a autorização do app com o Bitrix que venceu. **Abra o UC Talk
uma vez** pelo menu do Bitrix — em geral isso já renova. Se não resolver, chame
o suporte informando o domínio do seu portal.

### A aba do UC Talk não aparece no card

Procure no menu **"Mais"** do card. Se realmente não estiver lá, peça a um
**administrador** do seu Bitrix24 para abrir o UC Talk uma vez — o registro das
abas exige permissão de administrador e acontece sozinho nesse acesso.

### Recebi aviso de licença vencida

**O atendimento continua funcionando.** A licença vencida gera aviso, mas não
desliga nada. Fale com o comercial da UC Technology para regularizar.

---

## 7. Suporte

Ao abrir um chamado, informe:

- o endereço do seu Bitrix24 (ex.: `suaempresa.bitrix24.com.br`)
- o número de WhatsApp envolvido
- o horário aproximado do problema
- o que aparece na tela **Sessões WhatsApp**
