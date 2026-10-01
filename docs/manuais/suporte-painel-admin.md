# UC Talk — Manual do painel administrativo

Para a equipe de suporte da UC Technology.

O painel fica em `https://<seu-dominio>/admin`. Sem cookie válido ele manda para
a tela de login. A sessão expira; se uma ação devolver erro de autenticação,
entre de novo.

> **Regra geral:** antes de mexer em qualquer coisa, abra **Saúde do cliente**.
> Ela existe justamente para você não precisar ler log de container nem rodar
> SQL. Quase todo chamado se resolve — ou pelo menos se diagnostica — ali.

---

## 1. Por onde começar quando chega um chamado

Abra **Saúde do cliente**, digite o domínio do portal (ex.:
`cliente.bitrix24.com.br`) e leia os quatro cartões de cima para baixo. Eles
estão nessa ordem de propósito: o de cima quebra o de baixo.

| Cartão | O que ele responde |
|---|---|
| **Conexão Bitrix** | O app fala com o portal? O token renova? As abas do CRM existem? |
| **Sessões WhatsApp** | O número está mesmo atendendo *agora*? |
| **Mensagens** | Está entrando e saindo? O que falhou, e por quê? |
| **Licença** | O que o contrato libera, até quando, e quem pagou o quê |

Cada cartão fica **verde** quando está tudo certo e **vermelho** com o problema
escrito por extenso. Quando o painel não conseguiu descobrir o estado, ele
escreve isso em vez de chutar — "não consegui conferir" nunca quer dizer "está
quebrado".

---

## 2. Os sintomas mais comuns

### "O cliente parou de receber mensagens"

Na Saúde do cliente, olhe nesta ordem:

1. **Token vencido** (cartão Conexão Bitrix) — a causa mais frequente. O painel
   diz desde quando. O cliente precisa abrir o UC Talk no Bitrix uma vez para
   reautorizar.
2. **Conector inativo** — sem ele a Linha Aberta recusa a mensagem e ela morre
   em silêncio. O sistema reativa sozinho de hora em hora; para forçar agora,
   use **Testar conexão** no próprio cartão.
3. **Número desvinculado ou fora do ar** (cartão Sessões) — veja a seção
   seguinte.
4. **Fila de entrada empilhando** (cartão Mensagens) — se houver mensagens
   presas, o botão **Reentregar** reenvia as que falharam.

### "O número aparece conectado mas não chega nada"

Olhe a etiqueta ao lado do número no cartão **Sessões WhatsApp**. São três
estados, e **as ações são diferentes**:

| Etiqueta | O que aconteceu | O que fazer |
|---|---|---|
| **online** | Tudo certo | Nada |
| **offline** | A conexão caiu (rede, reinício do serviço) | **Esperar.** O sistema reconecta sozinho. Se passar de alguns minutos, aí sim investigue |
| **desvinculado** | Removeram o aparelho em *Aparelhos conectados* no celular | **Ler o QR de novo.** Reconectar não resolve: a conexão existe, o que falta é a autorização |

> Esta distinção custou caro para existir. Um aparelho desvinculado mantém a
> conexão aberta para sempre, então o sistema antigo mostrava o número como
> conectado e ninguém era avisado — o cliente ficava dias sem receber nada.
> Se a etiqueta disser **desvinculado**, não adianta reiniciar nada: use
> **Conectar WhatsApp** e peça ao cliente para ler o QR.

Na lista **Tenants**, o mesmo aparece na coluna Conexões como `0 / 1 QR`, com o
motivo logo abaixo.

### "A aba do UC Talk sumiu do card do CRM"

O cartão **Conexão Bitrix** mostra `Abas no card: 3 de 3` com ✅/❌ por aba
(Contato, Lead, Negócio).

- **Está 3 de 3:** a aba existe. Ela está no menu **"Mais"** do card — o Bitrix
  recolhe ali as abas que não cabem na barra, e o card de um cliente com muitos
  campos costuma ter dez ou mais abas antes dela. Oriente o cliente a clicar em
  "Mais".
- **Falta alguma:** registrar aba exige **usuário administrador** do portal. Se
  o app foi instalado por um usuário comum, o registro falhou em silêncio. Peça
  a um admin do Bitrix para abrir o UC Talk uma vez — o registro acontece
  sozinho nesse acesso. Se ele já for admin e mesmo assim faltar, use
  **Ferramentas → Reinstalar abas do app**.
- **Diz que não conseguiu conferir:** o portal não respondeu. Isso **não**
  significa que as abas sumiram. Não tome nenhuma ação com base nisso.

### "O cliente diz que contratou X e não tem"

Abra **Licenças**, ache o cliente e clique em **Editar contrato**. Os benefícios
são por cliente, marcados à mão:

- **nº de números** que ele pode conectar
- **Cloud API + Templates** — libera a aba Templates no painel do cliente
- **Automações** — robôs de BizProc
- **Relatórios** — libera as abas Relatórios e Histórico

