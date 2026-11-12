package panel

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"

	"qrdinamics/internal/config"
	"qrdinamics/internal/mail"
	"qrdinamics/internal/privacy"
	"qrdinamics/internal/safety"
	"qrdinamics/internal/testdb"
)

type outbox struct {
	mu   sync.Mutex
	msgs []mail.Message
}

func (o *outbox) Send(_ context.Context, m mail.Message) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.msgs = append(o.msgs, m)
	return nil
}

func (o *outbox) lastLink(t *testing.T) string {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.msgs) == 0 {
		t.Fatal("no e-mail sent")
	}
	m := regexp.MustCompile(`https?://\S+/verify\?token=\S+`).FindString(o.msgs[len(o.msgs)-1].Body)
	if m == "" {
		t.Fatal("no verification link in the e-mail")
	}
	return m
}

type denyList map[string]bool

func (d denyList) Check(_ context.Context, u string) error {
	if d[u] {
		return safety.ErrUnsafe
	}
	return nil
}

type client struct {
	t   *testing.T
	srv *httptest.Server
	c   *http.Client
}

func newClient(t *testing.T, srv *httptest.Server) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, srv: srv, c: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (c *client) do(method, path string, form url.Values) (int, string, http.Header) {
	c.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, c.srv.URL+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.c.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func (c *client) get(path string) (int, string, http.Header) { return c.do("GET", path, nil) }
func (c *client) post(path string, form url.Values) (int, string, http.Header) {
	return c.do("POST", path, form)
}

func expect(t *testing.T, what string, got, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: status %d, want %d", what, got, want)
	}
}

func contains(t *testing.T, what, body string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(body, s) {
			t.Fatalf("%s: body lacks %q", what, s)
		}
	}
}

func setup(t *testing.T) (*client, *outbox, *Panel) {
	t.Helper()
	pool := testdb.Open(t)
	box := &outbox{}
	cfg := config.Panel{
		Env: config.Dev, PanelBaseURL: "http://panel.test", RedirectBaseURL: "https://qr.test",
		ContactEmail: "hola@example.com",
	}
	p, err := New(cfg, Deps{DB: pool, Mail: box, Checker: denyList{"https://phish.example/": true}})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(p.Handler)
	t.Cleanup(srv.Close)
	return newClient(t, srv), box, p
}

