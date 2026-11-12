package config

import (
	"strings"
	"testing"
)

func TestLoadPanelFailsFast(t *testing.T) {
	t.Setenv("APP_ENV", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("PANEL_BASE_URL", "localhost:8080")
	t.Setenv("REDIRECT_BASE_URL", "")
	t.Setenv("ABUSE_EMAIL", "")
	_, err := LoadPanel()
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"APP_ENV", "DATABASE_URL", "PANEL_BASE_URL must be an absolute", "REDIRECT_BASE_URL is required", "ABUSE_EMAIL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
}

func TestLoadPanelProdNeedsSafeBrowsing(t *testing.T) {
	t.Setenv("APP_ENV", "prod")
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("PANEL_BASE_URL", "https://app.example")
	t.Setenv("REDIRECT_BASE_URL", "https://qr.example/")
	t.Setenv("ABUSE_EMAIL", "abus@example.com")
	t.Setenv("SAFE_BROWSING_API_KEY", "")
	if _, err := LoadPanel(); err == nil || !strings.Contains(err.Error(), "SAFE_BROWSING_API_KEY") {
		t.Fatalf("err = %v", err)
	}
	t.Setenv("SAFE_BROWSING_API_KEY", "k")
	c, err := LoadPanel()
	if err != nil {
		t.Fatal(err)
	}
	if c.RedirectBaseURL != "https://qr.example" {
		t.Fatalf("trailing slash not trimmed: %q", c.RedirectBaseURL)
	}
}

func TestLoadRedirectRejectsSlowSync(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("SNAPSHOT_PATH", "/tmp/s.json")
	t.Setenv("ABUSE_EMAIL", "abus@example.com")
	t.Setenv("SYNC_INTERVAL", "90s")
	if _, err := LoadRedirect(); err == nil || !strings.Contains(err.Error(), "60 s") {
		t.Fatalf("err = %v", err)
	}
}
