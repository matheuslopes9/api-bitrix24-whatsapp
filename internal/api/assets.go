package api

import (
	_ "embed"

	"github.com/gofiber/fiber/v2"
)

//go:embed assets/chart.js
var chartJS []byte

//go:embed assets/logo.png
var logoPNG []byte

//go:embed assets/logo_uc.png
var logoUCPNG []byte

// logoEmailPNG e' a marca da UC Technology usada nos ALERTAS por e-mail.
//
// Servida pelo proprio app, e nao buscada em ferramentas.uctechnology.com.br
// como faz o template original. Duas razoes:
//
//   - o e-mail nao fica dependendo de outro servico estar no ar. Alerta
//     costuma chegar justamente quando algo esta' quebrado, e a hora de
//     descobrir que a imagem sumiu nao pode ser essa;
//   - em homolog o alerta nao puxa imagem do ambiente de producao.
//
// Cliente de e-mail nao manda cookie nem header: a rota e' publica de
// proposito, e o conteudo e' so' a marca.
//
//go:embed assets/logo_email.png
var logoEmailPNG []byte

func (h *handlers) serveChartJS(c *fiber.Ctx) error {
	c.Set("Content-Type", "application/javascript; charset=utf-8")
	c.Set("Cache-Control", "public, max-age=86400")
	return c.Send(chartJS)
}

func (h *handlers) serveLogo(c *fiber.Ctx) error {
	c.Set("Content-Type", "image/png")
	c.Set("Cache-Control", "public, max-age=86400")
	return c.Send(logoUCPNG)
}

// serveLogoEmail responde a logo dos alertas. Cache longo porque a marca
// nao muda, e cliente de e-mail rebusca a imagem a cada abertura.
func (h *handlers) serveLogoEmail(c *fiber.Ctx) error {
	c.Set("Content-Type", "image/png")
	c.Set("Cache-Control", "public, max-age=604800")
	return c.Send(logoEmailPNG)
}

func (h *handlers) serveFavicon(c *fiber.Ctx) error {
	c.Set("Content-Type", "image/png")
	c.Set("Cache-Control", "public, max-age=86400")
	return c.Send(logoUCPNG)
}
