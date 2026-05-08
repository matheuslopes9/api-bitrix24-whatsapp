package whatsapp

// Limites exclusivos do caminho QR Code (whatsmeow / WhatsApp Multi-Device).
// Mantido em arquivo separado dos limites do Cloud API (cloud.go) para que
// os dois caminhos evoluam de forma independente.

import "errors"

// MaxQRMediaBytes — limite oficial do WhatsApp Multi-Device para envio de
// documento via protocolo MD: 2 GB.
// Ref: https://blog.whatsapp.com/reactions-2gb-file-sharing-512-groups
const MaxQRMediaBytes = 2 * 1024 * 1024 * 1024

// ErrQRMediaTooLarge é retornado quando uma mídia (inbound ou outbound) no
// caminho QR excede MaxQRMediaBytes. O caller decide a UX (avisar cliente,
// marcar falha pro operador etc).
var ErrQRMediaTooLarge = errors.New("qr media exceeds 2GB")
