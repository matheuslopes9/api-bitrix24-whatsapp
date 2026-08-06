// config.go — carga de configuracao do gateway a partir de variaveis de
// ambiente. Deixa o pacote autossuficiente: qualquer sistema pode importar
// este gateway e montar as credenciais chamando ConfigFromEnv() / BoletoConfigFromEnv(),
// sem depender de nenhum framework de config externo.
//
// TOKEN, PARAMETROS E CHAVES — todos vem daqui:
//   ITAU_CLIENT_ID       (obrigatorio) — tambem e' o CN do certificado
//   ITAU_CLIENT_SECRET   (obrigatorio) — o "token"/segredo do OAuth
//   ITAU_API_KEY         (opcional)    — x-itau-apikey; se vazio usa o ClientID
//   ITAU_CHAVE_PIX       (PIX)         — chave que recebe os pagamentos
//   ITAU_CERT_PATH       (mTLS)        — caminho do .crt
//   ITAU_KEY_PATH        (mTLS)        — caminho do .key
//   ITAU_ENV             (ambiente)    — producao | sandbox
//   ITAU_BASE_URL        (opcional)    — override do endpoint PIX
//   ITAU_TOKEN_URL       (opcional)    — override do STS
//   ITAU_BOLETO_URL      (opcional)    — override do cash_management
//   ITAU_AGENCIA / ITAU_CONTA / ITAU_CONTA_DAC / ITAU_CARTEIRA /
//   ITAU_ESPECIE / ITAU_ETAPA          — dados da conta (boleto)
package gateway

import "os"

// env le uma variavel, com fallback.
func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// ConfigFromEnv monta a Config do cliente PIX/boleto lendo o ambiente.
// Os defaults de path de certificado seguem o padrao de deploy em container
// (/app/certs). Ajuste as envs conforme seu sistema.
func ConfigFromEnv() Config {
	return Config{
		ClientID:     os.Getenv("ITAU_CLIENT_ID"),
		ClientSecret: os.Getenv("ITAU_CLIENT_SECRET"),
		APIKey:       os.Getenv("ITAU_API_KEY"),
		ChavePIX:     os.Getenv("ITAU_CHAVE_PIX"),
		CertPath:     env("ITAU_CERT_PATH", "/app/certs/itau.crt"),
		KeyPath:      env("ITAU_KEY_PATH", "/app/certs/itau.key"),
		Ambiente:     env("ITAU_ENV", "sandbox"),
		BaseURL:      os.Getenv("ITAU_BASE_URL"),
		TokenURL:     os.Getenv("ITAU_TOKEN_URL"),
	}
}

// BoletoConfigFromEnv monta a BoletoConfig (dados da conta) lendo o ambiente.
// Os defaults refletem a conta usada no projeto original; troque pelos seus.
func BoletoConfigFromEnv() BoletoConfig {
	return BoletoConfig{
		BaseURL:  os.Getenv("ITAU_BOLETO_URL"),
		Agencia:  env("ITAU_AGENCIA", ""),
		Conta:    env("ITAU_CONTA", ""),
		ContaDAC: env("ITAU_CONTA_DAC", ""),
		Carteira: env("ITAU_CARTEIRA", "109"),
		Especie:  env("ITAU_ESPECIE", "08"),
		Etapa:    env("ITAU_ETAPA", "efetivacao"),
	}
}

// NewFromEnv e' o atalho mais comum: le tudo do ambiente e ja' devolve um
// Client pronto (ou erro se o certificado for exigido e nao existir).
func NewFromEnv() (*Client, error) {
	return New(ConfigFromEnv())
}