Marcar ou desmarcar vale **na hora**: peça ao cliente para recarregar o painel.

**Licença vencida não bloqueia nada.** Ela aparece como vencida e gera aviso,
mas o atendimento continua. Ninguém fica sem WhatsApp por boleto atrasado —
isso é decisão de produto, não defeito.

Para lançar um pagamento use **Registrar pagamento**, informando quando pagou e
até quando cobre. A vigência se estende sozinha, e fica registrado **quem**
lançou.

### "Estamos recebendo alerta de um cliente que cancelou"

Vá em **Alertas → De quais clientes somos avisados**, ache o cliente e clique
em **Desligar alerta**. O sistema pede um motivo — escreva algo que responda a
pergunta daqui a seis meses, do tipo `contrato encerrado em 09/2026`.

**O cliente continua inteiro no sistema.** Histórico de conversas, licenças e
pagamentos ficam como estão; só os avisos por e-mail param. Apagar o cliente
para parar de receber e-mail seria destruir registro por causa de ruído.

Isso importa porque **alerta que não exige ação ensina o plantão a ignorar a
caixa** — e o próximo aviso de verdade chega no meio do que já aprenderam a
pular.

Para religar, o mesmo lugar: **Reativar alerta**.

> Um cliente silenciado aparece com o aviso **⚠ Alertas DESLIGADOS** na Saúde
> dele. Olhe para isso antes de concluir que está tudo bem: um portal mudo
> responde "sem problemas" de um jeito em que não dá para confiar, porque
> ninguém seria avisado se não estivesse.

---

## 3. As telas, uma a uma

| Tela | Para quê |
|---|---|
| **Visão geral** | Números do sistema inteiro. "Números conectados" mostra `conectados agora / ativos no banco` — se divergir, algum número caiu |
| **Tenants** | Todos os portais com o app instalado: conexões, mensagens em 24h, token e licença |
| **Consumo** | Uso de recursos por cliente |
| **Saúde do cliente** | O diagnóstico completo de um portal — comece sempre por aqui |
| **Licenças** | Contratos, vigência e histórico de pagamentos |
| **Sistema** | O processo em tempo real (memória, filas) |
| **Logs ao vivo** | Stream direto do servidor, quando a Saúde não bastou |
| **Alertas** | Quem recebe aviso, de quê, a cada quanto tempo — e de quais clientes |
| **Usuários admin** | Quem entra no painel |
| **IPs bloqueados** | Bloqueios por tentativa de invasão |
| **Auditoria** | Histórico de quem fez o quê |
| **Preview do app** | Ver o painel exatamente como o cliente vê |
| **Ferramentas** | Reparos pontuais — cada botão resolve um sintoma |

---

## 4. Ferramentas — o que cada botão faz

### Reparo de um cliente

- **Re-registrar automações** — quando os robôs de BizProc sumiram do portal
- **Reinstalar abas do app** — quando falta aba no card **e** o usuário já é
  administrador do portal

### Faxina global

- **Limpar sessões banidas** — remove do banco números banidos pelo WhatsApp
- **Limpar portais fantasma** — remove registros de instalação que nunca se
  completaram

### Destrutivo

- **Esvaziar filas** — **descarta mensagens que ainda não foram entregues.** Só
  use quando a fila estiver travada com lixo conhecido e você aceitar a perda.
  Não há como desfazer.

---

## 5. Credenciais do app — leia antes de mexer

Na Saúde do cliente existe o botão **Credenciais do app**, para o caso raro de
um portal com app OAuth **próprio**.

**Na esmagadora maioria dos casos você não deve tocar nisso.** O app Partner da
UC tem um `client_id` só, que serve todos os portais instalados, e ele vem do
ambiente do servidor. O cartão Conexão Bitrix diz qual está valendo:

- *"Renovando pelo app do ambiente — o mesmo para todos os portais. É o
  esperado."* → está certo, não mexa.
- *"Este portal tem app OAuth **próprio**"* → alguém cadastrou uma exceção
  deliberadamente.

Para desfazer uma exceção cadastrada por engano: abra **Credenciais do app** e
**salve os dois campos vazios**. O portal volta ao app do ambiente.

---

## 6. Quando escalar para o desenvolvimento

Escale quando:

- A Saúde disser que o conector **segue inativo depois de republicar** — o
  reparo automático já tentou e não resolveu.
- Aparecer `wrong_client` com token e credenciais aparentemente corretos.
- A fila de entrada crescer sem parar, ou a fila morta tiver mensagens que você
  não sabe explicar.
- Qualquer tela mostrar um estado que **contradiz** outra. Duas telas
  discordando é sinal de defeito, não de configuração.

Leve junto: o domínio do portal, o print da Saúde do cliente e o horário
aproximado do problema.
