package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
)

// webhookTriggerIDKeyType is unexported so foreign packages cannot collide on
// the context key — they go through SetWebhookTriggerID instead.
type webhookTriggerIDKeyType struct{}

var webhookTriggerIDKey = webhookTriggerIDKeyType{}

// SetWebhookTriggerID stashes the resolved trigger ID on the request context
// so the request logger can include it in the audit line without revealing
// the bearer token in the URL path. Called by the webhook handler right after
// the trigger row is looked up.
//
// Mutates `*r` in place so the wrapping middleware (which is still holding the
// original `*http.Request`) reads the value back out of context after
// ServeHTTP returns. Reassigning a local `r` variable would not propagate the
// new context back up to the caller, which is the trap a previous version of
// this helper fell into.
func SetWebhookTriggerID(r *http.Request, triggerID string) {
	if triggerID == "" {
		return
	}
	*r = *r.WithContext(context.WithValue(r.Context(), webhookTriggerIDKey, triggerID))
}

// webhookTriggerIDFromContext returns the trigger ID stashed by
// SetWebhookTriggerID, or "" when none was set.
func webhookTriggerIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(webhookTriggerIDKey).(string)
	return v
}

// webhookIngressPathPrefix is the public webhook ingress path. The path
// segment after this prefix IS a bearer credential, so the logger must
// redact it — see redactWebhookPath.
const webhookIngressPathPrefix = "/api/webhooks/autopilots/"

// redactWebhookPath returns a logger-safe version of a request path. For
// the autopilot webhook ingress path the trailing token segment is replaced
// with "[redacted]"; every other path passes through untouched.
//
// Why this exists: r.URL.Path for a successful webhook delivery is
// "/api/webhooks/autopilots/awt_<32-byte-base64>", and the token is the
// only credential gating the route. Without redaction, every successful
// delivery prints a replayable URL into the structured log stream.
func redactWebhookPath(path string) string {
	if !strings.HasPrefix(path, webhookIngressPathPrefix) {
		return path
	}
	rest := path[len(webhookIngressPathPrefix):]
	if rest == "" {
		return path
	}
	// Preserve any sub-path after the token (currently none, but defensive).
	if slash := strings.IndexByte(rest, '/'); slash >= 0 {
		return webhookIngressPathPrefix + "[redacted]" + rest[slash:]
	}
	return webhookIngressPathPrefix + "[redacted]"
}

// boundedBuffer captures up to Cap bytes from a stream then silently drops the
// rest. Used by RequestLogger so a large response body cannot blow up logger
// memory while we mirror just enough bytes to classify the response.
type boundedBuffer struct {
	buf bytes.Buffer
	cap int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	remain := b.cap - b.buf.Len()
	if remain <= 0 {
		return len(p), nil
	}
	if len(p) > remain {
		b.buf.Write(p[:remain])
		return len(p), nil
	}
	b.buf.Write(p)
	return len(p), nil
}

func (b *boundedBuffer) Bytes() []byte { return b.buf.Bytes() }

// softNotFoundBodyCaptureLimit is the maximum number of body bytes the
// request logger inspects to decide whether a 404 is an expected stale-state
// signal (runtime/task deleted server-side). The JSON error envelope is small
// — 256 bytes is enough to see the "error" field — and the cap means an
// unbounded handler body cannot blow up logger memory.
const softNotFoundBodyCaptureLimit = 256

// softNotFoundMarkers are 404 response bodies the daemon emits routinely as
// part of normal lifecycle events: a runtime deleted from the UI, a task GC'd
// after an issue was removed, etc. Logging these at Warn turned production
// stderr into a flood whenever a runtime was deleted (see issue #2391). They
// stay machine-recognizable at Info, while genuine 4xx (wrong path, bad
// auth, real bugs) keep Warn.
var softNotFoundMarkers = []string{
	"runtime not found",
	"task not found",
}

