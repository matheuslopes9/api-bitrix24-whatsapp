// bp_robot.go — 2 atividades BizProc no menu CRM > Automacoes:
//
//   [UC Talk] WhatsApp — Nao Oficial
//      CODE: uctalk_wa_unofficial
//      Modo: texto livre via Multi-Device (whatsmeow)
//      Campos: to_phone, message (texto livre), template_id (opcional)
//      Risco: pode banir o numero se usado em massa pra contatos frios
//
//   [UC Talk] WhatsApp — Oficial (HSM)
//      CODE: uctalk_wa_official
//      Modo: template HSM via Cloud API (Meta) — caminho seguro
//      Campos: to_phone, template_name (dropdown atualizado da Meta),
//              var_1..var_10 (preenchidos conforme template usar)
//      Sem campo de texto livre: Meta exige template aprovado.
//
// O dropdown de template_name e' montado no register-time com os
// meta_template_name dos templates aprovados ja importados no /dashboard.
// Quando o cliente importa templates novos da Meta, chamamos
// ReregisterBPRobotOfficialForPortal pra atualizar o dropdown.

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

// Codes dos 2 robots no Bitrix24.
const (
	BPRobotCodeUnofficial = "uctalk_wa_unofficial"
	BPRobotCodeOfficial   = "uctalk_wa_official"

	// Legado — primeira versao tinha 1 robot so com campo "mode".
	// Mantido pra cleanup no install: deletamos se ainda existir.
	BPRobotCodeLegacy = "uctalk_send_whatsapp"
)

// bpRobotHandlerPath: handler unico recebe POSTs dos 2 robots. Diferenciamos
// pelo campo "code" do payload Bitrix (ele mesmo manda o CODE do robot).
const bpRobotHandlerPath = "/bitrix/bp/send"

// RegisterBPRobotForPortal e' chamado no install do app. Best-effort:
// nao quebra install se falhar. Limpa robot legado, registra os 2 novos.
func (h *handlers) RegisterBPRobotForPortal(ctx context.Context, portal *db.BitrixPortal) {
	if portal == nil {
		return
	}
	creds := h.portalToCreds(portal)
	// Limpa robot legado (1 commit antigo) se existir. Best-effort, ignora erro.
	_ = h.bitrixClient.DeleteBPRobot(ctx, creds, BPRobotCodeLegacy)

	handlerURL := h.cfg.App.BaseURL() + bpRobotHandlerPath

	// 1) Robot Nao Oficial — texto livre Multi-Device
	if err := h.bitrixClient.RegisterBPRobot(ctx, creds,
		BPRobotCodeUnofficial,
		"UC Talk — WhatsApp (Nao Oficial)",
		handlerURL,
		bpPropsUnofficial(),
	); err != nil {
		// "ja existe" e' OK — Bitrix gravou em algum register anterior.
		if !strings.Contains(err.Error(), "ALREADY_INSTALLED") {
			h.log.Warn("bp-robot: register unofficial failed",
				zap.String("domain", portal.Domain), zap.Error(err))
		}
	} else {
		h.log.Info("bp-robot: unofficial registered",
			zap.String("domain", portal.Domain))
	}

	// 2) Robot Oficial — template HSM. Monta dropdown com templates ja
	// importados da Meta no dominio.
	tplOptions, _ := h.metaTemplateOptions(ctx, portal.Domain)
	if err := h.bitrixClient.RegisterBPRobot(ctx, creds,
		BPRobotCodeOfficial,
		"UC Talk — WhatsApp Oficial (HSM)",
		handlerURL,
		bpPropsOfficial(tplOptions),
	); err != nil {
		if !strings.Contains(err.Error(), "ALREADY_INSTALLED") {
			h.log.Warn("bp-robot: register official failed",
				zap.String("domain", portal.Domain), zap.Error(err))
		}
	} else {
		h.log.Info("bp-robot: official registered",
			zap.String("domain", portal.Domain),
			zap.Int("templates", len(tplOptions)))
	}
}

// ReregisterBPRobotOfficialForPortal atualiza so o robot Oficial — usado
// apos importar templates da Meta pra refrescar o dropdown. Idempotente.
func (h *handlers) ReregisterBPRobotOfficialForPortal(ctx context.Context, portal *db.BitrixPortal) error {
	if portal == nil {
		return fmt.Errorf("portal nil")
	}
	creds := h.portalToCreds(portal)
	// Delete + add: Bitrix nao tem "update" idempotente pro dropdown.
	_ = h.bitrixClient.DeleteBPRobot(ctx, creds, BPRobotCodeOfficial)
	tplOptions, _ := h.metaTemplateOptions(ctx, portal.Domain)
	handlerURL := h.cfg.App.BaseURL() + bpRobotHandlerPath
	return h.bitrixClient.RegisterBPRobot(ctx, creds,
		BPRobotCodeOfficial,
		"UC Talk — WhatsApp Oficial (HSM)",
		handlerURL,
		bpPropsOfficial(tplOptions),
	)
}

