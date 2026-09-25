package main

import (
	"strings"

	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
)

// tipoDaMidia classifica o arquivo enviado pelo mime.
//
// Antes todo arquivo de saida virava "document": a imagem enviada pelo
// operador aparecia como documento no historico, na aba do CRM e nos
// relatorios por tipo.
func tipoDaMidia(mimeType string) db.MessageType {
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return db.MsgTypeImage
	case strings.HasPrefix(mimeType, "video/"):
		return db.MsgTypeVideo
	case strings.HasPrefix(mimeType, "audio/"):
		return db.MsgTypeAudio
	}
	return db.MsgTypeDocument
}
