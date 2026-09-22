// Package config loads boot-time configuration: YAML defaults overridden by
// TIDE_* environment variables. Only what is needed before the database is
// reachable belongs here (CONVENTIONS.md §6).
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

type Config struct {
	Server   Server   `mapstructure:"server"`
	Database Database `mapstructure:"database"`
	Redis    Redis    `mapstructure:"redis"`
	Secrets  Secrets  `mapstructure:"secrets"`
	Log      Log      `mapstructure:"log"`
}

type Server struct {
	Addr           string   `mapstructure:"addr"`
	TrustedProxies []string `mapstructure:"trusted_proxies"`
	DevWebProxy    string   `mapstructure:"dev_web_proxy"`
	// Metrics serves Prometheus metrics at /metrics when enabled. Give
	// MetricsToken (TIDE_SERVER_METRICS_TOKEN) whenever the path is reachable
	// from outside the cluster: scrapers then send it as a bearer token.
	Metrics      bool   `mapstructure:"metrics"`
	MetricsToken string `mapstructure:"metrics_token"`
}

type Database struct {
	URL        string `mapstructure:"url"`
	MigrateURL string `mapstructure:"migrate_url"`
}

// Redis is the optional shared cache. Empty means every replica keeps its
// own copy, which is correct but pays for the catalog once per replica.
// Nothing here is a source of truth, so losing Redis never loses data.
type Redis struct {
	URL string `mapstructure:"url"`
}

type Secrets struct {
	Key string `mapstructure:"key"`
}

type Log struct {
	Level string `mapstructure:"level"`
}

// keys lists every config path so environment overrides bind even when the
// YAML file is absent (viper only maps env vars for keys it knows).
var defaults = map[string]any{
	"server.addr":            ":8080",
	"server.trusted_proxies": []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "127.0.0.1/32"},
	"server.dev_web_proxy":   "",
	"server.metrics":         true,
	"server.metrics_token":   "",
	"database.url":           "",
	"database.migrate_url":   "",
	"redis.url":              "",
	"secrets.key":            "",
	"log.level":              "info",
}

// Load reads path (optional; "" skips the file) and applies TIDE_* overrides.
// Unknown YAML keys are errors so typos surface immediately.
func Load(path string) (*Config, error) {
	v := viper.New()
	for k, d := range defaults {
		v.SetDefault(k, d)
	}
	v.SetEnvPrefix("TIDE")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	if path != "" {
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
		v.SetConfigFile(path)
		v.SetConfigType("yaml")
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("config: parse %s: %w", path, err)
		}
	}
	var c Config
	if err := v.Unmarshal(&c, viper.DecodeHook(mapstructure.StringToSliceHookFunc(",")), func(dc *mapstructure.DecoderConfig) {
		dc.ErrorUnused = true
	}); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return &c, nil
}

// ValidateServe checks what `tide serve` needs and fails fast with messages
// that name the environment variable to set.
func (c *Config) ValidateServe() error {
	var errs []error
	if c.Database.URL == "" {
		errs = append(errs, errors.New("TIDE_DATABASE_URL is required"))
	}
	if key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(c.Secrets.Key)); err != nil || len(key) != 32 {
		errs = append(errs, errors.New("TIDE_SECRETS_KEY must be base64 of exactly 32 bytes (openssl rand -base64 32)"))
	}
	if c.Redis.URL != "" {
		if u, err := url.Parse(c.Redis.URL); err != nil || (u.Scheme != "redis" && u.Scheme != "rediss") || u.Host == "" {
			errs = append(errs, errors.New("TIDE_REDIS_URL must be redis://host:port or rediss://host:port"))
		}
	}
	if c.Server.DevWebProxy != "" {
		if u, err := url.Parse(c.Server.DevWebProxy); err != nil || u.Host == "" {
			errs = append(errs, errors.New("TIDE_SERVER_DEV_WEB_PROXY must be an absolute URL"))
		}
	}
	for _, p := range c.Server.TrustedProxies {
		if _, _, err := net.ParseCIDR(p); err != nil && net.ParseIP(p) == nil {
			errs = append(errs, fmt.Errorf("server.trusted_proxies: %q is not an IP or CIDR", p))
		}
	}
	return errors.Join(errs...)
}

func (c *Config) ValidateMigrate() error {
	if c.Database.MigrateURL == "" {
		return errors.New("TIDE_DATABASE_MIGRATE_URL is required")
	}
	return nil
}
