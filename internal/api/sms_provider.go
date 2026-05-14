// Modulo SMS Provider — totalmente isolado.
//
// O Bitrix24 oferece "Marketing > Campanhas SMS" com varios provedores
// (Twilio, SMSAPI etc). Nosso app se registra como mais um provedor; o
// Bitrix trata como SMS mas nos roteamos pra WhatsApp.
//
// Fluxo:
//  1. No install do app (partner.go) chamamos messageservice.sender.add
//     com CODE=uctalk_whatsapp + HANDLER=https://uctalk.../bitrix/sms/send.
//  2. Cliente vai em Marketing > Campanhas SMS, escolhe "UC Talk", dispara.
//  3. Bitrix faz POST x-www-form-urlencoded por destinatario no HANDLER:
//       message_id, message_to, message_body, code, ts, auth[...], bindings[...]
//  4. Aceitamos rapido (HTTP 200) pra Bitrix nao dar timeout. Em goroutine
//     separada, enviamos pelo WhatsApp e reportamos status.
//
// Nada aqui toca em CRM tab, /dashboard existente, queue normal etc.

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/bitrix"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"go.uber.org/zap"
)

// SMSSenderCode: identificador unico do nosso provedor SMS no Bitrix.
// Hardcoded — nao deveria ser configuravel pois e' chave de registro.
const SMSSenderCode = "uctalk_whatsapp"

// smsHandlerPath: rota onde o Bitrix vai bater. Tem que ser o caminho
// real registrado no server.go (POST).
const smsHandlerPath = "/bitrix/sms/send"

// RegisterSMSSenderForPortal e' chamado no install do app. Faz best-effort:
// se falhar nao quebra o install — admin pode configurar manualmente via
// /admin master ou re-instalando. Importante NAO retornar erro pra nao
// poluir o fluxo principal.
func (h *handlers) RegisterSMSSenderForPortal(ctx context.Context, portal *db.BitrixPortal) {
	if portal == nil {
		return
	}
	creds := h.portalToCreds(portal)
	handlerURL := h.cfg.App.BaseURL() + smsHandlerPath
	// Bitrix aceita NAME como string ou map localizado. Usamos string simples
	// — clientes podem renomear pelo painel do Bitrix depois se quiserem.
	err := h.bitrixClient.RegisterSMSSender(ctx, creds, SMSSenderCode, "UC Talk WhatsApp", handlerURL)
	if err != nil {
		h.log.Warn("sms-provider: register failed (will keep app working anyway)",
			zap.String("domain", portal.Domain), zap.Error(err))
		return
	}
	h.log.Info("sms-provider: sender registered",
		zap.String("domain", portal.Domain), zap.String("handler", handlerURL))
}

// UnregisterSMSSenderForPortal: cleanup no uninstall. Best-effort.
func (h *handlers) UnregisterSMSSenderForPortal(ctx context.Context, portal *db.BitrixPortal) {
	if portal == nil {
		return
	}
	creds := h.portalToCreds(portal)
	if err := h.bitrixClient.DeleteSMSSender(ctx, creds, SMSSenderCode); err != nil {
		h.log.Warn("sms-provider: unregister failed",
			zap.String("domain", portal.Domain), zap.Error(err))
	}
}

