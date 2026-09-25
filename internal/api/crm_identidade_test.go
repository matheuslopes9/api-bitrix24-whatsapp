package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/config"
	"go.uber.org/zap"
)

const segredoTeste = "segredo-de-teste"

func handlersDeTeste() *handlers {
	return &handlers{cfg: &config.Config{App: config.AppConfig{Secret: segredoTeste}}, log: zap.NewNop()}
}

func TestUserCookieIdaEVolta(t *testing.T) {
	raw := signUserCookie(segredoTeste, "a.bitrix24.com.br", "42", time.Now().Add(time.Hour))
	d, u, ok := verifyUserCookie(segredoTeste, raw)
	if !ok || d != "a.bitrix24.com.br" || u != "42" {
		t.Fatalf("verify = %q %q %v", d, u, ok)
	}
	// Trocar o usuario sem refazer a assinatura tem que falhar.
	adulterado := strings.Replace(raw, "|42|", "|1|", 1)
	if _, _, ok := verifyUserCookie(segredoTeste, adulterado); ok {
		t.Fatal("aceitou cookie com usuario trocado")
	}
	if _, _, ok := verifyUserCookie("outro-segredo", raw); ok {
		t.Fatal("aceitou cookie assinado com outro segredo")
	}
	vencido := signUserCookie(segredoTeste, "a.bitrix24.com.br", "42", time.Now().Add(-time.Minute))
	if _, _, ok := verifyUserCookie(segredoTeste, vencido); ok {
		t.Fatal("aceitou cookie vencido")
	}
}

// Cookie emitido ANTES da verificacao de token (assinatura sem "v2:") pode
// ter sido forjado por qualquer um — nao pode mais valer.
func TestCookieDeTenantAntigoNaoVale(t *testing.T) {
	exp := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	payload := exp + "|vitima.bitrix24.com.br"
	mac := hmac.New(sha256.New, []byte(segredoTeste))
	mac.Write([]byte(payload))
	antigo := payload + "|" + hex.EncodeToString(mac.Sum(nil))
	if _, ok := verifyTenantCookie(segredoTeste, antigo); ok {
		t.Fatal("cookie no formato antigo ainda vale")
	}
	novo := signTenantCookie(segredoTeste, "vitima.bitrix24.com.br", time.Now().Add(time.Hour))
	if d, ok := verifyTenantCookie(segredoTeste, novo); !ok || d != "vitima.bitrix24.com.br" {
		t.Fatalf("cookie novo recusado: %q %v", d, ok)
	}
}

