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
		r.Render(w, 200, name, Page{Title: "t", Error: "<b>x</b>"})
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
