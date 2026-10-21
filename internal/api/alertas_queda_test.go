package api

import (
	"strings"
	"testing"
	"time"
)

type pausaFake struct {
	motivo  string
	recebeu string
}

func (p *pausaFake) ReconexaoPausada(jid string) (bool, string, time.Duration) {
	p.recebeu = jid
	if p.motivo == "" {
		return false, "", 0
	}
	return true, p.motivo, 10 * time.Minute
}

// Cada motivo pede uma acao diferente; o e-mail tem que dizer qual.
func TestTextoDaQuedaPorMotivo(t *testing.T) {
	casos := []struct {
		motivo, noAssunto, naAcao string
	}{
		{"whatsmeow desatualizado (405)", "desatualizada", "NÃO peça ao cliente para ler o QR"},
		{"restricao temporaria do WhatsApp: x", "restrito", "Aguarde o prazo"},
		{"sessao tomada por outra conexao (StreamReplaced)", "tomada", "dois containers"},
		{"", "desconectado", "pareie de novo"},
	}
	for _, c := range casos {
		wa := &pausaFake{motivo: c.motivo}
		_, _, acao, assunto := textoDaQueda("5519920187703", false, wa)
		if !strings.Contains(assunto, c.noAssunto) || !strings.Contains(acao, c.naAcao) {
			t.Errorf("motivo %q: assunto %q / acao %q", c.motivo, assunto, acao)
		}
		// A pausa e' guardada pelo numero extraido de um JID: consultar com o
		// numero puro nunca acharia o motivo.
		if !strings.HasSuffix(wa.recebeu, "@s.whatsapp.net") {
			t.Errorf("consultou a pausa com %q; precisa ser JID completo", wa.recebeu)
		}
	}
}

// Desvinculado tem prioridade: o socket esta' de pe, e a unica saida e' o QR.
func TestTextoDaQuedaDesvinculado(t *testing.T) {
	_, _, acao, assunto := textoDaQueda("5519920187703", true, &pausaFake{motivo: "whatsmeow desatualizado (405)"})
	if !strings.Contains(assunto, "DESVINCULADO") || !strings.Contains(acao, "QR Code") {
		t.Errorf("assunto %q / acao %q", assunto, acao)
	}
}
