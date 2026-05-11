package bitrix

// Rate limit do client Bitrix24 — uma instancia por dominio.
// O Bitrix24 documenta:
//   - 2 req/s por method (rest_qps_limit)
//   - ~50 req/s no portal total (X-RateLimit-To-Reset 503)
//
// Como excedemos isso facilmente em rajadas (50 conversas simultaneas
// no Open Lines = 50 chamadas a imconnector.send.status.delivery),
// metemos um limiter de janela deslizante por (domain+method) que
// bloqueia ate ter slot disponivel.
//
// Para QUERY_LIMIT_EXCEEDED retornado pelo Bitrix mesmo apos o limiter
// (pode acontecer se outros sistemas consomem o mesmo portal), o caller
// faz retry exponencial via callWithRetry.

import (
	"context"
	"strings"
	"sync"
	"time"
)

// methodLimiter implementa janela deslizante simples: guarda timestamps
// das ultimas N chamadas; espera o slot mais antigo expirar se cheio.
type methodLimiter struct {
	mu         sync.Mutex
	timestamps []time.Time
	maxPerSec  int
}

func newMethodLimiter(maxPerSec int) *methodLimiter {
	return &methodLimiter{
		timestamps: make([]time.Time, 0, maxPerSec),
		maxPerSec:  maxPerSec,
	}
}

// wait bloqueia ate poder fazer 1 request (respeitando maxPerSec).
// Retorna se o ctx foi cancelado.
func (l *methodLimiter) wait(ctx context.Context) error {
	for {
		l.mu.Lock()
		now := time.Now()
		// purge timestamps > 1s atras
		cutoff := now.Add(-time.Second)
		i := 0
		for i < len(l.timestamps) && l.timestamps[i].Before(cutoff) {
			i++
		}
		l.timestamps = l.timestamps[i:]

		if len(l.timestamps) < l.maxPerSec {
			l.timestamps = append(l.timestamps, now)
			l.mu.Unlock()
			return nil
		}
		// cheio — espera ate o slot mais antigo expirar
		oldest := l.timestamps[0]
		waitDur := oldest.Add(time.Second).Sub(now)
		l.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitDur):
		}
	}
}

// rateLimiter mantem 1 methodLimiter por (domain, method).
type rateLimiter struct {
	mu             sync.Mutex
	limiters       map[string]*methodLimiter
	defaultPerSec  int
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{
		limiters:      map[string]*methodLimiter{},
		defaultPerSec: 2, // Bitrix doc: 2 req/s por method
	}
}

func (r *rateLimiter) wait(ctx context.Context, domain, method string) error {
	key := domain + "|" + method
	r.mu.Lock()
	lim, ok := r.limiters[key]
	if !ok {
		lim = newMethodLimiter(r.defaultPerSec)
		r.limiters[key] = lim
	}
	r.mu.Unlock()
	return lim.wait(ctx)
}

// isRateLimitError detecta o erro QUERY_LIMIT_EXCEEDED que vem do Bitrix
// quando passamos do limite mesmo depois do limiter local (ex: outro
// sistema do cliente tambem consome o portal).
func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "QUERY_LIMIT_EXCEEDED") ||
		strings.Contains(msg, "Too many requests")
}
