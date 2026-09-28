package api

// tarefas.go — contencao de panico nas tarefas de fundo.
//
// O PROBLEMA: o app dispara 16 goroutines de fundo e NENHUMA tinha recover().
// Em Go, panico em goroutine nao sobe pro chamador: derruba o PROCESSO. O
// middleware recover do Fiber (server.go) so' cobre o que roda dentro de um
// handler HTTP — jobs periodicos e o "go func()" solto depois da resposta
// ficam de fora.
//
// O efeito de um unico nil map ou index fora de faixa num job de alertas seria
// o connector inteiro caindo, para TODOS os clientes, por um defeito numa
// tarefa acessoria. E cairia sem explicacao util: o processo morre e o
// orquestrador sobe outro.
//
// Os tres jobs perpetuos (alertas, reprocesso da fila, aviso de licenca) sao os
// mais expostos: rodam para sempre, tocam banco e Bitrix a cada volta, e uma
// unica iteracao ruim bastaria.

import (
	"runtime/debug"

	"go.uber.org/zap"
)

// semPanico roda fn e converte panico em log.
//
// Envolve UMA ITERACAO, nao o laco inteiro, de proposito: assim uma volta ruim
// nao mata o job junto. O ciclo seguinte roda normalmente — que e' o que se
// espera de um job periodico diante de um dado estranho pontual.
//
// A stack vai no log porque panico contido sem stack e' quase impossivel de
// diagnosticar depois: nao ha crash, nao ha core, so' um erro solto.
func semPanico(log *zap.Logger, tarefa string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("panico contido em tarefa de fundo — o app continua de pe",
				zap.String("tarefa", tarefa),
				zap.Any("panico", r),
				zap.ByteString("stack", debug.Stack()),
			)
		}
	}()
	fn()
}
