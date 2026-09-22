package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadEnvOverridesAndValidation(t *testing.T) {
	t.Setenv("TIDE_DATABASE_URL", "postgres://x")
	t.Setenv("TIDE_SECRETS_KEY", "ZGV2LW9ubHktZW5jcnlwdGlvbi1rZXktMzItYnl0ZXM=")
	t.Setenv("TIDE_SERVER_TRUSTED_PROXIES", "10.0.0.0/8,127.0.0.1")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Database.URL != "postgres://x" || c.Server.Addr != ":8080" || len(c.Server.TrustedProxies) != 2 {
		t.Fatalf("%+v", c)
	}
	if err := c.ValidateServe(); err != nil {
		t.Fatal(err)
	}
	c.Secrets.Key = "short"
	if err := c.ValidateServe(); err == nil || !strings.Contains(err.Error(), "TIDE_SECRETS_KEY") {
		t.Fatalf("want key error, got %v", err)
	}
}

func TestUnknownYAMLKey(t *testing.T) {
	p := filepath.Join(t.TempDir(), "tide.yaml")
	os.WriteFile(p, []byte("server:\n  adr: \":1\"\n"), 0o600)
	if _, err := Load(p); err == nil {
		t.Fatal("typo in YAML must fail")
	}
}

func TestShippedConfigLoads(t *testing.T) {
	if _, err := Load("../../configs/tide.yaml"); err != nil {
		t.Fatal(err)
	}
}
