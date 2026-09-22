# Fluxo 5 — Licença, contrato e pagamento

Como o UC Talk decide o que cada cliente pode usar, e como o pagamento entra
no sistema.

## O modelo

O UC Talk **não é mais SaaS de marketplace**. Não há trial, plano Básico/Pro,
cupom nem cobrança online. Quem instala é a UC Technology, direto no portal do
cliente, e o cliente paga pelo comercial.

Duas tabelas sustentam isso:

```
 tenant_licenses           o que o contrato libera, por cliente
   ├─ max_sessions         quantos números WhatsApp
   ├─ feat_cloud_api       Cloud API (Meta) + Templates
   ├─ feat_automations     robôs BizProc
   ├─ feat_reports         Relatórios
   ├─ feat_sms             existe no código, oculto na UI
   └─ valid_until          NULL = sem prazo

 license_payments          lançado à mão pelo suporte
   ├─ paid_at              quando o cliente pagou
   ├─ covers_until         até quando libera o uso
   └─ recorded_by          quem lançou (auditoria)
```

## Por que os benefícios vivem na licença

O modelo antigo guardava o **código** do plano no cliente
(`tenant_plans.plan = 'pro'`) e as flags de feature numa **segunda** tabela,
`plan_definitions`. O gate cruzava as duas:

```
 tenant_plans.plan = 'pro'  ──►  plan_definitions['pro'].feat_templates
                                  ▲
                                  └── reescrita inteira a cada save pela UI
```

Bastava o catálogo dessincronizar para o cliente ficar **rotulado Pro e sem
nenhuma feature** — era o bug do "o Pro não funciona, cliente continua trial".

Hoje é uma leitura só, sem catálogo intermediário. Não há o que
dessincronizar, e o contrato de cada cliente é seguido literalmente em vez de
forçar todo mundo em dois pacotes.

## Vencimento avisa, não bloqueia

```
 valid_until passou
      │
      ├─► painel do cliente mostra "RENOVAR"
      ├─► admin marca a licença como vencida
      ├─► job diário avisa o financeiro
      └─► o app CONTINUA FUNCIONANDO
```

É decisão de produto: inadimplência não derruba o atendimento do cliente
final. Quem cobra é o comercial.

Isso removeu dois gates do modelo antigo. O de
[handlers.go](../../internal/api/handlers.go) era o pior — devolvia `200` pro
Bitrix e **descartava a mensagem em silêncio**: o operador via como enviada e
o cliente nunca recebia. Descartar mensagem sem sinal é o pior jeito possível
de comunicar inadimplência.

Os gates que sobraram devolvem **403**, não o `402 Payment Required` de antes:
não existe mais auto-serviço para o cliente destravar sozinho. O recurso
simplesmente não faz parte do contrato dele.

## Registrar um pagamento

```
 suporte abre Licenças → "Registrar pagamento"
      │  pago em / libera até / valor / forma
      ▼
 POST /admin/api/license/payment
      │
      ├─ grava em license_payments (recorded_by = quem está logado)
      └─ estende tenant_licenses.valid_until
           GREATEST(valid_until, covers_until)
```

As duas escritas vão na **mesma transação**: um pagamento registrado que não
estendesse a licença deixaria o cliente marcado como vencido tendo pago, e o
financeiro seria avisado à toa.

O `GREATEST` existe para que lançar um pagamento atrasado (conciliação fora de
ordem) não **encurte** uma licença que já vai mais longe.

`recorded_by` vem do operador logado, nunca do corpo da requisição — quem
registrou é fato de auditoria, não algo que o chamador escolhe.

## Aviso para o financeiro

O financeiro **não tem login**. Recebe aviso em dois momentos:

| Gatilho | Quando |
|---|---|
| `vencendo` | faltam 7 dias ou menos |
| `vencida` | passou da data e não houve pagamento novo |

Job diário em [license_notify.go](../../internal/api/license_notify.go). O
transporte é plugável (`LicenseNotifier`) porque a UC Technology já tem
sistema de envio próprio — a implementação atual registra em `WARN`, que
aparece na aba "Logs ao vivo" do admin.

**A deduplicação usa a data de vencimento como parte da chave**
(`domain + kind + ref_date`). É o que permite avisar de novo depois que um
pagamento avança a vigência: com chave só por cliente e tipo, um cliente
avisado uma vez nunca mais seria.

## Papéis

| | Administrador | Suporte |
|---|---|---|
| Clientes, saúde, logs, ferramentas | ✅ | ✅ |
| Licenças e pagamentos | ✅ | ✅ |
| Ver quem tem acesso admin | ✅ | ✅ |
| Criar / desativar / remover usuário admin | ✅ | ❌ |
| Purge de portal, cleanup de arquivos, flush de fila | ✅ | ❌ |

Criar usuário é exclusivo do Administrador **de propósito**: se o suporte
pudesse criar um usuário `superadmin`, ele se promoveria e toda a separação
viraria decorativa. Listar continua liberado — ver quem tem acesso faz parte
do diagnóstico.

## Consultas de diagnóstico

```sql
-- Algum cliente instalado sem licença? Deve voltar VAZIO.
SELECT p.domain FROM bitrix_portals p
  LEFT JOIN tenant_licenses l ON l.domain = p.domain
 WHERE l.domain IS NULL AND p.domain <> p.member_id;

-- Quem precisa de renovação
SELECT domain, valid_until, (valid_until - CURRENT_DATE) AS dias
  FROM tenant_licenses
 WHERE valid_until IS NOT NULL AND valid_until <= CURRENT_DATE + 7
 ORDER BY valid_until;

-- Avisos já enviados (por que o financeiro não recebeu de novo)
SELECT domain, kind, ref_date, sent_at FROM license_notifications
 ORDER BY sent_at DESC LIMIT 20;
```