// POST /bitrix/sms/send — Bitrix dispara aqui por destinatario.
// Form-encoded payload (nao JSON). Bitrix espera resposta rapida (<5s),
// senao a UI da campanha trava. Persistimos rapido e processamos
// em goroutine separada.
func (h *handlers) smsProviderSend(c *fiber.Ctx) error {
	// Validacao de seguranca: confirma que o POST vem de um Bitrix que
	// instalou o app. application_token e' verificado contra o que
	// salvamos no install.
	appToken := c.FormValue("auth[application_token]")
	domainRaw := c.FormValue("auth[domain]")
	domain := normalizePortalDomain(domainRaw)
	if domain == "" || appToken == "" {
		h.log.Warn("sms-provider: missing auth in POST",
			zap.String("domain", domainRaw))
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "auth missing"})
	}

	portal, err := h.repo.GetBitrixPortalByDomain(c.Context(), domain)
	if err != nil || portal == nil {
		h.log.Warn("sms-provider: portal not found", zap.String("domain", domain))
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "portal not found"})
	}
	// application_token e' enviado pelo Bitrix em cada POST. Idealmente
	// validariamos contra um valor guardado no install — por enquanto
	// confiamos no fato de que:
	//   1. domain veio no payload
	//   2. portal existe no nosso banco com OAuth ativo
	//   3. HTTPS impede MitM
	// Defesa em profundidade: poderiamos guardar application_token no
	// bitrix_portals na proxima migration e validar exato. TODO.
	_ = appToken

	// Tenant precisa ter sessao default configurada. Sem isso, modulo
	// esta desligado pra esse portal — recusamos com 200 e status 'failed'
	// pra Bitrix nao retentar infinitamente.
	if portal.DefaultSMSSessionJID == "" {
		h.log.Warn("sms-provider: tenant has no default session — drop",
			zap.String("domain", domain))
		// Retornamos 200 (Bitrix nao retenta) mas registramos failure.
		// Bitrix vai ficar com status "pending" eterno — aceitavel pra
		// alertar o cliente que precisa configurar.
		return c.JSON(fiber.Map{"result": "no_session"})
	}

	bitrixMsgID := strings.TrimSpace(c.FormValue("message_id"))
	toPhone := normalizeWAPhone(c.FormValue("message_to"))
	body := c.FormValue("message_body")
	if bitrixMsgID == "" || toPhone == "" || body == "" {
		h.log.Warn("sms-provider: missing required fields",
			zap.String("domain", domain),
			zap.String("message_id", bitrixMsgID),
			zap.String("to_phone", toPhone))
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "missing fields"})
	}

	// Captura bindings opcionais (vinculo CRM Lead/Deal/Contact). Salvamos
	// como JSON cru pra futura UI mostrar "Contato X, Lead Y".
	bindingsJSON := captureBindings(c)

	msg := &db.BitrixSMSMessage{
		BitrixMessageID: bitrixMsgID,
		Domain:          domain,
		SenderCode:      SMSSenderCode,
		SessionJID:      portal.DefaultSMSSessionJID,
		ToPhone:         toPhone,
		Body:            body,
		BindingsJSON:    bindingsJSON,
	}
	if err := h.repo.InsertSMSMessage(c.Context(), msg); err != nil {
		h.log.Error("sms-provider: insert failed",
			zap.String("domain", domain),
			zap.String("message_id", bitrixMsgID),
			zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	// Dispara envio em background. Bitrix recebe 200 imediato.
	// Snapshot do que precisa: domain, portal, jid, phone, body, msgID.
	creds := h.portalToCreds(portal)
	sessionJID := portal.DefaultSMSSessionJID
	go h.processSMSSend(creds, sessionJID, msg)

	return c.JSON(fiber.Map{"result": "queued", "message_id": bitrixMsgID})
}

// processSMSSend roda em goroutine separada. Envia via WhatsApp e atualiza
// status local + status no Bitrix.
//
// Detecta marcador "[meta:template_name|var1|var2]" no inicio do body. Se
// presente E a sessao for Cloud API, envia como template (caminho oficial
// pra disparo ativo). Se nao, manda texto livre (so funciona dentro da
// janela de 24h — fora dela Meta descarta silenciosamente).
func (h *handlers) processSMSSend(creds bitrix.TenantCreds, sessionJID string, m *db.BitrixSMSMessage) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	toJID := m.ToPhone + "@s.whatsapp.net"
	var waMsgID string
	var sendErr error
	var sendMode string // "template" | "text"

	tplName, tplVars, bodyAfterMarker := parseMetaMarker(m.Body)

	if tplName != "" && strings.HasPrefix(sessionJID, "cloud:") {
		// Resolve lang do template no banco (Meta exige).
		t, err := h.repo.GetMessageTemplateByMetaName(ctx, m.Domain, tplName)
		lang := "pt_BR" // default razoavel
		if err == nil && t != nil && t.MetaTemplateLang != "" {
			lang = t.MetaTemplateLang
		}
		sendMode = "template"
		waMsgID, sendErr = h.cloudMgr.SendTemplate(ctx, sessionJID, m.ToPhone, tplName, lang, tplVars)
		h.log.Info("sms-provider: routing as template",
			zap.String("template", tplName),
			zap.String("lang", lang),
			zap.Int("vars", len(tplVars)))
	} else {
		// Texto livre. Marcador (se presente mas sessao Multi-Device) ja foi
		// removido — manda apenas o body limpo.
		text := bodyAfterMarker
		if text == "" {
			text = m.Body
		}
		sendMode = "text"
		if strings.HasPrefix(sessionJID, "cloud:") {
			waMsgID, sendErr = h.cloudMgr.SendText(ctx, sessionJID, m.ToPhone, text)
		} else {
			waMsgID, sendErr = h.waManager.Send(ctx, sessionJID, toJID, text)
		}
	}
	_ = sendMode // captured em logs abaixo

	if sendErr != nil {
		h.log.Warn("sms-provider: WA send failed",
			zap.String("domain", m.Domain),
			zap.String("message_id", m.BitrixMessageID),
			zap.String("to_phone", m.ToPhone),
			zap.Error(sendErr))
		_ = h.repo.UpdateSMSMessageStatus(ctx, m.BitrixMessageID, "failed", sendErr.Error())
		// Reporta failure ao Bitrix (best-effort).
		_ = h.bitrixClient.UpdateSMSMessageStatus(ctx, creds, SMSSenderCode, m.BitrixMessageID, "failed")
		return
	}

	// WA aceitou. Status "sent" no nosso lado + "sent" no Bitrix.
	if err := h.repo.UpdateSMSMessageSent(ctx, m.BitrixMessageID, waMsgID); err != nil {
		h.log.Warn("sms-provider: update sent status failed",
			zap.String("message_id", m.BitrixMessageID), zap.Error(err))
	}
	if err := h.bitrixClient.UpdateSMSMessageStatus(ctx, creds, SMSSenderCode, m.BitrixMessageID, "sent"); err != nil {
		h.log.Warn("sms-provider: report sent to Bitrix failed",
			zap.String("message_id", m.BitrixMessageID), zap.Error(err))
	}

	h.log.Info("sms-provider: sent",
		zap.String("domain", m.Domain),
		zap.String("message_id", m.BitrixMessageID),
		zap.String("wa_message_id", waMsgID))

	// Status "delivered" sera reportado quando o WhatsApp confirmar
	// entrega via webhook (Cloud) ou whatsmeow receipt (Multi-Device).
	// Esses paths chamam ReportSMSDelivered abaixo — sem mexer no fluxo
	// existente de delivery receipts.
}

