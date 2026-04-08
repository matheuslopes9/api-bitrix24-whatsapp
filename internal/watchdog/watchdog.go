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
	sessions := w.waManager.ListSessions()
	alive := 0
	dead := 0

	for _, jid := range sessions {
		if w.waManager.Ping(jid) {
			alive++
		} else {
			dead++
			w.log.Warn("session not responding, attempting reconnect", zap.String("jid", jid))
			_ = w.repo.UpdateSessionStatus(ctx, jid, db.SessionDisconnected)
		}
	}

	// Tenta recarregar sessões desconectadas do banco
	if dead > 0 {
		dbSessions, err := w.repo.ListActiveSessions(ctx)
		if err != nil {
			w.log.Error("watchdog: list sessions error", zap.Error(err))
			return
		}

		reconnected := 0
		for _, s := range dbSessions {
			if !w.waManager.Ping(s.JID) {
				// A reconexão é feita via LoadAll, aqui apenas logamos
				w.log.Info("session needs reconnect", zap.String("jid", s.JID), zap.String("phone", s.Phone))
				reconnected++
			}
		}

		if reconnected > 0 {
			w.log.Info("watchdog reconnect triggered", zap.Int("count", reconnected))
		}
	}

	w.log.Debug("watchdog heartbeat", zap.Int("alive", alive), zap.Int("dead", dead))
}
