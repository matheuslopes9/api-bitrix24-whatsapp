// bp_robot.go — Bitrix24 BizProc Activity ("Robot") customizado.
//
// O Bitrix24 oferece "CRM > Configuracoes > Automacoes" onde o cliente
// monta workflows arrastando atividades. Nosso app se registra como uma
// atividade "UC Talk: Enviar WhatsApp" via bizproc.robot.add. Quando o
// robot dispara (ex: lead muda de estagio), Bitrix POSTa no nosso handler
// com os campos preenchidos pelo cliente (destinatario, modo, template,
// mensagem).
//
// Modos:
//   - "unofficial": texto livre via Multi-Device (whatsmeow). Usa gate
//     compartilhado (5s/typing, ver wa_send_gate.go). Alto risco de
//     banimento se usado pra disparo frio em massa.
//   - "official": Cloud API + template HSM aprovado pela Meta. Caminho
//     seguro pra cold outreach. Cliente precisa cadastrar o template
//     em /dashboard > Templates com meta_template_name preenchido.
//
// Isolamento: tudo aqui e' novo, nao toca em CRM tab/SMS provider/etc.
// Reusa apenas o gate compartilhado e bitrixClient.UpdateSMSMessageStatus
// (que vale pra qualquer "messageservice" no Bitrix — mas robot nao usa
// porque BizProc nao reporta status, e' fire-and-forget).

package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"go.uber.org/zap"
)

// BPRobotCode: identificador unico do robot no Bitrix24.
const BPRobotCode = "uctalk_send_whatsapp"

// bpRobotHandlerPath: rota onde Bitrix POSTa quando o robot dispara.
const bpRobotHandlerPath = "/bitrix/bp/send"

// RegisterBPRobotForPortal e' chamado no install do app. Best-effort:
// se falhar (scope `bizproc` faltando no manifest), nao quebra install.
func (h *handlers) RegisterBPRobotForPortal(ctx context.Context, portal *db.BitrixPortal) {
	if portal == nil {
		return
	}
	creds := h.portalToCreds(portal)
	handlerURL := h.cfg.App.BaseURL() + bpRobotHandlerPath
	err := h.bitrixClient.RegisterBPRobot(ctx, creds, BPRobotCode, "UC Talk: Enviar WhatsApp", handlerURL)
	if err != nil {
		h.log.Warn("bp-robot: register failed (will keep app working anyway)",
			zap.String("domain", portal.Domain), zap.Error(err))
		return
	}
	h.log.Info("bp-robot: registered",
		zap.String("domain", portal.Domain), zap.String("handler", handlerURL))
}

// UnregisterBPRobotForPortal: cleanup no uninstall. Best-effort.
func (h *handlers) UnregisterBPRobotForPortal(ctx context.Context, portal *db.BitrixPortal) {
	if portal == nil {
		return
	}
	creds := h.portalToCreds(portal)
	if err := h.bitrixClient.DeleteBPRobot(ctx, creds, BPRobotCode); err != nil {
		h.log.Warn("bp-robot: unregister failed",
			zap.String("domain", portal.Domain), zap.Error(err))
	}
}

