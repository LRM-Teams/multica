package metrics

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
)

func TestHistogramQuantileP95(t *testing.T) {
	// 100 samples: 90 under 0.5s, 10 under 2.5s → p95 in (0.5, 2.5]
	buckets := map[float64]uint64{
		0.1:  50,
		0.5:  90,
		1.0:  90,
		2.5:  100,
		5.0:  100,
		10.0: 100,
	}
	p95, ok := histogramQuantile(0.95, buckets, 100)
	if !ok {
		t.Fatal("expected ok")
	}
	if p95 <= 0.5 || p95 > 2.5 {
		t.Fatalf("p95=%v want in (0.5, 2.5]", p95)
	}
}

func TestHTTPRequestSLOAlerterFiresOnSustainedBreach(t *testing.T) {
	reg := prometheus.NewRegistry()
	httpM := NewHTTPMetrics()
	for _, c := range httpM.Collectors() {
		if err := reg.Register(c); err != nil {
			t.Fatal(err)
		}
	}

	var now atomic.Value
	start := time.Date(2026, 7, 28, 2, 0, 0, 0, time.UTC)
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

	alerter := NewHTTPRequestSLOAlerter(reg, HTTPRequestSLOConfig{
		ThresholdSeconds: 1.0,
		Interval:         time.Second,
		Sustain:          2 * time.Minute,
		MinSamples:       20,
		WebhookURL:       "http://example.test/alert",
		HTTPClient:       client,
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:              func() time.Time { return now.Load().(time.Time) },
	})

	// Seed previous counters with zero observations.
	alerter.Evaluate(context.Background())

	// Window 1: slow activity traffic.
	for i := 0; i < 50; i++ {
		httpM.duration.WithLabelValues("GET", "/api/agents/{id}/activity", "200").Observe(1.5)
	}
	now.Store(start.Add(30 * time.Second))
	alerter.Evaluate(context.Background())
	if posts.Load() != 0 {
		t.Fatalf("webhook fired too early: %d", posts.Load())
	}

	// Keep breaching across sustain window (each window adds more slow samples).
	for step := 1; step <= 5; step++ {
		for i := 0; i < 50; i++ {
			httpM.duration.WithLabelValues("GET", "/api/agents/{id}/activity", "200").Observe(1.5)
		}
		now.Store(start.Add(time.Duration(step)*30*time.Second + 2*time.Minute))
		alerter.Evaluate(context.Background())
	}
	if posts.Load() != 1 {
		t.Fatalf("want 1 fire, got %d body=%s", posts.Load(), lastBody.Load())
	}
	body := lastBody.Load().([]byte)
	if !bytes.Contains(body, []byte(`"status":"firing"`)) {
		t.Fatalf("payload missing firing: %s", body)
	}
	if !bytes.Contains(body, []byte(`/api/agents/{id}/activity`)) {
		t.Fatalf("payload missing route: %s", body)
	}
	if !bytes.Contains(body, []byte(`"priority":"hot_path"`)) {
		t.Fatalf("payload missing hot_path priority: %s", body)
	}

	// No spam while still slow.
	for i := 0; i < 50; i++ {
		httpM.duration.WithLabelValues("GET", "/api/agents/{id}/activity", "200").Observe(1.5)
	}
	now.Store(start.Add(5 * time.Minute))
	alerter.Evaluate(context.Background())
	if posts.Load() != 1 {
		t.Fatalf("want no re-fire, got %d", posts.Load())
	}

	// Recover window: only fast samples in the delta.
	for i := 0; i < 50; i++ {
		httpM.duration.WithLabelValues("GET", "/api/agents/{id}/activity", "200").Observe(0.05)
	}
	now.Store(start.Add(6 * time.Minute))
	alerter.Evaluate(context.Background())
	if posts.Load() != 2 {
		t.Fatalf("want fire+resolve=2 posts, got %d body=%s", posts.Load(), lastBody.Load())
	}
	body = lastBody.Load().([]byte)
	if !bytes.Contains(body, []byte(`"status":"resolved"`)) {
		t.Fatalf("payload missing resolved: %s", body)
	}
}

func TestHTTPMiddlewareRecordsPriorityRoutes(t *testing.T) {
	registry := NewRegistry(RegistryOptions{Version: "t", Commit: "c"})
	r := chi.NewRouter()
	r.Use(registry.HTTP.Middleware)
	r.Get("/api/agents/{id}/activity", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	r.Get("/api/agent-task-snapshot", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	for _, path := range []string{"/api/agents/abc/activity?tab=all", "/api/agent-task-snapshot"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != 200 {
			t.Fatalf("%s -> %d", path, rec.Code)
		}
	}
	metricsRec := httptest.NewRecorder()
	NewHandler(registry.Gatherer).ServeHTTP(metricsRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := metricsRec.Body.String()
	for _, want := range []string{
		`route="/api/agents/{id}/activity"`,
		`route="/api/agent-task-snapshot"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s in\n%s", want, body)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
