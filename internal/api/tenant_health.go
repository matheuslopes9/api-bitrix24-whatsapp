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
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/logbuffer"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/whatsapp"
	"go.uber.org/zap"
)

// GET /admin/api/metrics — KPIs globais do painel.
//
// Junta o que esta' no BANCO com o que esta' VIVO no processo. As duas coisas
// divergem com frequencia (o status da sessao no banco atrasa apos deploy) e
// a divergencia costuma ser o proprio problema — por isso as duas aparecem.
func (h *handlers) adminMetrics(c *fiber.Ctx) error {
	m, err := h.repo.GetAdminMetrics(c.Context())
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	out := fiber.Map{
		"tenants_total":      m.TenantsTotal,
		"licencas_em_vigor":  m.LicencasVigor,
		"licencas_vencidas":  m.LicencasVencidas,
		"licencas_vencendo":  m.LicencasVencendo,
		"sessions_active":    m.SessionsActive,
		"msgs_24h":           m.Msgs24h,
		"msgs_entrada_24h":   m.MsgsEntrada24h,
		"msgs_saida_24h":     m.MsgsSaida24h,
		"falhas_24h":         m.Falhas24h,
		"tokens_vencidos":    m.TokensVencidos,
		"tenants_sem_sessao": m.TenantsSemSessao,
	}
	// Sessoes realmente conectadas AGORA, lidas do manager.
	if h.waManager != nil {
		out["sessoes_conectadas"] = len(h.waManager.ConnectedSessions())
	}
	// Profundidade das filas: entrada empilhando e' mensagem de cliente
	// esperando, e nao aparecia em lugar nenhum do painel.
	if h.q != nil {
		entrada, saida, mortas := h.q.Lengths(c.Context())
		out["fila_entrada"] = entrada
		out["fila_saida"] = saida
		out["fila_mortas"] = mortas
	}
	return c.JSON(out)
}

