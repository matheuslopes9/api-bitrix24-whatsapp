package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/config"
	"go.uber.org/zap"
)

const (
	keyInbound  = "queue:inbound"  // mensagens WA → Bitrix
	keyOutbound = "queue:outbound" // mensagens Bitrix → WA
	keyDead     = "queue:dead"     // falhou após todos os retries
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
	MediaData   []byte    `json:"media_data,omitempty"` // bytes da mídia já baixada
	MediaName   string    `json:"media_name,omitempty"` // nome do arquivo para exibição
	// Grupo: se a msg veio de grupo, GroupJID e' o JID do grupo (XXX@g.us)
	// e GroupName e' o nome do grupo. Quando preenchido, o chat e' aberto
	// no Bitrix como 1 chat unico do grupo (em vez de 1 chat por participante),
	// e Text recebe prefixo "*Nome do remetente:* texto" pra atendente
	// distinguir quem mandou.
	IsGroup    bool      `json:"is_group,omitempty"`
	GroupJID   string    `json:"group_jid,omitempty"`
	GroupName  string    `json:"group_name,omitempty"`
	RetryCount int       `json:"retry_count"`
	CreatedAt  time.Time `json:"created_at"`
	// LastError guarda o motivo da ultima falha. Sem isso o job morria na
	// dead queue mudo: dava pra ver QUE falhou, nunca POR QUE — e o
	// diagnostico virava adivinhacao.
	LastError string `json:"last_error,omitempty"`

	// LimiteHits conta quantas vezes este job esbarrou em limite de taxa do
	// WhatsApp. Separado de RetryCount de proposito: 429 e' condicao
	// TEMPORARIA, nao defeito da mensagem. Contar junto fazia a mensagem
	// morrer por um problema que ia passar sozinho.
	LimiteHits int `json:"limite_hits,omitempty"`

	// AutoReprocessos conta quantas vezes o job voltou pra fila pelo
	// reprocessamento AUTOMATICO. Existe pra job que falha sempre nao ficar
	// eternamente indo e voltando entre a fila e a dead queue, queimando
	// recurso e escondendo os que ainda tem chance. O reprocesso manual
	// ignora esse limite: ali tem gente decidindo.
	AutoReprocessos int `json:"auto_reprocessos,omitempty"`

	// CRMPhone e' o telefone NA FORMA QUE O CRM GUARDA, quando ela difere do
	// numero real do WhatsApp (tipicamente o 9o digito).
	//
	// Sao dois papeis diferentes que antes eram o mesmo campo:
	//   - identidade da conversa -> tem que ser o numero REAL, senao ida e
	//     volta discordam e o Bitrix abre dois dialogos;
	//   - telefone pra casar o contato -> tem que ser a forma do CRM, senao
	//     o Bitrix nao acha o contato que ja' existe e cria outro.
	// Vazio = usar FromPhone pros dois.
	CRMPhone string `json:"crm_phone,omitempty"`
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
	BitrixConnector string `json:"bitrix_connector,omitempty"`
	BitrixLine      int    `json:"bitrix_line,omitempty"`
	BitrixImChatID  string `json:"bitrix_im_chat_id,omitempty"`
	BitrixImMsgID   string `json:"bitrix_im_msg_id,omitempty"`
	BitrixChatExtID string `json:"bitrix_chat_ext_id,omitempty"` // chat.id do evento

	// Arquivo enviado pelo operador (outbound)
	FileURL  string `json:"file_url,omitempty"` // downloadLink do evento
	FileName string `json:"file_name,omitempty"`
	FileMime string `json:"file_mime,omitempty"`
	FileSize int64  `json:"file_size,omitempty"` // bytes — vem no webhook do Bitrix (files[0][size])

	// Nome do operador que enviou (para salvar no histórico)
	OperatorName string `json:"operator_name,omitempty"`

	// LastError: ver InboundJob.LastError.
	LastError string `json:"last_error,omitempty"`

	// LimiteHits: ver InboundJob.LimiteHits.
	LimiteHits int `json:"limite_hits,omitempty"`

	// AutoReprocessos: ver InboundJob.AutoReprocessos.
	AutoReprocessos int `json:"auto_reprocessos,omitempty"`
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

// EhLimiteDeTaxa reconhece o 429 do WhatsApp.
//
// Vem como "rate-overlimit" na resposta da consulta usync, que o whatsmeow
// dispara inclusive DENTRO do SendMessage pra descobrir o LID do destino.
// Nao e' erro da mensagem: e' o servidor pedindo pra esperar.
func EhLimiteDeTaxa(err error) bool {
	if err == nil {
		return false
	}
	t := strings.ToLower(err.Error())
	return strings.Contains(t, "rate-overlimit") ||
		strings.Contains(t, "status 429") ||
		strings.Contains(t, "too many requests")
}

// EhFalhaPermanente reconhece o que NAO adianta tentar de novo.
//
// O caso real: numero que nao existe no WhatsApp. O whatsmeow devolve
// "no LID found for X from server" — o servidor respondeu, e a resposta foi
// que aquele numero nao tem conta. Tentar 6 vezes so' gasta cota de usync
// (que e' limitada e cuja falta derruba os envios que TEM chance) e enche a
// dead queue de coisa que nunca vai sair.
//
// Isso acontece bastante com a base do cliente: telefone do CRM guardado com
// o 9o digito quando a conta so' existe sem ele, ou contato que simplesmente
// nao usa WhatsApp.
func EhFalhaPermanente(err error) bool {
	if err == nil {
		return false
	}
	t := strings.ToLower(err.Error())
	return strings.Contains(t, "no lid found") ||
		strings.Contains(t, "nao esta no whatsapp") ||
		strings.Contains(t, "is not on whatsapp")
}

// limiteMaxHits e' quantas vezes se espera o limite passar antes de desistir.
// Com o backoff abaixo isso da' mais de uma hora de paciencia — tempo de
// sobra pra uma janela de rate limit passar.
const limiteMaxHits = 12

// esperaPorLimite cresce devagar e para em 10 minutos. Backoff curto sob 429
// so' piora: cada tentativa consome mais cota e estica a punicao.
func esperaPorLimite(hits int) time.Duration {
	d := time.Duration(hits) * 30 * time.Second
	if d > 10*time.Minute {
		return 10 * time.Minute
	}
	return d
}

// RetryInbound recoloca um job na fila com backoff exponencial.
func (q *Queue) RetryInbound(ctx context.Context, job *InboundJob, motivo error) error {
	if motivo != nil {
		job.LastError = motivo.Error()
	}
	if EhFalhaPermanente(motivo) {
		q.log.Warn("falha permanente — nao adianta tentar de novo",
			zap.String("id", job.ID), zap.String("motivo", job.LastError))
		job.RetryCount = q.cfg.MaxRetry + 1 // marca como esgotado, sem tentar
		return q.push(ctx, keyDead, job)
	}
	if EhLimiteDeTaxa(motivo) {
		job.LimiteHits++
		if job.LimiteHits <= limiteMaxHits {
			espera := esperaPorLimite(job.LimiteHits)
			q.log.Warn("limite de taxa do WhatsApp — aguardando sem gastar tentativa",
				zap.String("id", job.ID), zap.Int("hits", job.LimiteHits),
				zap.Duration("espera", espera))
			time.Sleep(espera)
			return q.push(ctx, keyInbound, job)
		}
	}
	job.RetryCount++
	if job.RetryCount > q.cfg.MaxRetry {
		q.log.Warn("job moved to dead queue",
			zap.String("id", job.ID), zap.Int("retries", job.RetryCount),
			zap.String("motivo", job.LastError))
		return q.push(ctx, keyDead, job)
	}
	delay := q.backoff(job.RetryCount)
	q.log.Info("retry inbound job", zap.String("id", job.ID), zap.Int("attempt", job.RetryCount), zap.Duration("delay", delay))
	time.Sleep(delay)
	return q.push(ctx, keyInbound, job)
}

// RetryOutbound recoloca um job de saída na fila.
func (q *Queue) RetryOutbound(ctx context.Context, job *OutboundJob, motivo error) error {
	if motivo != nil {
		job.LastError = motivo.Error()
	}
	if EhFalhaPermanente(motivo) {
		q.log.Warn("falha permanente — nao adianta tentar de novo",
			zap.String("id", job.ID), zap.String("para", job.ToJID),
			zap.String("motivo", job.LastError))
		job.RetryCount = q.cfg.MaxRetry + 1 // marca como esgotado, sem tentar
		return q.push(ctx, keyDead, job)
	}
	if EhLimiteDeTaxa(motivo) {
		job.LimiteHits++
		if job.LimiteHits <= limiteMaxHits {
			espera := esperaPorLimite(job.LimiteHits)
			q.log.Warn("limite de taxa do WhatsApp — aguardando sem gastar tentativa",
				zap.String("id", job.ID), zap.Int("hits", job.LimiteHits),
				zap.Duration("espera", espera))
			time.Sleep(espera)
			return q.push(ctx, keyOutbound, job)
		}
	}
	job.RetryCount++
	if job.RetryCount > q.cfg.MaxRetry {
		q.log.Warn("outbound job moved to dead queue",
			zap.String("id", job.ID), zap.String("motivo", job.LastError))
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

// DirecaoDead diz de que fila um item da dead queue veio.
type DirecaoDead int

const (
	// DirecaoIndefinida: nao da' pra saber a direcao, o item nao e'
	// processavel.
	DirecaoIndefinida DirecaoDead = iota
	DirecaoEntrada
	DirecaoSaida
)

// SondaDead e' o minimo que precisa ser lido de um item da dead queue pra
// decidir o que fazer com ele.
type SondaDead struct {
	ID         string
	SessionJID string
	Direcao    DirecaoDead
	// UltimoErro permite decidir, antes de reenfileirar, se vale a pena.
	UltimoErro string
}

// classificarDead descobre se um item bruto da dead queue e' de entrada ou
// de saida.
//
// Nao da' pra confiar em "decodificou sem erro": encoding/json ignora campo
// desconhecido e zera campo ausente, entao QUALQUER um dos dois jobs
// decodifica limpo em qualquer um dos dois structs. O que separa e' campo
// exclusivo: to_jid so' existe na saida, from_jid so' na entrada.
//
// A ordem importa: to_jid e' checado primeiro porque confundir saida com
// entrada e' o erro caro — devolve a mensagem do atendente pro Contact
// Center como se fosse cliente novo e anonimo.
func classificarDead(raw []byte) (SondaDead, error) {
	var bruto struct {
		ID         string `json:"id"`
		SessionJID string `json:"session_jid"`
		FromJID    string `json:"from_jid"`
		ToJID      string `json:"to_jid"`
		LastError  string `json:"last_error"`
	}
	if err := json.Unmarshal(raw, &bruto); err != nil {
		return SondaDead{}, err
	}
	s := SondaDead{ID: bruto.ID, SessionJID: bruto.SessionJID, UltimoErro: bruto.LastError}
	switch {
	case strings.TrimSpace(bruto.ToJID) != "":
		s.Direcao = DirecaoSaida
	case strings.TrimSpace(bruto.FromJID) != "":
		s.Direcao = DirecaoEntrada
	}
	return s, nil
}

// ReprocessarDead devolve os jobs da dead queue para as filas de origem.
//
// BUG QUE ISSO CORRIGE: a dead queue e' UMA lista so' — RetryInbound e
// RetryOutbound empurram para a mesma chave. A versao anterior desta funcao
// decodificava TUDO como InboundJob e reenfileirava tudo como entrada.
//
// encoding/json nao reclama disso: campos desconhecidos sao ignorados e os
// ausentes ficam zerados. Um OutboundJob virava um InboundJob "valido" com
// from_jid, from_phone e from_name VAZIOS, e o texto ainda trazia o prefixo
// do operador ("*Fulano:* oi"). O processador entao abria um chat no Contact
// Center sem identidade nenhuma — e o Bitrix rotula isso como "Guest".
// Resultado: a propria mensagem do atendente voltava como se fosse um cliente
// novo e anonimo, um chat "Guest" por mensagem reprocessada.
//
// Agora cada item e' classificado antes de voltar pra fila. O discriminador
// e' estrutural e vale tambem pros itens antigos ja' gravados: to_jid so'
// existe na saida, from_jid so' na entrada. Quem nao tem nenhum dos dois nao
// da' pra processar e e' descartado em vez de virar um chat fantasma.
// maxAutoReprocessos e' quantas voltas automaticas um job pode levar antes de
// ficar parado esperando decisao humana.
const maxAutoReprocessos = 3

// ReprocessarDead mantem a assinatura antiga pro caminho MANUAL, onde tem
// gente decidindo e nao ha limite de voltas.
func (q *Queue) ReprocessarDead(ctx context.Context, sessoesValidas map[string]bool) (int, int, error) {
	return q.reprocessarDead(ctx, sessoesValidas, false)
}

// ReprocessarDeadAuto e' a versao periodica: respeita maxAutoReprocessos pra
// job cronicamente quebrado nao circular pra sempre.
func (q *Queue) ReprocessarDeadAuto(ctx context.Context, sessoesValidas map[string]bool) (int, int, error) {
	return q.reprocessarDead(ctx, sessoesValidas, true)
}

func (q *Queue) reprocessarDead(ctx context.Context, sessoesValidas map[string]bool, automatico bool) (int, int, error) {
	itens, err := q.rdb.LRange(ctx, keyDead, 0, -1).Result()
	if err != nil {
		return 0, 0, err
	}
	if len(itens) == 0 {
		return 0, 0, nil
	}
	if err := q.rdb.Del(ctx, keyDead).Err(); err != nil {
		return 0, 0, err
	}

	var reenfileirados, descartados int
	for _, raw := range itens {
		sonda, err := classificarDead([]byte(raw))
		if err != nil {
			descartados++
			continue
		}
		if !sessoesValidas[numeroDoJID(sonda.SessionJID)] {
			descartados++
			q.log.Info("dead queue: descartado (sessao nao existe mais)",
				zap.String("id", sonda.ID), zap.String("session_jid", sonda.SessionJID))
			continue
		}

		// Reprocesso AUTOMATICO nao insiste no que ja' se sabe que nao sai:
		// numero que nao existe no WhatsApp vai continuar nao existindo.
		// O manual ignora isso — ali tem gente decidindo.
		if automatico && EhFalhaPermanente(errors.New(sonda.UltimoErro)) {
			_ = q.rdb.RPush(ctx, keyDead, raw).Err()
			continue
		}

		switch sonda.Direcao {
		case DirecaoSaida:
			var job OutboundJob
			if err := json.Unmarshal([]byte(raw), &job); err != nil {
				descartados++
				continue
			}
			if automatico && job.AutoReprocessos >= maxAutoReprocessos {
				_ = q.push(ctx, keyDead, &job) // devolve, sem contar como descarte
				continue
			}
			job.RetryCount = 0
			job.LimiteHits = 0
			if automatico {
				job.AutoReprocessos++
			}
			if err := q.PushOutbound(ctx, &job); err != nil {
				q.log.Warn("dead queue: falha ao reenfileirar saida",
					zap.String("id", job.ID), zap.Error(err))
				descartados++
				continue
			}
			reenfileirados++
		case DirecaoEntrada:
			var job InboundJob
			if err := json.Unmarshal([]byte(raw), &job); err != nil {
				descartados++
				continue
			}
			if automatico && job.AutoReprocessos >= maxAutoReprocessos {
				_ = q.push(ctx, keyDead, &job) // devolve, sem contar como descarte
				continue
			}
			job.RetryCount = 0
			job.LimiteHits = 0
			if automatico {
				job.AutoReprocessos++
			}
			if err := q.PushInbound(ctx, &job); err != nil {
				q.log.Warn("dead queue: falha ao reenfileirar entrada",
					zap.String("id", job.ID), zap.Error(err))
				descartados++
				continue
			}
			reenfileirados++
		default:
			descartados++
			q.log.Warn("dead queue: descartado (sem from_jid nem to_jid — nao da' pra saber a direcao)",
				zap.String("id", sonda.ID))
		}
	}
	q.log.Info("dead queue reprocessada",
		zap.Int("reenfileirados", reenfileirados), zap.Int("descartados", descartados))
	return reenfileirados, descartados, nil
}

// numeroDoJID tira device suffix e dominio: "5581...:5@s.whatsapp.net" -> "5581..."
func numeroDoJID(jid string) string {
	if i := strings.IndexByte(jid, '@'); i > 0 {
		jid = jid[:i]
	}
	if i := strings.IndexByte(jid, ':'); i > 0 {
		jid = jid[:i]
	}
	return jid
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

// LengthsDe conta, em cada fila, so' os jobs cuja sessao o filtro aceita.
//
// Existe porque Lengths e' global: o painel do cliente mostrava a fila e as
// falhas de TODOS os clientes como se fossem dele. Le as listas inteiras, o
// que e' aceitavel porque entrada e saida ficam perto de zero em operacao
// normal e a dead queue e' justamente o que precisa estar pequena.
func (q *Queue) LengthsDe(ctx context.Context, aceita func(sessionJID string) bool) (inbound, outbound, dead int64) {
	contar := func(key string) int64 {
		itens, err := q.rdb.LRange(ctx, key, 0, -1).Result()
		if err != nil {
			return 0
		}
		var n int64
		for _, s := range itens {
			var j struct {
				SessionJID string `json:"session_jid"`
			}
			if json.Unmarshal([]byte(s), &j) == nil && aceita(j.SessionJID) {
				n++
			}
		}
		return n
	}
	return contar(keyInbound), contar(keyOutbound), contar(keyDead)
}

// Ping verifica conectividade com o Redis (pro monitoramento do admin).
func (q *Queue) Ping(ctx context.Context) error {
	return q.rdb.Ping(ctx).Err()
}

// Flush descarta TODOS os jobs das filas especificadas. Retorna os contadores
// removidos. Útil para limpar acúmulo de testes ou em emergências.
// kinds aceita "inbound", "outbound", "dead" — passar []string{"inbound","outbound","dead"} limpa tudo.
func (q *Queue) Flush(ctx context.Context, kinds []string) (removed map[string]int64, err error) {
	removed = map[string]int64{}
	for _, k := range kinds {
		var key string
		switch k {
		case "inbound":
			key = keyInbound
		case "outbound":
			key = keyOutbound
		case "dead":
			key = keyDead
		default:
			continue
		}
		n, _ := q.rdb.LLen(ctx, key).Result()
		if _, derr := q.rdb.Del(ctx, key).Result(); derr != nil {
			return removed, derr
		}
		removed[k] = n
	}
	return removed, nil
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
