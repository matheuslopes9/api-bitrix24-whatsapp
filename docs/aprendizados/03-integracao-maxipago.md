# Integração MaxiPago (Gateway de Pagamento)

Gateway usado para cobrança dos planos. API é **XML por POST**. Liquidação de
cartão/boleto via **Rede**; liquidação de **PIX via Itaú**.

## Endpoints

| Ambiente | URL |
|---|---|
| Sandbox | `https://testapi.maxipago.net/UniversalAPI/postXML` |
| Produção | `https://api.maxipago.net/UniversalAPI/postXML` |

Portal de configuração (sandbox): `https://testportal.maxipago.net/`
Autenticação em cada request: `<verification><merchantId/><merchantKey/></verification>`.
`responseCode 0` = sucesso.

## processorIDs (definidos em [config.go](../../internal/config/config.go))

| Meio | processorID | Fonte |
|---|---|---|
| Cartão (simulador sandbox) | `1` | `MAXIPAGO_PROCESSOR_CARD` |
| Boleto (teste) | `12` | `MAXIPAGO_PROCESSOR_BOLETO` |
| **PIX** | **`206`** | `MAXIPAGO_PROCESSOR_PIX` (confirmado na doc oficial) |

Config editável pela **UI admin** (menu Gateway) — prioridade ao banco
(`billing_config`), com fallback pro env. Ver `effectiveBilling(ctx)` em
[internal/api/billing.go](../../internal/api/billing.go).

## ⚠️ Aprendizado #1: IP allowlist é OBRIGATÓRIO

**Sintoma:** toda transação retornava `DECLINED (código 1)`, mesmo com credenciais
válidas.

**Causa-raiz:** a MaxiPago exige **allowlist de IP**. O IP de saída do servidor
precisa estar cadastrado no portal em **"Configuração de IP's"**.

**Fix:** cadastrar `187.110.174.122/32` (IP de saída do EasyPanel, compartilhado
por prod e homolog) no portal → **Configuração de IP's**. ✅ Feito.

**Lição:** antes de depurar credenciais ou payload, confirme o allowlist de IP. O
`DECLINED (1)` genérico frequentemente é IP bloqueado, não erro de transação. Se o
EasyPanel trocar de IP de saída, quebra de novo.

## ⚠️ Aprendizado #2: PIX é liquidado pelo ITAÚ, não pela Rede

**Descoberta:** no portal, em **Admin → PIX → Credenciais**, o aviso diz:
> "Preencha os dados abaixo com as credenciais informadas pelo Itaú."

Campos exigidos:
- **Tipo de Chave PIX** (E-mail / CNPJ / Telefone / Aleatória)
- **Chave PIX** — da conta Itaú
- **Client ID** — da aplicação PIX do Itaú
- **Token Temporário** — JWT do Itaú (campo grande; **expira em ~5 min**)

**Implicação crítica:** as credenciais PIX **não** vêm da MaxiPago nem da Rede —
vêm do **banco (Itaú)**. É preciso conta PJ no Itaú com **API PIX Recebimentos**
habilitada (escopo `cob.write`). Credenciais de outro sistema (ex: um
`faturamento` que só emite boleto) **não servem** — escopo errado.

**Contato Rede (e-commerce, para habilitar PIX sandbox):**
(11) 4001-4433 / 0800 728 4433 / ecommerce@userede.com.br

**Lição:** PIX via MaxiPago depende de um processo **bancário** externo (Itaú),
com prazo fora do nosso controle. Não travar o projeto nisso — boleto já permite
vender.

## Aprendizado #3: cartão já tem roteamento, mas falta captura PCI-safe

No portal, **Roteamento** → **Roteamento de Cartões** já tem MASTERCARD e VISA
apontando para `SIMULATOR - 1234`. O lado do gateway está pronto.

**O que falta:** a captura segura. Onde o cliente digita o número do cartão define
o risco PCI-DSS:
- **SmartPage / Checkout hospedado** (recomendado): cliente digita no ambiente da
  MaxiPago, número nunca passa pelo nosso servidor. **Não implementado** — aguarda
  confirmação da URL de redirecionamento pelo suporte.
- **Formulário no nosso site**: obriga certificação PCI-DSS. **Recusado** por
  risco/custo.

**Lição:** nunca capture número de cartão passando pelo nosso servidor sem PCI.
Preferir SmartPage. Cartão fica bloqueado até a URL do SmartPage ser confirmada —
mas **PIX e boleto não dependem disso**.

## Fluxo implementado

1. Trial de 7 dias expira → dashboard mostra popup "veja os planos"
2. Cliente escolhe plano → `POST /ui/billing/checkout` com `{plan, method:
   "pix"|"boleto", coupon}`
3. Backend aplica desconto do cupom, cria transação (boleto ou PIX) via XML
4. MaxiPago notifica mudança de status em
   `POST /billing/maxipago/postback`
5. Pagamento confirmado → `SetTenantPlan(domain, plano, active, +N dias)` —
   **liberação automática**, sem intervenção manual

### PIX (struct XML)
```go
type mpPix struct {
    ExpirationTime string `xml:"expirationTime,omitempty"`
    PaymentInfo    string `xml:"paymentInfo,omitempty"`
}
```
Resposta traz `<emv>` (copia-e-cola) + `<imagem_base64>` (QR).

## Pendências MaxiPago (ver [06-pendencias.md](06-pendencias.md))

- [ ] Credencial PIX real do Itaú (Chave + Client ID + Token) — processo bancário
- [ ] Registrar URL de postback no portal:
      `https://uctalk-homolog-connector.omva7z.easypanel.host/billing/maxipago/postback`
- [ ] Confirmar URL do SmartPage com o suporte (destrava cartão)
- [x] IP `187.110.174.122/32` no allowlist
