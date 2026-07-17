package config

import (
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	App      AppConfig
	Postgres PostgresConfig
	Redis    RedisConfig
	WhatsApp WhatsAppConfig
	Bitrix   BitrixConfig
	Queue    QueueConfig
	Watchdog WatchdogConfig
	Billing  BillingConfig
}

// BillingConfig — gateway maxiPago (Rede). Sandbox: testapi.maxipago.net.
// MERCHANT_ID/KEY vem do portal maxiPago (Configuracoes > Dados da Loja).
// Env "sandbox" | "production" decide a base URL da API XML.
type BillingConfig struct {
	MaxiPagoMerchantID  string // MAXIPAGO_MERCHANT_ID
	MaxiPagoMerchantKey string // MAXIPAGO_MERCHANT_KEY
	MaxiPagoEnv         string // MAXIPAGO_ENV: sandbox (default) | production
	ProcessorCard       string // MAXIPAGO_PROCESSOR_CARD: 1 = simulador sandbox
	ProcessorBoleto     string // MAXIPAGO_PROCESSOR_BOLETO: 12 = boleto teste
	ProPriceCents       int    // MAXIPAGO_PRO_PRICE_CENTS: preco do Pro em centavos
	ActivateDays        int    // MAXIPAGO_ACTIVATE_DAYS: dias de Pro por pagamento
}

type AppConfig struct {
	Port          string
	Env           string
	Secret        string
	PublicURL     string // APP_BASE_URL — URL pública do servidor (ex: https://meuapp.easypanel.host)
	AdminUser     string // ADMIN_USER — login do super-admin para /admin
	AdminPassword string // ADMIN_PASSWORD — senha em texto plano (compare time-constant)
}

type PostgresConfig struct {
	Host         string
	Port         string
	User         string
	Password     string
	DB           string
	SSLMode      string
	MaxOpenConns int
	MaxIdleConns int
}

type RedisConfig struct {
	Host     string
	Port     string
	Password string
	DB       int
}

type WhatsAppConfig struct {
	SessionsDir string
	MediaDir    string
	LogLevel    string
}

type BitrixConfig struct {
	Domain       string
	ClientID     string
	ClientSecret string
	RedirectURI  string
	OpenLineID   int
}

type QueueConfig struct {
	Workers          int
	MaxRetry         int
	RetryBaseDelayMs int
}

type WatchdogConfig struct {
	PingIntervalSecs int
}

