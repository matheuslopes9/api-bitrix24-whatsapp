package api

// Pagina interna de stress test do webhook /bitrix/connector/event.
// Dispara N requests concorrentes para o próprio processo (loopback)
// simulando o payload form-encoded do ONIMCONNECTORMESSAGEADD do Bitrix.

import (
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
)

// stressTestPage serve HTML estático com form + JS que dispara o teste.
func (h *handlers) stressTestPage(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/html; charset=utf-8")
	return c.SendString(stressTestHTML)
}

// stressTestConnectors retorna a lista de conectores cadastrados (QR + Cloud)
// para popular o dropdown da página. Mostra connector_id, line, session_jid e tipo.
func (h *handlers) stressTestConnectors(c *fiber.Ctx) error {
	accts, err := h.repo.ListBitrixAccounts(c.Context())
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	out := make([]fiber.Map, 0, len(accts))
	for _, a := range accts {
		kind := "qr"
		if strings.HasPrefix(a.ConnectorID, "wa_cloud_") {
			kind = "cloud"
		}
		out = append(out, fiber.Map{
			"connector_id": a.ConnectorID,
			"line":         a.OpenLineID,
			"session_jid":  a.SessionJID,
			"domain":       a.Domain,
			"kind":         kind,
		})
	}
	return c.JSON(fiber.Map{"connectors": out})
}

