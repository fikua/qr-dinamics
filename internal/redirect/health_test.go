package redirect

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHealthIsOKForALoadedEmptyTable(t *testing.T) {
	table := NewTable()
	h := &Handler{Table: table, Recorder: NewRecorder(&fakeSink{}, func(string, error) {}), Now: time.Now}
	mux := http.NewServeMux()
	h.Routes(mux)

	health := func() int {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", "/health", nil))
		return w.Code
	}
	if got := health(); got != http.StatusServiceUnavailable {
		t.Fatalf("never loaded: health = %d, want 503", got)
	}
	table.Replace(nil) // a full reload that found no codes
	if got := health(); got != http.StatusOK {
		t.Fatalf("loaded, no codes: health = %d, want 200", got)
	}
}
