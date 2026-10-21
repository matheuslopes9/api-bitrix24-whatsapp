package whatsapp

import "testing"

// A versao da Meta vem da env quando valida; typo cai no padrao em vez de
// derrubar todo envio da Cloud API.
func TestVersaoGraphAPI(t *testing.T) {
	casos := map[string]string{
		"":          defaultCloudAPIVersion,
		"v26.0":     "v26.0",
		" v24.0 ":   "v24.0",
		"26.0":      defaultCloudAPIVersion,
		"v26":       defaultCloudAPIVersion,
		"latest":    defaultCloudAPIVersion,
		"v25.0/../": defaultCloudAPIVersion,
	}
	for env, quer := range casos {
		t.Setenv("META_GRAPH_API_VERSION", env)
		if got := VersaoGraphAPI(); got != quer {
			t.Errorf("env %q: veio %q, queria %q", env, got, quer)
		}
	}
	if defaultCloudAPIVersion != "v25.0" {
		t.Errorf("padrao mudou para %s — confira a data de fim de linha na Meta antes", defaultCloudAPIVersion)
	}
}
