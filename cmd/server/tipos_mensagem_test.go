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
