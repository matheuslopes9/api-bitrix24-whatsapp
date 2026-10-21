package dependencias

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

var hoje = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

func data(s string) *time.Time {
	t, _ := time.Parse("2006-01-02", s)
	return &t
}

// O caso real de 21/10/2026: Alpine 3.19 sem suporte desde 01/11/2025.
func TestCicloForaDeSuporteECritico(t *testing.T) {
	ciclos := []Ciclo{
		{Ciclo: "3.24", Ultima: "3.24.1", FimSuporte: data("2028-05-01")},
		{Ciclo: "3.19", Ultima: "3.19.9", FimSuporte: data("2025-11-01")},
	}
	it := avaliarCiclo(Item{EmUso: "3.19.9"}, ciclos, "3.19", hoje)
	if it.Status != Critico || it.MaisNova != "3.24.1" || !strings.Contains(it.Detalhe, "01/11/2025") {
		t.Fatalf("%+v", it)
	}
}

func TestCicloPertoDoFimPedeAtualizar(t *testing.T) {
	ciclos := []Ciclo{{Ciclo: "16", Ultima: "16.15", FimSuporte: data("2026-12-01")}}
	if it := avaliarCiclo(Item{EmUso: "16.15"}, ciclos, "16", hoje); it.Status != Atualizar {
		t.Fatalf("%+v", it)
	}
}

// Versao principal mais nova, sozinha, nao e' urgencia: so' informa.
func TestCicloComSuporteEmDia(t *testing.T) {
	ciclos := []Ciclo{
		{Ciclo: "18", Ultima: "18.2", FimSuporte: data("2030-11-01")},
		{Ciclo: "16", Ultima: "16.15", FimSuporte: data("2028-11-09")},
	}
	it := avaliarCiclo(Item{EmUso: "16.15"}, ciclos, "16", hoje)
	if it.Status != EmDia || !strings.Contains(it.Detalhe, "sem urgência") {
		t.Fatalf("%+v", it)
	}
}

// Correcao nova no mesmo ciclo (16.4 -> 16.15) pede atualizar; versao sem
// patch (Python "3.12" declarado) nao.
func TestCicloComCorrecaoNova(t *testing.T) {
	ciclos := []Ciclo{{Ciclo: "1.26", Ultima: "1.26.10", FimSuporte: nil}}
	if it := avaliarCiclo(Item{EmUso: "1.26.3"}, ciclos, "1.26", hoje); it.Status != Atualizar {
		t.Fatalf("1.26.3: %+v", it)
	}
	ciclos = []Ciclo{{Ciclo: "3.12", Ultima: "3.12.15", FimSuporte: data("2028-10-31")}}
	if it := avaliarCiclo(Item{EmUso: "3.12"}, ciclos, "3.12", hoje); it.Status != EmDia {
		t.Fatalf("3.12: %+v", it)
	}
}

// O caso real: o codigo em v20.0 em 21/10/2026, vencida em 24/09/2026.
func TestMetaVencidaECritica(t *testing.T) {
	if it := avaliarMeta("v20.0", hoje); it.Status != Critico {
		t.Fatalf("v20: %+v", it)
	}
	if it := avaliarMeta("v25.0", hoje); it.Status != EmDia {
		t.Fatalf("v25: %+v", it)
	}
	if it := avaliarMeta("v21.0", hoje); it.Status != Atualizar { // vence 21/01/2027
		t.Fatalf("v21: %+v", it)
	}
	if it := avaliarMeta("v99.0", hoje); it.Status != Desconhecido {
		t.Fatalf("v99: %+v", it)
	}
}

func TestTabelaMetaEmOrdem(t *testing.T) {
	for i := 1; i < len(versoesMeta); i++ {
		if versoesMeta[i-1].lancamento <= versoesMeta[i].lancamento {
			t.Fatalf("%s deve vir antes de %s (mais nova no topo)", versoesMeta[i].versao, versoesMeta[i-1].versao)
		}
	}
}

func TestDataDaPseudoVersao(t *testing.T) {
	d, ok := dataDaPseudoVersao("v0.0.0-20260928140511-35f522c88ce3")
	if !ok || d.Format("2006-01-02 15:04") != "2026-09-28 14:05" {
		t.Fatalf("%v %v", d, ok)
	}
	if _, ok := dataDaPseudoVersao("v1.6.0"); ok {
		t.Fatal("versao normal nao e' pseudo-versao")
	}
}

func TestEscaparModulo(t *testing.T) {
	if got := escaparModulo("github.com/HugoSmits86/nativewebp"); got != "github.com/!hugo!smits86/nativewebp" {
		t.Fatal(got)
	}
}

// whatsmeow com mais de 60 dias de atraso vira critico: versao velha leva 405
// e derruba todos os numeros. Atraso pequeno so' pede atualizar.
func TestWhatsmeowAtrasadoECritico(t *testing.T) {
	v := &Verificador{agora: func() time.Time { return hoje }}
	m := modulosGo[0]
	if m.caminho != "go.mau.fi/whatsmeow" {
		t.Fatal("o whatsmeow deixou de ser o primeiro do catalogo")
	}
	v.fontes.ultimaVersaoGo = func(context.Context, string) (string, time.Time, error) {
		return "v0.0.0-20261017175727-b2b1e71d45e2", time.Date(2026, 10, 17, 17, 57, 0, 0, time.UTC), nil
	}
	if it := v.itemGo(context.Background(), m, "v0.0.0-20260928140511-35f522c88ce3"); it.Status != Atualizar {
		t.Fatalf("19 dias de atraso: %+v", it)
	}
	if it := v.itemGo(context.Background(), m, "v0.0.0-20260601000000-aaaaaaaaaaaa"); it.Status != Critico {
		t.Fatalf("4 meses de atraso: %+v", it)
	}
	if it := v.itemGo(context.Background(), m, "v0.0.0-20261017175727-b2b1e71d45e2"); it.Status != EmDia {
		t.Fatalf("em dia: %+v", it)
	}
}

// O proxy de e-mail e' outro container; as versoes dele ficam declaradas no
// codigo. Mudou o Dockerfile ou o requirements.txt e esqueceu aqui: quebra.
func TestVersoesDoProxyBatemComOsArquivos(t *testing.T) {
	docker, err := os.ReadFile("../../tools/oauth2-email-service/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	if m := regexp.MustCompile(`FROM python:([0-9.]+)`).FindSubmatch(docker); m == nil || string(m[1]) != versaoPythonProxy {
		t.Fatalf("Dockerfile do proxy usa python %s, declarado %s", m, versaoPythonProxy)
	}
	req, err := os.ReadFile("../../tools/oauth2-email-service/requirements.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pacotesPythonProxy {
		if !strings.Contains(string(req), p.nome+"=="+p.versao) {
			t.Errorf("requirements.txt nao tem %s==%s", p.nome, p.versao)
		}
	}
}

// Todo modulo do catalogo tem que estar no go.mod: catalogo com dependencia
// que saiu do projeto mostraria "desconhecido" pra sempre.
func TestCatalogoBateComGoMod(t *testing.T) {
	gomod, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range modulosGo {
		if !strings.Contains(string(gomod), m.caminho+" ") {
			t.Errorf("%s esta' no catalogo mas nao no go.mod", m.caminho)
		}
	}
}
