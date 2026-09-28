package api

import "testing"

// Esta e' a resposta REAL do imconnector.status, copiada do homolog em 28/09
// (crm.uctechnology.com.br, conector wa_qr_551920187040, linha 7), com o
// conector funcionando.
//
// O teste existe porque a primeira versao do parse procurava os campos dentro
// de um envelope "result" que o client.call() ja' remove. O encoding/json
// aceitou em silencio, os booleanos viraram false, e a tela de Saude acusou
// conector quebrado enquanto o Bitrix respondia STATUS:true. Falha silenciosa
// de parse nao aparece em log nem em erro — so' num teste com a forma real.
const respostaRealDoBitrix = `{"LINE":7,"CONNECTOR":"wa_qr_551920187040","ERROR":false,"CONFIGURED":true,"STATUS":true}`

func TestLerStatusConectorFormaRealDoBitrix(t *testing.T) {
	s, ok := lerStatusConector([]byte(respostaRealDoBitrix))
	if !ok {
		t.Fatal("nao entendeu a resposta real do Bitrix")
	}
	if !s.Status {
		t.Error("STATUS veio false — a tela diria 'falhou' com o conector ativo")
	}
	if !s.Configured {
		t.Error("CONFIGURED veio false")
	}
	if s.Error {
		t.Error("ERROR veio true")
	}
	if s.Connector != "wa_qr_551920187040" {
		t.Errorf("CONNECTOR = %q", s.Connector)
	}
}

// O envelope "result" NAO existe neste ponto. Se alguem reintroduzir o parse
// errado, os campos ficam zerados — e ok=false tem que pegar isso, senao volta
// a mentir dizendo que o conector caiu.
func TestEnvelopeResultNaoEnganaEmSilencio(t *testing.T) {
	comEnvelope := `{"result":{"LINE":7,"CONNECTOR":"wa_qr_1","ERROR":false,"CONFIGURED":true,"STATUS":true}}`
	if _, ok := lerStatusConector([]byte(comEnvelope)); ok {
		t.Fatal("aceitou a forma errada como valida — e' exatamente o bug que isto previne")
	}
}

// Conector de fato inativo tem que ser reportado como inativo.
func TestConectorInativoEhReportado(t *testing.T) {
	s, ok := lerStatusConector([]byte(
		`{"LINE":7,"CONNECTOR":"wa_qr_1","ERROR":false,"CONFIGURED":false,"STATUS":false}`))
	if !ok {
		t.Fatal("nao entendeu a resposta")
	}
	if s.Status || s.Configured {
		t.Error("deveria estar inativo")
	}
}

// ERROR:true e' outro caso: o Bitrix marcou o conector como inoperante, e a
// providencia (reativar) e' diferente de "nunca foi configurado".
func TestFlagDeErroEhDistinguida(t *testing.T) {
	s, ok := lerStatusConector([]byte(
		`{"LINE":7,"CONNECTOR":"wa_qr_1","ERROR":true,"CONFIGURED":false,"STATUS":false}`))
	if !ok || !s.Error {
		t.Fatal("perdeu o flag ERROR")
	}
}

// Resposta vazia ou lixo nao pode virar "conector quebrado" — nao sabemos.
func TestRespostaIlegivelNaoViraDiagnostico(t *testing.T) {
	for _, entrada := range []string{``, `null`, `[]`, `{"foo":1}`, `nao e json`} {
		if _, ok := lerStatusConector([]byte(entrada)); ok {
			t.Errorf("aceitou %q como resposta valida", entrada)
		}
	}
}
