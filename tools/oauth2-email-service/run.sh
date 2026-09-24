#!/usr/bin/env bash
# Inicia o proxy OAuth2 SMTP. Cria o venv e instala dependências na 1ª execução.
set -e
cd "$(dirname "$0")"

if [ ! -d "venv" ]; then
  echo "Criando ambiente virtual..."
  python3 -m venv venv
  ./venv/bin/pip install --upgrade pip
  ./venv/bin/pip install -r requirements.txt
fi

echo "Iniciando proxy OAuth2 SMTP..."
exec ./venv/bin/python oauth2_smtp_proxy.py
