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

// ListarTokensVencidos devolve os portais cujo token ja' passou da validade.
//
// Enquanto o token nao renova, NENHUMA mensagem do cliente chega no Contact
// Center — e o sintoma que o cliente relata ("parou de chegar") nao aponta
// pra causa. Este e' o alerta que teria economizado as 16 horas de queda.
func (r *Repository) ListarTokensVencidos(ctx context.Context) ([]PortalComTokenVencido, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT ON (d) d, expires_at FROM (
			SELECT LOWER(REGEXP_REPLACE(domain, '^https?://(www\.)?', '')) AS d,
			       expires_at
			  FROM bitrix_tokens
			 WHERE expires_at < NOW()
		) t
		ORDER BY d, expires_at DESC`)
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
