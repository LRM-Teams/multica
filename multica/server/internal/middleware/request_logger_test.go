package middleware

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// withCapturedLogs swaps the default slog logger for one that writes to buf,
// then restores it on cleanup. Returns the buffer so tests can inspect what
// RequestLogger emitted.
//
// Uses a shared mutex because t.Parallel tests would otherwise race on the
// global slog.Default — tests in this file intentionally do NOT run in
// parallel for that reason.
var defaultLoggerMu sync.Mutex

func withCapturedLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	defaultLoggerMu.Lock()
	buf := &bytes.Buffer{}
	orig := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() {
		slog.SetDefault(orig)
		defaultLoggerMu.Unlock()
	})
	return buf
}

func runRequestLogger(t *testing.T, status int, body string) *bytes.Buffer {
	t.Helper()
	resetClientErrCoalescerForTest()
	logs := withCapturedLogs(t)
	handler := RequestLogger(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/daemon/heartbeat", nil).
		WithContext(context.Background())
	handler.ServeHTTP(httptest.NewRecorder(), req)
	return logs
}

// resetClientErrCoalescerForTest clears the process-global 4xx coalescer so a
// prior test's fingerprint cannot suppress a later test's single request. The
// coalescer is intentionally package-global (one flood per process); tests that
// exercise flooding reset it once at the start of their own burst.
func resetClientErrCoalescerForTest() {
	repeatedClientErrCoalescer.mu.Lock()
	repeatedClientErrCoalescer.items = make(map[clientErrKey]*clientErrEntry)
	repeatedClientErrCoalescer.mu.Unlock()
}

// countLevel counts how many captured log lines carry the given slog level.
func countLevel(logs *bytes.Buffer, level string) int {
	return strings.Count(logs.String(), "level="+level+" ") + strings.Count(logs.String(), "level="+level+"\n")
}

// requireLogLevel asserts that the captured output contains exactly the
// expected slog level prefix and not any of the disallowed ones.
func requireLogLevel(t *testing.T, logs *bytes.Buffer, want string, disallowed ...string) {
	t.Helper()
	out := logs.String()
	if !strings.Contains(out, "level="+want) {
		t.Fatalf("expected level=%s in logs, got:\n%s", want, out)
	}
	for _, dis := range disallowed {
		if strings.Contains(out, "level="+dis) {
			t.Fatalf("did not expect level=%s in logs, got:\n%s", dis, out)
		}
	}
}

func TestRequestLogger_RuntimeNotFound404DowngradesToInfo(t *testing.T) {
	// The whole reason this middleware change exists: a flood of WRN lines
	// after a runtime is deleted (issue #2391). The daemon catches the same
	// body and self-heals, so the line is signal-not-noise.
	logs := runRequestLogger(t, http.StatusNotFound, `{"error":"runtime not found"}`)
	requireLogLevel(t, logs, "INFO", "WARN", "ERROR")
}

func TestRequestLogger_TaskNotFound404DowngradesToInfo(t *testing.T) {
	logs := runRequestLogger(t, http.StatusNotFound, `{"error":"task not found"}`)
	requireLogLevel(t, logs, "INFO", "WARN", "ERROR")
}

func TestRequestLogger_GenericNotFound404KeepsWarn(t *testing.T) {
	// A 404 with an unfamiliar body is still a real 404 — most likely a
	// daemon hitting a wrong path, which is what Warn is for. We do NOT
	// want to downgrade these blindly.
	logs := runRequestLogger(t, http.StatusNotFound, `{"error":"not found"}`)
	requireLogLevel(t, logs, "WARN", "INFO", "ERROR")
}

func TestRequestLogger_400StaysWarn(t *testing.T) {
	logs := runRequestLogger(t, http.StatusBadRequest, `{"error":"bad input"}`)
	requireLogLevel(t, logs, "WARN", "INFO", "ERROR")
}

func TestRequestLogger_500StaysError(t *testing.T) {
	logs := runRequestLogger(t, http.StatusInternalServerError, `{"error":"boom"}`)
	requireLogLevel(t, logs, "ERROR", "WARN", "INFO")
}

func TestRequestLogger_200StaysInfo(t *testing.T) {
	logs := runRequestLogger(t, http.StatusOK, `{"ok":true}`)
	requireLogLevel(t, logs, "INFO", "WARN", "ERROR")
}

func TestRequestLogger_HealthEndpointIsSkipped(t *testing.T) {
	logs := withCapturedLogs(t)
	handler := RequestLogger(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if logs.Len() != 0 {
		t.Fatalf("/health should not be logged, got:\n%s", logs.String())
	}
}

func TestRequestLogger_BodyStillReachesClient(t *testing.T) {
	// The body capture is implemented via Tee, which must mirror writes
	// rather than swallow them. Regress-protect: assert the response writer
	// still gets the full body.
	rec := httptest.NewRecorder()
	handler := RequestLogger(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"runtime not found"}`))
	}))
	_ = withCapturedLogs(t)
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/daemon/heartbeat", nil))
	if got := rec.Body.String(); got != `{"error":"runtime not found"}` {
		t.Fatalf("response body lost or mutated: got %q", got)
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestRequestLogger_LargeBodyBeyondCaptureLimit(t *testing.T) {
	// If the soft-404 marker only appears beyond the capture limit we
	// intentionally keep Warn — capturing arbitrary-size bodies is the
	// memory blowup we are guarding against. This test pins that
	// trade-off.
	prefix := strings.Repeat("x", softNotFoundBodyCaptureLimit+8)
	logs := runRequestLogger(t, http.StatusNotFound, prefix+`{"error":"runtime not found"}`)
	requireLogLevel(t, logs, "WARN", "INFO", "ERROR")
}

func TestRedactWebhookPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"/api/webhooks/autopilots/awt_secret", "/api/webhooks/autopilots/[redacted]"},
		{"/api/webhooks/autopilots/awt_secret/", "/api/webhooks/autopilots/[redacted]/"},
		{"/api/webhooks/autopilots/", "/api/webhooks/autopilots/"},
		{"/api/webhooks/github", "/api/webhooks/github"},
		{"/api/runtimes/abc", "/api/runtimes/abc"},
		{"/", "/"},
	}
	for _, tc := range cases {
		if got := redactWebhookPath(tc.in); got != tc.want {
			t.Errorf("redactWebhookPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRequestLogger_RedactsWebhookTokenInPath(t *testing.T) {
	logs := withCapturedLogs(t)
	handler := RequestLogger(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/autopilots/awt_supersecret", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)
	out := logs.String()
	if strings.Contains(out, "awt_supersecret") {
		t.Fatalf("token leaked into logs:\n%s", out)
	}
	if !strings.Contains(out, "[redacted]") {
		t.Fatalf("expected [redacted] in logs:\n%s", out)
	}
}

func TestRequestLogger_IncludesWebhookTriggerIDFromContext(t *testing.T) {
	// Exercise the real production flow: the webhook handler resolves the
	// trigger, then calls SetWebhookTriggerID(r, ...) which mutates *r in
	// place. After the handler returns, the wrapping RequestLogger
	// middleware reads the stashed ID off the (now-updated) request
	// context. If SetWebhookTriggerID didn't mutate in place, the
	// middleware would see the old context and the trigger ID would
	// silently drop from the audit line.
	logs := withCapturedLogs(t)
	handler := RequestLogger(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetWebhookTriggerID(r, "trigger-abc")
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/autopilots/awt_supersecret", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)
	out := logs.String()
	if !strings.Contains(out, "webhook_trigger_id=trigger-abc") {
		t.Fatalf("expected webhook_trigger_id in logs, got:\n%s", out)
	}
	if strings.Contains(out, "awt_supersecret") {
		t.Fatalf("token leaked into logs:\n%s", out)
	}
}

func TestIsSoftNotFound(t *testing.T) {
	t.Parallel()

	cases := []struct {
		body string
		want bool
	}{
		{`{"error":"runtime not found"}`, true},
		{`{"error":"task not found"}`, true},
		{`{"error":"Runtime Not Found"}`, true},
		{`{"error":"not found"}`, false},
		{`{"error":"workspace not found"}`, false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isSoftNotFound([]byte(tc.body)); got != tc.want {
			t.Errorf("isSoftNotFound(%q) = %v, want %v", tc.body, got, tc.want)
		}
	}
}

// TestRequestLogger_RepeatedIdentical400FloodIsCoalesced is the core LRM-640
// regression: a runaway client hammering one endpoint with the same malformed
// body must not emit one WARN line per request. Within a single coalesce
// window only the first occurrence logs; the rest are counted for a summary.
func TestRequestLogger_RepeatedIdentical400FloodIsCoalesced(t *testing.T) {
	resetClientErrCoalescerForTest()
	logs := withCapturedLogs(t)

	const n = 200 // ~the prod flood rate for several seconds
	handler := RequestLogger(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"validation_failed","message":"message.content required"}`))
	}))
	for i := 0; i < n; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/env-dispatch", nil).
			WithContext(context.Background())
		req.Header.Set("X-User-ID", "d405e7b6-6a17-44c8-9dfc-bd6c84f5c80d")
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}

	out := logs.String()
	warnCount := countLevel(logs, "WARN")
	if warnCount != 1 {
		t.Fatalf("expected exactly 1 WARN (first of window) for %d identical 400s, got %d. logs:\n%s", n, warnCount, out)
	}
	// The per-request WARN line never carried the response body (only the
	// endpoint identity); assert it still pinpoints the runaway endpoint.
	if !strings.Contains(out, "path=/api/v1/env-dispatch") || !strings.Contains(out, "user_id=d405e7b6") {
		t.Fatalf("the single WARN line must still identify the endpoint and caller, got:\n%s", out)
	}
	if strings.Contains(out, "http request repeated") {
		t.Fatalf("summary must not fire mid-window (window not rolled yet), got:\n%s", out)
	}
}

