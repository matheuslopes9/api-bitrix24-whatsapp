package queue

import (
	"errors"
	"testing"
	"time"
)

// 429 do WhatsApp e' condicao TEMPORARIA, nao defeito da mensagem. Tratar
// como falha comum queimava as 5 tentativas em segundos e matava a mensagem
// por um problema que ia passar sozinho — foi o que encheu a dead queue de
// hora em hora.
func TestEhLimiteDeTaxa(t *testing.T) {
	deveSer := []string{
		// O erro exato visto em producao, vindo de dentro do SendMessage.
		"failed to get user info for 5588981859136@s.whatsapp.net to fill LID cache: failed to send usync query: info query returned status 429: rate-overlimit",
		"info query returned status 429: rate-overlimit",
		"Rate-Overlimit",
		"HTTP status 429",
		"too many requests",
	}
	for _, m := range deveSer {
		if !EhLimiteDeTaxa(errors.New(m)) {
			t.Errorf("deveria reconhecer como limite de taxa: %q", m)
		}
	}

	naoDeveSer := []string{
		"numero 5581999887766 nao esta no WhatsApp",
		"bitrix error: insufficient_scope",
		"session not found: 5581@s.whatsapp.net",
		"context deadline exceeded",
		"",
	}
	for _, m := range naoDeveSer {
		if EhLimiteDeTaxa(errors.New(m)) {
			t.Errorf("NAO deveria ser limite de taxa: %q", m)
		}
	}
	if EhLimiteDeTaxa(nil) {
		t.Error("nil nao e' limite de taxa")
	}
}

// Backoff curto sob 429 piora o problema: cada tentativa consome mais cota e
// estica a punicao. A espera tem que CRESCER e ter teto.
func TestEsperaPorLimiteCresceEParaNoTeto(t *testing.T) {
	anterior := time.Duration(0)
	for hits := 1; hits <= limiteMaxHits; hits++ {
		d := esperaPorLimite(hits)
		if d < anterior {
			t.Fatalf("hits=%d: espera diminuiu (%v depois de %v)", hits, d, anterior)
		}
		if d > 10*time.Minute {
			t.Fatalf("hits=%d: espera %v passou do teto de 10min", hits, d)
		}
		anterior = d
	}
	if esperaPorLimite(1) <= 0 {
		t.Error("a primeira espera tem que ser maior que zero")
	}
	if esperaPorLimite(1000) != 10*time.Minute {
		t.Error("com muitos hits a espera tem que ficar no teto, nao crescer sem fim")
	}
}

// A paciencia total precisa cobrir uma janela real de rate limit. Somando as
// esperas ate' desistir, tem que dar bem mais que alguns minutos.
func TestPacienciaTotalCobreUmaJanelaLonga(t *testing.T) {
	var total time.Duration
	for hits := 1; hits <= limiteMaxHits; hits++ {
		total += esperaPorLimite(hits)
	}
	if total < 30*time.Minute {
		t.Fatalf("paciencia total de %v e' curta demais pra uma janela de rate limit", total)
	}
}