// UnregisterBPRobotForPortal: cleanup completo no uninstall.
func (h *handlers) UnregisterBPRobotForPortal(ctx context.Context, portal *db.BitrixPortal) {
	if portal == nil {
		return
	}
	creds := h.portalToCreds(portal)
	_ = h.bitrixClient.DeleteBPRobot(ctx, creds, BPRobotCodeUnofficial)
	_ = h.bitrixClient.DeleteBPRobot(ctx, creds, BPRobotCodeOfficial)
	_ = h.bitrixClient.DeleteBPRobot(ctx, creds, BPRobotCodeLegacy)
}

// bpPropsUnofficial: campos do robot Nao Oficial.
func bpPropsUnofficial() map[string]interface{} {
	return map[string]interface{}{
		"to_phone": map[string]interface{}{
			"Name":        map[string]string{"en": "Recipient phone", "pt-BR": "Telefone do destinatario"},
			"Description": map[string]string{"pt-BR": "Numero WhatsApp do contato (use {{Lead.Phone}} ou variavel similar)."},
			"Type":        "string",
			"Required":    "Y",
			"Multiple":    "N",
		},
		"message": map[string]interface{}{
			"Name":        map[string]string{"en": "Message text", "pt-BR": "Mensagem (texto livre)"},
			"Description": map[string]string{"pt-BR": "Texto que sera enviado. Pode usar variaveis do CRM tipo {{Lead.Name}}. Para listas frias, use o robot Oficial em vez deste."},
			"Type":        "string",
			"Required":    "Y",
			"Multiple":    "N",
		},
		"template_id": map[string]interface{}{
			"Name":        map[string]string{"en": "Template UUID (optional)", "pt-BR": "ID do template Nao Oficial (opcional)"},
			"Description": map[string]string{"pt-BR": "Se preenchido, usa o body do template em /dashboard > Templates > Nao Oficial em vez do campo Mensagem. Cole o UUID."},
			"Type":        "string",
			"Required":    "N",
			"Multiple":    "N",
		},
	}
}

// bpPropsOfficial: campos do robot Oficial. Recebe lista de templates Meta
// pra montar dropdown.
func bpPropsOfficial(tplOptions map[string]string) map[string]interface{} {
	// Se nao ha templates Meta cadastrados, dropdown fica vazio mas robot
	// continua registrado (cliente vai ver mensagem ao tentar usar).
	tplProp := map[string]interface{}{
		"Name":        map[string]string{"en": "Meta template", "pt-BR": "Template aprovado pela Meta"},
		"Description": map[string]string{"pt-BR": "Selecione o template HSM ja aprovado pela Meta. Para adicionar mais opcoes, importe em /dashboard > Templates > Oficial Meta."},
		"Type":        "select",
		"Required":    "Y",
		"Options":     tplOptions,
	}

	props := map[string]interface{}{
		"to_phone": map[string]interface{}{
			"Name":        map[string]string{"en": "Recipient phone", "pt-BR": "Telefone do destinatario"},
			"Description": map[string]string{"pt-BR": "Numero WhatsApp do contato (use {{Lead.Phone}} ou variavel similar)."},
			"Type":        "string",
			"Required":    "Y",
			"Multiple":    "N",
		},
		"template_name": tplProp,
	}
	// 10 campos de variaveis. Operador preenche apenas as que o template usa.
	for i := 1; i <= 10; i++ {
		key := fmt.Sprintf("var_%d", i)
		props[key] = map[string]interface{}{
			"Name":        map[string]string{"pt-BR": fmt.Sprintf("Variavel {{%d}} do template", i)},
			"Description": map[string]string{"pt-BR": fmt.Sprintf("Valor para {{%d}} no template Meta. Deixe vazio se o template nao usa essa posicao. Pode usar variaveis CRM como {{Lead.Name}}.", i)},
			"Type":        "string",
			"Required":    "N",
			"Multiple":    "N",
		}
	}
	return props
}

// metaTemplateOptions monta o map {nome: label} dos templates HSM do dominio.
// label e' "nome (idioma)" pra desambiguar.
func (h *handlers) metaTemplateOptions(ctx context.Context, domain string) (map[string]string, error) {
	tpls, err := h.repo.ListMessageTemplates(ctx, domain)
	if err != nil {
		return map[string]string{}, err
	}
	out := map[string]string{}
	for _, t := range tpls {
		if t.MetaTemplateName == "" {
			continue
		}
		label := t.MetaTemplateName
		if t.MetaTemplateLang != "" {
			label += " (" + t.MetaTemplateLang + ")"
		}
		out[t.MetaTemplateName] = label
	}
	return out, nil
}

// ─── Handler unico recebe POST dos 2 robots, diferencia pelo CODE ─────────

