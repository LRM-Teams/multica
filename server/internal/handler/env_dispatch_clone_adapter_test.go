package handler

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/service"
)

// TestEnvDispatchCloneAdapterLoadSourceAgentNotFound verifies the adapter
// translates a missing source agent row into a clear "not found" error (so the
// orchestration layer can compensate rather than treat it as a transient
// retry). Uses the in-memory fake DBTX from env_dispatch_runtime_lookup_test.go
// (same package); happy-path SQL is DB-backed and runs in CI.
func TestEnvDispatchCloneAdapterLoadSourceAgentNotFound(t *testing.T) {
	a := &envDispatchCloneAdapter{h: &Handler{}, tx: &fakeRuntimeLookupDBTX{row: &fakeRuntimeRow{err: pgx.ErrNoRows}}}
	_, err := a.LoadSourceAgent(context.Background(), "ws-1", "src-missing")
	if err == nil || !strings.Contains(err.Error(), "source agent not found") {
		t.Fatalf("want source-not-found error, got %v", err)
	}
}

// TestEnvDispatchCloneAdapterSetBindingDerivedAgentMissingBinding verifies a
// zero-rows update (binding already gone) surfaces as an error so the caller
// does not silently believe the binding was patched.
func TestEnvDispatchCloneAdapterSetBindingDerivedAgentMissingBinding(t *testing.T) {
	// fakeRuntimeLookupDBTX.Exec returns pgconn.CommandTag{} (RowsAffected=0).
	a := &envDispatchCloneAdapter{h: &Handler{}, tx: &fakeRuntimeLookupDBTX{}}
	err := a.SetBindingDerivedAgent(context.Background(), "bind-missing", "derived-1")
	if err == nil || !strings.Contains(err.Error(), "binding not found") {
		t.Fatalf("want binding-not-found error, got %v", err)
	}
}

func TestEnvDispatchCloneAdapterCreateDerivedAgentUsesUniqueDerivedName(t *testing.T) {
	fake := &fakeRuntimeLookupDBTX{row: &fakeRuntimeRow{values: []string{"derived-1"}}}
	a := &envDispatchCloneAdapter{h: &Handler{}, tx: fake}

	derivedID, err := a.CreateDerivedAgent(context.Background(), service.CreateDerivedAgentInput{
		WorkspaceID: "ws-1", SourceAgentID: "src-1", RuntimeID: "rt-1", Name: "env-bind-1",
	})
	if err != nil {
		t.Fatalf("create derived agent: %v", err)
	}
	if derivedID != "derived-1" {
		t.Fatalf("derivedID = %q, want derived-1", derivedID)
	}
	if len(fake.queryRowArgs) != 5 || fake.queryRowArgs[2] != "env-bind-1" {
		t.Fatalf("derived insert args = %#v, want unique name as third argument", fake.queryRowArgs)
	}
	if !strings.Contains(fake.queryRowSQL, "ON CONFLICT (workspace_id, name)") ||
		!strings.Contains(fake.queryRowSQL, "agent.source_agent_id = EXCLUDED.source_agent_id") {
		t.Fatalf("derived insert must reuse only the same lineage on retry: %s", fake.queryRowSQL)
	}
}

func TestEnvDispatchBindingExecutionAgentIDPrefersDerivedAgent(t *testing.T) {
	derivedID := "derived-1"
	binding := envAgentSandboxBinding{SourceAgentID: "source-1", DerivedAgentID: &derivedID}
	if got := envDispatchBindingExecutionAgentID(binding); got != derivedID {
		t.Fatalf("execution agent = %q, want %q", got, derivedID)
	}
	binding.DerivedAgentID = nil
	if got := envDispatchBindingExecutionAgentID(binding); got != "source-1" {
		t.Fatalf("legacy execution agent = %q, want source-1", got)
	}
}

func TestCleanupFailedEnvDispatchDerivedAgentDetachesBeforeDelete(t *testing.T) {
	fake := &fakeRuntimeLookupDBTX{}
	h := &Handler{DB: fake}
	if err := h.cleanupFailedEnvDispatchDerivedAgent(context.Background(), "binding-1", "source-1", "derived-1"); err != nil {
		t.Fatalf("cleanup derived agent: %v", err)
	}
	if !strings.Contains(fake.execSQL, "UPDATE environment_agent_sandbox") ||
		!strings.Contains(fake.execSQL, "DELETE FROM agent") ||
		!strings.Contains(fake.execSQL, "EXISTS (SELECT 1 FROM cleared)") {
		t.Fatalf("cleanup must atomically detach and delete the derived agent: %s", fake.execSQL)
	}
	if len(fake.execArgs) != 3 || fake.execArgs[0] != "binding-1" || fake.execArgs[1] != "derived-1" || fake.execArgs[2] != "source-1" {
		t.Fatalf("cleanup args = %#v", fake.execArgs)
	}
}

// TestEnvDispatchCloneAdapterExecFallsBackToHandlerDB verifies that without a
// transaction the adapter uses h.DB (the field is non-nil only under
// CloneEnvDispatchAgentTx).
func TestEnvDispatchCloneAdapterExecFallsBackToHandlerDB(t *testing.T) {
	fake := &fakeRuntimeLookupDBTX{row: &fakeRuntimeRow{err: pgx.ErrNoRows}}
	a := &envDispatchCloneAdapter{h: &Handler{DB: fake}}
	// tx is nil, so exec() returns h.DB (the fake); LoadSourceAgent surfaces the
	// fake's ErrNoRows as not-found, proving h.DB is the fallback.
	_, err := a.LoadSourceAgent(context.Background(), "ws-1", "src-1")
	if err == nil || !strings.Contains(err.Error(), "source agent not found") {
		t.Fatalf("want not-found via h.DB fallback, got %v", err)
	}
}
