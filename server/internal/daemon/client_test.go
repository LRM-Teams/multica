package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestClient_IdentityHeaders_PostJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Client-Platform"); got != "daemon" {
			t.Errorf("expected X-Client-Platform daemon, got %q", got)
		}
		if got := r.Header.Get("X-Client-Version"); got != "9.9.9" {
			t.Errorf("expected X-Client-Version 9.9.9, got %q", got)
		}
		if got := r.Header.Get("X-Client-OS"); got != normalizeGOOS(runtime.GOOS) {
			t.Errorf("expected X-Client-OS %q, got %q", normalizeGOOS(runtime.GOOS), got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("expected Authorization Bearer tok, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"ok": "1"})
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.SetToken("tok")
	c.SetVersion("9.9.9")

	if err := c.postJSON(context.Background(), "/api/daemon/test", map[string]any{}, nil); err != nil {
		t.Fatalf("postJSON: %v", err)
	}
}

func TestClient_IdentityHeaders_GetJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Client-Platform"); got != "daemon" {
			t.Errorf("expected X-Client-Platform daemon, got %q", got)
		}
		if got := r.Header.Get("X-Client-Version"); got != "1.2.3" {
			t.Errorf("expected X-Client-Version 1.2.3, got %q", got)
		}
		if got := r.Header.Get("X-Client-OS"); got == "" {
			t.Errorf("expected X-Client-OS to be set")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.SetToken("tok")
	c.SetVersion("1.2.3")

	var out map[string]any
	if err := c.getJSON(context.Background(), "/api/daemon/test", &out); err != nil {
		t.Fatalf("getJSON: %v", err)
	}
}

func TestClient_VersionOmittedWhenUnset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Client-Platform"); got != "daemon" {
			t.Errorf("expected X-Client-Platform daemon, got %q", got)
		}
		// SetVersion not called → header must be omitted (not "").
		if vals := r.Header.Values("X-Client-Version"); len(vals) != 0 {
			t.Errorf("expected X-Client-Version absent, got %v", vals)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	if err := c.postJSON(context.Background(), "/api/daemon/test", nil, nil); err != nil {
		t.Fatalf("postJSON: %v", err)
	}
}