// GET /admin/api/mensagens-recentes?limite=40 — o que esta' passando agora.
//
// Ate' aqui, saber se as mensagens estavam fluindo exigia abrir o log do
// container. Esta lista responde de relance: qual cliente, que direcao, se
// entregou, e o motivo quando falhou.
func (h *handlers) adminMensagensRecentes(c *fiber.Ctx) error {
	limite := 40
	if v := strings.TrimSpace(c.Query("limite")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limite = n
		}
	}
	msgs, err := h.repo.ListRecentMessagesComDominio(c.Context(), limite)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"total": len(msgs), "mensagens": msgs})
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
	res["atualizado_em"] = portal.UpdatedAt.Format(time.RFC3339)
	res["master_user_id"] = portal.LegacyAdminUserID
	// URL que o suporte abre pra ver o app COMO O CLIENTE VE. E' o mesmo
	// /dashboard servido no iframe do Bitrix, com preview=1 pra silenciar o
	// SDK BX24 (que so' existe dentro do Bitrix de verdade).
	res["preview_url"] = "/dashboard?preview=1&domain=" + url.QueryEscape(domain)
	if portal.OpenLineID == 0 {
		res["problema"] = "nenhuma Linha Aberta vinculada — mensagens nao tem onde chegar"
	}

	// Token. E' o item que mais deu problema: uma resposta 200-com-erro do
	// OAuth do Bitrix chegou a gravar token VAZIO por cima do bom, e o
	// sintoma so' aparecia como NO_AUTH_FOUND no log.
	tok := fiber.Map{}
	creds := h.portalToCreds(portal)
	// O token que VALE, nao o ultimo tocado. Um dominio pode ter varias linhas
	// de token — uma por app que ja' autorizou — e a busca por updated_at
	// fazia linha orfa antiga vencer linha nova e boa: a tela dizia "vencido"
	// num cliente com 117 mensagens de entrada no dia.
	t, terr := h.repo.GetBitrixTokenUtilizavel(ctx, domain)
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
		vencido := time.Now().After(t.ExpiresAt)
		tok["expira_em"] = t.ExpiresAt.Format(time.RFC3339)
		tok["expirado"] = vencido
		tok["atualizado_em"] = t.UpdatedAt.Format(time.RFC3339)
		// "ok" so' porque os dois campos estao preenchidos era enganoso: o
		// token pode estar VENCIDO ha' horas, com o refresh falhando em loop,
		// e o painel dizia ok enquanto nenhuma mensagem chegava no Contact
		// Center. Estado tem que refletir se da' pra usar, nao se esta'
		// preenchido.
		// Qual app emitiu o token vs qual app estamos usando pra renovar.
		// Divergencia aqui e' a causa do "wrong_client": o Bitrix recusa
		// renovar um token com o client_id/secret de outro app. client_id nao
		// e' segredo — o segredo e' o client_secret, que nao aparece aqui.
		// Qual app renova este token. Quando a env global esta' vazia, quem
		// renova e' a credencial cadastrada na conta do cliente — mostrar o
		// campo vazio daria a entender que nao ha credencial nenhuma.
		emUso := creds.ClientID
		if emUso == "" {
			if accts, aerr := h.repo.ListBitrixAccountsByDomain(ctx, domain); aerr == nil {
				for _, a := range accts {
					if a.ClientID != "" && a.ClientSecret != "" {
						emUso = a.ClientID
						break
					}
				}
			}
		}
		tok["client_id_do_token"] = t.ClientID
		tok["client_id_em_uso"] = emUso
		if t.ClientID != "" && emUso != "" && t.ClientID != emUso {
			tok["problema_app"] = "o token foi emitido por um app e a renovacao usa outro — " +
				"e' isso que faz o Bitrix responder wrong_client"
		}
		if vencido {
			tok["estado"] = "vencido"
			tok["problema"] = "token vencido em " + t.ExpiresAt.Format("02/01 15:04") +
				" e a renovacao nao esta' passando — o app precisa ser reautorizado no portal Bitrix. " +
				"Enquanto isso nenhuma mensagem do cliente chega no Contact Center."
		} else {
			tok["estado"] = "ok"
		}
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
				// client_id nao e' segredo e e' o que identifica o app que
				// emitiu o token; tem_secret diz se da' pra renovar com ele
				// sem expor o segredo em si.
				"client_id":  a.ClientID,
				"tem_secret": a.ClientSecret != "",
			})
		}
		res["conectores"] = lista
	} else {
		res["conectores"] = []fiber.Map{}
		res["problema_conector"] = "nenhuma sessao WhatsApp vinculada a uma Linha Aberta — " +
			"mensagem recebida nao chega no Contact Center"
	}
	// Linhas Abertas do portal — o suporte precisa saber POR QUAL linha o
	// cliente esta atendendo, e quais existem pra escolher.
	if raw, lerr := h.bitrixClient.ListOpenLines(ctx, creds); lerr == nil {
		res["linhas_raw"] = string(raw)
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
	// Profundidade das filas VIVAS. So' a dead queue era reportada, entao
	// fila de entrada empilhando sem ninguem consumir ficava invisivel: o
	// painel dizia "0 presas" com mensagem de cliente parada esperando.
	// Estes numeros sao globais, nao por tenant — a fila e' uma so'.
	entrada, saida, _ := h.q.Lengths(ctx)
	res["aguardando_entrada"] = entrada
	res["aguardando_saida"] = saida
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

// POST /admin/api/tenant/testar-conexao?domain=... — testa a integracao DE
// VERDADE, batendo no Bitrix agora.
//
// Diferente do GET /health, que le estado guardado, aqui cada item faz uma
// chamada real. E' a diferenca entre "o banco diz que o token e' valido" e
// "o token funciona": foi exatamente esse buraco que deixou o teclife com
// token vazio sem ninguem perceber, porque o expires_at continuava no futuro.
// GET /admin/api/tenant/credenciais?domain= — o que esta gravado hoje.
//
// Devolve o client_secret porque o suporte precisa CONFERIR, nao adivinhar.
// Regravar no escuro a cada duvida e' pior: credencial errada derruba a
// renovacao do token e para o atendimento do cliente.
func (h *handlers) adminGetCredenciais(c *fiber.Ctx) error {
	domain := normalizePortalDomain(strings.TrimSpace(c.Query("domain")))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain obrigatorio"})
	}
	id, secret, err := h.repo.GetBitrixAccountCredentials(c.Context(), domain)
	if err != nil {
		// Sem linha nao e' erro: e' portal que ainda nao teve credencial
		// cadastrada. A tela abre em branco e o suporte preenche.
		return c.JSON(fiber.Map{"client_id": "", "client_secret": "", "cadastrado": false})
	}
	return c.JSON(fiber.Map{
		"client_id":     id,
		"client_secret": secret,
		"cadastrado":    id != "" && secret != "",
	})
}

