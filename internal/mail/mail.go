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
