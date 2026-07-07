package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestShouldRunPiMemoryPostRun(t *testing.T) {
	longOutput := strings.Repeat("x", DefaultPiMemoryPostRunMinOutputChars)
	tests := []struct {
		name       string
		provider   string
		cfg        Config
		task       Task
		result     TaskResult
		tools      int32
		wantRun    bool
		wantModel  bool
		wantReason string
	}{
		{
			name:       "short no tools skipped",
			provider:   "pi",
			result:     TaskResult{Status: "completed", Comment: "ok", SessionID: "session.jsonl"},
			wantReason: "short_no_tools",
		},
		{
			name:       "non pi skipped",
			provider:   "codex",
			result:     TaskResult{Status: "completed", Comment: "done", SessionID: "session.jsonl"},
			tools:      3,
			wantReason: "non_pi_provider",
		},
		{
			name:       "no session skipped",
			provider:   "pi",
			result:     TaskResult{Status: "completed", Comment: "done"},
			tools:      1,
			wantReason: "no_session",
		},
		{
			name:       "tool call triggers structured extraction",
			provider:   "pi",
			result:     TaskResult{Status: "completed", Comment: "done", SessionID: "session.jsonl"},
			tools:      1,
			wantRun:    true,
			wantReason: "tools",
		},
		{
			name:       "long output triggers model in auto mode",
			provider:   "pi",
			result:     TaskResult{Status: "completed", Comment: longOutput, SessionID: "session.jsonl"},
			wantRun:    true,
			wantModel:  true,
			wantReason: "long_output",
		},
		{
			name:       "always mode triggers short output",
			provider:   "pi",
			cfg:        Config{PiMemoryPostRun: "always"},
			result:     TaskResult{Status: "completed", Comment: "ok", SessionID: "session.jsonl"},
			wantRun:    true,
			wantReason: "always",
		},
		{
			name:       "off mode never triggers",
			provider:   "pi",
			cfg:        Config{PiMemoryPostRun: "off"},
			result:     TaskResult{Status: "completed", Comment: longOutput, SessionID: "session.jsonl"},
			tools:      4,
			wantReason: "disabled",
		},
		{
			name:       "provider failure skips despite tools",
			provider:   "pi",
			result:     TaskResult{Status: "blocked", Comment: "rate limit reached", SessionID: "session.jsonl", FailureReason: "agent_error.provider_capacity_or_rate_limit"},
			tools:      3,
			wantReason: "provider_failure",
		},
		{
			name:       "failure fix validation sample triggers through tools gate",
			provider:   "pi",
			result:     TaskResult{Status: "blocked", Comment: "修复后 go test 验证通过", SessionID: "session.jsonl"},
			tools:      3,
			wantRun:    true,
			wantModel:  true,
			wantReason: "tools",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldRunPiMemoryPostRun(tc.task, tc.result, tc.provider, tc.tools, tc.cfg)
			if got.Run != tc.wantRun || got.IncludeModel != tc.wantModel || got.Reason != tc.wantReason {
				t.Fatalf("decision = %+v, want run=%v includeModel=%v reason=%q", got, tc.wantRun, tc.wantModel, tc.wantReason)
			}
		})
	}
}