func Load() (*Config, error) {
	viper.SetConfigFile(".env")
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	_ = viper.ReadInConfig()

	cfg := &Config{
		App: AppConfig{
			Port:          viper.GetString("APP_PORT"),
			Env:           viper.GetString("APP_ENV"),
			Secret:        viper.GetString("APP_SECRET"),
			PublicURL:     viper.GetString("APP_BASE_URL"),
			AdminUser:     viper.GetString("ADMIN_USER"),
			AdminPassword: viper.GetString("ADMIN_PASSWORD"),
		},
		Postgres: PostgresConfig{
			Host:         viper.GetString("POSTGRES_HOST"),
			Port:         viper.GetString("POSTGRES_PORT"),
			User:         viper.GetString("POSTGRES_USER"),
			Password:     viper.GetString("POSTGRES_PASSWORD"),
			DB:           viper.GetString("POSTGRES_DB"),
			SSLMode:      getEnvWithDefault("POSTGRES_SSLMODE", "disable"),
			MaxOpenConns: viper.GetInt("POSTGRES_MAX_OPEN_CONNS"),
			MaxIdleConns: viper.GetInt("POSTGRES_MAX_IDLE_CONNS"),
		},
		Redis: RedisConfig{
			Host:     viper.GetString("REDIS_HOST"),
			Port:     viper.GetString("REDIS_PORT"),
			Password: viper.GetString("REDIS_PASSWORD"),
			DB:       viper.GetInt("REDIS_DB"),
		},
		WhatsApp: WhatsAppConfig{
			// Path ABSOLUTO por default pra bater com o volume persistente do
			// EasyPanel montado em /app/sessions. Path relativo "./sessions"
			// depende do working directory do processo — se divergir de /app,
			// os arquivos .db do whatsmeow (chaves de cripto) vao pra outro
			// lugar que NAO e' o volume, e o QR cai a cada deploy.
			SessionsDir: getEnvWithDefault("WA_SESSIONS_DIR", "/app/sessions"),
			MediaDir:    getEnvWithDefault("WA_MEDIA_DIR", "/app/media"),
			LogLevel:    getEnvWithDefault("WA_LOG_LEVEL", "INFO"),
		},
		Bitrix: BitrixConfig{
			Domain:       viper.GetString("BITRIX_DOMAIN"),
			ClientID:     viper.GetString("BITRIX_CLIENT_ID"),
			ClientSecret: viper.GetString("BITRIX_CLIENT_SECRET"),
			RedirectURI:  viper.GetString("BITRIX_REDIRECT_URI"),
			OpenLineID:   getIntWithDefault("BITRIX_OPEN_LINE_ID", 1),
		},
		Queue: QueueConfig{
			Workers:          getIntWithDefault("QUEUE_WORKERS", 20),
			MaxRetry:         getIntWithDefault("QUEUE_MAX_RETRY", 5),
			RetryBaseDelayMs: getIntWithDefault("QUEUE_RETRY_BASE_DELAY_MS", 1000),
		},
		Watchdog: WatchdogConfig{
			PingIntervalSecs: getIntWithDefault("WATCHDOG_PING_INTERVAL_SECS", 30),
		},
		Billing: BillingConfig{
			MaxiPagoMerchantID:  viper.GetString("MAXIPAGO_MERCHANT_ID"),
			MaxiPagoMerchantKey: viper.GetString("MAXIPAGO_MERCHANT_KEY"),
			MaxiPagoEnv:         getEnvWithDefault("MAXIPAGO_ENV", "sandbox"),
			ProcessorCard:       getEnvWithDefault("MAXIPAGO_PROCESSOR_CARD", "1"),
			ProcessorBoleto:     getEnvWithDefault("MAXIPAGO_PROCESSOR_BOLETO", "12"),
			ProPriceCents:       getIntWithDefault("MAXIPAGO_PRO_PRICE_CENTS", 19900),
			ActivateDays:        getIntWithDefault("MAXIPAGO_ACTIVATE_DAYS", 30),
		},
	}

	setDefaults(cfg)
	return cfg, nil
}

// BaseURL retorna a URL pública do servidor sem trailing slash.
// Usada como base para construir redirect URIs e event handler URLs.
// Se APP_BASE_URL não estiver configurado, usa http://localhost:<port> como fallback.
func (c *AppConfig) BaseURL() string {
	u := strings.TrimRight(c.PublicURL, "/")
	if u == "" {
		u = "http://localhost:" + c.Port
	}
	return u
}

func (c *PostgresConfig) DSN() string {
	return "host=" + c.Host +
		" port=" + c.Port +
		" user=" + c.User +
		" password=" + c.Password +
		" dbname=" + c.DB +
		" sslmode=" + c.SSLMode
}

func (c *PostgresConfig) URL() string {
	return "postgres://" + c.User + ":" + c.Password +
		"@" + c.Host + ":" + c.Port + "/" + c.DB + "?sslmode=" + c.SSLMode
}

func (c *RedisConfig) Addr() string {
	return c.Host + ":" + c.Port
}

func (c *QueueConfig) RetryBaseDelay() time.Duration {
	return time.Duration(c.RetryBaseDelayMs) * time.Millisecond
}

func (c *WatchdogConfig) PingInterval() time.Duration {
	return time.Duration(c.PingIntervalSecs) * time.Second
}

func getEnvWithDefault(key, def string) string {
	v := viper.GetString(key)
	if v == "" {
		return def
	}
	return v
}

func getIntWithDefault(key string, def int) int {
	v := viper.GetInt(key)
	if v == 0 {
		return def
	}
	return v
}

func setDefaults(cfg *Config) {
	if cfg.App.Port == "" {
		cfg.App.Port = "8080"
	}
	if cfg.App.Env == "" {
		cfg.App.Env = "production"
	}
	if cfg.Postgres.MaxOpenConns == 0 {
		cfg.Postgres.MaxOpenConns = 50
	}
	if cfg.Postgres.MaxIdleConns == 0 {
		cfg.Postgres.MaxIdleConns = 10
	}
}
