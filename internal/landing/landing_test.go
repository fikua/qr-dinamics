package landing

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func get(t *testing.T, info Info, path string) (int, string, http.Header) {
	t.Helper()
	h, err := NewHandler(info)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Routes(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	return w.Code, w.Body.String(), w.Header()
}

func TestClosedBetaAsksForAccess(t *testing.T) {
	code, body, h := get(t, Info{PublicURL: "https://qr.test", ContactEmail: "hola@example.com"}, "/")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	for _, want := range []string{"Demana accés a la beta", "mailto:hola@example.com?subject=", `href="https://qr.test/"`, "/legal/privacitat", `alt="Codi QR`} {
		if !strings.Contains(body, want) {
			t.Errorf("landing lacks %q", want)
		}
	}
	if strings.Contains(body, "Crea el teu QR gratis") {
		t.Error("closed beta must not offer sign-up")
	}
	if h.Get("Set-Cookie") != "" || !strings.Contains(h.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Error("landing must set no cookies and a strict CSP")
	}
}

func TestOpenSignup(t *testing.T) {
	_, body, _ := get(t, Info{PublicURL: "https://qr.test", SignupURL: "https://app.test/register", ContactEmail: "a@b.c"}, "/")
	if !strings.Contains(body, `href="https://app.test/register"`) || strings.Contains(body, "Demana accés a la beta") {
		t.Fatal("open sign-up must link to the panel")
	}
}

// Guard against claims the product cannot back (consumer-protection rules).
func TestNoUnbackedClaims(t *testing.T) {
	_, body, _ := get(t, Info{PublicURL: "https://qr.test", ContactEmail: "a@b.c"}, "/")
	lower := strings.ToLower(body)
	for _, banned := range []string{"per sempre", "mai no deixa", "garantit", "garantia", "99,9", "soc 2", "milers de clients", "★", "només avui", "oferta acaba"} {
		if strings.Contains(lower, banned) {
			t.Errorf("landing contains %q", banned)
		}
	}
}

func TestAssets(t *testing.T) {
	info := Info{PublicURL: "https://qr.test", ContactEmail: "a@b.c"}
	if code, body, h := get(t, info, "/landing/qr.svg"); code != 200 || !strings.HasPrefix(body, "<svg") || h.Get("Content-Type") != "image/svg+xml" {
		t.Fatal("demo qr")
	}
	if code, body, _ := get(t, info, "/landing/landing.css"); code != 200 || !strings.Contains(body, ".hero") {
		t.Fatal("css")
	}
}

func TestAssetURLsAreVersioned(t *testing.T) {
	_, body, _ := get(t, Info{PublicURL: "https://qr.test", ContactEmail: "a@b.c"}, "/")
	for _, ref := range []string{`/brand/brand.css?v=`, `/landing/landing.css?v=`, "<!--email_off-->", "<!--/email_off-->"} {
		if !strings.Contains(body, ref) {
			t.Errorf("unversioned asset: want %q", ref)
		}
	}
}

func getLang(t *testing.T, acceptLanguage, cookie string) (string, http.Header) {
	t.Helper()
	h, err := NewHandler(Info{PublicURL: "https://qr.test", ContactEmail: "a@b.c"})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Routes(mux)
	r := httptest.NewRequest("GET", "/", nil)
	if acceptLanguage != "" {
		r.Header.Set("Accept-Language", acceptLanguage)
	}
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: "lang", Value: cookie})
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w.Body.String(), w.Header()
}

func TestLandingFollowsTheBrowserLanguage(t *testing.T) {
	for _, tc := range []struct{ accept, cookie, lang, want string }{
		{"", "", "ca", "Demana accés a la beta"},
		{"es-ES,es;q=0.9", "", "es", "Pide acceso a la beta"},
		{"en-GB,en;q=0.9", "", "en", "Request beta access"},
		{"fr-FR", "", "en", "Request beta access"},
		{"es-ES", "en", "en", "Request beta access"}, // a manual choice wins
		{"en", "ca", "ca", "Demana accés a la beta"},
	} {
		body, h := getLang(t, tc.accept, tc.cookie)
		if !strings.Contains(body, `<html lang="`+tc.lang+`">`) || !strings.Contains(body, tc.want) {
			t.Errorf("accept %q cookie %q: want lang %s with %q", tc.accept, tc.cookie, tc.lang, tc.want)
		}
		if !strings.Contains(h.Get("Vary"), "Accept-Language") || !strings.HasPrefix(h.Get("Cache-Control"), "private") {
			t.Errorf("the page varies by language and must not be shared-cached: %v", h)
		}
		for _, l := range []string{"/lang/ca", "/lang/es", "/lang/en"} {
			if !strings.Contains(body, `href="`+l+`"`) {
				t.Errorf("language selector lacks %s", l)
			}
		}
	}
}

// The beta e-mail link is in the visitor's language too.
func TestBetaMailtoIsTranslated(t *testing.T) {
	body, _ := getLang(t, "en", "")
	if !strings.Contains(body, "Access%20to%20the%20QR%20din") {
		t.Fatalf("English mailto subject missing")
	}
}

// Nothing of the other languages may leak into a translated page.
func TestNoCatalanLeftInTranslations(t *testing.T) {
	for _, accept := range []string{"es", "en"} {
		body, _ := getLang(t, accept, "")
		for _, cat := range []string{"Com funciona", "Demana accés", "Preguntes", "Per a qui", "Restaurants i bars", "Escaneja'm"} {
			if strings.Contains(body, cat) {
				t.Errorf("%s landing still has Catalan %q", accept, cat)
			}
		}
	}
}
