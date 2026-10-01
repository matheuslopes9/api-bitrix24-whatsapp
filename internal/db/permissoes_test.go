package db

import (
	"os"
	"strings"
	"testing"
)

// A permissao por numero negava por padrao, e o resultado era o contrario do
// pretendido: conectar um numero novo deixava o time inteiro sem conseguir
// responder pela aba do CRM. A mensagem mandava "pedir para um admin liberar",
// e o unico usuario que podia liberar — o master — tinha como unica acao
// disponivel TRANSFERIR o master pra outra pessoa.
//
// Estes testes leem o SQL. Nao substituem o teste contra Postgres, mas pegam a
// classe de erro que mais assusta aqui: inverter a condicao e abrir (ou fechar)
// tudo de uma vez, para todos os clientes, sem ninguem notar.

func TestRegraDePermissaoLiberaQuandoNumeroNaoEstaRestrito(t *testing.T) {
	q := strings.Join(strings.Fields(sqlPermissaoDeEnvio), " ")

	// "NOT EXISTS ... crm_numero_restrito" e' o padrao aberto. Se virar
	// "EXISTS", todo numero sem restricao passa a bloquear — o bug antigo de
	// volta, e silencioso.
	if !strings.Contains(q, "NOT EXISTS ( SELECT 1 FROM crm_numero_restrito") {
		t.Error("a regra deixou de liberar numero nao-restrito — padrao voltou a ser negar")
	}
	// O OR e' o que torna a restricao uma EXCECAO. Com AND, exigiria as duas
	// coisas e ninguem enviaria por numero aberto.
	if !strings.Contains(q, ") OR EXISTS (") {
		t.Error("as duas condicoes nao estao em OR — restricao deixaria de ser excecao")
	}
	// Wildcard do master: sem isto, quem tinha acesso a todos os numeros
	// perderia o acesso na virada.
	if !strings.Contains(q, "session_jid = $3 OR session_jid = ''") {
		t.Error("o wildcard do master sumiu da regra")
	}
}

// Numero restrito so' deixa passar quem tem linha. Se a subconsulta de
// permissao parar de filtrar por user_id, QUALQUER pessoa do portal passaria
// num numero restrito — o controle existiria na tela e nao no servidor.
func TestRegraDePermissaoFiltraPorUsuarioNoNumeroRestrito(t *testing.T) {
	q := strings.Join(strings.Fields(sqlPermissaoDeEnvio), " ")
	if !strings.Contains(q, "FROM crm_user_permissions WHERE domain = $1 AND user_id = $2") {
		t.Error("a checagem do numero restrito nao filtra por dominio e usuario")
	}
}

// Toda consulta aqui precisa escopar por dominio. Uma que esqueca o filtro
// vazaria permissao entre clientes — a falha mais grave que este sistema pode
// ter, e a mais facil de introduzir editando SQL depressa.
func TestConsultasDePermissaoEscopamPorDominio(t *testing.T) {
	if !strings.Contains(sqlPermissaoDeEnvio, "domain = $1") {
		t.Fatal("IsSessionAllowed sem filtro de dominio")
	}
	if strings.Count(sqlPermissaoDeEnvio, "domain = $1") != 2 {
		t.Error("as duas subconsultas precisam filtrar por dominio, nao so' uma")
	}
}

// DUAS funcoes respondem "esse usuario pode usar esse numero?":
//
//	IsSessionAllowed       -> o servidor decide se aceita o envio
//	ListUserAllowedSessions -> a aba do CRM monta o seletor de numero
//
// Em 01/10 elas divergiram. Mudei a regra para "liberado por padrao" so' na
// primeira, e a segunda continuou exigindo linha em crm_user_permissions. O
// efeito foi o pior possivel: a aba mostrava "Sem permissao" e escondia TODOS
// os numeros, enquanto o servidor teria aceitado o envio. A pessoa nem tentava,
// e a tela de Permissoes mostrava o numero verde ao lado do nome dela.
//
// A divergencia e' invisivel em teste de unidade de cada uma. Este teste olha
// as duas juntas, que e' o unico lugar onde ela aparece.
func TestAsDuasDecisoesDePermissaoOlhamARestricao(t *testing.T) {
	fonte := leFonte(t, "permissoes.go") + leFonte(t, "repository.go")

	// A consulta do envio.
	if !strings.Contains(sqlPermissaoDeEnvio, "crm_numero_restrito") {
		t.Error("IsSessionAllowed nao consulta a restricao do numero")
	}

	// A lista que a aba usa. Se ela parar de chamar NumerosRestritos, volta a
	// exigir permissao explicita num numero aberto — e a aba volta a esconder
	// numeros que o servidor aceitaria.
	ini := strings.Index(fonte, "func (r *Repository) ListUserAllowedSessions")
	if ini < 0 {
		t.Fatal("ListUserAllowedSessions sumiu — este teste precisa ser reapontado")
	}
	corpo := fonte[ini:]
	if fim := strings.Index(corpo, "\nfunc "); fim > 0 {
		corpo = corpo[:fim]
	}
	if !strings.Contains(corpo, "NumerosRestritos") {
		t.Error("ListUserAllowedSessions nao olha a restricao — a aba do CRM vai " +
			"esconder numeros que o envio aceita")
	}
}

func leFonte(t *testing.T, nome string) string {
	t.Helper()
	b, err := os.ReadFile(nome)
	if err != nil {
		t.Fatalf("nao consegui ler %s: %v", nome, err)
	}
	return string(b)
}
