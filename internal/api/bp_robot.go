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

	// 1) Robot Nao Oficial — dropdowns de sessao QR + template Nao Oficial.
	sessionsQR, _ := h.sessionOptionsForDomain(ctx, portal.Domain, false)
	tplUnofficial, _ := h.unofficialTemplateOptions(ctx, portal.Domain)
	if err := h.bitrixClient.RegisterBPRobot(ctx, creds,
		BPRobotCodeUnofficial,
		"UC Talk — WhatsApp (Nao Oficial)",
		handlerURL,
		bpPropsUnofficial(sessionsQR, tplUnofficial),
	); err != nil {
		// "ja existe" e' OK — Bitrix gravou em algum register anterior.
		if !strings.Contains(err.Error(), "ALREADY_INSTALLED") {
			h.log.Warn("bp-robot: register unofficial failed",
				zap.String("domain", portal.Domain), zap.Error(err))
		}
	} else {
		h.log.Info("bp-robot: unofficial registered",
			zap.String("domain", portal.Domain),
			zap.Int("sessions", len(sessionsQR)),
			zap.Int("templates", len(tplUnofficial)))
	}

	// 2) Robot Oficial — dropdowns de sessao Cloud + template HSM.
	sessionsCloud, _ := h.sessionOptionsForDomain(ctx, portal.Domain, true)
	tplOfficial, _ := h.metaTemplateOptions(ctx, portal.Domain)
	if err := h.bitrixClient.RegisterBPRobot(ctx, creds,
		BPRobotCodeOfficial,
		"UC Talk — WhatsApp Oficial (HSM)",
		handlerURL,
		bpPropsOfficial(sessionsCloud, tplOfficial),
	); err != nil {
		if !strings.Contains(err.Error(), "ALREADY_INSTALLED") {
			h.log.Warn("bp-robot: register official failed",
				zap.String("domain", portal.Domain), zap.Error(err))
		}
	} else {
		h.log.Info("bp-robot: official registered",
			zap.String("domain", portal.Domain),
			zap.Int("sessions", len(sessionsCloud)),
			zap.Int("templates", len(tplOfficial)))
	}
}

// ReregisterBPRobotOfficialForPortal atualiza so o robot Oficial — usado
// apos importar templates Meta ou conectar/desconectar sessoes Cloud.
// Idempotente.
func (h *handlers) ReregisterBPRobotOfficialForPortal(ctx context.Context, portal *db.BitrixPortal) error {
	if portal == nil {
		return fmt.Errorf("portal nil")
	}
	creds := h.portalToCreds(portal)
	// Delete + add: Bitrix nao tem "update" idempotente pro dropdown.
	_ = h.bitrixClient.DeleteBPRobot(ctx, creds, BPRobotCodeOfficial)
	sessionsCloud, _ := h.sessionOptionsForDomain(ctx, portal.Domain, true)
	tplOptions, _ := h.metaTemplateOptions(ctx, portal.Domain)
	handlerURL := h.cfg.App.BaseURL() + bpRobotHandlerPath
	return h.bitrixClient.RegisterBPRobot(ctx, creds,
		BPRobotCodeOfficial,
		"UC Talk — WhatsApp Oficial (HSM)",
		handlerURL,
		bpPropsOfficial(sessionsCloud, tplOptions),
	)
}

