// tenant_health.go — visao de suporte: o estado real do app de um cliente
// num unico lugar.
//
// Por que existe: ate' agora, descobrir por que o app de um cliente estava
// quebrado exigia abrir o log do container e rodar SQL na mao. Os bugs mais
// graves desta semana (token do Bitrix zerado, sessao duplicada brigando,
// ciclo de reconexao de 30s) so' apareceram porque alguem leu o log linha a
// linha. Todo o dado ja' existia — espalhado por 11 rotas de diagnostico.
// Aqui ele vira uma resposta so'.
package api

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/logbuffer"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/whatsapp"
)

// GET /admin/api/metrics — KPIs globais do painel.
func (h *handlers) adminMetrics(c *fiber.Ctx) error {
	m, err := h.repo.GetAdminMetrics(c.Context())
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(m)
}

// GET /admin/api/tenant/health?domain=... — diagnostico completo do cliente.
func (h *handlers) adminTenantHealth(c *fiber.Ctx) error {
	ctx := c.Context()
	domain := normalizePortalDomain(strings.TrimSpace(c.Query("domain")))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain obrigatorio"})
	}

	out := fiber.Map{"domain": domain, "gerado_em": time.Now().Format(time.RFC3339)}

	sessoes := h.healthSessoes(ctx, domain)
	out["bitrix"] = h.healthBitrix(ctx, domain)
	out["sessoes"] = sessoes
	out["mensagens"] = h.healthMensagens(ctx, domain)
	out["licenca"] = h.healthLicenca(ctx, domain)
	out["log"] = h.healthLog(domain, sessoes)
	out["fila"] = h.healthFila(ctx, sessoes)

	return c.JSON(out)
}

// healthBitrix: o lado Bitrix da integracao — portal, token, conector.
func (h *handlers) healthBitrix(ctx context.Context, domain string) fiber.Map {
	res := fiber.Map{}

	portal, err := h.repo.GetBitrixPortalByDomain(ctx, domain)
	if err != nil || portal == nil {
		res["instalado"] = false
		res["problema"] = "portal nao encontrado — o app nao esta instalado neste domínio"
		return res
	}
	res["instalado"] = true
	res["member_id"] = portal.MemberID
	res["open_line_id"] = portal.OpenLineID
	res["connector_id"] = portal.ConnectorID
	res["instalado_em"] = portal.InstalledAt.Format(time.RFC3339)
	if portal.OpenLineID == 0 {
		res["problema"] = "nenhuma Linha Aberta vinculada — mensagens nao tem onde chegar"
	}

	// Token. E' o item que mais deu problema: uma resposta 200-com-erro do
	// OAuth do Bitrix chegou a gravar token VAZIO por cima do bom, e o
	// sintoma so' aparecia como NO_AUTH_FOUND no log.
	tok := fiber.Map{}
	creds := h.portalToCreds(portal)
	t, terr := h.repo.GetBitrixTokenByClientID(ctx, "https://"+domain, creds.ClientID)
	if terr != nil || t == nil {
		t, terr = h.repo.GetBitrixToken(ctx, "https://"+domain)
	}
	switch {
	case terr != nil || t == nil:
		tok["estado"] = "ausente"
		tok["problema"] = "nenhum token salvo — o app precisa ser reinstalado/reautorizado"
	case t.AccessToken == "" || t.RefreshToken == "":
		tok["estado"] = "corrompido"
		tok["tem_access"] = t.AccessToken != ""
		tok["tem_refresh"] = t.RefreshToken != ""
		tok["problema"] = "token vazio no banco — reautorize o app no portal Bitrix"
	default:
		tok["estado"] = "ok"
		tok["expira_em"] = t.ExpiresAt.Format(time.RFC3339)
		tok["expirado"] = time.Now().After(t.ExpiresAt)
		tok["atualizado_em"] = t.UpdatedAt.Format(time.RFC3339)
	}
	res["token"] = tok

	// Vinculo conector <-> sessao WhatsApp. Sem ele, ProcessInbound aborta
	// com "bitrix account not found" e a mensagem do cliente morre antes de
	// chegar no Contact Center.
	accts, aerr := h.repo.ListBitrixAccountsByDomain(ctx, domain)
	if aerr == nil && len(accts) > 0 {
		lista := make([]fiber.Map, 0, len(accts))
		for _, a := range accts {
			lista = append(lista, fiber.Map{
				"session_jid":  a.SessionJID,
				"connector_id": a.ConnectorID,
				"open_line_id": a.OpenLineID,
				"status":       string(a.Status),
			})
		}
		res["conectores"] = lista
	} else {
		res["conectores"] = []fiber.Map{}
		res["problema_conector"] = "nenhuma sessao WhatsApp vinculada a uma Linha Aberta — " +
			"mensagem recebida nao chega no Contact Center"
	}
	return res
}

