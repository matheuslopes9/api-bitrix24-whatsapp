package dependencias

import (
	"time"
)

// versaoMeta e' uma linha do changelog da Graph API.
type versaoMeta struct {
	versao     string
	lancamento string // AAAA-MM-DD
	fimDeLinha string // AAAA-MM-DD; vazio = ainda nao definido pela Meta
}

// versoesMeta vem de https://developers.facebook.com/docs/graph-api/changelog
// (lido em 21/10/2026). A Meta nao publica isso em API, entao a tabela e'
// mantida aqui: quando sair versao nova, acrescente a linha no topo — o teste
// TestTabelaMetaEmOrdem confere a ordem.
//
// Regra da Meta: cada versao vale ate' ~2 anos depois do lancamento da
// SEGUINTE. Vencida, a chamada e' redirecionada pra versao mais antiga ainda
// ativa — nao da' erro, so' muda o comportamento calado.
var versoesMeta = []versaoMeta{
	{"v26.0", "2026-07-29", ""},
	{"v25.0", "2026-02-18", "2028-07-29"},
	{"v24.0", "2025-10-08", "2028-02-18"},
	{"v23.0", "2025-05-29", "2027-10-08"},
	{"v22.0", "2025-01-21", "2027-05-20"},
	{"v21.0", "2024-10-02", "2027-01-21"},
	{"v20.0", "2024-05-21", "2026-09-24"},
	{"v19.0", "2024-01-23", "2026-05-21"},
	{"v18.0", "2023-09-12", "2026-01-26"},
	{"v17.0", "2023-05-23", "2025-09-12"},
}

// avisoMeta: com quanto tempo de antecedencia a versao em uso vira
// "atualizar". Migrar versao de API pede teste com numero oficial — tempo de
// sobra pra isso, sem virar correria.
const avisoMeta = 180 * 24 * time.Hour

func (v *Verificador) metaGraph() Item {
	emUso := ""
	if v.versaoGraph != nil {
		emUso = v.versaoGraph()
	}
	return avaliarMeta(emUso, v.agora())
}

func avaliarMeta(emUso string, agora time.Time) Item {
	it := Item{
		Nome: "Graph API da Meta", Categoria: "API externa",
		ParaQue: "Envio e recebimento pela API oficial do WhatsApp (Cloud API) e templates",
		Critico: true, EmUso: emUso, MaisNova: versoesMeta[0].versao,
		Fonte: "https://developers.facebook.com/docs/graph-api/changelog",
	}
	var atual *versaoMeta
	for i := range versoesMeta {
		if versoesMeta[i].versao == emUso {
			atual = &versoesMeta[i]
			break
		}
	}
	if atual == nil {
		it.Status = Desconhecido
		it.Detalhe = "Versão " + emUso + " não está na tabela de datas (internal/dependencias/meta.go). " +
			"Confira o changelog da Meta e acrescente a linha."
		return it
	}
	if atual.fimDeLinha == "" {
		it.Status, it.Detalhe = EmDia, "Versão em uso ainda sem data de fim de linha definida pela Meta."
		return it
	}
	fim, _ := time.Parse("2006-01-02", atual.fimDeLinha)
	it.FimSuporte = &fim
	switch {
	case agora.After(fim):
		it.Status = Critico
		it.Detalhe = "Versão fora de linha desde " + fim.Format("02/01/2006") + ". A Meta redireciona as chamadas " +
			"para outra versão, com comportamento diferente. Troque META_GRAPH_API_VERSION (ou o padrão em " +
			"internal/whatsapp/cloud.go) e teste envio e templates com um número oficial."
	case fim.Sub(agora) < avisoMeta:
		it.Status = Atualizar
		it.Detalhe = "Fim de linha em " + fim.Format("02/01/2006") + ". Planeje a troca de versão e o teste com número oficial."
	default:
		it.Status, it.Detalhe = EmDia, "Válida até "+fim.Format("02/01/2006")+"."
	}
	return it
}
