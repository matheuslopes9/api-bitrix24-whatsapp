// Package dependencias diz o que o UC Talk usa, em que versao, e o que precisa
// de atencao — pra o time ser avisado ANTES de uma biblioteca velha ou uma
// versao vencida da Meta virar incidente.
//
// O PORQUE: em 21/10/2026 o envio pela Cloud API usava a Graph API v20.0, fora
// de linha havia quase um mes, e a listagem de templates estava fixa em v18.0,
// vencida desde janeiro. A imagem base (Alpine 3.19) estava sem suporte havia
// um ano. Nada disso dava erro: a Meta redireciona versao vencida e a imagem
// continua subindo. So' aparecia pra quem fosse procurar.
//
// De onde vem cada dado:
//
//   - versao EM USO: do proprio binario (runtime/debug.ReadBuildInfo) e do
//     ambiente (Alpine, PostgreSQL, Redis). Nada de lista mantida a mao que
//     envelhece calada.
//   - versao MAIS NOVA e FIM DE SUPORTE: proxy.golang.org, endoflife.date e
//     PyPI — APIs publicas, sem chave.
//   - API da Meta: a Meta nao publica as datas em API; ficam em meta.go,
//     copiadas do changelog oficial.
package dependencias

import (
	"context"
	"fmt"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"
)

// Status de um item, do mais grave pro mais tranquilo.
type Status string

const (
	Critico      Status = "critico"      // fora de suporte, vencido, ou atraso que ja' e' risco
	Atualizar    Status = "atualizar"    // ha' versao mais nova, ou o fim de suporte esta' perto
	EmDia        Status = "em_dia"       // nada a fazer
	Desconhecido Status = "desconhecido" // nao deu pra consultar agora
)

// Item e' uma linha da tela.
type Item struct {
	Nome       string     `json:"nome"`
	Categoria  string     `json:"categoria"` // "Biblioteca Go", "Plataforma", "API externa", "Serviço de e-mail"
	ParaQue    string     `json:"para_que"`
	Critico    bool       `json:"critico"` // quebra o atendimento se der problema
	EmUso      string     `json:"em_uso"`
	MaisNova   string     `json:"mais_nova,omitempty"`
	FimSuporte *time.Time `json:"fim_suporte,omitempty"`
	Status     Status     `json:"status"`
	Detalhe    string     `json:"detalhe"` // o que fazer, em portugues
	Fonte      string     `json:"fonte,omitempty"`
}

// Relatorio e' a ultima verificacao.
type Relatorio struct {
	Itens        []Item    `json:"itens"`
	VerificadoEm time.Time `json:"verificado_em"`
	Criticos     int       `json:"criticos"`
	Atualizar    int       `json:"atualizar"`
}

// Ambiente devolve as versoes que so' o processo em execucao sabe. Vazio =
// nao deu pra saber (ex.: rodando fora do container, banco fora do ar).
type Ambiente interface {
	VersaoPostgres(ctx context.Context) string
	VersaoRedis(ctx context.Context) string
	VersaoAlpine() string
}

// Verificador junta tudo. Guarda o ultimo relatorio pra tela nao disparar
// consulta externa a cada abertura.
type Verificador struct {
	fontes      fontes
	ambiente    Ambiente
	agora       func() time.Time
	versaoGraph func() string

	mu     sync.RWMutex
	ultimo *Relatorio
}

// Novo monta o verificador com as fontes reais (internet).
func Novo(amb Ambiente, versaoGraph func() string) *Verificador {
	return &Verificador{fontes: fontesHTTP(), ambiente: amb, agora: time.Now, versaoGraph: versaoGraph}
}

// Ultimo devolve o relatorio guardado, ou nil se nunca verificou.
func (v *Verificador) Ultimo() *Relatorio {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.ultimo
}

// Verificar consulta tudo agora e guarda o resultado.
func (v *Verificador) Verificar(ctx context.Context) *Relatorio {
	var itens []Item
	itens = append(itens, v.bibliotecasGo(ctx)...)
	itens = append(itens, v.plataforma(ctx)...)
	itens = append(itens, v.metaGraph())
	itens = append(itens, v.servicoEmail(ctx)...)

	ordem := map[Status]int{Critico: 0, Atualizar: 1, Desconhecido: 2, EmDia: 3}
	sort.SliceStable(itens, func(i, j int) bool {
		if ordem[itens[i].Status] != ordem[itens[j].Status] {
			return ordem[itens[i].Status] < ordem[itens[j].Status]
		}
		return itens[i].Critico && !itens[j].Critico
	})
	r := &Relatorio{Itens: itens, VerificadoEm: v.agora()}
	for _, it := range itens {
		switch it.Status {
		case Critico:
			r.Criticos++
		case Atualizar:
			r.Atualizar++
		}
	}
	v.mu.Lock()
	v.ultimo = r
	v.mu.Unlock()
	return r
}