func TestOwnerJourney(t *testing.T) {
	c, box, _ := setup(t)

	// Anonymous: redirected to login; legal pages are public.
	code, _, h := c.get("/qr")
	expect(t, "anonymous /qr", code, http.StatusSeeOther)
	if h.Get("Location") != "/login" {
		t.Fatalf("anonymous /qr -> %q", h.Get("Location"))
	}
	for _, p := range []string{"/legal/avis-legal", "/legal/privacitat", "/legal/termes", "/legal/cookies", "/legal/llicencies"} {
		code, _, _ := c.get(p)
		expect(t, p, code, http.StatusOK)
	}
	code, body, _ := c.get("/register")
	expect(t, "register form", code, http.StatusOK)
	contains(t, "register form", body, "/brand/brand.css?v=", "/static/app.css?v=", `name="accept_terms" required`, `name="marketing">`, "Salta al contingut", "/legal/privacitat")
	if strings.Contains(body, `name="accept_terms" checked`) {
		t.Fatal("terms checkbox must not be pre-ticked")
	}

	// Sign-up needs the terms ticked.
	code, body, _ = c.post("/register", url.Values{"email": {"bar@example.com"}, "password": {"una-contrasenya"}})
	expect(t, "register without terms", code, http.StatusUnprocessableEntity)
	contains(t, "register without terms", body, "acceptar els termes")
	for _, f := range []url.Values{
		{"email": {"no-es-correu"}, "password": {"una-contrasenya"}, "accept_terms": {"on"}},
		{"email": {"x@example.com"}, "password": {"curta"}, "accept_terms": {"on"}},
	} {
		code, _, _ = c.post("/register", f)
		expect(t, "invalid register", code, http.StatusUnprocessableEntity)
	}
	code, _, _ = c.post("/register", url.Values{"email": {"bar@example.com"}, "password": {"una-contrasenya"}, "accept_terms": {"on"}})
	expect(t, "register", code, http.StatusSeeOther)
	code, _, _ = newClient(t, c.srv).post("/register", url.Values{"email": {"bar@example.com"}, "password": {"una-contrasenya"}, "accept_terms": {"on"}})
	expect(t, "register twice", code, http.StatusUnprocessableEntity)

	// Unverified: cannot create QR codes; can ask for a new e-mail.
	code, body, _ = c.get("/qr")
	expect(t, "unverified list", code, http.StatusOK)
	contains(t, "unverified list", body, "Confirma el teu correu")
	code, _, h = c.get("/qr/new")
	expect(t, "unverified new", code, http.StatusSeeOther)
	code, _, h = c.post("/verify/resend", url.Values{})
	expect(t, "resend", code, http.StatusSeeOther)
	code, body, _ = c.get(h.Get("Location"))
	contains(t, "after resend", body, "correu nou de confirmació")

	// Verify.
	link, _ := url.Parse(box.lastLink(t))
	code, body, _ = c.get("/verify?" + link.RawQuery)
	expect(t, "verify", code, http.StatusOK)
	contains(t, "verify", body, "Correu confirmat")
	code, _, _ = c.get("/verify?token=nope")
	expect(t, "bad token", code, http.StatusBadRequest)

	// Create.
	code, body, _ = c.get("/qr/new")
	expect(t, "new form", code, http.StatusOK)
	code, body, _ = c.post("/qr/new", url.Values{"destination": {"javascript:alert(1)"}})
	expect(t, "invalid destination", code, http.StatusUnprocessableEntity)
	contains(t, "invalid destination", body, "http://")
	code, body, _ = c.post("/qr/new", url.Values{"destination": {"https://phish.example/"}})
	expect(t, "unsafe destination", code, http.StatusUnprocessableEntity)
	contains(t, "unsafe destination", body, "perillosos")
	code, _, _ = c.post("/qr/new", url.Values{"name": {strings.Repeat("x", 81)}, "destination": {"https://example.com"}})
	expect(t, "long name", code, http.StatusUnprocessableEntity)
	code, _, h = c.post("/qr/new", url.Values{"name": {"=Carta"}, "destination": {"https://example.com/estiu"}})
	expect(t, "create", code, http.StatusSeeOther)
	detail := strings.Split(h.Get("Location"), "?")[0]

	code, body, _ = c.get(h.Get("Location"))
	expect(t, "detail", code, http.StatusOK)
	contains(t, "detail", body, "QR creat", "https://qr.test/", `alt="Codi QR de https://qr.test/`, "(inicial)")

	// Downloads.
	code, body, hdr := c.get(detail + "/qr.png")
	expect(t, "png", code, http.StatusOK)
	if hdr.Get("Content-Type") != "image/png" || !strings.Contains(hdr.Get("Content-Disposition"), "attachment") {
		t.Fatalf("png headers %v", hdr)
	}
	code, body, hdr = c.get(detail + "/qr.svg?inline")
	expect(t, "svg", code, http.StatusOK)
	if hdr.Get("Content-Disposition") != "" || !strings.HasPrefix(body, "<svg") {
		t.Fatal("inline svg")
	}

	// Edit, rename, pause, resume.
	code, h2body, _ := c.post(detail+"/destination", url.Values{"destination": {"nope"}})
	expect(t, "bad edit", code, http.StatusUnprocessableEntity)
	contains(t, "bad edit", h2body, "http://")
	code, _, h = c.post(detail+"/destination", url.Values{"destination": {"https://example.com/hivern"}})
	expect(t, "edit", code, http.StatusSeeOther)
	_, body, _ = c.get(h.Get("Location"))
	contains(t, "after edit", body, "Destí actualitzat", "https://example.com/hivern")
	code, _, _ = c.post(detail+"/name", url.Values{"name": {strings.Repeat("y", 81)}})
	expect(t, "long rename", code, http.StatusUnprocessableEntity)
	code, _, _ = c.post(detail+"/name", url.Values{"name": {"Carta d'hivern"}})
	expect(t, "rename", code, http.StatusSeeOther)
	code, _, h = c.post(detail+"/pause", url.Values{})
	expect(t, "pause", code, http.StatusSeeOther)
	_, body, _ = c.get(h.Get("Location"))
	contains(t, "paused", body, "QR pausat", "Reactiva el QR")
	code, _, _ = c.post(detail+"/pause", url.Values{})
	expect(t, "pause twice", code, http.StatusUnprocessableEntity)
	code, _, h = c.post(detail+"/resume", url.Values{})
	expect(t, "resume", code, http.StatusSeeOther)
	_, body, _ = c.get(h.Get("Location"))
	contains(t, "resumed", body, "QR reactivat")

	// List and search.
	_, body, _ = c.get("/qr")
	contains(t, "list", body, "Carta d&#39;hivern", "1 de 3 QR")
	_, body, _ = c.get("/qr?q=zzz")
	contains(t, "empty search", body, "Cap QR coincideix")

	// Someone else's QR, and nonsense ids.
	other := newClient(t, c.srv)
	other.post("/register", url.Values{"email": {"altre@example.com"}, "password": {"una-contrasenya"}, "accept_terms": {"on"}})
	for _, p := range []string{detail, detail + "/qr.png"} {
		code, _, _ = other.get(p)
		expect(t, "other owner "+p, code, http.StatusNotFound)
	}
	code, _, _ = other.post(detail+"/destination", url.Values{"destination": {"https://evil.example"}})
	expect(t, "other owner edit", code, http.StatusNotFound)
	for _, p := range []string{"/qr/abc", "/qr/0", "/qr/abc/qr.svg"} {
		code, _, _ = c.get(p)
		expect(t, p, code, http.StatusNotFound)
	}

	// Delete a second QR: its slot is freed.
	_, _, h = c.post("/qr/new", url.Values{"destination": {"https://example.com/temporal"}})
	tmp := strings.Split(h.Get("Location"), "?")[0]
	code, _, h = c.post(tmp+"/delete", url.Values{})
	expect(t, "delete qr", code, http.StatusSeeOther)
	_, body, _ = c.get(h.Get("Location"))
	contains(t, "after qr delete", body, "QR esborrat", "1 de 3 QR")
	code, _, _ = c.post(tmp+"/delete", url.Values{})
	expect(t, "delete qr twice", code, http.StatusNotFound)

	// Account page, marketing consent.
	code, body, _ = c.get("/compte")
	expect(t, "account", code, http.StatusOK)
	contains(t, "account", body, "bar@example.com", "Termes i condicions", "Esborra el compte")
	code, _, h = c.post("/compte/marketing", url.Values{"marketing": {"on"}})
	expect(t, "marketing on", code, http.StatusSeeOther)
	_, body, _ = c.get(h.Get("Location"))
	contains(t, "marketing on", body, "Rebràs les novetats")
	_, _, h = c.post("/compte/marketing", url.Values{})
	_, body, _ = c.get(h.Get("Location"))
	contains(t, "marketing off", body, "No rebràs més novetats")

	// Export.
	code, body, hdr = c.get("/compte/dades.json")
	expect(t, "json export", code, http.StatusOK)
	var exp privacy.Export
	if err := json.Unmarshal([]byte(body), &exp); err != nil {
		t.Fatal(err)
	}
	if exp.Account.Email != "bar@example.com" || len(exp.QRCodes) != 1 || len(exp.QRCodes[0].Destinations) != 2 || len(exp.Consents) != 5 {
		t.Fatalf("export = %+v", exp)
	}
	code, body, _ = c.get("/compte/qr.csv")
	expect(t, "csv export", code, http.StatusOK)
	rows, _ := csv.NewReader(strings.NewReader(body)).ReadAll()
	if len(rows) != 2 || rows[1][2] != "Carta d'hivern" {
		t.Fatalf("csv = %v", rows)
	}

	// Delete: needs ESBORRA and the password.
	code, body, _ = c.post("/compte/esborra", url.Values{"password": {"una-contrasenya"}, "confirm": {"si"}})
	expect(t, "delete without confirm", code, http.StatusUnprocessableEntity)
	contains(t, "delete without confirm", body, "Escriu ESBORRA")
	code, body, _ = c.post("/compte/esborra", url.Values{"password": {"incorrecta!!"}, "confirm": {"ESBORRA"}})
	expect(t, "delete wrong password", code, http.StatusUnprocessableEntity)
	code, _, h = c.post("/compte/esborra", url.Values{"password": {"una-contrasenya"}, "confirm": {"ESBORRA"}})
	expect(t, "delete", code, http.StatusSeeOther)
	code, body, _ = c.get(h.Get("Location"))
	expect(t, "deleted page", code, http.StatusOK)
	contains(t, "deleted page", body, "Compte esborrat")
	code, _, _ = c.get("/qr")
	expect(t, "after delete", code, http.StatusSeeOther)
	code, _, _ = newClient(t, c.srv).post("/login", url.Values{"email": {"bar@example.com"}, "password": {"una-contrasenya"}})
	expect(t, "login after delete", code, http.StatusUnauthorized)
}

