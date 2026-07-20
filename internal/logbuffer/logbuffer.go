// Package logbuffer mantem um ring buffer em memoria das ultimas N linhas
// de log + um pub/sub pra streaming em tempo real (SSE) no painel admin.
//
// Uso: no boot, envolver o zapcore com NewCore() num zapcore.NewTee, pra
// que cada log escrito no stdout tambem caia aqui. O handler /admin/api/
// logs/stream le do Subscribe().
package logbuffer

import (
	"sync"

	"go.uber.org/zap/zapcore"
)

const ringSize = 500 // ultimas 500 linhas mantidas em memoria

// Line e' uma entrada de log ja serializada (JSON do zap).
type Line struct {
	Seq  uint64 `json:"seq"`
	Text string `json:"text"`
}

var (
	mu       sync.RWMutex
	ring     []Line
	seq      uint64
	subs     = map[uint64]chan Line{}
	subSeqID uint64
)

// push adiciona uma linha ao ring e notifica subscribers (nao-bloqueante).
func push(text string) {
	mu.Lock()
	seq++
	ln := Line{Seq: seq, Text: text}
	ring = append(ring, ln)
	if len(ring) > ringSize {
		ring = ring[len(ring)-ringSize:]
	}
	// Copia os canais pra notificar fora do lock.
	chans := make([]chan Line, 0, len(subs))
	for _, ch := range subs {
		chans = append(chans, ch)
	}
	mu.Unlock()

	for _, ch := range chans {
		select {
		case ch <- ln:
		default: // subscriber lento — descarta (nao bloqueia o logger)
		}
	}
}

// Snapshot devolve as ultimas linhas atualmente no ring.
func Snapshot() []Line {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Line, len(ring))
	copy(out, ring)
	return out
}

// Subscribe registra um novo consumidor. Retorna o canal e uma funcao de
// cancelamento (chamar no fim do handler SSE).
func Subscribe() (<-chan Line, func()) {
	mu.Lock()
	subSeqID++
	id := subSeqID
	ch := make(chan Line, 64)
	subs[id] = ch
	mu.Unlock()
	return ch, func() {
		mu.Lock()
		delete(subs, id)
		close(ch)
		mu.Unlock()
	}
}

// ─── zapcore integration ───────────────────────────────────────────────────

// Core e' um zapcore.Core que serializa cada entry via o encoder e empurra
// pro ring buffer. Use dentro de um zapcore.NewTee junto do core principal.
type Core struct {
	zapcore.LevelEnabler
	enc zapcore.Encoder
}

// NewCore cria um core que espelha logs pro ring buffer. enc deve ser um
// encoder JSON (mesmo do logger principal, clonado).
func NewCore(enc zapcore.Encoder, enab zapcore.LevelEnabler) zapcore.Core {
	return &Core{LevelEnabler: enab, enc: enc}
}

func (c *Core) With(fields []zapcore.Field) zapcore.Core {
	clone := c.enc.Clone()
	for i := range fields {
		fields[i].AddTo(clone)
	}
	return &Core{LevelEnabler: c.LevelEnabler, enc: clone}
}

func (c *Core) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(ent.Level) {
		return ce.AddCore(ent, c)
	}
	return ce
}

func (c *Core) Write(ent zapcore.Entry, fields []zapcore.Field) error {
	buf, err := c.enc.EncodeEntry(ent, fields)
	if err != nil {
		return err
	}
	push(buf.String())
	buf.Free()
	return nil
}

func (c *Core) Sync() error { return nil }
