// Package i18n picks the language of a request (Catalan, Spanish or English)
// and translates the interface texts.
//
// Catalan is the source language: the Catalan text itself is the catalogue
// key, so a missing translation falls back to readable Catalan instead of an
// opaque identifier. Spanish and English live in catalog_es.go and
// catalog_en.go.
//
// The language is chosen, in order, from the "lang" cookie (set when the
// visitor picks a language by hand) and the Accept-Language header. With no
// header at all the page is Catalan; with a header that names none of the
// three languages it is English.
package i18n

import (
	"context"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Lang is a supported interface language, as its ISO 639-1 code.
type Lang string

const (
	CA Lang = "ca"
	ES Lang = "es"
	EN Lang = "en"
)

// All lists the supported languages in the order the selector shows them.
var All = []Lang{CA, ES, EN}

// Name is the language's name in its own language, for the selector.
func (l Lang) Name() string {
	switch l {
	case ES:
		return "Español"
	case EN:
		return "English"
	}
	return "Català"
}

// Locale is the BCP 47 tag for the <html lang> attribute.
func (l Lang) Locale() string { return string(l) }

// CookieName is the cookie that remembers a manual choice.
const CookieName = "lang"

const cookieMaxAge = 365 * 24 * 60 * 60

// Parse returns the supported language for code, if any.
func Parse(code string) (Lang, bool) {
	for _, l := range All {
		if string(l) == code {
			return l, true
		}
	}
	return "", false
}

// FromRequest returns the language of the request.
func FromRequest(r *http.Request) Lang {
	if c, err := r.Cookie(CookieName); err == nil {
		if l, ok := Parse(c.Value); ok {
			return l
		}
	}
	return fromAcceptLanguage(r.Header.Get("Accept-Language"))
}

// fromAcceptLanguage picks the supported language with the highest quality.
func fromAcceptLanguage(header string) Lang {
	if strings.TrimSpace(header) == "" {
		return CA
	}
	type cand struct {
		lang Lang
		q    float64
		pos  int
	}
	var cands []cand
	for i, part := range strings.Split(header, ",") {
		tag, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		q := 1.0
		for _, p := range strings.Split(params, ";") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(p), "q="); ok {
				if f, err := strconv.ParseFloat(v, 64); err == nil {
					q = f
				}
			}
		}
		primary, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(tag)), "-")
		if l, ok := Parse(primary); ok && q > 0 {
			cands = append(cands, cand{l, q, i})
		}
	}
	if len(cands) == 0 {
		return EN
	}
	sort.SliceStable(cands, func(a, b int) bool { return cands[a].q > cands[b].q })
	return cands[0].lang
}

type ctxKey struct{}

// Middleware stores the request's language in its context, for code that
// only has the context (e-mails).
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, FromRequest(r))))
	})
}

// FromContext returns the language stored by Middleware, Catalan by default.
func FromContext(ctx context.Context) Lang {
	if l, ok := ctx.Value(ctxKey{}).(Lang); ok {
		return l
	}
	return CA
}

// T translates a Catalan text. Unknown texts and Catalan come back as is.
func T(l Lang, key string) string {
	var m map[string]string
	switch l {
	case ES:
		m = es
	case EN:
		m = en
	default:
		return key
	}
	if v, ok := m[key]; ok {
		return v
	}
	return key
}

// Switch serves GET /lang/{code}: it remembers the choice in a cookie and
// goes back to the page the visitor came from (same site only).
func Switch(w http.ResponseWriter, r *http.Request) {
	l, ok := Parse(r.PathValue("code"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: string(l), Path: "/", MaxAge: cookieMaxAge,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
		// Always Secure: production is HTTPS end to end, and browsers keep
		// Secure cookies on http://localhost.
		Secure: true,
	})
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, backPath(r), http.StatusSeeOther)
}

// backPath returns the path (and query) of the Referer when it is on this
// host, and "/" otherwise, so the redirect can never leave the site.
func backPath(r *http.Request) string {
	ref, err := url.Parse(r.Referer())
	if err != nil || ref.Host != r.Host || !strings.HasPrefix(ref.Path, "/") || strings.HasPrefix(ref.Path, "//") ||
		strings.HasPrefix(ref.Path, "/lang/") {
		return "/"
	}
	p := ref.EscapedPath()
	if ref.RawQuery != "" {
		p += "?" + ref.RawQuery
	}
	return p
}

// Routes registers the language switch.
func Routes(mux *http.ServeMux) { mux.HandleFunc("GET /lang/{code}", Switch) }

// Funcs are the template functions for one language: t translates a Catalan
// text and langs lists the languages for the selector. Parse templates with
// Funcs(CA) and call Funcs(lang) on a clone when rendering.
func Funcs(l Lang) template.FuncMap {
	return template.FuncMap{
		"t":     func(key string) string { return T(l, key) },
		"langs": func() []Lang { return All },
	}
}
