package metrics

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// DefaultHTTPRequestSLOSeconds is Frank's served-latency gate: every API
// endpoint p95 must stay under 1s. This is wall-clock served time (middleware
// observe), not SQL statement time.
const DefaultHTTPRequestSLOSeconds = 1.0

// PriorityHTTPRoutes are the first routes Parker asked to hang on the SLO
// (accident hot paths). Alerts for these routes set priority=hot_path.
//
// Note: FE Activity reads GET /api/agents/{id}/runner-activity — query
// params are not separate metric routes (and must not be, to avoid
// high-cardinality labels).
var PriorityHTTPRoutes = []string{
	"/api/agents/{id}/runner-activity",
	"/api/agent-task-snapshot",
}

// HTTPRequestSLOConfig controls the in-process HTTP p95 alerter.
//
// Why in-process: production docker (aliyun) historically had METRICS_ADDR
// empty and no Prometheus Operator — helm PrometheusRule alone never fired.
// This watcher evaluates the SLO-only histogram exposed by the scrape endpoint
// so alerts work on every deployment while preserving its exclusion policy.
type HTTPRequestSLOConfig struct {
	// ThresholdSeconds is the p95 ceiling (default 1s).
	ThresholdSeconds float64
	// Interval between evaluations (default 30s).
	Interval time.Duration
	// Sustain is how long p95 must stay above threshold before firing
	// (default 2m). Mirrors PrometheusRule `for:`.
	Sustain time.Duration
	// MinSamples skips low-traffic routes in the evaluation window.
	MinSamples uint64
	// WebhookURL optional POST target for alert payloads (JSON).
	WebhookURL string
	// Logger defaults to slog.Default().
	Logger *slog.Logger
	// Now for tests.
	Now func() time.Time
	// HTTPClient for webhook posts (tests inject).
	HTTPClient *http.Client
}

func HTTPRequestSLOConfigFromEnv() HTTPRequestSLOConfig {
	cfg := HTTPRequestSLOConfig{
		ThresholdSeconds: DefaultHTTPRequestSLOSeconds,
		Interval:         30 * time.Second,
		Sustain:          2 * time.Minute,
		MinSamples:       20,
		WebhookURL:       strings.TrimSpace(os.Getenv("HTTP_SLO_ALERT_WEBHOOK_URL")),
	}
	if v := strings.TrimSpace(os.Getenv("HTTP_SLO_P95_SECONDS")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			cfg.ThresholdSeconds = f
		}
	}
	if v := strings.TrimSpace(os.Getenv("HTTP_SLO_EVAL_INTERVAL")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 5*time.Second {
			cfg.Interval = d
		}
	}
	if v := strings.TrimSpace(os.Getenv("HTTP_SLO_SUSTAIN")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= time.Second {
			cfg.Sustain = d
		}
	}
	return cfg
}

type histSnap struct {
	buckets map[float64]uint64
	count   uint64
}

// HTTPRequestSLOAlerter periodically computes served HTTP p95 per route from
// the in-process Prometheus registry and fires when p95 exceeds the SLO.
//
// Quantiles use the *delta* of cumulative histograms between evaluations
// (equivalent to Prometheus rate()/increase() over the eval interval).
type HTTPRequestSLOAlerter struct {
	gatherer prometheus.Gatherer
	cfg      HTTPRequestSLOConfig

	mu           sync.Mutex
	prev         map[string]histSnap // method\x00route -> last cumulative
	breachingFor map[string]time.Time
	firing       map[string]bool
}

