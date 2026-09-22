// Package logbuffer mantem um ring buffer em memoria das ultimas N linhas
// de log + um pub/sub pra streaming em tempo real (SSE) no painel admin.
//
// Uso: no boot, envolver o zapcore com NewCore() num zapcore.NewTee, pra
// que cada log escrito no stdout tambem caia aqui. O handler /admin/api/
// logs/stream le do Subscribe().
package logbuffer

import (
	"strings"
	"sync"

	"go.uber.org/zap/zapcore"
)

const ringSize = 500 // ultimas 500 linhas mantidas em memoria

// Line e' uma entrada de log ja serializada (JSON do zap).
//
// Domain e Ref sao extraidos do proprio JSON na escrita, pra permitir
// mostrar o log DE UM CLIENTE sem misturar com os outros. Sem isso, a
// unica visao possivel e' o fluxo global — inutil pra suporte, porque num
// servidor com varios tenants o log do cliente que voce investiga fica
// afogado no dos demais.
type Line struct {
	Seq    uint64 `json:"seq"`
	Text   string `json:"text"`
	Domain string `json:"domain,omitempty"` // dominio Bitrix, quando o log traz
	Ref    string `json:"ref,omitempty"`    // jid ou phone, quando traz
}

// extrairCampo pega o valor de "campo":"valor" do JSON ja' serializado.
//
// Busca por substring em vez de json.Unmarshal de proposito: isto roda no
// caminho de escrita de TODO log. Um parse completo aqui custaria caro e
// so' pra ler dois campos.
func extrairCampo(texto, campo string) string {
	chave := `"` + campo + `":"`
	i := strings.Index(texto, chave)
	if i < 0 {
		return ""
	}
	i += len(chave)
	j := strings.IndexByte(texto[i:], '"')
	if j < 0 {
		return ""
	}
	return texto[i : i+j]
}

// normalizarDominio tira protocolo, www e barra final, pra casar
// "https://x.bitrix24.com.br/" com "x.bitrix24.com.br".
func normalizarDominio(d string) string {
	d = strings.ToLower(strings.TrimSpace(d))
	d = strings.TrimPrefix(d, "https://")
	d = strings.TrimPrefix(d, "http://")
	d = strings.TrimPrefix(d, "www.")
	return strings.TrimSuffix(d, "/")
}

// numeroBase extrai o numero de um JID, sem device suffix nem dominio:
// "558196807479:5@s.whatsapp.net" -> "558196807479"
func numeroBase(ref string) string {
	if i := strings.IndexByte(ref, '@'); i > 0 {
		ref = ref[:i]
	}
	if i := strings.IndexByte(ref, ':'); i > 0 {
		ref = ref[:i]
	}
	return ref
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
	dom := normalizarDominio(extrairCampo(text, "domain"))
	ref := extrairCampo(text, "jid")
	if ref == "" {
		ref = extrairCampo(text, "phone")
	}

	mu.Lock()
	seq++
	ln := Line{Seq: seq, Text: text, Domain: dom, Ref: numeroBase(ref)}
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

// Pertence diz se a linha e' do cliente informado. Casa por dominio OU por
// um dos numeros das sessoes dele — muitos logs do caminho WhatsApp trazem
// so' o jid, sem dominio, e sao justamente os mais uteis pro suporte.
func (l Line) Pertence(dominio string, numeros []string) bool {
	if dominio != "" && l.Domain == normalizarDominio(dominio) {
		return true
	}
	if l.Ref == "" {
		return false
	}
	for _, n := range numeros {
		if n != "" && numeroBase(n) == l.Ref {
			return true
		}
	}
	return false
}

// SnapshotDo devolve so' as linhas do cliente informado, mais recentes por
// ultimo. limite <= 0 devolve tudo que casar.
func SnapshotDo(dominio string, numeros []string, limite int) []Line {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Line, 0, 64)
	for _, l := range ring {
		if l.Pertence(dominio, numeros) {
			out = append(out, l)
		}
	}
	if limite > 0 && len(out) > limite {
		out = out[len(out)-limite:]
	}
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
