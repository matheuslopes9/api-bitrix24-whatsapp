// crm_identidade.go — quem esta' chamando as rotas /bitrix/crm/*.
//
// O BURACO: essas rotas recebiam ?domain= e user_id do proprio chamador e
// confiavam nos dois. Sem login nenhum dava pra ler nome e telefone de
// contato de qualquer portal, ler conversas, se declarar master e enviar
// WhatsApp como qualquer operador.
//
// Agora a identidade vem de dois cookies assinados, emitidos por
// /bitrix/auth DEPOIS de o Bitrix confirmar o token:
//   - uctalk_tenant: o portal;
//   - uctalk_user:   o usuario do Bitrix dono do token, naquele portal.
//
// O middleware sobrescreve domain/user_id da query com esses valores; os
// handlers com corpo JSON ou multipart leem de identidadeCRM.
package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

const userCookieName = "uctalk_user"

// signUserCookie: "exp|domain|userID|hmac".
func signUserCookie(secret, domain, userID string, expiresAt time.Time) string {
	payload := strconv.FormatInt(expiresAt.Unix(), 10) + "|" + domain + "|" + userID
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("user-v1:" + payload))
	return payload + "|" + hex.EncodeToString(mac.Sum(nil))
}

func verifyUserCookie(secret, raw string) (domain, userID string, ok bool) {
	partes := strings.Split(raw, "|")
	if len(partes) != 4 {
		return "", "", false
	}
	exp, err := strconv.ParseInt(partes[0], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", "", false
	}
	payload := strings.Join(partes[:3], "|")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("user-v1:" + payload))
	if subtle.ConstantTimeCompare([]byte(partes[3]), []byte(hex.EncodeToString(mac.Sum(nil)))) != 1 {
		return "", "", false
	}
	if partes[1] == "" || partes[2] == "" {
		return "", "", false
	}
	return partes[1], partes[2], true
}

// exigirIdentidadeCRM protege as rotas JSON de /bitrix/crm/*.
func (h *handlers) exigirIdentidadeCRM(c *fiber.Ctx) error {
	// Super-admin no "Preview do app": escolhe o portal por ?domain= e nao
	// tem usuario do Bitrix — le, mas nao envia como ninguem.
	if _, _, ok := verifyAdminCookie(h.cfg.App.Secret, c.Cookies(adminCookieName)); ok {
		if q := normalizePortalDomain(c.Query("domain")); q != "" {
			c.Locals("tenant_domain", q)
			c.Locals("crm_user_id", "")
			c.Locals("auth_source", "admin")
			return c.Next()
		}
	}
	dominio, ok := verifyTenantCookie(h.cfg.App.Secret, c.Cookies(tenantCookieName))
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "sessao expirada — reabra a aba do UC Talk", "codigo": "sem_identidade"})
	}
	udom, userID, ok := verifyUserCookie(h.cfg.App.Secret, c.Cookies(userCookieName))
	if !ok || udom != dominio {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "sessao expirada — reabra a aba do UC Talk", "codigo": "sem_identidade"})
	}
	// Pedido explicito de OUTRO portal e' recusado, nao corrigido em
	// silencio: quem manda isso nao e' a nossa tela.
	if q := normalizePortalDomain(c.Query("domain")); q != "" && q != dominio {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "portal diferente da sessao"})
	}
	c.Locals("tenant_domain", dominio)
	c.Locals("crm_user_id", userID)
	args := c.Request().URI().QueryArgs()
	args.Set("domain", dominio)
	if args.Has("user_id") {
		args.Set("user_id", userID)
	}
	if args.Has("caller_user_id") {
		args.Set("caller_user_id", userID)
	}
	return c.Next()
}

// identidadeCRM devolve portal e usuario confirmados (so' valem depois do
// middleware exigirIdentidadeCRM).
func identidadeCRM(c *fiber.Ctx) (dominio, userID string) {
	dominio, _ = c.Locals("tenant_domain").(string)
	userID, _ = c.Locals("crm_user_id").(string)
	return
}

// exigirNumeroDoPortal recusa envio por numero que nao e' do portal.
//
// A permissao por numero nao bastava: o master tem permissao "curinga"
// (session_jid vazio em crm_user_permissions), que liberava QUALQUER
// session_jid — inclusive o de outro cliente.
//
// Devolve ok=false quando JA' escreveu a resposta de erro. Nao da' pra usar
// so' o error: c.JSON devolve nil quando escreve com sucesso, entao um
// "if err != nil" deixaria o envio seguir depois do 403.
func (h *handlers) exigirNumeroDoPortal(c *fiber.Ctx, sessionJID string) (ok bool, resp error) {
	pode, err := h.podeOperarNumero(c, sessionJID)
	if err != nil {
		return false, c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error()})
	}
	if !pode {
		return false, c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "este numero nao pertence a este portal"})
	}
	return true, nil
}
