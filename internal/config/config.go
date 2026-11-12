// Package config reads the environment once at startup and fails fast when
// something mandatory is missing.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

// Env is the deployment environment.
type Env string

const (
	Dev  Env = "dev"
	Prod Env = "prod"
)

// Panel is the configuration of the management panel.
type Panel struct {
	Env             Env
	Addr            string
	DatabaseURL     string
	PanelBaseURL    string // public URL of the panel, used in e-mail links
	RedirectBaseURL string // public URL encoded in every QR, e.g. https://qr.example
	SafeBrowsingKey string // empty only in dev
	ContactEmail    string // published contact: legal notice, abuse reports, data requests
	Legal           LegalHolder
	// Cloudflare Access: when both are set, every panel request must carry a
	// valid Access JWT for this application (closed beta).
	AccessTeamDomain string // e.g. fikua.cloudflareaccess.com
	AccessAudience   string // the Access application's AUD tag
}

// Redirect is the configuration of the redirect service.
type Redirect struct {
	Env           Env
	Addr          string
	DatabaseURL   string
	SnapshotPath  string        // local copy of the redirect table, read when the database is down
	SyncInterval  time.Duration // how often the in-memory table is refreshed from the database
	CountryHeader string        // header set by the edge proxy with the ISO country code; empty = no country
	ContactEmail  string
	PanelBaseURL  string // linked from the legal pages
	PublicBaseURL string // this service's public URL (what every QR encodes)
	SignupURL     string // landing call to action; empty while the beta is closed (asks for access by e-mail)
	Legal         LegalHolder
}

// LegalHolder identifies who provides the service (legal notice, LSSI art. 10).
// Optional while the panel is closed; required before it is public.
type LegalHolder struct {
	Name    string
	TaxID   string
	Address string
}

// Complete reports whether every field is set.
func (h LegalHolder) Complete() bool { return h.Name != "" && h.TaxID != "" && h.Address != "" }

func (r *reader) legal() LegalHolder {
	return LegalHolder{
		Name:    strings.TrimSpace(os.Getenv("LEGAL_HOLDER_NAME")),
		TaxID:   strings.TrimSpace(os.Getenv("LEGAL_HOLDER_TAX_ID")),
		Address: strings.TrimSpace(os.Getenv("LEGAL_HOLDER_ADDRESS")),
	}
}

// LoadPanel reads the panel configuration.
func LoadPanel() (Panel, error) {
	r := reader{}
	c := Panel{
		Env:              r.env(),
		Addr:             r.optional("PANEL_ADDR", ":8080"),
		DatabaseURL:      r.required("DATABASE_URL"),
		PanelBaseURL:     r.url("PANEL_BASE_URL"),
		RedirectBaseURL:  r.url("REDIRECT_BASE_URL"),
		SafeBrowsingKey:  os.Getenv("SAFE_BROWSING_API_KEY"),
		ContactEmail:     r.required("CONTACT_EMAIL"),
		Legal:            r.legal(),
		AccessTeamDomain: strings.TrimSpace(os.Getenv("CF_ACCESS_TEAM_DOMAIN")),
		AccessAudience:   strings.TrimSpace(os.Getenv("CF_ACCESS_AUD")),
	}
	if (c.AccessTeamDomain == "") != (c.AccessAudience == "") {
		r.errs = append(r.errs, errors.New("CF_ACCESS_TEAM_DOMAIN and CF_ACCESS_AUD go together: set both or neither"))
	}
	// A panel anyone can sign up to must identify its provider.
	if c.AccessTeamDomain == "" && c.Env == Prod && !c.Legal.Complete() {
		r.errs = append(r.errs, errors.New("LEGAL_HOLDER_NAME, LEGAL_HOLDER_TAX_ID and LEGAL_HOLDER_ADDRESS are required before the panel is public (no Cloudflare Access)"))
	}
	if c.Env == Prod && c.SafeBrowsingKey == "" {
		r.errs = append(r.errs, errors.New("SAFE_BROWSING_API_KEY is required in prod"))
	}
	return c, r.err()
}

// LoadRedirect reads the redirect service configuration.
func LoadRedirect() (Redirect, error) {
	r := reader{}
	c := Redirect{
		Env:           r.env(),
		Addr:          r.optional("REDIRECT_ADDR", ":8081"),
		DatabaseURL:   r.required("DATABASE_URL"),
		SnapshotPath:  r.required("SNAPSHOT_PATH"),
		SyncInterval:  r.duration("SYNC_INTERVAL", 10*time.Second),
		CountryHeader: os.Getenv("COUNTRY_HEADER"),
		ContactEmail:  r.required("CONTACT_EMAIL"),
		PanelBaseURL:  r.url("PANEL_BASE_URL"),
		PublicBaseURL: r.url("REDIRECT_BASE_URL"),
		SignupURL:     r.optionalURL("SIGNUP_URL"),
		Legal:         r.legal(),
	}
	if c.SyncInterval >= time.Minute {
		r.errs = append(r.errs, errors.New("SYNC_INTERVAL must be under 1m: a destination change must be live in less than 60 s"))
	}
	return c, r.err()
}

type reader struct{ errs []error }

func (r *reader) env() Env {
	switch v := Env(os.Getenv("APP_ENV")); v {
	case Dev, Prod:
		return v
	default:
		r.errs = append(r.errs, fmt.Errorf("APP_ENV must be %q or %q, got %q", Dev, Prod, v))
		return ""
	}
}

func (r *reader) required(key string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		r.errs = append(r.errs, fmt.Errorf("%s is required", key))
	}
	return v
}

func (r *reader) optional(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func (r *reader) url(key string) string {
	v := strings.TrimRight(r.required(key), "/")
	if v == "" {
		return v
	}
	if u, err := url.Parse(v); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		r.errs = append(r.errs, fmt.Errorf("%s must be an absolute http(s) URL, got %q", key, v))
	}
	return v
}

func (r *reader) optionalURL(key string) string {
	if strings.TrimSpace(os.Getenv(key)) == "" {
		return ""
	}
	return r.url(key)
}

func (r *reader) duration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		r.errs = append(r.errs, fmt.Errorf("%s must be a positive duration, got %q", key, v))
		return def
	}
	return d
}

func (r *reader) err() error { return errors.Join(r.errs...) }
