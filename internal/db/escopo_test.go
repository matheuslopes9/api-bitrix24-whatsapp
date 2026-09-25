package db

import (
	"strings"
	"testing"
)

// A expressao SQL tem que preservar o prefixo "cloud:". Se ela reduzir
// "cloud:123" a "cloud", toda Cloud API casa com toda Cloud API — o mesmo
// vazamento entre clientes que o escopo existe pra impedir.
func TestSQLNumeroBasePreservaPrefixoCloud(t *testing.T) {
	sql := sqlNumeroBase("x")
	if !strings.Contains(sql, "LIKE 'cloud:%'") {
		t.Fatalf("expressao nao trata Cloud separadamente: %s", sql)
	}
	if strings.Contains(sql, "%!") {
		t.Fatalf("erro de formatacao na expressao: %s", sql)
	}
}

func TestFiltroEscopoUsaOsParametrosPedidos(t *testing.T) {
	f := filtroEscopo(2, 3)
	if !strings.HasPrefix(f, "($2::bool OR ") || !strings.HasSuffix(f, "= ANY($3::text[]))") {
		t.Fatalf("filtro inesperado: %s", f)
	}
}
