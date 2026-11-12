package access

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

type fixture struct {
	key      *rsa.PrivateKey
	verifier *Verifier
	now      time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cdn-cgi/access/certs" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	}))
	t.Cleanup(srv.Close)
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	v := NewVerifier(strings.TrimPrefix(srv.URL, "https://"), "aud-1")
	v.client = srv.Client()
	v.now = func() time.Time { return now }
	return &fixture{key: key, verifier: v, now: now}
}

func (f *fixture) token(t *testing.T, kid string, claims jwt.Claims) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: f.key},
		(&jose.SignerOptions{}).WithHeader("kid", kid))
	if err != nil {
		t.Fatal(err)
	}
	s, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (f *fixture) claims() jwt.Claims {
	return jwt.Claims{
		Issuer:   f.verifier.issuer,
		Audience: jwt.Audience{"aud-1"},
		IssuedAt: jwt.NewNumericDate(f.now.Add(-time.Minute)),
		Expiry:   jwt.NewNumericDate(f.now.Add(time.Hour)),
	}
}

func TestVerify(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if err := f.verifier.Verify(ctx, f.token(t, "k1", f.claims())); err != nil {
		t.Fatalf("valid token: %v", err)
	}

	wrongAud := f.claims()
	wrongAud.Audience = jwt.Audience{"other-app"}
	expired := f.claims()
	expired.Expiry = jwt.NewNumericDate(f.now.Add(-time.Hour))
	wrongIss := f.claims()
	wrongIss.Issuer = "https://evil.cloudflareaccess.com"

	for name, tok := range map[string]string{
		"empty":          "",
		"garbage":        "not-a-jwt",
		"wrong audience": f.token(t, "k1", wrongAud),
		"expired":        f.token(t, "k1", expired),
		"wrong issuer":   f.token(t, "k1", wrongIss),
		"unknown kid":    f.token(t, "k2", f.claims()),
	} {
		if err := f.verifier.Verify(ctx, tok); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	f.key = other
	if err := f.verifier.Verify(ctx, f.token(t, "k1", f.claims())); err == nil {
		t.Error("token signed with another key: accepted")
	}
}

func TestMiddleware(t *testing.T) {
	f := newFixture(t)
	h := f.verifier.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/qr", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("without token: %d, want 403", w.Code)
	}

	req := httptest.NewRequest("GET", "/qr", nil)
	req.Header.Set(header, f.token(t, "k1", f.claims()))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 204 {
		t.Fatalf("with token: %d, want 204", w.Code)
	}
}
