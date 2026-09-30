package api

import (
	"testing"

	"github.com/uctechnology/api-bitrix24-whatsapp/internal/config"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
)

// Existiam duas fontes de credencial e uma delas quase nunca era consultada:
// portalToCreds lia o ambiente (38 chamadas), localCredsForDomain lia a conta
// (3 chamadas). Cadastrar pela tela "Credenciais do app" alcancava 3 dos 41
// caminhos — e a tela ainda respondia que a renovacao passaria a usar o app
// novo, o que era falso.
//
// A regra agora e' uma so': ambiente por padrao, credencial do portal quando
// existe. Estes testes travam essa regra e, principalmente, travam o fato de
// que as DUAS funcoes respondem igual — a divergencia entre elas era o bug.

func handlersComCreds(env db.AppCreds, porPortal map[string]db.AppCreds) *handlers {
	cache := novoCredsApp()
	if porPortal != nil {
		cache.trocar(porPortal)
	}
	return &handlers{
		cfg: &config.Config{
			Bitrix: config.BitrixConfig{ClientID: env.ClientID, ClientSecret: env.ClientSecret},
		},
		credsApp: cache,
	}
}

func TestCredencialCaiNoAmbienteQuandoPortalNaoTemAppProprio(t *testing.T) {
	h := handlersComCreds(db.AppCreds{ClientID: "app.uc", ClientSecret: "s3gr3d0"}, nil)

	id, secret, origem := h.credenciaisDoPortal("teclife.bitrix24.com.br")
	if id != "app.uc" || secret != "s3gr3d0" {
		t.Errorf("credencial = %q/%q, esperado a do ambiente", id, secret)
	}
	if origem != "ambiente" {
		t.Errorf("origem = %q, esperado ambiente", origem)
	}
}

func TestCredencialDoPortalGanhaDoAmbiente(t *testing.T) {
	h := handlersComCreds(
		db.AppCreds{ClientID: "app.uc", ClientSecret: "s3gr3d0"},
		map[string]db.AppCreds{"teclife.bitrix24.com.br": {ClientID: "app.teclife", ClientSecret: "outro"}},
	)

	id, secret, origem := h.credenciaisDoPortal("teclife.bitrix24.com.br")
	if id != "app.teclife" || secret != "outro" {
		t.Errorf("credencial = %q/%q — a cadastrada na tela tem que ganhar", id, secret)
	}
	if origem != "portal" {
		t.Errorf("origem = %q, esperado portal — a tela precisa poder dizer a verdade", origem)
	}

	// O portal vizinho nao pode ser contaminado: sao clientes diferentes.
	if id, _, origem := h.credenciaisDoPortal("crm.uctechnology.com.br"); id != "app.uc" || origem != "ambiente" {
		t.Errorf("vizinho pegou credencial alheia: %q (%s)", id, origem)
	}
}

// O dominio chega em formatos diferentes conforme o caminho (com https://, com
// www., com caixa alta). Se a chave nao normalizar, a excecao simplesmente nao
// e' encontrada e o portal cai para o ambiente calado — o modo de falha mais
// dificil de perceber que existe aqui.
func TestCredencialDoPortalNormalizaODominio(t *testing.T) {
	h := handlersComCreds(
		db.AppCreds{ClientID: "app.uc"},
		map[string]db.AppCreds{"teclife.bitrix24.com.br": {ClientID: "app.teclife", ClientSecret: "x"}},
	)
	for _, forma := range []string{
		"teclife.bitrix24.com.br",
		"https://teclife.bitrix24.com.br",
		"TECLIFE.bitrix24.com.br",
		"www.teclife.bitrix24.com.br",
	} {
		if id, _, _ := h.credenciaisDoPortal(forma); id != "app.teclife" {
			t.Errorf("%q resolveu para %q — a excecao do portal foi perdida", forma, id)
		}
	}
}

// A divergencia entre as duas funcoes ERA o bug. Se voltarem a discordar,
// cadastrar pela tela volta a alcancar so' parte do sistema.
func TestAsDuasFuncoesDeCredencialConcordam(t *testing.T) {
	h := handlersComCreds(
		db.AppCreds{ClientID: "app.uc", ClientSecret: "s3gr3d0"},
		map[string]db.AppCreds{"teclife.bitrix24.com.br": {ClientID: "app.teclife", ClientSecret: "outro"}},
	)
	for _, dom := range []string{"teclife.bitrix24.com.br", "crm.uctechnology.com.br"} {
		portal := &db.BitrixPortal{Domain: dom}
		viaPortal := h.portalToCreds(portal)
		viaLocal := h.localCredsForDomain(nil, dom, portal)
		if viaPortal.ClientID != viaLocal.ClientID || viaPortal.ClientSecret != viaLocal.ClientSecret {
			t.Errorf("%s: portalToCreds=%q e localCredsForDomain=%q — as duas fontes divergiram de novo",
				dom, viaPortal.ClientID, viaLocal.ClientID)
		}
	}
}

// Credencial apagada na tela tem que sumir do cache. Um merge deixaria a
// excecao viva para sempre e so' um restart devolveria o app do ambiente.
func TestRecarregarSubstituiEmVezDeAcumular(t *testing.T) {
	c := novoCredsApp()
	c.trocar(map[string]db.AppCreds{"teclife.bitrix24.com.br": {ClientID: "app.teclife"}})
	c.trocar(map[string]db.AppCreds{})

	if _, ok := c.get("teclife.bitrix24.com.br"); ok {
		t.Error("credencial removida continuou no cache — nao havia como desfazer sem restart")
	}
}

// Medido no homolog em 30/09: os dois portais tinham gravado na conta
// exatamente o client_id da env. Tratar isso como "app proprio" poria um aviso
// amarelo em TODO cliente para dizer que nada esta' diferente — e aviso que
// aparece sempre e' aviso que ninguem le. So' e' excecao o que de fato difere.
func TestCredencialIgualAAmbienteNaoContaComoExcecao(t *testing.T) {
	h := handlersComCreds(
		db.AppCreds{ClientID: "app.uc", ClientSecret: "s3gr3d0"},
		map[string]db.AppCreds{"teclife.bitrix24.com.br": {ClientID: "app.uc", ClientSecret: "s3gr3d0"}},
	)
	id, _, origem := h.credenciaisDoPortal("teclife.bitrix24.com.br")
	if id != "app.uc" {
		t.Errorf("credencial = %q, esperado app.uc", id)
	}
	if origem != "ambiente" {
		t.Errorf("origem = %q: o valor e' identico ao do ambiente, nao e' app proprio", origem)
	}
}

// Mas um secret diferente COM o mesmo client_id e' excecao de verdade: e' outro
// app, e confundir os dois e' o que faz o Bitrix responder wrong_client.
func TestSecretDiferenteAindaContaComoExcecao(t *testing.T) {
	h := handlersComCreds(
		db.AppCreds{ClientID: "app.uc", ClientSecret: "s3gr3d0"},
		map[string]db.AppCreds{"teclife.bitrix24.com.br": {ClientID: "app.uc", ClientSecret: "OUTRO"}},
	)
	_, secret, origem := h.credenciaisDoPortal("teclife.bitrix24.com.br")
	if secret != "OUTRO" || origem != "portal" {
		t.Errorf("secret=%q origem=%q — a excecao real foi perdida", secret, origem)
	}
}
