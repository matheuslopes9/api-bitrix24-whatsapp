package api

import (
	_ "embed"

	"github.com/gofiber/fiber/v2"
)

//go:embed assets/chart.js
var chartJS []byte

func (h *handlers) serveChartJS(c *fiber.Ctx) error {
	c.Set("Content-Type", "application/javascript; charset=utf-8")
	c.Set("Cache-Control", "public, max-age=86400")
	return c.Send(chartJS)
}
