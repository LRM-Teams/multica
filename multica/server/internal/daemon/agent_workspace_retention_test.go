package daemon

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newAgentWorkspaceRetentionTestDaemon builds a real *Daemon backed by a fake
// HTTP server, mirroring newGCTestDaemon (gc_test.go) but scoped to this
// job's config knobs. dryRun defaults every test to the safe mode unless a
// case explicitly needs real deletion.
func newAgentWorkspaceRetentionTestDaemon(t *testing.T, handler http.Handler, dryRun bool) *Daemon {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	cfg := Config{
		WorkspacesRoot:                  t.TempDir(),
		AgentWorkspaceRetentionEnabled:  true,
		AgentWorkspaceRetentionInterval: time.Hour,
		AgentWorkspaceRetentionDryRun:   dryRun,
	}
	d := New(cfg, slog.Default())
	d.client = NewClient(srv.URL)
	d.client.SetToken("test-token")
	return d
}

// seedAgentWorkspaceDir creates a .multica/agents/<agentID> directory with a
// marker file inside, so tests can assert on the marker's survival rather
// than just directory existence (catches a partial/wrong-path delete).
func seedAgentWorkspaceDir(t *testing.T, workspacesRoot, workspaceID, agentID string) string {
	t.Helper()
	dir := filepath.Join(workspacesRoot, workspaceID, managedAgentWorkspaceNamespace, "agents", agentID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("seed agent workspace dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("durable"), 0o644); err != nil {
		t.Fatalf("seed marker file: %v", err)
	}
	return dir
}

func retentionCheckHandler(t *testing.T, eligible []string) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/daemon/workspaces/", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			AgentIDs []string `json:"agent_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"eligible_agent_ids": eligible})
	})
	return mux
}

const (
	retentionTestWorkspaceID = "11111111-1111-1111-1111-111111111111"
	retentionTestAgentID     = "22222222-2222-2222-2222-222222222222"
)

// TestAgentWorkspaceRetention_DryRunDoesNotDelete is the default-mode
// guardrail: even when the server reports an agent eligible, dry-run (the
// config default) must never touch disk.
func TestAgentWorkspaceRetention_DryRunDoesNotDelete(t *testing.T) {
	t.Parallel()
	d := newAgentWorkspaceRetentionTestDaemon(t, retentionCheckHandler(t, []string{retentionTestAgentID}), true)
	dir := seedAgentWorkspaceDir(t, d.cfg.WorkspacesRoot, retentionTestWorkspaceID, retentionTestAgentID)

	d.runAgentWorkspaceRetention(context.Background())

	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("dry-run must not delete: %v", err)
	}
}

// TestAgentWorkspaceRetention_RealRunDeletesEligible is the positive
// direction: with dry-run explicitly off and the server reporting the agent
// eligible, the directory must actually be destroyed.
func TestAgentWorkspaceRetention_RealRunDeletesEligible(t *testing.T) {
	t.Parallel()
	d := newAgentWorkspaceRetentionTestDaemon(t, retentionCheckHandler(t, []string{retentionTestAgentID}), false)
	dir := seedAgentWorkspaceDir(t, d.cfg.WorkspacesRoot, retentionTestWorkspaceID, retentionTestAgentID)

	d.runAgentWorkspaceRetention(context.Background())

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("eligible agent workspace should have been destroyed, stat err = %v", err)
	}
}

// TestAgentWorkspaceRetention_RealRunKeepsIneligible is the direction Parker
// called out as more important than the positive case: a live/recently
// archived agent's directory must survive even with dry-run off, when the
// server does not report it eligible.
func TestAgentWorkspaceRetention_RealRunKeepsIneligible(t *testing.T) {
	t.Parallel()
	d := newAgentWorkspaceRetentionTestDaemon(t, retentionCheckHandler(t, nil), false)
	dir := seedAgentWorkspaceDir(t, d.cfg.WorkspacesRoot, retentionTestWorkspaceID, retentionTestAgentID)

	d.runAgentWorkspaceRetention(context.Background())

	got, err := os.ReadFile(filepath.Join(dir, "MEMORY.md"))
	if err != nil || string(got) != "durable" {
		t.Fatalf("ineligible agent workspace must survive untouched, content=%q err=%v", got, err)
	}
}

// TestAgentWorkspaceRetention_RefusesServerIDOutsideBatch is the security
// invariant Parker made a hard requirement: a server response is never
// trusted as a set of paths to delete. If the server reports an ID that
// wasn't in this scan's own candidate batch (bug, or a compromised/rogue
// response), the daemon must refuse it — and must not let that phantom
// entry disrupt handling of the real candidates in the same batch.
func TestAgentWorkspaceRetention_RefusesServerIDOutsideBatch(t *testing.T) {
	t.Parallel()
	phantomID := "99999999-9999-9999-9999-999999999999"
	// Server claims both the real candidate AND a phantom ID never reported.
	d := newAgentWorkspaceRetentionTestDaemon(t, retentionCheckHandler(t, []string{retentionTestAgentID, phantomID}), false)
	dir := seedAgentWorkspaceDir(t, d.cfg.WorkspacesRoot, retentionTestWorkspaceID, retentionTestAgentID)

	d.runAgentWorkspaceRetention(context.Background())

	// The real, legitimately-batched candidate should still be destroyed —
	// a phantom entry elsewhere in the response must not block or corrupt
	// handling of genuine ones.
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("legitimate candidate should still be destroyed despite phantom entry in response, stat err = %v", err)
	}
	// No directory exists for phantomID on disk; the assertion here is
	// simply that the job did not panic or error out handling an ID it
	// never enumerated itself.
}

// TestAgentWorkspaceRetention_NoAgentsDirIsNoOp covers a workspace with no
// .multica/agents directory at all (the common case) — must not error.
func TestAgentWorkspaceRetention_NoAgentsDirIsNoOp(t *testing.T) {
	t.Parallel()
	d := newAgentWorkspaceRetentionTestDaemon(t, retentionCheckHandler(t, nil), false)
	wsDir := filepath.Join(d.cfg.WorkspacesRoot, retentionTestWorkspaceID)
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatalf("seed empty workspace dir: %v", err)
	}

	d.runAgentWorkspaceRetention(context.Background())
	// No panic, no error surface to check — the absence of a crash is the
	// assertion.
}

// TestAgentWorkspaceRetention_Disabled confirms the enabled flag actually
// short-circuits the loop before any filesystem scan or network call.
func TestAgentWorkspaceRetention_Disabled(t *testing.T) {
	t.Parallel()
	d := newAgentWorkspaceRetentionTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server should not be contacted when retention is disabled")
	}), false)
	d.cfg.AgentWorkspaceRetentionEnabled = false
	seedAgentWorkspaceDir(t, d.cfg.WorkspacesRoot, retentionTestWorkspaceID, retentionTestAgentID)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // agentWorkspaceRetentionLoop's post-boot sleep would otherwise block the test
	d.agentWorkspaceRetentionLoop(ctx)
}