// triggerBPRobotRefresh dispara re-register dos 2 robots em goroutine —
// usado quando algo que afeta os dropdowns muda (template CRUD, sessao
// conectada/desconectada). Best-effort: log + ignora erro.
//
// Recebe domain (nao portal) por conveniencia: handlers tem o domain
// imediato; portal lookup acontece dentro da goroutine.
func (h *handlers) triggerBPRobotRefresh(domain, reason string) {
	if domain == "" {
		return
	}
	go func() {
		ctx := context.Background()
		portal, err := h.repo.GetBitrixPortalByDomain(ctx, normalizePortalDomain(domain))
		if err != nil || portal == nil {
			h.log.Warn("bp-robot refresh: portal lookup falhou",
				zap.String("domain", domain),
				zap.String("reason", reason),
				zap.Error(err))
			return
		}
		// Cleanup legacy robot (uctalk_send_whatsapp). Best-effort.
		// Garante que qualquer refresh tambem limpa o robot antigo caso
		// tenha sobrado de instalacao anterior — sem precisar reinstalar.
		creds := h.portalToCreds(portal)
		_ = h.bitrixClient.DeleteBPRobot(ctx, creds, BPRobotCodeLegacy)

		if err := h.ReregisterBPRobotUnofficialForPortal(ctx, portal); err != nil {
			h.log.Warn("bp-robot refresh: unofficial falhou",
				zap.String("domain", domain), zap.String("reason", reason), zap.Error(err))
		}
		if err := h.ReregisterBPRobotOfficialForPortal(ctx, portal); err != nil {
			h.log.Warn("bp-robot refresh: official falhou",
				zap.String("domain", domain), zap.String("reason", reason), zap.Error(err))
		}
		h.log.Info("bp-robot refresh: done",
			zap.String("domain", domain), zap.String("reason", reason))
	}()
}

// OnSessionConnect e' o callback injetado no whatsapp.Manager
// (SetSessionConnectHandler). Disparado quando uma sessao QR fica ativa
// (pareamento via QR novo ou reconexao). Resolve o dominio Bitrix da
// sessao pelo JID e dispara o refresh dos robots BizProc — assim o
// dropdown de "WhatsApp session" no robot passa a listar o numero recem
// conectado SEM o cliente precisar rodar refresh manual.
//
// LACUNA QUE ISSO FECHA: antes, o refresh so' era disparado por CRUD de
// template. Conectar um QR novo nao re-registrava o robot, entao o
// dropdown de sessao ficava vazio ate o cliente mexer num template ou
// clicar refresh manual. Agora conectar a sessao ja' popula o dropdown.
//
// Best-effort e assincrono (ja' vem em goroutine do fireSessionConnect):
// falha no lookup ou refresh nao afeta a conexao WhatsApp.
func (h *handlers) OnSessionConnect(phone, jid string) {
	if jid == "" {
		return
	}
	ctx := context.Background()
	acct, err := h.repo.GetBitrixAccountByJID(ctx, jid)
	if err != nil || acct == nil || acct.Domain == "" {
		// Sessao ainda nao vinculada a um portal Bitrix (ex: acabou de
		// parear e o vinculo e' criado depois). Sem dominio, nao ha robot
		// pra refresh — no-op silencioso (Debug, nao Warn: e' esperado).
		h.log.Debug("bp-robot: OnSessionConnect sem dominio vinculado ainda",
			zap.String("phone", phone), zap.String("jid", jid))
		return
	}
	h.triggerBPRobotRefresh(acct.Domain, "session_connected")
}

