package web

import (
	"math"
	"regexp"
	"strconv"
	"testing"
)

// TestPaletteMeetsWCAG checks the colour tokens of app.css in light and dark
// mode against WCAG 2.1 AA: 4.5:1 for text, 3:1 for component boundaries.
func TestPaletteMeetsWCAG(t *testing.T) {
	css, err := staticFS.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	blocks := regexp.MustCompile(`:root\s*\{([^}]*)\}`).FindAllSubmatch(css, 2)
	if len(blocks) != 2 {
		t.Fatalf("want a light and a dark :root block, found %d", len(blocks))
	}
	pairs := []struct {
		fg, bg string
		min    float64
	}{
		{"text", "bg", 4.5}, {"text", "surface", 4.5}, {"text", "warn-bg", 4.5},
		{"muted", "bg", 4.5}, {"muted", "surface", 4.5}, {"muted", "warn-bg", 4.5},
		{"accent", "bg", 4.5}, {"accent", "surface", 4.5}, {"accent-text", "accent", 4.5},
		{"danger", "bg", 4.5}, {"danger", "surface", 4.5}, {"danger-text", "danger", 4.5},
		{"ok", "bg", 4.5}, {"ok", "surface", 4.5},
		{"input-border", "bg", 3}, {"input-border", "surface", 3},
	}
	token := regexp.MustCompile(`--([\w-]+):\s*(#[0-9a-fA-F]{6})`)
	for i, mode := range []string{"light", "dark"} {
		tokens := map[string]string{}
		for _, m := range token.FindAllSubmatch(blocks[i][1], -1) {
			tokens[string(m[1])] = string(m[2])
		}
		for _, p := range pairs {
			fg, bg := tokens[p.fg], tokens[p.bg]
			if fg == "" || bg == "" {
				t.Errorf("%s: token --%s or --%s missing", mode, p.fg, p.bg)
				continue
			}
			if r := contrast(fg, bg); r < p.min {
				t.Errorf("%s: --%s on --%s = %.2f:1, want >= %.1f:1", mode, p.fg, p.bg, r, p.min)
			}
		}
	}
}

func contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	return (math.Max(la, lb) + 0.05) / (math.Min(la, lb) + 0.05)
}

func luminance(hex string) float64 {
	var c [3]float64
	for i := range 3 {
		v, _ := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
		x := float64(v) / 255
		if x <= 0.03928 {
			c[i] = x / 12.92
		} else {
			c[i] = math.Pow((x+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*c[0] + 0.7152*c[1] + 0.0722*c[2]
}
