package db

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// Pareamentos: quem pediu o pareamento de um numero que ainda nao tem
// vinculo em bitrix_accounts.
//
// Existia so' em memoria. Se o app reiniciasse (todo deploy) entre parear o
// numero e liga-lo a uma fila, o numero ficava SEM DONO: o cliente nao
// conseguia mais vincula-lo, porque a checagem de dono recusava, e so' o
// admin resolvia. Persistido, sobrevive ao restart.

// SalvarPareamento registra (ou atualiza) o dono do numero em pareamento.
func (r *Repository) SalvarPareamento(ctx context.Context, numero, dominio string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO pareamentos (numero, dominio) VALUES ($1, $2)
		ON CONFLICT (numero) DO UPDATE SET dominio = EXCLUDED.dominio, criado_em = NOW()`,
		numero, dominio)
	return err
}

// ApagarPareamento remove o registro (numero desconectado).
func (r *Repository) ApagarPareamento(ctx context.Context, numero string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM pareamentos WHERE numero = $1`, numero)
	return err
}

// PareamentosDoDominio lista os numeros que o dominio pareou nos ultimos
// 30 dias. Depois disso, numero que nunca foi ligado a uma fila volta a
// precisar do admin — ninguem deixa numero pareado e esquecido por um mes.
func (r *Repository) PareamentosDoDominio(ctx context.Context, dominio string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT numero FROM pareamentos
		 WHERE dominio = $1 AND criado_em > NOW() - INTERVAL '30 days'`, dominio)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// DonoDoNumero devolve o dominio dono do numero: primeiro pelo vinculo
// (bitrix_accounts), depois por pareamento pendente. "" = sem dono.
func (r *Repository) DonoDoNumero(ctx context.Context, numero string) (string, error) {
	var dono string
	err := r.pool.QueryRow(ctx, `
		SELECT dominio FROM (
			SELECT LOWER(REGEXP_REPLACE(domain, '^https?://(www\.)?', '')) AS dominio, 1 AS ordem
			  FROM bitrix_accounts
			 WHERE `+sqlNumeroBase("session_jid")+` = $1 AND domain <> ''
			UNION ALL
			SELECT dominio, 2 FROM pareamentos
			 WHERE numero = $1 AND criado_em > NOW() - INTERVAL '30 days'
		) d ORDER BY ordem LIMIT 1`, numero).Scan(&dono)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return dono, err
}
