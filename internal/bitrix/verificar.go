package bitrix

// verificar.go — prova de que quem chama tem um token VALIDO no portal.
//
// O PORQUE: /bitrix/auth, /bitrix/install e /bitrix/callback aceitavam
// {domain, access_token} e gravavam o token por cima do que existia, alem de
// emitir o cookie de tenant. Nada conferia o token. Qualquer um mandava
// {domain: <vitima>, access_token: "x"} e recebia o cookie da vitima — que e'
// o que abre todo o /ui/* — e ainda derrubava a integracao dela, porque o
// token real era trocado por lixo.
//
// A prova e' o proprio portal: chamamos /rest/profile NO DOMINIO informado,
// com o token informado. So' o portal verdadeiro aceita um token dele.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// IdentidadeBitrix e' quem o portal diz que o token pertence.
type IdentidadeBitrix struct {
	Dominio string
	UserID  string
	Admin   bool
}

// ErrTokenRecusado: o portal respondeu, mas nao aceitou o token.
var ErrTokenRecusado = errors.New("token recusado pelo portal")

// VerificarToken confirma o token no portal e devolve o usuario dono dele.
func VerificarToken(ctx context.Context, dominio, accessToken string) (*IdentidadeBitrix, error) {
	dominio, err := DominioSeguro(dominio)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(accessToken) == "" {
		return nil, fmt.Errorf("%w: token vazio", ErrTokenRecusado)
	}
	u := "https://" + dominio + "/rest/profile.json?auth=" + url.QueryEscape(accessToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := clientePara(dominio).Do(req)
	if err != nil {
		// Nao incluir a URL no erro: ela carrega o token.
		return nil, fmt.Errorf("portal %s inacessivel", dominio)
	}
	defer resp.Body.Close()
	corpo, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	var r struct {
		Result struct {
			ID    json.Number `json:"ID"`
			Admin bool        `json:"ADMIN"`
		} `json:"result"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(corpo, &r); err != nil {
		return nil, fmt.Errorf("%w: resposta invalida do portal (HTTP %d)", ErrTokenRecusado, resp.StatusCode)
	}
	if r.Error != "" || resp.StatusCode != http.StatusOK || r.Result.ID.String() == "" {
		motivo := r.Error
		if motivo == "" {
			motivo = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("%w: %s", ErrTokenRecusado, motivo)
	}
	return &IdentidadeBitrix{Dominio: dominio, UserID: r.Result.ID.String(), Admin: r.Result.Admin}, nil
}

// DominioSeguro normaliza e recusa o que nao pode ser um portal.
//
// O dominio vem de quem chama e e' pra ele que o servidor faz a requisicao.
// Sem esta checagem, "localhost", "redis:6379" ou "10.0.0.5" viravam SSRF
// pra dentro da rede do EasyPanel.
func DominioSeguro(d string) (string, error) {
	d = strings.ToLower(strings.TrimSpace(d))
	d = strings.TrimPrefix(strings.TrimPrefix(d, "https://"), "http://")
	d = strings.TrimRight(d, "/")
	if d == "" || len(d) > 253 {
		return "", fmt.Errorf("dominio invalido")
	}
	for _, r := range d {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '.') {
			return "", fmt.Errorf("dominio invalido: %q", d)
		}
	}
	if !strings.Contains(d, ".") || strings.HasPrefix(d, ".") || strings.HasSuffix(d, ".") || strings.Contains(d, "..") {
		return "", fmt.Errorf("dominio invalido: %q", d)
	}
	if net.ParseIP(d) != nil {
		return "", fmt.Errorf("dominio nao pode ser IP: %q", d)
	}
	return d, nil
}

// clienteVerificacao recusa conectar em endereco interno MESMO que o DNS do
// dominio aponte pra la' — a checagem e' feita no IP ja' resolvido, no
// momento da conexao, pra nao cair em DNS rebinding.
var clienteVerificacao = &http.Client{
	Timeout: 10 * time.Second,
	// Redirect poderia levar pra endereco interno por outro caminho.
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	Transport: &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout: 5 * time.Second,
			Control: func(_, endereco string, _ syscall.RawConn) error {
				host, _, err := net.SplitHostPort(endereco)
				if err != nil {
					return err
				}
				ip := net.ParseIP(host)
				if ip == nil || !ipPublico(ip) {
					return fmt.Errorf("endereco nao publico recusado: %s", host)
				}
				return nil
			},
		}).DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 8 * time.Second,
	},
}

func ipPublico(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() ||
		ip.IsInterfaceLocalMulticast() || cgnat.Contains(ip))
}

// 100.64.0.0/10 (CGNAT) — IsPrivate nao cobre, e redes de container usam.
var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// dominiosRedeInterna podem resolver pra IP privado (portal na mesma rede do
// app). Vem de BITRIX_DOMINIOS_REDE_INTERNA. Sem isso, um portal on-premise
// atras de DNS interno ficaria sem login nenhum.
var dominiosRedeInterna = map[string]bool{}

// PermitirRedeInterna registra os dominios liberados. Chamar no boot.
func PermitirRedeInterna(dominios []string) {
	for _, d := range dominios {
		if n, err := DominioSeguro(d); err == nil {
			dominiosRedeInterna[n] = true
		}
	}
}

var clienteRedeInterna = &http.Client{
	Timeout:       10 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func clientePara(dominio string) *http.Client {
	if dominiosRedeInterna[dominio] {
		return clienteRedeInterna
	}
	return clienteVerificacao
}