// POST /admin/api/tenant/credenciais — cadastra o app OAuth do portal.
//
// Body: {domain, client_id, client_secret}
//
// O app e' instalado por cliente, cada portal com seu proprio app OAuth. Sem
// este cadastro as credenciais so' podiam vir das envs globais, que servem
// pra um app so'. Vazias, o refresh POSTa client_id="" e o Bitrix responde
// wrong_client — foi o que derrubou a Open Line por 16 horas.
func (h *handlers) adminSalvarCredenciais(c *fiber.Ctx) error {
	var body struct {
		Domain       string `json:"domain"`
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "body invalido"})
	}
	domain := normalizePortalDomain(strings.TrimSpace(body.Domain))
	clientID := strings.TrimSpace(body.ClientID)
	clientSecret := strings.TrimSpace(body.ClientSecret)
	if domain == "" || clientID == "" || clientSecret == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain, client_id e client_secret sao obrigatorios"})
	}

	linhas, err := h.repo.SetBitrixAccountCredentials(c.Context(), domain, clientID, clientSecret)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	if linhas == 0 {
		return c.Status(404).JSON(fiber.Map{
			"error": "nenhuma conexao encontrada para " + domain + " — pareie um numero antes de cadastrar as credenciais",
		})
	}
	// client_secret NUNCA vai pro log. client_id nao e' segredo e ajuda a
	// conferir depois qual app ficou gravado.
	h.log.Info("credenciais OAuth do portal atualizadas",
		zap.String("domain", domain),
		zap.String("client_id", clientID),
		zap.Int64("conexoes", linhas),
		zap.String("por", h.adminActor(c)))

	return c.JSON(fiber.Map{
		"ok":        true,
		"conexoes":  linhas,
		"mensagem":  "credenciais gravadas — a renovacao do token passa a usar este app",
		"client_id": clientID,
	})
}

func (h *handlers) adminTestarConexao(c *fiber.Ctx) error {
	ctx := c.Context()
	domain := normalizePortalDomain(strings.TrimSpace(c.Query("domain")))
	if domain == "" {
		return c.Status(400).JSON(fiber.Map{"error": "domain obrigatorio"})
	}
	portal, err := h.repo.GetBitrixPortalByDomain(ctx, domain)
	if err != nil || portal == nil {
		return c.JSON(fiber.Map{"ok": false, "testes": []fiber.Map{{
			"nome": "Portal instalado", "ok": false,
			"detalhe": "portal nao encontrado — o app nao esta instalado neste dominio",
		}}})
	}
	creds := h.portalToCreds(portal)
	testes := make([]fiber.Map, 0, 6)
	add := func(nome string, ok bool, detalhe string) {
		testes = append(testes, fiber.Map{"nome": nome, "ok": ok, "detalhe": detalhe})
	}

	add("Portal instalado", true, "member_id "+portal.MemberID)

	// 1. Token: uma chamada real vale mais que o expires_at do banco.
	if raw, err := h.bitrixClient.ListOpenLines(ctx, creds); err != nil {
		add("Token de acesso", false, err.Error())
	} else {
		add("Token de acesso", true, "autenticou e listou as Linhas Abertas")
		_ = raw
	}

	// 2. Linha Aberta configurada.
	if portal.OpenLineID == 0 {
		add("Linha Aberta", false, "nenhuma linha vinculada — mensagem nao tem onde chegar")
	} else {
		add("Linha Aberta", true, "linha "+itoa(portal.OpenLineID))
	}

	// 3. Conector ativo na linha, por vinculo.
	accts, _ := h.repo.ListBitrixAccountsByDomain(ctx, domain)
	if len(accts) == 0 {
		add("Conector", false, "nenhuma sessao WhatsApp vinculada a uma Linha Aberta")
	}
	for _, a := range accts {
		raw, err := h.bitrixClient.GetConnectorStatus(ctx, creds, a.ConnectorID, a.OpenLineID)
		if err != nil {
			add("Conector "+a.ConnectorID, false, err.Error())
			continue
		}
		s := string(raw)
		ativo := strings.Contains(s, `"STATUS":true`) || strings.Contains(s, `"STATUS":"Y"`)
		det := "linha " + itoa(a.OpenLineID)
		if !ativo {
			det += " — registrado mas NAO ativado (imconnector.activate)"
		}
		add("Conector "+a.ConnectorID, ativo, det)
	}

	// 4. Sessao WhatsApp viva em memoria, nao o status do banco.
	viva := 0
	if h.waManager != nil {
		for _, s := range h.healthSessoes(ctx, domain) {
			if b, _ := s["conectada_agora"].(bool); b {
				viva++
			}
		}
	}
	if viva == 0 {
		add("Sessao WhatsApp", false, "nenhum numero conectado agora")
	} else {
		add("Sessao WhatsApp", true, itoa(viva)+" numero(s) conectado(s)")
	}

	tudoOK := true
	for _, t := range testes {
		if b, _ := t["ok"].(bool); !b {
			tudoOK = false
		}
	}
	h.repo.WriteAudit(ctx, h.adminActor(c), "tenant.testar-conexao", domain,
		"ok="+boolStr(tudoOK), clientIP(c))
	return c.JSON(fiber.Map{"ok": tudoOK, "testes": testes})
}
