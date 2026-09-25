package whatsapp

// ritmo.go — envio no ritmo de uma pessoa escrevendo.
//
// O PORQUE: a fila de saida tem 20 workers e so' serializava por
// DESTINATARIO. Pelo mesmo numero, 20 mensagens pra 20 contatos saiam no
// mesmo segundo — e o robo de automacao tinha um controle proprio, que nao
// conversava com a fila. Rajada assim e' o que derruba numero.
//
// A regra e' uma so', pra todo caminho de envio pelo mesmo numero: entre um
// envio e o proximo passa pelo menos o tempo de escrever a proxima mensagem.
// Quem esta' so' respondendo um cliente de vez em quando nao espera nada;
// quem dispara varias seguidas sai no ritmo de alguem digitando.

import (
	"context"
	"strings"
	"sync"
	"time"
)

const (
	ritmoMinimo  = 2 * time.Second        // mensagem curta ou so' arquivo
	ritmoPorChar = 100 * time.Millisecond // ~10 caracteres por segundo
	ritmoMaximo  = 12 * time.Second       // texto longo nao pode travar a fila
)

// TempoDeEscrita e' quanto uma pessoa levaria pra escrever o texto.
func TempoDeEscrita(texto string) time.Duration {
	d := ritmoMinimo + time.Duration(len([]rune(strings.TrimSpace(texto))))*ritmoPorChar
	if d > ritmoMaximo {
		d = ritmoMaximo
	}
	return d
}

type vezDoNumero struct {
	mu       sync.Mutex
	ultimoEm time.Time
}

// vezes: um controle por NUMERO (sem device suffix, que muda a cada
// re-pareamento — senao o mesmo numero teria dois controles).
var vezes sync.Map // numero -> *vezDoNumero

func chaveDoNumero(sessionJID string) string {
	n := sessionJID
	if i := strings.IndexByte(n, '@'); i > 0 {
		n = n[:i]
	}
	if i := strings.IndexByte(n, ':'); i > 0 {
		n = n[:i]
	}
	return n
}

// AguardarVez segura o numero e espera o ritmo antes de enviar o texto.
//
// Quem chama TEM que chamar liberar() depois do envio (com ou sem erro) —
// e' ali que o relogio do numero e' marcado. Retorna erro so' se o
// contexto acabar durante a espera; nesse caso nao ha' o que liberar.
func (m *Manager) AguardarVez(ctx context.Context, sessionJID, texto string) (liberar func(), err error) {
	v, _ := vezes.LoadOrStore(chaveDoNumero(sessionJID), &vezDoNumero{})
	vez := v.(*vezDoNumero)
	vez.mu.Lock()
	if falta := TempoDeEscrita(texto) - time.Since(vez.ultimoEm); falta > 0 {
		select {
		case <-time.After(falta):
		case <-ctx.Done():
			vez.mu.Unlock()
			return nil, ctx.Err()
		}
	}
	var uma sync.Once
	return func() {
		uma.Do(func() {
			vez.ultimoEm = time.Now()
			vez.mu.Unlock()
		})
	}, nil
}
