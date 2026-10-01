package db

import (
	"errors"
	"testing"
)

// A deteccao de desinstalacao se apoia inteira em reconhecer UM erro do Bitrix.
// Classificar errado tem custo nos dois sentidos, e eles nao sao simetricos:
//
//   - marcar de menos devolve o ruido que isto veio resolver (alerta de token
//     vencido sobre um app que nao existe mais);
//   - marcar de mais poe "DESINSTALADO" na tela do suporte durante uma
//     instabilidade de rede, e alguem pode fechar o contrato de um cliente
//     que esta' atendendo normalmente.
//
// Por isso a regra e' estreita de proposito, e estes testes travam a largura.

func TestReconheceAppRemovido(t *testing.T) {
	// A forma exata com que o erro chega: client.callOnce formata como
	// "bitrix error: <codigo> — <descricao>".
	reais := []string{
		"bitrix error: APPLICATION_NOT_FOUND — Application not found",
		"parse placement.get: bitrix error: APPLICATION_NOT_FOUND",
		"bitrix error: application_not_found — Application not found",
	}
	for _, msg := range reais {
		if !ErroDeAppRemovido(errors.New(msg)) {
			t.Errorf("nao reconheceu app removido em %q — o cliente ficaria na lista como ativo", msg)
		}
	}
}

// Estes erros acontecem com o app INSTALADO e funcionando. Tratar qualquer um
// deles como desinstalacao seria dizer que o cliente saiu enquanto ele atende.
func TestNaoConfundeOutrasFalhasComDesinstalacao(t *testing.T) {
	naoSao := []string{
		"bitrix error: expired_token — The access token provided has expired",
		"bitrix error: NO_AUTH_FOUND — Wrong authorization data",
		"bitrix error: ERROR_OAUTH — refresh token invalido",
		"bitrix error: QUERY_LIMIT_EXCEEDED — Too many requests",
		"portal cliente.bitrix24.com.br inacessivel",
		"context deadline exceeded",
		"refresh token: sem credencial utilizavel",
	}
	for _, msg := range naoSao {
		if ErroDeAppRemovido(errors.New(msg)) {
			t.Errorf("%q foi classificado como desinstalacao — um cliente ativo apareceria como DESINSTALADO", msg)
		}
	}
}

func TestErroNuloNaoEhDesinstalacao(t *testing.T) {
	if ErroDeAppRemovido(nil) {
		t.Error("sucesso tratado como desinstalacao")
	}
}

// Desinstalado() e' o que as tres telas consultam. nil tem que significar
// instalado — inverter isso marcaria TODOS os clientes como desinstalados.
func TestDesinstaladoSoEhVerdadeComData(t *testing.T) {
	if (&BitrixPortal{}).Desinstalado() {
		t.Error("portal sem data marcado como desinstalado — todo cliente apareceria fora")
	}
	var nenhum *BitrixPortal
	if nenhum.Desinstalado() {
		t.Error("portal nil marcado como desinstalado")
	}
}
