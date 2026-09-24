package config

import (
	"os"
	"testing"
)

func TestLoad_minimalConfig(t *testing.T) {
	tmp := t.TempDir()
	path := tmp + "/config.yaml"
	content := `
server:
  addr: ":9090"
  env: dev
db:
  primary: "postgres://u:p@localhost:5432/db"
auth:
  jwtSecret: "test-secret-key"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Server.Addr != ":9090" {
		t.Errorf("server.addr = %q, want :9090", cfg.Server.Addr)
	}
	if cfg.Server.Env != "dev" {
		t.Errorf("server.env = %q, want dev", cfg.Server.Env)
	}
}

func TestLoad_defaults(t *testing.T) {
	tmp := t.TempDir()
	path := tmp + "/config.yaml"
	// Server + DB is required minimal config.
	content := `
server:
  addr: ":8080"
  env: production
db:
  primary: "postgres://u:p@localhost:5432/db"
auth:
  jwtSecret: "test-jwt-secret"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Defaults for optional sections
	if cfg.Queue.Driver != "memory" {
		t.Errorf("queue.driver default = %q, want memory", cfg.Queue.Driver)
	}
	if cfg.LLM.DefaultProvider != "deepseek" {
		t.Errorf("llm.defaultProvider default = %q, want deepseek", cfg.LLM.DefaultProvider)
	}
	if cfg.Storage.Driver != "local" {
		t.Errorf("storage.driver default = %q, want local", cfg.Storage.Driver)
	}
	if cfg.Search.Driver != "pg" {
		t.Errorf("search.driver default = %q, want pg", cfg.Search.Driver)
	}
	if cfg.Cache.TTL != "60s" {
		t.Errorf("cache.ttl default = %q, want 60s", cfg.Cache.TTL)
	}
}

func TestLoad_envOverride(t *testing.T) {
	os.Setenv("APP_SERVER_ADDR", ":9999")
	defer os.Unsetenv("APP_SERVER_ADDR")

	tmp := t.TempDir()
	path := tmp + "/config.yaml"
	content := `
server:
  addr: ":8080"
  env: dev
db:
  primary: "postgres://u:p@localhost:5432/db"
auth:
  jwtSecret: "test-secret"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Server.Addr != ":9999" {
		t.Errorf("env override failed: server.addr = %q, want :9999", cfg.Server.Addr)
	}
}

func TestLoad_missingRequired_returnsError(t *testing.T) {
	tmp := t.TempDir()
	path := tmp + "/config.yaml"
	content := `server: {}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing db.primary, got nil")
	}
}

func TestValidate_LLMPricingConsistency(t *testing.T) {
	cfg := Config{
		Server: ServerConfig{Addr: ":8080", Env: "dev"},
		DB:     DBConfig{Primary: "postgres://x"},
		Auth:   AuthConfig{JWTSecret: "test-secret"},
		LLM: LLMConfig{
			Models: []ModelConfig{
				{ID: "dummy", Provider: "test", InputCostPerM: 0.14, OutputCostPerM: 0.42},
			},
		},
	}
	// Validate should pass for consistent pricing.
	if err := cfg.Validate(); err != nil {
		t.Errorf("valid config should not error: %v", err)
	}
}

func TestValidate_emptyModelID_returnsError(t *testing.T) {
	cfg := Config{
		Server: ServerConfig{Addr: ":8080", Env: "dev"},
		DB:     DBConfig{Primary: "postgres://x"},
		LLM: LLMConfig{
			Models: []ModelConfig{
				{ID: "", Provider: "test"},
			},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for empty model ID")
	}
}