// RequestLogger is a structured HTTP request logger using slog.
// It replaces Chi's built-in chimw.Logger with colored, structured output.
func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip the hot liveness endpoint to keep logs readable.
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)

		// Capture a small body prefix so 404s can be classified by content.
		// chimw.WrapResponseWriter exposes Tee for exactly this — the body
		// keeps flowing to the client; we mirror up to N bytes for inspection.
		bodyPrefix := &boundedBuffer{cap: softNotFoundBodyCaptureLimit}
		ww.Tee(bodyPrefix)

		next.ServeHTTP(ww, r)

		duration := time.Since(start)
		status := ww.Status()

		attrs := []any{
			"method", r.Method,
			"path", redactWebhookPath(r.URL.Path),
			"status", status,
			"duration", duration.Round(time.Microsecond).String(),
		}
		if rid := chimw.GetReqID(r.Context()); rid != "" {
			attrs = append(attrs, "request_id", rid)
		}
		if uid := r.Header.Get("X-User-ID"); uid != "" {
			attrs = append(attrs, "user_id", uid)
		}
		if tid := webhookTriggerIDFromContext(r.Context()); tid != "" {
			attrs = append(attrs, "webhook_trigger_id", tid)
		}
		if platform, version, os := ClientMetadataFromContext(r.Context()); platform != "" || version != "" || os != "" {
			if platform != "" {
				attrs = append(attrs, "client_platform", platform)
			}
			if version != "" {
				attrs = append(attrs, "client_version", version)
			}
			if os != "" {
				attrs = append(attrs, "client_os", os)
			}
		}

		// Decide the level for this request, preserving the soft-404 downgrade
		// (lifecycle 404 — runtime/task deleted server-side — is signal, not a
		// bug; the daemon catches the same body and self-heals).
		level := slog.LevelInfo
		switch {
		case status >= 500:
			level = slog.LevelError
		case status == http.StatusNotFound && isSoftNotFound(bodyPrefix.Bytes()):
			level = slog.LevelInfo
		case status >= 400:
			level = slog.LevelWarn
		}

		// Coalesce repeated identical 4xx request logs so a single runaway
		// client (e.g. a script hammering one endpoint with the same malformed
		// body — LRM-640's empty-content env-dispatch loop at ~1k req/min) cannot
		// turn the warn channel into a multi-thousand-line-per-hour flood. The
		// FIRST occurrence in each window still logs at its real level; later
		// duplicates are counted and summarized in one INFO line when the window
		// rolls over. The HTTP 4xx response itself is unaffected — the client
		// still receives it every time (we dedupe the log line, never the error).
		// 5xx is intentionally excluded: server faults stay loud.
		emit := true
		if status >= 400 && status < 500 {
			key := clientErrKey{
				method: r.Method,
				path:   redactWebhookPath(r.URL.Path),
				status: status,
				userID: r.Header.Get("X-User-ID"),
				code:   errorCodeFromBody(bodyPrefix.Bytes()),
			}
			logFirst, summary := repeatedClientErrCoalescer.observe(key, start)
			summary.log()
			emit = logFirst
		}
		if emit {
			slog.Default().Log(context.Background(), level, "http request", attrs...)
		}
	})
}

// repeatedClientErrWindow is the dedup window for repeated identical 4xx
// request-log lines. Within one window only the first occurrence of a given
// fingerprint is logged at its real level; the rest are counted and reported
// in a single summary when the window rolls. Short enough that a transient
// burst still yields a few WARN signals, long enough that a sustained ~18
// req/s loop collapses from ~18k lines/min to a handful.
const repeatedClientErrWindow = 10 * time.Second

// repeatedClientErrCap bounds the fingerprint map so a caller that spins
// distinct fingerprints cannot grow it unbounded. Real floods are one
// fingerprint repeated, not thousands of distinct ones, so on overflow we
// simply reset the whole map.
const repeatedClientErrCap = 4096

type clientErrKey struct {
	method string
	path   string
	status int
	userID string
	code   string // response body "error" code, best-effort; "" when unparseable
}

type clientErrEntry struct {
	windowStart time.Time
	count       int // occurrences in this window, including the first (logged) one
}

// clientErrSummary describes a just-closed window of repeated 4xx logs so the
// caller can emit one audit line after releasing the coalescer lock.
type clientErrSummary struct {
	key       clientErrKey
	count     int
	windowDur time.Duration
}

func (s *clientErrSummary) log() {
	if s == nil || s.count <= 1 {
		return
	}
	slog.Info("http request repeated",
		"method", s.key.method,
		"path", s.key.path,
		"status", s.key.status,
		"user_id", s.key.userID,
		"error_code", s.key.code,
		"count", s.count,
		"window", s.windowDur.Round(time.Millisecond).String(),
		"note", "identical 4xx repeated within window; only the first was logged at its real level",
	)
}

type repeatedClientErrCoalescerT struct {
	mu    sync.Mutex
	items map[clientErrKey]*clientErrEntry
}

var repeatedClientErrCoalescer = &repeatedClientErrCoalescerT{
	items: make(map[clientErrKey]*clientErrEntry),
}

// observe records a 4xx occurrence and decides whether it should be logged.
// Returns logFirst=true when this is the first occurrence of a fresh window
// (the caller logs it at the real level). Returns logFirst=false when it is a
// duplicate within the current window (the caller skips the per-request line).
// When a prior window just closed, a non-nil summary is returned for the caller
// to emit AFTER releasing the lock, so we never do I/O under the lock.
func (c *repeatedClientErrCoalescerT) observe(key clientErrKey, now time.Time) (logFirst bool, summary *clientErrSummary) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	if !ok || now.Sub(e.windowStart) >= repeatedClientErrWindow {
		if ok && e.count > 1 {
			summary = &clientErrSummary{key: key, count: e.count, windowDur: now.Sub(e.windowStart)}
		}
		if len(c.items) >= repeatedClientErrCap {
			c.items = make(map[clientErrKey]*clientErrEntry)
		}
		c.items[key] = &clientErrEntry{windowStart: now, count: 1}
		return true, summary
	}
	e.count++
	return false, nil
}

// errorCodeFromBody extracts the response "error" code (e.g.
// "validation_failed", "not_found") from a captured 4xx body prefix, for use
// as part of the coalesce fingerprint. Returns "" when the body is empty or
// not the expected JSON shape — the fingerprint still works, just coarser.
func errorCodeFromBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var v struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &v) != nil {
		return ""
	}
	return v.Error
}

// isSoftNotFound reports whether the captured response body matches one of
// the expected stale-state 404 signals listed in softNotFoundMarkers.
func isSoftNotFound(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	lower := strings.ToLower(string(body))
	for _, marker := range softNotFoundMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
