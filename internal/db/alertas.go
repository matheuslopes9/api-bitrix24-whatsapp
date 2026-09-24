package db

import (
	"context"
	"time"
)

// DeveAvisar decide se um alerta pode ser enviado agora, e ja' registra o
// envio quando a resposta e' sim.
//
// POR QUE E' UMA COISA SO': se registrar fosse chamada separada, um erro no
// meio do envio deixaria a porta aberta pra reenviar em loop. Aqui a janela
// e' consumida no momento da decisao — no pior caso perde-se UM aviso, o que
// e' melhor que inundar a caixa do time.
//
// janela e' o intervalo minimo entre dois avisos do mesmo (tipo, ref). Token
// vencido pede janela longa (o problema persiste por horas); queda de sessao
// pede curta (pode voltar e cair de novo).
func (r *Repository) DeveAvisar(ctx context.Context, tipo, ref, dominio string, janela time.Duration) (bool, error) {
	var ultimo *time.Time
	err := r.pool.QueryRow(ctx, `
		SELECT MAX(enviado_em) FROM alertas_operacionais
		 WHERE tipo = $1 AND ref = $2`, tipo, ref).Scan(&ultimo)
	if err != nil {
		return false, err
	}
	if ultimo != nil && time.Since(*ultimo) < janela {
		return false, nil
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO alertas_operacionais (tipo, ref, dominio) VALUES ($1,$2,$3)`,
		tipo, ref, dominio)
	if err != nil {
		return false, err
	}
	return true, nil
}

// LimparAlertasAntigos evita a tabela crescer pra sempre. Alerta com mais de
// 90 dias nao serve nem pra deduplicar nem pra auditoria operacional.
func (r *Repository) LimparAlertasAntigos(ctx context.Context) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM alertas_operacionais WHERE enviado_em < NOW() - INTERVAL '90 days'`)
	return err
}

// PortalComTokenVencido e' o minimo pra escrever o alerta.
type PortalComTokenVencido struct {
	Domain    string
	ExpiresAt time.Time
}

// ListarTokensVencidos devolve os portais que NAO TEM NENHUM token valido.
//
// A versao anterior listava qualquer LINHA vencida, e um dominio pode ter
// varias: uma por app que ja' autorizou. Sobrava linha orfa de instalacao
// antiga, e ela sozinha disparava alerta de "token vencido" num cliente que
// estava atendendo normalmente — 117 mensagens de entrada no dia. Alerta
// falso e' pior que nenhum: ensina o time a ignorar.
//
// O que importa e' se sobrou ALGUM token utilizavel. Por isso HAVING sobre o
// MAX: so' e' problema quando ate' o mais novo ja' venceu.
func (r *Repository) ListarTokensVencidos(ctx context.Context) ([]PortalComTokenVencido, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT d, MAX(expires_at) AS mais_novo FROM (
			SELECT LOWER(REGEXP_REPLACE(domain, '^https?://(www\.)?', '')) AS d,
			       expires_at
			  FROM bitrix_tokens
		) t
		GROUP BY d
		HAVING MAX(expires_at) < NOW()
		ORDER BY d`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PortalComTokenVencido
	for rows.Next() {
		var p PortalComTokenVencido
		if err := rows.Scan(&p.Domain, &p.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetBitrixTokenUtilizavel devolve o token que de fato vale pro dominio: o de
// validade mais longa.
//
// GetBitrixToken ordena por updated_at, que nao e' a mesma coisa — linha
// antiga tocada por um UPDATE qualquer vence linha nova e boa. Foi o que fez
// a tela de saude dizer "token vencido" num cliente que estava atendendo:
// ela lia a linha orfa de 23/09 enquanto o token bom, emitido pelo app
// cadastrado, estava logo ao lado.
func (r *Repository) GetBitrixTokenUtilizavel(ctx context.Context, domain string) (*BitrixToken, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, domain, client_id, access_token, refresh_token, expires_at, scope, created_at, updated_at
		  FROM bitrix_tokens
		 WHERE LOWER(REGEXP_REPLACE(domain, '^https?://(www\.)?', '')) = LOWER($1)
		   AND access_token <> ''
		 ORDER BY expires_at DESC
		 LIMIT 1`, domain)
	var t BitrixToken
	if err := row.Scan(&t.ID, &t.Domain, &t.ClientID, &t.AccessToken, &t.RefreshToken,
		&t.ExpiresAt, &t.Scope, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}
	return &t, nil
}
