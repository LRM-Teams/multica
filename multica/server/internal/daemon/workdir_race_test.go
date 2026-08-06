package daemon

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

// TestHandleTask_DoesNotCallStartTaskItself is the regression guard for
// issue #3999 race A. handleTask must not call /tasks/{id}/start before
// runner.run — the runner is now responsible for calling StartTask only
// after execenv.Prepare/Reuse has put env.WorkDir on disk, so consumers
// that read status==running can resolve the workdir path without racing
// the daemon's os.MkdirAll.
//
// Before the fix: handleTask called StartTask before invoking the runner,
// flipping the server-side state to "running" while the per-task workdir
// still didn't exist on disk. Hermes/OpenClaw agents that resolved
// /multica_workspaces/{ws}/{short-id}/workdir from the running signal
// would then hit FileNotFoundError.
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

	d := &Daemon{
		client:             NewClient(srv.URL),
		logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		workspaces:         make(map[string]*workspaceState),
		runtimeIndex:       map[string]Runtime{"rt-1": {ID: "rt-1", Provider: "claude"}},
		activeEnvRoots:     make(map[string]int),
		cancelPollInterval: time.Hour, // disable poll-cancel path; we only care about the entry-side ordering
	}

	// Fake runner that does NOT call StartTask — production runTask does
	// the call itself, after Prepare/Reuse confirms env.WorkDir on disk.
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
	}))
	t.Cleanup(srv.Close)

	d := &Daemon{
		client:         NewClient(srv.URL),
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		workspaces:     make(map[string]*workspaceState),
		runtimeIndex:   map[string]Runtime{"rt-1": {ID: "rt-1", Provider: "claude"}},
		activeEnvRoots: make(map[string]int),
		cfg: Config{
			WorkspacesRoot: t.TempDir(),
			Agents: map[string]AgentEntry{
				"claude": {Path: filepath.Join(t.TempDir(), "unused-agent-binary")},
			},
		},
	}

	result, err := d.runTask(context.Background(), canonicalInboxTaskForTest(Task{
		ID:            "task-chat-no-token",
		WorkspaceID:   "ws-chat-no-token",
		RuntimeID:     "rt-1",
		IssueID:       "issue-chat-no-token",
		ChatSessionID: "chat-1",
		AgentID:       "agent-1",
		Agent:         &AgentData{ID: "agent-1", Name: "test-agent"},
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

func TestRunTask_ChatTransportSetupErrorsFailBeforeAgentExecution(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name                string
		resolveExecutable   func() (string, error)
		prepareCLITransport func(Config, string, string, string, string, string) (string, string, error)
		wantStage           string
	}{
		{
			name:              "executable missing",
			resolveExecutable: func() (string, error) { return "", os.ErrNotExist },
			wantStage:         "resolve_multica_executable",
		},
		{
			name:              "wrapper preparation fails",
			resolveExecutable: func() (string, error) { return "/test/multica", nil },
			prepareCLITransport: func(Config, string, string, string, string, string) (string, string, error) {
				return "", "", os.ErrPermission
			},
			wantStage: "prepare_task_cli_wrapper",
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var startCalls atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/start") {
					startCalls.Add(1)
				}
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(srv.Close)

			d := &Daemon{
				client:              NewClient(srv.URL),
				logger:              slog.New(slog.NewTextHandler(io.Discard, nil)),
				workspaces:          make(map[string]*workspaceState),
				runtimeIndex:        map[string]Runtime{"rt-1": {ID: "rt-1", Provider: "claude"}},
				activeEnvRoots:      make(map[string]int),
				resolveExecutable:   tc.resolveExecutable,
				prepareCLITransport: tc.prepareCLITransport,
				cfg: Config{
					WorkspacesRoot: t.TempDir(),
					Agents: map[string]AgentEntry{
						"claude": {Path: filepath.Join(t.TempDir(), "unused-agent-binary")},
					},
				},
			}

			result, err := d.runTask(context.Background(), canonicalInboxTaskForTest(Task{
				ID:            "task-chat-transport-setup",
				WorkspaceID:   "ws-chat-transport-setup",
				RuntimeID:     "rt-1",
				IssueID:       "issue-chat-transport-setup",
				ChatSessionID: "chat-1",
				AuthToken:     "task-token",
				Agent:         &AgentData{Name: "test-agent"},
			}), "claude", 0, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatalf("runTask error = %v, want fail-closed TaskResult", err)
			}
			if result.Status != "failed" || result.FailureReason != "transport_unavailable" || !strings.Contains(result.Comment, tc.wantStage) {
				t.Fatalf("chat task result = %+v, want transport_unavailable %s", result, tc.wantStage)
			}
			if startCalls.Load() != 0 {
				t.Fatalf("legacy StartTask calls = %d, want zero", startCalls.Load())
			}
		})
	}
}

