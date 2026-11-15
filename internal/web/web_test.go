package web

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRendererParsesEveryPage(t *testing.T) {
	r, err := NewRenderer("hola@example.com")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"login", "register", "verified", "not_found"} {
		w := httptest.NewRecorder()
		r.Render(w, httptest.NewRequest("GET", "/", nil), 200, name, Page{Title: "t", Error: "<b>x</b>"})
		if w.Code != 200 {
			t.Fatalf("%s: status %d: %s", name, w.Code, w.Body)
		}
		if body := w.Body.String(); !strings.Contains(body, "&lt;b&gt;x&lt;/b&gt;") {
			t.Fatalf("%s: error message not escaped", name)
		}
	}
	for _, name := range []string{"qr_list", "qr_new", "qr_detail", "account", "account_deleted"} {
		if _, ok := r.pages[name]; !ok {
			t.Fatalf("page %s not parsed", name)
		}
	}
}

func TestRendersInTheRequestLanguage(t *testing.T) {
	r, err := NewRenderer("hola@example.com")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ accept, lang, want string }{
		{"", "ca", "Inicia la sessió"},
		{"es-ES,es;q=0.9", "es", "Inicia sesión"},
		{"en-US,en;q=0.9", "en", "Sign in"},
	} {
		req := httptest.NewRequest("GET", "/login", nil)
		if tc.accept != "" {
			req.Header.Set("Accept-Language", tc.accept)
		}
		w := httptest.NewRecorder()
		r.Render(w, req, 200, "login", Page{Title: "Inicia la sessió", Error: "La contrasenya no és correcta."})
		body := w.Body.String()
		if !strings.Contains(body, `<html lang="`+tc.lang+`">`) || !strings.Contains(body, "<h1>"+tc.want+"</h1>") {
			t.Errorf("%s: wrong language: %s", tc.lang, body)
		}
		if tc.lang != "ca" && strings.Contains(body, "no és correcta") {
			t.Errorf("%s: error message not translated", tc.lang)
		}
		if !strings.Contains(w.Header().Get("Vary"), "Accept-Language") {
			t.Error("missing Vary")
		}
	}
}

func TestEveryPageRendersInEveryLanguage(t *testing.T) {
	r, err := NewRenderer("hola@example.com")
	if err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"es", "en"} {
		for _, name := range []string{"login", "register", "verified", "not_found", "account_deleted"} {
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("Accept-Language", lang)
			w := httptest.NewRecorder()
			r.Render(w, req, 200, name, Page{Title: "t"})
			if w.Code != 200 || strings.Contains(w.Body.String(), "Torna") || strings.Contains(w.Body.String(), "Inicia la sessió") {
				t.Errorf("%s/%s: %d, Catalan left over", lang, name, w.Code)
			}
		}
	}
}
