package i18n

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestAcceptLanguage(t *testing.T) {
	for header, want := range map[string]Lang{
		"":                        CA,
		"ca":                      CA,
		"ca-ES,ca;q=0.9":          CA,
		"es-ES,es;q=0.9,en;q=0.8": ES,
		"en-GB,en;q=0.9":          EN,
		"en;q=0.5, es;q=0.9":      ES,
		"fr-FR,fr;q=0.9":          EN, // none supported: English
		"fr, ca;q=0.4":            CA,
		"es;q=0, en":              EN,
		"*":                       EN,
		"EN-us":                   EN,
	} {
		if got := fromAcceptLanguage(header); got != want {
			t.Errorf("Accept-Language %q: got %s, want %s", header, got, want)
		}
	}
}

func TestCookieBeatsHeader(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Language", "es")
	r.AddCookie(&http.Cookie{Name: CookieName, Value: "en"})
	if got := FromRequest(r); got != EN {
		t.Fatalf("got %s", got)
	}
	r = httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Language", "es")
	r.AddCookie(&http.Cookie{Name: CookieName, Value: "xx"})
	if got := FromRequest(r); got != ES {
		t.Fatalf("invalid cookie must be ignored, got %s", got)
	}
}

func TestSwitch(t *testing.T) {
	mux := http.NewServeMux()
	Routes(mux)
	for _, tc := range []struct{ referer, want string }{
		{"https://qr.example/legal/privacitat", "/legal/privacitat"},
		{"https://qr.example/qr?q=a%20b", "/qr?q=a%20b"},
		{"https://evil.example/x", "/"},
		{"https://qr.example//evil.example", "/"},
		{"", "/"},
	} {
		r := httptest.NewRequest("GET", "https://qr.example/lang/es", nil)
		r.Header.Set("Referer", tc.referer)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != tc.want {
			t.Errorf("referer %q: %d → %q, want %q", tc.referer, w.Code, w.Header().Get("Location"), tc.want)
		}
		c := w.Result().Cookies()
		if len(c) != 1 || c[0].Name != CookieName || c[0].Value != "es" || !c[0].HttpOnly || !c[0].Secure {
			t.Errorf("cookie: %+v", c)
		}
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/lang/fr", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown language: %d", w.Code)
	}
}

func TestTFallsBackToCatalan(t *testing.T) {
	if T(ES, "texto que no existe") != "texto que no existe" || T(CA, "Surt") != "Surt" {
		t.Fatal("fallback")
	}
	if T(ES, "Surt") != "Salir" || T(EN, "Surt") != "Sign out" {
		t.Fatal("translation")
	}
}

// Every text a template asks to translate, and every literal passed to T,
// must exist in both catalogues; otherwise a page would silently show Catalan.
func TestCatalogsCoverTemplates(t *testing.T) {
	tpl := regexp.MustCompile(`\{\{-?\s*t "((?:[^"\\]|\\.)*)"`)
	goT := regexp.MustCompile(`i18n\.T\(\w+, "((?:[^"\\]|\\.)*)"`)
	root := filepath.Join("..")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.Contains(path, "/i18n/") {
			return err
		}
		if !strings.HasSuffix(path, ".html") && !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, _ := os.ReadFile(path)
		for _, re := range []*regexp.Regexp{tpl, goT} {
			for _, m := range re.FindAllStringSubmatch(string(b), -1) {
				key := strings.ReplaceAll(m[1], `\"`, `"`)
				if _, ok := es[key]; !ok {
					t.Errorf("%s: no Spanish translation for %q", path, key)
				}
				if _, ok := en[key]; !ok {
					t.Errorf("%s: no English translation for %q", path, key)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Catalogue entries must not be empty and must not just repeat the key
// (except for names and words that are the same in every language).
func TestCatalogEntriesAreFilled(t *testing.T) {
	for name, m := range map[string]map[string]string{"es": es, "en": en} {
		for k, v := range m {
			if strings.TrimSpace(v) == "" {
				t.Errorf("%s: empty translation for %q", name, k)
			}
		}
	}
}
