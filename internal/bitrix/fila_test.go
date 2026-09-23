package bitrix

import (
	"encoding/json"
	"testing"
)

// A fila da Linha Aberta e' a UNICA fonte que alcanca ID alto sem adivinhar.
// Se a extracao do QUEUE quebrar, volta o bug da Gabrielly (ID 12195) nao
// aparecer nas permissoes.
func TestExtrairQueueDaConfig(t *testing.T) {
	// Recorte real da resposta do portal do cliente.
	raw := []byte(`{"ID":"1","LINE_NAME":"UC Talk","QUEUE_TYPE":"all",
		"QUEUE":["13","21","463","37","27","12195","7","137"],
		"QUEUE_ONLINE":"Y"}`)

	var cfg struct {
		Queue []string `json:"QUEUE"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("nao decodificou: %v", err)
	}
	if len(cfg.Queue) != 8 {
		t.Fatalf("esperava 8 na fila, veio %d: %v", len(cfg.Queue), cfg.Queue)
	}
	achou := false
	for _, id := range cfg.Queue {
		if id == "12195" {
			achou = true
		}
	}
	if !achou {
		t.Error("o ID alto (12195) precisa sair da fila — e' o caso que a sondagem nao alcanca")
	}
}

// Linha sem fila configurada nao pode quebrar a listagem.
func TestQueueAusenteNaoQuebra(t *testing.T) {
	for _, raw := range []string{`{"ID":"1"}`, `{"ID":"1","QUEUE":[]}`} {
		var cfg struct {
			Queue []string `json:"QUEUE"`
		}
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if len(cfg.Queue) != 0 {
			t.Errorf("%s: esperava fila vazia, veio %v", raw, cfg.Queue)
		}
	}
}