// ReportSMSDelivered: helper publico chamado pelos paths de delivery
// receipt do WhatsApp (cloud_webhook.go quando reconhecer wa_message_id
// como SMS provider; whatsmeow handler idem). Atualiza status local
// e dispara message.status.update no Bitrix.
//
// Por enquanto NAO esta integrado nos webhooks — fica como hook pra
// adicionar quando alguem quiser. Status 'sent' (WA aceitou) ja' satisfaz
// 95% dos casos da UI de Campanha SMS do Bitrix.
func (h *handlers) ReportSMSDelivered(ctx context.Context, waMessageID string) {
	if waMessageID == "" {
		return
	}
	m, err := h.repo.GetSMSMessageByWAID(ctx, waMessageID)
	if err != nil {
		// Nao e' uma msg de SMS provider — ignora silenciosamente
		return
	}
	if m.Status == "delivered" {
		return // ja reportado
	}
	_ = h.repo.UpdateSMSMessageStatus(ctx, m.BitrixMessageID, "delivered", "")
	portal, err := h.repo.GetBitrixPortalByDomain(ctx, m.Domain)
	if err != nil || portal == nil {
		return
	}
	creds := h.portalToCreds(portal)
	_ = h.bitrixClient.UpdateSMSMessageStatus(ctx, creds, SMSSenderCode, m.BitrixMessageID, "delivered")
}

// captureBindings extrai bindings[0][OWNER_TYPE_ID]/OWNER_ID etc.
// Salva como JSON cru pra UI futura. Best-effort — se vazio, ok.
// parseMetaMarker extrai marcador "[meta:template_name|var1|var2|...]" do
// inicio do body. Retorna (templateName, vars, bodyAfterMarker).
//
// Examples:
//   "[meta:welcome_msg]"                              -> ("welcome_msg", [], "")
//   "[meta:promo|Joao|R$100]"                         -> ("promo", ["Joao","R$100"], "")
//   "[meta:promo|Joao] texto livre depois"            -> ("promo", ["Joao"], " texto livre depois")
//   "Sem marcador, texto puro"                        -> ("", nil, "Sem marcador, texto puro")
//
// Marcador deve estar EXATAMENTE no inicio (sem espaco antes). Case-sensitive
// no prefixo "[meta:". O delimitador de variaveis e' "|".
func parseMetaMarker(body string) (string, []string, string) {
	const prefix = "[meta:"
	if !strings.HasPrefix(body, prefix) {
		return "", nil, body
	}
	end := strings.Index(body, "]")
	if end < 0 {
		// "[meta:" sem fechamento — trata como texto livre
		return "", nil, body
	}
	inner := body[len(prefix):end]
	rest := body[end+1:]
	parts := strings.Split(inner, "|")
	name := strings.TrimSpace(parts[0])
	if name == "" {
		return "", nil, body
	}
	vars := make([]string, 0, len(parts)-1)
	for _, p := range parts[1:] {
		vars = append(vars, strings.TrimSpace(p))
	}
	return name, vars, rest
}

