package db

// permissoes.go — quem pode enviar por qual numero, e quem decide isso.
//
// O MODELO: numero nasce LIBERADO. Restringir e' uma acao deliberada, e so'
// depois dela as linhas de crm_user_permissions passam a valer.
//
// O modelo anterior negava por padrao, e o efeito era o oposto do pretendido:
// conectar um numero novo deixava o time inteiro sem conseguir responder pela
// aba do CRM, com a mensagem "peca para um admin liberar" — enquanto o unico
// usuario que podia liberar era um master unico, cuja unica acao disponivel na
// tela era TRANSFERIR o master pra outra pessoa. Para destravar um colega, o
// master precisava abrir mao do controle.
//
// Controle que so' se exerce desfazendo a si mesmo nao e' controle: e' um nó.

import (
	"context"
	"strings"
)

// IsSessionAllowed diz se um operador pode enviar por um numero.
//
// Numa consulta so' porque isto roda no caminho de TODO envio pela aba do CRM.
// A pergunta e': "o numero nao esta' restrito, OU este usuario esta' na lista
// dele?".
// sqlPermissaoDeEnvio e' a regra inteira — a decisao de quem envia e quem nao
// envia esta' nesta consulta, nao no Go em volta. Fica numa constante para o
// teste poder ler a MESMA string que roda em producao: um teste com a consulta
// copiada continuaria passando depois de alguem inverter a condicao aqui.
const sqlPermissaoDeEnvio = `
	SELECT
		NOT EXISTS (
			SELECT 1 FROM crm_numero_restrito
			 WHERE domain = $1 AND session_jid = $3
		)
		OR EXISTS (
			SELECT 1 FROM crm_user_permissions
			 WHERE domain = $1 AND user_id = $2
			   AND (session_jid = $3 OR session_jid = '')
		)`

func (r *Repository) IsSessionAllowed(ctx context.Context, domain, userID, sessionJID string) (bool, error) {
	sessionJID = normalizarSessionJID(sessionJID)
	var ok bool
	err := r.pool.QueryRow(ctx, sqlPermissaoDeEnvio, domain, userID, sessionJID).Scan(&ok)
	return ok, err
}

// NumerosRestritos devolve os numeros do portal que estao sob controle.
func (r *Repository) NumerosRestritos(ctx context.Context, domain string) (map[string]bool, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT session_jid FROM crm_numero_restrito WHERE domain = $1`, domain)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var jid string
		if err := rows.Scan(&jid); err != nil {
			return nil, err
		}
		out[jid] = true
	}
	return out, rows.Err()
}

// DefinirRestricao liga ou desliga o controle de um numero.
//
// Desligar NAO apaga as permissoes ja' cadastradas: se alguem restringir de
// novo, a lista de antes volta inteira. Apagar faria um clique desfazer um
// trabalho de configuracao que ninguem pediu pra desfazer.
func (r *Repository) DefinirRestricao(ctx context.Context, domain, sessionJID string, restrito bool, por string) error {
	sessionJID = normalizarSessionJID(sessionJID)
	if restrito {
		_, err := r.pool.Exec(ctx, `
			INSERT INTO crm_numero_restrito (domain, session_jid, restrito_por)
			VALUES ($1, $2, $3)
			ON CONFLICT (domain, session_jid) DO UPDATE
			   SET restrito_em = NOW(), restrito_por = EXCLUDED.restrito_por`,
			domain, sessionJID, por)
		return err
	}
	_, err := r.pool.Exec(ctx,
		`DELETE FROM crm_numero_restrito WHERE domain = $1 AND session_jid = $2`,
		domain, sessionJID)
	return err
}

// Gestor e' quem pode liberar e restringir numeros do portal.
type Gestor struct {
	UserID    string `json:"user_id"`
	UserName  string `json:"user_name"`
	CriadoPor string `json:"criado_por"`
}

// ListarGestores devolve os gestores NOMEADOS.
//
// Nao inclui os administradores do portal Bitrix, que sao gestores por direito
// e conferidos na hora: guardar uma copia aqui significaria que tirar o cargo
// de admin no Bitrix nao tiraria o poder no UC Talk, e a lista viraria mentira
// com o tempo.
func (r *Repository) ListarGestores(ctx context.Context, domain string) ([]Gestor, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT user_id, user_name, criado_por
		  FROM crm_gestores WHERE domain = $1 ORDER BY user_name, user_id`, domain)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Gestor
	for rows.Next() {
		var g Gestor
		if err := rows.Scan(&g.UserID, &g.UserName, &g.CriadoPor); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// EhGestorNomeado consulta so' a tabela — o papel de admin do Bitrix e a
// condicao de master sao conferidos em outro lugar (api/permissoes_gestao.go).
func (r *Repository) EhGestorNomeado(ctx context.Context, domain, userID string) (bool, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM crm_gestores WHERE domain = $1 AND user_id = $2`,
		domain, userID).Scan(&n)
	return n > 0, err
}

func (r *Repository) AdicionarGestor(ctx context.Context, domain, userID, userName, por string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO crm_gestores (domain, user_id, user_name, criado_por)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (domain, user_id) DO UPDATE SET user_name = EXCLUDED.user_name`,
		domain, strings.TrimSpace(userID), strings.TrimSpace(userName), por)
	return err
}

func (r *Repository) RemoverGestor(ctx context.Context, domain, userID string) (int64, error) {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM crm_gestores WHERE domain = $1 AND user_id = $2`, domain, userID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
