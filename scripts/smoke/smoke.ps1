<#
.SYNOPSIS
  Smoke test do UC Talk — valida um deploy (homolog ou producao) sem precisar
  de login admin. Bate nas rotas publicas, confirma que o admin esta trancado
  e que o webhook do Itau aguenta payloads bons e ruins.

.EXAMPLE
  # homolog (default)
  ./scripts/smoke/smoke.ps1

.EXAMPLE
  # outra URL
  ./scripts/smoke/smoke.ps1 -BaseUrl https://uctalk.uctechnology.com.br

.NOTES
  Nao envia nem pede senha de admin. So verifica que /admin/* responde
  401/302 (protecao ativa) — nunca tenta logar.
#>
param(
  [string]$BaseUrl = "https://uctalk-homolog-connector.omva7z.easypanel.host"
)

$ErrorActionPreference = "Stop"
$BaseUrl = $BaseUrl.TrimEnd("/")
$pass = 0; $fail = 0; $warn = 0

# HTTP helper via HttpWebRequest — funciona igual em PS 5.1 e PS 7. Nao segue
# redirects (pra checar 302) e nunca lanca em 3xx/4xx/5xx: le o status direto
# da resposta OU da WebException. Devolve @{ Code; Body; Location }.
function Invoke-Check {
  param([string]$Method, [string]$Path, [hashtable]$Headers, [string]$Body)
  $req = [System.Net.HttpWebRequest]::Create("$BaseUrl$Path")
  $req.Method = $Method
  $req.AllowAutoRedirect = $false
  $req.Timeout = 20000
  $req.UserAgent = "uctalk-smoke/1.0"
  if ($Headers) { foreach ($k in $Headers.Keys) { $req.Headers[$k] = $Headers[$k] } }
  if ($Body) {
    $req.ContentType = "application/json"
    $bytes = [System.Text.Encoding]::UTF8.GetBytes($Body)
    $req.ContentLength = $bytes.Length
    $rs = $req.GetRequestStream(); $rs.Write($bytes, 0, $bytes.Length); $rs.Close()
  }
  $resp = $null
  try {
    $resp = $req.GetResponse()          # 2xx/3xx
  } catch [System.Net.WebException] {
    $resp = $_.Exception.Response       # 4xx/5xx trazem a resposta aqui
    if (-not $resp) { return @{ Code = 0; Body = $_.Exception.Message; Location = $null } }
  }
  $code = [int]$resp.StatusCode
  $loc  = $resp.Headers["Location"]
  $body = ""
  try {
    $sr = New-Object System.IO.StreamReader($resp.GetResponseStream())
    $body = $sr.ReadToEnd(); $sr.Close()
  } catch {}
  $resp.Close()
  return @{ Code = $code; Body = $body; Location = $loc }
}

function Ok  ($msg) { Write-Host "  [ OK ] $msg"   -ForegroundColor Green;  $script:pass++ }
function Bad ($msg) { Write-Host "  [FAIL] $msg"   -ForegroundColor Red;    $script:fail++ }
function Wn  ($msg) { Write-Host "  [WARN] $msg"   -ForegroundColor Yellow; $script:warn++ }
function Section($t){ Write-Host ""; Write-Host "== $t ==" -ForegroundColor Cyan }

Write-Host "Smoke test -> $BaseUrl" -ForegroundColor White

# ── 1. Health ────────────────────────────────────────────────────────────
Section "Health"
$r = Invoke-Check GET "/health"
if ($r.Code -eq 200 -and $r.Body -match '"status"\s*:\s*"ok"') {
  Ok "/health 200 e status=ok"
} else {
  Bad "/health esperava 200+status=ok, veio $($r.Code) :: $($r.Body)"
}

# ── 2. Rotas publicas ────────────────────────────────────────────────────
Section "Rotas publicas"
$r = Invoke-Check GET "/"
if ($r.Code -eq 302 -and "$($r.Location)" -match "/dashboard") { Ok "/ redireciona pra /dashboard" }
else { Bad "/ esperava 302->/dashboard, veio $($r.Code) loc=$($r.Location)" }

$r = Invoke-Check GET "/assets/logo.png"
if ($r.Code -eq 200) { Ok "/assets/logo.png 200 (logo do layout carrega)" }
else { Bad "/assets/logo.png esperava 200, veio $($r.Code)" }

foreach ($p in @("/planos", "/welcome", "/connect", "/metrics")) {
  $r = Invoke-Check GET $p
  if ($r.Code -eq 200) { Ok "$p 200" } else { Bad "$p esperava 200, veio $($r.Code)" }
}

# /dashboard: 404 anonimo (protecao), 200 como iframe/Bitrix.
$r = Invoke-Check GET "/dashboard"
if ($r.Code -eq 404) { Ok "/dashboard bloqueia acesso direto (404, protecao anti-acesso-direto)" }
else { Wn "/dashboard anonimo veio $($r.Code) (esperado 404)" }

$r = Invoke-Check GET "/dashboard" @{ "Sec-Fetch-Dest" = "iframe" }
if ($r.Code -eq 200) { Ok "/dashboard 200 quando chamado como iframe Bitrix" }
else { Bad "/dashboard como iframe esperava 200, veio $($r.Code)" }

# ── 3. Protecao do admin (sem login) ─────────────────────────────────────
Section "Protecao do admin (sem login)"
$r = Invoke-Check GET "/admin"
if ($r.Code -eq 302 -and "$($r.Location)" -match "/admin/login") { Ok "/admin redireciona pro login" }
else { Bad "/admin esperava 302->/admin/login, veio $($r.Code) loc=$($r.Location)" }

foreach ($p in @("/api/tenants", "/api/itau-status", "/api/metrics")) {
  $r = Invoke-Check GET "/admin$p"
  if ($r.Code -eq 401) { Ok "GET /admin$p bloqueado (401)" }
  else { Bad "GET /admin$p esperava 401 sem login, veio $($r.Code)" }
}
$r = Invoke-Check POST "/admin/api/itau-test?method=pix"
if ($r.Code -eq 401) { Ok "POST /admin/api/itau-test bloqueado (401)" }
else { Bad "POST /admin/api/itau-test esperava 401, veio $($r.Code)" }

# ── 4. Webhook Itau (robustez) ───────────────────────────────────────────
Section "Webhook Itau"
$good = '{"pix":[{"endToEndId":"E00000000202601010000smoke","txid":"txid-inexistente-smoke-000000","valor":1.00,"horario":"2026-01-01T12:00:00Z","chave":"smoke@uc.com"}]}'
foreach ($route in @("/billing/itau/pix", "/billing/itau")) {
  $r = Invoke-Check POST $route @{} $good
  if ($r.Code -eq 200) { Ok "POST $route 200 (payload valido, txid inexistente ignorado)" }
  else { Bad "POST $route esperava 200, veio $($r.Code)" }
}
# Corpo vazio e lixo NUNCA devem dar 500 (senao o Itau re-tenta em loop).
$r = Invoke-Check POST "/billing/itau/pix" @{} '{}'
if ($r.Code -eq 200) { Ok "POST /billing/itau/pix corpo vazio -> 200 (nao quebra)" }
else { Bad "corpo vazio esperava 200, veio $($r.Code)" }
$r = Invoke-Check POST "/billing/itau/pix" @{} 'nao-e-json'
if ($r.Code -eq 200) { Ok "POST /billing/itau/pix corpo lixo -> 200 (nao quebra)" }
else { Bad "corpo lixo esperava 200, veio $($r.Code)" }

# ── Resumo ───────────────────────────────────────────────────────────────
Write-Host ""
Write-Host ("Resultado: {0} OK, {1} WARN, {2} FAIL" -f $pass, $warn, $fail) -ForegroundColor White
if ($fail -gt 0) { exit 1 } else { exit 0 }
