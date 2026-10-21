package dependencias

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"
)

// Ciclo e' uma linha de endoflife.date (ex.: Go 1.26, Alpine 3.19).
type Ciclo struct {
	Ciclo      string
	Ultima     string
	FimSuporte *time.Time // nil = sem data definida
}

// fontes isola a internet, pra o teste nao depender dela.
type fontes struct {
	ultimaVersaoGo   func(ctx context.Context, modulo string) (string, time.Time, error)
	ciclos           func(ctx context.Context, produto string) ([]Ciclo, error)
	ultimaVersaoPyPI func(ctx context.Context, pacote string) (string, error)
}

func fontesHTTP() fontes {
	cli := &http.Client{Timeout: 15 * time.Second}
	return fontes{
		ultimaVersaoGo: func(ctx context.Context, modulo string) (string, time.Time, error) {
			var r struct {
				Version string
				Time    time.Time
			}
			err := getJSON(ctx, cli, "https://proxy.golang.org/"+escaparModulo(modulo)+"/@latest", &r)
			return r.Version, r.Time, err
		},
		ciclos: func(ctx context.Context, produto string) ([]Ciclo, error) {
			var brutos []struct {
				Cycle  string          `json:"cycle"`
				Latest string          `json:"latest"`
				EOL    json.RawMessage `json:"eol"`
			}
			if err := getJSON(ctx, cli, "https://endoflife.date/api/"+produto+".json", &brutos); err != nil {
				return nil, err
			}
			out := make([]Ciclo, 0, len(brutos))
			for _, b := range brutos {
				out = append(out, Ciclo{Ciclo: b.Cycle, Ultima: b.Latest, FimSuporte: dataEOL(b.EOL)})
			}
			return out, nil
		},
		ultimaVersaoPyPI: func(ctx context.Context, pacote string) (string, error) {
			var r struct {
				Info struct {
					Version string `json:"version"`
				} `json:"info"`
			}
			err := getJSON(ctx, cli, "https://pypi.org/pypi/"+pacote+"/json", &r)
			return r.Info.Version, err
		},
	}
}

func getJSON(ctx context.Context, cli *http.Client, url string, destino any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "uctalk-dependencias")
	resp, err := cli.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s respondeu %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(destino)
}

// escaparModulo segue a regra do proxy de modulos do Go: maiuscula vira "!"
// + minuscula ("HugoSmits86" -> "!hugo!smits86"). Sem isso, modulo com
// maiuscula no caminho da' 404.
func escaparModulo(m string) string {
	var b strings.Builder
	for _, r := range m {
		if unicode.IsUpper(r) {
			b.WriteByte('!')
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// dataEOL: o campo "eol" do endoflife.date e' uma data ("2025-11-01") ou um
// booleano (false = sem fim definido, true = ja' acabou, sem data).
func dataEOL(raw json.RawMessage) *time.Time {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			return &t
		}
		return nil
	}
	var acabou bool
	if json.Unmarshal(raw, &acabou) == nil && acabou {
		t := time.Unix(0, 0)
		return &t
	}
	return nil
}
