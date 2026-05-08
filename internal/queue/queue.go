package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/config"
	"go.uber.org/zap"
)

const (
	keyInbound  = "queue:inbound"   // mensagens WA → Bitrix
	keyOutbound = "queue:outbound"  // mensagens Bitrix → WA
	keyDead     = "queue:dead"      // falhou após todos os retries
)

// InboundJob representa uma mensagem recebida pelo WhatsApp aguardando entrega no Bitrix.
type InboundJob struct {
	ID          string    `json:"id"`
	SessionJID  string    `json:"session_jid"`
	SessionID   uuid.UUID `json:"session_id"`
	FromJID     string    `json:"from_jid"`
	FromPhone   string    `json:"from_phone"`
	FromName    string    `json:"from_name"`
	MessageID   string    `json:"message_id"`
	MessageType string    `json:"message_type"`
	Text        string    `json:"text,omitempty"`
	MediaURL    string    `json:"media_url,omitempty"`
	MediaMime   string    `json:"media_mime,omitempty"`
	MediaData   []byte    `json:"media_data,omitempty"`  // bytes da mídia já baixada
	MediaName   string    `json:"media_name,omitempty"` // nome do arquivo para exibição
	RetryCount  int       `json:"retry_count"`
	CreatedAt   time.Time `json:"created_at"`
}

// OutboundJob representa uma resposta do Bitrix aguardando envio para o WhatsApp.
type OutboundJob struct {
	ID         string    `json:"id"`
	SessionJID string    `json:"session_jid"`
	ToJID      string    `json:"to_jid"`
	MessageID  string    `json:"message_id"`
	Text       string    `json:"text,omitempty"`
	MediaURL   string    `json:"media_url,omitempty"`
	MediaMime  string    `json:"media_mime,omitempty"`
	RetryCount int       `json:"retry_count"`
	CreatedAt  time.Time `json:"created_at"`

	// Campos para confirmar delivery ao Bitrix após envio no WA
	BitrixConnector  string `json:"bitrix_connector,omitempty"`
	BitrixLine       int    `json:"bitrix_line,omitempty"`
	BitrixImChatID   string `json:"bitrix_im_chat_id,omitempty"`
	BitrixImMsgID    string `json:"bitrix_im_msg_id,omitempty"`
	BitrixChatExtID  string `json:"bitrix_chat_ext_id,omitempty"` // chat.id do evento

	// Arquivo enviado pelo operador (outbound)
	FileURL      string `json:"file_url,omitempty"`      // downloadLink do evento
	FileName     string `json:"file_name,omitempty"`
	FileMime     string `json:"file_mime,omitempty"`

	// Nome do operador que enviou (para salvar no histórico)
	OperatorName string `json:"operator_name,omitempty"`
}

// Queue gerencia as filas via Redis.
type Queue struct {
	rdb *redis.Client
	cfg *config.QueueConfig
	log *zap.Logger
}

func New(rdb *redis.Client, cfg *config.QueueConfig, log *zap.Logger) *Queue {
	return &Queue{rdb: rdb, cfg: cfg, log: log}
}

// PushInbound coloca uma mensagem na fila de entrada.
func (q *Queue) PushInbound(ctx context.Context, job *InboundJob) error {
	if job.ID == "" {
		job.ID = uuid.NewString()
	}
	job.CreatedAt = time.Now()
	return q.push(ctx, keyInbound, job)
}

// PopInbound retira um job da fila de entrada (blocking, timeout 5s).
func (q *Queue) PopInbound(ctx context.Context) (*InboundJob, error) {
	data, err := q.rdb.BLPop(ctx, 5*time.Second, keyInbound).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, err
	}
	var job InboundJob
	if err := json.Unmarshal([]byte(data[1]), &job); err != nil {
		return nil, fmt.Errorf("unmarshal inbound: %w", err)
	}
	return &job, nil
}

// PushOutbound coloca uma mensagem na fila de saída.
func (q *Queue) PushOutbound(ctx context.Context, job *OutboundJob) error {
	if job.ID == "" {
		job.ID = uuid.NewString()
	}
	job.CreatedAt = time.Now()
	return q.push(ctx, keyOutbound, job)
}

