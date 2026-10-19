package queue

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// ConfirmacaoPendente e' o que falta pra avisar o Bitrix que a mensagem do
// operador CHEGOU no celular do cliente.
//
// O PORQUE: no Bitrix o status "entregue" (imconnector.send.status.delivery)
// tambem marca a mensagem como LIDA pelo cliente, e e' a unica confirmacao
// que existe. A gente mandava esse status assim que o SendMessage voltava —
// e o SendMessage so' diz que o SERVIDOR do WhatsApp aceitou. Com o celular
// do cliente desligado, numero errado ou contato que bloqueou, o operador via
// "lida" numa mensagem que nunca chegou. Agora o status espera o recibo do
// aparelho (events.Receipt).
type ConfirmacaoPendente struct {
	SessionJID string `json:"session_jid"`
	Connector  string `json:"connector"`
	Line       int    `json:"line"`
	ImChatID   string `json:"im_chat_id"`
	ImMsgID    string `json:"im_msg_id"`
	ChatExtID  string `json:"chat_ext_id"`
}

const (
	prefixoPendente = "confirmacao:pendente:"
	prefixoRecibo   = "confirmacao:recibo:"
	// Celular desligado por dias ainda recebe quando liga — o WhatsApp
	// guarda a mensagem. Uma semana cobre isso sem acumular lixo.
	ttlPendente = 7 * 24 * time.Hour
	// O recibo pode chegar ANTES de a gente guardar a pendencia: cliente
	// online responde em milissegundos, e o recibo vem por outra goroutine.
	ttlRecibo = 10 * time.Minute
)

// GuardarConfirmacao registra a pendencia logo depois do envio. Se o recibo
// ja' chegou nesse meio tempo, devolve a pendencia pra quem chamou confirmar
// agora — senao devolve nil e quem confirma e' ReciboChegou.
//
// Junto com ReciboChegou, nunca confirma duas vezes nem perde a confirmacao:
// cada lado grava a sua marca ANTES de olhar a do outro, e so' quem conseguir
// o GETDEL da pendencia confirma.
func (q *Queue) GuardarConfirmacao(ctx context.Context, waID string, c ConfirmacaoPendente) (*ConfirmacaoPendente, error) {
	dados, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	if err := q.rdb.Set(ctx, prefixoPendente+waID, dados, ttlPendente).Err(); err != nil {
		return nil, err
	}
	n, err := q.rdb.Exists(ctx, prefixoRecibo+waID).Result()
	if err != nil || n == 0 {
		return nil, err
	}
	return q.tomarPendente(ctx, waID)
}

// ReciboChegou e' chamado quando o celular do cliente confirma o recebimento.
// Devolve a pendencia, se houver, e ela deixa de existir.
func (q *Queue) ReciboChegou(ctx context.Context, waID string) (*ConfirmacaoPendente, error) {
	if err := q.rdb.Set(ctx, prefixoRecibo+waID, "1", ttlRecibo).Err(); err != nil {
		return nil, err
	}
	return q.tomarPendente(ctx, waID)
}

func (q *Queue) tomarPendente(ctx context.Context, waID string) (*ConfirmacaoPendente, error) {
	dados, err := q.rdb.GetDel(ctx, prefixoPendente+waID).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c ConfirmacaoPendente
	if err := json.Unmarshal(dados, &c); err != nil {
		return nil, err
	}
	return &c, nil
}
