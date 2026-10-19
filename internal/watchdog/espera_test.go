package watchdog

import (
	"testing"
	"time"
)

func TestEsperaReconexaoCresceEParaEmCincoMinutos(t *testing.T) {
	quer := map[int]time.Duration{
		0:  0,
		1:  30 * time.Second,
		2:  time.Minute,
		3:  2 * time.Minute,
		4:  4 * time.Minute,
		5:  5 * time.Minute,
		50: 5 * time.Minute,
	}
	for falhas, d := range quer {
		if got := esperaReconexao(falhas); got != d {
			t.Errorf("esperaReconexao(%d) = %v, queria %v", falhas, got, d)
		}
	}
}
