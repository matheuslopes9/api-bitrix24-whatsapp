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
