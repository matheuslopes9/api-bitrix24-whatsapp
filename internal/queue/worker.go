package queue

import (
	"context"
	"math/rand"
	"sync"
	"time"

	"go.uber.org/zap"
)

// InboundProcessor define a função que processa um job inbound (entrega ao Bitrix).
type InboundProcessor func(ctx context.Context, job *InboundJob) error

// OutboundProcessor define a função que processa um job outbound (envia via WhatsApp).
type OutboundProcessor func(ctx context.Context, job *OutboundJob) error

// WorkerPool gerencia um pool de goroutines consumidoras.
type WorkerPool struct {
	q          *Queue
	numWorkers int
	log        *zap.Logger

	// jidLocks garante que mensagens para o mesmo JID não sejam enviadas em paralelo.
	// Cada JID tem seu próprio mutex — workers diferentes podem rodar em paralelo
	// desde que sejam para JIDs diferentes.
	jidMu   sync.Mutex
	jidLocks map[string]*sync.Mutex
}

func NewWorkerPool(q *Queue, numWorkers int, log *zap.Logger) *WorkerPool {
	return &WorkerPool{
		q:          q,
		numWorkers: numWorkers,
		log:        log,
		jidLocks:   make(map[string]*sync.Mutex),
	}
}

// lockForJID retorna o mutex exclusivo para um JID de destino.
func (wp *WorkerPool) lockForJID(jid string) *sync.Mutex {
	wp.jidMu.Lock()
	defer wp.jidMu.Unlock()
	if _, ok := wp.jidLocks[jid]; !ok {
		wp.jidLocks[jid] = &sync.Mutex{}
	}
	return wp.jidLocks[jid]
}

// typingDelay simula o tempo de digitação com base no tamanho do texto.
// Para mídia ou texto curto: 1–2s. Para textos longos: até 4s.
// Adiciona jitter para parecer mais humano.
func typingDelay(text string) time.Duration {
	base := 1000 // ms mínimo
	perChar := 30 // ms por caractere (simula ~200 chars/min)
	chars := len([]rune(text))
	if chars > 100 {
		chars = 100 // cap em 100 chars de influência
	}
	ms := base + chars*perChar
	if ms > 4000 {
		ms = 4000 // máximo 4s
	}
	// jitter ±30%
	jitter := int(float64(ms) * 0.3)
	ms += rand.Intn(2*jitter+1) - jitter
	return time.Duration(ms) * time.Millisecond
}

// StartInbound inicia N workers consumindo a fila inbound.
func (wp *WorkerPool) StartInbound(ctx context.Context, processor InboundProcessor) {
	for i := 0; i < wp.numWorkers; i++ {
		go wp.inboundLoop(ctx, i, processor)
	}
	wp.log.Info("inbound workers started", zap.Int("count", wp.numWorkers))
}

// StartOutbound inicia N workers consumindo a fila outbound.
func (wp *WorkerPool) StartOutbound(ctx context.Context, processor OutboundProcessor) {
	for i := 0; i < wp.numWorkers; i++ {
		go wp.outboundLoop(ctx, i, processor)
	}
	wp.log.Info("outbound workers started", zap.Int("count", wp.numWorkers))
}

func (wp *WorkerPool) inboundLoop(ctx context.Context, id int, processor InboundProcessor) {
	for {
		select {
		case <-ctx.Done():
			wp.log.Info("inbound worker stopped", zap.Int("worker_id", id))
			return
		default:
		}

		job, err := wp.q.PopInbound(ctx)
		if err != nil {
			wp.log.Error("pop inbound error", zap.Int("worker_id", id), zap.Error(err))
			continue
		}
		if job == nil {
			continue // timeout sem mensagem
		}

		if err := processor(ctx, job); err != nil {
			wp.log.Warn("inbound processing failed, retrying",
				zap.String("job_id", job.ID),
				zap.Int("retry", job.RetryCount),
				zap.Error(err))
			_ = wp.q.RetryInbound(ctx, job)
		}
	}
}

func (wp *WorkerPool) outboundLoop(ctx context.Context, id int, processor OutboundProcessor) {
	for {
		select {
		case <-ctx.Done():
			wp.log.Info("outbound worker stopped", zap.Int("worker_id", id))
			return
		default:
		}

		job, err := wp.q.PopOutbound(ctx)
		if err != nil {
			wp.log.Error("pop outbound error", zap.Int("worker_id", id), zap.Error(err))
			continue
		}
		if job == nil {
			continue
		}

		// Serializa por JID: só um worker por vez envia para o mesmo número.
		// Isso evita que múltiplas mensagens simultâneas para o mesmo contato
		// sejam interpretadas como spam pelo WhatsApp.
		mu := wp.lockForJID(job.ToJID)
		mu.Lock()

		if err := processor(ctx, job); err != nil {
			wp.log.Warn("outbound processing failed, retrying",
				zap.String("job_id", job.ID),
				zap.Int("retry", job.RetryCount),
				zap.Error(err))
			_ = wp.q.RetryOutbound(ctx, job)
		}

		mu.Unlock()
	}
}
