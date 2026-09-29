package main

// voto_enquete.go — diz em QUE o cliente votou, nao so' que votou.
//
// O voto do WhatsApp chega cifrado com o segredo da enquete original. Aberto,
// ele ainda NAO traz o texto da opcao: so' o SHA-256 dela. Para nomear, e'
// preciso ter as opcoes originais e refazer o hash — por isso a tabela
// enquetes (db/enquetes.go) guarda o que chegou na criacao.
//
// Cada degrau que falha degrada em vez de sumir: sem descriptografia, sem
// enquete guardada ou sem casar o hash, o atendente ainda ve que houve voto.
// Perder o voto por nao conseguir nomear a opcao seria trocar informacao
// parcial por nenhuma.

import (
	"context"
	"crypto/sha256"
	"strings"

	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/whatsapp"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types/events"
	"go.uber.org/zap"
)

func textoDoVoto(
	ctx context.Context,
	waManager *whatsapp.Manager,
	repo *db.Repository,
	log *zap.Logger,
	sessionJID string,
	evt *events.Message,
	voto *waE2E.PollUpdateMessage,
) string {
	const generico = "[Voto em enquete]"

	aberto, err := waManager.AbrirVoto(ctx, sessionJID, evt)
	if err != nil || aberto == nil {
		log.Info("voto de enquete: nao consegui abrir — registrando generico",
			zap.String("msg_id", evt.Info.ID), zap.Error(err))
		return generico
	}

	// A qual enquete este voto pertence. Sem a referencia nao ha' o que casar.
	idEnquete := voto.GetPollCreationMessageKey().GetID()
	if idEnquete == "" {
		return generico
	}
	pergunta, opcoes, err := repo.OpcoesDaEnquete(ctx, idEnquete)
	if err != nil || len(opcoes) == 0 {
		// Enquete criada antes desta tabela existir, ou ja' limpa. Nao e' erro.
		log.Info("voto de enquete: opcoes nao encontradas — registrando generico",
			zap.String("enquete_id", idEnquete))
		return generico
	}

	// O hash e' SHA-256 do texto da opcao (whatsmeow: HashPollOptions).
	porHash := make(map[[32]byte]string, len(opcoes))
	for _, o := range opcoes {
		porHash[sha256.Sum256([]byte(o))] = o
	}

	var escolhidas []string
	for _, h := range aberto.GetSelectedOptions() {
		if len(h) != 32 {
			continue
		}
		var chave [32]byte
		copy(chave[:], h)
		if nome, ok := porHash[chave]; ok {
			escolhidas = append(escolhidas, nome)
		}
	}

	// Voto ANULADO e' informacao, nao ausencia: o cliente desmarcou tudo, e o
	// atendente precisa saber disso tanto quanto saberia de uma escolha.
	if len(escolhidas) == 0 {
		if len(aberto.GetSelectedOptions()) == 0 {
			return rotuloVoto(pergunta, "removeu o voto")
		}
		return generico
	}
	return rotuloVoto(pergunta, "votou em "+strings.Join(escolhidas, ", "))
}

// rotuloVoto monta a linha que o atendente le. A pergunta entra quando existe:
// numa conversa com mais de uma enquete, "votou em Op01" sozinho nao diz de
// qual enquete se trata.
func rotuloVoto(pergunta, acao string) string {
	if p := strings.TrimSpace(pergunta); p != "" {
		return "[Enquete: " + p + "] " + acao
	}
	return "[Enquete] " + acao
}
