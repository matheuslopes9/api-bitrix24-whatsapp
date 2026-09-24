package db

import (
	"context"
	"strings"
	"time"
)

// ConfigAlertas e' o que o painel edita. Vive no banco e nao no ambiente
// porque trocar destinatario nao pode exigir reiniciar o app — reiniciar
// derruba o atendimento de todos os clientes.
type ConfigAlertas struct {
	SMTPHost      string `json:"smtp_host"`
	SMTPPort      int    `json:"smtp_port"`
	EmailSender   string `json:"email_sender"`
	EmailReplyTo  string `json:"email_reply_to"`
	Destinatarios string `json:"destinatarios"` // separados por virgula

	TokenAtivo      bool `json:"token_ativo"`
	TokenJanelaH    int  `json:"token_janela_h"`
	SessaoAtivo     bool `json:"sessao_ativo"`
	SessaoJanelaMin int  `json:"sessao_janela_min"`
	LicencaAtivo    bool `json:"licenca_ativo"`

	AtualizadoEm  time.Time `json:"atualizado_em"`
	AtualizadoPor string    `json:"atualizado_por"`
}

// ListaDestinatarios separa o campo livre numa lista limpa.
func (c ConfigAlertas) ListaDestinatarios() []string {
	var out []string
	for _, p := range strings.Split(c.Destinatarios, ",") {
		if e := strings.TrimSpace(p); e != "" {
			out = append(out, e)
		}
	}
	return out
}

// Configurado diz se da' pra enviar. Sem isso o chamador nao distingue "nao
// enviei porque falhou" de "nao enviei porque nao ha pra onde".
func (c ConfigAlertas) Configurado() bool {
	return c.SMTPHost != "" && c.SMTPPort > 0 && c.EmailSender != "" && len(c.ListaDestinatarios()) > 0
}

func (r *Repository) GetConfigAlertas(ctx context.Context) (*ConfigAlertas, error) {
	var c ConfigAlertas
	err := r.pool.QueryRow(ctx, `
		SELECT smtp_host, smtp_port, email_sender, email_reply_to, destinatarios,
		       token_ativo, token_janela_h, sessao_ativo, sessao_janela_min, licenca_ativo,
		       atualizado_em, atualizado_por
		  FROM config_alertas WHERE id = TRUE`).
		Scan(&c.SMTPHost, &c.SMTPPort, &c.EmailSender, &c.EmailReplyTo, &c.Destinatarios,
			&c.TokenAtivo, &c.TokenJanelaH, &c.SessaoAtivo, &c.SessaoJanelaMin, &c.LicencaAtivo,
			&c.AtualizadoEm, &c.AtualizadoPor)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *Repository) SalvarConfigAlertas(ctx context.Context, c *ConfigAlertas, quem string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE config_alertas SET
			smtp_host = $1, smtp_port = $2, email_sender = $3, email_reply_to = $4,
			destinatarios = $5, token_ativo = $6, token_janela_h = $7,
			sessao_ativo = $8, sessao_janela_min = $9, licenca_ativo = $10,
			atualizado_em = NOW(), atualizado_por = $11
		 WHERE id = TRUE`,
		c.SMTPHost, c.SMTPPort, c.EmailSender, c.EmailReplyTo, c.Destinatarios,
		c.TokenAtivo, c.TokenJanelaH, c.SessaoAtivo, c.SessaoJanelaMin, c.LicencaAtivo, quem)
	return err
}

// AlertaEnviado e' uma linha do historico. Serve pra responder "esse alerta
// chegou a sair?" sem abrir o log do container.
type AlertaEnviado struct {
	Tipo      string    `json:"tipo"`
	Ref       string    `json:"ref"`
	Dominio   string    `json:"dominio"`
	EnviadoEm time.Time `json:"enviado_em"`
}

func (r *Repository) ListarAlertasEnviados(ctx context.Context, limite int) ([]AlertaEnviado, error) {
	if limite <= 0 || limite > 200 {
		limite = 50
	}
	rows, err := r.pool.Query(ctx, `
		SELECT tipo, ref, dominio, enviado_em
		  FROM alertas_operacionais
		 ORDER BY enviado_em DESC
		 LIMIT $1`, limite)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]AlertaEnviado, 0, limite)
	for rows.Next() {
		var a AlertaEnviado
		if err := rows.Scan(&a.Tipo, &a.Ref, &a.Dominio, &a.EnviadoEm); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
