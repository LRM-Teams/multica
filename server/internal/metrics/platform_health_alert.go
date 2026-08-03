package metrics

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Platform-ops health alerter (task #73 Phase A PR2).
//
// Reads the BusinessSampler gauge multica_reminder_scheduled_overdue and
// fires when the global overdue count crosses the approved threshold
// (default >= 3). Not a full APM: one metric, one alert shape, webhook or
// slog-only. User-facing per-reminder discovery is #67.

const (
	// DefaultReminderOverdueAlertThreshold is the Parker/Barry-approved
	// fire floor for Phase A (prefer fewer false positives; env-tunable).
	DefaultReminderOverdueAlertThreshold = 3.0

	defaultPlatformHealthInterval = 30 * time.Second
	// Cooldown while still breaching: avoid webhook spam on a stuck path.
	defaultPlatformHealthCooldown = 30 * time.Minute

	// MetricReminderScheduledOverdue is the series name from PR1.
	MetricReminderScheduledOverdue = "multica_reminder_scheduled_overdue"

	alertNameReminderScheduledOverdue = "MulticaReminderScheduledOverdue"
	alertClassPlatformHealth          = "platform_health"
)

// PlatformHealthAlertConfig controls the in-process platform-ops alerter.
type PlatformHealthAlertConfig struct {
	// Threshold is the overdue count that triggers fire (default 3).
	Threshold float64
	// Interval between evaluations (default 30s).
	Interval time.Duration
	// Cooldown is the minimum gap between successive firing webhooks while
	// still above threshold (default 30m).
	Cooldown time.Duration
	// WebhookURL is the resolved POST target. Empty → slog only.
	WebhookURL string
	// WebhookSource records how WebhookURL was chosen: "ops",
	// "slo_fallback", or "" when none configured.
	WebhookSource string
	Logger        *slog.Logger
	Now           func() time.Time
	HTTPClient    *http.Client
}

// PlatformHealthAlertConfigFromEnv builds config from env.
//
// Webhook priority (Parker/Barry):
//  1. OPS_ALERT_WEBHOOK_URL (preferred)
//  2. if empty, HTTP_SLO_ALERT_WEBHOOK_URL as fallback (payload carries
//     alert_class=platform_health so receivers can route)
//  3. empty → slog only
func PlatformHealthAlertConfigFromEnv() PlatformHealthAlertConfig {
	ops := strings.TrimSpace(os.Getenv("OPS_ALERT_WEBHOOK_URL"))
	slo := strings.TrimSpace(os.Getenv("HTTP_SLO_ALERT_WEBHOOK_URL"))
	webhook, source := "", ""
	switch {
	case ops != "":
		webhook, source = ops, "ops"
	case slo != "":
		webhook, source = slo, "slo_fallback"
	}

	cfg := PlatformHealthAlertConfig{
		Threshold:     DefaultReminderOverdueAlertThreshold,
		Interval:      defaultPlatformHealthInterval,
		Cooldown:      defaultPlatformHealthCooldown,
		WebhookURL:    webhook,
		WebhookSource: source,
	}
	if v := strings.TrimSpace(os.Getenv("OPS_REMINDER_OVERDUE_THRESHOLD")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			cfg.Threshold = f
		}
	}
	if v := strings.TrimSpace(os.Getenv("OPS_HEALTH_EVAL_INTERVAL")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 5*time.Second {
			cfg.Interval = d
		}
	}
	if v := strings.TrimSpace(os.Getenv("OPS_HEALTH_ALERT_COOLDOWN")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= time.Minute {
			cfg.Cooldown = d
		}
	}
	return cfg
}

// PlatformHealthAlerter evaluates platform-ops gauges from a Prometheus
// gatherer (same process that scrapes /metrics).
type PlatformHealthAlerter struct {
	gatherer prometheus.Gatherer
	cfg      PlatformHealthAlertConfig

	mu       sync.Mutex
	firing   bool
	lastFire time.Time
}

// NewPlatformHealthAlerter returns nil when gatherer is nil.
func NewPlatformHealthAlerter(gatherer prometheus.Gatherer, cfg PlatformHealthAlertConfig) *PlatformHealthAlerter {
	if gatherer == nil {
		return nil
	}
	if cfg.Threshold <= 0 {
		cfg.Threshold = DefaultReminderOverdueAlertThreshold
	}
	if cfg.Interval <= 0 {
		cfg.Interval = defaultPlatformHealthInterval
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = defaultPlatformHealthCooldown
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 5 * time.Second}
	}
	return &PlatformHealthAlerter{
		gatherer: gatherer,
		cfg:      cfg,
	}
}

