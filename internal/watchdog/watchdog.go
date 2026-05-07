package watchdog

import (
	"context"
	"time"

	"github.com/uctechnology/api-bitrix24-whatsapp/internal/config"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/whatsapp"
	"go.uber.org/zap"
)

// Watchdog monitora as sessões ativas e tenta reconectar as que caíram.
type Watchdog struct {
	waManager *whatsapp.Manager
	repo      *db.Repository
	cfg       *config.WatchdogConfig
	log       *zap.Logger
}

func New(waManager *whatsapp.Manager, repo *db.Repository, cfg *config.WatchdogConfig, log *zap.Logger) *Watchdog {
	return &Watchdog{waManager: waManager, repo: repo, cfg: cfg, log: log}
}

// Start inicia o loop do watchdog em uma goroutine separada.
func (w *Watchdog) Start(ctx context.Context) {
	go w.loop(ctx)
	w.log.Info("watchdog started", zap.Duration("interval", w.cfg.PingInterval()))
}

func (w *Watchdog) loop(ctx context.Context) {
	ticker := time.NewTicker(w.cfg.PingInterval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.log.Info("watchdog stopped")
			return
		case <-ticker.C:
			w.check(ctx)
		}
	}
}

func (w *Watchdog) check(ctx context.Context) {
	// Busca todas as sessões do banco (ativas + desconectadas, exceto banidas)
	dbSessions, err := w.repo.ListAllSessions(ctx)
	if err != nil {
		w.log.Error("watchdog: list sessions error", zap.Error(err))
		return
	}

	alive := 0
	reconnected := 0

	for _, s := range dbSessions {
		if w.waManager.Ping(s.JID) {
			alive++
			continue
		}

		// Sessão não está respondendo — tenta reconectar
		w.log.Warn("session not responding, attempting reconnect", zap.String("jid", s.JID))
		if err := w.waManager.Reconnect(ctx, &s); err != nil {
			w.log.Error("watchdog reconnect failed", zap.String("jid", s.JID), zap.Error(err))
			_ = w.repo.UpdateSessionStatus(ctx, s.JID, db.SessionDisconnected)
		} else {
			reconnected++
			w.log.Info("watchdog reconnected session", zap.String("jid", s.JID))
		}
	}

	w.log.Debug("watchdog heartbeat", zap.Int("alive", alive), zap.Int("reconnected", reconnected))
}
