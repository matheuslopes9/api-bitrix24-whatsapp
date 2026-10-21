package whatsapp

import (
	"testing"

	"go.mau.fi/whatsmeow/types"
)

// O caso que derrubava o primeiro envio ativo: o CRM tem o numero COM o 9, o
// WhatsApp responde que existe, mas o telefone da conta (pn_jid) e' SEM o 9.
// O LID fica guardado pro canonico, entao o destino tem que ser ele.
func TestTelefoneCanonicoDaRespostaPorLID(t *testing.T) {
	r := types.IsOnWhatsAppResponse{
		Query:       "+5535999507638",
		JID:         types.NewJID("98681319620715", types.HiddenUserServer),
		PhoneNumber: types.NewJID("553599507638", types.DefaultUserServer),
		IsIn:        true,
	}
	if got := telefoneCanonico(r); got.User != "553599507638" || got.Server != types.DefaultUserServer {
		t.Fatalf("queria o telefone canonico, veio %v", got)
	}
}

// Resposta por telefone (sem LID): o proprio JID e' o canonico.
func TestTelefoneCanonicoDaRespostaPorTelefone(t *testing.T) {
	r := types.IsOnWhatsAppResponse{JID: types.NewJID("553599507638", types.DefaultUserServer), IsIn: true}
	if got := telefoneCanonico(r); got.User != "553599507638" {
		t.Fatalf("veio %v", got)
	}
}

// So' LID, sem telefone: nao inventa destino — nunca enderecar pelo LID.
func TestTelefoneCanonicoNuncaDevolveLID(t *testing.T) {
	r := types.IsOnWhatsAppResponse{JID: types.NewJID("98681319620715", types.HiddenUserServer), IsIn: true}
	if got := telefoneCanonico(r); !got.IsEmpty() {
		t.Fatalf("devolveu %v; sem telefone tem que ser vazio", got)
	}
}