// POST /bitrix/bp/send — Bitrix dispara aqui. Body form-encoded inclui
// "code" indicando qual robot (uctalk_wa_unofficial | uctalk_wa_official).
//
// Resposta rapida (200) e processamento em goroutine — BizProc nao espera
// ack de delivery.
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
		h.log.Warn("bp-robot: tenant has no default session — drop",
			zap.String("domain", domain))
		return c.JSON(fiber.Map{"result": "no_session"})
	}

	code := strings.TrimSpace(c.FormValue("code"))
	toPhone := normalizeWAPhone(c.FormValue("properties[to_phone]"))
	if toPhone == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "to_phone vazio"})
	}

	switch code {
	case BPRobotCodeOfficial:
		return h.bpHandleOfficial(c, portal, toPhone, domain)
	case BPRobotCodeUnofficial, BPRobotCodeLegacy, "": // legacy/empty cai aqui
		return h.bpHandleUnofficial(c, portal, toPhone, domain)
	default:
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "code desconhecido: " + code})
	}
}

func (h *handlers) bpHandleUnofficial(c *fiber.Ctx, portal *db.BitrixPortal, toPhone, domain string) error {
	rawMessage := c.FormValue("properties[message]")
	templateIDStr := strings.TrimSpace(c.FormValue("properties[template_id]"))

	// Se preencheu template_id, usa o body dele. Senao, usa o campo message.
	bodyText := rawMessage
	if templateIDStr != "" {
		tid, perr := uuid.Parse(templateIDStr)
		if perr != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "template_id invalido"})
		}
		tpl, err := h.repo.GetMessageTemplateByID(c.Context(), tid, domain)
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "template nao encontrado"})
		}
		// Template Oficial nao deveria ser usado aqui — texto livre quebra
		// a logica HSM. Recusa com log claro.
		if tpl.MetaTemplateName != "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "template_id aponta para um template Oficial Meta. Use o robot 'UC Talk — Oficial (HSM)' em vez deste.",
			})
		}
		bodyText = tpl.Body
	}
	if strings.TrimSpace(bodyText) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "mensagem vazia"})
	}

	go h.processBPRobotSend(&bpSendJob{
		domain:     domain,
		sessionJID: portal.DefaultSMSSessionJID,
		toPhone:    toPhone,
		mode:       "unofficial",
		bodyText:   bodyText,
	})
	return c.JSON(fiber.Map{"result": "queued", "mode": "unofficial"})
}

func (h *handlers) bpHandleOfficial(c *fiber.Ctx, portal *db.BitrixPortal, toPhone, domain string) error {
	templateName := strings.TrimSpace(c.FormValue("properties[template_name]"))
	if templateName == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "template_name obrigatorio"})
	}

	// Resolve por nome dentro do dominio.
	tpls, err := h.repo.ListMessageTemplates(c.Context(), domain)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	var tpl *db.MessageTemplate
	for _, t := range tpls {
		if t.MetaTemplateName == templateName {
			tpl = t
			break
		}
	}
	if tpl == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "template Meta nao encontrado: " + templateName + " — importe em /dashboard > Templates > Oficial",
		})
	}
	if tpl.MetaTemplateLang == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "template sem idioma configurado"})
	}

	// Sessao Oficial = Cloud. Se default e' Multi-Device, recusa.
	if !strings.HasPrefix(portal.DefaultSMSSessionJID, "cloud:") {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "robot Oficial exige sessao padrao Cloud API. Atual e' Multi-Device — configure em /dashboard > Campanhas SMS.",
		})
	}

	// Coleta variaveis var_1..var_10 (em ordem; pula vazias no fim).
	var vars []string
	for i := 1; i <= 10; i++ {
		v := strings.TrimSpace(c.FormValue(fmt.Sprintf("properties[var_%d]", i)))
		if v == "" {
			// Vazia no meio nao corta — Meta pode falhar se variavel ficar
			// em branco, mas isso e' problema do template em si. Mandamos
			// como veio pra erro ser claro.
			vars = append(vars, "")
		} else {
			vars = append(vars, v)
		}
	}
	// Truncar trailing vazios (Meta nao gosta de array com vazios no fim
	// se o template nao tem todas as posicoes).
	for len(vars) > 0 && vars[len(vars)-1] == "" {
		vars = vars[:len(vars)-1]
	}

	go h.processBPRobotSend(&bpSendJob{
		domain:       domain,
		sessionJID:   portal.DefaultSMSSessionJID,
		toPhone:      toPhone,
		mode:         "official",
		templateName: tpl.MetaTemplateName,
		templateLang: tpl.MetaTemplateLang,
		templateVars: vars,
	})
	return c.JSON(fiber.Map{"result": "queued", "mode": "official", "template": tpl.MetaTemplateName})
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
		waMsgID, sendErr = h.cloudMgr.SendTemplate(ctx, j.sessionJID, j.toPhone,
			j.templateName, j.templateLang, j.templateVars)

	case j.mode == "official" && !isCloud:
		sendErr = fmt.Errorf("modo Oficial exige sessao Cloud API; sessao default e' Multi-Device")

	case isCloud:
		waMsgID, sendErr = h.cloudMgr.SendText(ctx, j.sessionJID, j.toPhone, j.bodyText)

	default:
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
