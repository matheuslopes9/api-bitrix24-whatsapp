package main

import (
	"strings"
	"testing"
)

// O operador le o motivo no Bitrix e na aba: o que ele pode resolver vem em
// portugues; o resto passa como veio (continua util pro suporte).
func TestMotivoParaOperador(t *testing.T) {
	casos := map[string]string{
		"no LID found for 5535999507638@s.whatsapp.net from server": "não reconhece este número",
		"numero 553599507638 nao esta no WhatsApp (tentado: x)":     "não reconhece este número",
		"session not found: 551920187703@s.whatsapp.net":            "desconectado",
	}
	for entra, quer := range casos {
		if got := motivoParaOperador(entra); !strings.Contains(got, quer) {
			t.Errorf("motivoParaOperador(%q) = %q, queria conter %q", entra, got, quer)
		}
	}
	if got := motivoParaOperador("context deadline exceeded"); got != "context deadline exceeded" {
		t.Errorf("motivo desconhecido devia passar intacto: %q", got)
	}
}
