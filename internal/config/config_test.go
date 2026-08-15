package config

import "testing"

func TestLoadUsesPortableDefaults(t *testing.T) {
	t.Setenv("MONGODB_URI", "mongodb://example.invalid/personal_memory")
	t.Setenv("AUTH_BEARER_TOKEN", "01234567890123456789012345678901")
	t.Setenv("HTTP_ADDR", "")
	t.Setenv("PORT", "9090")
	t.Setenv("DEFAULT_TIMEZONE", "Asia/Bangkok")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.HTTPAddr != ":9090" {
		t.Fatalf("expected PORT fallback, got %q", cfg.HTTPAddr)
	}
	if cfg.MongoDatabase != "personal_memory" || cfg.MaxCollections != 100 {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
}

func TestLoadRejectsShortToken(t *testing.T) {
	t.Setenv("MONGODB_URI", "mongodb://example.invalid/personal_memory")
	t.Setenv("AUTH_BEARER_TOKEN", "short")

	if _, err := Load(); err == nil {
		t.Fatal("expected a short bearer token to be rejected")
	}
}
