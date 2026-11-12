// Package safety checks destination URLs before they are stored (RF-09).
package safety

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrUnsafe means the URL is listed as malicious.
var ErrUnsafe = errors.New("unsafe url")

// ErrInvalid means the URL is not an absolute http(s) URL.
var ErrInvalid = errors.New("invalid url")

// Checker tells whether a destination URL may be used.
type Checker interface {
	Check(ctx context.Context, rawURL string) error
}

// Normalize trims the URL and checks it is an absolute http(s) URL with a host
// and no credentials (US-02).
func Normalize(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 {
		return "", ErrInvalid
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", ErrInvalid
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", ErrInvalid
	}
	if u.Host == "" || u.User != nil || strings.ContainsAny(u.Host, " \t") {
		return "", ErrInvalid
	}
	return u.String(), nil
}

// SafeBrowsing checks URLs against the Google Safe Browsing Lookup API v4.
type SafeBrowsing struct {
	APIKey string
	Client *http.Client
}

// NewSafeBrowsing returns a checker with a short timeout.
func NewSafeBrowsing(apiKey string) *SafeBrowsing {
	return &SafeBrowsing{APIKey: apiKey, Client: &http.Client{Timeout: 5 * time.Second}}
}

func (s *SafeBrowsing) Check(ctx context.Context, rawURL string) error {
	body, _ := json.Marshal(map[string]any{
		"client": map[string]string{"clientId": "qr-dinamics", "clientVersion": "0.1"},
		"threatInfo": map[string]any{
			"threatTypes":      []string{"MALWARE", "SOCIAL_ENGINEERING", "UNWANTED_SOFTWARE", "POTENTIALLY_HARMFUL_APPLICATION"},
			"platformTypes":    []string{"ANY_PLATFORM"},
			"threatEntryTypes": []string{"URL"},
			"threatEntries":    []map[string]string{{"url": rawURL}},
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://safebrowsing.googleapis.com/v4/threatMatches:find?key="+url.QueryEscape(s.APIKey), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.Client.Do(req)
	if err != nil {
		return fmt.Errorf("safe browsing request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("safe browsing: status %d", resp.StatusCode)
	}
	var out struct {
		Matches []json.RawMessage `json:"matches"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("safe browsing: decode: %w", err)
	}
	if len(out.Matches) > 0 {
		return ErrUnsafe
	}
	return nil
}

// Disabled accepts every URL. Only allowed in dev (config enforces it).
type Disabled struct{}

func (Disabled) Check(context.Context, string) error { return nil }

// New returns the Safe Browsing checker, or Disabled when no key is set.
func New(apiKey string) Checker {
	if apiKey == "" {
		slog.Warn("SAFE_BROWSING_API_KEY not set: destination URLs are not checked (dev only)")
		return Disabled{}
	}
	return NewSafeBrowsing(apiKey)
}
