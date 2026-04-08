package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/api"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/bitrix"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/config"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/queue"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/telemetry"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/watchdog"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/whatsapp"
	"go.mau.fi/whatsmeow/types/events"
	"go.uber.org/zap"
)

func main() {
	// ─── Logger ──────────────────────────────────────────────────────────
	log, _ := zap.NewProduction()
	defer log.Sync()

	// ─── Config ──────────────────────────────────────────────────────────
	cfg, err := config.Load()
	if err != nil {
		log.Fatal("config load failed", zap.Error(err))
	}
	log.Info("config loaded", zap.String("env", cfg.App.Env))

	// ─── Contexto com cancelamento ───────────────────────────────────────
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ─── PostgreSQL ──────────────────────────────────────────────────────
	pool, err := db.NewPool(ctx, &cfg.Postgres, log)
	if err != nil {
		log.Fatal("postgres connect failed", zap.Error(err))
	}
	defer pool.Close()
	repo := db.NewRepository(pool)

	// ─── Redis ───────────────────────────────────────────────────────────
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr(),
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatal("redis connect failed", zap.Error(err))
	}
	log.Info("Redis connected", zap.String("addr", cfg.Redis.Addr()))
	defer rdb.Close()

	// ─── Métricas ────────────────────────────────────────────────────────
	metrics := telemetry.New()

	// ─── Fila ────────────────────────────────────────────────────────────
	q := queue.New(rdb, &cfg.Queue, log)
	workers := queue.NewWorkerPool(q, cfg.Queue.Workers, log)

	// ─── Bitrix24 ────────────────────────────────────────────────────────
	bitrixClient := bitrix.NewClient(&cfg.Bitrix, repo, log)
	// lineID=1 — ajuste para o ID do seu canal Open Lines no Bitrix24
	bitrixProcessor := bitrix.NewProcessor(bitrixClient, repo, log, 1)

	// ─── WhatsApp Manager ────────────────────────────────────────────────
	msgHandler := buildMessageHandler(ctx, q, metrics, log)
	waManager := whatsapp.NewManager(&cfg.WhatsApp, repo, log, msgHandler)

	// Carrega todas as sessões salvas no banco
	if err := waManager.LoadAll(ctx); err != nil {
		log.Warn("load sessions warning", zap.Error(err))
	}

	// ─── Workers inbound: WA → Bitrix ─────────────────────────────────────
	workers.StartInbound(ctx, func(c context.Context, job *queue.InboundJob) error {
		metrics.MessagesInbound.Inc()
		if err := bitrixProcessor.ProcessInbound(c, job); err != nil {
			metrics.BitrixErrors.Inc()
			return err
		}
		return nil
	})

	// ─── Workers outbound: Bitrix → WA ───────────────────────────────────
	workers.StartOutbound(ctx, func(c context.Context, job *queue.OutboundJob) error {
		metrics.MessagesOutbound.Inc()
		if err := waManager.Send(c, job.SessionJID, job.ToJID, job.Text); err != nil {
			metrics.MessagesFailed.Inc()
			return err
		}
		return nil
	})

	// ─── Watchdog ────────────────────────────────────────────────────────
	wd := watchdog.New(waManager, repo, &cfg.Watchdog, log)
	wd.Start(ctx)

	// ─── HTTP Server ─────────────────────────────────────────────────────
	app := api.New(cfg, repo, waManager, bitrixClient, q, metrics, log)

	go func() {
		if err := app.Listen(":" + cfg.App.Port); err != nil {
			log.Error("http server error", zap.Error(err))
		}
	}()
	log.Info("HTTP server started", zap.String("port", cfg.App.Port))

	// ─── Graceful Shutdown ───────────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("shutdown signal received — draining queues (max 30s)...")
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := app.ShutdownWithContext(shutdownCtx); err != nil {
		log.Error("http shutdown error", zap.Error(err))
	}

	for _, jid := range waManager.ListSessions() {
		waManager.Disconnect(jid)
	}

	log.Info("connector stopped gracefully")
}

// buildMessageHandler cria o handler que converte eventos WhatsApp em InboundJobs.
func buildMessageHandler(
	ctx context.Context,
	q *queue.Queue,
	metrics *telemetry.Metrics,
	log *zap.Logger,
) whatsapp.MessageHandler {
	return func(sessionID uuid.UUID, sessionJID string, evt *events.Message) {
		if evt.Info.IsFromMe {
			return
		}

		text := ""
		if evt.Message.GetConversation() != "" {
			text = evt.Message.GetConversation()
		} else if ext := evt.Message.GetExtendedTextMessage(); ext != nil {
			text = ext.GetText()
		}

		job := &queue.InboundJob{
			SessionID:   sessionID,
			SessionJID:  sessionJID,
			FromJID:     evt.Info.Sender.String(),
			FromPhone:   evt.Info.Sender.User,
			FromName:    evt.Info.PushName,
			MessageID:   evt.Info.ID,
			MessageType: string(db.MsgTypeText),
			Text:        text,
		}

		if err := q.PushInbound(ctx, job); err != nil {
			log.Error("push inbound failed", zap.String("msg_id", evt.Info.ID), zap.Error(err))
			return
		}

		metrics.MessagesInbound.Inc()
		log.Debug("message queued", zap.String("from", job.FromPhone), zap.String("text", text))
	}
}