func TestClient_RuntimeScopedCallsUseRuntimeDaemonToken(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/api/daemon/runtimes/rt-1/agents/agent-1/credential" {
			t.Fatalf("path = %q, want ensure credential path", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer mdt-runtime" {
			t.Fatalf("Authorization = %q, want runtime daemon token", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode ensure body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"cred-1","agent_id":"agent-1","token_prefix":"mac_abc","token":"mac_secret"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.SetToken("mul-profile")
	c.SetRuntimeDaemonToken("rt-1", "mdt-runtime", time.Now().Add(time.Hour))

	if _, err := c.EnsureAgentCredential(context.Background(), "rt-1", "agent-1", "cred-cached"); err != nil {
		t.Fatalf("EnsureAgentCredential: %v", err)
	}
	if got, _ := body["credential_id"].(string); got != "cred-cached" {
		t.Fatalf("credential_id = %q, want cred-cached", got)
	}
}

func TestClient_ResetAgentRuntimeSessionUsesRuntimeTokenAndOperationID(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/api/daemon/runtimes/runtime-1/agents/agent-1/session/reset" {
			t.Fatalf("path = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer runtime-token" {
			t.Fatalf("Authorization = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"reset"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.SetRuntimeDaemonToken("runtime-1", "runtime-token", time.Now().Add(time.Hour))
	if err := c.ResetAgentRuntimeSession(context.Background(), "operation-1", "agent-1", "runtime-1"); err != nil {
		t.Fatalf("ResetAgentRuntimeSession: %v", err)
	}
	if body["operation_id"] != "operation-1" {
		t.Fatalf("operation_id = %#v", body["operation_id"])
	}
}

func TestClient_RuntimeScopedCallsSkipExpiredRuntimeDaemonToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/api/daemon/runtimes/rt-1/agents/agent-1/credential" {
			t.Fatalf("path = %q, want ensure credential path", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer mul-profile" {
			t.Fatalf("Authorization = %q, want bootstrap profile token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"cred-1","agent_id":"agent-1","token_prefix":"mac_abc","token":"mac_secret"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.SetToken("mul-profile")
	c.SetRuntimeDaemonToken("rt-1", "mdt-runtime", time.Now().Add(-time.Hour))

	if _, err := c.EnsureAgentCredential(context.Background(), "rt-1", "agent-1", ""); err != nil {
		t.Fatalf("EnsureAgentCredential: %v", err)
	}
}

func TestClient_DrainAgentInboxReturnsFullBatchAndHasMore(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/daemon/runtimes/rt-1/agent-inbox/drain" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"events": [
				{"id":"e1","delivery_id":"d1","conversation_id":"c1","lease_token":"t1","seq_from":1,"seq_to":2,"requires_wake":true},
				{"id":"e2","delivery_id":"d2","conversation_id":"c1","lease_token":"t2","seq_from":3,"seq_to":4,"requires_wake":true}
			],
			"has_more": true,
			"last_seen_seq": 4
		}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL)
	batch, err := c.DrainAgentInbox(context.Background(), "rt-1")
	if err != nil {
		t.Fatalf("DrainAgentInbox: %v", err)
	}
	if batch == nil || !batch.HasMore || len(batch.Events) != 2 {
		t.Fatalf("batch = %#v", batch)
	}
	if batch.Events[0].ID != "e1" || batch.Events[1].ID != "e2" {
		t.Fatalf("events = %#v", batch.Events)
	}
	if batch.Events[0].RuntimeID != "rt-1" || batch.Events[1].RuntimeID != "rt-1" {
		t.Fatalf("runtime not stamped: %#v", batch.Events)
	}
}

func TestClient_CompleteAgentInboxEventSendsInternalOutput(t *testing.T) {
	var body map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/api/daemon/agent-inbox/events/event-1/complete" {
			t.Fatalf("path = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"acked_seq":42,"terminal_outcome":"failed","resume_unsafe":true}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.SetToken("profile-token")
	lease := AgentInboxLease{ID: "event-1", DeliveryID: "delivery-1", LeaseToken: "lease-1"}
	internal := json.RawMessage(`{"decision":"SILENT","confidence":0.1}`)
	usage := []TaskUsageEntry{{Provider: "openai", Model: "gpt-5", InputTokens: 7, OutputTokens: 2}}
	receipt, err := c.CompleteAgentInboxEvent(context.Background(), lease, TaskResult{ExecutionID: "execution-1", InternalOutput: internal, Usage: usage, TransportAttempted: true})
	if err != nil {
		t.Fatalf("CompleteAgentInboxEvent: %v", err)
	}
	if !receipt.OK || receipt.AckedSeq != 42 || receipt.TerminalOutcome != "failed" || !receipt.ResumeUnsafe {
		t.Fatalf("completion receipt = %+v", receipt)
	}
	if got := string(body["internal_output"]); got != string(internal) {
		t.Fatalf("internal_output = %s, want %s", got, internal)
	}
	if got := string(body["execution_id"]); got != `"execution-1"` {
		t.Fatalf("execution_id = %s", got)
	}
	if got := string(body["transport_attempted"]); got != `true` {
		t.Fatalf("transport_attempted = %s, want true", got)
	}
	var gotUsage []TaskUsageEntry
	if err := json.Unmarshal(body["usage"], &gotUsage); err != nil {
		t.Fatalf("decode usage: %v", err)
	}
	if !reflect.DeepEqual(gotUsage, usage) {
		t.Fatalf("usage = %#v, want %#v", gotUsage, usage)
	}
}

func TestClient_CompleteAgentInboxEventSendsTypedChannelOnboardingDecision(t *testing.T) {
	var body map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.SetToken("profile-token")
	lease := AgentInboxLease{ID: "onboarding-event", DeliveryID: "delivery-1", LeaseToken: "lease-1"}
	if _, err := c.CompleteAgentInboxEvent(context.Background(), lease, TaskResult{
		ChannelOnboardingDecision: protocol.ChannelOnboardingDecisionSkipped,
	}); err != nil {
		t.Fatalf("CompleteAgentInboxEvent: %v", err)
	}
	if got := string(body["channel_onboarding_decision"]); got != `"`+protocol.ChannelOnboardingDecisionSkipped+`"` {
		t.Fatalf("channel_onboarding_decision = %s", got)
	}
}

func TestClient_RegisterForWorkspaceUsesBootstrapThenWorkspaceDaemonToken(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		wantAuth := "Bearer mul-profile"
		if call == 2 {
			wantAuth = "Bearer mdt-workspace"
		}
		if got := r.Header.Get("Authorization"); got != wantAuth {
			t.Fatalf("call %d Authorization = %q, want %q", call, got, wantAuth)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"runtimes":[{"id":"rt-1","workspace_id":"ws-1","provider":"pi"}]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.SetToken("mul-profile")
	if _, err := c.RegisterForWorkspace(context.Background(), "ws-1", map[string]any{}); err != nil {
		t.Fatalf("first RegisterForWorkspace: %v", err)
	}
	c.SetWorkspaceDaemonToken("ws-1", "mdt-workspace", time.Now().Add(time.Hour))
	if _, err := c.RegisterForWorkspace(context.Background(), "ws-1", map[string]any{}); err != nil {
		t.Fatalf("second RegisterForWorkspace: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("calls = %d, want 2", got)
	}
}

func TestClient_RegisterForWorkspaceSkipsExpiredWorkspaceDaemonToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer mul-profile" {
			t.Fatalf("Authorization = %q, want bootstrap profile token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"runtimes":[{"id":"rt-1","workspace_id":"ws-1","provider":"pi"}]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.SetToken("mul-profile")
	c.SetWorkspaceDaemonToken("ws-1", "mdt-workspace", time.Now().Add(-time.Hour))

	if _, err := c.RegisterForWorkspace(context.Background(), "ws-1", map[string]any{}); err != nil {
		t.Fatalf("RegisterForWorkspace: %v", err)
	}
}

// noSleepRetry replaces retrySleep with an immediate no-op so tests don't
// actually wait the 4s/8s/16s/... backoffs. Returns a restore func.
func noSleepRetry(t *testing.T) func() {
	t.Helper()
	prev := retrySleep
	retrySleep = func(ctx context.Context, _ time.Duration) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return nil
	}
	return func() { retrySleep = prev }
}

func TestIsTransientError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil is not transient", nil, false},
		{"5xx is transient", &requestError{StatusCode: http.StatusBadGateway}, true},
		{"503 is transient", &requestError{StatusCode: http.StatusServiceUnavailable}, true},
		{"408 is transient", &requestError{StatusCode: http.StatusRequestTimeout}, true},
		{"429 is transient", &requestError{StatusCode: http.StatusTooManyRequests}, true},
		{"400 is permanent", &requestError{StatusCode: http.StatusBadRequest}, false},
		{"401 is permanent", &requestError{StatusCode: http.StatusUnauthorized}, false},
		{"404 is permanent", &requestError{StatusCode: http.StatusNotFound}, false},
		{"transport-level error is transient", errors.New("connection reset by peer"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTransientError(tc.err); got != tc.want {
				t.Fatalf("isTransientError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestPostJSONWithRetry_TransientThenSuccess(t *testing.T) {
	defer noSleepRetry(t)()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	schedule := []time.Duration{time.Nanosecond, time.Nanosecond, time.Nanosecond}
	if err := c.postJSONWithRetry(context.Background(), "/x", map[string]any{}, nil, schedule); err != nil {
		t.Fatalf("postJSONWithRetry: %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("expected 3 attempts (2 transient + 1 success), got %d", got)
	}
}

func TestPostJSONWithRetry_TransientExhausts(t *testing.T) {
	defer noSleepRetry(t)()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	schedule := []time.Duration{time.Nanosecond, time.Nanosecond}
	err := c.postJSONWithRetry(context.Background(), "/x", map[string]any{}, nil, schedule)
	if err == nil {
		t.Fatal("expected error after schedule exhausted, got nil")
	}
	if !isTransientError(err) {
		t.Fatalf("expected transient error, got %v", err)
	}
	if got := calls.Load(); got != int32(len(schedule)+1) {
		t.Fatalf("expected %d attempts (initial + %d retries), got %d", len(schedule)+1, len(schedule), got)
	}
}

func TestPostJSONWithRetry_PermanentBailsImmediately(t *testing.T) {
	defer noSleepRetry(t)()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	schedule := []time.Duration{time.Nanosecond, time.Nanosecond, time.Nanosecond}
	err := c.postJSONWithRetry(context.Background(), "/x", map[string]any{}, nil, schedule)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected exactly 1 attempt on permanent error, got %d", got)
	}
}

func TestPostJSONWithRetry_CtxCancelStopsRetries(t *testing.T) {
	// Use the real sleeper here so we can observe a cancel preempting it.
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// Cancel quickly so the first sleep is aborted long before its 1s.
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	c := NewClient(srv.URL)
	schedule := []time.Duration{time.Second, time.Second, time.Second}
	start := time.Now()
	err := c.postJSONWithRetry(ctx, "/x", map[string]any{}, nil, schedule)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error after ctx cancel, got nil")
	}
	if elapsed > 750*time.Millisecond {
		t.Fatalf("expected ctx cancel to short-circuit retry, took %s", elapsed)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected exactly 1 attempt before cancel, got %d", got)
	}
}

func TestDefaultTerminalRetrySchedule_MatchesAgreedPlan(t *testing.T) {
	// MUL-2780 settled on a 5-step exponential backoff (4s, 8s, 16s, 32s, 64s).
	// Pin it so a future "tidy this up" refactor can't silently flatten or
	// shorten the recovery window without explicit discussion.
	want := []time.Duration{4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, 64 * time.Second}
	if len(defaultTerminalRetrySchedule) != len(want) {
		t.Fatalf("schedule length: got %d, want %d", len(defaultTerminalRetrySchedule), len(want))
	}
	for i, d := range want {
		if defaultTerminalRetrySchedule[i] != d {
			t.Errorf("schedule[%d]: got %s, want %s", i, defaultTerminalRetrySchedule[i], d)
		}
	}
}

func TestNormalizeGOOS(t *testing.T) {
	cases := map[string]string{
		"darwin":  "macos",
		"windows": "windows",
		"linux":   "linux",
		"freebsd": "freebsd",
	}
	for in, want := range cases {
		if got := normalizeGOOS(in); got != want {
			t.Errorf("normalizeGOOS(%q) = %q, want %q", in, got, want)
		}
	}
}
