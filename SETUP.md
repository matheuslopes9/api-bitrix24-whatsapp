# Checklist de configuração — WhatsApp ↔ Bitrix24 Connector

## O que você precisa me fornecer para preencher o .env completo

### 1. Bitrix24
- [ ] **Domínio** do seu Bitrix24 (ex: `https://suaempresa.bitrix24.com.br`)
- [ ] **Client ID** do App criado em Bitrix24 (Desenvolvedor → Meus Apps)
- [ ] **Client Secret** do mesmo App
- [ ] **URL pública** do servidor onde o conector vai rodar (para o Redirect URI do OAuth2)
- [ ] **ID do canal Open Lines** (Bitrix24 → Contact Center → Open Lines → ver o ID na URL)

### 2. Servidor
- [ ] **IP ou domínio público** do servidor (ex: `https://connector.suaempresa.com.br`)
- [ ] **Porta** que deseja expor (padrão: 8080)
- [ ] **Secret key** para proteger a API (qualquer string forte)

### 3. Banco / Cache (se não usar o Docker padrão)
- [ ] Senha customizada para o Postgres (ou deixa o padrão do docker-compose)
- [ ] Senha customizada para o Redis (ou deixa o padrão)

### 4. WhatsApp
- [ ] **Números** que serão conectados (formato: 5511999999999)
  - Cada número vira uma sessão independente

---

## O que JÁ está pré-configurado (não precisa mudar)

| Variável | Valor padrão | Observação |
|---|---|---|
| `POSTGRES_HOST` | `postgres` | OK para Docker |
| `POSTGRES_PORT` | `5432` | OK |
| `POSTGRES_USER` | `whatsapp` | OK |
| `POSTGRES_DB` | `whatsapp_bitrix` | OK |
| `REDIS_HOST` | `redis` | OK para Docker |
| `REDIS_PORT` | `6379` | OK |
| `QUEUE_WORKERS` | `20` | OK para até 1k conversas |
| `QUEUE_MAX_RETRY` | `5` | OK |
| `WATCHDOG_PING_INTERVAL_SECS` | `30` | OK |
| `APP_PORT` | `3000` | OK |

---

## Resumo: só preciso de 5 informações críticas

```
1. Domínio Bitrix24:    https://_____.bitrix24.com.br
2. Client ID:           _________________________________
3. Client Secret:       _________________________________
4. URL do seu servidor: https://_________________________
5. ID do Open Lines:    _____ (número inteiro)
```

Me passe essas 5 informações e eu preencho o .env completo na hora.
