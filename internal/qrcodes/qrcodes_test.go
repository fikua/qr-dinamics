package qrcodes

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"
	"time"
)

func TestNewCode(t *testing.T) {
	seen := map[string]bool{}
	for range 2000 {
		c, err := NewCode()
		if err != nil {
			t.Fatal(err)
		}
		if !ValidCode(c) {
			t.Fatalf("NewCode() = %q, not a valid code", c)
		}
		if strings.ContainsAny(c, "0Oo1lIi") {
			t.Fatalf("NewCode() = %q contains a confusable character", c)
		}
		seen[c] = true
	}
	if len(seen) < 1990 {
		t.Fatalf("only %d distinct codes out of 2000", len(seen))
	}
}

func TestValidCode(t *testing.T) {
	tests := map[string]bool{
		"abc2345":  true,
		"ABC2345":  true,
		"abc234":   false, // too short
		"abc23456": false, // too long
		"abc0345":  false, // 0 is excluded
		"abcl345":  false, // l is excluded
		"abc-345":  false,
		"":         false,
		"healthz":  false,
	}
	for in, want := range tests {
		if got := ValidCode(in); got != want {
			t.Errorf("ValidCode(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestRenderPNGIsAtLeast1000px(t *testing.T) {
	b, err := RenderPNG("https://qr.example/abc2345")
	if err != nil {
		t.Fatal(err)
	}
	side, err := imageSize(b)
	if err != nil {
		t.Fatal(err)
	}
	if side < minPNGSize {
		t.Fatalf("PNG side = %d px, want >= %d", side, minPNGSize)
	}
}

func TestRenderSVGIsWellFormedWithQuietZone(t *testing.T) {
	b, err := RenderSVG("https://qr.example/abc2345")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		ViewBox string `xml:"viewBox,attr"`
	}
	if err := xml.NewDecoder(bytes.NewReader(b)).Decode(&doc); err != nil {
		t.Fatalf("SVG is not well-formed XML: %v", err)
	}
	// Version 2 (25 modules) fits this URL at level Q; plus 4 modules each side.
	if !strings.HasPrefix(doc.ViewBox, "0 0 ") {
		t.Fatalf("unexpected viewBox %q", doc.ViewBox)
	}
	// No black module may sit in the quiet zone: every path starts at >= 4.
	if bytes.Contains(b, []byte("M0 ")) || bytes.Contains(b, []byte("M1 ")) || bytes.Contains(b, []byte("M3 ")) {
		t.Fatal("black module inside the quiet zone")
	}
}

func TestQuotaLimitsNewAccounts(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	s := &Service{now: func() time.Time { return now }}
	saved := planQuota["pro"]
	planQuota["pro"] = 100
	t.Cleanup(func() {
		planQuota["pro"] = saved
		if saved == 0 {
			delete(planQuota, "pro")
		}
	})

	if got := s.Quota(Owner{Plan: "free", CreatedAt: now.Add(-30 * 24 * time.Hour)}); got != 3 {
		t.Errorf("free quota = %d, want 3", got)
	}
	if got := s.Quota(Owner{Plan: "pro", CreatedAt: now.Add(-24 * time.Hour)}); got != newAccountMaxQR {
		t.Errorf("new pro account quota = %d, want %d (RF-10)", got, newAccountMaxQR)
	}
	if got := s.Quota(Owner{Plan: "pro", CreatedAt: now.Add(-8 * 24 * time.Hour)}); got != 100 {
		t.Errorf("established pro quota = %d, want 100", got)
	}
	if got := s.Quota(Owner{Plan: "unknown", CreatedAt: now.Add(-30 * 24 * time.Hour)}); got != 3 {
		t.Errorf("unknown plan quota = %d, want the free quota", got)
	}
}

func TestEscapeLike(t *testing.T) {
	if got := escapeLike(`50%_off\`); got != `50\%\_off\\` {
		t.Fatalf("escapeLike = %q", got)
	}
}
