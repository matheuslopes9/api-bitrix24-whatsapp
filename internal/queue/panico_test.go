package queue

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// Panico em goroutine derruba o PROCESSO inteiro em Go — nao so' a goroutine.
// Como todo o trafego de mensagem passa pelos workers, um unico nil inesperado
// no processamento tirava o connector do ar para TODOS os clientes.
//
// Estes testes provam que o panico vira erro comum, que segue o caminho normal
// de retry em vez de matar o app.
func TestPanicoNoProcessamentoViraErro(t *testing.T) {
	wp := &WorkerPool{log: zap.NewNop()}

	err := wp.semPanico(context.Background(), "inbound", "job-1", func() error {
		var m map[string]string
		m["explode"] = "agora" // panico: escrita em mapa nil
		return nil
	})

	if err == nil {
		t.Fatal("panico foi engolido — o worker seguiria como se tivesse dado certo")
	}
	if !strings.Contains(err.Error(), "panico") {
		t.Errorf("erro nao identifica a causa: %v", err)
	}
}

// Nil pointer e' o panico mais provavel aqui: o processador navega structs
// vindas de JSON, onde campo ausente vira ponteiro nil.
func TestNilPointerTambemEhContido(t *testing.T) {
	wp := &WorkerPool{log: zap.NewNop()}
	type registro struct{ Nome string }

	err := wp.semPanico(context.Background(), "outbound", "job-2", func() error {
		var r *registro
		_ = r.Nome // panico
		return nil
	})
	if err == nil {
		t.Fatal("nil pointer nao foi contido")
	}
}

// Erro normal tem que passar INTACTO: quem chama decide retry a partir dele, e
// trocar o erro por outro texto quebraria a classificacao da fila morta.
func TestErroComumPassaIntacto(t *testing.T) {
	wp := &WorkerPool{log: zap.NewNop()}
	original := errors.New("numero nao existe no whatsapp")

	err := wp.semPanico(context.Background(), "outbound", "job-3", func() error {
		return original
	})
	if !errors.Is(err, original) {
		t.Fatalf("erro original se perdeu: %v", err)
	}
}

// Sucesso continua sucesso — a protecao nao pode inventar falha.
func TestSucessoContinuaSucesso(t *testing.T) {
	wp := &WorkerPool{log: zap.NewNop()}
	if err := wp.semPanico(context.Background(), "inbound", "job-4", func() error { return nil }); err != nil {
		t.Fatalf("inventou erro onde nao havia: %v", err)
	}
}
