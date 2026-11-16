// Package audit emits the audit events of QR dinàmics and counts its business
// operations (Fikua observability convention, OBS-09 and OBS-12).
//
// An audit event is an OpenTelemetry log record with event.domain = audit, a
// name from the catalogue below and only the attributes the catalogue lists.
// The platform Collector archives it for five years in a locked bucket: it is
// the blocked copy that remains after an account is erased (LOPDGDD art. 32).
// Nothing in the archive can be corrected, so an event or attribute is added
// here only after the catalogue has been reviewed (2026-11-16, Oriol Canadés).
//
//	event.name                 when                                        attributes
//	account.registered         an account was created                      account.id, account.email_hmac
//	consent.recorded           a consent was given or withdrawn            account.id, consent.kind, consent.version, consent.granted
//	account.verified           the e-mail address was verified             account.id
//	account.erased             the owner erased the account                account.id, account.email_hmac, codes.tombstoned
//	data.exported              the owner exported their data               account.id, format
//	code.created               a QR code was created                       account.id, code, destination.host
//	code.destination_changed   a QR code points somewhere else             account.id, code, destination.host
//	code.paused, code.resumed,
//	code.deleted               a QR code changed status                    account.id, code
//	auth.rejected              a login was refused                         reason
//	retention.purged           the daily retention purge ran               purged.scans, purged.sessions, purged.tokens, purged.unverified_accounts
//
// Never an e-mail address, a full URL, an IP address or a password.
// account.email_hmac is an HMAC-SHA256 of the normalized address with a secret
// key: with the key, a given address can be matched to an erased account; the
// archive alone does not reveal it.
//
// Events are emitted after the action is committed. Emission never fails the
// caller: the SDK drops what it cannot export.
package audit

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/metric"
)

const scope = "qrdinamics/audit"

var (
	keyMu    sync.RWMutex
	emailKey []byte
)

// SetEmailKey sets the HMAC key of account.email_hmac (AUDIT_EMAIL_HMAC_KEY).
// Without a key the attribute is left out.
func SetEmailKey(key string) {
	keyMu.Lock()
	defer keyMu.Unlock()
	emailKey = []byte(key)
}

// EmailHMAC returns the hex HMAC-SHA256 of the trimmed, lower-cased address, or
// "" when no key is set.
func EmailHMAC(email string) string {
	keyMu.RLock()
	defer keyMu.RUnlock()
	if len(emailKey) == 0 {
		return ""
	}
	m := hmac.New(sha256.New, emailKey)
	m.Write([]byte(strings.ToLower(strings.TrimSpace(email))))
	return hex.EncodeToString(m.Sum(nil))
}

// DestinationHost is the host of a destination URL, never its path or query.
func DestinationHost(destination string) string {
	u, err := url.Parse(destination)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// Emit sends one audit event. attrs must be those of the catalogue.
func Emit(ctx context.Context, name string, attrs ...attribute.KeyValue) {
	var r log.Record
	r.SetSeverity(log.SeverityInfo)
	r.SetSeverityText("INFO")
	r.SetBody(attribute.StringValue(name))
	r.AddAttributes(attribute.String("event.domain", "audit"), attribute.String("event.name", name))
	r.AddAttributes(attrs...)
	global.GetLoggerProvider().Logger(scope).Emit(ctx, r)
}

// AccountRegistered, and the functions below, are the catalogue: one per event.
func AccountRegistered(ctx context.Context, accountID int64, email string) {
	Emit(ctx, "account.registered", withEmail([]attribute.KeyValue{attribute.Int64("account.id", accountID)}, email)...)
}

func ConsentRecorded(ctx context.Context, accountID int64, kind, version string, granted bool) {
	Emit(ctx, "consent.recorded",
		attribute.Int64("account.id", accountID),
		attribute.String("consent.kind", kind),
		attribute.String("consent.version", version),
		attribute.Bool("consent.granted", granted))
}

func AccountVerified(ctx context.Context, accountID int64) {
	Emit(ctx, "account.verified", attribute.Int64("account.id", accountID))
}

func AccountErased(ctx context.Context, accountID int64, email string, codesTombstoned int64) {
	Emit(ctx, "account.erased", withEmail([]attribute.KeyValue{
		attribute.Int64("account.id", accountID),
		attribute.Int64("codes.tombstoned", codesTombstoned),
	}, email)...)
}

func DataExported(ctx context.Context, accountID int64, format string) {
	Emit(ctx, "data.exported", attribute.Int64("account.id", accountID), attribute.String("format", format))
}

func CodeCreated(ctx context.Context, accountID int64, code, destination string) {
	Emit(ctx, "code.created",
		attribute.Int64("account.id", accountID),
		attribute.String("code", code),
		attribute.String("destination.host", DestinationHost(destination)))
}

func CodeDestinationChanged(ctx context.Context, accountID int64, code, destination string) {
	Emit(ctx, "code.destination_changed",
		attribute.Int64("account.id", accountID),
		attribute.String("code", code),
		attribute.String("destination.host", DestinationHost(destination)))
}

// CodeStatusChanged emits code.paused, code.resumed or code.deleted.
func CodeStatusChanged(ctx context.Context, name string, accountID int64, code string) {
	Emit(ctx, name, attribute.Int64("account.id", accountID), attribute.String("code", code))
}

func AuthRejected(ctx context.Context, reason string) {
	Emit(ctx, "auth.rejected", attribute.String("reason", reason))
}

func RetentionPurged(ctx context.Context, scans, sessions, tokens, unverifiedAccounts int64) {
	Emit(ctx, "retention.purged",
		attribute.Int64("purged.scans", scans),
		attribute.Int64("purged.sessions", sessions),
		attribute.Int64("purged.tokens", tokens),
		attribute.Int64("purged.unverified_accounts", unverifiedAccounts))
}

func withEmail(attrs []attribute.KeyValue, email string) []attribute.KeyValue {
	if h := EmailHMAC(email); h != "" {
		attrs = append(attrs, attribute.String("account.email_hmac", h))
	}
	return attrs
}

var (
	counterOnce sync.Once
	counter     metric.Int64Counter
)

// Count adds one business operation to qr.business.operations (OBS-09).
// errorType is set only when the operation failed or was refused.
func Count(ctx context.Context, operation, outcome, errorType string) {
	counterOnce.Do(func() {
		counter, _ = otel.GetMeterProvider().Meter(scope).Int64Counter("qr.business.operations",
			metric.WithDescription("Business operations of QR dinàmics, by operation and outcome"),
			metric.WithUnit("{operation}"))
	})
	if counter == nil {
		return
	}
	attrs := []attribute.KeyValue{attribute.String("operation", operation), attribute.String("outcome", outcome)}
	if errorType != "" {
		attrs = append(attrs, attribute.String("error.type", errorType))
	}
	counter.Add(ctx, 1, metric.WithAttributes(attrs...))
}
