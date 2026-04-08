package telemetry

import (
	"github.com/gofiber/fiber/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/valyala/fasthttp/fasthttpadaptor"
)

// Metrics agrupa todos os contadores Prometheus.
type Metrics struct {
	MessagesInbound  prometheus.Counter
	MessagesOutbound prometheus.Counter
	MessagesFailed   prometheus.Counter
	ActiveSessions   prometheus.Gauge
	QueueInbound     prometheus.Gauge
	QueueOutbound    prometheus.Gauge
	QueueDead        prometheus.Gauge
	BitrixErrors     prometheus.Counter
}

func New() *Metrics {
	m := &Metrics{
		MessagesInbound: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "wa_messages_inbound_total",
			Help: "Total de mensagens recebidas do WhatsApp",
		}),
		MessagesOutbound: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "wa_messages_outbound_total",
			Help: "Total de mensagens enviadas pelo WhatsApp",
		}),
		MessagesFailed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "wa_messages_failed_total",
			Help: "Total de mensagens que falharam em todas as tentativas",
		}),
		ActiveSessions: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "wa_active_sessions",
			Help: "Número de sessões WhatsApp ativas",
		}),
		QueueInbound: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "queue_inbound_length",
			Help: "Tamanho atual da fila inbound",
		}),
		QueueOutbound: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "queue_outbound_length",
			Help: "Tamanho atual da fila outbound",
		}),
		QueueDead: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "queue_dead_length",
			Help: "Tamanho atual da fila dead-letter",
		}),
		BitrixErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "bitrix_errors_total",
			Help: "Total de erros nas chamadas ao Bitrix24",
		}),
	}

	prometheus.MustRegister(
		m.MessagesInbound,
		m.MessagesOutbound,
		m.MessagesFailed,
		m.ActiveSessions,
		m.QueueInbound,
		m.QueueOutbound,
		m.QueueDead,
		m.BitrixErrors,
	)

	return m
}

// Handler retorna o handler do Fiber para expor as métricas no /metrics.
func (m *Metrics) Handler() fiber.Handler {
	h := fasthttpadaptor.NewFastHTTPHandler(promhttp.Handler())
	return func(c *fiber.Ctx) error {
		h(c.Context())
		return nil
	}
}
