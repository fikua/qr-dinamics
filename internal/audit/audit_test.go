package audit

import (
	"context"
	"sort"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/log/logtest"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// records installs an in-memory logger provider and returns the attribute keys
// and values of every audit record emitted by fn.
func records(t *testing.T, fn func(ctx context.Context)) []map[string]string {
	t.Helper()
	rec := logtest.NewRecorder()
	prev := global.GetLoggerProvider()
	global.SetLoggerProvider(rec)
	t.Cleanup(func() { global.SetLoggerProvider(prev) })

	fn(context.Background())

	var out []map[string]string
	for _, sc := range rec.Result() {
		for _, r := range sc {
			m := map[string]string{"body": r.Body.AsString()}
			for _, kv := range r.Attributes {
				m[string(kv.Key)] = kv.Value.Emit()
			}
			out = append(out, m)
		}
	}
	return out
}

func keys(m map[string]string) []string {
	var k []string
	for key := range m {
		k = append(k, key)
	}
	sort.Strings(k)
	return k
}

func TestCodeCreatedKeepsOnlyTheDestinationHost(t *testing.T) {
	got := records(t, func(ctx context.Context) {
		CodeCreated(ctx, 7, "Ab3Xy", "https://Shop.example.com/offer?email=owner@example.com")
	})
	if len(got) != 1 {
		t.Fatalf("records = %d, want 1", len(got))
	}
	r := got[0]
	if r["event.domain"] != "audit" || r["event.name"] != "code.created" || r["body"] != "code.created" {
		t.Errorf("event = %v", r)
	}
	if r["destination.host"] != "shop.example.com" {
		t.Errorf("destination.host = %q", r["destination.host"])
	}
	want := []string{"account.id", "body", "code", "destination.host", "event.domain", "event.name"}
	if g := keys(r); !equal(g, want) {
		t.Errorf("attributes = %v, want %v", g, want)
	}
}

func TestAccountEventsCarryTheEmailHMACNeverTheAddress(t *testing.T) {
	SetEmailKey("test-key")
	t.Cleanup(func() { SetEmailKey("") })

	got := records(t, func(ctx context.Context) {
		AccountRegistered(ctx, 7, "Owner@Example.com")
		AccountErased(ctx, 7, " owner@example.com ", 3)
	})
	if len(got) != 2 {
		t.Fatalf("records = %d, want 2", len(got))
	}
	h := got[0]["account.email_hmac"]
	if len(h) != 64 {
		t.Fatalf("account.email_hmac = %q, want 64 hex chars", h)
	}
	if got[1]["account.email_hmac"] != h {
		t.Error("the same address must give the same HMAC after normalization")
	}
	for _, r := range got {
		for k, v := range r {
			if v == "owner@example.com" || v == "Owner@Example.com" {
				t.Errorf("%s carries the address", k)
			}
		}
	}
	if got[1]["codes.tombstoned"] != "3" {
		t.Errorf("codes.tombstoned = %q", got[1]["codes.tombstoned"])
	}
}

func TestWithoutKeyTheHMACIsLeftOut(t *testing.T) {
	SetEmailKey("")
	got := records(t, func(ctx context.Context) { AccountRegistered(ctx, 7, "owner@example.com") })
	if _, ok := got[0]["account.email_hmac"]; ok {
		t.Error("account.email_hmac present without a key")
	}
}

func TestAuthRejectedHasOnlyTheReason(t *testing.T) {
	got := records(t, func(ctx context.Context) { AuthRejected(ctx, "invalid_credentials") })
	want := []string{"body", "event.domain", "event.name", "reason"}
	if g := keys(got[0]); !equal(g, want) {
		t.Errorf("attributes = %v, want %v", g, want)
	}
}

func TestCount(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	ctx := context.Background()
	Count(ctx, "scan", "redirected", "")
	Count(ctx, "scan", "redirected", "")
	Count(ctx, "code.create", "rejected", "quota")

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatal(err)
	}
	points := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "qr.business.operations" {
				continue
			}
			for _, p := range m.Data.(metricdata.Sum[int64]).DataPoints {
				op, _ := p.Attributes.Value(attribute.Key("operation"))
				outcome, _ := p.Attributes.Value(attribute.Key("outcome"))
				et, _ := p.Attributes.Value(attribute.Key("error.type"))
				points[op.AsString()+"/"+outcome.AsString()+"/"+et.AsString()] = p.Value
			}
		}
	}
	if points["scan/redirected/"] != 2 || points["code.create/rejected/quota"] != 1 {
		t.Errorf("points = %v", points)
	}
}

func TestDestinationHost(t *testing.T) {
	for in, want := range map[string]string{
		"https://example.com/a?b=c": "example.com",
		"http://EXAMPLE.com:8080/":  "example.com",
		"not a url %%":              "",
	} {
		if got := DestinationHost(in); got != want {
			t.Errorf("DestinationHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