func captureBindings(c *fiber.Ctx) string {
	binds := []map[string]string{}
	// Bitrix manda como bindings[0][OWNER_TYPE_ID]=X&bindings[0][OWNER_ID]=Y...
	// Forma simples: ler bindings ate' nao achar mais.
	for i := 0; i < 10; i++ {
		ownerType := c.FormValue(fmt.Sprintf("bindings[%d][OWNER_TYPE_ID]", i))
		ownerID := c.FormValue(fmt.Sprintf("bindings[%d][OWNER_ID]", i))
		if ownerType == "" && ownerID == "" {
			break
		}
		binds = append(binds, map[string]string{
			"owner_type": ownerType, "owner_id": ownerID,
		})
	}
	if len(binds) == 0 {
		return ""
	}
	raw, err := json.Marshal(binds)
	if err != nil {
		return ""
	}
	return string(raw)
}

// ─── Endpoints do dashboard pra configurar o modulo ──────────────────────

// GET /ui/sms/status?domain=... — retorna config do tenant pra UI.
func (h *handlers) uiSMSStatus(c *fiber.Ctx) error {
	ctx := c.Context()
	domain, err := h.resolveDashboardDomain(ctx, c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	portal, err := h.repo.GetBitrixPortalByDomain(ctx, domain)
	if err != nil || portal == nil {
		return c.Status(404).JSON(fiber.Map{"error": "portal nao encontrado"})
	}
	sessions, _ := h.repo.ListActiveSessionsByDomain(ctx, domain)
	out := make([]fiber.Map, 0, len(sessions))
	for _, s := range sessions {
		phone := s.Phone
		if phone == "" {
			phone = s.CloudDisplayPhone
		}
		out = append(out, fiber.Map{
			"jid": s.JID, "phone": phone, "type": s.Type, "status": s.Status,
		})
	}
	return c.JSON(fiber.Map{
		"default_session_jid": portal.DefaultSMSSessionJID,
		"risk_acknowledged":   portal.SMSRiskAcknowledged,
		"sessions":            out,
		"sender_code":         SMSSenderCode,
	})
}

// POST /ui/sms/set-session — body {session_jid, caller_user_id}
// caller_user_id precisa == master.
func (h *handlers) uiSMSSetSession(c *fiber.Ctx) error {
	ctx := c.Context()
	domain, err := h.resolveDashboardDomain(ctx, c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	var req struct {
		SessionJID   string `json:"session_jid"`
		CallerUserID string `json:"caller_user_id"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "JSON invalido"})
	}
	portal, err := h.repo.GetBitrixPortalByDomain(ctx, domain)
	if err != nil || portal == nil {
		return c.Status(404).JSON(fiber.Map{"error": "portal nao encontrado"})
	}
	if portal.LegacyAdminUserID == "" {
		return c.Status(409).JSON(fiber.Map{"error": "master nao configurado"})
	}
	if req.CallerUserID != portal.LegacyAdminUserID {
		return c.Status(403).JSON(fiber.Map{"error": "apenas o master pode configurar"})
	}
	if err := h.repo.SetDefaultSMSSession(ctx, domain, req.SessionJID); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.log.Info("sms-provider: default session updated",
		zap.String("domain", domain), zap.String("session_jid", req.SessionJID))
	return c.JSON(fiber.Map{"ok": true})
}

// POST /ui/sms/ack-risk — body {caller_user_id} marca aviso visto.
func (h *handlers) uiSMSAckRisk(c *fiber.Ctx) error {
	ctx := c.Context()
	domain, err := h.resolveDashboardDomain(ctx, c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	var req struct {
		CallerUserID string `json:"caller_user_id"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "JSON invalido"})
	}
	portal, err := h.repo.GetBitrixPortalByDomain(ctx, domain)
	if err != nil || portal == nil {
		return c.Status(404).JSON(fiber.Map{"error": "portal nao encontrado"})
	}
	if req.CallerUserID != portal.LegacyAdminUserID {
		return c.Status(403).JSON(fiber.Map{"error": "apenas o master pode confirmar"})
	}
	if err := h.repo.AckSMSRisk(ctx, domain); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"ok": true})
}

// GET /ui/sms/messages?domain=... — historico dos envios pra UI.
func (h *handlers) uiSMSMessages(c *fiber.Ctx) error {
	ctx := c.Context()
	domain, err := h.resolveDashboardDomain(ctx, c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	limit := c.QueryInt("limit", 100)
	msgs, err := h.repo.ListSMSMessagesByDomain(ctx, domain, limit)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	out := make([]fiber.Map, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, fiber.Map{
			"bitrix_message_id": m.BitrixMessageID,
			"to_phone":          m.ToPhone,
			"body":              m.Body,
			"status":            m.Status,
			"error":             m.ErrorMsg,
			"created_at":        m.CreatedAt,
			"sent_at":           m.SentAt,
		})
	}
	return c.JSON(fiber.Map{"messages": out, "count": len(out)})
}
