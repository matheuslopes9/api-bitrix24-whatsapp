// media_ui.go — entrega o arquivo de uma mensagem pra aba do CRM.
package api

import (
	"errors"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/media"
	"go.uber.org/zap"
)

// tiposExibiveis sao os unicos servidos INLINE. O resto vai como download.
//
// Allow-list de proposito: o arquivo vem do cliente final e e' servido da
// NOSSA origem, onde o operador tem cookie. Um .html ou .svg aberto inline
// executaria script como se fosse o painel. O tipo declarado pelo WhatsApp
// tambem vem do remetente, entao nao basta confiar nele pra liberar.
var tiposExibiveis = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true,
	"audio/ogg": true, "audio/mpeg": true, "audio/mp4": true, "audio/aac": true,
	"audio/amr": true, "audio/wav": true, "audio/x-wav": true,
	"video/mp4": true, "video/3gpp": true, "video/webm": true, "video/quicktime": true,
	"application/pdf": true,
}

// tipoParaServir decide o Content-Type e se o arquivo abre no navegador.
func tipoParaServir(mimeDeclarado string) (contentType string, inline bool) {
	base, _, err := mime.ParseMediaType(mimeDeclarado)
	if err != nil {
		base = strings.TrimSpace(strings.SplitN(mimeDeclarado, ";", 2)[0])
	}
	base = strings.ToLower(base)
	if tiposExibiveis[base] {
		return base, true
	}
	return "application/octet-stream", false
}

// GET /ui/media/:id[?baixar=1] — arquivo da mensagem :id (UUID).
func (h *handlers) uiMedia(c *fiber.Ctx) error {
	if h.midias == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "armazenamento de arquivos indisponivel"})
	}
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "id invalido"})
	}
	m, err := h.repo.GetMidiaDaMensagem(c.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "mensagem nao encontrada"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	// Mesma resposta pra "nao existe" e "nao e' seu": nao confirmar a
	// existencia de mensagem de outro cliente.
	pode, err := h.podeOperarNumero(c, m.NossoJID())
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error()})
	}
	if !pode {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "mensagem nao encontrada"})
	}
	if strings.HasPrefix(m.MediaURL, media.PrefixoGrande) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "arquivo grande demais para ficar guardado — abra pelo Contact Center"})
	}
	caminho, err := h.midias.Caminho(m.MediaURL)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "mensagem sem arquivo guardado"})
	}
	f, err := os.Open(caminho)
	if err != nil {
		// Retencao ja' apagou, ou o volume foi trocado.
		return c.Status(fiber.StatusGone).JSON(fiber.Map{"error": "arquivo expirado"})
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return c.Status(fiber.StatusGone).JSON(fiber.Map{"error": "arquivo expirado"})
	}

	nome := media.NomeDaRef(m.MediaURL)
	tipo, inline := tipoParaServir(m.MediaMime)
	if c.Query("baixar") == "1" {
		inline = false
	}
	disp := "attachment"
	if inline {
		disp = "inline"
	}
	// ServeContent cuida de Range — sem isso o player nao avanca no audio
	// ou video, e o Safari nem toca.
	return adaptor.HTTPHandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", tipo)
		w.Header().Set("Content-Disposition", disp+"; filename*=UTF-8''"+url.PathEscape(nome))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "private, max-age=3600")
		if tipo != "application/pdf" {
			// Defesa extra caso algo escape da allow-list. O leitor de PDF
			// do navegador nao abre com sandbox, por isso a excecao.
			w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
		}
		http.ServeContent(w, r, nome, info.ModTime(), f)
	})(c)
}

// salvarMidia grava o arquivo pra aba do CRM. Falha aqui nunca derruba a
// entrega da mensagem — so' volta ao rotulo sem arquivo.
func (h *handlers) salvarMidia(nome, mimeType string, dados []byte) (ref string) {
	return SalvarMidia(h.midias, h.log, nome, mimeType, dados)
}

// SalvarMidia e' a versao exportada, usada tambem pelos workers em main.go.
func SalvarMidia(s *media.Store, log *zap.Logger, nome, mimeType string, dados []byte) string {
	if s == nil || len(dados) == 0 {
		return ""
	}
	ref, err := s.Salvar(nome, mimeType, dados, time.Now())
	if err != nil {
		log.Warn("midia: falha ao guardar arquivo pra aba do CRM",
			zap.String("arquivo", nome), zap.Error(err))
		return ""
	}
	return ref
}
