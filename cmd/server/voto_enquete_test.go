package main

import (
	"crypto/sha256"
	"strings"
	"testing"
)

// O voto do WhatsApp so' traz o SHA-256 da opcao, nunca o texto. Este teste
// fixa a regra do casamento: se o hash deixar de bater, o atendente volta a ver
// "[Voto em enquete]" sem saber em que o cliente votou.
func TestHashDaOpcaoCasaComOTexto(t *testing.T) {
	opcoes := []string{"Op01", "Op02", "Opção com acento"}
	porHash := map[[32]byte]string{}
	for _, o := range opcoes {
		porHash[sha256.Sum256([]byte(o))] = o
	}
	for _, o := range opcoes {
		h := sha256.Sum256([]byte(o))
		if porHash[h] != o {
			t.Errorf("hash de %q nao casou", o)
		}
	}
	// Opcao que nao esta' na enquete nao pode casar por acidente.
	if _, existe := porHash[sha256.Sum256([]byte("Op99"))]; existe {
		t.Error("opcao inexistente casou")
	}
}

// A pergunta entra no rotulo quando existe: numa conversa com mais de uma
// enquete, "votou em Op01" sozinho nao diz de qual enquete se trata.
func TestRotuloDoVotoIdentificaAEnquete(t *testing.T) {
	com := rotuloVoto("Qual cor?", "votou em Azul")
	if !strings.Contains(com, "Qual cor?") || !strings.Contains(com, "Azul") {
		t.Errorf("rotulo perdeu informacao: %q", com)
	}
	sem := rotuloVoto("", "votou em Azul")
	if !strings.Contains(sem, "Azul") {
		t.Errorf("sem pergunta, ainda tem que dizer a opcao: %q", sem)
	}
	if strings.Contains(sem, "[Enquete: ]") {
		t.Errorf("pergunta vazia vazou pro rotulo: %q", sem)
	}
}

// Voto anulado e' INFORMACAO, nao ausencia: o cliente desmarcou tudo e o
// atendente precisa saber disso tanto quanto saberia de uma escolha.
func TestVotoRemovidoEhDito(t *testing.T) {
	r := rotuloVoto("Qual cor?", "removeu o voto")
	if !strings.Contains(r, "removeu") {
		t.Errorf("remocao de voto nao aparece: %q", r)
	}
}
