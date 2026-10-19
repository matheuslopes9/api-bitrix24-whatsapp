package queue

import (
	"context"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/config"
	"go.uber.org/zap"
)

func filaDeTeste(t *testing.T) *Queue {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return New(rdb, &config.QueueConfig{MaxRetry: 2}, zap.NewNop())
}

var pendTeste = ConfirmacaoPendente{SessionJID: "5519@s.whatsapp.net", Connector: "uc", Line: 7, ImMsgID: "74533"}

// Caso normal: guarda depois do envio, o recibo do celular chega depois e
// e' ele quem confirma — uma vez so'.
func TestConfirmacaoQuandoReciboChegaDepois(t *testing.T) {
	q, ctx := filaDeTeste(t), context.Background()
	if ja, err := q.GuardarConfirmacao(ctx, "WA1", pendTeste); err != nil || ja != nil {
		t.Fatalf("sem recibo ainda, nao era pra confirmar agora: %v %v", ja, err)
	}
	pend, err := q.ReciboChegou(ctx, "WA1")
	if err != nil || pend == nil || pend.ImMsgID != "74533" {
		t.Fatalf("o recibo devia devolver a pendencia: %+v %v", pend, err)
	}
	// "lido" depois do "entregue": nao confirma de novo.
	if pend, _ := q.ReciboChegou(ctx, "WA1"); pend != nil {
		t.Fatal("segundo recibo confirmou de novo")
	}
}

// Cliente online responde em milissegundos: o recibo chega antes de a
// pendencia ser gravada. Quem confirma entao e' quem guarda.
func TestConfirmacaoQuandoReciboChegaAntes(t *testing.T) {
	q, ctx := filaDeTeste(t), context.Background()
	if pend, _ := q.ReciboChegou(ctx, "WA2"); pend != nil {
		t.Fatal("sem pendencia, nao havia o que confirmar")
	}
	ja, err := q.GuardarConfirmacao(ctx, "WA2", pendTeste)
	if err != nil || ja == nil {
		t.Fatalf("o recibo ja' tinha chegado: era pra confirmar na hora: %v %v", ja, err)
	}
	if pend, _ := q.ReciboChegou(ctx, "WA2"); pend != nil {
		t.Fatal("confirmou duas vezes")
	}
}

// Recibo de mensagem que nao veio do operador do Bitrix (aba do CRM, robo):
// nao ha' pendencia, nada a confirmar.
func TestReciboSemPendencia(t *testing.T) {
	q, ctx := filaDeTeste(t), context.Background()
	if pend, err := q.ReciboChegou(ctx, "WA3"); err != nil || pend != nil {
		t.Fatalf("%+v %v", pend, err)
	}
}

// A fila desistindo da mensagem tem que avisar — nas duas portas pra dead
// queue: falha permanente e tentativas esgotadas. E so' uma vez.
func TestDesistenciaAvisaOperador(t *testing.T) {
	q, ctx := filaDeTeste(t), context.Background()
	var avisados []string
	q.AoDesistirDeSaida(func(job *OutboundJob) { avisados = append(avisados, job.ID) })

	perm := &OutboundJob{ID: "permanente"}
	_ = q.RetryOutbound(ctx, perm, errors.New("numero 5581999887766 nao esta no WhatsApp"))

	esgotado := &OutboundJob{ID: "esgotado", RetryCount: 2}
	_ = q.RetryOutbound(ctx, esgotado, errors.New("timeout"))

	if len(avisados) != 2 || avisados[0] != "permanente" || avisados[1] != "esgotado" {
		t.Fatalf("avisos: %v", avisados)
	}
	if esgotado.LastError != "timeout" {
		t.Fatalf("o aviso precisa do motivo: %q", esgotado.LastError)
	}
}
