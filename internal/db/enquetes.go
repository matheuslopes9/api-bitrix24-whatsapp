package db

// enquetes.go — guarda as opcoes de uma enquete para conseguir NOMEAR o voto.
//
// O PORQUE: o voto do WhatsApp chega cifrado e, mesmo depois de aberto, so'
// traz o SHA-256 das opcoes escolhidas — nunca o texto delas. Quem quiser
// dizer em QUE o cliente votou precisa ter as opcoes originais para refazer o
// hash e casar.
//
// Sem isso o atendente via "[Voto em enquete]" e mais nada: sabia que houve
// voto, nao em que.

import (
	"context"
	"time"
)

// SalvarEnquete guarda as opcoes no momento em que a enquete chega.
//
// ON CONFLICT DO UPDATE porque o WhatsApp reenvia a mesma enquete em sync
// multi-device; gravar de novo e' idempotente e mantem o registro mais recente.
func (r *Repository) SalvarEnquete(ctx context.Context, waMessageID, pergunta string, opcoes []string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO enquetes (wa_message_id, pergunta, opcoes)
		VALUES ($1, $2, $3)
		ON CONFLICT (wa_message_id) DO UPDATE
		   SET pergunta = EXCLUDED.pergunta,
		       opcoes   = EXCLUDED.opcoes`,
		waMessageID, pergunta, opcoes)
	return err
}

// OpcoesDaEnquete devolve as opcoes originais. Enquete ausente NAO e' erro:
// pode ter sido criada antes desta tabela existir, ou ja' ter sido limpa. Quem
// chama cai no rotulo generico em vez de perder o voto.
func (r *Repository) OpcoesDaEnquete(ctx context.Context, waMessageID string) (pergunta string, opcoes []string, err error) {
	row := r.pool.QueryRow(ctx,
		`SELECT pergunta, opcoes FROM enquetes WHERE wa_message_id = $1`, waMessageID)
	if err := row.Scan(&pergunta, &opcoes); err != nil {
		return "", nil, err
	}
	return pergunta, opcoes, nil
}

// LimparEnquetesAntigas descarta o que ja' nao serve. O voto chega junto da
// enquete ou logo depois; guardar por 90 dias cobre folgado e evita a tabela
// crescer para sempre num portal movimentado.
func (r *Repository) LimparEnquetesAntigas(ctx context.Context, idade time.Duration) (int64, error) {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM enquetes WHERE criado_em < NOW() - $1::interval`,
		idade.String())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
