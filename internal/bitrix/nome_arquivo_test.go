package bitrix

import "testing"

func TestNomeParaBitrix(t *testing.T) {
	casos := map[string]string{
		"relatorio.pdf": "relatorio.pdf",
		"Arena _ Benchmark & Compare the Best AI Models.pdf": "Arena _ Benchmark e Compare the Best AI Models.pdf",
		// O caso real: o comeco (que identifica o arquivo) tem que ficar.
		"PSE-SystemLog-83.21.0.117-beta1-download-20260331195609-QbCLXR9X2G0A9WkY.tar": "PSE-SystemLog-83.21.0.117-beta1-download-2026….tar",
	}
	for in, esperado := range casos {
		got := NomeParaBitrix(in)
		if got != esperado {
			t.Errorf("NomeParaBitrix(%q) = %q, esperado %q", in, got, esperado)
		}
		if n := len([]rune(got)); n > maxNomeArquivoBitrix {
			t.Errorf("%q tem %d caracteres (max %d)", got, n, maxNomeArquivoBitrix)
		}
	}
}
