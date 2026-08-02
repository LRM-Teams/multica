package handler

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestUpsertAgentRuntime_ClearsStaleOfflineReason is the regression guard
// for the bug found while writing the agent intentional-stop signal design
// doc (docs/superpowers/specs/2026-08-02-agent-intentional-stop-signal-design.md,
// Open Question 1): UpsertAgentRuntime's DO UPDATE previously never touched
// offline_reason, so a runtime that was gracefully stopped (offline_reason
// set) and later reconnected would carry the stale reason forever — making
// a FUTURE real (silence-based) disconnect incorrectly still read as
// "stopped" instead of "disconnected". This test seeds a stale reason from
// a prior deregister, then calls UpsertAgentRuntime the way a daemon
// register/reconnect does, and asserts the reason is cleared.
func TestUpsertAgentRuntime_ClearsStaleOfflineReason(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	workspaceID := parseUUID(testWorkspaceID)
	daemonID := "offline-reason-daemon-" + randomID()
	provider := "cursor"

	// Seed a row that looks like it was gracefully deregistered in the past:
	// status=offline, offline_reason set.
	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider,
			status, device_info, metadata, offline_reason, visibility
		)
		VALUES ($1, $2, $3, 'cloud', $4, 'offline', '', '{}'::jsonb, 'daemon_deregistered', 'private')
		RETURNING id
	`, testWorkspaceID, daemonID, "offline-reason-runtime-"+randomID(), provider).Scan(&runtimeID); err != nil {
		t.Fatalf("seed stale-offline-reason runtime: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})

	// Reconnect: same (workspace_id, daemon_id, provider) triggers the
	// ON CONFLICT DO UPDATE path, exactly like a real daemon register.
	if _, err := testHandler.Queries.UpsertAgentRuntime(ctx, db.UpsertAgentRuntimeParams{
		WorkspaceID: workspaceID,
		DaemonID:    pgtype.Text{String: daemonID, Valid: true},
		Name:        "offline-reason-runtime-reconnected",
		RuntimeMode: "cloud",
		Provider:    provider,
		Status:      "online",
		DeviceInfo:  "",
		Metadata:    []byte("{}"),
		OwnerID:     pgtype.UUID{},
	}); err != nil {
		t.Fatalf("UpsertAgentRuntime: %v", err)
	}

	var offlineReason pgtype.Text
	if err := testPool.QueryRow(ctx, `
		SELECT offline_reason FROM agent_runtime WHERE id = $1
	`, runtimeID).Scan(&offlineReason); err != nil {
		t.Fatalf("load reconnected runtime: %v", err)
	}
	if offlineReason.Valid {
		t.Fatalf("offline_reason = %q, want NULL after reconnect (stale reason must not survive a reconnect — a future real disconnect must not be mislabeled as an intentional stop)", offlineReason.String)
	}
}

// TestDaemonDeregister_SetsOfflineReason is the write-side check for task ①
// (agent intentional-stop signal): a graceful daemon shutdown (Ctrl-C,
// `multica daemon stop`, daemon restart) calls /api/daemon/deregister, and
// the runtime it marks offline must carry a known offline_reason so the
// read side (RuntimeConnectivity / agentRuntimeDisplayStatus) can report
// "stopped" instead of riding the silence-based Stale→Dead ramp.
func TestDaemonDeregister_SetsOfflineReason(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := createAgentHealthFixture(t, "online", time.Now(), time.Now())
	_ = agentID

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/daemon/deregister", map[string]any{
		"runtime_ids": []string{runtimeID},
	})
	testHandler.DaemonDeregister(w, req)
	if w.Code != 200 {
		t.Fatalf("DaemonDeregister: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var status string
	var offlineReason pgtype.Text
	if err := testPool.QueryRow(context.Background(), `
		SELECT status, offline_reason FROM agent_runtime WHERE id = $1
	`, runtimeID).Scan(&status, &offlineReason); err != nil {
		t.Fatalf("load deregistered runtime: %v", err)
	}
	if status != "offline" {
		t.Fatalf("status = %q, want offline", status)
	}
	if !offlineReason.Valid || offlineReason.String != "daemon_deregistered" {
		t.Fatalf("offline_reason = %+v, want valid %q", offlineReason, "daemon_deregistered")
	}
}

// TestEphemeralSandboxManagerCleanup_SetsOfflineReason is the sandbox-teardown
// counterpart write-side check: SetAgentRuntimeOffline's ephemeral-sandbox
// caller is the same "server knows this runtime is done" family as
// DaemonDeregister, so it gets its own confirmed reason_code too.
func TestEphemeralSandboxManagerCleanup_SetsOfflineReason(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID, _ := setupBoundRuntimeAgent(t, "pi")
	runtimeID, _, err := (&envDispatchDepsAdapter{h: testHandler}).PrecreateAgentRuntime(ctx, testWorkspaceID, testUserID, agentID)
	if err != nil {
		t.Fatalf("precreate runtime: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_runtime WHERE id = $1`, runtimeID) })
	lifecycle := &fakeRetrySandboxLifecycle{
		oldRef: service.SandboxInstanceRef{InstanceID: "old", WorkspaceID: testWorkspaceID, CreatorUserID: testUserID},
	}
	manager := newEphemeralSandboxManager(testHandler, lifecycle)
	contextJSON := mergeEphemeralSandboxContext(nil, "old", testUserID)
	if err := manager.Cleanup(ctx, db.AgentInboxEvent{
		ID:        util.MustParseUUID("aaaaaaaa-0000-0000-0000-000000000002"),
		AgentID:   parseUUID(agentID),
		RuntimeID: parseUUID(runtimeID),
		Context:   contextJSON,
	}); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	var offlineReason pgtype.Text
	if err := testPool.QueryRow(ctx, `
		SELECT offline_reason FROM agent_runtime WHERE id = $1
	`, runtimeID).Scan(&offlineReason); err != nil {
		t.Fatalf("load torn-down runtime: %v", err)
	}
	if !offlineReason.Valid || offlineReason.String != "sandbox_teardown" {
		t.Fatalf("offline_reason = %+v, want valid %q", offlineReason, "sandbox_teardown")
	}
}

// TestAttachAgentRuntimeNames_StoppedRuntimeShowsStopped is the wiring check
// for the primary agent-list/detail endpoint (GET /agents, GetAgent), which
// goes through attachAgentRuntimeNames's own hand-rolled raw-SQL query
// rather than a sqlc-generated `SELECT *`. That query originally omitted
// offline_reason, so a confirmed-stopped runtime would silently fall back to
// "offline" on the one surface most users actually look at, even though the
// pure agentRuntimeDisplayStatus function and the Activity Health tab both
// already handled it correctly (found during adversarial review, task ①).
func TestAttachAgentRuntimeNames_StoppedRuntimeShowsStopped(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := createAgentHealthFixture(t, "offline", time.Now(), time.Now())
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_runtime SET offline_reason = 'daemon_deregistered' WHERE id = $1
	`, runtimeID); err != nil {
		t.Fatalf("seed offline_reason: %v", err)
	}

	resps := []AgentResponse{{ID: agentID, RuntimeID: runtimeID, Status: "idle"}}
	testHandler.attachAgentRuntimeNames(context.Background(), resps)

	if resps[0].RuntimeDisplayStatus != agentDisplayStatusStopped {
		t.Fatalf("RuntimeDisplayStatus = %q, want %q — the agent-list endpoint must surface a confirmed intentional stop, not fall back to generic offline",
			resps[0].RuntimeDisplayStatus, agentDisplayStatusStopped)
	}
}
