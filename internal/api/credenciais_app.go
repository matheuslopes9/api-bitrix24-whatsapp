package api

// credenciais_app.go — de onde sai o client_id/client_secret de cada portal.
//
// O PROBLEMA QUE ISTO RESOLVE: existiam duas fontes de credencial e uma delas
// quase nunca era consultada.
//
//	portalToCreds       -> BITRIX_CLIENT_ID/SECRET do ambiente   (38 chamadas)
//	localCredsForDomain -> o que a tela "Credenciais do app" grava (3 chamadas)
//
// Ou seja: cadastrar a credencial pela tela dava a impressao de resolver e
// alcancava 3 dos 41 caminhos. A tela ainda respondia "a renovacao do token
// passa a usar este app" — o que era simplesmente falso. Quem cadastrasse para
// consertar um wrong_client sairia da tela achando que tinha consertado.
//
// A REGRA AGORA, uma so': o ambiente e' o padrao (o app Partner da UC, o mesmo
// para todos os clientes instalados); a credencial gravada na conta e' uma
// EXCECAO por portal, e ganha quando existe. Ninguem digita uma credencial na
// tela por acidente — se ela esta' la', e' porque aquele portal tem um app
// proprio.
//
// POR QUE EM CACHE: portalToCreds nao recebe ctx e e' chamada em 38 lugares,
// varios no caminho quente de mensagem. Trocar a assinatura de todos para
// consultar o banco a cada chamada seria pagar uma ida ao Postgres pelo que
// quase sempre e' o valor do ambiente. O cache carrega no boot, se atualiza no
// exato momento em que alguem grava pela tela, e revisa sozinho de tempos em
// tempos para o caso de alguem mexer direto no banco.

import (
	"context"
	"sync"
	"time"

	"github.com/uctechnology/api-bitrix24-whatsapp/internal/bitrix"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"go.uber.org/zap"
)

// intervaloRevisaoCreds: alguem editar bitrix_accounts direto no banco e' raro
// e nunca urgente. De 5 em 5 minutos pega isso sem custo relevante — o caminho
// normal (a tela) ja' atualiza na hora.
const intervaloRevisaoCreds = 5 * time.Minute

// credsApp guarda as excecoes por dominio. Vazio = todo mundo usa o ambiente,
// que e' o estado esperado.
type credsApp struct {
	mu   sync.RWMutex
	pord map[string]db.AppCreds
}

func novoCredsApp() *credsApp { return &credsApp{pord: map[string]db.AppCreds{}} }

func (c *credsApp) get(dominio string) (db.AppCreds, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.pord[normalizeDomainKey(dominio)]
	return v, ok
}

func (c *credsApp) trocar(novo map[string]db.AppCreds) {
	c.mu.Lock()
	c.pord = novo
	c.mu.Unlock()
}

func (c *credsApp) dominios() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.pord))
	for d := range c.pord {
		out = append(out, d)
	}
	return out
}

// recarregarCredenciais le o banco e troca o mapa inteiro de uma vez.
//
// Troca por substituicao, nao por merge: credencial APAGADA na tela tem que
// sumir daqui tambem. Um merge deixaria a excecao viva para sempre, e o unico
// jeito de voltar ao ambiente seria reiniciar o processo.
func (h *handlers) recarregarCredenciais(ctx context.Context) {
	if h.repo == nil || h.credsApp == nil {
		return
	}
	novo, err := h.repo.CredenciaisPorDominio(ctx)
	if err != nil {
		// Mantem o que ja' estava: derrubar as excecoes por causa de um erro
		// de leitura faria portais com app proprio cairem para o ambiente
		// errado — trocaria uma falha de leitura por wrong_client.
		h.log.Warn("credenciais: falha ao recarregar — mantendo o que estava", zap.Error(err))
		return
	}
	antes := len(h.credsApp.dominios())
	h.credsApp.trocar(novo)
	if len(novo) != antes {
		h.log.Info("credenciais de app por portal recarregadas",
			zap.Int("portais_com_app_proprio", len(novo)),
			zap.Strings("dominios", h.credsApp.dominios()))
	}
}

// IniciarRevisaoCredenciais carrega o cache no boot e revisa periodicamente.
func (h *handlers) IniciarRevisaoCredenciais(ctx context.Context) {
	if h.repo == nil || h.credsApp == nil {
		return
	}
	h.recarregarCredenciais(ctx) // sincrono: as primeiras chamadas ja' acertam
	go func() {
		t := time.NewTicker(intervaloRevisaoCreds)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				semPanico(h.log, "revisao de credenciais", func() { h.recarregarCredenciais(ctx) })
			}
		}
	}()
}

// credenciaisDoPortal devolve a credencial em vigor e de onde ela veio.
//
// A segunda resposta existe porque a tela precisa dizer a verdade: mostrar
// "credencial cadastrada" enquanto o sistema usa a do ambiente foi exatamente
// o que fez a tela mentir por semanas.
func (h *handlers) credenciaisDoPortal(dominio string) (id, secret, origem string) {
	envID, envSecret := h.cfg.Bitrix.ClientID, h.cfg.Bitrix.ClientSecret
	if h.credsApp != nil {
		if c, ok := h.credsApp.get(dominio); ok {
			// Gravado igual ao ambiente nao e' excecao nenhuma — e' o mesmo app
			// Partner, copiado para a conta em algum momento. Medido no homolog
			// em 30/09: os DOIS portais tinham o client_id da env gravado, e
			// chamar isso de "app proprio" poria um aviso amarelo em todo
			// cliente para avisar que nada e' diferente. Aviso que aparece
			// sempre e' aviso que ninguem le.
			if c.ClientID == envID && c.ClientSecret == envSecret {
				return envID, envSecret, "ambiente"
			}
			return c.ClientID, c.ClientSecret, "portal"
		}
	}
	return envID, envSecret, "ambiente"
}

// portalToCreds monta as credenciais para uma chamada ao Bitrix deste portal.
func (h *handlers) portalToCreds(p *db.BitrixPortal) bitrix.TenantCreds {
	if p == nil {
		return bitrix.TenantCreds{}
	}
	id, secret, _ := h.credenciaisDoPortal(p.Domain)
	return bitrix.TenantCreds{
		Domain:       "https://" + p.Domain,
		ClientID:     id,
		ClientSecret: secret,
		RedirectURI:  h.cfg.App.BaseURL() + "/bitrix/install",
	}
}
