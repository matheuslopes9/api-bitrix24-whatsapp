// licenses.go — licenca por cliente e historico de pagamentos.
//
// Substitui o trio tenant_plans + plan_definitions + billing_charges do
// modelo SaaS antigo. Fica em arquivo proprio, e nao no repository.go, que
// ja' passa de 3 mil linhas e e' apontado como god object em
// docs/aprendizados/01-arquitetura.md — nao faz sentido piorar.
package db

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// TenantLicense e' o que o contrato do cliente libera.
//
// Diferente do modelo antigo, os beneficios ficam AQUI, no proprio cliente —
// nao num catalogo de planos separado. Era a indirecao pelo catalogo que
// fazia um tenant marcado "Pro" aparecer sem nenhuma feature quando a linha
// do plano era reescrita pela UI.
type TenantLicense struct {
	Domain string `db:"domain"`

	MaxSessions     int  `db:"max_sessions"`
	FeatCloudAPI    bool `db:"feat_cloud_api"`   // Cloud API (Meta) + Templates
	FeatAutomations bool `db:"feat_automations"` // robos BizProc
	FeatReports     bool `db:"feat_reports"`
	FeatSMS         bool `db:"feat_sms"` // oculto na UI por enquanto

	// ValidUntil nil = sem prazo. Vencer NAO bloqueia o app.
	ValidUntil *time.Time `db:"valid_until"`
	Notes      string     `db:"notes"`

	// Onboarding herdado de tenant_plans (nao e' cobranca).
	WelcomeShown    bool       `db:"welcome_shown"`
	MasterAutoSetAt *time.Time `db:"master_auto_set_at"`

	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// Expired diz se a vigencia passou. Serve para AVISAR, nao para bloquear:
// a decisao de produto e' que inadimplencia nunca derruba o atendimento do
// cliente final — quem cobra e' o comercial.
func (l *TenantLicense) Expired() bool {
	if l == nil || l.ValidUntil == nil {
		return false
	}
	return time.Now().After(endOfDay(*l.ValidUntil))
}

// DaysUntilExpiry retorna quantos dias faltam (negativo se ja' venceu) e
// false quando a licenca nao tem prazo.
func (l *TenantLicense) DaysUntilExpiry() (int, bool) {
	if l == nil || l.ValidUntil == nil {
		return 0, false
	}
	d := time.Until(endOfDay(*l.ValidUntil))
	return int(d.Hours() / 24), true
}

// endOfDay estende a data para 23:59:59 — valid_until e' DATE, e uma licenca
// valida "ate 22/09" tem que valer o dia 22 inteiro, nao expirar 00:00.
func endOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, t.Location())
}

// LicensePayment e' um pagamento lancado a mao pelo suporte.
type LicensePayment struct {
	ID          uuid.UUID `db:"id"`
	Domain      string    `db:"domain"`
	PaidAt      time.Time `db:"paid_at"`      // quando o cliente pagou
	CoversUntil time.Time `db:"covers_until"` // ate quando libera o uso
	AmountCents int64     `db:"amount_cents"`
	Method      string    `db:"method"`
	Notes       string    `db:"notes"`
	RecordedBy  string    `db:"recorded_by"`
	CreatedAt   time.Time `db:"created_at"`
}

const licenseCols = `domain, max_sessions, feat_cloud_api, feat_automations,
	feat_reports, feat_sms, valid_until, notes, welcome_shown,
	master_auto_set_at, created_at, updated_at`

func scanLicense(r interface{ Scan(...any) error }, l *TenantLicense) error {
	return r.Scan(&l.Domain, &l.MaxSessions, &l.FeatCloudAPI, &l.FeatAutomations,
		&l.FeatReports, &l.FeatSMS, &l.ValidUntil, &l.Notes, &l.WelcomeShown,
		&l.MasterAutoSetAt, &l.CreatedAt, &l.UpdatedAt)
}

// GetLicense busca a licenca do dominio. Devolve (nil, nil) quando nao
// existe — ausencia de licenca nao e' erro, e' um cliente ainda nao
// configurado. O caller decide o que fazer.
func (r *Repository) GetLicense(ctx context.Context, domain string) (*TenantLicense, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT `+licenseCols+` FROM tenant_licenses WHERE domain = $1`, domain)
	var l TenantLicense
	if err := scanLicense(row, &l); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &l, nil
}

// EnsureLicense garante que o dominio tem licenca, criando no minimo
// (1 sessao, sem prazo, sem features) se ainda nao houver.
//
// Chamado no install: um cliente recem-instalado tem que conseguir parear um
// numero e operar antes de alguem passar no admin configurar o contrato.
func (r *Repository) EnsureLicense(ctx context.Context, domain string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO tenant_licenses (domain) VALUES ($1)
		ON CONFLICT (domain) DO NOTHING`, domain)
	return err
}