// ─── Bibliotecas Go ─────────────────────────────────────────────────────

// moduloGo e' o que o time precisa saber de cada dependencia direta. A
// VERSAO nao fica aqui — vem do binario.
type moduloGo struct {
	caminho string
	paraQue string
	critico bool
	// atrasoCritico: a partir de quanto atraso a falta de atualizacao vira
	// risco. So' o whatsmeow tem: o WhatsApp muda o protocolo, e versao velha
	// e' recusada com 405 — derruba TODOS os numeros de uma vez.
	atrasoCritico time.Duration
}

var modulosGo = []moduloGo{
	{"go.mau.fi/whatsmeow", "Conexão com o WhatsApp (modo QR Code)", true, 60 * 24 * time.Hour},
	{"github.com/gofiber/fiber/v2", "Servidor web: webhooks do Bitrix, painéis e aba do CRM", true, 0},
	{"github.com/valyala/fasthttp", "Base HTTP do Fiber", true, 0},
	{"github.com/jackc/pgx/v5", "Banco de dados PostgreSQL", true, 0},
	{"github.com/redis/go-redis/v9", "Filas de mensagens (Redis)", true, 0},
	{"github.com/mattn/go-sqlite3", "Armazena as sessões do WhatsApp (QR)", true, 0},
	{"google.golang.org/protobuf", "Formato das mensagens do WhatsApp", true, 0},
	{"golang.org/x/crypto", "Criptografia (senhas do painel, assinaturas)", true, 0},
	{"go.uber.org/zap", "Logs", false, 0},
	{"github.com/prometheus/client_golang", "Métricas (/metrics)", false, 0},
	{"github.com/spf13/viper", "Leitura da configuração (variáveis de ambiente)", false, 0},
	{"github.com/google/uuid", "Identificadores", false, 0},
	{"github.com/HugoSmits86/nativewebp", "Converte figurinha PNG para WebP", false, 0},
	{"golang.org/x/image", "Leitura de imagens para figurinhas", false, 0},
	{"github.com/xuri/excelize/v2", "Exportação de relatórios em Excel", false, 0},
	{"github.com/mdp/qrterminal/v3", "QR Code no terminal (diagnóstico)", false, 0},
	{"rsc.io/qr", "Geração do QR Code de pareamento", false, 0},
}

// versoesNoBinario le as versoes das dependencias compiladas neste binario.
// Variavel pra teste.
var versoesNoBinario = func() map[string]string {
	out := map[string]string{}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return out
	}
	for _, d := range info.Deps {
		v := d.Version
		if d.Replace != nil {
			v = d.Replace.Version
		}
		out[d.Path] = v
	}
	return out
}

func (v *Verificador) bibliotecasGo(ctx context.Context) []Item {
	emUso := versoesNoBinario()
	itens := make([]Item, len(modulosGo))
	var wg sync.WaitGroup
	for i, m := range modulosGo {
		wg.Add(1)
		go func(i int, m moduloGo) {
			defer wg.Done()
			itens[i] = v.itemGo(ctx, m, emUso[m.caminho])
		}(i, m)
	}
	wg.Wait()
	itens = append(itens, v.itemLinguagemGo(ctx))
	return itens
}

