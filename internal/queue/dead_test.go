package queue

import "testing"

// A dead queue e' UMA lista so': RetryInbound e RetryOutbound empurram os dois
// tipos de job pra mesma chave. Reprocessar decodificando tudo como entrada
// fazia a mensagem do proprio atendente voltar pro Contact Center como cliente
// novo e anonimo — o Bitrix rotula isso "Guest". Estes testes fixam o
// discriminador que separa os dois.
func TestClassificarDead(t *testing.T) {
	casos := []struct {
		nome string
		json string
		quer DirecaoDead
	}{
		{
			"job de saida e' reconhecido pelo to_jid",
			`{"id":"a","session_jid":"5581...@s.whatsapp.net","to_jid":"5511999@s.whatsapp.net","text":"*Fulano:* oi"}`,
			DirecaoSaida,
		},
		{
			"job de entrada e' reconhecido pelo from_jid",
			`{"id":"b","session_jid":"5581...@s.whatsapp.net","from_jid":"5511999@s.whatsapp.net","from_phone":"5511999","message_id":"X"}`,
			DirecaoEntrada,
		},
		{
			"saida real da dead queue, com os campos que a entrada nao tem",
			`{"id":"c","session_jid":"558196807479:5@s.whatsapp.net","to_jid":"5581988@s.whatsapp.net","operator_name":"Manuela Negreiros","bitrix_im_msg_id":"77","text":"*Manuela Negreiros:*\noi"}`,
			DirecaoSaida,
		},
		{
			"sem from_jid nem to_jid nao da' pra saber a direcao",
			`{"id":"d","session_jid":"5581...@s.whatsapp.net","text":"*Fulano:* oi"}`,
			DirecaoIndefinida,
		},
		{
			"campo so' com espaco conta como ausente",
			`{"id":"e","session_jid":"5581...@s.whatsapp.net","to_jid":"   ","from_jid":"  "}`,
			DirecaoIndefinida,
		},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			s, err := classificarDead([]byte(c.json))
			if err != nil {
				t.Fatalf("erro inesperado: %v", err)
			}
			if s.Direcao != c.quer {
				t.Fatalf("direcao = %v, queria %v", s.Direcao, c.quer)
			}
		})
	}
}

// O caso que de fato quebrou em producao: um OutboundJob decodifica SEM ERRO
// dentro de InboundJob, com todos os campos de identidade zerados. Se a
// classificacao dependesse de "decodificou, entao e' entrada", esse job
// viraria um chat "Guest". Este teste trava justamente esse engano.
func TestJobDeSaidaNaoEhConfundidoComEntrada(t *testing.T) {
	raw := []byte(`{"id":"a7a6ac2a","session_jid":"558196807479:5@s.whatsapp.net","to_jid":"5581988887777@s.whatsapp.net","text":"*Manuela Negreiros:*\noi","retry_count":6}`)

	s, err := classificarDead(raw)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if s.Direcao != DirecaoSaida {
		t.Fatalf("job de saida classificado como %v — voltaria pro Contact Center como Guest", s.Direcao)
	}
	if s.ID != "a7a6ac2a" {
		t.Errorf("id = %q", s.ID)
	}
	if s.SessionJID != "558196807479:5@s.whatsapp.net" {
		t.Errorf("session_jid = %q", s.SessionJID)
	}
}

// Guarda a assimetria: to_jid ganha de from_jid quando os dois aparecem.
// Confundir entrada com saida atrasa uma mensagem; confundir saida com
// entrada suja a lista de atendimento com chat fantasma.
func TestToJIDTemPrecedencia(t *testing.T) {
	raw := []byte(`{"id":"x","to_jid":"551199@s.whatsapp.net","from_jid":"551188@s.whatsapp.net"}`)
	s, err := classificarDead(raw)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if s.Direcao != DirecaoSaida {
		t.Fatalf("com os dois campos, to_jid deveria ganhar; veio %v", s.Direcao)
	}
}

func TestJSONInvalidoRetornaErro(t *testing.T) {
	if _, err := classificarDead([]byte(`{nao e json`)); err == nil {
		t.Fatal("json invalido deveria retornar erro, nao um job silenciosamente vazio")
	}
}
