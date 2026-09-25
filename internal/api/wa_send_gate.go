// wa_send_gate.go — duracao do "digitando..." do robo de automacao.
//
// O controle de ritmo por numero que morava aqui (waSessionGate) foi
// unificado em whatsapp/ritmo.go, junto com a fila do operador: os dois
// caminhos nao conversavam e somavam as taxas no mesmo WhatsApp.

package api

import (
	"math/rand"
	"time"
)

// WAHumanTypingDuration calcula um tempo plausivel de "digitacao" pra
// uma msg. Humano medio em mobile digita ~4 chars/seg. Jitter +-30% pra
// variar entre msgs e evitar padrao "exatamente 3s sempre".
//
// Clamps: minimo 1.5s (msg curta nao pode digitar instantaneo), maximo
// 4s (msg longa nao pode demorar mais que isso ou destinatario desiste).
func WAHumanTypingDuration(text string) time.Duration {
	const cps = 4.0
	base := float64(len(text)) / cps
	jitter := (rand.Float64()*0.6 - 0.3) * base
	d := time.Duration((base + jitter) * float64(time.Second))
	if d < 1500*time.Millisecond {
		d = 1500 * time.Millisecond
	}
	if d > 4*time.Second {
		d = 4 * time.Second
	}
	return d
}
