# OAuth2 Email Service — Microsoft 365 (Azure AD)

Clone autônomo do serviço de envio de e-mail do Portal UC Technology, usando
autenticação **OAuth2 (client credentials)** com o **Azure AD** para enviar
e-mails pelo **Microsoft 365 / Outlook 365**.

Usa a **mesma configuração** que já funciona em produção.

## Como funciona

```
  send_email.py  ──SMTP(127.0.0.1:2525)──▶  oauth2_smtp_proxy.py  ──OAuth2/XOAUTH2──▶  smtp.office365.com
   (seu código)        (sem senha)            (proxy local)          (token Azure AD)     (Microsoft 365)
```

O `oauth2_smtp_proxy.py` é um servidor SMTP local que:
1. Recebe e-mails comuns por SMTP em `127.0.0.1:2525` (sem autenticação).
2. Obtém um token OAuth2 do Azure AD via **MSAL** (`acquire_token_for_client`,
   escopo `https://outlook.office365.com/.default`), com cache e renovação automática.
3. Reenvia o e-mail para `smtp.office365.com:587` autenticando com **XOAUTH2**.
4. Reescreve o header `From:` para `EMAIL_SENDER`.

Assim, qualquer aplicação envia e-mail "sem senha" para o proxy local, e o
proxy cuida do OAuth2 — sem precisar de senha de aplicativo nem SMTP básico
(que a Microsoft desativou).

## Arquivos

| Arquivo | Função |
|---|---|
| `oauth2_smtp_proxy.py` | O serviço principal (proxy SMTP → OAuth2). |
| `send_email.py` | Cliente de exemplo: envia um e-mail via proxy. |
| `.env` | Configuração (credenciais Azure + e-mails). **Sigiloso.** |
| `requirements.txt` | Dependências Python. |
| `run.sh` | Cria o venv, instala deps e inicia o proxy. |

## Configuração (`.env`)

```
AZURE_TENANT_ID=...        # ID do diretório (tenant) no Azure
AZURE_CLIENT_ID=...        # ID do aplicativo (app registration)
AZURE_CLIENT_SECRET=...    # Segredo do aplicativo
SENDER_EMAIL=...           # Conta que autentica no OAuth2 (caixa real)
EMAIL_SENDER=...           # Endereço que aparece no "From:" (ex.: noreply@...)
EMAIL_REPLY_TO=...         # Reply-To
EMAIL_RECIPIENT=...        # Destinatário padrão do teste
PROXY_HOST=127.0.0.1       # Host do proxy local
PROXY_PORT=2525            # Porta do proxy local
SMTP_HOST=127.0.0.1        # Onde o cliente conecta (= proxy)
SMTP_PORT=2525
```

> ⚠️ O `.env` deste pacote já vem com as credenciais reais de produção.
> Trate o arquivo como **confidencial** e não o versione em repositório público.

### Pré-requisito no Azure AD
O app registration precisa da permissão de aplicativo **`SMTP.SendAsApp`**
(Office 365 Exchange Online), com consentimento de administrador, e a conta
`SENDER_EMAIL` deve ter licença/caixa para envio.

## Como rodar

```bash
# 1) Inicie o proxy (1ª vez cria o venv e instala dependências)
chmod +x run.sh
./run.sh

# 2) Em outro terminal, envie um e-mail de teste
./venv/bin/python send_email.py                       # teste para EMAIL_RECIPIENT
./venv/bin/python send_email.py dest@x.com "Oi" "<b>Olá</b>"
```

Para usar em produção, rode o proxy como serviço (systemd) e faça suas
aplicações enviarem e-mail para `127.0.0.1:2525` (SMTP simples, sem TLS/senha).
