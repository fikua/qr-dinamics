package mail

import (
	"context"
	"strings"
	"testing"

	"qrdinamics/internal/i18n"
)

func TestLogAndFooter(t *testing.T) {
	if err := (Log{}).Send(context.Background(), Message{To: "a@b.c", Subject: "s", Body: "b"}); err != nil {
		t.Fatal(err)
	}
	f := ServiceFooter(i18n.CA, "https://app.test")
	for _, want := range []string{"https://app.test/compte", "https://app.test/legal/privacitat", "no publicitat"} {
		if !strings.Contains(f, want) {
			t.Fatalf("footer lacks %q", want)
		}
	}
}

func TestFooterIsTranslated(t *testing.T) {
	if f := ServiceFooter(i18n.ES, "https://app.test"); !strings.Contains(f, "no publicidad") || strings.Contains(f, "no publicitat") {
		t.Fatalf("Spanish footer: %q", f)
	}
	if f := ServiceFooter(i18n.EN, "https://app.test"); !strings.Contains(f, "not advertising") {
		t.Fatalf("English footer: %q", f)
	}
}