func TestLoginLogout(t *testing.T) {
	c, _, _ := setup(t)
	c.post("/register", url.Values{"email": {"bar@example.com"}, "password": {"una-contrasenya"}, "accept_terms": {"on"}})
	code, _, _ := c.post("/logout", url.Values{})
	expect(t, "logout", code, http.StatusSeeOther)
	code, _, _ = c.get("/qr")
	expect(t, "after logout", code, http.StatusSeeOther)

	code, body, _ := c.get("/login")
	expect(t, "login form", code, http.StatusOK)
	code, body, _ = c.post("/login", url.Values{"email": {"bar@example.com"}, "password": {"incorrecta!!"}})
	expect(t, "bad login", code, http.StatusUnauthorized)
	contains(t, "bad login", body, "incorrectes")
	code, _, _ = c.post("/login", url.Values{"email": {"BAR@example.com"}, "password": {"una-contrasenya"}})
	expect(t, "login", code, http.StatusSeeOther)
	code, _, _ = c.get("/qr")
	expect(t, "logged in", code, http.StatusOK)

	// A forged cookie is cleared.
	jar := c.c.Jar
	u, _ := url.Parse(c.srv.URL)
	jar.SetCookies(u, []*http.Cookie{{Name: "sid", Value: "forged", Path: "/"}})
	code, _, _ = c.get("/qr")
	expect(t, "forged session", code, http.StatusSeeOther)
}

