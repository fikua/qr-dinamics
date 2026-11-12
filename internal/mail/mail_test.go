package mail

import (
	"context"
	"strings"
	"testing"
)

func TestLogAndFooter(t *testing.T) {
	if err := (Log{}).Send(context.Background(), Message{To: "a@b.c", Subject: "s", Body: "b"}); err != nil {
		t.Fatal(err)
	}
	f := ServiceFooter("https://app.test")
	for _, want := range []string{"https://app.test/compte", "https://app.test/legal/privacitat", "no publicitat"} {
		if !strings.Contains(f, want) {
			t.Fatalf("footer lacks %q", want)
		}
	}
}
