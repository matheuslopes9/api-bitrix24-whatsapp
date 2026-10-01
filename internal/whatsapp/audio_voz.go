package whatsapp

// audio_voz.go — audio do operador chegando como MENSAGEM DE VOZ.
//
// Audio enviado pelo Contact Center chegava como arquivo anexado: um
// retangulo com clipe de papel e nome de arquivo. Mensagem de voz e' outra
// coisa — bolha com onda sonora, avatar redondo e duracao. Quem recebe
// distingue na hora, e o conector existe pra que nao de' pra distinguir.
//
// Sao dois detalhes, nao um:
//
//  1. PTT=true, que diz ao WhatsApp "isto e' voz, nao anexo";
//  2. a DURACAO. O codigo mandava Seconds=0 e a bolha aparecia como "0:00" —
//     tecnicamente mensagem de voz, visivelmente quebrada.
//
// A duracao sai do proprio arquivo, sem decodificar o audio: em Ogg Opus o
// granule da ultima pagina ja' e' a contagem de amostras a 48 kHz.

import (
	"encoding/binary"
	"strings"
)

// taxaOpus: Opus sempre numera amostras a 48 kHz, qualquer que seja a taxa
// original do audio. Por isso a conta nao depende do arquivo.
const taxaOpus = 48000

// EhMensagemDeVoz diz se este audio deve chegar como voz, nao como anexo.
//
// So' Ogg/Opus. E' o formato que o proprio WhatsApp usa para gravar voz, e o
// unico que todo cliente reproduz dentro da bolha. Mandar um MP3 marcado como
// voz produz uma bolha que parte dos aparelhos nao toca — pior que o anexo,
// porque falha depois de parecer que ia funcionar.
func EhMensagemDeVoz(mime string) bool {
	m := strings.ToLower(strings.TrimSpace(mime))
	return strings.HasPrefix(m, "audio/ogg") || strings.HasPrefix(m, "audio/opus")
}

// DuracaoOggOpus devolve a duracao em segundos lendo o container.
//
// Nao decodifica o audio: percorre as paginas Ogg e usa o granule da ultima,
// que em Opus e' a quantidade de amostras a 48 kHz. Desconta o pre-skip
// declarado no OpusHead, que sao amostras de aquecimento do codec e nao
// tocam — sem descontar, audios curtos ficam alguns centesimos mais longos.
//
// Devolve ok=false para qualquer coisa que nao entenda. Quem chama manda 0,
// que e' o que ja' fazia: duracao errada seria pior que ausente.
func DuracaoOggOpus(dados []byte) (uint32, bool) {
	const cabecalhoPagina = 27 // "OggS" + campos fixos, antes da tabela de segmentos

	var ultimoGranule uint64
	var preSkip uint16
	var achouCabecalho, achouPagina bool

	for i := 0; i+cabecalhoPagina <= len(dados); {
		if string(dados[i:i+4]) != "OggS" {
			// Nao e' Ogg, ou o fluxo esta' corrompido a partir daqui. Para em
			// vez de varrer o arquivo inteiro procurando assinatura.
			break
		}
		granule := binary.LittleEndian.Uint64(dados[i+6 : i+14])
		nSegmentos := int(dados[i+26])
		inicioTabela := i + cabecalhoPagina
		if inicioTabela+nSegmentos > len(dados) {
			break // tabela truncada
		}
		tamanhoDados := 0
		for _, s := range dados[inicioTabela : inicioTabela+nSegmentos] {
			tamanhoDados += int(s)
		}
		inicioCorpo := inicioTabela + nSegmentos
		fimCorpo := inicioCorpo + tamanhoDados
		if fimCorpo > len(dados) {
			break // corpo truncado
		}

		// O OpusHead e' o primeiro pacote do fluxo e traz o pre-skip.
		if !achouCabecalho && tamanhoDados >= 12 &&
			string(dados[inicioCorpo:inicioCorpo+8]) == "OpusHead" {
			preSkip = binary.LittleEndian.Uint16(dados[inicioCorpo+10 : inicioCorpo+12])
			achouCabecalho = true
		}

		// Granule -1 marca pagina sem amostras completas; nao serve de total.
		if granule != ^uint64(0) {
			ultimoGranule = granule
			achouPagina = true
		}
		i = fimCorpo
	}

	if !achouPagina || ultimoGranule <= uint64(preSkip) {
		return 0, false
	}
	segundos := (ultimoGranule - uint64(preSkip)) / taxaOpus
	if segundos == 0 {
		// Audio com menos de 1 segundo existe (um "oi", um riso). Mostrar 0:00
		// faria parecer vazio, entao arredonda pra 1.
		segundos = 1
	}
	return uint32(segundos), true
}
