package main

import (
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

// Localizacao NAO estava na cadeia de tipos ate' 29/09: caia no filtro de
// "evento vazio" e o atendente nao via nada. O cliente mandava o endereco e a
// mensagem simplesmente sumia.
func TestLocalizacaoViraTextoUtil(t *testing.T) {
	txt := textoDeLocalizacao(-22.9068, -43.1729, "Pao de Acucar", "Av. Pasteur, 520")
	for _, esperado := range []string{"Pao de Acucar", "Av. Pasteur, 520", "-22.906800", "-43.172900", "maps.google.com"} {
		if !strings.Contains(txt, esperado) {
			t.Errorf("faltou %q em:\n%s", esperado, txt)
		}
	}
}

// Sem nome nem endereco (o caso de "enviar minha localizacao atual"), as
// coordenadas e o link ainda precisam sair — senao o atendente recebe um rotulo
// vazio, que e' tao inutil quanto nao receber nada.
func TestLocalizacaoSemNomeAindaTemCoordenadaELink(t *testing.T) {
	txt := textoDeLocalizacao(-23.5505, -46.6333, "", "")
	if !strings.Contains(txt, "-23.550500") || !strings.Contains(txt, "maps.google.com") {
		t.Fatalf("coordenada ou link sumiram:\n%s", txt)
	}
}

// Endereco igual ao nome nao pode aparecer duas vezes.
func TestLocalizacaoNaoRepeteNomeEEndereco(t *testing.T) {
	txt := textoDeLocalizacao(1, 2, "Praca Central", "Praca Central")
	if strings.Count(txt, "Praca Central") != 1 {
		t.Errorf("nome repetido:\n%s", txt)
	}
}

// tipoNaoTratado separa o evento fantasma do multi-device (normal, so'
// metadado) do tipo real que a gente nao sabe ler (mensagem do cliente
// sumindo). Antes os dois logavam identicos e o segundo passava por ruido.
func TestFantasmaNaoViraAlarme(t *testing.T) {
	if got := tipoNaoTratado(nil); got != "" {
		t.Errorf("mensagem nil deveria ser fantasma, veio %q", got)
	}
	if got := tipoNaoTratado(&waE2E.Message{}); got != "" {
		t.Errorf("mensagem vazia deveria ser fantasma, veio %q", got)
	}
}

// Tipo real que nao tratamos TEM que ser nomeado, senao vira ruido no log e
// ninguem descobre que existe conteudo que o atendente nunca recebeu.
func TestTipoRealNaoTratadoEhNomeado(t *testing.T) {
	casos := map[string]*waE2E.Message{
		"resposta de botao": {ButtonsResponseMessage: &waE2E.ButtonsResponseMessage{
			SelectedButtonID: proto.String("sim"),
		}},
		"resposta de lista": {ListResponseMessage: &waE2E.ListResponseMessage{
			Title: proto.String("opcao"),
		}},
		"pedido do catalogo": {OrderMessage: &waE2E.OrderMessage{
			OrderID: proto.String("123"),
		}},
	}
	for esperado, msg := range casos {
		if got := tipoNaoTratado(msg); got != esperado {
			t.Errorf("esperava %q, veio %q", esperado, got)
		}
	}
}

// O WhatsApp versiona a enquete: PollCreationMessage e V2..V6, todas com o
// mesmo formato. O cliente moderno manda V3 ou acima, entao olhar so' o campo
// base — o que o codigo fazia — e' nao ver enquete nenhuma. Medido em 29/09: a
// enquete do teste caiu como "evento vazio".
func TestEnqueteEhAchadaEmQualquerVariante(t *testing.T) {
	enq := &waE2E.PollCreationMessage{Name: proto.String("Qual cor?")}
	casos := map[string]*waE2E.Message{
		"base": {PollCreationMessage: enq},
		"V2":   {PollCreationMessageV2: enq},
		"V3":   {PollCreationMessageV3: enq},
		"V5":   {PollCreationMessageV5: enq},
		"V6":   {PollCreationMessageV6: enq},
	}
	for nome, msg := range casos {
		got := enqueteDaMensagem(msg)
		if got == nil {
			t.Errorf("%s: enquete nao encontrada", nome)
		} else if got.GetName() != "Qual cor?" {
			t.Errorf("%s: pergunta errada %q", nome, got.GetName())
		}
	}
	if enqueteDaMensagem(&waE2E.Message{}) != nil {
		t.Error("mensagem sem enquete devolveu enquete")
	}
	if enqueteDaMensagem(nil) != nil {
		t.Error("nil devolveu enquete")
	}
}

// Numa edicao ao vivo o texto novo fica em ProtocolMessage.EditedMessage — o
// whatsmeow so' desembrulha isso no history sync. Sem cavar ali, o atendente
// recebia "[Mensagem editada pelo cliente]" sem saber PARA QUE mudou.
func TestTextoNovoDaEdicaoEhExtraido(t *testing.T) {
	msg := &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
		Type:          waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(),
		EditedMessage: &waE2E.Message{Conversation: proto.String("texto corrigido")},
	}}
	if got := textoDaEdicao(msg); got != "texto corrigido" {
		t.Errorf("esperava o texto novo, veio %q", got)
	}
}

// Editar a legenda de uma imagem tambem tem que trazer a legenda nova.
func TestEdicaoDeLegendaDeMidia(t *testing.T) {
	msg := &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
		Type: waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(),
		EditedMessage: &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
			Caption: proto.String("legenda nova"),
		}},
	}}
	if got := textoDaEdicao(msg); got != "legenda nova" {
		t.Errorf("esperava a legenda nova, veio %q", got)
	}
}

// Sem conteudo de edicao, devolve vazio — quem chama decide o fallback.
func TestEdicaoSemConteudoDevolveVazio(t *testing.T) {
	for _, m := range []*waE2E.Message{nil, {}, {ProtocolMessage: &waE2E.ProtocolMessage{}}} {
		if got := textoDaEdicao(m); got != "" {
			t.Errorf("esperava vazio, veio %q", got)
		}
	}
}

// A mensagem JA' descriptografada (a que AbrirEdicao devolve) vem sem o
// ProtocolMessage por fora — o texto esta' na raiz. textoDaEdicao tem que
// servir os dois formatos, senao a descriptografia funciona e o texto some
// mesmo assim.
func TestTextoDaEdicaoAceitaMensagemJaAberta(t *testing.T) {
	aberta := &waE2E.Message{Conversation: proto.String("texto ja decifrado")}
	if got := textoDaEdicao(aberta); got != "texto ja decifrado" {
		t.Errorf("mensagem aberta: esperava o texto, veio %q", got)
	}
	abertaExt := &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
		Text: proto.String("texto longo decifrado"),
	}}
	if got := textoDaEdicao(abertaExt); got != "texto longo decifrado" {
		t.Errorf("extendedText aberto: veio %q", got)
	}
}
