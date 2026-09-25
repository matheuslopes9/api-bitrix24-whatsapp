// sms_legado.go — limpeza do modulo de Campanhas SMS, removido em 25/09/2026.
//
// O app se registrava como provedor em Bitrix > Marketing > Campanhas SMS e
// roteava as campanhas pro WhatsApp nao oficial. Saiu do produto: disparo
// em massa por ali e' o caminho mais curto pro banimento do numero.
//
// Tirar o codigo nao tira o provedor dos portais onde ele ja' foi
// registrado: o Bitrix continuaria oferecendo "UC Talk WhatsApp" e mandando
// cada envio pra uma rota que nao existe mais. Por isso, quando o portal
// abre o app, o provedor antigo e' descadastrado (uma vez por processo).
package api

import (
	"context"
	"sync"

	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"go.uber.org/zap"
)

// codigoProvedorSMSLegado e' o CODE usado no messageservice.sender.add.
const codigoProvedorSMSLegado = "uctalk_whatsapp"

var provedorSMSRemovido sync.Map // dominio -> struct{}

// removerProvedorSMSLegado descadastra o provedor no portal. Best-effort:
// portal onde ele nunca existiu devolve erro, e isso e' o caso normal.
func (h *handlers) removerProvedorSMSLegado(ctx context.Context, portal *db.BitrixPortal) {
	if portal == nil || portal.Domain == "" {
		return
	}
	if _, feito := provedorSMSRemovido.LoadOrStore(portal.Domain, struct{}{}); feito {
		return
	}
	if err := h.bitrixClient.DeleteSMSSender(ctx, h.portalToCreds(portal), codigoProvedorSMSLegado); err != nil {
		h.log.Debug("sms legado: provedor nao removido (provavelmente nunca existiu)",
			zap.String("domain", portal.Domain), zap.Error(err))
		return
	}
	h.log.Info("sms legado: provedor UC Talk removido das Campanhas SMS do portal",
		zap.String("domain", portal.Domain))
}
