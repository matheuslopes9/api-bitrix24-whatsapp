# coding: utf-8
"""
Cliente de envio de e-mail — usa o proxy OAuth2 SMTP local.

Este script NÃO fala diretamente com a Microsoft: ele envia o e-mail para o
proxy local (oauth2_smtp_proxy.py), que faz a autenticação OAuth2 com o
Azure AD e reenvia para o Microsoft 365. Por isso, o proxy precisa estar
rodando antes (veja o README).

Uso:
    python send_email.py destino@exemplo.com "Assunto" "Corpo em HTML ou texto"

Sem argumentos, envia um e-mail de teste para EMAIL_RECIPIENT do .env.
"""

import os
import sys
import smtplib
import logging
from email.mime.multipart import MIMEMultipart
from email.mime.text import MIMEText
from dotenv import load_dotenv

load_dotenv()

logging.basicConfig(level=logging.INFO, format="%(asctime)s - %(levelname)s - %(message)s")
logger = logging.getLogger(__name__)

# Aponta para o proxy OAuth2 local (mesma config do sistema original).
SMTP_HOST = os.getenv("SMTP_HOST", "127.0.0.1")
SMTP_PORT = int(os.getenv("SMTP_PORT", "2525"))
EMAIL_SENDER = os.getenv("EMAIL_SENDER", "noreply@uctechnology.com.br")
EMAIL_REPLY_TO = os.getenv("EMAIL_REPLY_TO", EMAIL_SENDER)
EMAIL_RECIPIENT = os.getenv("EMAIL_RECIPIENT", EMAIL_SENDER)


def send_email(to: str, subject: str, html: str) -> None:
    """Envia um e-mail HTML através do proxy OAuth2 local."""
    msg = MIMEMultipart("alternative")
    msg["Subject"] = subject
    msg["From"] = f"UC Technology <{EMAIL_SENDER}>"
    msg["To"] = to
    msg["Reply-To"] = EMAIL_REPLY_TO
    msg.attach(MIMEText(html, "html", "utf-8"))

    with smtplib.SMTP(SMTP_HOST, SMTP_PORT, timeout=15) as smtp:
        smtp.sendmail(EMAIL_SENDER, [to], msg.as_string())
    logger.info(f"E-mail enviado para {to} via proxy {SMTP_HOST}:{SMTP_PORT}")


if __name__ == "__main__":
    if len(sys.argv) >= 4:
        destino, assunto, corpo = sys.argv[1], sys.argv[2], sys.argv[3]
    else:
        destino = EMAIL_RECIPIENT
        assunto = "Teste — OAuth2 Email Service"
        corpo = (
            "<h2>Funcionou! ✅</h2>"
            "<p>Este e-mail foi enviado pelo clone do serviço OAuth2 SMTP "
            "da Microsoft (Azure AD), via proxy local.</p>"
        )
        logger.info(f"Sem argumentos — enviando e-mail de teste para {destino}")

    try:
        send_email(destino, assunto, corpo)
    except Exception as e:
        logger.error(f"Falha ao enviar e-mail: {e}")
        logger.error("O proxy OAuth2 (oauth2_smtp_proxy.py) está rodando?")
        sys.exit(1)
