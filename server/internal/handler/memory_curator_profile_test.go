package handler

import (
	"context"
	"testing"
	"time"
)

// Task #53: memoryCuratorRunStatus trusted agent_runtime.status directly,
// which can read "online" for up to ~180s after the runtime actually went
// silent (sweeper lag). This status is written straight into the API
// response the user sees right after starting a curation run
// (StartMemoryCurationRun), so a lying "queued" here directly misleads the
// user about whether the run will actually dispatch.
func TestMemoryCuratorRunStatus_StaleHeartbeatIsWaitingRuntime(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()

	var staleRuntimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, visibility, last_seen_at, updated_at) VALUES ($1,  'memory-curator-run-status-stale-daemon',  'Memory Curator Run Status Stale Runtime',  'local',  'codex',  'online', 
		          '',  '{}'::jsonb,  'private',  now() - interval '10 minutes',  now() - interval '9 minutes')
		RETURNING id::text
	`,  testWorkspaceID).Scan(&staleRuntimeID); err != nil {
		t.Fatal(err)
	}
	var curatorAgentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, display_name, runtime_mode, runtime_id, owner_id, model)
		VALUES ($1, 'memory-curator-run-status-curator', 'Memory Curator Run Status Curator', 'local', $2, $3, 'composer-1.5')
		RETURNING id::text
	`, testWorkspaceID, staleRuntimeID, testUserID).Scan(&curatorAgentID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, curatorAgentID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, staleRuntimeID)
	})

	profile := memoryCuratorProfileResponse{
		WorkspaceID:    testWorkspaceID,
		UserID:         testUserID,
		RuntimeID:      staleRuntimeID,
		CuratorAgentID: curatorAgentID,
	}
	status, err := testHandler.memoryCuratorRunStatus(ctx, profile)
	if err != nil {
		t.Fatalf("memoryCuratorRunStatus: %v", err)
	}
	if status != "waiting_runtime" {
		t.Fatalf("stale-heartbeat runtime (status column still 'online'): run status = %q, want %q (must key off heartbeat freshness, not the raw status column)", status, "waiting_runtime")
	}
}

// Task #53: resolveActiveMemoryCurationTargetAgentIDs joined on
// rt.status = 'online' to decide which agents are eligible curation
// targets "right now". A stale-heartbeat runtime whose status column still
// says "online" would falsely make its agent selectable, only for the
// resulting run to immediately skip once queued.
func TestResolveActiveMemoryCurationTargetAgentIDs_StaleHeartbeatExcluded(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()

	var staleRuntimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, visibility, last_seen_at, updated_at) VALUES ($1,  'memory-curator-active-targets-stale-daemon',  'Memory Curator Active Targets Stale Runtime',  'local',  'codex',  'online', 
		          '',  '{}'::jsonb,  'private',  now() - interval '10 minutes',  now() - interval '9 minutes')
		RETURNING id::text
	`,  testWorkspaceID).Scan(&staleRuntimeID); err != nil {
		t.Fatal(err)
	}
	var targetAgentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, display_name, runtime_mode, runtime_id, owner_id, model)
		VALUES ($1, 'memory-curator-active-targets-agent', 'Memory Curator Active Targets Agent', 'local', $2, $3, 'composer-1.5')
		RETURNING id::text
	`, testWorkspaceID, staleRuntimeID, testUserID).Scan(&targetAgentID); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	var issueID string
	if err := testPool.QueryRow(ctx, `
		WITH bumped AS (
			UPDATE workspace SET issue_counter = issue_counter + 1
			WHERE id = $1 RETURNING issue_counter
		)
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, number)
		SELECT $1, 'memory curator active targets stale heartbeat', 'todo', 'none', 'member', $2, bumped.issue_counter
		FROM bumped
		RETURNING id::text
	`, testWorkspaceID, testUserID).Scan(&issueID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_inbox_event (agent_id, issue_id, runtime_id, status, created_at, completed_at)
		VALUES ($1, $2, $3, 'acked', $4, $4)
	`, targetAgentID, issueID, staleRuntimeID, day.Add(12*time.Hour)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE issue_id = $1`, issueID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, targetAgentID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, staleRuntimeID)
	})

	profile := memoryCuratorProfileResponse{
		WorkspaceID: testWorkspaceID,
		UserID:      testUserID,
		TargetScope: "owned_all",
	}
	ids, err := testHandler.resolveActiveMemoryCurationTargetAgentIDs(ctx, profile, day)
	if err != nil {
		t.Fatalf("resolveActiveMemoryCurationTargetAgentIDs: %v", err)
	}
	for _, id := range ids {
		if id == targetAgentID {
			t.Fatalf("stale-heartbeat runtime (status column still 'online'): agent %s returned as active target, want excluded (must key off heartbeat freshness, not the raw status column)", targetAgentID)
		}
	}
}
