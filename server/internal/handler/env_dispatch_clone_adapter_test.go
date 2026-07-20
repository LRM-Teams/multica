package handler

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
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
