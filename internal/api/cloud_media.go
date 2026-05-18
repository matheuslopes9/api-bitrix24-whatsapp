package api

// Endpoint público que serve arquivos temporários para a Meta Cloud API
// baixar via link. Usado para tipos rejeitados pelo upload direto
// (zip, tar, msi, exe, etc) — a Meta baixa do nosso servidor.

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"
)

// GET /cloud-media/:token
// Retorna o arquivo armazenado no Redis pelo token. URL pública (sem auth)
// — o token aleatório de 32 chars é a única proteção. TTL curto (1h).
//
// O Content-Type retornado vem do MIME guardado quando o arquivo foi salvo,
// permitindo que a Meta valide corretamente o conteúdo via HEAD/GET.
func (h *handlers) cloudMediaServe(c *fiber.Ctx) error {
	token := c.Params("token")
	if token == "" {
		return c.Status(400).SendString("token required")
	}
	media, err := h.q.LoadMedia(c.Context(), token)
	if err != nil {
		h.log.Warn("cloudMediaServe: load failed", zap.String("token_prefix", safePrefix(token, 6)), zap.Error(err))
		return c.Status(500).SendString("internal error")
	}
	if media == nil {
		return c.Status(404).SendString("media not found or expired")
	}
	mime := media.Mime
	if mime == "" {
		mime = "application/octet-stream"
	}
	c.Set("Content-Type", mime)
	if media.FileName != "" {
		c.Set("Content-Disposition", `inline; filename="`+escapeQ(media.FileName)+`"`)
	}
	return c.Send(media.Data)
}

// generateMediaToken cria um token aleatório de 32 chars hex (16 bytes).
// Usado como path do endpoint público — improvavel que alguem advinhe.
func generateMediaToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// escapeQ escapa aspas para uso em headers HTTP.
func escapeQ(s string) string {
	out := make([]byte, 0, len(s))
	for _, c := range []byte(s) {
		if c == '"' || c == '\\' {
			out = append(out, '\\')
		}
		out = append(out, c)
	}
	return string(out)
}