func (v *Verificador) itemGo(ctx context.Context, m moduloGo, emUso string) Item {
	it := Item{
		Nome: m.caminho, Categoria: "Biblioteca Go", ParaQue: m.paraQue, Critico: m.critico,
		EmUso: emUso, Fonte: "https://pkg.go.dev/" + m.caminho,
	}
	if emUso == "" {
		it.Status, it.Detalhe = Desconhecido, "Versão em uso não encontrada no binário (rodando fora do build de produção?)."
		return it
	}
	ultima, quando, err := v.fontes.ultimaVersaoGo(ctx, m.caminho)
	if err != nil {
		it.Status, it.Detalhe = Desconhecido, "Não foi possível consultar proxy.golang.org: "+err.Error()
		return it
	}
	it.MaisNova = ultima
	if ultima == emUso {
		it.Status, it.Detalhe = EmDia, "Na versão mais nova."
		return it
	}
	it.Status = Atualizar
	it.Detalhe = "Há versão mais nova. Atualize com: go get " + m.caminho + "@latest && go mod tidy — e rode os testes."
	if m.atrasoCritico > 0 {
		if usada, ok := dataDaPseudoVersao(emUso); ok && !quando.IsZero() && quando.Sub(usada) > m.atrasoCritico {
			it.Status = Critico
			it.Detalhe = "Versão em uso tem mais de " + diasTexto(m.atrasoCritico) + " de atraso. O WhatsApp muda o protocolo " +
				"e recusa cliente antigo (erro 405), derrubando TODOS os números de uma vez. " + it.Detalhe
		}
	}
	return it
}

// dataDaPseudoVersao le a data de uma pseudo-versao Go
// (v0.0.0-20260928140511-35f522c88ce3). O whatsmeow so' publica assim.
func dataDaPseudoVersao(v string) (time.Time, bool) {
	partes := strings.Split(v, "-")
	if len(partes) < 3 {
		return time.Time{}, false
	}
	ts := partes[len(partes)-2]
	if i := strings.LastIndex(ts, "."); i >= 0 { // v1.2.3-0.20260928140511-hash
		ts = ts[i+1:]
	}
	t, err := time.Parse("20060102150405", ts)
	return t, err == nil
}

func diasTexto(d time.Duration) string {
	return fmt.Sprintf("%d dias", int(d.Hours()/24))
}

func (v *Verificador) itemLinguagemGo(ctx context.Context) Item {
	emUso := strings.TrimPrefix(runtime.Version(), "go")
	return v.itemCiclo(ctx, Item{
		Nome: "Go", Categoria: "Plataforma", ParaQue: "Linguagem e compilador do UC Talk (imagem golang no Dockerfile)",
		Critico: true, EmUso: emUso, Fonte: "https://endoflife.date/go",
	}, "go", cicloMenor(emUso))
}

// ─── Plataforma ─────────────────────────────────────────────────────────

func (v *Verificador) plataforma(ctx context.Context) []Item {
	var itens []Item
	alpine := ""
	pg, rd := "", ""
	if v.ambiente != nil {
		alpine = v.ambiente.VersaoAlpine()
		pg = v.ambiente.VersaoPostgres(ctx)
		rd = v.ambiente.VersaoRedis(ctx)
	}
	itens = append(itens, v.itemCiclo(ctx, Item{
		Nome: "Alpine Linux", Categoria: "Plataforma", ParaQue: "Sistema base da imagem Docker do UC Talk",
		Critico: true, EmUso: alpine, Fonte: "https://endoflife.date/alpine-linux",
	}, "alpine-linux", cicloMenor(alpine)))
	itens = append(itens, v.itemCiclo(ctx, Item{
		Nome: "PostgreSQL", Categoria: "Plataforma", ParaQue: "Banco de dados (mensagens, clientes, licenças)",
		Critico: true, EmUso: pg, Fonte: "https://endoflife.date/postgresql",
	}, "postgresql", cicloMaior(pg)))
	itens = append(itens, v.itemCiclo(ctx, Item{
		Nome: "Redis", Categoria: "Plataforma", ParaQue: "Filas de envio e recebimento",
		Critico: true, EmUso: rd, Fonte: "https://endoflife.date/redis",
	}, "redis", cicloMenor(rd)))
	return itens
}

// itemCiclo avalia algo que tem ciclos com fim de suporte (endoflife.date).
func (v *Verificador) itemCiclo(ctx context.Context, it Item, produto, ciclo string) Item {
	if it.EmUso == "" || ciclo == "" {
		it.Status, it.Detalhe = Desconhecido, "Versão em uso não identificada neste ambiente."
		return it
	}
	ciclos, err := v.fontes.ciclos(ctx, produto)
	if err != nil {
		it.Status, it.Detalhe = Desconhecido, "Não foi possível consultar endoflife.date: "+err.Error()
		return it
	}
	return avaliarCiclo(it, ciclos, ciclo, v.agora())
}

