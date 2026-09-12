// Package config provides typed configuration loaded from YAML with env overrides.
package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the root configuration structure.
type Config struct {
	Server    ServerConfig    `yaml:"server"`
	DB        DBConfig        `yaml:"db"`
	Store     StoreConfig     `yaml:"store"`
	Cache     CacheConfig     `yaml:"cache"`
	Queue     QueueConfig     `yaml:"queue"`
	LLM       LLMConfig       `yaml:"llm"`
	Storage   StorageConfig   `yaml:"storage"`
	Search    SearchConfig    `yaml:"search"`
	Auth      AuthConfig      `yaml:"auth"`
	Billing   BillingConfig   `yaml:"billing"`
	Engines   EnginesConfig   `yaml:"engines"`
	RateLimit RateLimitConfig `yaml:"rateLimit"`
}

// StoreConfig selects the persistence backend for all services.
// memory = MVP 内存 store（测试/开发）；postgres = 平台库持久化（生产）。
type StoreConfig struct {
	Driver string `yaml:"driver"` // memory | postgres
}

// RateLimitConfig holds rate limiting settings.
type RateLimitConfig struct {
	Enabled bool `yaml:"enabled"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Addr string `yaml:"addr"`
	Env  string `yaml:"env"` // dev | production
}

// DBConfig holds database connection settings.
type DBConfig struct {
	Primary        string   `yaml:"primary"`
	Replicas       []string `yaml:"replicas"`
	ReadWriteSplit bool     `yaml:"readWriteSplit"`
	MaxConns       int      `yaml:"maxConns"`
	Tenant         struct {
		AutoCreate     bool   `yaml:"autoCreate"`
		MigrateOnBoot  bool   `yaml:"migrateOnBoot"`
		PoolMaxConns   int    `yaml:"poolMaxConns"`
		MaxPools       int    `yaml:"maxPools"`
		IdleTimeout    string `yaml:"idleTimeout"`
	} `yaml:"tenant"`
}

// CacheConfig holds Redis cache settings.
type CacheConfig struct {
	Addr string `yaml:"addr"`
	TTL  string `yaml:"ttl"`
}

// QueueConfig holds message queue settings.
type QueueConfig struct {
	Driver string `yaml:"driver"` // memory | redis | rabbitmq | nats
}

// LLMConfig holds LLM provider and model settings.
type LLMConfig struct {
	DefaultProvider string        `yaml:"defaultProvider"`
	Models          []ModelConfig `yaml:"models"`
}

// ModelConfig defines one LLM model.
type ModelConfig struct {
	ID                 string  `yaml:"id"`
	Provider           string  `yaml:"provider"`
	BaseURL            string  `yaml:"baseUrl"`
	APIKey             string  `yaml:"apiKey"`
	InputCostPerM      float64 `yaml:"inputCostPerM"`
	OutputCostPerM     float64 `yaml:"outputCostPerM"`
	UserInputPricePerM float64 `yaml:"userInputPricePerM"`
	UserOutputPricePerM float64 `yaml:"userOutputPricePerM"`
	MaxTokens          int     `yaml:"maxTokens"`
}

// StorageConfig holds object storage settings.
type StorageConfig struct {
	Driver string `yaml:"driver"` // local | s3
}

// SearchConfig holds full-text search settings.
type SearchConfig struct {
	Driver string `yaml:"driver"` // pg | elasticsearch
}

// AuthConfig holds authentication settings.
type AuthConfig struct {
	JWTSecret  string `yaml:"jwtSecret"`
	AccessTTL  string `yaml:"accessTTL"`
	RefreshTTL string `yaml:"refreshTTL"`
}

// BillingConfig holds billing settings.
type BillingConfig struct {
	Currency      string `yaml:"currency"`
	Gateway       string `yaml:"gateway"`
	InvoicePrefix string `yaml:"invoicePrefix"`
}

// EnginesConfig holds Python engine service URLs.
type EnginesConfig struct {
	Query struct {
		URL     string `yaml:"url"`
		Timeout string `yaml:"timeout"`
	} `yaml:"query"`
	Media struct {
		URL     string `yaml:"url"`
		Timeout string `yaml:"timeout"`
	} `yaml:"media"`
	Insight struct {
		URL     string `yaml:"url"`
		Timeout string `yaml:"timeout"`
	} `yaml:"insight"`
	Report struct {
		URL     string `yaml:"url"`
		Timeout string `yaml:"timeout"`
	} `yaml:"report"`
	Forum struct {
		URL     string `yaml:"url"`
		Timeout string `yaml:"timeout"`
	} `yaml:"forum"`
}

// Load reads a YAML config file, applies default values, and overrides with
// APP_ prefixed environment variables (e.g., APP_SERVER_ADDR → server.addr).
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	cfg := defaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("config: parse yaml: %w", err)
	}

	applyEnvOverrides(cfg)

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Validate checks that the configuration is self-consistent.
func (c *Config) Validate() error {
	if c.Server.Addr == "" {
		return fmt.Errorf("server.addr is required")
	}
	if c.DB.Primary == "" {
		return fmt.Errorf("db.primary is required")
	}
	for _, m := range c.LLM.Models {
		if m.ID == "" {
			return fmt.Errorf("llm.models[].id must not be empty")
		}
	}
	return nil
}

// defaultConfig returns a Config with safe defaults applied.
func defaultConfig() *Config {
	return &Config{
		Server: ServerConfig{Addr: ":8080", Env: "production"},
		Store:  StoreConfig{Driver: "memory"},
		Queue:  QueueConfig{Driver: "memory"},
		Cache:  CacheConfig{TTL: "60s"},
		LLM:    LLMConfig{DefaultProvider: "deepseek"},
		Storage: StorageConfig{Driver: "local"},
		Search:  SearchConfig{Driver: "pg"},
		Auth:    AuthConfig{AccessTTL: "15m", RefreshTTL: "720h"},
		Billing: BillingConfig{Currency: "CNY", Gateway: "manual", InvoicePrefix: "INV-"},
	}
}

// applyEnvOverrides maps APP_ prefixed env vars to config fields.
// Supported overrides: APP_SERVER_ADDR
func applyEnvOverrides(cfg *Config) {
	// Simple key-value overrides for high-priority operational settings.
	if v := os.Getenv("APP_SERVER_ADDR"); v != "" {
		cfg.Server.Addr = v
	}
	if v := os.Getenv("APP_DB_PRIMARY"); v != "" {
		cfg.DB.Primary = v
	}
	if v := os.Getenv("APP_QUEUE_DRIVER"); v != "" {
		cfg.Queue.Driver = v
	}
	if v := os.Getenv("APP_STORAGE_DRIVER"); v != "" {
		cfg.Storage.Driver = v
	}
	if v := os.Getenv("APP_AUTH_JWT_SECRET"); v != "" {
		cfg.Auth.JWTSecret = v
	}
	// Config path separator normalized from underscores to dots.
	// e.g., APP_DB_READ_WRITE_SPLIT → db.readWriteSplit
	for _, e := range os.Environ() {
		parts := strings.SplitN(e, "=", 2)
		key := parts[0]
		if !strings.HasPrefix(key, "APP_DB_READ_WRITE_SPLIT") {
			continue
		}
		if strings.ToLower(parts[1]) == "true" {
			cfg.DB.ReadWriteSplit = true
		}
	}
}
