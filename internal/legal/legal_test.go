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
		if !strings.Contains(body, "/brand/brand.css?v=") || !strings.Contains(body, "/legal/legal.css?v=") {
			t.Fatalf("%s: stylesheet URLs must be versioned (stale CDN cache)", p.Slug)
		}
		if !strings.Contains(body, "<!--email_off-->") || !strings.Contains(body, "<!--/email_off-->") {
			t.Fatalf("%s: contact addresses must be excluded from Cloudflare e-mail obfuscation", p.Slug)
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
	if !strings.Contains(body, "Nom Cognom, amb NIF 00000000T i domicili a Carrer 1, Barcelona.</p>") || strings.Contains(body, "pendents") {
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

func serveLang(t *testing.T, info Info, path, accept string) (int, string) {
	t.Helper()
	h, err := NewHandler(info)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Routes(mux)
	r := httptest.NewRequest("GET", path, nil)
	r.Header.Set("Accept-Language", accept)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w.Code, w.Body.String()
}

func TestPagesInEveryLanguage(t *testing.T) {
	info := Info{ContactEmail: "hola@example.com", PanelURL: "https://app.test", RedirectURL: "https://qr.test"}
	titles := map[string]map[string]string{
		"es": {"avis-legal": "Aviso legal", "privacitat": "Política de privacidad", "termes": "Términos y condiciones", "cookies": "Política de cookies", "llicencies": "Licencias de terceros"},
		"en": {"avis-legal": "Legal notice", "privacitat": "Privacy policy", "termes": "Terms and conditions", "cookies": "Cookie policy", "llicencies": "Third-party licences"},
	}
	for lang, bySlug := range titles {
		for slug, title := range bySlug {
			code, body := serveLang(t, info, "/legal/"+slug, lang)
			if code != http.StatusOK {
				t.Fatalf("%s/%s: status %d", lang, slug, code)
			}
			if !strings.Contains(body, "<h1>"+title+"</h1>") || !strings.Contains(body, `<html lang="`+lang+`">`) {
				t.Errorf("%s/%s: wrong title or lang", lang, slug)
			}
			if !strings.Contains(body, `class="translation-note"`) || !strings.Contains(strings.ToLower(body), "catal") {
				t.Errorf("%s/%s: translations must say that Catalan prevails", lang, slug)
			}
		}
	}
	_, ca := serveLang(t, info, "/legal/privacitat", "ca")
	if strings.Contains(ca, "translation-note") {
		t.Error("the Catalan original needs no translation note")
	}
}

// Translated pages must keep the data-driven numbers and links of the original.
func TestTranslatedPagesKeepFacts(t *testing.T) {
	info := Info{ContactEmail: "hola@example.com", PanelURL: "https://app.test", RedirectURL: "https://qr.test", SafeBrowsingEnabled: true}
	for _, lang := range []string{"ca", "es", "en"} {
		_, body := serveLang(t, info, "/legal/privacitat", lang)
		for _, want := range []string{"24", "30", "https://app.test/compte", "Safe Browsing", "hola@example.com"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s privacy page lacks %q", lang, want)
			}
		}
		_, ck := serveLang(t, info, "/legal/cookies", lang)
		if !strings.Contains(ck, "<code>lang</code>") || !strings.Contains(ck, "<code>sid</code>") {
			t.Errorf("%s cookie policy must list the lang and sid cookies", lang)
		}
	}
}