// ListLicenses devolve todas as licencas, as que vencem primeiro no topo
// (sem prazo por ultimo) — a ordem util para o suporte cobrar renovacao.
func (r *Repository) ListLicenses(ctx context.Context) ([]*TenantLicense, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+licenseCols+` FROM tenant_licenses
		  ORDER BY valid_until ASC NULLS LAST, domain ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*TenantLicense
	for rows.Next() {
		var l TenantLicense
		if err := scanLicense(rows, &l); err != nil {
			return nil, err
		}
		out = append(out, &l)
	}
	return out, rows.Err()
}

// SaveLicenseFeatures grava os beneficios contratados e a vigencia.
//
// NAO toca welcome_shown nem master_auto_set_at: sao estado de onboarding
// que mora na mesma tabela por conveniencia, e um save de contrato feito
// pelo suporte nao pode reapresentar as boas-vindas pro cliente.
func (r *Repository) SaveLicenseFeatures(ctx context.Context, l *TenantLicense) error {
	if l.MaxSessions < 1 {
		l.MaxSessions = 1
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO tenant_licenses
			(domain, max_sessions, feat_cloud_api, feat_automations,
			 feat_reports, feat_sms, valid_until, notes)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (domain) DO UPDATE SET
			max_sessions     = EXCLUDED.max_sessions,
			feat_cloud_api   = EXCLUDED.feat_cloud_api,
			feat_automations = EXCLUDED.feat_automations,
			feat_reports     = EXCLUDED.feat_reports,
			feat_sms         = EXCLUDED.feat_sms,
			valid_until      = EXCLUDED.valid_until,
			notes            = EXCLUDED.notes,
			updated_at       = NOW()`,
		l.Domain, l.MaxSessions, l.FeatCloudAPI, l.FeatAutomations,
		l.FeatReports, l.FeatSMS, l.ValidUntil, l.Notes)
	return err
}

// SetLicenseWelcomeShown marca que as boas-vindas ja' foram exibidas.
// Substitui SetWelcomeShown, que escrevia em tenant_plans.
func (r *Repository) SetLicenseWelcomeShown(ctx context.Context, domain string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO tenant_licenses (domain, welcome_shown) VALUES ($1, TRUE)
		ON CONFLICT (domain) DO UPDATE SET welcome_shown = TRUE, updated_at = NOW()`,
		domain)
	return err
}

// MarkLicenseMasterAutoSet registra que o master foi definido automaticamente.
// Substitui MarkMasterAutoSet, que escrevia em tenant_plans.
func (r *Repository) MarkLicenseMasterAutoSet(ctx context.Context, domain string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO tenant_licenses (domain, master_auto_set_at) VALUES ($1, NOW())
		ON CONFLICT (domain) DO UPDATE SET master_auto_set_at = NOW(), updated_at = NOW()`,
		domain)
	return err
}

// ─── Pagamentos ───────────────────────────────────────────────────────────