// TestHandleTask_KeepsEnvRootActiveAcrossCompletion is the regression guard
// for issue #3999 race B. After runner.run returns, the in-process active
// guard installed inside runTask (defer unmarkActiveEnvRoot at the
// goroutine's exit) has already fired by the time handleTask calls
// reportTaskResult and execenv.WriteGCMeta. Without an outer guard at the
// handleTask level, the GC loop sees a window where the directory has
// neither isActiveEnvRoot nor a .gc_meta.json file — falling through to
// orphanByMTime, gated only by the 72h GCOrphanTTL.
//
// This test fakes the inner guard's lifecycle (mark + deferred unmark),
// then asserts that at the moment /complete is hit (i.e. between runner.run
// returning and WriteGCMeta running), isActiveEnvRoot(envRoot) is still
// true thanks to the outer guard handleTask installs.
func TestHandleTask_KeepsEnvRootActiveAcrossCompletion(t *testing.T) {
	t.Parallel()

	workspacesRoot := t.TempDir()
	workspaceID := "ws-active-during-complete"
	taskID := "task-active-during-complete"
	expectedEnvRoot := execenv.PredictRootDir(workspacesRoot, workspaceID, taskID)

	var (
		completeCalled   atomic.Bool
		activeAtComplete atomic.Bool
	)

	d := &Daemon{
		logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		workspaces:         make(map[string]*workspaceState),
		runtimeIndex:       map[string]Runtime{"rt-1": {ID: "rt-1", Provider: "claude"}},
		activeEnvRoots:     make(map[string]int),
		cancelPollInterval: time.Hour,
		cfg:                Config{WorkspacesRoot: workspacesRoot},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/complete") {
			completeCalled.Store(true)
			// This is the exact window race B exposed: the inner deferred
			// unmark has already fired (see fake runner below); only the
			// outer guard installed by handleTask keeps the env root in the
			// active set at this moment.
			if d.isActiveEnvRoot(expectedEnvRoot) {
				activeAtComplete.Store(true)
			}
		}
		w.WriteHeader(http.StatusOK)
		// See the sibling handler in TestHandleTask_DoesNotCallStartTaskItself:
		// an empty body here decodes as io.EOF, which reads as a transient
		// error and sends handleTask into a real multi-second retry backoff
		// instead of the mocked-instant round trip this test expects.
		_, _ = w.Write([]byte("{}"))
	}))
	t.Cleanup(srv.Close)
	d.client = NewClient(srv.URL)

	// Fake runner mimics the real runTask's mark/defer-unmark pair. Without
	// the outer guard added in handleTask, the deferred unmark would bring
	// isActiveEnvRoot back to false before reportTaskResult fires.
	d.runner = taskRunnerFunc(func(_ context.Context, tk Task, _ string, _ int, _ *slog.Logger) (TaskResult, error) {
		predicted := execenv.PredictRootDir(d.cfg.WorkspacesRoot, tk.WorkspaceID, tk.ID)
		d.markActiveEnvRoot(predicted)
		defer d.unmarkActiveEnvRoot(predicted)
		return TaskResult{
			Status:  "completed",
			EnvRoot: predicted,
		}, nil
	})

	task := canonicalInboxTaskForTest(Task{
		ID:          taskID,
		WorkspaceID: workspaceID,
		RuntimeID:   "rt-1",
		IssueID:     "issue-active-during-complete",
		Agent:       &AgentData{Name: "test-agent"},
	})

	d.handleTask(context.Background(), task, 0)

	if !completeCalled.Load() {
		t.Fatal("/complete was never hit — handleTask did not reach reportTaskResult")
	}
	if !activeAtComplete.Load() {
		t.Fatal("env root was NOT in the active set at /complete time — issue #3999 race B regression: GC could reclaim the directory between runner.run returning and WriteGCMeta landing on disk")
	}
	// And the outer guard must have been released by the time handleTask
	// returned, otherwise we'd be leaking active marks across tasks.
	if d.isActiveEnvRoot(expectedEnvRoot) {
		t.Fatal("env root remained active after handleTask returned — outer guard's deferred unmark did not fire")
	}
}