// ReregisterBPRobotUnofficialForPortal atualiza so o robot Nao Oficial.
// Chamado quando CRUD de templates Nao Oficiais ou conexao QR muda. Idempotente.
func (h *handlers) ReregisterBPRobotUnofficialForPortal(ctx context.Context, portal *db.BitrixPortal) error {
	if portal == nil {
		return fmt.Errorf("portal nil")
	}
	creds := h.portalToCreds(portal)
	_ = h.bitrixClient.DeleteBPRobot(ctx, creds, BPRobotCodeUnofficial)
	sessionsQR, _ := h.sessionOptionsForDomain(ctx, portal.Domain, false)
	tplOptions, _ := h.unofficialTemplateOptions(ctx, portal.Domain)
	handlerURL := h.cfg.App.BaseURL() + bpRobotHandlerPath
	return h.bitrixClient.RegisterBPRobot(ctx, creds,
		BPRobotCodeUnofficial,
		"UC Talk — WhatsApp (Nao Oficial)",
		handlerURL,
		bpPropsUnofficial(sessionsQR, tplOptions),
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

// bpPropsUnofficial: campos do robot Nao Oficial. Recebe dropdown de
// sessoes QR (Multi-Device) ativas e dropdown de templates Nao Oficiais
// (manuais, sem meta_template_name).
func bpPropsUnofficial(sessionOptions, tplOptions map[string]string) map[string]interface{} {
	return map[string]interface{}{
		"session_jid": map[string]interface{}{
			"Name":        map[string]string{"en": "WhatsApp session", "pt-BR": "Sessao WhatsApp (Multi-Device)"},
			"Description": map[string]string{"pt-BR": "Numero conectado via QR Code que fara o disparo. Para adicionar, conecte em /dashboard > Sessoes WhatsApp."},
			"Type":        "select",
			"Required":    "Y",
			"Options":     sessionOptions,
		},
		"to_phone": map[string]interface{}{
			"Name":        map[string]string{"en": "Recipient phone", "pt-BR": "Telefone do destinatario"},
			"Description": map[string]string{"pt-BR": "Numero WhatsApp do contato (use {{Lead.Phone}} ou variavel similar do CRM)."},
			"Type":        "string",
			"Required":    "Y",
			"Multiple":    "N",
		},
		"template_id": map[string]interface{}{
			"Name":        map[string]string{"en": "Message template", "pt-BR": "Template Nao Oficial"},
			"Description": map[string]string{"pt-BR": "Escolha um template ja cadastrado em /dashboard > Templates > Nao Oficial. Variaveis CRM como {{Lead.Name}} no body do template sao substituidas automaticamente."},
			"Type":        "select",
			"Required":    "Y",
			"Options":     tplOptions,
		},
	}
}

// bpPropsOfficial: campos do robot Oficial. Recebe dropdown de sessoes
// Cloud ativas + dropdown de templates Meta HSM.
func bpPropsOfficial(sessionOptions, tplOptions map[string]string) map[string]interface{} {
	props := map[string]interface{}{
		"session_jid": map[string]interface{}{
			"Name":        map[string]string{"en": "WhatsApp Cloud session", "pt-BR": "Sessao WhatsApp Cloud API"},
			"Description": map[string]string{"pt-BR": "Numero conectado via Cloud API (Meta) que fara o disparo do template HSM. Configure em /dashboard > Sessoes WhatsApp."},
			"Type":        "select",
			"Required":    "Y",
			"Options":     sessionOptions,
		},
		"to_phone": map[string]interface{}{
			"Name":        map[string]string{"en": "Recipient phone", "pt-BR": "Telefone do destinatario"},
			"Description": map[string]string{"pt-BR": "Numero WhatsApp do contato (use {{Lead.Phone}} ou variavel similar do CRM)."},
			"Type":        "string",
			"Required":    "Y",
			"Multiple":    "N",
		},
		"template_name": map[string]interface{}{
			"Name":        map[string]string{"en": "Meta template", "pt-BR": "Template aprovado pela Meta"},
			"Description": map[string]string{"pt-BR": "Selecione o template HSM ja aprovado pela Meta. Para adicionar mais opcoes, importe em /dashboard > Templates > Oficial Meta."},
			"Type":        "select",
			"Required":    "Y",
			"Options":     tplOptions,
		},
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

// unofficialTemplateOptions monta o map {uuid: titulo} dos templates Nao
// Oficiais (manuais, sem meta_template_name). Key e' o UUID porque o robot
// resolve por ID — title pode ter caractere especial e mudar com tempo.
func (h *handlers) unofficialTemplateOptions(ctx context.Context, domain string) (map[string]string, error) {
	tpls, err := h.repo.ListMessageTemplates(ctx, domain)
	if err != nil {
		return map[string]string{}, err
	}
	out := map[string]string{}
	for _, t := range tpls {
		if t.MetaTemplateName != "" {
			continue // pula Oficial
		}
		out[t.ID.String()] = t.Title
	}
	return out, nil
}

// sessionOptionsForDomain monta o map {jid: label} das sessoes ativas do
// dominio filtradas por tipo. Pra Oficial: prefixa "cloud:" no JID porque
// e' assim que o cloudMgr identifica. Pra Nao Oficial: JID cru do QR.
func (h *handlers) sessionOptionsForDomain(ctx context.Context, domain string, wantCloud bool) (map[string]string, error) {
	sessions, err := h.repo.ListSessionsByDomain(ctx, domain)
	if err != nil {
		return map[string]string{}, err
	}
	out := map[string]string{}
	for _, s := range sessions {
		if s.Status != db.SessionActive {
			continue
		}
		isCloud := s.Type == db.SessionTypeCloudAPI
		if isCloud != wantCloud {
			continue
		}
		// Label: telefone + display_name pra operador identificar.
		phone := s.Phone
		if phone == "" {
			phone = s.CloudDisplayPhone
		}
		label := phone
		if s.DisplayName != "" {
			label = phone + " — " + s.DisplayName
		}
		if isCloud {
			out["cloud:"+s.JID] = label
		} else {
			out[s.JID] = label
		}
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
	appToken := c.FormValue("auth[application_token]")
	// SEGURANCA: valida application_token contra o que persistimos no install.
	// Bloqueia atacante anonimo mandando POST com auth[domain] forjado.
	portal, err := h.validateBitrixAppToken(c.Context(), domain, appToken)
	if err != nil {
		h.log.Warn("bp-robot: auth invalid",
			zap.String("domain", domain), zap.Error(err))
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "auth invalid"})
	}
	// PLAN GATE: BizProc robots sao feature Pro (envio automatizado em massa).
	plan, _ := h.repo.GetTenantPlan(c.Context(), portal.Domain)
	if plan == nil || !plan.HasProFeatures() {
		h.log.Warn("bp-robot: plano Pro requerido",
			zap.String("domain", portal.Domain))
		return c.Status(fiber.StatusPaymentRequired).JSON(fiber.Map{
			"error": "Robots BizProc sao feature do plano Pro. Faca upgrade pra usar.",
			"code":  "plan_pro_required",
		})
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
	sessionJID := strings.TrimSpace(c.FormValue("properties[session_jid]"))
	templateIDStr := strings.TrimSpace(c.FormValue("properties[template_id]"))

	if sessionJID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "session_jid obrigatorio — escolha a sessao WhatsApp no dropdown do robot",
		})
	}
	// Robot Nao Oficial so aceita sessao QR/Multi-Device (sem prefix "cloud:").
	if strings.HasPrefix(sessionJID, "cloud:") {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "sessao escolhida e' Cloud API — use o robot 'UC Talk — WhatsApp Oficial (HSM)' em vez deste",
		})
	}
	if templateIDStr == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "template_id obrigatorio — escolha um template Nao Oficial no dropdown do robot",
		})
	}

	tid, perr := uuid.Parse(templateIDStr)
	if perr != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "template_id invalido"})
	}
	tpl, err := h.repo.GetMessageTemplateByID(c.Context(), tid, domain)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "template nao encontrado"})
	}
	// Defensive check: template Oficial selecionado por engano (cliente colou
	// UUID errado via API direta). Recusa com mensagem clara.
	if tpl.MetaTemplateName != "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "template_id aponta pra um template Oficial Meta. Use o robot 'UC Talk — Oficial (HSM)'.",
		})
	}
	bodyText := tpl.Body
	if strings.TrimSpace(bodyText) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "template tem body vazio"})
	}

	go h.processBPRobotSend(&bpSendJob{
		domain:     domain,
		sessionJID: sessionJID,
		toPhone:    toPhone,
		mode:       "unofficial",
		bodyText:   bodyText,
	})
	return c.JSON(fiber.Map{"result": "queued", "mode": "unofficial", "template_id": tpl.ID.String()})
}

func (h *handlers) bpHandleOfficial(c *fiber.Ctx, portal *db.BitrixPortal, toPhone, domain string) error {
	sessionJID := strings.TrimSpace(c.FormValue("properties[session_jid]"))
	templateName := strings.TrimSpace(c.FormValue("properties[template_name]"))

	if sessionJID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "session_jid obrigatorio — escolha a sessao Cloud no dropdown do robot",
		})
	}
	if !strings.HasPrefix(sessionJID, "cloud:") {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "robot Oficial exige sessao Cloud API. A escolhida e' Multi-Device — use o robot 'UC Talk — WhatsApp (Nao Oficial)'.",
		})
	}
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
		sessionJID:   sessionJID,
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
