package legal

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"qrdinamics/internal/accounts"
	"qrdinamics/internal/privacy"
)

func serve(t *testing.T, info Info, path string) (int, string, http.Header) {
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

func TestPagesRender(t *testing.T) {
	info := Info{ContactEmail: "hola@example.com", PanelURL: "https://app.test", RedirectURL: "https://qr.test"}
	for _, p := range Pages {
		code, body, h := serve(t, info, "/legal/"+p.Slug)
		if code != http.StatusOK {
			t.Fatalf("%s: status %d", p.Slug, code)
		}
		if !strings.Contains(body, "<h1>"+p.Title+"</h1>") || !strings.Contains(body, "hola@example.com") {
			t.Fatalf("%s: missing title or contact", p.Slug)
		}
		if h.Get("Content-Security-Policy") == "" {
			t.Fatalf("%s: no CSP", p.Slug)
		}
	}
	if code, _, _ := serve(t, info, "/legal/nope"); code != http.StatusNotFound {
		t.Fatalf("unknown page: %d", code)
	}
	if code, _, h := serve(t, info, "/legal/"); code != http.StatusSeeOther || h.Get("Location") != "/legal/avis-legal" {
		t.Fatalf("/legal/: %d %q", code, h.Get("Location"))
	}
	if code, body, _ := serve(t, info, "/legal/legal.css"); code != http.StatusOK || !strings.Contains(body, "article") {
		t.Fatal("css")
	}
}

func TestHolder(t *testing.T) {
	_, body, _ := serve(t, Info{ContactEmail: "a@b.c"}, "/legal/avis-legal")
	if !strings.Contains(body, "pendents de publicació") {
		t.Fatal("incomplete holder must be flagged as pending")
	}
	h := Holder{Name: "Nom Cognom", TaxID: "00000000T", Address: "Carrer 1, Barcelona"}
	if !h.Complete() || (Holder{Name: "x"}).Complete() {
		t.Fatal("Complete()")
	}
	_, body, _ = serve(t, Info{Holder: h, ContactEmail: "a@b.c"}, "/legal/avis-legal")
	if !strings.Contains(body, "Nom Cognom, amb NIF 00000000T") || strings.Contains(body, "pendents") {
		t.Fatal("complete holder not shown")
	}
}

func TestSafeBrowsingProcessorListedOnlyWhenEnabled(t *testing.T) {
	_, off, _ := serve(t, Info{}, "/legal/privacitat")
	_, on, _ := serve(t, Info{SafeBrowsingEnabled: true}, "/legal/privacitat")
	if strings.Contains(off, "Safe Browsing") || !strings.Contains(on, "Safe Browsing") {
		t.Fatal("Safe Browsing must be listed as a processor exactly when it is used")
	}
}

// The published texts must match what the code does.
func TestPublishedFactsMatchCode(t *testing.T) {
	if Version != accounts.LegalVersion {
		t.Fatalf("legal.Version %q != accounts.LegalVersion %q", Version, accounts.LegalVersion)
	}
	if got := time.Duration(ScanRetentionMonths) * 30 * 24 * time.Hour; got != privacy.ScanRetention {
		t.Fatalf("scan retention: page says %v, code purges after %v", got, privacy.ScanRetention)
	}
	if got := time.Duration(UnverifiedAccountDays) * 24 * time.Hour; got != privacy.UnverifiedRetention {
		t.Fatalf("unverified accounts: page says %v, code %v", got, privacy.UnverifiedRetention)
	}
	_, body, _ := serve(t, Info{}, "/legal/llicencies")
	if !strings.Contains(body, "SIL Open Font License") {
		t.Fatal("the Inter font licence must be shown")
	}
	if !strings.Contains(thirdPartyLicenses, "github.com/jackc/pgx/v5") || !strings.Contains(thirdPartyLicenses, "Apache License") {
		t.Fatal("third-party licences file looks incomplete; run make licenses")
	}
}