// PopOutbound retira um job da fila de saída (blocking, timeout 5s).
func (q *Queue) PopOutbound(ctx context.Context) (*OutboundJob, error) {
	data, err := q.rdb.BLPop(ctx, 5*time.Second, keyOutbound).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, err
	}
	var job OutboundJob
	if err := json.Unmarshal([]byte(data[1]), &job); err != nil {
		return nil, fmt.Errorf("unmarshal outbound: %w", err)
	}
	return &job, nil
}

// RetryInbound recoloca um job na fila com backoff exponencial.
func (q *Queue) RetryInbound(ctx context.Context, job *InboundJob) error {
	job.RetryCount++
	if job.RetryCount > q.cfg.MaxRetry {
		q.log.Warn("job moved to dead queue", zap.String("id", job.ID), zap.Int("retries", job.RetryCount))
		return q.push(ctx, keyDead, job)
	}
	delay := q.backoff(job.RetryCount)
	q.log.Info("retry inbound job", zap.String("id", job.ID), zap.Int("attempt", job.RetryCount), zap.Duration("delay", delay))
	time.Sleep(delay)
	return q.push(ctx, keyInbound, job)
}

// RetryOutbound recoloca um job de saída na fila.
func (q *Queue) RetryOutbound(ctx context.Context, job *OutboundJob) error {
	job.RetryCount++
	if job.RetryCount > q.cfg.MaxRetry {
		q.log.Warn("outbound job moved to dead queue", zap.String("id", job.ID))
		return q.push(ctx, keyDead, job)
	}
	delay := q.backoff(job.RetryCount)
	time.Sleep(delay)
	return q.push(ctx, keyOutbound, job)
}

// PeekDead retorna até n itens da dead queue sem removê-los (para diagnóstico).
func (q *Queue) PeekDead(ctx context.Context, n int) ([]json.RawMessage, error) {
	items, err := q.rdb.LRange(ctx, keyDead, 0, int64(n-1)).Result()
	if err != nil {
		return nil, err
	}
	out := make([]json.RawMessage, 0, len(items))
	for _, s := range items {
		out = append(out, json.RawMessage(s))
	}
	return out, nil
}

// MarkProcessed registra um ID como já processado por TTL segundos.
// Retorna true se este é o primeiro processamento; false se já foi processado.
// Usado para deduplicar eventos do Bitrix que podem chegar 2x (ex: ONIMCONNECTORMESSAGEADD).
func (q *Queue) MarkProcessed(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	// SETNX = SET if Not eXists. Retorna 1 se setou (primeiro), 0 se já existia (duplicado).
	ok, err := q.rdb.SetNX(ctx, "dedup:"+key, "1", ttl).Result()
	if err != nil {
		return false, err
	}
	return ok, nil
}

// MediaCache representa um arquivo armazenado temporariamente no Redis para
// servir via endpoint público. Usado pelo Cloud API para enviar tipos não
// suportados no upload direto (zip, tar, msi, etc.) — a Meta baixa via link.
type MediaCache struct {
	Data     []byte `json:"data"`
	Mime     string `json:"mime"`
	FileName string `json:"file_name"`
}

// StoreMedia salva bytes + metadados no Redis com TTL (default 1 hora).
// Retorna o token aleatório que vai compor a URL pública.
func (q *Queue) StoreMedia(ctx context.Context, token string, m *MediaCache, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = time.Hour
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return q.rdb.Set(ctx, "cloudmedia:"+token, data, ttl).Err()
}

// LoadMedia recupera os bytes + metadados pelo token. Retorna nil se expirou.
func (q *Queue) LoadMedia(ctx context.Context, token string) (*MediaCache, error) {
	raw, err := q.rdb.Get(ctx, "cloudmedia:"+token).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m MediaCache
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// Lengths retorna o tamanho atual das filas (para telemetria).
func (q *Queue) Lengths(ctx context.Context) (inbound, outbound, dead int64) {
	inbound, _ = q.rdb.LLen(ctx, keyInbound).Result()
	outbound, _ = q.rdb.LLen(ctx, keyOutbound).Result()
	dead, _ = q.rdb.LLen(ctx, keyDead).Result()
	return
}

func (q *Queue) push(ctx context.Context, key string, v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return q.rdb.RPush(ctx, key, data).Err()
}

// backoff exponencial: base * 2^(retry-1), máximo 5 minutos.
func (q *Queue) backoff(retry int) time.Duration {
	base := q.cfg.RetryBaseDelay()
	d := base
	for i := 1; i < retry; i++ {
		d *= 2
		if d > 5*time.Minute {
			return 5 * time.Minute
		}
	}
	return d
}
