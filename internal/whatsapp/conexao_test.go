package whatsapp

import (
	"testing"
	"time"
)

// A pausa vale pro numero, nao pro JID exato: o device suffix muda a cada
// reconexao (":66" -> ":67") e o watchdog le o JID do banco.
func TestReconexaoPausadaPorNumero(t *testing.T) {
	m := &Manager{}
	m.pausas.Store("5519988740346", pausaReconexao{ate: time.Now().Add(time.Minute), motivo: "teste"})

	pausada, motivo, falta := m.ReconexaoPausada("5519988740346:67@s.whatsapp.net")
	if !pausada || motivo != "teste" || falta <= 0 {
		t.Fatalf("devia estar pausada: %v %q %v", pausada, motivo, falta)
	}
	if p, _, _ := m.ReconexaoPausada("5511999999999@s.whatsapp.net"); p {
		t.Fatal("pausa vazou pra outro numero")
	}
}

// Passado o prazo, o watchdog volta a tentar.
func TestReconexaoPausadaExpira(t *testing.T) {
	m := &Manager{}
	m.pausas.Store("5519988740346", pausaReconexao{ate: time.Now().Add(-time.Second), motivo: "teste"})
	if p, _, _ := m.ReconexaoPausada("5519988740346@s.whatsapp.net"); p {
		t.Fatal("pausa vencida ainda segurando a reconexao")
	}
	if _, ok := m.pausas.Load("5519988740346"); ok {
		t.Fatal("pausa vencida nao foi limpa")
	}
}
