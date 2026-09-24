// reprocesso.go — devolve sozinho a fila presa, de tempos em tempos.
//
// POR QUE EXISTE: mensagem parava na dead queue por condicao TEMPORARIA —
// limite de taxa do WhatsApp, token renovando, Bitrix lento — e ficava la'
// ate' alguem abrir o painel e clicar. Na pratica isso significava cliente
// esperando resposta por horas, de madrugada e fim de semana, por um
// problema que ja' tinha passado sozinho.
//
// O botao manual continua: aqui so' entram as voltas automaticas, limitadas
// por AutoReprocessos pra job cronicamente quebrado nao circular pra sempre.
package api

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// intervaloReprocesso: curto o bastante pra cliente nao esperar horas, longo
// o bastante pra dar tempo da condicao temporaria passar.
const intervaloReprocesso = 15 * time.Minute

func (h *handlers) IniciarReprocessoAutomatico(ctx context.Context) {
	if h.q == nil {
		return
	}
	go func() {
		// Deixa o boot assentar: sessoes ainda estao carregando e reentregar
		// agora so' geraria falha nova.
		time.Sleep(3 * time.Minute)
		t := time.NewTicker(intervaloReprocesso)
		defer t.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			h.reprocessarPresas(ctx)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	h.log.Info("reprocessamento automatico da fila ativo",
		zap.Duration("intervalo", intervaloReprocesso),
		zap.Int("voltas_maximas_por_job", 3))
}

func (h *handlers) reprocessarPresas(pai context.Context) {
	ctx, cancel := context.WithTimeout(pai, 2*time.Minute)
	defer cancel()

	// So' reentrega pra numero REALMENTE conectado: mandar pra sessao morta
	// gera falha nova e enche a dead queue de novo.
	validos := map[string]bool{}
	if h.waManager != nil {
		for _, s := range h.waManager.ConnectedSessions() {
			validos[s.Phone] = true
		}
	}
	if len(validos) == 0 {
		return
	}
	reenf, desc, err := h.q.ReprocessarDeadAuto(ctx, validos)
	if err != nil {
		h.log.Warn("reprocesso automatico falhou", zap.Error(err))
		return
	}
	if reenf == 0 && desc == 0 {
		return // nada preso: silencio, pra nao poluir o log a cada 15min
	}
	h.log.Info("reprocesso automatico da fila",
		zap.Int("reenfileiradas", reenf), zap.Int("descartadas", desc))
}
