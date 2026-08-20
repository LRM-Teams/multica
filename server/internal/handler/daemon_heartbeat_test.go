package handler

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/daemonws"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// createTestAgentRuntimeWithDaemonID inserts a minimal agent_runtime row
// bound to daemonID and returns it fully loaded (so callers get a real
// db.AgentRuntime, not just an ID, for functions like recordHeartbeat that
// take the whole struct).
func createTestAgentRuntimeWithDaemonID(t *testing.T, daemonID string) db.AgentRuntime {
	t.Helper()
	ctx := context.Background()
	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, visibility, last_seen_at)
		VALUES (
			$1,  $2,  $3,  'local',  'daemon-heartbeat-test',  'online', 
			'',  '{}'::jsonb,  'private',  now()
		)
		RETURNING id
	`,  testWorkspaceID,  daemonID,  "heartbeat-runtime-"+randomID()).Scan(&runtimeID); err != nil {
		t.Fatalf("create test agent_runtime: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
		testPool.Exec(context.Background(), `DELETE FROM daemon_heartbeat WHERE daemon_id = $1`, daemonID)
	})
	rt, err := testHandler.Queries.GetAgentRuntime(ctx, parseUUID(runtimeID))
	if err != nil {
		t.Fatalf("reload test agent_runtime: %v", err)
	}
	return rt
}

func TestComputerConnectedByRunner_SocketIsAuthoritativeWhenHubAvailable(t *testing.T) {
	h := &Handler{DaemonHub: daemonws.NewHub()}
	now := time.Now()
	hb := &db.DaemonHeartbeat{LastSeenAt: pgtimestamptz(now.Add(-10 * time.Second))}
	if h.computerConnectedByRunner("daemon-1", "workspace-1", hb, now) {
		t.Fatal("fresh heartbeat must not mark a Computer online without a current Runner socket")
	}
}

func TestComputerConnectedByRunner_FallsBackToHeartbeatWhenHubUnavailable(t *testing.T) {
	h := &Handler{}
	now := time.Now()
	fresh := &db.DaemonHeartbeat{LastSeenAt: pgtimestamptz(now.Add(-10 * time.Second))}
	if !h.computerConnectedByRunner("daemon-1", "workspace-1", fresh, now) {
		t.Fatal("legacy composition without Hub must use heartbeat freshness")
	}
	stale := &db.DaemonHeartbeat{LastSeenAt: pgtimestamptz(now.Add(-10 * time.Minute))}
	if h.computerConnectedByRunner("daemon-1", "workspace-1", stale, now) {
		t.Fatal("legacy composition without Hub must treat a stale heartbeat as offline")
	}
}

func TestComputerConnected_NilHeartbeatIsDisconnected(t *testing.T) {
	if computerConnected(nil, time.Now()) {
		t.Fatal("computerConnected(nil) = true, want false — a daemon that has never sent a heartbeat cannot be connected")
	}
}

func TestComputerConnected_FreshHeartbeatIsConnected(t *testing.T) {
	now := time.Now()
	hb := &db.DaemonHeartbeat{LastSeenAt: pgtimestamptz(now.Add(-10 * time.Second))}
	if !computerConnected(hb, now) {
		t.Fatal("computerConnected = false for a 10s-old heartbeat, want true")
	}
}

func TestComputerConnected_StaleHeartbeatIsDisconnected(t *testing.T) {
	now := time.Now()
	hb := &db.DaemonHeartbeat{LastSeenAt: pgtimestamptz(now.Add(-10 * time.Minute))}
	if computerConnected(hb, now) {
		t.Fatal("computerConnected = true for a 10m-old heartbeat, want false")
	}
}

// TestComputerConnected_IndependentOfRuntimeHealth pins task #58's actual
// acceptance criterion: a fresh daemon heartbeat and stale runtime heartbeats
// coexist without contradiction — "computer connected" must come out true
// while the runtime/agent's own display status (a completely separate,
// already-correct computation from #1664) comes out disconnected. Before
// #58, there was no daemon-level signal at all, so this scenario could only
// be answered by aggregating the runtime rows — which is exactly the s144
// "Active now / Offline" bug this task fixes.
func TestComputerConnected_IndependentOfRuntimeHealth(t *testing.T) {
	now := time.Now()

	daemonHeartbeat := &db.DaemonHeartbeat{LastSeenAt: pgtimestamptz(now.Add(-5 * time.Second))}
	// Same fixture shape as TestAgentRuntimeDisplayStatus_StaleOnlineRuntimeShowsOfflineNotComputerDisconnected:
	// status still says "online" (the lying field #1687 stopped trusting)
	// but the heartbeat itself is stale (> 150s) and within the reconnect
	// window (< 5m). That connectivity detail still resolves to Agent Offline.
	staleRuntime := db.AgentRuntime{
		Status:     "online",
		LastSeenAt: pgtimestamptz(now.Add(-3 * time.Minute)),
		UpdatedAt:  pgtimestamptz(now.Add(-2 * time.Minute)),
	}

	if !computerConnected(daemonHeartbeat, now) {
		t.Fatal("computer must show connected: the daemon itself just heartbeat, independent of any runtime")
	}
	if got := agentRuntimeDisplayStatus("idle", staleRuntime, pgtype.Timestamptz{}, "", pgtype.Timestamptz{}, now); got != agentDisplayStatusOffline {
		t.Fatalf("agent display status = %q, want %q — Computer disconnect makes the Agent offline", got, agentDisplayStatusOffline)
	}
}

func TestRecordDaemonHeartbeat_UpsertsAndUpdatesLastSeenAt(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	workspaceID := parseUUID(testWorkspaceID)
	daemonID := "test-daemon-" + t.Name()

	if err := testHandler.Queries.RecordDaemonHeartbeat(ctx, db.RecordDaemonHeartbeatParams{
		WorkspaceID: workspaceID,
		DaemonID:    daemonID,
	}); err != nil {
		t.Fatalf("first RecordDaemonHeartbeat: %v", err)
	}
	first, err := testHandler.Queries.GetDaemonHeartbeat(ctx, db.GetDaemonHeartbeatParams{
		WorkspaceID: workspaceID,
		DaemonID:    daemonID,
	})
	if err != nil {
		t.Fatalf("GetDaemonHeartbeat after first record: %v", err)
	}

	time.Sleep(10 * time.Millisecond)
	if err := testHandler.Queries.RecordDaemonHeartbeat(ctx, db.RecordDaemonHeartbeatParams{
		WorkspaceID: workspaceID,
		DaemonID:    daemonID,
	}); err != nil {
		t.Fatalf("second RecordDaemonHeartbeat: %v", err)
	}
	second, err := testHandler.Queries.GetDaemonHeartbeat(ctx, db.GetDaemonHeartbeatParams{
		WorkspaceID: workspaceID,
		DaemonID:    daemonID,
	})
	if err != nil {
		t.Fatalf("GetDaemonHeartbeat after second record: %v", err)
	}

	if !second.LastSeenAt.Time.After(first.LastSeenAt.Time) {
		t.Fatalf("second heartbeat LastSeenAt (%v) is not after first (%v) — the upsert did not bump last_seen_at", second.LastSeenAt.Time, first.LastSeenAt.Time)
	}
}

// TestRecordHeartbeat_WritesDaemonHeartbeatEvenWhenRuntimeRowSkipsDBWrite
// mutation check: gate the daemon_heartbeat write behind recordHeartbeat's
// pre-existing needDBWrite optimization (which is allowed to skip the
// per-runtime DB write on a hot heartbeat) → this test fails because the
// daemon-level row is never created on the first call. The daemon heartbeat
// must not inherit that runtime-specific debounce.
func TestRecordHeartbeat_WritesDaemonHeartbeatOnFirstCall(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	rt := createTestAgentRuntimeWithDaemonID(t, "test-daemon-"+t.Name())

	if err := testHandler.recordHeartbeat(ctx, rt); err != nil {
		t.Fatalf("recordHeartbeat: %v", err)
	}

	hb, err := testHandler.Queries.GetDaemonHeartbeat(ctx, db.GetDaemonHeartbeatParams{
		WorkspaceID: rt.WorkspaceID,
		DaemonID:    rt.DaemonID.String,
	})
	if err != nil {
		t.Fatalf("expected a daemon_heartbeat row after the first recordHeartbeat call, got error: %v", err)
	}
	if time.Since(hb.LastSeenAt.Time) > 10*time.Second {
		t.Fatalf("daemon_heartbeat.last_seen_at = %v, expected it to be freshly written", hb.LastSeenAt.Time)
	}
}

func TestWorkspaceRunnerHeartbeatRejectsRuntimeAssignedToAnotherComputer(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	rt := createTestAgentRuntimeWithDaemonID(t, "computer-owner-"+randomID())
	for _, daemonID := range []string{"", "computer-other-" + randomID()} {
		_, err := testHandler.HandleDaemonWSHeartbeat(context.Background(), daemonws.ClientIdentity{
			DaemonID:    daemonID,
			WorkspaceID: testWorkspaceID,
		}, protocol.DaemonHeartbeatRequestPayload{RuntimeID: uuidToString(rt.ID)})
		if err == nil || !strings.Contains(err.Error(), "runtime not assigned to connection Computer") {
			t.Fatalf("Workspace Runner daemon_id %q error = %v", daemonID, err)
		}
	}
}
