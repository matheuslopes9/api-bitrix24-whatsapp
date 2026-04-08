package queue

import (
	"context"

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
}

func NewWorkerPool(q *Queue, numWorkers int, log *zap.Logger) *WorkerPool {
	return &WorkerPool{q: q, numWorkers: numWorkers, log: log}
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

		if err := processor(ctx, job); err != nil {
			wp.log.Warn("outbound processing failed, retrying",
				zap.String("job_id", job.ID),
				zap.Int("retry", job.RetryCount),
				zap.Error(err))
			_ = wp.q.RetryOutbound(ctx, job)
		}
	}
}