func TestPlumbing(t *testing.T) {
	c, _, _ := setup(t)
	code, _, h := c.get("/")
	expect(t, "root", code, http.StatusSeeOther)
	if h.Get("Location") != "/qr" {
		t.Fatalf("root -> %q", h.Get("Location"))
	}
	code, body, _ := c.get("/healthz")
	expect(t, "healthz", code, http.StatusOK)
	if body != "ok" {
		t.Fatalf("healthz body %q", body)
	}
	code, _, h = c.get("/static/app.css")
	expect(t, "css", code, http.StatusOK)
	if h.Get("Content-Security-Policy") == "" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("security headers missing")
	}
	code, _, _ = c.get("/legal/nope")
	expect(t, "unknown legal page", code, http.StatusNotFound)
	code, _, _ = c.get("/compte/esborrat")
	expect(t, "deleted page", code, http.StatusOK)

	// Cross-site form posts are rejected (CSRF).
	req, _ := http.NewRequest("POST", c.srv.URL+"/logout", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := c.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	expect(t, "cross-site post", resp.StatusCode, http.StatusForbidden)
}

func TestAccessRestriction(t *testing.T) {
	pool := testdb.Open(t)
	cfg := config.Panel{Env: config.Dev, PanelBaseURL: "http://panel.test", RedirectBaseURL: "https://qr.test",
		ContactEmail: "hola@example.com", AccessTeamDomain: "team.invalid", AccessAudience: "aud"}
	p, err := New(cfg, Deps{DB: pool, Mail: &outbox{}, Checker: denyList{}})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	p.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/login", nil))
	expect(t, "without access token", w.Code, http.StatusForbidden)
}
