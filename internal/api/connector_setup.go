// connector_setup.go — a UNICA definicao de "qual conector e' desta sessao" e
// da ordem em que ele e' publicado no Bitrix.
//
// # OS DOIS BUGS QUE ISTO CORRIGE
//
//  1. A ORDEM ESTAVA INVERTIDA. Os quatro pontos que publicavam o conector
//     faziam register -> connector.data.set -> activate. A documentacao do
//     imconnector.activate diz o contrario:
//
//     "The connector settings specified by the imconnector.connector.data.set
//     method are deleted along with the status record. After enabling the
//     connector again, pass the settings once more."
//
//     Ou seja: activate APAGA os dados. Chamar data.set antes dele e' jogar a
//     configuracao fora. O imconnector.status devolve CONFIGURED=false ("true if
//     registered, connected, and active on this line simultaneously") e, como
//     STATUS so' e' true quando CONFIGURED e' true, a linha nao aceita mensagem.
//     Era exatamente o que a tela de saude mostrava: "registrado mas NAO ativado".
//
//  2. DOIS CAMINHOS GERAVAM connector_id DIFERENTE PRA MESMA COISA.
//     uiLinkQueue gravava "wa_qr_<telefone>"; partner link gravava o
//     portal.ConnectorID generico ("whatsapp_uc_v2") — e registrava esse no
//     Bitrix. Como a migration 014_qr_connector_per_session roda a cada boot e
//     reescreve todo connector_id que nao comeca com "wa_qr_"/"wa_cloud_", no
//     restart seguinte o banco passava a apontar pra um conector que NUNCA foi
//     registrado no portal. O envio continuava, mas a confirmacao de entrega
//     (imconnector.send.status.delivery) ia pro conector errado.
//
// Por isso a regra do ID e a sequencia de publicacao moram aqui, e nao
// espalhadas por handlers.go/partner.go.
package api

import (
	"context"
	"errors"
	"strings"

	"github.com/uctechnology/api-bitrix24-whatsapp/internal/bitrix"
	"go.uber.org/zap"
)

var (
	errSessaoCloudSemID = errors.New("sessão Cloud API sem phone_number_id — não dá pra derivar o connector_id")
	errJIDInvalido      = errors.New("session_jid inválido para gerar connector_id")
)

// connectorDaSessao devolve o connector_id e o nome de exibicao de uma sessao.
//
// Cada sessao WhatsApp tem o SEU conector no Bitrix. Compartilhar um so' faria
// as mensagens de todos os numeros do portal desaguarem no mesmo canal.
//
//	Cloud API: "wa_cloud_<phone_number_id>"
//	QR Code:   "wa_qr_<telefone>"   (ex: wa_qr_5519910001772)
//
// O telefone sai do JID sem o device suffix (":7") — o suffix muda a cada
// re-pareamento e criaria um conector novo, orfao, toda vez que o cliente
// reconectasse. E' a mesma normalizacao de numeroBase, em tenant_isolation.go.
//
// O formato aqui tem que bater com o da migration 014_qr_connector_per_session,
// que roda a cada boot: se divergir, o banco e o Bitrix ficam apontando pra
// conectores diferentes.
func (h *handlers) connectorDaSessao(ctx context.Context, sessionJID string) (id, nome string, err error) {
	if strings.HasPrefix(sessionJID, "cloud:") {
		sess, e := h.repo.GetSessionByJID(ctx, sessionJID)
		if e != nil || sess == nil || sess.CloudPhoneNumberID == "" {
			return "", "", errSessaoCloudSemID
		}
		return "wa_cloud_" + sess.CloudPhoneNumberID, "UC Talk Oficial +" + sess.CloudDisplayPhone, nil
	}
	fone := sessionJID
	if at := strings.IndexByte(fone, '@'); at > 0 {
		fone = fone[:at]
	}
	if c := strings.IndexByte(fone, ':'); c > 0 {
		fone = fone[:c]
	}
	if fone == "" {
		return "", "", errJIDInvalido
	}
	return "wa_qr_" + fone, "UC Talk +" + fone, nil
}

