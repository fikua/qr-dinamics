// Package mail sends transactional e-mail.
package mail

import (
	"context"
	"log/slog"
)

// Message is one transactional e-mail.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Sender sends e-mail.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// Log writes e-mails to the log instead of sending them. Dev only.
type Log struct{}

func (Log) Send(_ context.Context, m Message) error {
	slog.Info("email (not sent, dev mailer)", "to", m.To, "subject", m.Subject, "body", m.Body)
	return nil
}

// ServiceFooter is appended to every transactional e-mail. These e-mails are
// part of the service (verification, billing, security) and carry no
// marketing, so they have no unsubscribe link; the footer says why they are
// sent and where to manage preferences. Marketing e-mails, when they exist,
// must use a different footer with a one-click unsubscribe link and the
// List-Unsubscribe header.
func ServiceFooter(panelBaseURL string) string {
	return "\n\n--\nReps aquest correu perquè tens un compte a QR dinàmics. És un missatge del servei, necessari per fer servir el compte, i no publicitat." +
		"\nPreferències i dades del compte: " + panelBaseURL + "/compte" +
		"\nPolítica de privacitat: " + panelBaseURL + "/legal/privacitat"
}
