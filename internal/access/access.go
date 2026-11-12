// Package access restricts the panel to users signed in through Cloudflare
// Access. Cloudflare blocks unauthenticated requests at the edge, but the
// origin is reachable directly, so the panel also verifies the signed JWT
// Cloudflare adds to every request it lets through.
package access

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

const header = "Cf-Access-Jwt-Assertion"

// Verifier checks Cloudflare Access JWTs for one application.
type Verifier struct {
	issuer   string // https://<team>.cloudflareaccess.com
	audience string // the Access application's AUD tag
	client   *http.Client
	now      func() time.Time

	mu        sync.Mutex
	keys      jose.JSONWebKeySet
	fetchedAt time.Time
}

// NewVerifier builds a verifier for teamDomain (e.g. fikua.cloudflareaccess.com).
func NewVerifier(teamDomain, audience string) *Verifier {
	return &Verifier{
		issuer:   "https://" + teamDomain,
		audience: audience,
		client:   &http.Client{Timeout: 5 * time.Second},
		now:      time.Now,
	}
}

// Middleware rejects requests without a valid Access JWT.
func (v *Verifier) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := v.Verify(r.Context(), r.Header.Get(header)); err != nil {
			slog.Warn("cloudflare access check failed", "err", err, "path", r.URL.Path)
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Verify checks the token's signature, issuer, audience and validity period.
func (v *Verifier) Verify(ctx context.Context, token string) error {
	if token == "" {
		return errors.New("missing access token")
	}
	tok, err := jwt.ParseSigned(token, []jose.SignatureAlgorithm{jose.RS256})
	if err != nil {
		return fmt.Errorf("parse token: %w", err)
	}
	if len(tok.Headers) == 0 || tok.Headers[0].KeyID == "" {
		return errors.New("token without key id")
	}
	key, err := v.key(ctx, tok.Headers[0].KeyID)
	if err != nil {
		return err
	}
	var claims jwt.Claims
	if err := tok.Claims(key, &claims); err != nil {
		return fmt.Errorf("verify signature: %w", err)
	}
	return claims.ValidateWithLeeway(jwt.Expected{
		Issuer:      v.issuer,
		AnyAudience: jwt.Audience{v.audience},
		Time:        v.now(),
	}, 30*time.Second)
}

// key returns the public key with kid, refreshing the key set when the kid is
// unknown (Cloudflare rotates keys) at most once a minute.
func (v *Verifier) key(ctx context.Context, kid string) (*jose.JSONWebKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if k := v.keys.Key(kid); len(k) > 0 {
		return &k[0], nil
	}
	if v.now().Sub(v.fetchedAt) < time.Minute {
		return nil, fmt.Errorf("unknown key id %q", kid)
	}
	keys, err := v.fetch(ctx)
	v.fetchedAt = v.now()
	if err != nil {
		return nil, err
	}
	v.keys = keys
	if k := v.keys.Key(kid); len(k) > 0 {
		return &k[0], nil
	}
	return nil, fmt.Errorf("unknown key id %q", kid)
}

func (v *Verifier) fetch(ctx context.Context) (jose.JSONWebKeySet, error) {
	var set jose.JSONWebKeySet
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.issuer+"/cdn-cgi/access/certs", nil)
	if err != nil {
		return set, err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return set, fmt.Errorf("fetch access certs: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return set, fmt.Errorf("fetch access certs: status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		return set, fmt.Errorf("decode access certs: %w", err)
	}
	return set, nil
}
