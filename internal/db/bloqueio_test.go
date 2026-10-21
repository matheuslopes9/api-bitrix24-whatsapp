package db

import (
	"context"
	"testing"
)

func TestChaveDominio(t *testing.T) {
	for _, d := range []string{"crm.uctechnology.com.br", "https://crm.uctechnology.com.br/", "HTTP://www.CRM.uctechnology.com.br", " crm.uctechnology.com.br "} {
		if got := chaveDominio(d); got != "crm.uctechnology.com.br" {
			t.Errorf("%q -> %q", d, got)
		}
	}
}

// Sem banco (ou com falha), nao bloqueia: em duvida, atender.
func TestPortalBloqueadoSemBancoNaoBloqueia(t *testing.T) {
	var r *Repository
	if bloq, _ := r.PortalBloqueado(context.Background(), "x.com"); bloq {
		t.Fatal("bloqueou sem conseguir consultar")
	}
}
