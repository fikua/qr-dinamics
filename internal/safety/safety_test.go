package safety

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	ok := []string{"https://example.com", " http://example.com/carta?x=1 ", "https://bar.cat/menú"}
	for _, in := range ok {
		if _, err := Normalize(in); err != nil {
			t.Errorf("Normalize(%q) = %v, want ok", in, err)
		}
	}
	bad := []string{"", "example.com", "ftp://example.com", "javascript:alert(1)", "https://", "https://user:pw@example.com", "https://" + strings.Repeat("a", 2100)}
	for _, in := range bad {
		if _, err := Normalize(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("Normalize(%q) = %v, want ErrInvalid", in, err)
		}
	}
}

func TestSafeBrowsing(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		want       error
	}{
		{"clean", `{}`, 200, nil},
		{"match", `{"matches":[{"threatType":"MALWARE"}]}`, 200, ErrUnsafe},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			sb := NewSafeBrowsing("k")
			sb.Client = srv.Client()
			sb.Client.Transport = rewrite{srv.URL}
			if err := sb.Check(context.Background(), "https://example.com"); !errors.Is(err, tc.want) {
				t.Fatalf("Check = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestSafeBrowsingErrorIsNotUnsafe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	sb := NewSafeBrowsing("k")
	sb.Client.Transport = rewrite{srv.URL}
	err := sb.Check(context.Background(), "https://example.com")
	if err == nil || errors.Is(err, ErrUnsafe) {
		t.Fatalf("Check = %v, want a non-ErrUnsafe error", err)
	}
}

// rewrite sends every request to the test server.
type rewrite struct{ base string }

func (rw rewrite) RoundTrip(r *http.Request) (*http.Response, error) {
	req := r.Clone(r.Context())
	u, _ := req.URL.Parse(rw.base + r.URL.Path + "?" + r.URL.RawQuery)
	req.URL = u
	req.Host = u.Host
	return http.DefaultTransport.RoundTrip(req)
}
