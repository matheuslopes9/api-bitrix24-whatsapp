# Proxy SMTP OAuth2 — Microsoft 365 (Azure AD)
#
# Sobe como SERVICO separado. O UC Talk fala SMTP simples com ele pela rede
# interna, e o proxy resolve o XOAUTH2 com a Microsoft. Nenhuma credencial do
# Azure entra no container do UC Talk — e' a razao de existirem dois.
#
# ATENCAO A DUAS COISAS, que sao a causa dos dois erros comuns:
#
#   1. PROXY_HOST tem que ser 0.0.0.0. O default do codigo e' 127.0.0.1, que
#      em container so' aceita conexao de dentro DELE MESMO — o UC Talk, em
#      outro container, leva "connection refused".
#
#   2. A porta aqui tem que bater com SMTP_PORT do UC Talk. O README fala em
#      2525, mas o .env de producao usa 2526. Divergir da' "no such host" ou
#      timeout, dependendo de qual dos dois estiver errado.
FROM python:3.12-slim

WORKDIR /app

# Dependencias primeiro: camada cacheada, rebuild so' quando elas mudam.
COPY requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt

COPY oauth2_smtp_proxy.py entrypoint.py ./

# Escuta em todas as interfaces. Sem isto o servico sobe "saudavel" e mesmo
# assim e' inalcancavel — falha silenciosa que custa caro pra diagnosticar.
ENV PROXY_HOST=0.0.0.0 \
    PROXY_PORT=2526 \
    HEALTH_PORT=80 \
    PYTHONUNBUFFERED=1

# 2526 = SMTP, so' rede interna. 80 = saude HTTP, que e' o que a sonda do
# orquestrador espera encontrar. Sem a segunda, a sonda bate no SMTP, nao
# entende a resposta e manda SIGTERM — o container reinicia em loop com o
# proxy funcionando perfeitamente.
EXPOSE 2526 80

# O .env NAO entra na imagem de proposito: ele carrega o client_secret do
# Azure AD. As variaveis vem do ambiente do servico (EasyPanel), que e' onde
# segredo deve morar.
CMD ["python", "-u", "entrypoint.py"]