func TestRunPiMemoryPostRunInvokesCuratorAndSync(t *testing.T) {
	tmp := t.TempDir()
	sessionFile := filepath.Join(tmp, "pi-session.jsonl")
	if err := os.WriteFile(sessionFile, []byte(`{"type":"turn_end"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var syncCalls atomic.Int32
	var gotName string
	var gotArgs []string
	var gotEnv map[string]string
	d := &Daemon{
		cfg: Config{
			ServerBaseURL:               "https://api.example.test",
			WorkspacesRoot:              tmp,
			PiMemoryPostRunTimeout:      time.Second,
			PiMemoryPostRunMinTools:     1,
			PiMemoryPostRun:             "auto",
			PiMemoryPostRunIncludeModel: "auto",
		},
		piMemoryPostRunExec: func(ctx context.Context, name string, args []string, env map[string]string) ([]byte, error) {
			gotName = name
			gotArgs = append([]string(nil), args...)
			gotEnv = cloneStringMap(env)
			return []byte(`{"status":"ok"}`), nil
		},
		piMemoryPostRunSync: func(ctx context.Context, rt Runtime) error {
			syncCalls.Add(1)
			if rt.ID != "rt-1" {
				t.Fatalf("runtime ID = %q, want rt-1", rt.ID)
			}
			return nil
		},
	}

	d.runPiMemoryPostRun(context.Background(),
		Task{ID: "task-1", WorkspaceID: "ws-1", AgentID: "agent-1"},
		TaskResult{Status: "completed", SessionID: sessionFile},
		Runtime{ID: "rt-1", Provider: "pi", WorkspaceID: "ws-1"},
		piMemoryPostRunDecision{Run: true, Reason: "tools"},
		discardLogger(),
	)

	if gotName != "jhp-pi-memory-curator" {
		t.Fatalf("command = %q", gotName)
	}
	wantArgs := []string{"session-history-backfill", sessionFile, "--memory-dir", filepath.Join(tmp, "ws-1", ".multica", "agents", "agent-1", "memory"), "--limit", "1", "--force", "--json"}
	if strings.Join(gotArgs, "\x00") != strings.Join(wantArgs, "\x00") {
		t.Fatalf("args = %#v, want %#v", gotArgs, wantArgs)
	}
	if containsArg(gotArgs, "--include-model") {
		t.Fatalf("unexpected --include-model in args: %#v", gotArgs)
	}
	for key, want := range map[string]string{
		"PI_AGENT_ROOT":           filepath.Join(tmp, "ws-1", ".multica", "agents", "agent-1"),
		"PI_MEMORY_DIR":           filepath.Join(tmp, "ws-1", ".multica", "agents", "agent-1", "memory"),
		"PI_SKILL_DRAFTS_DIR":     filepath.Join(tmp, "ws-1", ".multica", "agents", "agent-1", "skills", "drafts"),
		"PI_AGENT_SYNC_QUEUE_DIR": filepath.Join(tmp, "ws-1", ".multica", "agents", "agent-1", "sync_queue"),
		"MULTICA_SERVER_URL":      "https://api.example.test",
		"MULTICA_WORKSPACE_ID":    "ws-1",
		"MULTICA_AGENT_ID":        "agent-1",
		"MULTICA_TASK_ID":         "task-1",
		"MULTICA_RUN_ID":          "task-1",
	} {
		if gotEnv[key] != want {
			t.Fatalf("env[%s] = %q, want %q", key, gotEnv[key], want)
		}
	}
	if syncCalls.Load() != 1 {
		t.Fatalf("sync calls = %d, want 1", syncCalls.Load())
	}
}

func TestRunPiMemoryPostRunIncludesModelWhenDecisionRequestsIt(t *testing.T) {
	tmp := t.TempDir()
	sessionFile := filepath.Join(tmp, "pi-session.jsonl")
	if err := os.WriteFile(sessionFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var gotArgs []string
	d := &Daemon{
		cfg: Config{WorkspacesRoot: tmp, PiMemoryPostRunTimeout: time.Second},
		piMemoryPostRunExec: func(ctx context.Context, name string, args []string, env map[string]string) ([]byte, error) {
			gotArgs = append([]string(nil), args...)
			return nil, nil
		},
		piMemoryPostRunSync: func(ctx context.Context, rt Runtime) error { return nil },
	}

	d.runPiMemoryPostRun(context.Background(),
		Task{ID: "task-1", WorkspaceID: "ws-1", AgentID: "agent-1"},
		TaskResult{Status: "completed", SessionID: sessionFile},
		Runtime{ID: "rt-1", Provider: "pi", WorkspaceID: "ws-1"},
		piMemoryPostRunDecision{Run: true, IncludeModel: true, Reason: "long_output"},
		discardLogger(),
	)
	if !containsArg(gotArgs, "--include-model") {
		t.Fatalf("args = %#v, want --include-model", gotArgs)
	}
}

func TestRunPiMemoryPostRunSkipsMissingSession(t *testing.T) {
	var execCalls atomic.Int32
	d := &Daemon{
		cfg: Config{WorkspacesRoot: t.TempDir(), PiMemoryPostRunTimeout: time.Second},
		piMemoryPostRunExec: func(ctx context.Context, name string, args []string, env map[string]string) ([]byte, error) {
			execCalls.Add(1)
			return nil, nil
		},
		piMemoryPostRunSync: func(ctx context.Context, rt Runtime) error { return nil },
	}

	d.runPiMemoryPostRun(context.Background(),
		Task{ID: "task-1", WorkspaceID: "ws-1", AgentID: "agent-1"},
		TaskResult{Status: "completed", SessionID: filepath.Join(t.TempDir(), "missing.jsonl")},
		Runtime{ID: "rt-1", Provider: "pi", WorkspaceID: "ws-1"},
		piMemoryPostRunDecision{Run: true, Reason: "tools"},
		discardLogger(),
	)
	if execCalls.Load() != 0 {
		t.Fatalf("exec calls = %d, want 0", execCalls.Load())
	}
}

func TestRunPiMemoryPostRunDoesNotSyncOnCuratorFailure(t *testing.T) {
	tmp := t.TempDir()
	sessionFile := filepath.Join(tmp, "pi-session.jsonl")
	if err := os.WriteFile(sessionFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var syncCalls atomic.Int32
	d := &Daemon{
		cfg: Config{WorkspacesRoot: tmp, PiMemoryPostRunTimeout: time.Second},
		piMemoryPostRunExec: func(ctx context.Context, name string, args []string, env map[string]string) ([]byte, error) {
			return []byte("boom"), errors.New("curator failed")
		},
		piMemoryPostRunSync: func(ctx context.Context, rt Runtime) error {
			syncCalls.Add(1)
			return nil
		},
	}

	d.runPiMemoryPostRun(context.Background(),
		Task{ID: "task-1", WorkspaceID: "ws-1", AgentID: "agent-1"},
		TaskResult{Status: "completed", SessionID: sessionFile},
		Runtime{ID: "rt-1", Provider: "pi", WorkspaceID: "ws-1"},
		piMemoryPostRunDecision{Run: true, Reason: "tools"},
		discardLogger(),
	)
	if syncCalls.Load() != 0 {
		t.Fatalf("sync calls = %d, want 0", syncCalls.Load())
	}
}

func containsArg(args []string, arg string) bool {
	for _, a := range args {
		if a == arg {
			return true
		}
	}
	return false
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
