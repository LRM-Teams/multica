package metrics

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
)

type HTTPMetrics struct {
	requests    *prometheus.CounterVec
	duration    *prometheus.HistogramVec
	sloDuration *prometheus.HistogramVec
	inFlight    prometheus.Gauge
}

func NewHTTPMetrics() *HTTPMetrics {
	return &HTTPMetrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "multica",
			Subsystem: "http",
			Name:      "requests_total",
			Help:      "Total HTTP requests served by the API server.",
		}, []string{"method", "route", "status"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "multica",
			Subsystem: "http",
			Name:      "request_duration_seconds",
			Help:      "HTTP request duration observed by the API server.",
			Buckets:   []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}, []string{"method", "route", "status"}),
		sloDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "multica",
			Subsystem: "http",
			Name:      "slo_request_duration_seconds",
			Help:      "Eligible API request duration for the HTTP p95 SLO, excluding streams, transfers, and agent transport.",
			Buckets:   []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}, []string{"method", "route", "status"}),
		inFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "multica",
			Subsystem: "http",
			Name:      "in_flight_requests",
			Help:      "Current number of in-flight HTTP requests served by the API server.",
		}),
	}
}

func (m *HTTPMetrics) Collectors() []prometheus.Collector {
	return []prometheus.Collector{m.requests, m.duration, m.sloDuration, m.inFlight}
}

func (m *HTTPMetrics) Middleware(next http.Handler) http.Handler {
	if m == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isHealthProbePath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		m.inFlight.Inc()
		defer m.inFlight.Dec()

		start := time.Now()
		ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)

		status := ww.Status()
		if status == 0 {
			status = http.StatusOK
		}
		labels := prometheus.Labels{
			"method": r.Method,
			"route":  routePattern(r),
			"status": strconv.Itoa(status),
		}
		duration := time.Since(start).Seconds()
		m.requests.With(labels).Inc()
		m.duration.With(labels).Observe(duration)
		if isSLOEligibleRequest(r, labels["route"], ww.Header()) {
			m.sloDuration.With(labels).Observe(duration)
		}
	})
}

func routePattern(r *http.Request) string {
	if rctx := chi.RouteContext(r.Context()); rctx != nil {
		if pattern := rctx.RoutePattern(); pattern != "" {
			return pattern
		}
	}
	return "unmatched"
}

func isSLOEligibleRequest(r *http.Request, route string, responseHeaders http.Header) bool {
	if !strings.HasPrefix(route, "/api/") {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") || strings.HasSuffix(route, "/ws") || strings.HasSuffix(route, "/connect") {
		return false
	}
	if strings.HasPrefix(route, "/api/daemon/") || strings.HasPrefix(route, "/api/sandbox/") || strings.HasPrefix(route, "/api/agent/") {
		return false
	}
	switch route {
	case "/api/upload-file", "/api/agent/attachments/upload", "/api/agent/upload-file":
		return false
	}
	return !strings.HasPrefix(strings.ToLower(responseHeaders.Get("Content-Type")), "text/event-stream")
}

func isHealthProbePath(path string) bool {
	switch path {
	case "/health", "/healthz", "/readyz":
		return true
	default:
		return false
	}
}
