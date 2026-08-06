#!/usr/bin/env bash
# Smoke test do UC Talk — valida um deploy (homolog/producao) sem login admin.
# Bate nas rotas publicas, confirma que o admin esta trancado e que o webhook
# do Itau aguenta payloads bons e ruins. Nao envia nem pede senha de admin.
#
# Uso:
#   ./scripts/smoke/smoke.sh                       # homolog (default)
#   ./scripts/smoke/smoke.sh https://SEU-DOMINIO   # outra URL
#
# Requer: curl. Sai com codigo 1 se qualquer teste FAIL.

set -u
BASE="${1:-https://uctalk-homolog-connector.omva7z.easypanel.host}"
BASE="${BASE%/}"
pass=0; fail=0; warn=0
BODY=""
BODY_FILE="$(mktemp 2>/dev/null || echo /tmp/smoke_body.$$)"
trap 'rm -f "$BODY_FILE"' EXIT

green() { printf '  \033[32m[ OK ]\033[0m %s\n' "$1"; pass=$((pass+1)); }
red()   { printf '  \033[31m[FAIL]\033[0m %s\n' "$1"; fail=$((fail+1)); }
yellow(){ printf '  \033[33m[WARN]\033[0m %s\n' "$1"; warn=$((warn+1)); }
section(){ printf '\n\033[36m== %s ==\033[0m\n' "$1"; }

# req PATH [METHOD] [DATA]: faz a chamada, grava o corpo em $BODY_FILE e imprime
# "HTTP<sp>LOCATION". Le o corpo depois com: BODY=$(cat "$BODY_FILE").
# (Nao exportamos BODY direto porque $(req ...) roda em subshell e a var se
# perderia — por isso o corpo vai pra um arquivo lido pelo processo pai.)
req() {
  local method="${2:-GET}" path="$1" data="${3:-}"
  if [ -n "$data" ]; then
    curl -s -o "$BODY_FILE" -w '%{http_code} %{redirect_url}' \
      -X "$method" -H 'Content-Type: application/json' -d "$data" \
      --max-time 20 "$BASE$path"
  else
    curl -s -o "$BODY_FILE" -w '%{http_code} %{redirect_url}' \
      -X "$method" --max-time 20 "$BASE$path"
  fi
}

printf 'Smoke test -> %s\n' "$BASE"

# ── 1. Health ──────────────────────────────────────────────────────────────
section "Health"
read -r code _ <<<"$(req /health)"; BODY=$(cat "$BODY_FILE")
if [ "$code" = "200" ] && echo "$BODY" | grep -q '"status"[[:space:]]*:[[:space:]]*"ok"'; then
  green "/health 200 e status=ok"
else red "/health esperava 200+status=ok, veio $code :: $BODY"; fi

# ── 2. Rotas publicas ──────────────────────────────────────────────────────
section "Rotas publicas"
read -r code loc <<<"$(req /)"
case "$loc" in *"/dashboard") [ "$code" = "302" ] && green "/ redireciona pra /dashboard" || red "/ veio $code loc=$loc";; *) red "/ esperava 302->/dashboard, veio $code loc=$loc";; esac

read -r code _ <<<"$(req /assets/logo.png)"
[ "$code" = "200" ] && green "/assets/logo.png 200 (logo do layout carrega)" || red "/assets/logo.png esperava 200, veio $code"

for p in /planos /welcome /connect /metrics; do
  read -r code _ <<<"$(req "$p")"
  [ "$code" = "200" ] && green "$p 200" || red "$p esperava 200, veio $code"
done

read -r code _ <<<"$(req /dashboard)"
[ "$code" = "404" ] && green "/dashboard bloqueia acesso direto (404, protecao)" || yellow "/dashboard anonimo veio $code (esperado 404)"

code=$(curl -s -o /dev/null -w '%{http_code}' -H 'Sec-Fetch-Dest: iframe' --max-time 20 "$BASE/dashboard")
[ "$code" = "200" ] && green "/dashboard 200 quando chamado como iframe Bitrix" || red "/dashboard como iframe esperava 200, veio $code"

# ── 3. Protecao do admin (sem login) ───────────────────────────────────────
section "Protecao do admin (sem login)"
read -r code loc <<<"$(req /admin)"
case "$loc" in *"/admin/login") [ "$code" = "302" ] && green "/admin redireciona pro login" || red "/admin veio $code";; *) red "/admin esperava 302->/admin/login, veio $code loc=$loc";; esac

for p in /api/tenants /api/itau-status /api/metrics; do
  read -r code _ <<<"$(req "/admin$p")"
  [ "$code" = "401" ] && green "GET /admin$p bloqueado (401)" || red "GET /admin$p esperava 401, veio $code"
done
read -r code _ <<<"$(req '/admin/api/itau-test?method=pix' POST)"
[ "$code" = "401" ] && green "POST /admin/api/itau-test bloqueado (401)" || red "POST /admin/api/itau-test esperava 401, veio $code"

# ── 4. Webhook Itau (robustez) ─────────────────────────────────────────────
section "Webhook Itau"
GOOD='{"pix":[{"endToEndId":"E00000000202601010000smoke","txid":"txid-inexistente-smoke-000000","valor":1.00,"horario":"2026-01-01T12:00:00Z","chave":"smoke@uc.com"}]}'
for route in /billing/itau/pix /billing/itau; do
  read -r code _ <<<"$(req "$route" POST "$GOOD")"
  [ "$code" = "200" ] && green "POST $route 200 (payload valido, txid inexistente ignorado)" || red "POST $route esperava 200, veio $code"
done
read -r code _ <<<"$(req /billing/itau/pix POST '{}')"
[ "$code" = "200" ] && green "POST /billing/itau/pix corpo vazio -> 200" || red "corpo vazio esperava 200, veio $code"
read -r code _ <<<"$(req /billing/itau/pix POST 'nao-e-json')"
[ "$code" = "200" ] && green "POST /billing/itau/pix corpo lixo -> 200" || red "corpo lixo esperava 200, veio $code"

# ── Resumo ─────────────────────────────────────────────────────────────────
printf '\nResultado: %d OK, %d WARN, %d FAIL\n' "$pass" "$warn" "$fail"
[ "$fail" -gt 0 ] && exit 1 || exit 0