func pedido(t *testing.T, app *fiber.App, metodo, url, corpo string, cookies ...*http.Cookie) (int, string) {
	t.Helper()
	req := httptest.NewRequest(metodo, url, strings.NewReader(corpo))
	if corpo != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func cookiesDe(dominio, usuario string) []*http.Cookie {
	exp := time.Now().Add(time.Hour)
	return []*http.Cookie{
		{Name: tenantCookieName, Value: signTenantCookie(segredoTeste, dominio, exp)},
		{Name: userCookieName, Value: signUserCookie(segredoTeste, dominio, usuario, exp)},
	}
}

func TestExigirIdentidadeCRM(t *testing.T) {
	h := handlersDeTeste()
	app := fiber.New()
	app.Get("/crm/eco", h.exigirIdentidadeCRM, func(c *fiber.Ctx) error {
		return c.SendString(c.Query("domain") + "|" + c.Query("user_id"))
	})

	if st, _ := pedido(t, app, "GET", "/crm/eco?domain=a.com.br&user_id=1", ""); st != 401 {
		t.Errorf("sem cookie: status %d, esperado 401", st)
	}

	// Com cookies, domain e user_id da query sao SUBSTITUIDOS pelos
	// confirmados — o user_id=1 que a tela mandou nao vale nada.
	st, corpo := pedido(t, app, "GET", "/crm/eco?user_id=1", "", cookiesDe("a.com.br", "42")...)
	if st != 200 || corpo != "a.com.br|42" {
		t.Errorf("com cookie: %d %q, esperado 200 \"a.com.br|42\"", st, corpo)
	}

	if st, _ := pedido(t, app, "GET", "/crm/eco?domain=vitima.com.br", "", cookiesDe("a.com.br", "42")...); st != 403 {
		t.Errorf("domain de outro portal: status %d, esperado 403", st)
	}

	// Cookie de usuario de OUTRO portal junto com o tenant certo.
	misturado := []*http.Cookie{cookiesDe("a.com.br", "42")[0], cookiesDe("b.com.br", "42")[1]}
	if st, _ := pedido(t, app, "GET", "/crm/eco", "", misturado...); st != 401 {
		t.Errorf("cookies de portais diferentes: status %d, esperado 401", st)
	}
}

func TestEscoparAoTenantRecusaOutroPortal(t *testing.T) {
	h := handlersDeTeste()
	app := fiber.New()
	comoTenant := func(c *fiber.Ctx) error {
		c.Locals("auth_source", "tenant")
		c.Locals("tenant_domain", "a.com.br")
		return c.Next()
	}
	app.All("/ui/x", comoTenant, h.escoparAoTenant, func(c *fiber.Ctx) error {
		return c.SendString(c.Query("domain") + "|" + c.Query("portal") + "|" + string(c.Body()))
	})

	if st, _ := pedido(t, app, "GET", "/ui/x?portal=vitima.com.br", ""); st != 403 {
		t.Errorf("?portal= de outro: status %d, esperado 403", st)
	}
	if st, _ := pedido(t, app, "POST", "/ui/x", `{"domain":"vitima.com.br","open_line_id":1}`); st != 403 {
		t.Errorf("JSON domain de outro: status %d, esperado 403", st)
	}
	st, corpo := pedido(t, app, "POST", "/ui/x", `{"domain":"","open_line_id":1}`)
	if st != 200 || !strings.HasPrefix(corpo, "a.com.br|a.com.br|") || !strings.Contains(corpo, `"domain":"a.com.br"`) {
		t.Errorf("sem domain: %d %q — esperado portal do cookie preenchido na query e no corpo", st, corpo)
	}
}

// Visto no homolog em 25/09: um POST anonimo em /bitrix/connector/event,
// sem auth[domain], saiu de verdade pelo WhatsApp. Sem dominio nao ha' como
// saber de quem e' o evento — recusado em qualquer modo.
func TestEventoSemDominioEhRecusado(t *testing.T) {
	for _, modo := range []string{"", "exigir", "observar"} {
		t.Setenv("CONNECTOR_EVENT_TOKEN", modo)
		h := handlersDeTeste()
		app := fiber.New()
		app.Post("/evt", func(c *fiber.Ctx) error {
			if h.eventoPodeUsarSessao(c, "551920187040:4@s.whatsapp.net") {
				return c.SendString("enviaria")
			}
			return c.SendString("recusado")
		})
		req := httptest.NewRequest("POST", "/evt", strings.NewReader("data[CONNECTOR]=wa_qr_551920187040&data[MESSAGES][0][message][text]=forjado"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		if string(b) != "recusado" {
			t.Errorf("modo %q: evento sem auth[domain] %s", modo, b)
		}
	}
}

// "observar" deixava forjar resposta de operador por quem soubesse o dominio.
// O padrao tem que ser exigir a prova.
func TestModoPadraoExigeProva(t *testing.T) {
	t.Setenv("CONNECTOR_EVENT_TOKEN", "")
	if modoTokenDoEvento() != "exigir" {
		t.Fatal("padrao deveria ser exigir")
	}
	t.Setenv("CONNECTOR_EVENT_TOKEN", "OBSERVAR")
	if modoTokenDoEvento() != "observar" {
		t.Fatal("valvula de emergencia nao funciona")
	}
}
