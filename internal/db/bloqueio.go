package db

// bloqueio.go — bloquear um cliente pelo painel admin.
//
// Bloqueado, o cliente deixa de usar o UC Talk: mensagem do WhatsApp nao
// chega no Bitrix, mensagem do operador nao sai, e o painel e a aba do CRM
// mostram "acesso bloqueado". O cadastro FICA — conversas, licenca e
// pagamentos continuam sendo registro, e desbloquear devolve tudo como estava.
//
// POR QUE NAO APAGAR: apagar o portal nao bloqueia. Quando o cliente abre o
// app de novo, o fluxo de autenticacao recria o portal e a licenca sozinho, e
// ele volta a usar como se nada tivesse acontecido. So' uma marca guardada
// impede o uso.

import (
	"context"
	"strings"
	"sync"
	"time"
)

// Bloqueio e' o estado de um portal bloqueado.
type Bloqueio struct {
	Domain string    `json:"domain"`
	Em     time.Time `json:"bloqueado_em"`
	Motivo string    `json:"motivo"`
	Por    string    `json:"bloqueado_por"`
}

// cacheBloqueio: PortalBloqueado roda em TODA mensagem (entrada e saida) e
// em toda chamada do painel do cliente. Alguns segundos de cache tiram essa
// consulta do caminho quente; bloquear e desbloquear limpam o cache na hora,
// entao a acao do admin vale imediatamente neste processo.
var cacheBloqueio sync.Map // dominio normalizado -> entradaBloqueio

type entradaBloqueio struct {
	bloqueado bool
	motivo    string
	em        time.Time
}

const ttlCacheBloqueio = 15 * time.Second

// BloquearPortal marca o portal como bloqueado. Bloquear de novo atualiza o
// motivo e quem bloqueou.
func (r *Repository) BloquearPortal(ctx context.Context, dominio, motivo, por string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE bitrix_portals
		   SET bloqueado_em = COALESCE(bloqueado_em, NOW()),
		       bloqueado_motivo = $2,
		       bloqueado_por = $3,
		       updated_at = NOW()
		 WHERE LOWER(REGEXP_REPLACE(domain, '^https?://(www\.)?', '')) = LOWER($1)`,
		dominio, motivo, por)
	cacheBloqueio.Delete(chaveDominio(dominio))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// DesbloquearPortal tira a marca. Devolve false se o portal nao estava
// bloqueado (ou nao existe).
func (r *Repository) DesbloquearPortal(ctx context.Context, dominio string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE bitrix_portals
		   SET bloqueado_em = NULL, bloqueado_motivo = '', bloqueado_por = '', updated_at = NOW()
		 WHERE LOWER(REGEXP_REPLACE(domain, '^https?://(www\.)?', '')) = LOWER($1)
		   AND bloqueado_em IS NOT NULL`, dominio)
	cacheBloqueio.Delete(chaveDominio(dominio))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// PortalBloqueado diz se o portal esta' bloqueado, e por que.
//
// Erro de leitura NAO bloqueia: em duvida, atender. Bloquear um cliente
// pagante por instabilidade do banco seria pior do que deixar passar uma
// mensagem de um bloqueado.
func (r *Repository) PortalBloqueado(ctx context.Context, dominio string) (bool, string) {
	chave := chaveDominio(dominio)
	if chave == "" || r == nil || r.pool == nil {
		return false, ""
	}
	if v, ok := cacheBloqueio.Load(chave); ok {
		e := v.(entradaBloqueio)
		if time.Since(e.em) < ttlCacheBloqueio {
			return e.bloqueado, e.motivo
		}
	}
	var motivo string
	var em *time.Time
	err := r.pool.QueryRow(ctx, `
		SELECT bloqueado_em, bloqueado_motivo FROM bitrix_portals
		 WHERE LOWER(REGEXP_REPLACE(domain, '^https?://(www\.)?', '')) = LOWER($1)
		 LIMIT 1`, chave).Scan(&em, &motivo)
	if err != nil {
		// Portal inexistente (pgx.ErrNoRows) ou falha: nao bloqueia, e nao
		// guarda falha no cache pra a proxima consulta tentar de novo.
		return false, ""
	}
	bloq := em != nil
	cacheBloqueio.Store(chave, entradaBloqueio{bloqueado: bloq, motivo: motivo, em: time.Now()})
	return bloq, motivo
}

// ListarBloqueios devolve todos os portais bloqueados, pra tela de Tenants.
func (r *Repository) ListarBloqueios(ctx context.Context) (map[string]Bloqueio, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT LOWER(REGEXP_REPLACE(domain, '^https?://(www\.)?', '')),
		       bloqueado_em, bloqueado_motivo, bloqueado_por
		  FROM bitrix_portals WHERE bloqueado_em IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Bloqueio{}
	for rows.Next() {
		var b Bloqueio
		if err := rows.Scan(&b.Domain, &b.Em, &b.Motivo, &b.Por); err != nil {
			return nil, err
		}
		out[b.Domain] = b
	}
	return out, rows.Err()
}

// chaveDominio normaliza como as consultas SQL: sem protocolo, sem www, sem
// barra final, minusculo — "https://www.X.com/" e "x.com" sao o mesmo portal.
func chaveDominio(d string) string {
	d = strings.ToLower(strings.TrimSpace(d))
	d = strings.TrimPrefix(strings.TrimPrefix(d, "https://"), "http://")
	d = strings.TrimPrefix(d, "www.")
	return strings.TrimSuffix(d, "/")
}
