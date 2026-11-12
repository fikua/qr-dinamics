package accounts

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"

	"qrdinamics/internal/mail"
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

func TestRegisterVerifyLogin(t *testing.T) {
	ctx := context.Background()
	box := &outbox{}
	s := NewService(testdb.Open(t), box, "https://app.example")

	if _, err := s.Register(ctx, Signup{Email: "bar@example.com", Password: "una-contrasenya"}); !errors.Is(err, ErrTermsNotAccepted) {
		t.Fatalf("without accepting the terms: err = %v", err)
	}
	a, err := s.Register(ctx, Signup{Email: "  Bar@Example.com ", Password: "una-contrasenya", AcceptTerms: true})
	if err != nil {
		t.Fatal(err)
	}
	if a.Email != "bar@example.com" || a.Verified() || a.Plan != "free" {
		t.Fatalf("registered %+v", a)
	}
	if _, err := s.Register(ctx, Signup{Email: "bar@example.com", Password: "una-contrasenya", AcceptTerms: true}); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("duplicate: err = %v", err)
	}
	if _, err := s.Register(ctx, Signup{Email: "x@example.com", Password: "curta", AcceptTerms: true}); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("weak password: err = %v", err)
	}
	if _, err := s.Register(ctx, Signup{Email: "no-es-correu", Password: "una-contrasenya", AcceptTerms: true}); !errors.Is(err, ErrInvalidEmail) {
		t.Fatalf("invalid email: err = %v", err)
	}

	if len(box.msgs) != 1 {
		t.Fatalf("%d e-mails sent, want 1", len(box.msgs))
	}
	if !strings.Contains(box.msgs[0].Body, "https://app.example/compte") {
		t.Fatal("transactional e-mail lacks the service footer")
	}

	// Consent record: terms and privacy accepted, marketing not, with version.
	consents, err := s.Consents(ctx, a.ID)
	if err != nil || len(consents) != 3 {
		t.Fatalf("consents = %+v, %v", consents, err)
	}
	for _, c := range consents {
		if c.Version != LegalVersion || c.Granted != (c.Kind != "marketing") {
			t.Fatalf("consent %+v", c)
		}
	}
	if m, _ := s.Marketing(ctx, a.ID); m {
		t.Fatal("marketing must be off unless ticked")
	}
	if err := s.SetMarketing(ctx, a.ID, true); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.Marketing(ctx, a.ID); !m {
		t.Fatal("marketing not granted after SetMarketing(true)")
	}
	if err := s.CheckPassword(ctx, a.ID, "incorrecta!!"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("CheckPassword(wrong) = %v", err)
	}
	if err := s.CheckPassword(ctx, a.ID, "una-contrasenya"); err != nil {
		t.Fatalf("CheckPassword(right) = %v", err)
	}
	link := box.msgs[0].Body[strings.Index(box.msgs[0].Body, "https://"):]
	link = strings.Fields(link)[0]
	u, _ := url.Parse(link)
	token := u.Query().Get("token")

	if _, err := s.Login(ctx, "bar@example.com", "incorrecta!!"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password: err = %v", err)
	}
	if _, err := s.Login(ctx, "ningu@example.com", "una-contrasenya"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unknown user: err = %v", err)
	}
	session, err := s.Login(ctx, "BAR@example.com", "una-contrasenya")
	if err != nil {
		t.Fatal(err)
	}

	if err := s.VerifyEmail(ctx, token); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyEmail(ctx, token); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("token reused: err = %v", err)
	}
	got, err := s.BySession(ctx, session)
	if err != nil || !got.Verified() {
		t.Fatalf("BySession = %+v, %v", got, err)
	}

	if err := s.Logout(ctx, session); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BySession(ctx, session); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("after logout: err = %v", err)
	}
}
