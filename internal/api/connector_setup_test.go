package api

import "testing"

// O connector_id gerado aqui TEM que ser identico ao que a migration
// 014_qr_connector_per_session grava, porque ela roda a cada boot e reescreve a
// coluna. Se os dois formatos divergirem, no restart seguinte o banco aponta
// pra um conector que nunca foi registrado no Bitrix e o cliente perde o
// Contact Center sem nenhum erro aparecer.
//
// A migration faz:
//
//	'wa_qr_' || SPLIT_PART(SPLIT_PART(session_jid, '@', 1), ':', 1)
//
// Os casos abaixo sao essa mesma expressao, resolvida na mao.
func TestConnectorDaSessaoBateComAMigration(t *testing.T) {
	h := &handlers{}
	casos := map[string]string{
		"558196807479:7@s.whatsapp.net":  "wa_qr_558196807479",
		"558196807479:47@s.whatsapp.net": "wa_qr_558196807479",
		"558196807479@s.whatsapp.net":    "wa_qr_558196807479",
		"558196807479":                   "wa_qr_558196807479",
	}
	for jid, querido := range casos {
		id, nome, err := h.connectorDaSessao(t.Context(), jid)
		if err != nil {
			t.Fatalf("connectorDaSessao(%q) devolveu erro: %v", jid, err)
		}
		if id != querido {
			t.Errorf("connectorDaSessao(%q) = %q, queria %q", jid, id, querido)
		}
		if nome == "" {
			t.Errorf("connectorDaSessao(%q) devolveu nome vazio", jid)
		}
	}
}

// O device suffix muda a cada re-pareamento. Se entrasse no connector_id, cada
// reconexao criaria um conector novo no portal do cliente e o dialogo antigo
// ficaria orfao.
func TestDeviceSuffixNaoCriaConectorNovo(t *testing.T) {
	h := &handlers{}
	base, _, err := h.connectorDaSessao(t.Context(), "558196807479@s.whatsapp.net")
	if err != nil {
		t.Fatal(err)
	}
	for _, suf := range []string{":1", ":5", ":7", ":47", ":100"} {
		id, _, err := h.connectorDaSessao(t.Context(), "558196807479"+suf+"@s.whatsapp.net")
		if err != nil {
			t.Fatal(err)
		}
		if id != base {
			t.Fatalf("suffix %q gerou %q, deveria ser %q", suf, id, base)
		}
	}
}

// Numeros distintos nao podem colidir: conectores iguais fariam as mensagens de
// dois numeros do mesmo portal desaguarem no mesmo canal.
func TestConectoresDeNumerosDistintosNaoColidem(t *testing.T) {
	h := &handlers{}
	a, _, _ := h.connectorDaSessao(t.Context(), "558196807479:7@s.whatsapp.net")
	b, _, _ := h.connectorDaSessao(t.Context(), "558195098320:2@s.whatsapp.net")
	if a == b {
		t.Fatalf("numeros distintos colidiram em %q", a)
	}
}

// JID vazio nao pode virar "wa_qr_", que casaria com qualquer coisa.
func TestJIDVazioNaoViraConector(t *testing.T) {
	h := &handlers{}
	if id, _, err := h.connectorDaSessao(t.Context(), ""); err == nil {
		t.Fatalf("JID vazio gerou o conector %q em vez de erro", id)
	}
}
