package brand

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRoutes(t *testing.T) {
	mux := http.NewServeMux()
	Routes(mux)
	for path, ctype := range map[string]string{
		"/brand/brand.css":              "text/css",
		"/brand/inter-latin-wght.woff2": "font/woff2",
		"/brand/fikua-icon.svg":         "image/svg+xml",
	} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), ctype) {
			t.Errorf("%s: %d %q", path, w.Code, w.Header().Get("Content-Type"))
		}
	}
	if !strings.Contains(FontLicense(), "SIL Open Font License") {
		t.Fatal("font licence")
	}
}
