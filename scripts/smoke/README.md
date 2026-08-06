# Smoke test — valida um deploy

Bateria rápida que roda **contra um deploy no ar** (homolog ou produção) e
confirma que o essencial está de pé, **sem precisar de login admin**. Ideal pra
rodar logo depois de cada deploy no EasyPanel.

## Como rodar

**Windows (PowerShell):**

```powershell
# homolog (default)
./scripts/smoke/smoke.ps1

# outra URL
./scripts/smoke/smoke.ps1 -BaseUrl https://uctalk.uctechnology.com.br
```

**Linux / macOS / CI (bash + curl):**

```bash
./scripts/smoke/smoke.sh                       # homolog (default)
./scripts/smoke/smoke.sh https://SEU-DOMINIO   # outra URL
```

Sai com código **1** se qualquer teste falhar (bom pra pipeline de CI).

> **Rodou e deu FAIL em `POST /billing/itau`?** Essa rota (alias do webhook sem
> `/pix`) entrou no commit `48ad01f`. Se falhar com 404, o deploy ainda está numa
> versão antiga — faça o deploy no EasyPanel e rode de novo.

## O que ele checa

| Grupo | Verificação |
|---|---|
| **Health** | `/health` → 200 e `status=ok` |
| **Rotas públicas** | `/` → 302 `/dashboard`; `/assets/logo.png`, `/planos`, `/welcome`, `/connect`, `/metrics` → 200 |
| **Dashboard** | 404 pra acesso direto anônimo (proteção) **e** 200 quando chamado como iframe Bitrix |
| **Proteção admin** | `/admin` → 302 login; `/admin/api/*` → 401 sem login (não vaza dados) |
| **Webhook Itaú** | `/billing/itau/pix` **e** `/billing/itau` → 200 com payload válido; corpo vazio/lixo → 200 (nunca 500) |

## O que ele **não** cobre (e por quê)

Estas coisas vivem atrás do login admin ou dependem do estado do banco/Itaú —
o smoke não loga nem manipula secrets, então precisam de verificação manual:

- Layout do painel admin (só visível logado).
- Ordem dos planos Trial → Básico → Pro (aba Planos, logado).
- Registro de auditoria dos eventos de tenant.
- **Teste real de PIX/Boleto** no Itaú — use o botão *"Testar PIX (R$1)"* no
  painel (aba Gateway). É o único jeito de exercitar o certificado mTLS ponta a
  ponta; o boot não testa isso.

## Notas

- Não envia nem pede senha de admin. Só verifica que `/admin/*` responde
  401/302 (proteção ativa).
- O webhook usa um `txid` inexistente de propósito: valida que o endpoint
  **aceita e ignora** com segurança, sem ativar nenhum plano de verdade.
