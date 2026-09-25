package whatsapp

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestTempoDeEscritaCresceComOTextoEParaNoMaximo(t *testing.T) {
	if got := TempoDeEscrita(""); got != ritmoMinimo {
		t.Errorf("vazio = %v, esperado %v", got, ritmoMinimo)
	}
	curto, medio := TempoDeEscrita("oi"), TempoDeEscrita(strings.Repeat("a", 40))
	if !(curto < medio) {
		t.Errorf("texto maior deveria demorar mais: %v vs %v", curto, medio)
	}
	if got := TempoDeEscrita(strings.Repeat("a", 5000)); got != ritmoMaximo {
		t.Errorf("texto enorme = %v, esperado teto %v", got, ritmoMaximo)
	}
}

// Duas mensagens seguidas pelo mesmo numero: a segunda espera o tempo de
// escrita. Device suffix diferente (re-pareamento) continua sendo o mesmo
// numero.
func TestMesmoNumeroEsperaOTempoDeEscrita(t *testing.T) {
	m := &Manager{}
	ctx := context.Background()
	lib, err := m.AguardarVez(ctx, "5500000000001:4@s.whatsapp.net", "x")
	if err != nil {
		t.Fatal(err)
	}
	lib()
	inicio := time.Now()
	lib, err = m.AguardarVez(ctx, "5500000000001:9@s.whatsapp.net", "x")
	if err != nil {
		t.Fatal(err)
	}
	lib()
	if esperou := time.Since(inicio); esperou < TempoDeEscrita("x")-100*time.Millisecond {
		t.Fatalf("segunda mensagem saiu apos %v, esperado ~%v", esperou, TempoDeEscrita("x"))
	}
}

// Numeros diferentes nao esperam um pelo outro.
func TestNumerosDiferentesNaoSeEsperam(t *testing.T) {
	m := &Manager{}
	ctx := context.Background()
	lib, _ := m.AguardarVez(ctx, "5500000000002@s.whatsapp.net", "x")
	lib()
	inicio := time.Now()
	lib, _ = m.AguardarVez(ctx, "5500000000003@s.whatsapp.net", "x")
	lib()
	if esperou := time.Since(inicio); esperou > 200*time.Millisecond {
		t.Fatalf("numero diferente esperou %v", esperou)
	}
}

func TestContextoCanceladoNaoPrendeONumero(t *testing.T) {
	m := &Manager{}
	lib, _ := m.AguardarVez(context.Background(), "5500000000004@s.whatsapp.net", "x")
	lib()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := m.AguardarVez(ctx, "5500000000004@s.whatsapp.net", "x"); err == nil {
		t.Fatal("deveria desistir quando o contexto acaba")
	}
	// O numero nao pode ter ficado travado pela desistencia.
	feito := make(chan struct{})
	go func() {
		l, _ := m.AguardarVez(context.Background(), "5500000000004@s.whatsapp.net", "x")
		l()
		close(feito)
	}()
	select {
	case <-feito:
	case <-time.After(5 * time.Second):
		t.Fatal("numero ficou travado")
	}
}

func TestNumeroOcupadoEnquantoAlguemTemAVez(t *testing.T) {
	m := &Manager{}
	num := "5500000000005@s.whatsapp.net"
	if m.NumeroOcupado(num) {
		t.Fatal("numero nunca usado nao pode estar ocupado")
	}
	lib, _ := m.AguardarVez(context.Background(), num, "x")
	if !m.NumeroOcupado(num) {
		t.Fatal("com a vez tomada, deveria estar ocupado")
	}
	if m.NumeroOcupado("5500000000006@s.whatsapp.net") {
		t.Fatal("outro numero nao pode aparecer ocupado")
	}
	lib()
	if m.NumeroOcupado(num) {
		t.Fatal("depois de liberar nao pode seguir ocupado")
	}
}
