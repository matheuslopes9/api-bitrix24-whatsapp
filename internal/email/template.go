package email

import (
	"fmt"
	"html"
	"strings"
	"time"
)

// template.go — o template de alerta da UC Technology, portado pra Go.
//
// E' o mesmo layout que o backend de ferramentas ja' usa
// (alert_email_template.py): faixa vermelha de urgencia, logo, selo da
// categoria, corpo com barra lateral, tabela de dados, acao recomendada,
// botao e rodape. Alerta do UC Talk chega com a mesma cara dos outros
// alertas da plataforma, e quem esta' de plantao reconhece de relance.
//
// POR QUE O HTML E' ESCRITO ASSIM — parece antiquado, e e' de proposito:
//
//   - layout em <table>, nao flex/grid: o Outlook renderiza com o motor do
//     Word, que ignora layout moderno e empilha tudo numa coluna so';
//   - estilo inline em cada elemento: varios clientes descartam o <style>
//     do cabecalho, e o que esta' no atributo style sobrevive;
//   - largura fixa de 620px com max-width:100%: previsivel no desktop sem
//     estourar a tela do celular;
//   - fundo escuro declarado em TODA celula: cliente que forca tema claro
//     repinta o que estiver sem cor explicita, e o texto claro sumiria;
//   - o link aparece tambem como texto embaixo do botao: se as imagens ou o
//     botao forem bloqueados, ainda da' pra copiar o endereco.

const (
	logoURL = "https://ferramentas.uctechnology.com.br/api/img/logo.png"
	siteURL = "https://ferramentas.uctechnology.com.br"
)

// Categorias de alerta do UC Talk.
//
// Existe pra que o alerta de numero desconectado nao chegue carimbado como
// "Licenciamento" — era o problema do template antigo do time.
const (
	CatTokenVencido = "token_vencido"
	CatSessaoCaiu   = "sessao_desconectada"
	CatLicenca      = "licenca_vencimento"
	CatTeste        = "teste"
)

type perfilCategoria struct {
	setor string // aparece na faixa vermelha
	selo  string // o "badge" abaixo do logo
	rota  string // caminho do botao, relativo ao painel
}

var perfis = map[string]perfilCategoria{
	CatTokenVencido: {"Integração Bitrix24", "Token de Acesso Vencido", "/admin"},
	CatSessaoCaiu:   {"Conexão WhatsApp", "Número Desconectado", "/admin"},
	CatLicenca:      {"Licenciamento", "Vencimento de Licença", "/admin"},
	CatTeste:        {"Alertas", "Teste de Envio", "/admin"},
}

// perfilDe nunca falha: categoria desconhecida cai num padrao seguro em vez
// de quebrar o envio. Alerta que nao sai por causa de rotulo errado e' pior
// que alerta com rotulo generico.
func perfilDe(categoria string) perfilCategoria {
	if p, ok := perfis[categoria]; ok {
		return p
	}
	return perfilCategoria{"Alertas", "Alerta do Sistema", "/admin"}
}

// LinhaContexto e' uma linha da tabela de dados. Lista, e nao mapa, porque a
// ordem importa: cliente vem antes de detalhe tecnico.
type LinhaContexto struct {
	Rotulo string
	Valor  string
}

// Alerta e' tudo que o template precisa.
type Alerta struct {
	Categoria string
	Titulo    string          // uma linha, sem o prefixo [URGENTE]
	CorpoHTML string          // texto dentro da barra vermelha
	Contexto  []LinhaContexto // vazio = a tabela some inteira
	Acao      string          // o que fazer
	BaseURL   string          // painel do UC Talk, pro botao
}