func NewHTTPRequestSLOAlerter(gatherer prometheus.Gatherer, cfg HTTPRequestSLOConfig) *HTTPRequestSLOAlerter {
	if gatherer == nil {
		return nil
	}
	if cfg.ThresholdSeconds <= 0 {
		cfg.ThresholdSeconds = DefaultHTTPRequestSLOSeconds
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 30 * time.Second
	}
	if cfg.Sustain <= 0 {
		cfg.Sustain = 2 * time.Minute
	}
	if cfg.MinSamples == 0 {
		cfg.MinSamples = 20
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
	return &HTTPRequestSLOAlerter{
		gatherer:     gatherer,
		cfg:          cfg,
		prev:         map[string]histSnap{},
		breachingFor: map[string]time.Time{},
		firing:       map[string]bool{},
	}
}

// Run blocks until ctx is cancelled.
func (a *HTTPRequestSLOAlerter) Run(ctx context.Context) {
	if a == nil {
		return
	}
	a.cfg.Logger.Info("http request SLO alerter starting",
		"threshold_seconds", a.cfg.ThresholdSeconds,
		"interval", a.cfg.Interval.String(),
		"sustain", a.cfg.Sustain.String(),
		"webhook_configured", a.cfg.WebhookURL != "",
	)
	// Seed previous counters without alerting on the first window.
	_, _ = a.collectWindowP95()
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
func (a *HTTPRequestSLOAlerter) Evaluate(ctx context.Context) {
	if a == nil {
		return
	}
	series, err := a.collectWindowP95()
	if err != nil {
		a.cfg.Logger.Warn("http request SLO evaluate failed", "error", err)
		return
	}
	now := a.cfg.Now()
	priority := priorityRouteSet()

	a.mu.Lock()
	defer a.mu.Unlock()

	seen := map[string]struct{}{}
	for _, s := range series {
		key := s.Method + " " + s.Route
		seen[key] = struct{}{}
		if s.Samples < a.cfg.MinSamples {
			delete(a.breachingFor, key)
			if a.firing[key] {
				a.resolveLocked(ctx, s, now, "below_min_samples")
			}
			continue
		}
		if s.P95Seconds <= a.cfg.ThresholdSeconds {
			delete(a.breachingFor, key)
			if a.firing[key] {
				a.resolveLocked(ctx, s, now, "recovered")
			}
			continue
		}
		first, ok := a.breachingFor[key]
		if !ok {
			a.breachingFor[key] = now
			first = now
		}
		if now.Sub(first) < a.cfg.Sustain {
			continue
		}
		if a.firing[key] {
			continue
		}
		a.firing[key] = true
		s.Priority = "normal"
		if priority[s.Route] {
			s.Priority = "hot_path"
		}
		a.fireLocked(ctx, s, now, first)
	}

	for key := range a.firing {
		if _, ok := seen[key]; !ok {
			delete(a.firing, key)
			delete(a.breachingFor, key)
		}
	}
}

func (a *HTTPRequestSLOAlerter) fireLocked(ctx context.Context, s routeP95, now, firstBreach time.Time) {
	a.cfg.Logger.Error("HTTP request p95 SLO breach",
		"alert", "MulticaHTTPRequestP95High",
		"method", s.Method,
		"route", s.Route,
		"p95_seconds", s.P95Seconds,
		"threshold_seconds", a.cfg.ThresholdSeconds,
		"samples", s.Samples,
		"priority", s.Priority,
		"breaching_for", now.Sub(firstBreach).String(),
		"message", fmt.Sprintf("served p95 %.3fs > %.3fs for %s %s (Frank SLO: every API < 1s)",
			s.P95Seconds, a.cfg.ThresholdSeconds, s.Method, s.Route),
	)
	a.postWebhook(ctx, sloAlertPayload{
		Status:    "firing",
		Alert:     "MulticaHTTPRequestP95High",
		Method:    s.Method,
		Route:     s.Route,
		P95:       s.P95Seconds,
		Threshold: a.cfg.ThresholdSeconds,
		Samples:   s.Samples,
		Priority:  s.Priority,
		At:        now.UTC().Format(time.RFC3339),
	})
}

func (a *HTTPRequestSLOAlerter) resolveLocked(ctx context.Context, s routeP95, now time.Time, reason string) {
	delete(a.firing, s.Method+" "+s.Route)
	a.cfg.Logger.Info("HTTP request p95 SLO recovered",
		"alert", "MulticaHTTPRequestP95High",
		"method", s.Method,
		"route", s.Route,
		"p95_seconds", s.P95Seconds,
		"threshold_seconds", a.cfg.ThresholdSeconds,
		"reason", reason,
	)
	a.postWebhook(ctx, sloAlertPayload{
		Status:    "resolved",
		Alert:     "MulticaHTTPRequestP95High",
		Method:    s.Method,
		Route:     s.Route,
		P95:       s.P95Seconds,
		Threshold: a.cfg.ThresholdSeconds,
		Samples:   s.Samples,
		Priority:  s.Priority,
		Reason:    reason,
		At:        now.UTC().Format(time.RFC3339),
	})
}

type sloAlertPayload struct {
	Status    string  `json:"status"`
	Alert     string  `json:"alert"`
	Method    string  `json:"method"`
	Route     string  `json:"route"`
	P95       float64 `json:"p95_seconds"`
	Threshold float64 `json:"threshold_seconds"`
	Samples   uint64  `json:"samples"`
	Priority  string  `json:"priority,omitempty"`
	Reason    string  `json:"reason,omitempty"`
	At        string  `json:"at"`
}

func (a *HTTPRequestSLOAlerter) postWebhook(ctx context.Context, payload sloAlertPayload) {
	if a.cfg.WebhookURL == "" {
		return
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.WebhookURL, bytes.NewReader(body))
	if err != nil {
		a.cfg.Logger.Warn("http SLO webhook build failed", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.cfg.HTTPClient.Do(req)
	if err != nil {
		a.cfg.Logger.Warn("http SLO webhook post failed", "error", err)
		return
	}
	_ = resp.Body.Close()
}

type routeP95 struct {
	Method     string
	Route      string
	P95Seconds float64
	Samples    uint64
	Priority   string
}

func priorityRouteSet() map[string]bool {
	out := make(map[string]bool, len(PriorityHTTPRoutes))
	for _, r := range PriorityHTTPRoutes {
		out[r] = true
	}
	return out
}

// collectWindowP95 returns p95 over the *increase* since the previous call.
func (a *HTTPRequestSLOAlerter) collectWindowP95() ([]routeP95, error) {
	current, err := a.readCumulative()
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	var out []routeP95
	for key, cur := range current {
		prev, hadPrev := a.prev[key]
		a.prev[key] = cloneSnap(cur)
		if !hadPrev {
			continue // first observation seeds only
		}
		deltaCount := cur.count - prev.count
		if cur.count < prev.count {
			// Counter reset (process restart mid-eval)
			continue
		}
		if deltaCount == 0 {
			continue
		}
		deltaBuckets := map[float64]uint64{}
		for bound, c := range cur.buckets {
			pc := prev.buckets[bound]
			if c >= pc {
				deltaBuckets[bound] = c - pc
			} else {
				deltaBuckets[bound] = c
			}
		}
		p95, ok := histogramQuantile(0.95, deltaBuckets, deltaCount)
		if !ok {
			continue
		}
		parts := strings.SplitN(key, "\x00", 2)
		if len(parts) != 2 {
			continue
		}
		out = append(out, routeP95{
			Method:     parts[0],
			Route:      parts[1],
			P95Seconds: p95,
			Samples:    deltaCount,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Route == out[j].Route {
			return out[i].Method < out[j].Method
		}
		return out[i].Route < out[j].Route
	})
	return out, nil
}

func cloneSnap(s histSnap) histSnap {
	out := histSnap{count: s.count, buckets: map[float64]uint64{}}
	for k, v := range s.buckets {
		out.buckets[k] = v
	}
	return out
}

func (a *HTTPRequestSLOAlerter) readCumulative() (map[string]histSnap, error) {
	mfs, err := a.gatherer.Gather()
	if err != nil {
		return nil, err
	}
	byKey := map[string]*histSnap{}
	for _, mf := range mfs {
		if mf.GetName() != "multica_http_slo_request_duration_seconds" {
			continue
		}
		for _, m := range mf.GetMetric() {
			h := m.GetHistogram()
			if h == nil {
				continue
			}
			method, route := "", ""
			for _, lp := range m.GetLabel() {
				switch lp.GetName() {
				case "method":
					method = lp.GetValue()
				case "route":
					route = lp.GetValue()
				}
			}
			if method == "" || route == "" || route == "unmatched" {
				continue
			}
			key := method + "\x00" + route
			snap := byKey[key]
			if snap == nil {
				snap = &histSnap{buckets: map[float64]uint64{}}
				byKey[key] = snap
			}
			snap.count += h.GetSampleCount()
			for _, b := range h.GetBucket() {
				snap.buckets[b.GetUpperBound()] += b.GetCumulativeCount()
			}
		}
	}
	out := make(map[string]histSnap, len(byKey))
	for k, v := range byKey {
		out[k] = cloneSnap(*v)
	}
	return out, nil
}

// histogramQuantile estimates a quantile from Prometheus cumulative buckets.
// buckets maps upper_bound -> cumulative count. count is total samples.
func histogramQuantile(q float64, buckets map[float64]uint64, count uint64) (float64, bool) {
	if count == 0 || q <= 0 || q > 1 || len(buckets) == 0 {
		return 0, false
	}
	type bound struct {
		u float64
		c uint64
	}
	list := make([]bound, 0, len(buckets))
	for u, c := range buckets {
		list = append(list, bound{u: u, c: c})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].u < list[j].u })

	// Ensure buckets are cumulative-sorted; if caller passed delta already-
	// cumulative per bound from Prometheus, values are non-decreasing in u.
	rank := q * float64(count)
	var prevCount uint64
	var prevUpper float64
	for i, b := range list {
		if float64(b.c) < rank {
			prevCount = b.c
			prevUpper = b.u
			continue
		}
		if i == 0 {
			return b.u, true
		}
		bucketCount := float64(b.c - prevCount)
		if bucketCount <= 0 {
			return b.u, true
		}
		frac := (rank - float64(prevCount)) / bucketCount
		return prevUpper + frac*(b.u-prevUpper), true
	}
	return list[len(list)-1].u, true
}
