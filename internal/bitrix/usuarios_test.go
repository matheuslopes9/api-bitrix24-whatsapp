package bitrix

import "testing"

// A regra de quem aparece no painel de permissoes e' compartilhada pelos dois
// caminhos (user.get e sondagem de IDs). Se ela divergir, um caminho mostra
// gente que o outro esconde — e o suporte ve uma lista diferente conforme o
// scope concedido no portal.
func TestFiltrarInternosAtivos(t *testing.T) {
	entrada := []BitrixUser{
		{ID: "10", Name: "Zeca", Active: true},
		{ID: "2", Name: "Ana", Active: true},
		{ID: "3", Name: "Bot", Active: true, Bot: true},          // bot: fora
		{ID: "4", Name: "Externo", Active: true, Extranet: true}, // extranet: fora
		{ID: "5", Name: "Demitido", Active: false},               // inativo: fora
		{ID: "2", Name: "Ana dup", Active: true},                 // duplicado: fora
		{ID: "", Name: "Sem id", Active: true},                   // sem id: fora
	}
	got := filtrarInternosAtivos(entrada)
	if len(got) != 2 {
		t.Fatalf("deveria sobrar 2 usuarios, sobraram %d: %+v", len(got), got)
	}
	// Ordenado por nome: Ana antes de Zeca.
	if got[0].Name != "Ana" || got[1].Name != "Zeca" {
		t.Errorf("ordem errada: %s, %s", got[0].Name, got[1].Name)
	}
	// O duplicado que sobrou deve ser o PRIMEIRO visto, nao o ultimo.
	if got[0].ID != "2" {
		t.Errorf("dedup deveria manter o primeiro id 2, veio %q", got[0].ID)
	}
}

func TestFiltrarInternosAtivosVazio(t *testing.T) {
	if got := filtrarInternosAtivos(nil); len(got) != 0 {
		t.Errorf("entrada vazia deve devolver vazio, veio %+v", got)
	}
}

// A parada da sondagem por EVIDENCIA e' a parte facil de errar: parar cedo
// demais perde usuario (o bug original), e nunca parar trava a tela. Este
// teste exercita a regra isolada da chamada HTTP.
func TestParadaPorOndasVazias(t *testing.T) {
	const desistirApos = 3

	// Simula ondas: cada valor e' quantos usuarios aquela onda achou.
	// O buraco de 2 ondas no meio representa um portal com muita exclusao —
	// a varredura NAO pode parar ali.
	casos := []struct {
		nome           string
		ondas          []int
		ondasEsperadas int
	}{
		{"acha sempre", []int{5, 5, 5, 0, 0, 0}, 6},
		{"buraco no meio nao para", []int{5, 0, 0, 7, 0, 0, 0}, 7},
		{"vazio desde o inicio", []int{0, 0, 0, 9, 9}, 3},
	}

	for _, c := range casos {
		vazias, processadas := 0, 0
		for _, achados := range c.ondas {
			processadas++
			if achados == 0 {
				vazias++
				if vazias >= desistirApos {
					break
				}
			} else {
				vazias = 0
			}
		}
		if processadas != c.ondasEsperadas {
			t.Errorf("%s: processou %d ondas, esperava %d", c.nome, processadas, c.ondasEsperadas)
		}
	}
}

func boolPtr(b bool) *bool { return &b }

// Quem pode operar atendimento. Errar aqui tem dois custos opostos: deixar
// passar externo da' acesso indevido ao WhatsApp do cliente; excluir demais
// esvazia a tela de permissoes e ninguem consegue enviar.
func TestEhInternoAtivo(t *testing.T) {
	casos := []struct {
		nome   string
		u      BitrixUser
		aceita bool
	}{
		{"interno ativo", BitrixUser{Active: true, Intranet: boolPtr(true)}, true},
		{"sem o campo intranet passa", BitrixUser{Active: true}, true},
		{"inativo", BitrixUser{Active: false, Intranet: boolPtr(true)}, false},
		{"bot", BitrixUser{Active: true, Bot: true}, false},
		{"extranet", BitrixUser{Active: true, Extranet: true}, false},
		{"rede bitrix24", BitrixUser{Active: true, Network: true}, false},
		{"criado por conector", BitrixUser{Active: true, Connector: true}, false},
		{"intranet explicitamente false", BitrixUser{Active: true, Intranet: boolPtr(false)}, false},
	}
	for _, c := range casos {
		if got := ehInternoAtivo(c.u); got != c.aceita {
			t.Errorf("%s: ehInternoAtivo = %v, queria %v", c.nome, got, c.aceita)
		}
	}
}

// Caso real: a Gabrielly tem external_auth_id "socservices" (entrou por
// login social) mas intranet_user true. E' do quadro e PRECISA aparecer —
// filtrar por origem de login a excluiria por engano.
func TestLoginSocialNaoExcluiInterno(t *testing.T) {
	g := BitrixUser{ID: "12195", Name: "Gabrielly", Active: true, Intranet: boolPtr(true)}
	if !ehInternoAtivo(g) {
		t.Error("usuario interno com login social deve aparecer")
	}
}

// Contato do WhatsApp que o Bitrix registra como usuario de chat. Medido no
// portal do cliente: sao 473 deles contra 12 funcionarios. Se o filtro
// deixar passar, a tela de permissoes vira uma lista de clientes.
func TestContatoDeWhatsAppNaoEntra(t *testing.T) {
	// Flags reais de "Curem Compras" (id 71) e "Amor" (id 165).
	contato := BitrixUser{
		ID: "71", Name: "Curem Compras",
		Active: true, Extranet: true, Connector: true, Intranet: boolPtr(false),
	}
	if ehInternoAtivo(contato) {
		t.Error("contato do WhatsApp nao pode aparecer nas permissoes")
	}
}

// Funcionario desativado (demitido) nao deve aparecer: nao faz sentido dar
// permissao de envio a quem saiu. Flags reais dos ids 1, 5, 19, 25, 29.
func TestFuncionarioDesativadoNaoEntra(t *testing.T) {
	demitido := BitrixUser{
		ID: "1", Name: "Rebeca Santos",
		Active: false, Extranet: false, Connector: false, Intranet: boolPtr(true),
	}
	if ehInternoAtivo(demitido) {
		t.Error("funcionario desativado nao pode aparecer")
	}
}

// Funcionario de verdade: flags reais do Valdeir Assis (id 7).
func TestFuncionarioAtivoEntra(t *testing.T) {
	func7 := BitrixUser{
		ID: "7", Name: "Valdeir Assis", Position: "Gerente Financeiro",
		Active: true, Extranet: false, Connector: false, Intranet: boolPtr(true),
	}
	if !ehInternoAtivo(func7) {
		t.Error("funcionario interno ativo precisa aparecer")
	}
}
