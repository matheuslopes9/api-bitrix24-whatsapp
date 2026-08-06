package gateway

import (
	"encoding/json"
	"fmt"
)

// Notificacao e' um pagamento PIX recebido, como o Itau envia no webhook.
// Formato confirmado na doc "Webhook PIX" (secao 4 — exemplo de retorno):
//
//	{"endToEndId":"E1923...","txid":"0bpb...","valor":29.45,
//	 "horario":"2026-01-21T13:37:00.043Z","infoPagador":"",
//	 "chave":"chave@exemplo.com","componentesValor":null,"devolucoes":null}
//
// O Itau pode mandar UM objeto ou um lote sob a chave "pix": [...]. ParseWebhook
// normaliza os dois casos numa lista.
type Notificacao struct {
	EndToEndID  string `json:"endToEndId"`
	Txid        string `json:"txid"`   // reconcilia com a cobranca que criamos
	Valor       string `json:"-"`      // normalizado pra string "29.45" (ver UnmarshalJSON)
	Horario     string `json:"horario"`
	InfoPagador string `json:"infoPagador"`
	Chave       string `json:"chave"`
}

// UnmarshalJSON trata "valor" que pode vir como numero (29.45) OU string
// ("29.45") dependendo do endpoint. Normaliza sempre pra string 2-casas.
func (n *Notificacao) UnmarshalJSON(b []byte) error {
	type alias struct {
		EndToEndID  string          `json:"endToEndId"`
		Txid        string          `json:"txid"`
		Valor       json.RawMessage `json:"valor"`
		Horario     string          `json:"horario"`
		InfoPagador string          `json:"infoPagador"`
		Chave       string          `json:"chave"`
	}
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	n.EndToEndID = a.EndToEndID
	n.Txid = a.Txid
	n.Horario = a.Horario
	n.InfoPagador = a.InfoPagador
	n.Chave = a.Chave

	// valor: numero ou string.
	if len(a.Valor) > 0 {
		s := string(a.Valor)
		if len(s) >= 2 && s[0] == '"' {
			// string JSON — tira as aspas.
			var vs string
			if err := json.Unmarshal(a.Valor, &vs); err == nil {
				n.Valor = vs
			}
		} else {
			// numero — formata com 2 casas.
			var vf float64
			if err := json.Unmarshal(a.Valor, &vf); err == nil {
				n.Valor = fmt.Sprintf("%.2f", vf)
			}
		}
	}
	return nil
}

// ParseWebhook desserializa o corpo do POST do Itau numa lista de notificacoes.
// Aceita objeto unico, {"pix":[...]}, ou lista crua [...]. Retorna so' as que
// tem txid (as que conseguimos reconciliar com uma cobranca nossa).
func ParseWebhook(body []byte) ([]Notificacao, error) {
	// 1. Envelope {"pix":[...]}
	var env struct {
		Pix []Notificacao `json:"pix"`
	}
	if err := json.Unmarshal(body, &env); err == nil && len(env.Pix) > 0 {
		return filtrarComTxid(env.Pix), nil
	}

	// 2. Lista crua [...]
	var lista []Notificacao
	if err := json.Unmarshal(body, &lista); err == nil && len(lista) > 0 {
		return filtrarComTxid(lista), nil
	}

	// 3. Objeto unico
	var uma Notificacao
	if err := json.Unmarshal(body, &uma); err == nil && uma.EndToEndID != "" {
		return filtrarComTxid([]Notificacao{uma}), nil
	}

	return nil, fmt.Errorf("itau webhook: corpo nao reconhecido")
}

func filtrarComTxid(ns []Notificacao) []Notificacao {
	out := ns[:0]
	for _, n := range ns {
		if n.Txid != "" {
			out = append(out, n)
		}
	}
	return out
}
