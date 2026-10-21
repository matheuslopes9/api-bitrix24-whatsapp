package api

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

// As telas do cliente reconhecem o bloqueio pelo codigo; o motivo e'
// anotacao interna e nao pode vazar pro cliente.
func TestRespostaBloqueado(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c *fiber.Ctx) error { return respostaBloqueado(c) })
	resp, err := app.Test(httptest.NewRequest("GET", "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 403 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	var j map[string]any
	_ = json.Unmarshal(b, &j)
	if j["codigo"] != "bloqueado" || j["bloqueado"] != true {
		t.Fatalf("corpo %s", b)
	}
}

// As tres telas do cliente carregam o aviso NO <head>, antes dos scripts
// delas — senao a primeira chamada bloqueada passa sem aviso.
func TestTelasDoClienteTemOAvisoDeBloqueio(t *testing.T) {
	telas := map[string]string{"painel": dashboardHTML, "aba do CRM": crmTabHTML, "menu do app": bitrixAppMenuHTML}
	for nome, html := range telas {
		i := strings.Index(html, "Acesso ao UC Talk bloqueado")
		head := strings.Index(html, "</head>")
		if i < 0 || head < 0 || i > head {
			t.Errorf("%s: aviso de bloqueio ausente ou fora do <head>", nome)
		}
	}
	if strings.Contains(bloqueioJS, "`") {
		t.Fatal("assets/bloqueio.js tem crase: quebraria a raw string das telas")
	}
}
