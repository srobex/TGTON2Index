package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

const (
	defaultNetwork             = "mainnet"
	defaultCatchupHours        = 24
	defaultMasterSeqnoCache    = 1000
	defaultMinterCacheTTL      = 24 * time.Hour
	envPrefix                  = "HSI"
	configName                 = "config"
	defaultMainnetDatabaseName = "hyper_sniper_mainnet"
	defaultTestnetDatabaseName = "hyper_sniper_testnet"
)

// Config описывает все параметры приложения.
type Config struct {
	App      AppConfig      `mapstructure:"app"`
	Postgres PostgresConfig `mapstructure:"postgres"`
	Redis    RedisConfig    `mapstructure:"redis"`
	Notifier NotifierConfig `mapstructure:"notifier"`
}

// AppConfig содержит сетевые и общие параметры работы индексатора.
type AppConfig struct {
	Network              string   `mapstructure:"network"`
	Liteservers          []string `mapstructure:"liteservers_list"`
	CatchupHours         int      `mapstructure:"catchup_hours"`
	MasterSeqnoCacheSize int      `mapstructure:"master_seqno_cache_size"`
	MinterCacheTTL       string   `mapstructure:"minter_cache_ttl"`
}

// PostgresConfig описывает подключения к PostgreSQL для разных сетей.
type PostgresConfig struct {
	DSN       string `mapstructure:"dsn"`
	DSNTestnet string `mapstructure:"dsn_testnet"`
}

// RedisConfig содержит адрес Redis.
type RedisConfig struct {
	Addr string `mapstructure:"addr"`
}

// NotifierConfig описывает параметры уведомлений.
type NotifierConfig struct {
	TgBotToken string `mapstructure:"tg_bot_token"`
	TgChatID   string `mapstructure:"tg_chat_id"`
	WebhookURL string `mapstructure:"webhook_url"`
}

// Load читает config.yaml и переменные окружения с префиксом HSI.
func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetEnvPrefix(envPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	setDefaults(v)

	if path == "" {
		v.SetConfigName(configName)
		v.AddConfigPath(".")
	} else {
		v.SetConfigFile(path)
	}

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("не удалось прочитать конфиг: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("не удалось распарсить конфиг: %w", err)
	}

	if err := cfg.normalize(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// ResolvePostgresDSN выбирает DSN в зависимости от сети.
func (c *Config) ResolvePostgresDSN() string {
	if c.App.Network == "testnet" && c.Postgres.DSNTestnet != "" {
		return c.Postgres.DSNTestnet
	}
	return c.Postgres.DSN
}

// MinterCacheDuration возвращает TTL для кэша минтеров.
func (c *Config) MinterCacheDuration() time.Duration {
	d, err := time.ParseDuration(c.App.MinterCacheTTL)
	if err != nil || d <= 0 {
		return defaultMinterCacheTTL
	}
	return d
}

// CatchupDuration возвращает длительность окна для режима catchup.
func (c *Config) CatchupDuration() time.Duration {
	if c.App.CatchupHours <= 0 {
		return time.Duration(defaultCatchupHours) * time.Hour
	}
	return time.Duration(c.App.CatchupHours) * time.Hour
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("app.network", defaultNetwork)
	v.SetDefault("app.catchup_hours", defaultCatchupHours)
	v.SetDefault("app.master_seqno_cache_size", defaultMasterSeqnoCache)
	v.SetDefault("app.minter_cache_ttl", defaultMinterCacheTTL.String())
	v.SetDefault("postgres.dsn", fmt.Sprintf("postgres://sniper:sniper@localhost:5432/%s?sslmode=disable", defaultMainnetDatabaseName))
	v.SetDefault("postgres.dsn_testnet", fmt.Sprintf("postgres://sniper:sniper@localhost:5432/%s?sslmode=disable", defaultTestnetDatabaseName))
	v.SetDefault("redis.addr", "localhost:6379")
	v.SetDefault("notifier.tg_bot_token", "")
	v.SetDefault("notifier.tg_chat_id", "")
	v.SetDefault("notifier.webhook_url", "")
}

func (c *Config) normalize() error {
	c.App.Network = strings.ToLower(strings.TrimSpace(c.App.Network))
	if c.App.Network == "" {
		c.App.Network = defaultNetwork
	}

	if c.App.Network != "mainnet" && c.App.Network != "testnet" {
		return fmt.Errorf("некорректная сеть: %s", c.App.Network)
	}

	if c.Postgres.DSN == "" {
		return fmt.Errorf("postgres.dsn обязателен")
	}

	if c.Redis.Addr == "" {
		return fmt.Errorf("redis.addr обязателен")
	}

	return nil
}

