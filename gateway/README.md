# Gateway Itaú — PIX + Boleto (módulo reutilizável)

Cópia **autocontida** do sistema de pagamentos do UC Talk, separada num pacote
próprio (`package gateway`) pra ser reaproveitada em outros sistemas. Fala
direto com o Itaú por **mTLS + OAuth `client_credentials`**. Só depende da
biblioteca padrão do Go — nenhum acoplamento com o UC Talk.

> Este pacote foi **copiado** de `internal/itau/` sem alterações de lógica.
> A única diferença é `package gateway` (em vez de `package itau`) e o
> `config.go`/`.env.example` adicionados pra torná-lo independente.

## O que tem aqui

| Arquivo | Conteúdo |
|---|---|
| `itau.go` | Cliente PIX: token (OAuth), `CriarCobranca`, `ObterQRCode`. |
| `boleto.go` | Emissão de boleto (Cash Management V2). |
| `webhook.go` | Parse do webhook de PIX pago (`ParseWebhook`). |
| `config.go` | Carga de token/parâmetros/chaves via variáveis de ambiente. |
| `.env.example` | Todos os parâmetros e chaves documentados. |

## Token, parâmetros e chaves

Tudo vem de variáveis de ambiente (ver `.env.example`). Resumo:

| Env | Papel |
|---|---|
| `ITAU_CLIENT_ID` | Identidade OAuth **e** CN do certificado (têm que bater). |
| `ITAU_CLIENT_SECRET` | O segredo/token do OAuth. |
| `ITAU_API_KEY` | `x-itau-apikey` (opcional; cai no Client ID se vazio). |
| `ITAU_CHAVE_PIX` | Chave PIX que recebe. |
| `ITAU_CERT_PATH` / `ITAU_KEY_PATH` | Par mTLS (.crt/.key). |
| `ITAU_ENV` | `producao` \| `sandbox`. |
| `ITAU_AGENCIA` / `ITAU_CONTA` / `ITAU_CONTA_DAC` / `ITAU_CARTEIRA` … | Dados da conta (boleto). |

O token é obtido e **cacheado em memória** (renovado ~30s antes de expirar).
As credenciais vão **no corpo** do request de token (não Basic auth — o STS do
Itaú rejeita Basic com "CN inválido").

## Uso

```go
import "github.com/uctechnology/api-bitrix24-whatsapp/gateway"

// 1. Cliente a partir do ambiente (ou gateway.New(gateway.Config{...}))
cli, err := gateway.NewFromEnv()
if err != nil { log.Fatal(err) }

// 2. Cobrança PIX (txid: 26–35 chars [a-zA-Z0-9]; valor "123.45")
cob, err := cli.CriarCobranca(ctx, "meupedido00000000000000001", "1.00", "Assinatura X")
qr, _  := cli.ObterQRCode(ctx, cob.Txid)   // qr.EMV, qr.ImagemBase64

// 3. Boleto
bcfg := gateway.BoletoConfigFromEnv()
bol, err := cli.RegistrarBoleto(ctx, bcfg, gateway.EntradaBoleto{
    PagadorTipoDoc: "CNPJ", PagadorDoc: "11620571000145",
    PagadorNome: "Cliente X", ValorCentavos: 100,
    Vencimento: "2026-08-10", SeuNumero: "meupedido1", NossoNumero: "1",
})

// 4. Webhook (o Itaú chama sua URL quando o PIX é pago)
notes, err := gateway.ParseWebhook(corpoDoPost)  // []Notificacao com txid, valor…
```

## Identificando a origem (multi-sistema)

Se mais de um sistema usar a **mesma chave PIX**, o Itaú vê um recebedor só.
Para separar, **prefixe o `txid`** por sistema (ex: `sistemaA-...`, `sistemaB-...`):
o webhook devolve o `txid`, e cada sistema reconcilia/ignora pelo prefixo.
Para separação forte, use uma **chave PIX (ou subconta) por sistema**.

## Segurança

- **Certificados** (`*.crt`/`*.key`) e o `.env` preenchido **nunca** vão pro git.
- O `ITAU_CLIENT_SECRET` vem do ambiente do servidor — nunca em código/log.
- O boot **não** testa o certificado; valide gerando uma cobrança PIX real de
  valor baixo (ex: R$ 1,00).