// publicarConector deixa o conector utilizavel na linha, na ordem que o Bitrix
// exige.
//
//  1. imconnector.register            — cria/atualiza o conector do app.
//     Reregistrar o mesmo ID atualiza, nao duplica.
//  2. imconnector.activate            — liga na linha. APAGA os dados anteriores
//     e limpa o flag ERROR.
//  3. imconnector.connector.data.set  — so' agora os dados sobrevivem.
//
// Nenhuma falha e' fatal: isto roda em background, depois de o Bitrix ja' ter
// recebido a resposta. Mas cada uma vira log com o conector e a linha, porque o
// sintoma no cliente ("mensagem nao chega") nao diz qual dos tres passos falhou.
//
// event.bind NAO entra aqui de proposito: o Partner App tem INSTALLED:false e o
// Bitrix nao entrega eventos pra ele. Quem faz o bind e' o fluxo do app Local.
func (h *handlers) publicarConector(ctx context.Context, creds bitrix.TenantCreds, connectorID, nome string, lineID int, origem string) {
	campos := []zap.Field{
		zap.String("origem", origem),
		zap.String("domain", creds.Domain),
		zap.String("connector_id", connectorID),
		zap.Int("line", lineID),
	}
	if connectorID == "" || lineID <= 0 {
		h.log.Warn("publicarConector: conector ou linha ausente — nada publicado", campos...)
		return
	}
	if nome == "" {
		nome = "UC Talk"
	}
	appBase := h.cfg.App.BaseURL()

	if err := h.bitrixClient.RegisterConnector(ctx, creds, connectorID, nome, appBase+"/bitrix-connect"); err != nil {
		// APPLICATION_REGISTRATION_ERROR = ja' registrado por este app. Nao e'
		// motivo pra parar: o activate abaixo ainda precisa rodar.
		h.log.Warn("publicarConector: register falhou (pode ja' existir)", append(campos, zap.Error(err))...)
	}
	// Antes de data.set — ver o comentario no topo do arquivo.
	if err := h.bitrixClient.ActivateConnector(ctx, creds, connectorID, lineID, true); err != nil {
		h.log.Warn("publicarConector: activate falhou", append(campos, zap.Error(err))...)
		return // sem activate, o data.set abaixo nao tem onde pousar
	}
	if err := h.bitrixClient.SetConnectorData(ctx, creds, connectorID, lineID, ""); err != nil {
		h.log.Warn("publicarConector: data.set falhou — conector ativo porem NAO configurado",
			append(campos, zap.Error(err))...)
		return
	}
	h.log.Info("publicarConector: conector pronto na linha", campos...)
}

// conectorDeEnvio decide com que conector e em que linha uma mensagem SAINDO do
// CRM deve ser espelhada no Open Channel.
//
// A resposta certa e' o conector da SESSAO que esta' enviando — que e' o mesmo
// que o inbound usou pra criar o dialogo (processor.go usa acct.ConnectorID).
// Antes isto vinha de portal.ConnectorID, o generico: o dialogo nascia num
// conector e a confirmacao de entrega (imconnector.send.status.delivery) era
// enviada pra outro, entao a mensagem do operador ficava sem o "entregue".
//
// lineSugerida (quando > 0) tem prioridade: e' a linha que o operador escolheu
// na aba. Sem ela, vale a linha do vinculo; e por ultimo a do portal.
//
// Sem vinculo em bitrix_accounts ainda da' pra enviar — o WhatsApp nao depende
// do Bitrix — entao cai no generico do portal em vez de recusar.
func (h *handlers) conectorDeEnvio(ctx context.Context, sessionJID string, portalConnector string, portalLine, lineSugerida int) (connectorID string, lineID int) {
	lineID = lineSugerida

	if acct, err := h.repo.GetBitrixAccountByJID(ctx, sessionJID); err == nil && acct != nil && acct.ConnectorID != "" {
		connectorID = acct.ConnectorID
		if lineID <= 0 {
			lineID = acct.OpenLineID
		}
	} else {
		h.log.Warn("conectorDeEnvio: sessao sem vinculo em bitrix_accounts — usando o conector generico do portal",
			zap.String("session_jid", sessionJID), zap.Error(err))
		connectorID = portalConnector
	}

	if connectorID == "" {
		connectorID = "whatsapp_uc_v2"
	}
	if lineID <= 0 {
		lineID = portalLine
	}
	if lineID <= 0 {
		lineID = 1
	}
	return connectorID, lineID
}