// RecordPayment lanca um pagamento e estende a vigencia da licenca.
//
// As duas escritas vao na MESMA transacao: um pagamento registrado que nao
// estende a licenca deixaria o cliente marcado como vencido tendo pago, e o
// financeiro seria avisado a toa.
//
// A vigencia usa GREATEST: lancar um pagamento antigo (conciliacao atrasada)
// nao pode ENCURTAR uma licenca que ja' vai mais longe.
func (r *Repository) RecordPayment(ctx context.Context, p *LicensePayment) error {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op apos Commit

	if _, err := tx.Exec(ctx, `
		INSERT INTO license_payments
			(id, domain, paid_at, covers_until, amount_cents, method, notes, recorded_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		p.ID, p.Domain, p.PaidAt, p.CoversUntil, p.AmountCents,
		p.Method, p.Notes, p.RecordedBy); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO tenant_licenses (domain, valid_until)
		VALUES ($1, $2)
		ON CONFLICT (domain) DO UPDATE SET
			valid_until = GREATEST(
				COALESCE(tenant_licenses.valid_until, EXCLUDED.valid_until),
				EXCLUDED.valid_until),
			updated_at = NOW()`,
		p.Domain, p.CoversUntil); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// ListPayments devolve o historico de pagamentos do dominio, mais recente
// primeiro.
func (r *Repository) ListPayments(ctx context.Context, domain string, limit int) ([]*LicensePayment, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, domain, paid_at, covers_until, amount_cents, method,
		       notes, recorded_by, created_at
		  FROM license_payments
		 WHERE domain = $1
		 ORDER BY paid_at DESC, created_at DESC
		 LIMIT $2`, domain, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*LicensePayment
	for rows.Next() {
		var p LicensePayment
		if err := rows.Scan(&p.ID, &p.Domain, &p.PaidAt, &p.CoversUntil,
			&p.AmountCents, &p.Method, &p.Notes, &p.RecordedBy, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

// ─── Avisos de vencimento ─────────────────────────────────────────────────

// ListLicensesExpiringOrExpired devolve as licencas que vencem em ate
// `withinDays` dias, e as que ja' venceram. Base do job diario de aviso.
func (r *Repository) ListLicensesExpiringOrExpired(ctx context.Context, withinDays int) ([]*TenantLicense, error) {
	if withinDays < 0 {
		withinDays = 0
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+licenseCols+` FROM tenant_licenses
		  WHERE valid_until IS NOT NULL
		    AND valid_until <= CURRENT_DATE + make_interval(days => $1)
		  ORDER BY valid_until ASC`, withinDays)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*TenantLicense
	for rows.Next() {
		var l TenantLicense
		if err := scanLicense(rows, &l); err != nil {
			return nil, err
		}
		out = append(out, &l)
	}
	return out, rows.Err()
}

// MarkNotificationSent registra que o aviso foi enviado. Devolve false se
// aquele aviso JA' tinha sido enviado — e' o que impede o financeiro de
// receber o mesmo e-mail todo dia enquanto a licenca segue vencida.
func (r *Repository) MarkNotificationSent(ctx context.Context, domain, kind string, refDate time.Time) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		INSERT INTO license_notifications (domain, kind, ref_date)
		VALUES ($1,$2,$3)
		ON CONFLICT (domain, kind, ref_date) DO NOTHING`,
		domain, kind, refDate)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ─── Apoio ao diagnostico de suporte ──────────────────────────────────────

// ListBitrixAccountsByDomain devolve os vinculos sessao<->Linha Aberta do
// dominio. Usado pela tela de saude: sem vinculo, mensagem recebida nao
// chega no Contact Center, e era preciso ir no banco pra descobrir isso.
func (r *Repository) ListBitrixAccountsByDomain(ctx context.Context, domain string) ([]*BitrixAccount, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, session_jid, domain, client_id, client_secret, open_line_id,
		       connector_id, redirect_uri, status, created_at, updated_at
		  FROM bitrix_accounts
		 WHERE LOWER(REGEXP_REPLACE(domain, '^https?://(www\.)?', '')) = LOWER($1)
		 ORDER BY updated_at DESC`, domain)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*BitrixAccount
	for rows.Next() {
		var a BitrixAccount
		if err := rows.Scan(&a.ID, &a.SessionJID, &a.Domain, &a.ClientID, &a.ClientSecret,
			&a.OpenLineID, &a.ConnectorID, &a.RedirectURI, &a.Status,
			&a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &a)
	}
	return out, rows.Err()
}

// CountFailedMessagesByDomain conta mensagens que falharam no periodo.
//
// So' devolve numero util a partir da migration 043: antes dela a tabela
// messages nao tinha as colunas status/error_msg, e TODA atualizacao de
// status falhava em silencio — nenhuma mensagem chegava a ser marcada como
// 'failed' nem como 'delivered'.
func (r *Repository) CountFailedMessagesByDomain(ctx context.Context, domain string, since time.Time) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		  FROM messages m
		  JOIN whatsapp_sessions ws ON m.session_id = ws.id
		  JOIN bitrix_accounts ba
		    ON SPLIT_PART(SPLIT_PART(ws.jid,'@',1),':',1)
		     = SPLIT_PART(SPLIT_PART(ba.session_jid,'@',1),':',1)
		 WHERE LOWER(REGEXP_REPLACE(ba.domain, '^https?://(www\.)?', '')) = LOWER($1)
		   AND m.status = 'failed'
		   AND m.created_at >= $2`, domain, since).Scan(&n)
	return n, err
}