// stressTestRun executa o teste e retorna métricas em JSON.
// Body JSON: {"concurrent":50,"msgs_per_conv":1,"connector":"...","line":220,"timeout_sec":30}
func (h *handlers) stressTestRun(c *fiber.Ctx) error {
	var req struct {
		Concurrent  int    `json:"concurrent"`
		MsgsPerConv int    `json:"msgs_per_conv"`
		Connector   string `json:"connector"`
		Line        int    `json:"line"`
		TimeoutSec  int    `json:"timeout_sec"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid body: " + err.Error()})
	}
	if req.Concurrent <= 0 {
		req.Concurrent = 50
	}
	if req.Concurrent > 500 {
		return c.Status(400).JSON(fiber.Map{"error": "concurrent acima de 500 — risco de travar o serviço"})
	}
	if req.MsgsPerConv <= 0 {
		req.MsgsPerConv = 1
	}
	if req.MsgsPerConv > 50 {
		return c.Status(400).JSON(fiber.Map{"error": "msgs_per_conv acima de 50 — reduza"})
	}
	if req.Connector == "" {
		return c.Status(400).JSON(fiber.Map{"error": "connector obrigatório"})
	}
	if req.Line == 0 {
		req.Line = 220
	}
	if req.TimeoutSec <= 0 {
		req.TimeoutSec = 30
	}

	// Endpoint sempre loopback — o handler está no mesmo processo.
	endpoint := "http://127.0.0.1:" + h.cfg.App.Port + "/bitrix/connector/event"

	type result struct {
		latency time.Duration
		status  int
		errMsg  string
	}
	client := &http.Client{Timeout: time.Duration(req.TimeoutSec) * time.Second}
	total := req.Concurrent * req.MsgsPerConv
	resultsCh := make(chan result, total)

	startAll := time.Now()
	var wg sync.WaitGroup
	imChatBase := 9000 + rand.Intn(100000) // evita colisão entre runs

	for i := 0; i < req.Concurrent; i++ {
		wg.Add(1)
		go func(convIdx int) {
			defer wg.Done()
			clientPhone := fmt.Sprintf("5519%07d", 9000000+convIdx)
			chatID := clientPhone + "@s.whatsapp.net"
			imChatID := imChatBase + convIdx

			for m := 0; m < req.MsgsPerConv; m++ {
				imMsgID := 800000 + convIdx*1000 + m + rand.Intn(99)
				body := buildStressFormBody(req.Connector, req.Line, chatID, imChatID, imMsgID,
					fmt.Sprintf("Stress conv %d msg %d %d", convIdx, m, rand.Intn(99999)))
				start := time.Now()
				status, errStr := postStressForm(client, endpoint, body)
				latency := time.Since(start)
				resultsCh <- result{latency: latency, status: status, errMsg: errStr}
			}
		}(i)
	}

	wg.Wait()
	close(resultsCh)
	elapsed := time.Since(startAll)

	var latencies []time.Duration
	successCount, failCount, errCount := 0, 0, 0
	statusCounts := map[int]int{}
	var firstErrors []string
	for r := range resultsCh {
		if r.errMsg != "" {
			errCount++
			if len(firstErrors) < 5 {
				firstErrors = append(firstErrors, r.errMsg)
			}
			continue
		}
		latencies = append(latencies, r.latency)
		statusCounts[r.status]++
		if r.status >= 200 && r.status < 300 {
			successCount++
		} else {
			failCount++
		}
	}

	resp := fiber.Map{
		"elapsed_ms":    elapsed.Milliseconds(),
		"total":         total,
		"success":       successCount,
		"fail_http":     failCount,
		"errors":        errCount,
		"first_errors":  firstErrors,
		"throughput":    float64(total) / elapsed.Seconds(),
		"status_counts": statusCounts,
	}
	if len(latencies) > 0 {
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		var sum time.Duration
		for _, l := range latencies {
			sum += l
		}
		idxP50 := len(latencies) * 50 / 100
		idxP95 := len(latencies) * 95 / 100
		idxP99 := len(latencies) * 99 / 100
		if idxP99 >= len(latencies) {
			idxP99 = len(latencies) - 1
		}
		resp["latency_ms"] = fiber.Map{
			"min": latencies[0].Milliseconds(),
			"avg": (sum / time.Duration(len(latencies))).Milliseconds(),
			"p50": latencies[idxP50].Milliseconds(),
			"p95": latencies[idxP95].Milliseconds(),
			"p99": latencies[idxP99].Milliseconds(),
			"max": latencies[len(latencies)-1].Milliseconds(),
		}
	}
	return c.JSON(resp)
}

func buildStressFormBody(connector string, line int, chatID string, imChatID, imMsgID int, text string) string {
	v := url.Values{}
	v.Set("event", "ONIMCONNECTORMESSAGEADD")
	v.Set("event_handler_id", "stress")
	v.Set("data[CONNECTOR]", connector)
	v.Set("data[LINE]", strconv.Itoa(line))
	v.Set("data[MESSAGES][0][im][chat_id]", strconv.Itoa(imChatID))
	v.Set("data[MESSAGES][0][im][message_id]", strconv.Itoa(imMsgID))
	v.Set("data[MESSAGES][0][message][user_id]", "80")
	v.Set("data[MESSAGES][0][message][text]", text)
	v.Set("data[MESSAGES][0][chat][id]", chatID)
	v.Set("ts", strconv.FormatInt(time.Now().Unix(), 10))
	v.Set("auth[access_token]", "stress")
	v.Set("auth[domain]", "stress.local")
	v.Set("auth[member_id]", "stress")
	v.Set("auth[application_token]", "stress")
	v.Set("auth[user_id]", "80")
	return v.Encode()
}

func postStressForm(client *http.Client, endpoint, body string) (int, string) {
	req, err := http.NewRequest("POST", endpoint, strings.NewReader(body))
	if err != nil {
		return 0, err.Error()
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, ""
}

const stressTestHTML = `<!doctype html>
<html lang="pt-br">
<head>
<meta charset="utf-8">
<title>Stress Test — UC Talk</title>
<style>
  body { font-family: -apple-system, system-ui, sans-serif; max-width: 900px; margin: 2rem auto; padding: 0 1rem; color: #1a1a1a; }
  h1 { margin-bottom: 0.2em; }
  .desc { color: #666; margin-bottom: 1.5em; }
  fieldset { border: 1px solid #ddd; border-radius: 6px; padding: 1em 1.4em; margin-bottom: 1em; }
  legend { font-weight: 600; padding: 0 0.5em; }
  label { display: block; margin: 0.6em 0 0.2em; font-size: 0.92em; color: #333; }
  input { width: 100%; padding: 0.55em; border: 1px solid #ccc; border-radius: 4px; box-sizing: border-box; font-family: inherit; font-size: 0.95em; }
  .row { display: grid; grid-template-columns: 1fr 1fr; gap: 1em; }
  button { background: #2563eb; color: white; border: 0; padding: 0.8em 1.6em; border-radius: 5px; font-size: 1em; font-weight: 600; cursor: pointer; }
  button:hover { background: #1d4ed8; }
  button:disabled { background: #94a3b8; cursor: not-allowed; }
  #result { margin-top: 1.5em; }
  .metric-grid { display: grid; grid-template-columns: repeat(4, 1fr); gap: 0.8em; margin: 1em 0; }
  .metric { background: #f1f5f9; padding: 0.8em; border-radius: 6px; text-align: center; }
  .metric .label { font-size: 0.78em; color: #64748b; text-transform: uppercase; letter-spacing: 0.05em; }
  .metric .value { font-size: 1.5em; font-weight: 700; margin-top: 0.2em; }
  .ok { color: #16a34a; }
  .warn { color: #d97706; }
  .err { color: #dc2626; }
  pre { background: #1e293b; color: #e2e8f0; padding: 1em; border-radius: 6px; overflow-x: auto; font-size: 0.85em; }
  .latency-table { width: 100%; border-collapse: collapse; margin-top: 1em; }
  .latency-table td, .latency-table th { padding: 0.6em 0.8em; text-align: left; border-bottom: 1px solid #eee; }
  .latency-table th { background: #f8fafc; font-size: 0.85em; }
  .hint { font-size: 0.85em; color: #888; margin-top: 0.4em; }
  .spinner { display: inline-block; width: 1em; height: 1em; border: 2px solid #fff; border-top-color: transparent; border-radius: 50%; animation: spin 0.7s linear infinite; vertical-align: -0.15em; margin-right: 0.5em; }
  @keyframes spin { to { transform: rotate(360deg); } }
</style>
</head>
<body>
<h1>Stress Test — Webhook Bitrix</h1>
<p class="desc">Dispara N requisições POST concorrentes para <code>/bitrix/connector/event</code> simulando o evento <code>ONIMCONNECTORMESSAGEADD</code>. As requisições batem no próprio processo (loopback) — útil para medir a perf do webhook + Redis + Bitrix Client sob carga.</p>

<form id="form">
  <fieldset>
    <legend>Carga</legend>
    <div class="row">
      <div>
        <label for="concurrent">Conversas simultâneas</label>
        <input type="number" id="concurrent" value="50" min="1" max="500">
        <div class="hint">Cada conversa = um chat_id distinto (5519900XXXX). Max 500.</div>
      </div>
      <div>
        <label for="msgs">Msgs por conversa</label>
        <input type="number" id="msgs" value="1" min="1" max="50">
        <div class="hint">Sequenciais dentro de cada goroutine. Max 50.</div>
      </div>
    </div>
  </fieldset>

  <fieldset>
    <legend>Alvo</legend>
    <label for="connector_select">Conector</label>
    <select id="connector_select" style="width:100%;padding:0.55em;border:1px solid #ccc;border-radius:4px;box-sizing:border-box;font-family:inherit;font-size:0.95em;">
      <option value="">Carregando…</option>
    </select>
    <div class="hint">Lista carregada de <code>bitrix_accounts</code>. QR e Cloud aparecem juntos.</div>

    <label for="timeout">Timeout HTTP por request (s)</label>
    <input type="number" id="timeout" value="30" min="5" max="120">
  </fieldset>

  <button type="submit" id="runBtn">Rodar teste</button>
</form>

<div id="result"></div>

<script>
const form = document.getElementById('form');
const result = document.getElementById('result');
const btn = document.getElementById('runBtn');
const sel = document.getElementById('connector_select');

// Popular dropdown ao carregar
(async () => {
  try {
    const r = await fetch('/stress-test/connectors');
    const data = await r.json();
    if (!r.ok || !data.connectors) {
      sel.innerHTML = '<option value="">Erro: ' + (data.error || 'falha ao listar') + '</option>';
      return;
    }
    if (data.connectors.length === 0) {
      sel.innerHTML = '<option value="">(nenhum conector cadastrado)</option>';
      return;
    }
    sel.innerHTML = data.connectors.map(c => {
      const label = '[' + c.kind.toUpperCase() + '] ' + c.connector_id + ' — line ' + c.line + ' — ' + c.session_jid;
      const val = c.connector_id + '|' + c.line;
      return '<option value="' + val + '">' + label + '</option>';
    }).join('');
  } catch (err) {
    sel.innerHTML = '<option value="">Erro de rede: ' + err.message + '</option>';
  }
})();

form.addEventListener('submit', async (e) => {
  e.preventDefault();
  const sv = sel.value;
  if (!sv || !sv.includes('|')) {
    result.innerHTML = '<p class="err"><strong>Selecione um conector válido.</strong></p>';
    return;
  }
  const [connector, lineStr] = sv.split('|');
  btn.disabled = true;
  btn.innerHTML = '<span class="spinner"></span> Rodando...';
  result.innerHTML = '<p style="color:#666">Disparando requisições... aguarde.</p>';

  const body = {
    concurrent: parseInt(document.getElementById('concurrent').value, 10),
    msgs_per_conv: parseInt(document.getElementById('msgs').value, 10),
    connector: connector,
    line: parseInt(lineStr, 10),
    timeout_sec: parseInt(document.getElementById('timeout').value, 10),
  };

  try {
    const r = await fetch('/stress-test/run', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    const data = await r.json();
    if (!r.ok) {
      result.innerHTML = '<p class="err"><strong>Erro:</strong> ' + (data.error || r.status) + '</p>';
      return;
    }
    renderResult(data);
  } catch (err) {
    result.innerHTML = '<p class="err"><strong>Erro de rede:</strong> ' + err.message + '</p>';
  } finally {
    btn.disabled = false;
    btn.textContent = 'Rodar teste';
  }
});

function renderResult(d) {
  const successPct = (100 * d.success / d.total).toFixed(1);
  const pctClass = d.success === d.total ? 'ok' : (successPct >= 95 ? 'warn' : 'err');
  const lat = d.latency_ms || {};
  const elapsed = (d.elapsed_ms / 1000).toFixed(2);
  const tput = d.throughput.toFixed(1);

  let html = '<h2>Resultado</h2>';
  html += '<div class="metric-grid">';
  html += metric('Tempo total', elapsed + 's');
  html += metric('Throughput', tput + ' req/s');
  html += metric('Sucesso', successPct + '%', pctClass);
  html += metric('Total', d.total);
  html += '</div>';

  if (d.latency_ms) {
    html += '<h3>Latência</h3>';
    html += '<table class="latency-table">';
    html += '<tr><th>min</th><th>avg</th><th>p50</th><th>p95</th><th>p99</th><th>max</th></tr>';
    html += '<tr>';
    html += '<td>' + lat.min + ' ms</td>';
    html += '<td>' + lat.avg + ' ms</td>';
    html += '<td>' + lat.p50 + ' ms</td>';
    html += '<td>' + lat.p95 + ' ms</td>';
    html += '<td>' + lat.p99 + ' ms</td>';
    html += '<td>' + lat.max + ' ms</td>';
    html += '</tr></table>';
  }

  html += '<h3>Status HTTP</h3>';
  html += '<pre>' + JSON.stringify(d.status_counts, null, 2) + '</pre>';

  if (d.errors > 0) {
    html += '<h3 class="err">Erros (' + d.errors + ')</h3>';
    html += '<pre>' + (d.first_errors || []).join('\n') + '</pre>';
  }

  result.innerHTML = html;
}

function metric(label, value, cls) {
  return '<div class="metric"><div class="label">' + label + '</div><div class="value ' + (cls||'') + '">' + value + '</div></div>';
}
</script>
</body>
</html>`
