package api

import "testing"

// O bug #24 nao foi "a tela nao avisou": foi a tela avisar ERRADO, mandando o
// suporte re-registrar quatro abas que estavam funcionando. Um diagnostico
// errado custa mais que um diagnostico ausente, porque gera trabalho e destroi
// a confianca na propria tela.
//
// Por isso o que estes testes protegem nao e' a deteccao — e' a recusa a
// afirmar falta quando a resposta do portal nao foi entendida.

func TestNomeDoPlacementToleraACaixaDaChave(t *testing.T) {
	casos := []map[string]interface{}{
		{"placement": "CRM_CONTACT_DETAIL_TAB"},
		{"PLACEMENT": "CRM_CONTACT_DETAIL_TAB"},
		{"Placement": "crm_contact_detail_tab"},
		{"placement": "  CRM_CONTACT_DETAIL_TAB  "},
	}
	for _, c := range casos {
		if got := nomeDoPlacement(c); got != "CRM_CONTACT_DETAIL_TAB" {
			t.Errorf("%v -> %q; a Saude concluiria que a aba sumiu", c, got)
		}
	}
}

func TestNomeDoPlacementIgnoraOQueNaoReconhece(t *testing.T) {
	for _, c := range []map[string]interface{}{
		{"handler": "https://exemplo/tab"},
		{"placement": ""},
		{"placement": 42},
		{},
	} {
		if got := nomeDoPlacement(c); got != "" {
			t.Errorf("%v -> %q, esperado vazio", c, got)
		}
	}
}

// As tres abas conferidas tem que ser as mesmas tres que crm.go registra.
// Divergir faria a Saude acusar falta de uma aba que o app nunca tentou criar.
func TestAbasEsperadasBatemComAsQueOAppRegistra(t *testing.T) {
	for _, codigo := range []string{"CRM_CONTACT_DETAIL_TAB", "CRM_LEAD_DETAIL_TAB", "CRM_DEAL_DETAIL_TAB"} {
		if _, ok := abasEsperadas[codigo]; !ok {
			t.Errorf("%s e' registrado por crm.go mas a Saude nao confere", codigo)
		}
	}
	if len(abasEsperadas) != 3 {
		t.Errorf("abasEsperadas tem %d entradas; crm.go faz bind de 3", len(abasEsperadas))
	}
}
