# coding: utf-8
"""
OAuth2 SMTP Proxy - Microsoft Azure AD
Proxy local que converte autenticação SMTP básica em OAuth2 para Microsoft 365
Versão: 2.0 - Com suporte a FROM customizado
"""

import smtplib
import base64
import os
import sys
import logging
import re
import time
from datetime import datetime, timedelta
from aiosmtpd.controller import Controller
from dotenv import load_dotenv
import msal
import signal

# Carregar variáveis de ambiente
load_dotenv()

# ========================================
# CONFIGURAÇÕES
# ========================================
AZURE_TENANT_ID = os.getenv('AZURE_TENANT_ID')
AZURE_CLIENT_ID = os.getenv('AZURE_CLIENT_ID')
AZURE_CLIENT_SECRET = os.getenv('AZURE_CLIENT_SECRET')
SENDER_EMAIL = os.getenv('SENDER_EMAIL', 'suporte@uctechnology.com.br')
FROM_EMAIL = os.getenv('EMAIL_SENDER', 'noreply@uctechnology.com.br')
PROXY_HOST = os.getenv('PROXY_HOST', '127.0.0.1')
PROXY_PORT = int(os.getenv('PROXY_PORT', '2525'))

# Logger primeiro, para que a validação abaixo já possa usar.
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(levelname)s - %(message)s'
)

# Validar configurações — falha cedo com mensagem clara.
if not all([AZURE_TENANT_ID, AZURE_CLIENT_ID, AZURE_CLIENT_SECRET]):
    logging.critical("Configure AZURE_TENANT_ID, AZURE_CLIENT_ID e AZURE_CLIENT_SECRET no .env!")
    sys.exit(1)
logger = logging.getLogger(__name__)


# ========================================
# GERENCIADOR DE TOKENS OAUTH2
# ========================================
class OAuth2TokenManager:
    """Gerencia tokens OAuth2 com cache e renovação automática"""
    
    def __init__(self):
        self.token = None
        self.expires_at = None
        self.authority = f"https://login.microsoftonline.com/{AZURE_TENANT_ID}"
        self.scope = ["https://outlook.office365.com/.default"]
        
        self.app = msal.ConfidentialClientApplication(
            AZURE_CLIENT_ID,
            authority=self.authority,
            client_credential=AZURE_CLIENT_SECRET,
        )
    
    def get_access_token(self):
        """Obtém token OAuth2 (usa cache ou renova se expirado)"""
        
        # Verificar se token ainda é válido
        if self.token and self.expires_at and datetime.now() < self.expires_at:
            return self.token
        
        # Obter novo token
        logger.info("Obtendo novo token OAuth2...")
        
        result = self.app.acquire_token_for_client(scopes=self.scope)
        
        if "access_token" in result:
            self.token = result["access_token"]
            expires_in = result.get("expires_in", 3600)
            self.expires_at = datetime.now() + timedelta(seconds=expires_in - 300)
            logger.info("✓ Token OAuth2 obtido com sucesso!")
            return self.token
        else:
            error = result.get("error_description", result.get("error", "Erro desconhecido"))
            logger.error(f"❌ Falha ao obter token: {error}")
            raise Exception(f"Falha na autenticação OAuth2: {error}")


# ========================================
# HANDLER SMTP
# ========================================
class OAuth2SMTPHandler:
    """Handler que processa e-mails recebidos via SMTP e reenvia via OAuth2"""
    
    def __init__(self):
        self.token_manager = OAuth2TokenManager()
        self.email_count = 0
    
    async def handle_DATA(self, server, session, envelope):
        """Processa o conteúdo do e-mail e envia via OAuth2"""
        
        self.email_count += 1
        recipients = envelope.rcpt_tos
        
        logger.info(f"📧 Email #{self.email_count} de {envelope.mail_from} para {recipients}")
        
        try:
            # Obter token OAuth2
            token = self.token_manager.get_access_token()
            
            # Conectar ao SMTP da Microsoft
            smtp = smtplib.SMTP('smtp.office365.com', 587, timeout=30)
            smtp.set_debuglevel(0)
            smtp.ehlo()
            smtp.starttls()
            smtp.ehlo()
            
            # Autenticar com XOAUTH2
            auth_string = f"user={SENDER_EMAIL}\x01auth=Bearer {token}\x01\x01"
            auth_bytes = base64.b64encode(auth_string.encode()).decode()
            
            code, response = smtp.docmd('AUTH', f'XOAUTH2 {auth_bytes}')
            
            if code not in (235, 250):  # 235 = Auth success, 250 = OK
                raise Exception(f"Falha na autenticação: {code} - {response}")
            
            # Processar mensagem
            message_content = envelope.content.decode('utf-8', errors='replace')
            
            # Substituir FROM no header
            message_content = re.sub(
                r'^From:.*$',
                f'From: {FROM_EMAIL}',
                message_content,
                flags=re.MULTILINE,
                count=1
            )
            
            # Enviar e-mail
            smtp.sendmail(
                SENDER_EMAIL,  # Autenticação
                recipients,
                message_content.encode('utf-8')
            )
            
            smtp.quit()
            
            logger.info(f"✅ Email #{self.email_count} enviado com sucesso!")
            return '250 Message accepted for delivery'
            
        except Exception as e:
            logger.error(f"❌ Erro: {e}")
            return f'451 Temporary failure: {str(e)}'


# ========================================
# FUNÇÃO PRINCIPAL
# ========================================
def run_proxy():
    """Inicia o proxy SMTP OAuth2"""
    
    logger.info("")
    logger.info("="*60)
    logger.info("    OAUTH2 SMTP PROXY - MICROSOFT AZURE AD")
    logger.info("="*60)
    logger.info("")
    logger.info(f"📧 E-mail autenticado (OAuth2): {SENDER_EMAIL}")
    logger.info(f"📤 E-mail remetente (FROM):     {FROM_EMAIL}")
    logger.info(f"🔌 Proxy SMTP rodando em:       {PROXY_HOST}:{PROXY_PORT}")
    logger.info("")
    
    # Criar handler
    handler = OAuth2SMTPHandler()
    
    # Obter token inicial para validar credenciais
    try:
        handler.token_manager.get_access_token()
    except Exception as e:
        logger.error(f"❌ Falha ao obter token inicial: {e}")
        logger.error("Verifique as credenciais do Azure AD no .env")
        sys.exit(1)
    
    # Configurar controller
    controller = Controller(
        handler,
        hostname=PROXY_HOST,
        port=PROXY_PORT
    )
    
    # Função para shutdown gracioso
    def signal_handler(sig, frame):
        logger.info("\n🛑 Encerrando proxy...")
        controller.stop()
        sys.exit(0)
    
    signal.signal(signal.SIGINT, signal_handler)
    signal.signal(signal.SIGTERM, signal_handler)
    
    # Iniciar servidor
    controller.start()
    logger.info("✅ Proxy iniciado! Pressione Ctrl+C para parar.")
    logger.info("")
    
    # Manter rodando
    try:
        while True:
            time.sleep(1)
    except KeyboardInterrupt:
        pass
    finally:
        controller.stop()


# ========================================
# PONTO DE ENTRADA
# ========================================
if __name__ == "__main__":
    run_proxy()