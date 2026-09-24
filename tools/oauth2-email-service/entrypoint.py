#!/usr/bin/env python3
"""Sobe o proxy SMTP e um endpoint HTTP de saude ao lado dele.

POR QUE EXISTE
--------------
O EasyPanel (como quase todo orquestrador) sonda a porta do servico esperando
uma resposta HTTP. O proxy fala SMTP: a sonda conecta, nao entende a resposta,
marca o container como doente e manda SIGTERM. No log isso aparece como o
proxy subindo certo — token OAuth2 obtido, porta escutando — e sendo derrubado
poucos segundos depois, em loop:

    ✅ Proxy iniciado!
    ('127.0.0.1', 50028) EOF received
    🛑 Encerrando proxy...

Este wrapper responde 200 numa porta HTTP separada, entao a sonda passa e o
container fica de pe. O oauth2_smtp_proxy.py NAO foi tocado: ele e' o servico
de e-mail da empresa e roda em outros lugares.

Tambem reinicia o proxy se ele morrer sozinho, com espera entre tentativas —
sem isso, uma queda do lado da Microsoft deixaria o container vivo e mudo,
que e' o pior estado possivel: parece saudavel e nao entrega nada.
"""

import os
import signal
import subprocess
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, HTTPServer

HEALTH_PORT = int(os.getenv("HEALTH_PORT", "80"))

# Estado compartilhado: a saude reflete o proxy, nao o wrapper. Responder 200
# com o proxy morto so' esconderia o problema.
_proxy_vivo = threading.Event()


class Saude(BaseHTTPRequestHandler):
    def do_GET(self):
        ok = _proxy_vivo.is_set()
        self.send_response(200 if ok else 503)
        self.send_header("Content-Type", "text/plain; charset=utf-8")
        self.end_headers()
        self.wfile.write(b"proxy smtp ok\n" if ok else b"proxy smtp fora do ar\n")

    def do_HEAD(self):
        self.do_GET()

    # Silencia o log de acesso: a sonda bate a cada poucos segundos e
    # afogaria as linhas do proxy, que sao as que interessam.
    def log_message(self, *args):
        pass


def servir_saude():
    try:
        HTTPServer(("0.0.0.0", HEALTH_PORT), Saude).serve_forever()
    except Exception as e:  # noqa: BLE001
        print(f"[entrypoint] endpoint de saude falhou: {e}", flush=True)


def main():
    threading.Thread(target=servir_saude, daemon=True).start()
    print(f"[entrypoint] saude HTTP em 0.0.0.0:{HEALTH_PORT}", flush=True)

    proc = None

    def encerrar(sig, frame):
        print("[entrypoint] encerrando", flush=True)
        _proxy_vivo.clear()
        if proc and proc.poll() is None:
            proc.terminate()
            try:
                proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                proc.kill()
        sys.exit(0)

    signal.signal(signal.SIGTERM, encerrar)
    signal.signal(signal.SIGINT, encerrar)

    espera = 5
    while True:
        proc = subprocess.Popen([sys.executable, "-u", "oauth2_smtp_proxy.py"])
        _proxy_vivo.set()
        codigo = proc.wait()
        _proxy_vivo.clear()

        # Saiu sozinho. Credencial errada do Azure cai aqui (o proxy valida no
        # boot e encerra), e nesse caso reiniciar nao resolve — mas o log fica
        # repetindo o motivo, que e' melhor que o container sumir e ninguem
        # ver por que.
        print(f"[entrypoint] proxy encerrou com codigo {codigo}; "
              f"reiniciando em {espera}s", flush=True)
        time.sleep(espera)
        espera = min(espera * 2, 60)


if __name__ == "__main__":
    main()
