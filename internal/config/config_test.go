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
	t.Setenv("CONTACT_EMAIL", "")
	_, err := LoadPanel()
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"APP_ENV", "DATABASE_URL", "PANEL_BASE_URL must be an absolute", "REDIRECT_BASE_URL is required", "CONTACT_EMAIL"} {
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
	t.Setenv("CONTACT_EMAIL", "abus@example.com")
	t.Setenv("SAFE_BROWSING_API_KEY", "")
	if _, err := LoadPanel(); err == nil || !strings.Contains(err.Error(), "SAFE_BROWSING_API_KEY") {
		t.Fatalf("err = %v", err)
	}
	t.Setenv("SAFE_BROWSING_API_KEY", "k")
	// Public prod panel without the legal holder: refused.
	if _, err := LoadPanel(); err == nil || !strings.Contains(err.Error(), "LEGAL_HOLDER") {
		t.Fatalf("public panel without holder: err = %v", err)
	}
	// Behind Cloudflare Access (closed beta) it may start without it.
	t.Setenv("CF_ACCESS_TEAM_DOMAIN", "team.cloudflareaccess.com")
	t.Setenv("CF_ACCESS_AUD", "aud")
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
	t.Setenv("PANEL_BASE_URL", "https://app.example")
	t.Setenv("REDIRECT_BASE_URL", "https://qr.example")
	t.Setenv("CONTACT_EMAIL", "abus@example.com")
	t.Setenv("SYNC_INTERVAL", "90s")
	if _, err := LoadRedirect(); err == nil || !strings.Contains(err.Error(), "60 s") {
		t.Fatalf("err = %v", err)
	}
}

func TestLegalHolderComplete(t *testing.T) {
	t.Setenv("LEGAL_HOLDER_NAME", "Nom")
	t.Setenv("LEGAL_HOLDER_TAX_ID", "")
	t.Setenv("LEGAL_HOLDER_ADDRESS", "Adreça")
	r := reader{}
	if r.legal().Complete() {
		t.Fatal("holder without tax id reported complete")
	}
	t.Setenv("LEGAL_HOLDER_TAX_ID", "00000000T")
	if !r.legal().Complete() {
		t.Fatal("full holder reported incomplete")
	}
}