// avaliarCiclo: fora de suporte = critico; suporte acabando em 90 dias ou
// correcao nova no mesmo ciclo = atualizar. Versao principal nova sozinha nao
// e' urgencia — so' informa.
func avaliarCiclo(it Item, ciclos []Ciclo, ciclo string, agora time.Time) Item {
	if len(ciclos) > 0 {
		it.MaisNova = ciclos[0].Ultima
	}
	var atual *Ciclo
	for i := range ciclos {
		if ciclos[i].Ciclo == ciclo {
			atual = &ciclos[i]
			break
		}
	}
	if atual == nil {
		it.Status, it.Detalhe = Desconhecido, "Ciclo "+ciclo+" não encontrado em endoflife.date."
		return it
	}
	it.FimSuporte = atual.FimSuporte
	switch {
	case atual.FimSuporte != nil && agora.After(*atual.FimSuporte):
		it.Status = Critico
		it.Detalhe = "Fora de suporte desde " + atual.FimSuporte.Format("02/01/2006") +
			": sem correções de segurança. Migre para a versão " + it.MaisNova + " (ou outro ciclo com suporte)."
	case atual.FimSuporte != nil && atual.FimSuporte.Sub(agora) < 90*24*time.Hour:
		it.Status = Atualizar
		it.Detalhe = "O suporte acaba em " + atual.FimSuporte.Format("02/01/2006") + ". Planeje a migração."
	case atual.Ultima != "" && atual.Ultima != it.EmUso && strings.Count(it.EmUso, ".") >= 2:
		it.Status = Atualizar
		it.Detalhe = "Há correção nova no mesmo ciclo: " + atual.Ultima + "."
	default:
		it.Status, it.Detalhe = EmDia, "Com suporte."
		if it.MaisNova != "" && !strings.HasPrefix(it.MaisNova, ciclo) {
			it.Detalhe += " Existe versão principal mais nova (" + it.MaisNova + "), sem urgência."
		}
	}
	return it
}

// cicloMenor: "1.26.3" -> "1.26"; "3.19.9" -> "3.19".
func cicloMenor(v string) string {
	p := strings.Split(strings.TrimSpace(v), ".")
	if len(p) < 2 {
		return ""
	}
	return p[0] + "." + p[1]
}

// cicloMaior: "16.4" -> "16" (PostgreSQL numera o ciclo pelo primeiro numero).
func cicloMaior(v string) string {
	p := strings.Split(strings.TrimSpace(v), ".")
	if p[0] == "" {
		return ""
	}
	return p[0]
}

// ─── Servico de e-mail (proxy OAuth2) ──────────────────────────────────

// As versoes do proxy de e-mail ficam declaradas aqui porque ele e' OUTRO
// container: o UC Talk nao consegue perguntar a ele. O teste
// TestVersoesDoProxyBatemComOsArquivos confere contra o Dockerfile e o
// requirements.txt — mudou la' e esqueceu aqui, o teste quebra.
const versaoPythonProxy = "3.12"

var pacotesPythonProxy = []struct{ nome, versao, paraQue string }{
	{"msal", "1.34.0", "Autenticação OAuth2 com o Microsoft 365"},
	{"aiosmtpd", "1.4.6", "Servidor SMTP do proxy"},
	{"python-dotenv", "1.2.2", "Configuração do proxy"},
}

func (v *Verificador) servicoEmail(ctx context.Context) []Item {
	itens := []Item{v.itemCiclo(ctx, Item{
		Nome: "Python (proxy de e-mail)", Categoria: "Serviço de e-mail",
		ParaQue: "Base do serviço que envia os alertas pelo Microsoft 365",
		EmUso:   versaoPythonProxy, Fonte: "https://endoflife.date/python",
	}, "python", versaoPythonProxy)}
	for _, p := range pacotesPythonProxy {
		it := Item{
			Nome: p.nome, Categoria: "Serviço de e-mail", ParaQue: p.paraQue,
			EmUso: p.versao, Fonte: "https://pypi.org/project/" + p.nome,
		}
		ultima, err := v.fontes.ultimaVersaoPyPI(ctx, p.nome)
		switch {
		case err != nil:
			it.Status, it.Detalhe = Desconhecido, "Não foi possível consultar o PyPI: "+err.Error()
		case ultima == p.versao:
			it.MaisNova, it.Status, it.Detalhe = ultima, EmDia, "Na versão mais nova."
		default:
			it.MaisNova, it.Status = ultima, Atualizar
			it.Detalhe = "Há versão mais nova. Atualize em tools/oauth2-email-service/requirements.txt e refaça o deploy do serviço de e-mail."
		}
		itens = append(itens, it)
	}
	return itens
}
