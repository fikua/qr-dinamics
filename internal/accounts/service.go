// Package accounts handles registration, e-mail verification, login and
// sessions of business owners (US-01).
package accounts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	mailer "qrdinamics/internal/mail"
)

var (
	ErrEmailTaken         = errors.New("email already registered")
	ErrInvalidEmail       = errors.New("invalid email")
	ErrWeakPassword       = errors.New("password too short")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInvalidToken       = errors.New("invalid or expired token")
	ErrTermsNotAccepted   = errors.New("terms not accepted")
)

// LegalVersion identifies the terms and privacy policy an account accepts.
// Bump it whenever internal/legal changes those texts in substance.
const LegalVersion = "2026-11-12"

const (
	MinPasswordLength = 10
	sessionTTL        = 30 * 24 * time.Hour
	verifyTokenTTL    = 48 * time.Hour
	purposeVerify     = "verify_email"
)

// Account is a business owner's account.
type Account struct {
	ID              int64
	Email           string
	Plan            string
	EmailVerifiedAt *time.Time
	CreatedAt       time.Time
}

// Verified reports whether the account confirmed its e-mail.
func (a Account) Verified() bool { return a.EmailVerifiedAt != nil }

// Service implements the account use cases.
type Service struct {
	db           *pgxpool.Pool
	mail         mailer.Sender
	panelBaseURL string
	// dummyHash keeps login timing the same whether or not the e-mail exists.
	dummyHash []byte
}

// NewService builds the service.
func NewService(db *pgxpool.Pool, mail mailer.Sender, panelBaseURL string) *Service {
	random := make([]byte, 32)
	rand.Read(random)
	h, _ := bcrypt.GenerateFromPassword(random, bcrypt.DefaultCost)
	return &Service{db: db, mail: mail, panelBaseURL: panelBaseURL, dummyHash: h}
}

// NormalizeEmail lowercases and validates an address.
func NormalizeEmail(raw string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(raw))
	addr, err := mail.ParseAddress(e)
	if err != nil || addr.Address != e || len(e) > 254 {
		return "", ErrInvalidEmail
	}
	return e, nil
}

// Signup is what the registration form collects: nothing beyond what the
// service needs, plus the two separate, unticked-by-default consents.
type Signup struct {
	Email       string
	Password    string
	AcceptTerms bool // terms and privacy policy: required
	Marketing   bool // product news: optional, separate
}

// Register creates an unverified account, records the consents and sends
// the verification e-mail. No card is asked at registration.
func (s *Service) Register(ctx context.Context, in Signup) (Account, error) {
	email, err := NormalizeEmail(in.Email)
	if err != nil {
		return Account{}, err
	}
	password := in.Password
	if len([]rune(password)) < MinPasswordLength {
		return Account{}, ErrWeakPassword
	}
	if !in.AcceptTerms {
		return Account{}, ErrTermsNotAccepted
	}
	if len(password) > 72 { // bcrypt input limit
		password = password[:72]
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return Account{}, fmt.Errorf("hash password: %w", err)
	}
	var a Account
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO accounts (email, password_hash) VALUES ($1, $2)
			RETURNING id, email, plan, email_verified_at, created_at`, email, string(hash)).
			Scan(&a.ID, &a.Email, &a.Plan, &a.EmailVerifiedAt, &a.CreatedAt); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO consents (account_id, kind, version, granted) VALUES
			($1, 'terms', $2, true), ($1, 'privacy', $2, true), ($1, 'marketing', $2, $3)`,
			a.ID, LegalVersion, in.Marketing)
		return err
	})
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return Account{}, ErrEmailTaken
		}
		return Account{}, err
	}
	return a, s.SendVerification(ctx, a)
}

// Consent is one entry of an account's consent record.
type Consent struct {
	Kind      string
	Version   string
	Granted   bool
	CreatedAt time.Time
}

// Consents returns the consent record of an account, oldest first.
func (s *Service) Consents(ctx context.Context, accountID int64) ([]Consent, error) {
	rows, err := s.db.Query(ctx, `SELECT kind, version, granted, created_at FROM consents WHERE account_id = $1 ORDER BY created_at, id`, accountID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Consent, error) {
		var c Consent
		err := r.Scan(&c.Kind, &c.Version, &c.Granted, &c.CreatedAt)
		return c, err
	})
}

