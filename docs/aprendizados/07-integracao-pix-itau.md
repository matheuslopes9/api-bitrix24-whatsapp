# Integração Itaú — PIX + Boleto (MaxiPago aposentada)

**Todo o pagamento dos planos passou para o Itaú direto:**
- **PIX** → produto "Recebimentos PIX" / API `regulatorio-pix`
- **Boleto** → produto "Cash Management V2" (Emissão de Boletos)

**A MaxiPago foi aposentada.** O código dela ainda existe (funções `maxipago*`
em billing.go, usadas só pelo teste de gateway do admin), mas o checkout **não
a usa mais** — tanto PIX quanto boleto vão pelo Itaú. O postback MaxiPago e sua
rota foram removidos.

**Decisão:** o Itaú já dá boleto E PIX com o mesmo certificado; o MaxiPago virou
intermediário redundante (e o PIX dele nunca funcionou). Simplificou tudo.

## Boleto Itaú (Cash Management)

Emissão: `POST api.itau.com.br/cash_management/v2/boletos` (corpo envolto em
`{data:{...}}`). Código: [internal/itau/boleto.go](../../internal/itau/boleto.go).

**⚠️ "Nosso número" crescente e único (carteira 109):** cada boleto exige um
número sequencial que nunca se repete, nem entre reinícios. Implementado com a
tabela `boleto_numeracao` (migration 041) + `ProximoNossoNumero` (UPDATE ...
RETURNING atômico — serializa concorrência no Postgres).

**Dados da conta** (já conhecidos, defaults no config): agência **1565**, conta
**0099415** DAC **7**, carteira **109**, espécie **08**. Sobrescrevíveis por env.

---

## PIX Itaú (Recebimentos PIX)

## Como funciona

| Etapa | Endpoint |
|---|---|
| 1. Autenticar | `POST sts.itau.com.br/api/oauth/token` (mTLS + OAuth) |
| 2. Criar cobrança | `PUT pix-pj.api.itau.com/regulatorio-pix/v2/cob/{txid}` |
| 3. Pegar QR | `GET .../cob/{txid}/qrcode` |
| 4. Receber pagamento | Itaú chama nosso webhook `/billing/itau/pix` |

Código: [internal/itau/](../../internal/itau/) (cliente) +
[internal/api/billing_itau.go](../../internal/api/billing_itau.go) (integração).

## ⚠️ Aprendizados críticos

### 1. Certificado é compartilhado, PRODUTO não é
O **certificado mTLS** da empresa (e-CNPJ, `itau.crt`/`itau.key`) serve para
**boleto E PIX** — é um mecanismo de autenticação unificado. **MAS** o **Client
ID** precisa ter o produto **"Recebimentos PIX" habilitado separadamente** pelo
gerente. Mesmo Client ID que faz boleto, se só tem escopo Cash Management, dá
`403 Acesso Negado` na primeira chamada `/cob`.

> O cliente Go já trata esse 403 com mensagem explícita apontando pro produto
> PIX não habilitado ([itau.go](../../internal/itau/itau.go), `mapErro`).

### 2. Credenciais vão NO CORPO do token, não Basic auth
`sts.itau.com.br` **rejeita Basic auth** com "CN inválido". O `client_id` e
`client_secret` vão no **corpo** (`application/x-www-form-urlencoded`). O que
valida a identidade é o **CN do certificado mTLS == client_id**. (Descoberto no
projeto de faturamento, que já roda isso em produção.)

### 3. Webhook: cadastrar a URL SEM o sufixo /pix
Ao cadastrar o webhook no Itaú (`PUT /webhook/{chave}`), informe a URL **SEM**
`/pix` — o banco **acrescenta** o sufixo. Se cadastrar com `/pix`, o Itaú chama
`/pix/pix` e quebra.

- **Cadastrar no Itaú:** `https://SEU-DOMINIO/billing/itau`
- **O que o app serve:** `https://SEU-DOMINIO/billing/itau/pix`

### 4. Token dura ~5 min
Cacheado em memória, renovado 30s antes de expirar.

### 5. mTLS exige API Gateway com Alias (EasyPanel/Traefik)
A doc do Itaú avisa: "não exponha a API direto no load balancer; use API Gateway
com Alias". No EasyPanel (Traefik), confirmar que o certificado do **cliente**
(Itaú) chega até a aplicação no webhook. Se o Traefik terminar o TLS antes, a
validação mTLS do webhook precisa ser ajustada. **A validar em homologação.**

## Configuração (env no EasyPanel)

```
ITAU_CLIENT_ID=<client id da conta>
ITAU_CLIENT_SECRET=<secret>
ITAU_CHAVE_PIX=<chave PIX que recebe>
ITAU_CERT_PATH=/app/certs/itau.crt     # default
ITAU_KEY_PATH=/app/certs/itau.key      # default
ITAU_ENV=producao                      # ou sandbox
ITAU_BASE_URL=<DNS de homolog, se sandbox>   # opcional (PIX)
ITAU_API_KEY=<x-itau-apikey, se diferente do client_id>  # opcional

# Boleto (defaults já preenchidos com a conta conhecida — só ajustar se mudar):
ITAU_AGENCIA=1565
ITAU_CONTA=0099415
ITAU_CONTA_DAC=7
ITAU_CARTEIRA=109
ITAU_ESPECIE=08
ITAU_ETAPA=efetivacao                  # 'validacao' pra teste que não emite real
ITAU_BOLETO_URL=<base cash_management, se homolog>   # opcional
```

Quando `ITAU_CLIENT_ID` está setado, o método `pix` do checkout usa o Itaú
automaticamente. Sem ele, cai no MaxiPago (comportamento antigo).

**Certificado:** copiar `itau.crt` e `itau.key` para um volume do container em
`/app/certs/`. **Nunca** commitar — estão no `.gitignore`.

## Onde os certificados já existem

A pasta local `faturamento/` (ignorada no git) tem os certificados reais da
integração de boleto que já roda em produção:
- `faturamento/app/certs/itau.crt` + `itau.key` — válidos até **10/07/2027**
- Client ID de produção conhecido, conta agência 1565 / conta 99415-7

O certificado serve pro PIX; falta o gerente **habilitar o produto Recebimentos
PIX** para o Client ID e informar a **Chave PIX** + **DNS de homologação**.

## Pendências (ver [06-pendencias.md](06-pendencias.md))

- [ ] **Gerente Itaú:** habilitar produto "Recebimentos PIX" para o Client ID
- [ ] **Gerente Itaú:** informar a Chave PIX de recebimento e o DNS de homologação
- [ ] Copiar `itau.crt`/`itau.key` pro volume `/app/certs/` do EasyPanel
- [ ] Setar as envs `ITAU_*` no EasyPanel
- [ ] Cadastrar o webhook `https://SEU-DOMINIO/billing/itau` (sem `/pix`) no Itaú
- [ ] Validar mTLS do webhook atrás do Traefik/EasyPanel
- [ ] Testar em homologação antes de produção
