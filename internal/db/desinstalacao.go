package db

// desinstalacao.go — registrar que o cliente removeu o app do portal.
//
// O cliente que desinstala NAO avisa ninguem. O unico sinal e' o Bitrix passar
// a responder APPLICATION_NOT_FOUND em toda chamada daquele portal.
//
// Sem registrar isso, o portal ficava na lista de clientes com cara de ativo, a
// Saude dizia "integracao saudavel" (porque token e licenca continuam validos
// no NOSSO banco), e os alertas seguiam avisando que o token daquele cliente
// venceu — sobre um app que nao existe mais. Ruido que ensina o time a ignorar
// a caixa de alertas, que e' o custo real.
//
// NAO apagamos o portal: conversas, licenca e pagamentos continuam sendo
// registro, e cliente que reinstala precisa achar tudo onde estava.

import (
	"context"
	"strings"
)

// MarcarDesinstalado registra a desinstalacao. Idempotente: chamar de novo nao
// move a data, porque a primeira deteccao e' a que mais se aproxima do momento
// real — as seguintes so' confirmam o que ja' sabiamos.
func (r *Repository) MarcarDesinstalado(ctx context.Context, dominio, motivo string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE bitrix_portals
		   SET desinstalado_em = COALESCE(desinstalado_em, NOW()),
		       desinstalado_motivo = $2,
		       updated_at = NOW()
		 WHERE LOWER(REGEXP_REPLACE(domain, '^https?://(www\.)?', '')) = LOWER($1)`,
		dominio, motivo)
	return err
}

// MarcarInstalado desfaz a marca quando o portal volta a responder.
//
// Existe porque reinstalar e' comum: o cliente remove por engano, ou reinstala
// para resolver outro problema. Sem este caminho, um portal marcado ficaria
// marcado para sempre e a tela mentiria na direcao oposta — dizendo
// "desinstalado" sobre um cliente atendendo normalmente.
func (r *Repository) MarcarInstalado(ctx context.Context, dominio string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE bitrix_portals
		   SET desinstalado_em = NULL, desinstalado_motivo = '', updated_at = NOW()
		 WHERE LOWER(REGEXP_REPLACE(domain, '^https?://(www\.)?', '')) = LOWER($1)
		   AND desinstalado_em IS NOT NULL`, dominio)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// PortalDesinstalado consulta o estado sem carregar o portal inteiro.
func (r *Repository) PortalDesinstalado(ctx context.Context, dominio string) (bool, error) {
	var n int
	err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM bitrix_portals
		 WHERE LOWER(REGEXP_REPLACE(domain, '^https?://(www\.)?', '')) = LOWER($1)
		   AND desinstalado_em IS NOT NULL`, dominio).Scan(&n)
	return n > 0, err
}

// ErroDeAppRemovido reconhece a resposta do Bitrix para um app que nao existe
// mais no portal.
//
// POR QUE E' UMA FUNCAO E NAO UM strings.Contains SOLTO: a diferenca entre
// "nao consegui falar com o portal" e "o app nao existe mais la'" decide se o
// sistema espera ou marca o cliente como desinstalado. Errar para o lado de
// marcar transformaria uma instabilidade de rede em "cliente cancelou" na tela
// do suporte; errar para o outro lado mantem o ruido que isto veio resolver.
//
// So' os codigos que o Bitrix usa quando o APP sumiu entram aqui. Token
// vencido, portal fora do ar e timeout NAO entram: desses o app se recupera
// sozinho.
func ErroDeAppRemovido(err error) bool {
	if err == nil {
		return false
	}
	// APENAS este codigo. Considerei incluir NO_AUTH_FOUND e ERROR_OAUTH, que
	// tambem aparecem quando o app sumiu — mas os dois aparecem igualmente com
	// token ruim ou refresh vencido, que sao problemas de que o app se recupera
	// sozinho. Com eles na lista, um cliente ativo com token expirado viraria
	// "desinstalado" na tela do suporte, e alguem fecharia o contrato achando
	// que o cliente saiu.
	//
	// Cobrir menos e acertar sempre, em vez de cobrir mais e mentir as vezes.
	return strings.Contains(strings.ToUpper(err.Error()), "APPLICATION_NOT_FOUND")
}