// TestRequestLogger_Distinct400sAreNotCoalesced guards against over-coalescing:
// requests that differ by user, path, or error code must each get their own
// WARN line. Only byte-identical-in-fingerprint repeats collapse.
func TestRequestLogger_Distinct400sAreNotCoalesced(t *testing.T) {
	resetClientErrCoalescerForTest()
	logs := withCapturedLogs(t)

	variants := []struct {
		path, user, body string
	}{
		{"/api/v1/env-dispatch", "user-a", `{"error":"validation_failed"}`},
		{"/api/v1/env-dispatch", "user-b", `{"error":"validation_failed"}`}, // different user
		{"/api/v1/other", "user-a", `{"error":"validation_failed"}`},         // different path
		{"/api/v1/env-dispatch", "user-a", `{"error":"not_found"}`},         // different code
	}
	for _, v := range variants {
		req := httptest.NewRequest(http.MethodPost, v.path, nil).WithContext(context.Background())
		req.Header.Set("X-User-ID", v.user)
		h := RequestLogger(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(v.body))
		}))
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	if got := countLevel(logs, "WARN"); got != len(variants) {
		t.Fatalf("expected %d distinct WARN lines, got %d. logs:\n%s", len(variants), got, logs.String())
	}
}

// TestRepeatedClientErrCoalescer_WindowRolloverEmitsSummary drives the
// coalescer directly with a synthetic clock so we can assert that a closed
// window of repeats produces exactly one INFO summary with the right count,
// and that the first request of the new window logs normally again.
func TestRepeatedClientErrCoalescer_WindowRolloverEmitsSummary(t *testing.T) {
	resetClientErrCoalescerForTest()
	logs := withCapturedLogs(t)

	key := clientErrKey{method: "POST", path: "/api/v1/env-dispatch", status: 400, userID: "u1", code: "validation_failed"}
	t0 := time.Now()

	// First in window: logs normally.
	if logFirst, summary := repeatedClientErrCoalescer.observe(key, t0); !logFirst || summary != nil {
		t.Fatalf("first: expected logFirst=true summary=nil, got %v %v", logFirst, summary)
	}
	// 49 duplicates inside the window: suppressed, no summary yet.
	for i := 0; i < 49; i++ {
		logFirst, summary := repeatedClientErrCoalescer.observe(key, t0.Add(1*time.Second))
		if logFirst || summary != nil {
			t.Fatalf("dup %d: expected suppressed (logFirst=false summary=nil), got %v %v", i, logFirst, summary)
		}
	}
	// Window rolls over: first of new window logs normally AND returns a
	// summary for the just-closed window (count=50).
	logFirst, summary := repeatedClientErrCoalescer.observe(key, t0.Add(repeatedClientErrWindow+1))
	if !logFirst || summary == nil {
		t.Fatalf("rollover: expected logFirst=true summary!=nil, got %v %v", logFirst, summary)
	}
	summary.log()

	out := logs.String()
	if c := strings.Count(out, "http request repeated"); c != 1 {
		t.Fatalf("expected exactly 1 summary line, got %d. logs:\n%s", c, out)
	}
	if !strings.Contains(out, "count=50") {
		t.Fatalf("summary must report count=50 (1 logged + 49 suppressed), got:\n%s", out)
	}
	if !strings.Contains(out, "validation_failed") || !strings.Contains(out, "/api/v1/env-dispatch") {
		t.Fatalf("summary must identify the fingerprint (path + code), got:\n%s", out)
	}
}

// TestErrorCodeFromBody covers the fingerprint code extraction.
func TestErrorCodeFromBody(t *testing.T) {
	t.Parallel()
	cases := []struct {
		body string
		want string
	}{
		{`{"error":"validation_failed","message":"x"}`, "validation_failed"},
		{`{"error":"not_found"}`, "not_found"},
		{"", ""},
		{"not json", ""},
		{"{}", ""},
	}
	for _, tc := range cases {
		if got := errorCodeFromBody([]byte(tc.body)); got != tc.want {
			t.Errorf("errorCodeFromBody(%q) = %q, want %q", tc.body, got, tc.want)
		}
	}
}
