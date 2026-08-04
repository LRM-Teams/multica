package metrics

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestPlatformHealthAlertConfigFromEnvPriority(t *testing.T) {
	t.Setenv("OPS_ALERT_WEBHOOK_URL", "http://ops.test/hook")
	t.Setenv("HTTP_SLO_ALERT_WEBHOOK_URL", "http://slo.test/hook")
	cfg := PlatformHealthAlertConfigFromEnv()
	if cfg.WebhookURL != "http://ops.test/hook" || cfg.WebhookSource != "ops" {
		t.Fatalf("want ops preferred, got url=%q source=%q", cfg.WebhookURL, cfg.WebhookSource)
	}

	t.Setenv("OPS_ALERT_WEBHOOK_URL", "")
	cfg = PlatformHealthAlertConfigFromEnv()
	if cfg.WebhookURL != "http://slo.test/hook" || cfg.WebhookSource != "slo_fallback" {
		t.Fatalf("want slo fallback, got url=%q source=%q", cfg.WebhookURL, cfg.WebhookSource)
	}

	t.Setenv("HTTP_SLO_ALERT_WEBHOOK_URL", "")
	cfg = PlatformHealthAlertConfigFromEnv()
	if cfg.WebhookURL != "" || cfg.WebhookSource != "" {
		t.Fatalf("want empty when both unset, got url=%q source=%q", cfg.WebhookURL, cfg.WebhookSource)
	}
}

func TestPlatformHealthAlerterFiresAtThresholdWithCooldown(t *testing.T) {
	reg := prometheus.NewRegistry()
	g := prometheus.NewGauge(prometheus.GaugeOpts{Name: MetricReminderScheduledOverdue})
	if err := reg.Register(g); err != nil {
		t.Fatal(err)
	}

	var now atomic.Value
	start := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	now.Store(start)

	var posts atomic.Int32
	var lastBody atomic.Value
	lastBody.Store([]byte(nil))
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		posts.Add(1)
		body, _ := io.ReadAll(r.Body)
		lastBody.Store(body)
		return &http.Response{StatusCode: 204, Body: io.NopCloser(bytes.NewReader(nil)), Header: make(http.Header)}, nil
	})}

	alerter := NewPlatformHealthAlerter(reg, PlatformHealthAlertConfig{
		Threshold:     3,
		Interval:      time.Second,
		Cooldown:      30 * time.Minute,
		WebhookURL:    "http://example.test/alert",
		WebhookSource: "ops",
		HTTPClient:    client,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:           func() time.Time { return now.Load().(time.Time) },
	})

	// Below threshold: no fire.
	g.Set(2)
	alerter.Evaluate(context.Background())
	if posts.Load() != 0 {
		t.Fatalf("fired below threshold: %d", posts.Load())
	}

	// At threshold: fire immediately (no sustain window).
	g.Set(3)
	alerter.Evaluate(context.Background())
	if posts.Load() != 1 {
		t.Fatalf("want 1 fire at threshold, got %d", posts.Load())
	}
	body := lastBody.Load().([]byte)
	if !bytes.Contains(body, []byte(`"status":"firing"`)) {
		t.Fatalf("payload missing firing: %s", body)
	}
	if !bytes.Contains(body, []byte(alertNameReminderScheduledOverdue)) {
		t.Fatalf("payload missing alert name: %s", body)
	}
	if !bytes.Contains(body, []byte(`"alert_class":"platform_health"`)) {
		t.Fatalf("payload missing alert_class: %s", body)
	}
	if !bytes.Contains(body, []byte(`"count":3`)) {
		t.Fatalf("payload missing count: %s", body)
	}

	// Still breaching inside cooldown: no re-fire.
	g.Set(5)
	now.Store(start.Add(10 * time.Minute))
	alerter.Evaluate(context.Background())
	if posts.Load() != 1 {
		t.Fatalf("want no re-fire inside cooldown, got %d", posts.Load())
	}

	// After cooldown while still high: re-fire once.
	now.Store(start.Add(31 * time.Minute))
	alerter.Evaluate(context.Background())
	if posts.Load() != 2 {
		t.Fatalf("want re-fire after cooldown, got %d", posts.Load())
	}

	// Recover: resolved webhook.
	g.Set(0)
	now.Store(start.Add(32 * time.Minute))
	alerter.Evaluate(context.Background())
	if posts.Load() != 3 {
		t.Fatalf("want resolve webhook, got %d body=%s", posts.Load(), lastBody.Load())
	}
	body = lastBody.Load().([]byte)
	if !bytes.Contains(body, []byte(`"status":"resolved"`)) {
		t.Fatalf("payload missing resolved: %s", body)
	}
}

func TestPlatformHealthAlerterNoSeriesNoFire(t *testing.T) {
	// Empty registry: sampler off / series absent — must not invent a fire.
	reg := prometheus.NewRegistry()
	var posts atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		posts.Add(1)
		return &http.Response{StatusCode: 204, Body: io.NopCloser(bytes.NewReader(nil)), Header: make(http.Header)}, nil
	})}
	alerter := NewPlatformHealthAlerter(reg, PlatformHealthAlertConfig{
		Threshold:  3,
		WebhookURL: "http://example.test/alert",
		HTTPClient: client,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	alerter.Evaluate(context.Background())
	if posts.Load() != 0 {
		t.Fatalf("must not fire without series, got %d", posts.Load())
	}
}

func TestPlatformHealthAlerterSlogOnlyWhenNoWebhook(t *testing.T) {
	reg := prometheus.NewRegistry()
	g := prometheus.NewGauge(prometheus.GaugeOpts{Name: MetricReminderScheduledOverdue})
	_ = reg.Register(g)
	g.Set(10)

	var posts atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		posts.Add(1)
		return &http.Response{StatusCode: 204, Body: io.NopCloser(bytes.NewReader(nil)), Header: make(http.Header)}, nil
	})}
	// WebhookURL empty → no HTTP even if client is wired.
	alerter := NewPlatformHealthAlerter(reg, PlatformHealthAlertConfig{
		Threshold:  3,
		WebhookURL: "",
		HTTPClient: client,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	alerter.Evaluate(context.Background())
	if posts.Load() != 0 {
		t.Fatalf("slog-only must not POST, got %d", posts.Load())
	}
}