// healthSessoes cruza o que o BANCO diz com o que esta REALMENTE conectado
// em memoria. As duas fontes divergem com frequencia (o status no banco
// atrasa apos deploy), e a divergencia costuma ser o proprio problema.
func (h *handlers) healthSessoes(ctx context.Context, domain string) []fiber.Map {
	rows, err := h.repo.ListSessionsByDomain(ctx, domain)
	if err != nil {
		return []fiber.Map{{"erro": err.Error()}}
	}

	// Numeros realmente vivos agora, lidos do manager.
	vivos := map[string]string{} // numero base -> jid corrente
	if h.waManager != nil {
		for _, cs := range h.waManager.ConnectedSessions() {
			vivos[cs.Phone] = cs.JID
		}
	}

	out := make([]fiber.Map, 0, len(rows))
	for _, s := range rows {
		numero := whatsapp.PhoneFromJID(s.JID)
		jidVivo, conectada := vivos[numero]
		item := fiber.Map{
			"jid":             s.JID,
			"numero":          numero,
			"phone_no_banco":  s.Phone,
			"tipo":            string(s.Type),
			"status_no_banco": string(s.Status),
			"conectada_agora": conectada,
			"session_file":    s.SessionFile,
		}
		if s.LastSeen != nil {
			item["visto_em"] = s.LastSeen.Format(time.RFC3339)
		}
		// Divergencias que costumam explicar "o numero caiu".
		switch {
		case !conectada && string(s.Status) == "active":
			item["problema"] = "banco diz ativa mas nao ha' conexao viva — sessao caiu sem atualizar o status"
		case conectada && jidVivo != s.JID:
			item["problema"] = "device suffix mudou (re-pareamento): banco tem " + s.JID + ", vivo e' " + jidVivo
		}
		if s.Phone != "" && numero != "" && s.Phone != numero {
			item["problema_phone"] = "campo phone (" + s.Phone + ") diverge do numero real do JID (" + numero + ")"
		}
		out = append(out, item)
	}
	return out
}

// healthMensagens: volume e falhas.
func (h *handlers) healthMensagens(ctx context.Context, domain string) fiber.Map {
	res := fiber.Map{}
	in24, out24, err := h.repo.CountMessagesByDomain(ctx, domain, time.Now().Add(-24*time.Hour))
	if err == nil {
		res["entrada_24h"] = in24
		res["saida_24h"] = out24
		if in24 == 0 && out24 == 0 {
			res["observacao"] = "nenhuma mensagem em 24h — pode ser cliente parado ou integração quebrada"
		}
	}
	// Falhas com motivo. So' existem desde a migration 043, que criou as
	// colunas status/error_msg que faltavam.
	if falhas, ferr := h.repo.CountFailedMessagesByDomain(ctx, domain, time.Now().Add(-7*24*time.Hour)); ferr == nil {
		res["falhas_7d"] = falhas
	}
	return res
}

