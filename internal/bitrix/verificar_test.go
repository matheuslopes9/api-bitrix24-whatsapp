package bitrix

import (
	"net"
	"testing"
)

// O dominio vem de quem chama e e' pra ele que o servidor faz a requisicao.
// Qualquer coisa que nao seja um host publico vira SSRF pra dentro da rede.
func TestDominioSeguro(t *testing.T) {
	validos := map[string]string{
		"crm.uctechnology.com.br":          "crm.uctechnology.com.br",
		"https://Empresa.Bitrix24.com.br/": "empresa.bitrix24.com.br",
		"  b24-abc123.bitrix24.com.br ":    "b24-abc123.bitrix24.com.br",
	}
	for in, esperado := range validos {
		got, err := DominioSeguro(in)
		if err != nil || got != esperado {
			t.Errorf("DominioSeguro(%q) = %q, %v; esperado %q", in, got, err, esperado)
		}
	}
	for _, in := range []string{
		"", "localhost", "redis", "redis:6379", "10.0.0.5", "127.0.0.1",
		"[::1]", "evil.com/../x", "evil.com?x=1", "evil.com#a", "user@evil.com",
		"evil..com", ".evil.com", "evil.com.", "exa mple.com",
	} {
		if got, err := DominioSeguro(in); err == nil {
			t.Errorf("DominioSeguro(%q) aceitou: %q", in, got)
		}
	}
}

func TestIPPublico(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "0.0.0.0", "::1", "fe80::1", "fd00::1"} {
		if ipPublico(net.ParseIP(s)) {
			t.Errorf("%s tratado como publico", s)
		}
	}
	for _, s := range []string{"187.110.174.112", "8.8.8.8", "2001:4860:4860::8888"} {
		if !ipPublico(net.ParseIP(s)) {
			t.Errorf("%s tratado como interno", s)
		}
	}
}
