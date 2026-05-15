// wa_send_gate.go — utilitario compartilhado pra serializar e simular
// digitacao em envios em massa via WhatsApp (caminho nao oficial).
//
// Usado por:
//  - sms_provider.go (Bitrix Marketing > Campanhas SMS)
//  - bp_robot.go     (Bitrix CRM > Automacoes — robot UC Talk)
//
// Politica anti-banimento (Multi-Device whatsmeow):
//  - Gate por sessao serializa envios. N goroutines paralelas pegando o
//    gate da mesma sessionJID processam uma de cada vez.
//  - Intervalo minimo 5s entre msgs consecutivas na MESMA sessao.
//  - Antes de cada envio, simula "digitando..." 1.5-4s (jitter +-30%
//    proporcional ao tamanho do texto).
//  - Sessoes diferentes seguem em paralelo entre si.

package api

import (
	"math/rand"
	"sync"
	"time"
)

// WAGateMinInterval: gap minimo entre envios na mesma sessao.
const WAGateMinInterval = 5 * time.Second

type waSessionGate struct {
	mu       sync.Mutex
	lastSent time.Time
}

var (
	waGatesMu sync.Mutex
	waGates   = map[string]*waSessionGate{}
)

// GetWASessionGate retorna o gate da sessao (cria se nao existir).
// Caller deve fazer .mu.Lock() / .mu.Unlock() ao redor do envio.
func GetWASessionGate(sessionJID string) *waSessionGate {
	waGatesMu.Lock()
	defer waGatesMu.Unlock()
	g, ok := waGates[sessionJID]
	if !ok {
		g = &waSessionGate{}
		waGates[sessionJID] = g
	}
	return g
}

// Lock entra na fila desta sessao. Bloqueante.
func (g *waSessionGate) Lock() { g.mu.Lock() }

// Unlock libera a fila pra proxima msg.
func (g *waSessionGate) Unlock() { g.mu.Unlock() }

// WaitMinInterval bloqueia ate' completar WAGateMinInterval desde o
// ultimo envio nesta sessao. Deve ser chamado APOS Lock().
// Retorna false se o contexto cancelou enquanto esperava.
func (g *waSessionGate) WaitMinInterval(done <-chan struct{}) bool {
	elapsed := time.Since(g.lastSent)
	if elapsed >= WAGateMinInterval {
		return true
	}
	wait := WAGateMinInterval - elapsed
	select {
	case <-time.After(wait):
		return true
	case <-done:
		return false
	}
}

// MarkSent atualiza o timestamp do ultimo envio. Chamar depois do send.
func (g *waSessionGate) MarkSent() {
	g.lastSent = time.Now()
}

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
