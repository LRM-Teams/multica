package daemon

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// TestReportTaskFailure_ForceKilledTurnGetsRestartedByUserReason pins task
// #62's reason-code wiring: a Result.Error carrying agent.AgentForceKilledMarker
// (set by any of the four canonical-resident backends' ForceKill path) must
// surface as reason_code="restarted_by_user" on the wire to
// FailAgentInboxEvent — not fall through to the generic
// ProviderAuthRequiredMarker/empty-string path, and not read as an
// unexplained crash. Mirrors the existing ProviderAuthRequiredMarker check
// in shape (see reportTaskFailure), not a new mechanism.
func TestReportTaskFailure_ForceKilledTurnGetsRestartedByUserReason(t *testing.T) {
	var gotReasonCode string
	var gotFailureReason string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if v, ok := body["reason_code"].(string); ok {
			gotReasonCode = v
		}
		if v, ok := body["failure_reason"].(string); ok {
			gotFailureReason = v
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	d := &Daemon{client: client, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	taskLog := slog.New(slog.NewTextHandler(io.Discard, nil))

	task := Task{
		InboxEvent: &AgentInboxLease{
			ID:         "event-1",
			DeliveryID: "delivery-1",
			LeaseToken: "lease-1",
		},
	}

	errMsg := agent.AgentForceKilledMarker + ": cursor ACP session/prompt: read |0: file already closed"
	d.reportTaskFailure(t.Context(), task, errMsg, "session-1", "/work/dir", "process_failure", taskLog)

	if gotReasonCode != "restarted_by_user" {
		t.Fatalf("reason_code = %q, want %q", gotReasonCode, "restarted_by_user")
	}
	// failure_reason is untouched — reportTaskFailure only overrides
	// reason_code, matching the ProviderAuthRequiredMarker precedent, which
	// also leaves failure_reason alone.
	if gotFailureReason != "process_failure" {
		t.Fatalf("failure_reason = %q, want unchanged %q", gotFailureReason, "process_failure")
	}
}

// TestReportTaskFailure_OrdinaryFailureKeepsItsOwnReason confirms the new
// check doesn't leak into unrelated failures — a genuine crash's
// failure_reason still passes through as reason_code, unaffected.
func TestReportTaskFailure_OrdinaryFailureKeepsItsOwnReason(t *testing.T) {
	var gotReasonCode string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if v, ok := body["reason_code"].(string); ok {
			gotReasonCode = v
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	d := &Daemon{client: client, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	taskLog := slog.New(slog.NewTextHandler(io.Discard, nil))

	task := Task{
		InboxEvent: &AgentInboxLease{ID: "event-1", DeliveryID: "delivery-1", LeaseToken: "lease-1"},
	}

	d.reportTaskFailure(t.Context(), task, "provider returned 500 internal error", "session-1", "/work/dir", "process_failure", taskLog)

	if gotReasonCode != "process_failure" {
		t.Fatalf("reason_code = %q, want unchanged %q (a genuine failure must not get relabeled restarted_by_user)", gotReasonCode, "process_failure")
	}
}