// Renderizar monta o HTML final.
func Renderizar(a Alerta) string {
	p := perfilDe(a.Categoria)
	destino := strings.TrimRight(a.BaseURL, "/") + p.rota
	agora := time.Now().Format("02/01/2006 15:04:05")
	ano := time.Now().Format("2006")

	var b strings.Builder
	b.WriteString(`<!DOCTYPE html>
<html lang="pt-BR">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>` + html.EscapeString(a.Titulo) + `</title>
  <style>
    body { margin:0 !important; padding:0 !important; background-color:#1a1a1a !important;
           font-family:'Segoe UI',Tahoma,Geneva,Verdana,sans-serif;
           -webkit-text-size-adjust:100%; -ms-text-size-adjust:100%; }
    table { border-collapse:collapse; mso-table-lspace:0pt; mso-table-rspace:0pt; }
    img { border:0; }
    a { text-decoration:none; }
  </style>
</head>
<body style="margin:0;padding:0;background-color:#1a1a1a;">
  <table width="100%" cellpadding="0" cellspacing="0" style="background-color:#1a1a1a;padding:40px 20px;">
    <tr>
      <td align="center">
        <table width="620" cellpadding="0" cellspacing="0"
               style="max-width:620px;width:100%;background-color:#2d2d2d;
                      border:1px solid #404040;border-radius:10px;overflow:hidden;">

          <tr>
            <td style="background-color:#dc2626;padding:10px 30px;text-align:center;">
              <span style="color:#ffffff;font-size:12px;font-weight:700;
                           letter-spacing:2px;text-transform:uppercase;">
                &#9888; Alerta Urgente &mdash; ` + html.EscapeString(p.setor) + `
              </span>
            </td>
          </tr>

          <tr>
            <td style="background-color:#2d2d2d;padding:32px 30px 22px;
                       text-align:center;border-bottom:1px solid #404040;">
              <a href="` + siteURL + `" target="_blank">
                <img src="` + logoURL + `" alt="UC Technology" width="170"
                     style="width:170px;max-width:170px;height:auto;
                            margin:0 auto 20px auto;display:block;">
              </a>
              <span style="display:inline-block;padding:8px 22px;border-radius:6px;
                           font-size:12px;font-weight:600;text-transform:uppercase;
                           letter-spacing:1px;color:#fca5a5;
                           background-color:#3a2222;border:1px solid #7f1d1d;">
                &#128308; ` + html.EscapeString(p.selo) + `
              </span>
              <h1 style="margin:20px 0 0;font-size:22px;font-weight:600;
                         color:#ffffff;letter-spacing:0.2px;line-height:1.35;">
                ` + html.EscapeString(a.Titulo) + `
              </h1>
            </td>
          </tr>

          <tr>
            <td style="background-color:#2d2d2d;padding:28px 35px 32px;">
              <table width="100%" cellpadding="0" cellspacing="0"
                     style="background-color:#242a31;border-left:4px solid #dc2626;border-radius:6px;">
                <tr>
                  <td style="padding:18px 20px;color:#e6e8ea;font-size:15px;line-height:1.7;">
                    ` + a.CorpoHTML + `
                  </td>
                </tr>
              </table>
`)

	b.WriteString(tabelaContexto(a.Contexto))

	b.WriteString(`
              <table width="100%" cellpadding="0" cellspacing="0" style="margin-top:24px;">
                <tr>
                  <td style="padding:16px 18px;background-color:#242a31;border-radius:8px;">
                    <div style="color:#9aa4ae;font-size:13px;line-height:1.9;">
                      <strong style="color:#e6e8ea;">Horário:</strong> ` + agora + `<br>
                      <strong style="color:#e6e8ea;">Ação recomendada:</strong> ` + html.EscapeString(a.Acao) + `
                    </div>
                  </td>
                </tr>
              </table>

              <table width="100%" cellpadding="0" cellspacing="0" style="margin-top:22px;">
                <tr>
                  <td align="center">
                    <a href="` + destino + `" target="_blank"
                       style="display:inline-block;padding:14px 34px;border-radius:8px;
                              background-color:#dc2626;color:#ffffff;font-size:15px;
                              font-weight:600;letter-spacing:0.3px;">
                      Acessar o painel &rarr;
                    </a>
                    <div style="margin-top:12px;font-size:12px;color:#7a828a;
                                word-break:break-all;">
                      ou copie o link: <a href="` + destino + `" target="_blank"
                         style="color:#9aa4ae;">` + destino + `</a>
                    </div>
                  </td>
                </tr>
              </table>
            </td>
          </tr>

          <tr>
            <td style="background-color:#242424;padding:20px 30px;text-align:center;
                       border-top:1px solid #404040;">
              <div style="color:#8a8a8a;font-size:12px;line-height:1.6;">
                UC Technology &mdash; Central de Monitoramento e Ferramentas<br>
                Este é um e-mail automático. Não é necessário respondê-lo.
              </div>
              <div style="color:#5a5a5a;font-size:11px;margin-top:8px;">
                &copy; ` + ano + ` UC Technology
              </div>
            </td>
          </tr>

        </table>
      </td>
    </tr>
  </table>
</body>
</html>`)

	return b.String()
}

// tabelaContexto some inteira quando nao ha dados — tabela vazia com bordas
// so' ocuparia espaco e daria a impressao de informacao faltando.
func tabelaContexto(linhas []LinhaContexto) string {
	if len(linhas) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`
              <table width="100%" cellpadding="0" cellspacing="0"
                     style="margin-top:20px;border:1px solid #3a3a3a;border-radius:8px;
                            overflow:hidden;background-color:#242a31;">`)
	for _, l := range linhas {
		b.WriteString(fmt.Sprintf(`
                <tr>
                  <td style="padding:10px 16px;border-bottom:1px solid #3a3a3a;
                             color:#9aa4ae;font-size:13px;font-weight:600;white-space:nowrap;">%s</td>
                  <td style="padding:10px 16px;border-bottom:1px solid #3a3a3a;
                             color:#e6e8ea;font-size:14px;font-weight:600;">%s</td>
                </tr>`, html.EscapeString(l.Rotulo), html.EscapeString(l.Valor)))
	}
	b.WriteString(`
              </table>`)
	return b.String()
}
