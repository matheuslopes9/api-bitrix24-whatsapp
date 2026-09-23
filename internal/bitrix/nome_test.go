package bitrix

import (
	"testing"

	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
)

func TestTelefoneLegivel(t *testing.T) {
	casos := []struct{ entrada, querido string }{
		{"558199809595", "+55 81 9980-9595"},   // BR 12 digitos
		{"5581999809595", "+55 81 99980-9595"}, // BR 13 digitos (com 9)
		{"5519988740346", "+55 19 98874-0346"},
		{"", ""},
		{"127586399207476", "+127586399207476"},          // 15 digitos: ainda tratado como numero
		{"1275863992074761", "Contato WhatsApp (…4761)"}, // 16+: LID
	}
	for _, c := range casos {
		if got := telefoneLegivel(c.entrada); got != c.querido {
			t.Errorf("telefoneLegivel(%q) = %q, queria %q", c.entrada, got, c.querido)
		}
	}
}

func TestNomeDoContatoOrdemDeFallback(t *testing.T) {
	if got := nomeDoContato("Aída Tiné", nil, "558199809595"); got != "Aída Tiné" {
		t.Errorf("push name deve vencer, veio %q", got)
	}
	if got := nomeDoContato("", nil, "558199809595"); got != "+55 81 9980-9595" {
		t.Errorf("sem push name deve cair no telefone, veio %q", got)
	}
	if got := nomeDoContato("   ", nil, "558199809595"); got != "+55 81 9980-9595" {
		t.Errorf("push name so' com espaco conta como vazio, veio %q", got)
	}
	// Nome vazio e' o que faz o Bitrix rotular a conversa como "Guest", e
	// duas conversas "Guest" sao indistinguiveis pro atendente. Entao o
	// ultimo elo NAO pode devolver vazio, mesmo sem nome e sem numero.
	if got := nomeDoContato("", nil, ""); got == "" {
		t.Error("sem nome e sem numero nao pode devolver vazio: vazio vira \"Guest\" no Bitrix")
	}

	// Elo do meio: sem push name, usa o nome ja' gravado do contato.
	c := &db.ContactMapping{WAName: "Aída Tiné"}
	if got := nomeDoContato("", c, "558199809595"); got != "Aída Tiné" {
		t.Errorf("deveria usar o nome gravado, veio %q", got)
	}
	// Nome gravado vazio nao pode mascarar o telefone.
	vazio := &db.ContactMapping{WAName: "  "}
	if got := nomeDoContato("", vazio, "558199809595"); got != "+55 81 9980-9595" {
		t.Errorf("nome gravado vazio deve cair no telefone, veio %q", got)
	}
	// Push name novo vence o gravado (contato mudou o nome no WhatsApp).
	if got := nomeDoContato("Novo Nome", c, "558199809595"); got != "Novo Nome" {
		t.Errorf("push name novo deve vencer o gravado, veio %q", got)
	}
}
