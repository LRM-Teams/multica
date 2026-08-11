package daemon

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type agentStartIntentReport struct {
	Status        string `json:"status"`
	LifecycleSeq  int64  `json:"lifecycle_seq"`
	FailureCode   string `json:"failure_code"`
	RuntimeID     string
	StartDispatch string
}

func captureAgentStartIntentReports(t *testing.T) (*httptest.Server, func() []agentStartIntentReport) {
	t.Helper()
	var mu sync.Mutex
	var reports []agentStartIntentReport
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var report agentStartIntentReport
		if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
			t.Fatalf("decode start intent report: %v", err)
		}
		const prefix = "/api/daemon/runtimes/"
		const marker = "/agent-start-intents/"
		if len(r.URL.Path) <= len(prefix) || r.URL.Path[:len(prefix)] != prefix {
			t.Fatalf("unexpected report path %q", r.URL.Path)
		}
		rest := r.URL.Path[len(prefix):]
		parts := strings.Split(rest, marker)
		if len(parts) != 2 || !strings.HasSuffix(parts[1], "/report") {
			t.Fatalf("unexpected report path %q", r.URL.Path)
		}
		report.RuntimeID = parts[0]
		report.StartDispatch = strings.TrimSuffix(parts[1], "/report")
		mu.Lock()
		reports = append(reports, report)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	return server, func() []agentStartIntentReport {
		mu.Lock()
		defer mu.Unlock()
		return append([]agentStartIntentReport(nil), reports...)
	}
}

func TestHandleAgentStartIntentReportsAcceptedThenIndependentReady(t *testing.T) {
	server, reports := captureAgentStartIntentReports(t)
	defer server.Close()

	const workspaceID = "11111111-1111-4111-8111-111111111111"
	const runtimeID = "22222222-2222-4222-8222-222222222222"
	const agentID = "33333333-3333-4333-8333-333333333333"
	const dispatchID = "44444444-4444-4444-8444-444444444444"
	d := New(Config{ServerBaseURL: server.URL, WorkspacesRoot: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: workspaceID}
	var frames []struct {
		eventType string
		payload   any
	}
	d.attachWorkspaceRunnerMessageTransport(workspaceID, func(eventType string, payload any) error {
		frames = append(frames, struct {
			eventType string
			payload   any
		}{eventType: eventType, payload: payload})
		return nil
	})

	d.handleAgentStartIntent(t.Context(), protocol.DaemonHeartbeatPendingAgentStartIntent{
		StartDispatchID: dispatchID, AgentID: agentID, RuntimeID: runtimeID, WorkspaceID: workspaceID,
	})

	got := reports()
	if len(got) != 2 {
		t.Fatalf("reports = %#v, want accepted then ready", got)
	}
	if got[0].Status != "accepted" || got[0].LifecycleSeq != 1 || got[0].FailureCode != "" {
		t.Fatalf("acceptance report = %#v", got[0])
	}
	if got[1].Status != "ready" || got[1].LifecycleSeq != 2 || got[1].FailureCode != "" {
		t.Fatalf("ready report = %#v", got[1])
	}
	for _, report := range got {
		if report.RuntimeID != runtimeID || report.StartDispatch != dispatchID {
			t.Fatalf("report target = %#v", report)
		}
	}
	if !d.agentStartIntentReady(protocol.DaemonHeartbeatPendingAgentStartIntent{
		AgentID: agentID, RuntimeID: runtimeID, WorkspaceID: workspaceID,
	}) {
		t.Fatal("ready observation did not find the installed local residency, root, and coordinator")
	}
	if len(frames) != 3 {
		t.Fatalf("Workspace Runner initialization frames = %#v, want active status, session, then Message recovery", frames)
	}
	status, ok := frames[0].payload.(protocol.AgentStatusPayload)
	if frames[0].eventType != protocol.EventAgentStatus || !ok || status.AgentID != agentID || status.Status != protocol.AgentStatusActive || status.LaunchID == "" {
		t.Fatalf("first Workspace Runner lifecycle frame = %#v, want active Agent status", frames[0])
	}
	session, ok := frames[1].payload.(protocol.AgentSessionPayload)
	if frames[1].eventType != protocol.EventAgentSession || !ok || session.AgentID != agentID || session.LaunchID != status.LaunchID {
		t.Fatalf("second Workspace Runner lifecycle frame = %#v, want matching Agent session", frames[1])
	}
	if frames[2].eventType != protocol.EventAgentRecoveryRequest {
		t.Fatalf("third Workspace Runner initialization frame = %#v, want Message recovery request", frames[2])
	}
}

func TestHandleAgentStartIntentReportsTerminalFailureForUnknownLocalRuntime(t *testing.T) {
	server, reports := captureAgentStartIntentReports(t)
	defer server.Close()

	d := New(Config{ServerBaseURL: server.URL, WorkspacesRoot: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.handleAgentStartIntent(t.Context(), protocol.DaemonHeartbeatPendingAgentStartIntent{
		StartDispatchID: "44444444-4444-4444-8444-444444444444",
		AgentID:         "33333333-3333-4333-8333-333333333333",
		RuntimeID:       "22222222-2222-4222-8222-222222222222",
		WorkspaceID:     "11111111-1111-4111-8111-111111111111",
	})

	got := reports()
	if len(got) != 1 || got[0].Status != "failed" || got[0].LifecycleSeq != 1 || got[0].FailureCode != "local_runtime_unavailable" {
		t.Fatalf("reports = %#v, want one terminal unavailable failure", got)
	}
}