// healthLicenca: o contrato e os pagamentos.
func (h *handlers) healthLicenca(ctx context.Context, domain string) fiber.Map {
	lic, _ := h.repo.GetLicense(ctx, domain)
	res := licenseSummary(lic)
	if pags, err := h.repo.ListPayments(ctx, domain, 12); err == nil {
		res["pagamentos"] = pagamentosToClient(pags)
	}
	return res
}

// healthLog devolve as ultimas linhas de log DESTE cliente.
//
// Num servidor com varios tenants, a aba global de logs e' inutil pra
// suporte: o que interessa fica afogado no fluxo dos outros. Aqui o
// logbuffer filtra por dominio OU pelos numeros das sessoes do cliente —
// boa parte dos logs do caminho WhatsApp traz so' o jid, e sao justamente
// os que importam quando uma mensagem se perde.
func (h *handlers) healthLog(domain string, sessoes []fiber.Map) []fiber.Map {
	numeros := make([]string, 0, len(sessoes))
	for _, s := range sessoes {
		if n, ok := s["numero"].(string); ok && n != "" {
			numeros = append(numeros, n)
		}
	}
	linhas := logbuffer.SnapshotDo(domain, numeros, 120)
	out := make([]fiber.Map, 0, len(linhas))
	for _, l := range linhas {
		out = append(out, fiber.Map{"seq": l.Seq, "texto": l.Text})
	}
	return out
}

// healthFila conta o que esta parado na dead queue DESTE cliente, separando
// o que ainda da' pra reentregar do que nao da'.
func (h *handlers) healthFila(ctx context.Context, sessoes []fiber.Map) fiber.Map {
	res := fiber.Map{"presas": 0, "reentregaveis": 0}
	if h.q == nil {
		return res
	}
	itens, err := h.q.PeekDead(ctx, 500)
	if err != nil {
		return res
	}
	validos := map[string]bool{}
	for _, s := range sessoes {
		if n, ok := s["numero"].(string); ok && n != "" {
			validos[n] = true
		}
	}
	var presas, reentregaveis int
	for _, raw := range itens {
		var j struct {
			SessionJID string `json:"session_jid"`
		}
		if json.Unmarshal(raw, &j) != nil {
			continue
		}
		num := whatsapp.PhoneFromJID(j.SessionJID)
		if !validos[num] {
			continue // de outro cliente
		}
		presas++
		// Reentregavel = a sessao daquele job ainda e' uma sessao viva deste
		// cliente. Job de device que nao existe mais nao tem por onde sair.
		for _, s := range sessoes {
			if s["jid"] == j.SessionJID {
				reentregaveis++
				break
			}
		}
	}
	res["presas"] = presas
	res["reentregaveis"] = reentregaveis
	return res
}

// POST /admin/api/tenant/reprocessar-fila?domain=... — reentrega as
// mensagens presas na dead queue deste cliente.
func (h *handlers) adminReprocessarFila(c *fiber.Ctx) error {
	domain := normalizePortalDomain(strings.TrimSpace(c.Query("domain")))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain obrigatorio"})
	}
	if h.q == nil {
		return c.Status(503).JSON(fiber.Map{"error": "fila indisponivel"})
	}
	// Sessoes vivas do cliente: so' elas podem receber reentrega.
	validos := map[string]bool{}
	for _, s := range h.healthSessoes(c.Context(), domain) {
		if n, ok := s["numero"].(string); ok && n != "" {
			validos[n] = true
		}
	}
	if len(validos) == 0 {
		return c.Status(400).JSON(fiber.Map{
			"error": "cliente nao tem sessao WhatsApp — nao ha' por onde reentregar"})
	}
	reenf, desc, err := h.q.ReprocessarDead(c.Context(), validos)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.repo.WriteAudit(c.Context(), h.adminActor(c), "fila.reprocessar", domain,
		"reenfileirados="+itoa(reenf)+" descartados="+itoa(desc), clientIP(c))
	return c.JSON(fiber.Map{"ok": true, "reenfileirados": reenf, "descartados": desc})
}
