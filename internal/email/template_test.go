package email

import (
	"strings"
	"testing"
)

func alertaDeTeste() Alerta {
	return Alerta{
		Categoria: CatTokenVencido,
		Titulo:    "Token do Bitrix24 vencido — teclife.bitrix24.com.br",
		CorpoHTML: "<p>A renovação não está passando.</p>",
		Contexto: []LinhaContexto{
			{Rotulo: "Cliente", Valor: "teclife.bitrix24.com.br"},
			{Rotulo: "Venceu em", Valor: "23/09/2026 21:02"},
		},
		Acao:    "Abra Saúde do cliente e confira o bloco Token.",
		BaseURL: "https://uctalk-homolog.uctechnology.com.br",
	}
}

// As escolhas de marcacao abaixo sao o que faz o e-mail chegar igual no
// Outlook, no Gmail e no celular. Trocar por layout moderno quebra no Outlook,
// que renderiza com o motor do Word.
func TestTemplateSegueAsRegrasDeCompatibilidade(t *testing.T) {
	h := Renderizar(alertaDeTeste())

	if strings.Contains(h, "display:flex") || strings.Contains(h, "display:grid") {
		t.Error("layout moderno quebra no Outlook — o template usa <table>")
	}
	if !strings.Contains(h, `width="620"`) {
		t.Error("faltou a largura fixa de 620px")
	}
	if !strings.Contains(h, "max-width:620px;width:100%") {
		t.Error("sem max-width o e-mail estoura a tela do celular")
	}
	// Cliente que forca tema claro repinta o que estiver sem cor explicita, e
	// o texto claro sumiria.
	if n := strings.Count(h, "background-color:#2d2d2d"); n < 2 {
		t.Errorf("o fundo escuro precisa estar declarado em cada celula, achei %d", n)
	}
}

// Categoria errada faz o alerta de WhatsApp chegar carimbado como
// "Licenciamento" — era o problema do template anterior do time.
func TestCategoriaDefineSetorESelo(t *testing.T) {
	casos := map[string][2]string{
		CatTokenVencido: {"Integração Bitrix24", "Token de Acesso Vencido"},
		CatSessaoCaiu:   {"Conexão WhatsApp", "Número Desconectado"},
		CatLicenca:      {"Licenciamento", "Vencimento de Licença"},
		CatTeste:        {"Alertas", "Teste de Envio"},
	}
	for cat, quer := range casos {
		a := alertaDeTeste()
		a.Categoria = cat
		h := Renderizar(a)
		if !strings.Contains(h, quer[0]) {
			t.Errorf("%s: faltou o setor %q", cat, quer[0])
		}
		if !strings.Contains(h, quer[1]) {
			t.Errorf("%s: faltou o selo %q", cat, quer[1])
		}
	}
}

// Categoria desconhecida nao pode quebrar o envio: alerta que nao sai por
// causa de rotulo e' pior que alerta com rotulo generico.
func TestCategoriaDesconhecidaCaiEmPadraoSeguro(t *testing.T) {
	a := alertaDeTeste()
	a.Categoria = "inventada_que_nao_existe"
	h := Renderizar(a)
	if !strings.Contains(h, "Alerta do Sistema") {
		t.Error("categoria desconhecida deveria cair no selo generico")
	}
	if !strings.Contains(h, a.Titulo) {
		t.Error("o titulo tem que aparecer mesmo com categoria desconhecida")
	}
}

// O botao tem que levar ao painel do UC Talk, nao ao de ferramentas.
func TestBotaoApontaProPainelDoUCTalk(t *testing.T) {
	h := Renderizar(alertaDeTeste())
	if !strings.Contains(h, "https://uctalk-homolog.uctechnology.com.br/admin") {
		t.Error("o botao deveria levar ao painel do UC Talk")
	}
	// Se as imagens ou o botao forem bloqueados, ainda tem que dar pra copiar.
	if !strings.Contains(h, "ou copie o link:") {
		t.Error("faltou o link em texto embaixo do botao")
	}
}

// Tabela vazia com bordas so' ocuparia espaco e daria impressao de dado
// faltando.
func TestTabelaSomeQuandoNaoHaDados(t *testing.T) {
	a := alertaDeTeste()
	a.Contexto = nil
	if h := Renderizar(a); strings.Contains(h, "border:1px solid #3a3a3a") {
		t.Error("sem contexto a tabela de dados deveria sumir inteira")
	}
}

func TestContextoApareceNaOrdem(t *testing.T) {
	h := Renderizar(alertaDeTeste())
	i, j := strings.Index(h, "Cliente"), strings.Index(h, "Venceu em")
	if i < 0 || j < 0 {
		t.Fatal("faltou linha do contexto")
	}
	if i > j {
		t.Error("a ordem do contexto tem que ser respeitada — cliente antes do detalhe tecnico")
	}
}

// Valor vindo do Bitrix pode conter marcacao; escapar evita quebrar o layout.
func TestValorDoContextoEhEscapado(t *testing.T) {
	a := alertaDeTeste()
	a.Contexto = []LinhaContexto{{Rotulo: "Cliente", Valor: `<script>alert(1)</script>`}}
	if h := Renderizar(a); strings.Contains(h, "<script>alert(1)</script>") {
		t.Error("valor do contexto tem que ser escapado")
	}
}

// O logo e o rodape sao o que identificam a origem de relance.
func TestIdentidadeDaEmpresa(t *testing.T) {
	h := Renderizar(alertaDeTeste())
	for _, esperado := range []string{
		logoURL,
		"UC Technology &mdash; Central de Monitoramento e Ferramentas",
		"Este é um e-mail automático",
	} {
		if !strings.Contains(h, esperado) {
			t.Errorf("faltou %q", esperado)
		}
	}
}