// Run blocks until ctx is cancelled.
func (a *PlatformHealthAlerter) Run(ctx context.Context) {
	if a == nil {
		return
	}
	a.cfg.Logger.Info("platform health alerter starting",
		"threshold", a.cfg.Threshold,
		"interval", a.cfg.Interval.String(),
		"cooldown", a.cfg.Cooldown.String(),
		"webhook_configured", a.cfg.WebhookURL != "",
		"webhook_source", a.cfg.WebhookSource,
	)
	// First tick immediately so a cold start with already-overdue fleet
	// is not delayed a full interval.
	a.Evaluate(ctx)
	t := time.NewTicker(a.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.Evaluate(ctx)
		}
	}
}

// Evaluate is exported for tests.
func (a *PlatformHealthAlerter) Evaluate(ctx context.Context) {
	if a == nil {
		return
	}
	count, ok, err := a.readOverdueCount()
	if err != nil {
		a.cfg.Logger.Warn("platform health evaluate failed", "error", err)
		return
	}
	if !ok {
		// Sampler disabled or first scrape not ready — do not invent a fire.
		return
	}

	now := a.cfg.Now()
	a.mu.Lock()
	defer a.mu.Unlock()

	if count >= a.cfg.Threshold {
		if a.firing && now.Sub(a.lastFire) < a.cfg.Cooldown {
			return
		}
		a.firing = true
		a.lastFire = now
		a.fireLocked(ctx, count, now)
		return
	}
	if a.firing {
		a.firing = false
		a.resolveLocked(ctx, count, now)
	}
}

func (a *PlatformHealthAlerter) fireLocked(ctx context.Context, count float64, now time.Time) {
	a.cfg.Logger.Error("platform health: reminder overdue aggregate breach",
		"alert", alertNameReminderScheduledOverdue,
		"alert_class", alertClassPlatformHealth,
		"metric", MetricReminderScheduledOverdue,
		"count", count,
		"threshold", a.cfg.Threshold,
		"webhook_source", a.cfg.WebhookSource,
		"message", fmt.Sprintf(
			"scheduled reminders overdue ≥1h: count=%.0f (threshold=%.0f). Check reminder fire path / daemon fleet.",
			count, a.cfg.Threshold),
	)
	a.postWebhook(ctx, platformHealthPayload{
		Status:        "firing",
		Alert:         alertNameReminderScheduledOverdue,
		AlertClass:    alertClassPlatformHealth,
		Metric:        MetricReminderScheduledOverdue,
		Count:         count,
		Threshold:     a.cfg.Threshold,
		WebhookSource: a.cfg.WebhookSource,
		At:            now.UTC().Format(time.RFC3339),
	})
}

func (a *PlatformHealthAlerter) resolveLocked(ctx context.Context, count float64, now time.Time) {
	a.cfg.Logger.Info("platform health: reminder overdue aggregate recovered",
		"alert", alertNameReminderScheduledOverdue,
		"alert_class", alertClassPlatformHealth,
		"metric", MetricReminderScheduledOverdue,
		"count", count,
		"threshold", a.cfg.Threshold,
	)
	a.postWebhook(ctx, platformHealthPayload{
		Status:        "resolved",
		Alert:         alertNameReminderScheduledOverdue,
		AlertClass:    alertClassPlatformHealth,
		Metric:        MetricReminderScheduledOverdue,
		Count:         count,
		Threshold:     a.cfg.Threshold,
		WebhookSource: a.cfg.WebhookSource,
		Reason:        "below_threshold",
		At:            now.UTC().Format(time.RFC3339),
	})
}

type platformHealthPayload struct {
	Status        string  `json:"status"`
	Alert         string  `json:"alert"`
	AlertClass    string  `json:"alert_class"`
	Metric        string  `json:"metric"`
	Count         float64 `json:"count"`
	Threshold     float64 `json:"threshold"`
	WebhookSource string  `json:"webhook_source,omitempty"`
	Reason        string  `json:"reason,omitempty"`
	At            string  `json:"at"`
}

func (a *PlatformHealthAlerter) postWebhook(ctx context.Context, payload platformHealthPayload) {
	if a.cfg.WebhookURL == "" {
		return
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.WebhookURL, bytes.NewReader(body))
	if err != nil {
		a.cfg.Logger.Warn("platform health webhook build failed", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.cfg.HTTPClient.Do(req)
	if err != nil {
		a.cfg.Logger.Warn("platform health webhook post failed", "error", err)
		return
	}
	_ = resp.Body.Close()
}

// readOverdueCount returns (value, seriesPresent, err).
func (a *PlatformHealthAlerter) readOverdueCount() (float64, bool, error) {
	families, err := a.gatherer.Gather()
	if err != nil {
		return 0, false, err
	}
	for _, fam := range families {
		if fam.GetName() != MetricReminderScheduledOverdue {
			continue
		}
		metrics := fam.GetMetric()
		if len(metrics) == 0 {
			return 0, false, nil
		}
		// No labels: take the first (only) sample.
		g := metrics[0].GetGauge()
		if g == nil {
			u := metrics[0].GetUntyped()
			if u == nil {
				return 0, false, nil
			}
			return u.GetValue(), true, nil
		}
		return g.GetValue(), true, nil
	}
	return 0, false, nil
}