// POST /bitrix/bp/send — Bitrix dispara aqui quando o robot executa.
// Form-encoded payload com properties[*] e auth[*].
//
// Responde 200 RAPIDO (em background processa o envio). BizProc nao
// espera ack de delivery — fire-and-forget na perspectiva do robot.
func (h *handlers) bpRobotSend(c *fiber.Ctx) error {
	domainRaw := c.FormValue("auth[domain]")
	domain := normalizePortalDomain(domainRaw)
	if domain == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "auth missing"})
	}
	portal, err := h.repo.GetBitrixPortalByDomain(c.Context(), domain)
	if err != nil || portal == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "portal not found"})
	}
	if portal.DefaultSMSSessionJID == "" {
		// Robot reusa a sessao default do tenant (mesma config de SMS
		// Campaigns). Sem sessao configurada, robot nao envia.
		h.log.Warn("bp-robot: tenant has no default session — drop",
			zap.String("domain", domain))
		return c.JSON(fiber.Map{"result": "no_session"})
	}

	toPhone := normalizeWAPhone(c.FormValue("properties[to_phone]"))
	mode := strings.TrimSpace(c.FormValue("properties[mode]"))
	if mode == "" {
		mode = "unofficial"
	}
	templateIDStr := strings.TrimSpace(c.FormValue("properties[template_id]"))
	rawMessage := c.FormValue("properties[message]")
	rawVars := c.FormValue("properties[template_vars]")

	if toPhone == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "to_phone vazio"})
	}

	// Resolve template (se ID veio). Necessario pra modo "official"; opcional
	// pra "unofficial" — body do template substitui a propriedade "message".
	var tpl *db.MessageTemplate
	if templateIDStr != "" {
		tid, perr := uuid.Parse(templateIDStr)
		if perr != nil {
			h.log.Warn("bp-robot: template_id invalido",
				zap.String("domain", domain), zap.String("value", templateIDStr))
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "template_id invalido"})
		}
		tpl, err = h.repo.GetMessageTemplateByID(c.Context(), tid, domain)
		if err != nil {
			h.log.Warn("bp-robot: template nao encontrado",
				zap.String("domain", domain), zap.String("template_id", templateIDStr))
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "template nao encontrado"})
		}
	}

	if mode == "official" {
		if tpl == nil || tpl.MetaTemplateName == "" || tpl.MetaTemplateLang == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "modo Oficial exige template com meta_template_name e meta_template_lang preenchidos",
			})
		}
	}

	// Decide texto/template a enviar.
	var bodyText string
	var templateVars []string
	if tpl != nil {
		bodyText = tpl.Body
		if rawVars != "" {
			parts := strings.Split(rawVars, "|")
			for _, p := range parts {
				templateVars = append(templateVars, strings.TrimSpace(p))
			}
		}
	} else {
		bodyText = rawMessage
	}
	if bodyText == "" && (tpl == nil || tpl.MetaTemplateName == "") {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "mensagem vazia"})
	}

	// Snapshot pra goroutine (Fiber ctx morre apos return).
	job := &bpSendJob{
		domain:       domain,
		sessionJID:   portal.DefaultSMSSessionJID,
		toPhone:      toPhone,
		mode:         mode,
		bodyText:     bodyText,
		templateName: stringOrEmpty(tpl, func(t *db.MessageTemplate) string { return t.MetaTemplateName }),
		templateLang: stringOrEmpty(tpl, func(t *db.MessageTemplate) string { return t.MetaTemplateLang }),
		templateVars: templateVars,
	}
	go h.processBPRobotSend(job)

	return c.JSON(fiber.Map{"result": "queued"})
}

type bpSendJob struct {
	domain       string
	sessionJID   string
	toPhone      string
	mode         string // unofficial | official
	bodyText     string
	templateName string
	templateLang string
	templateVars []string
}

func (h *handlers) processBPRobotSend(j *bpSendJob) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Gate de sessao + intervalo minimo (mesmo do SMS).
	gate := GetWASessionGate(j.sessionJID)
	gate.Lock()
	if !gate.WaitMinInterval(ctx.Done()) {
		gate.Unlock()
		h.log.Warn("bp-robot: context cancelled in queue",
			zap.String("domain", j.domain), zap.String("to", j.toPhone))
		return
	}

	var waMsgID string
	var sendErr error
	isCloud := strings.HasPrefix(j.sessionJID, "cloud:")

	switch {
	case j.mode == "official" && isCloud:
		// Caminho oficial — template HSM via Cloud API.
		waMsgID, sendErr = h.cloudMgr.SendTemplate(ctx, j.sessionJID, j.toPhone,
			j.templateName, j.templateLang, j.templateVars)

	case j.mode == "official" && !isCloud:
		// Cliente pediu Oficial mas sessao padrao e' Multi-Device. Sem
		// caminho oficial — recusa com log claro.
		sendErr = fmt.Errorf("modo Oficial exige sessao Cloud API; sessao default e' Multi-Device")

	case isCloud:
		// Modo nao oficial mas sessao e' Cloud — manda texto livre (sujeito
		// a regra das 24h do Meta).
		waMsgID, sendErr = h.cloudMgr.SendText(ctx, j.sessionJID, j.toPhone, j.bodyText)

	default:
		// Modo nao oficial via Multi-Device. Simula typing + envia.
		toJID := j.toPhone + "@s.whatsapp.net"
		h.waManager.SendTyping(ctx, j.sessionJID, toJID, WAHumanTypingDuration(j.bodyText))
		waMsgID, sendErr = h.waManager.Send(ctx, j.sessionJID, toJID, j.bodyText)
	}

	gate.MarkSent()
	gate.Unlock()

	if sendErr != nil {
		h.log.Warn("bp-robot: send failed",
			zap.String("domain", j.domain),
			zap.String("session", j.sessionJID),
			zap.String("to", j.toPhone),
			zap.String("mode", j.mode),
			zap.Error(sendErr))
		return
	}
	h.log.Info("bp-robot: sent",
		zap.String("domain", j.domain),
		zap.String("to", j.toPhone),
		zap.String("mode", j.mode),
		zap.String("wa_message_id", waMsgID))
}

// stringOrEmpty: helper pra evitar nil-check verboso quando extraindo
// campos de *db.MessageTemplate.
func stringOrEmpty(t *db.MessageTemplate, f func(*db.MessageTemplate) string) string {
	if t == nil {
		return ""
	}
	return f(t)
}
