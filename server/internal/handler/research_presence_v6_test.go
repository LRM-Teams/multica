package handler

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestBuildResearchV6PresenceRoster(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	directorAgentID := createHandlerTestAgent(t, "v6-presence-director-"+uuid.NewString()[:8], nil)
	workerAgentID := createHandlerTestAgent(t, "v6-presence-worker-"+uuid.NewString()[:8], nil)
	staleAgentID := createHandlerTestAgent(t, "v6-presence-stale-"+uuid.NewString()[:8], nil)

	// Session insert and passport registration must commit together: the
	// session passport guard is a deferred constraint checked at COMMIT.
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin session fixture transaction: %v", err)
	}
	defer tx.Rollback(ctx)
	var sessionID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO research_session (
			workspace_id, created_by, title, goal, status, orchestrator_version
		) VALUES ($1::uuid, $2::uuid, 'V6 presence fixture', 'goal', 'running', 'research-run-v6')
		RETURNING id::text
	`, testWorkspaceID, testUserID).Scan(&sessionID); err != nil {
		t.Fatalf("create research session: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT research_ensure_run_session_passport($1::uuid, $2::uuid)`, testWorkspaceID, sessionID); err != nil {
		t.Fatalf("ensure session passport: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit session fixture: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM research_session WHERE id=$1::uuid`, sessionID)
	})

	for _, agentID := range []string{directorAgentID, workerAgentID, staleAgentID} {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO research_team_membership (
				workspace_id, session_id, agent_id, membership_generation,
				mission_prompt, mission_hash, mission_revision, state
			) VALUES ($1::uuid, $2::uuid, $3::uuid, 1, 'mission',
				'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 1, 'working')
		`, testWorkspaceID, sessionID, agentID); err != nil {
			t.Fatalf("create membership: %v", err)
		}
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO research_director_assignment (
			workspace_id, session_id, director_agent_id, generation, status, assigned_by_user_id, reason
		) VALUES ($1::uuid, $2::uuid, $3::uuid, 1, 'active', $4::uuid, 'presence fixture')
	`, testWorkspaceID, sessionID, directorAgentID, testUserID); err != nil {
		t.Fatalf("create director assignment: %v", err)
	}

	// Worker: running Work Item with a live lease + a progress-note caption.
	if _, err := testPool.Exec(ctx, `
		INSERT INTO research_work_item (
			workspace_id, session_id, kind, status, assigned_agent_id, goal_version,
			idempotency_key, lease_token, lease_expires_at, payload_schema_id, state_version, started_at
		) VALUES ($1::uuid, $2::uuid, 'research', 'running', $3::uuid, 1,
			'presence-worker', $4::uuid, now() + interval '10 minutes', 'schema', 1, now())
	`, testWorkspaceID, sessionID, workerAgentID, uuid.NewString()); err != nil {
		t.Fatalf("create running work item: %v", err)
	}
	// Stale agent: running Work Item whose lease already expired.
	if _, err := testPool.Exec(ctx, `
		INSERT INTO research_work_item (
			workspace_id, session_id, kind, status, assigned_agent_id, goal_version,
			idempotency_key, lease_token, lease_expires_at, payload_schema_id, state_version, started_at
		) VALUES ($1::uuid, $2::uuid, 'research', 'running', $3::uuid, 1,
			'presence-stale', $4::uuid, now() - interval '1 minute', 'schema', 1, now())
	`, testWorkspaceID, sessionID, staleAgentID, uuid.NewString()); err != nil {
		t.Fatalf("create expired work item: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO research_run_event (
			workspace_id, session_id, sequence, event_type, idempotency_key, actor_type, actor_id, payload
		) VALUES ($1::uuid, $2::uuid, 1, 'v6_work_progress_reported', 'test-progress-1', 'agent', $3::uuid,
			'{"text":"正在交叉验证三个来源","stage":"verifying"}'::jsonb)
	`, testWorkspaceID, sessionID, workerAgentID); err != nil {
		t.Fatalf("insert progress event: %v", err)
	}

	presence, err := testHandler.buildResearchV6PresenceRoster(
		ctx, parseUUID(testWorkspaceID), parseUUID(sessionID), time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("build V6 presence: %v", err)
	}
	if len(presence) != 3 {
		t.Fatalf("presence entries=%d, want 3", len(presence))
	}

	director := presence[directorAgentID]
	if director.Role != "lead" || director.Phase != ResearchPresencePhaseIdle {
		t.Fatalf("director entry=%+v, want lead/idle", director)
	}
	if director.Name == "" {
		t.Fatalf("director entry missing agent name: %+v", director)
	}

	worker := presence[workerAgentID]
	if worker.Phase != ResearchPresencePhaseRunning {
		t.Fatalf("worker phase=%q, want running", worker.Phase)
	}
	if worker.Activity != "正在交叉验证三个来源" {
		t.Fatalf("worker activity=%q, want progress note text", worker.Activity)
	}
	if worker.TaskID == nil || *worker.TaskID == "" {
		t.Fatalf("worker entry missing work item binding: %+v", worker)
	}
	if worker.ExpiresAt == nil {
		t.Fatalf("worker entry missing lease expiry: %+v", worker)
	}

	stale := presence[staleAgentID]
	if stale.Phase != ResearchPresencePhaseStale {
		t.Fatalf("stale phase=%q, want stale", stale.Phase)
	}
	if stale.StaleReason == nil || *stale.StaleReason != researchV6PresenceStaleReason {
		t.Fatalf("stale entry reason=%+v, want lease_expired", stale.StaleReason)
	}
}
