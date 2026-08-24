package daemon

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	workdirRaceWorkspaceID = "11111111-1111-1111-1111-111111111111"
	workdirRaceAgentID     = "22222222-2222-2222-2222-222222222222"
)

// TestHandleTask_DoesNotCallStartTaskItself is the regression guard for
// issue #3999 race A. handleTask must not call /tasks/{id}/start before
// runner.run — the runner is now responsible for calling StartTask only
// after execenv.Prepare/Reuse has provisioned the AgentRoot on disk, so consumers
// that read status==running can resolve the workdir path without racing
// the daemon's os.MkdirAll.
//
// Before the fix: handleTask called StartTask before invoking the runner,
// flipping the server-side state to "running" while the working directory
// still didn't exist on disk. Providers resolving the cwd from the running
// signal would then hit FileNotFoundError.
func TestHandleTask_DoesNotCallStartTaskItself(t *testing.T) {
	t.Parallel()

	var (
		startCalls   atomic.Int64
		runnerCalled atomic.Bool
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/start"):
			startCalls.Add(1)
		}
		w.WriteHeader(http.StatusOK)
		// Every daemon-facing endpoint here (including /complete, hit via
		// reportTaskResultForTask) decodes a JSON response body. An empty
		// body decodes as io.EOF, which isTransientError's fallback treats
		// as transient — silently sending handleTask into the real
		// (non-mocked) defaultTerminalRetrySchedule backoff and blowing
		// past any reasonable test timeout. See task #82.
		_, _ = w.Write([]byte("{}"))
	}))
	t.Cleanup(srv.Close)

	d := &WorkspaceDaemonCore{
		client:             NewClient(srv.URL),
		logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		workspaces:         make(map[string]*workspaceState),
		runtimeIndex:       map[string]Runtime{"rt-1": {ID: "rt-1", Provider: "claude"}},
		cancelPollInterval: time.Hour, // disable poll-cancel path; we only care about the entry-side ordering
	}

	// Fake runner that does NOT call StartTask — production runTask does
	// the call itself, after Prepare/Reuse confirms AgentRoot on disk.
	d.runner = taskRunnerFunc(func(_ context.Context, _ Task, _ string, _ int, _ *slog.Logger) (TaskResult, error) {
		runnerCalled.Store(true)
		return TaskResult{Status: "completed"}, nil
	})

	task := canonicalInboxTaskForTest(Task{
		ID:          "task-no-start",
		WorkspaceID: "ws-no-start",
		RuntimeID:   "rt-1",
		IssueID:     "issue-no-start",
		Agent:       &AgentData{Name: "test-agent"},
	})

	d.handleTask(context.Background(), task, 0)

	if !runnerCalled.Load() {
		t.Fatal("fake runner was never invoked — handleTask aborted before runner.run, can't assert ordering")
	}
	if got := startCalls.Load(); got != 0 {
		t.Fatalf("handleTask called /start %d time(s); StartTask must be runTask's responsibility now (issue #3999 race A)", got)
	}
}

func TestRunTask_ChatWithoutRunTokenFailsBeforeAgentExecution(t *testing.T) {
	t.Parallel()

	var startCalls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/start") {
			startCalls.Add(1)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	t.Cleanup(srv.Close)

	d := &WorkspaceDaemonCore{
		client:       NewClient(srv.URL),
		logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		workspaces:   make(map[string]*workspaceState),
		runtimeIndex: map[string]Runtime{"rt-1": {ID: "rt-1", Provider: "claude"}},
		cfg: Config{
			WorkspacesRoot: t.TempDir(),
			Agents: map[string]AgentEntry{
				"claude": {Path: filepath.Join(t.TempDir(), "unused-agent-binary")},
			},
		},
	}

	result, err := d.runTask(context.Background(), canonicalInboxTaskForTest(Task{
		ID:            "task-chat-no-token",
		WorkspaceID:   workdirRaceWorkspaceID,
		RuntimeID:     "rt-1",
		IssueID:       "issue-chat-no-token",
		ChatSessionID: "chat-1",
		AgentID:       workdirRaceAgentID,
		Agent:         &AgentData{ID: workdirRaceAgentID, Name: "test-agent"},
	}), "claude", 0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("runTask error = %v, want fail-closed TaskResult", err)
	}
	if result.Status != "failed" || result.FailureReason != "credential_unavailable" {
		t.Fatalf("chat task result = %+v, want credential_unavailable", result)
	}
	if startCalls.Load() != 0 {
		t.Fatalf("legacy StartTask calls = %d, want zero", startCalls.Load())
	}
}
