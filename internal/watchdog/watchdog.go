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

	// falhas: numero da sessao -> tentativas de reconexao que falharam em
	// seguida. So' a goroutine do loop mexe, entao nao precisa de trava.
	falhas map[string]*tentativas
}

// tentativas guarda o ritmo de reconexao de uma sessao.
type tentativas struct {
	seguidas int
	proxima  time.Time
}

// esperaReconexao e' quanto esperar depois de N falhas seguidas: 30s, 1min,
// 2min, 4min, e para em 5min.
//
// O PORQUE: o watchdog tentava a cada 30s pra sempre. Muita reconexao em
// sequencia e' justamente o que faz o WhatsApp restringir o numero — e uma
// sessao que nao voltou em 30s raramente volta nos 30s seguintes.
func esperaReconexao(falhas int) time.Duration {
	if falhas <= 0 {
		return 0
	}
	d := 30 * time.Second
	for i := 1; i < falhas && d < 5*time.Minute; i++ {
		d *= 2
	}
	if d > 5*time.Minute {
		d = 5 * time.Minute
	}
	return d
}

func New(waManager *whatsapp.Manager, repo *db.Repository, cfg *config.WatchdogConfig, log *zap.Logger) *Watchdog {
	return &Watchdog{waManager: waManager, repo: repo, cfg: cfg, log: log, falhas: map[string]*tentativas{}}
}

// Start inicia o loop do watchdog em uma goroutine separada.
func (w *Watchdog) Start(ctx context.Context) {
	go w.loop(ctx)
	w.log.Info("watchdog started", zap.Duration("interval", w.cfg.PingInterval()))
}

func (w *Watchdog) loop(ctx context.Context) {
	ticker := time.NewTicker(w.cfg.PingInterval())
	defer ticker.Stop()

	// Primeiro check IMEDIATO (o ticker so' dispara apos o intervalo). Assim,
	// se a conexao inicial do LoadAll falhou por handshake transiente no boot
	// pos-deploy, o watchdog ja' tenta reconectar em segundos — sem esperar
	// o primeiro tick (ate 30s de sessao Desconectada a toa).
	w.check(ctx)

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
		// Cloud API: sem WebSocket — não há ping/reconnect.
		// Sessões Cloud são stateless via HTTPS, "sempre vivas" enquanto
		// o token for válido.
		if s.Type == db.SessionTypeCloudAPI {
			alive++
			continue
		}
		numero := whatsapp.PhoneFromJID(s.JID)
		if w.waManager.Ping(s.JID) {
			delete(w.falhas, numero) // voltou: proxima queda recomeca do ritmo normal
			alive++
			continue
		}
		// Sessao tomada por outro processo, whatsmeow desatualizado (405) ou
		// numero restrito: reconectar agora so' piora. Ver whatsapp/conexao.go.
		if pausada, motivo, falta := w.waManager.ReconexaoPausada(s.JID); pausada {
			w.log.Warn("watchdog: reconexao pausada",
				zap.String("jid", s.JID), zap.String("motivo", motivo),
				zap.Duration("volta_em", falta.Round(time.Second)))
			continue
		}

		t := w.falhas[numero]
		if t != nil && time.Now().Before(t.proxima) {
			continue // ainda no intervalo de espera desta sessao
		}

		// Toda tentativa conta, mesmo a que "deu certo": o Reconnect voltar
		// sem erro nao garante conexao — a recusa do WhatsApp chega depois,
		// por evento. O contador so' zera quando o Ping acima confirmar a
		// sessao viva num ciclo seguinte; senao uma sessao que conecta e cai
		// ficaria no ritmo de 30s pra sempre.
		if t == nil {
			t = &tentativas{}
			w.falhas[numero] = t
		}
		t.seguidas++
		espera := esperaReconexao(t.seguidas)
		t.proxima = time.Now().Add(espera)

		// Sessão não está respondendo — tenta reconectar
		w.log.Warn("session not responding, attempting reconnect",
			zap.String("jid", s.JID), zap.Int("tentativa", t.seguidas))
		if err := w.waManager.Reconnect(ctx, &s); err != nil {
			w.log.Error("watchdog reconnect failed", zap.String("jid", s.JID), zap.Error(err),
				zap.Duration("proxima_tentativa_em", espera))
			_ = w.repo.UpdateSessionStatus(ctx, s.JID, db.SessionDisconnected)
		} else {
			reconnected++
			w.log.Info("watchdog reconnected session", zap.String("jid", s.JID))
		}
	}

	w.log.Debug("watchdog heartbeat", zap.Int("alive", alive), zap.Int("reconnected", reconnected))
}
