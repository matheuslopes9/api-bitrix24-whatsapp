package api

import (
	_ "embed"

	"github.com/gofiber/fiber/v2"
)

//go:embed assets/chart.js
var chartJS []byte

//go:embed assets/logo.png
var logoPNG []byte

func (h *handlers) serveChartJS(c *fiber.Ctx) error {
	c.Set("Content-Type", "application/javascript; charset=utf-8")
	c.Set("Cache-Control", "public, max-age=86400")
	return c.Send(chartJS)
}

func (h *handlers) serveLogo(c *fiber.Ctx) error {
	c.Set("Content-Type", "image/png")
	c.Set("Cache-Control", "public, max-age=86400")
	return c.Send(logoPNG)
}

func (h *handlers) serveFavicon(c *fiber.Ctx) error {
	c.Set("Content-Type", "image/png")
	c.Set("Cache-Control", "public, max-age=86400")
	return c.Send(logoPNG)
}