// Marketing reports the account's current marketing consent.
func (s *Service) Marketing(ctx context.Context, accountID int64) (bool, error) {
	var granted bool
	err := s.db.QueryRow(ctx, `SELECT granted FROM consents WHERE account_id = $1 AND kind = 'marketing' ORDER BY created_at DESC, id DESC LIMIT 1`, accountID).Scan(&granted)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return granted, err
}

// SetMarketing records a new marketing consent decision (grant or withdrawal).
func (s *Service) SetMarketing(ctx context.Context, accountID int64, granted bool) error {
	_, err := s.db.Exec(ctx, `INSERT INTO consents (account_id, kind, version, granted) VALUES ($1, 'marketing', $2, $3)`, accountID, LegalVersion, granted)
	return err
}

// CheckPassword confirms the account's password before a sensitive action.
func (s *Service) CheckPassword(ctx context.Context, accountID int64, password string) error {
	if len(password) > 72 {
		password = password[:72]
	}
	var hash string
	if err := s.db.QueryRow(ctx, `SELECT password_hash FROM accounts WHERE id = $1`, accountID).Scan(&hash); err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return ErrInvalidCredentials
	}
	return nil
}

// SendVerification e-mails a fresh verification link.
func (s *Service) SendVerification(ctx context.Context, a Account) error {
	token, hash, err := newToken()
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO email_tokens (token_hash, account_id, purpose, expires_at) VALUES ($1, $2, $3, $4)`,
		hash, a.ID, purposeVerify, time.Now().Add(verifyTokenTTL)); err != nil {
		return err
	}
	link := s.panelBaseURL + "/verify?token=" + token
	return s.mail.Send(ctx, mailer.Message{
		To:      a.Email,
		Subject: "Confirma el teu correu",
		Body: "Hola!\n\nConfirma el teu correu per començar a crear QR:\n" + link + "\n\nL'enllaç caduca en 48 hores." +
			mailer.ServiceFooter(s.panelBaseURL),
	})
}

// VerifyEmail consumes a verification token.
func (s *Service) VerifyEmail(ctx context.Context, token string) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var accountID int64
		err := tx.QueryRow(ctx, `
			UPDATE email_tokens SET used_at = now()
			WHERE token_hash = $1 AND purpose = $2 AND used_at IS NULL AND expires_at > now()
			RETURNING account_id`, hashToken(token), purposeVerify).Scan(&accountID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidToken
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE accounts SET email_verified_at = coalesce(email_verified_at, now()) WHERE id = $1`, accountID)
		return err
	})
}

// Login checks the credentials and opens a session; it returns the session token.
func (s *Service) Login(ctx context.Context, email, password string) (string, error) {
	email, err := NormalizeEmail(email)
	if err != nil {
		return "", ErrInvalidCredentials
	}
	if len(password) > 72 {
		password = password[:72]
	}
	var id int64
	var hash string
	err = s.db.QueryRow(ctx, `SELECT id, password_hash FROM accounts WHERE email = $1`, email).Scan(&id, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		bcrypt.CompareHashAndPassword(s.dummyHash, []byte(password))
		return "", ErrInvalidCredentials
	}
	if err != nil {
		return "", err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return "", ErrInvalidCredentials
	}
	token, tokenHash, err := newToken()
	if err != nil {
		return "", err
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO sessions (token_hash, account_id, expires_at) VALUES ($1, $2, $3)`,
		tokenHash, id, time.Now().Add(sessionTTL)); err != nil {
		return "", err
	}
	return token, nil
}

// Logout closes a session.
func (s *Service) Logout(ctx context.Context, token string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, hashToken(token))
	return err
}

// BySession returns the account of a valid session.
func (s *Service) BySession(ctx context.Context, token string) (Account, error) {
	var a Account
	err := s.db.QueryRow(ctx, `
		SELECT a.id, a.email, a.plan, a.email_verified_at, a.created_at
		FROM sessions s JOIN accounts a ON a.id = s.account_id
		WHERE s.token_hash = $1 AND s.expires_at > now()`, hashToken(token)).
		Scan(&a.ID, &a.Email, &a.Plan, &a.EmailVerifiedAt, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrInvalidToken
	}
	return a, err
}

// newToken returns a random URL-safe token and the hash stored in the database.
func newToken() (string, []byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	t := base64.RawURLEncoding.EncodeToString(b)
	return t, hashToken(t), nil
}

func hashToken(t string) []byte {
	h := sha256.Sum256([]byte(t))
	return h[:]
}
