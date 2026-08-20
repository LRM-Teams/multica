package handler

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/memorysignal"
)

func seedGuardTestAgent(t *testing.T, ctx context.Context) (wsUUID, agentUUID pgtype.UUID) {
	t.Helper()
	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider, device_info, owner_id)
		VALUES ($1, 'friction guard runtime', 'local', 'legacy_local', 'friction guard runtime', $2)
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	var agentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id, owner_id, model)
		VALUES ($1, 'friction guard agent', 'local', $2, $3, 'composer-1.5')
		RETURNING id
	`, testWorkspaceID, runtimeID, testUserID).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = testPool.Exec(cleanupCtx, `DELETE FROM agent_memory_curation_candidate WHERE source_agent_id = $1`, agentID)
		_, _ = testPool.Exec(cleanupCtx, `DELETE FROM agent WHERE id = $1`, agentID)
		_, _ = testPool.Exec(cleanupCtx, `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})
	return parseUUID(testWorkspaceID), parseUUID(agentID)
}

func TestEnqueueMemoryGuardCandidatesFriction(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	wsUUID, agentUUID := seedGuardTestAgent(t, ctx)

	friction := memorysignal.FrictionVector{RetryLoop: 2, SelfErrorStreak: 1}
	queued, err := testHandler.enqueueMemoryGuardCandidates(
		ctx, wsUUID, agentUUID, pgtype.UUID{}, "帮我修这个迁移测试", "", nil, nil, friction)
	if err != nil {
		t.Fatal(err)
	}
	if !queued {
		t.Fatal("non-zero friction with no durable write should queue a candidate")
	}

	var source, scope, retryLoop string
	if err := testPool.QueryRow(ctx, `
		SELECT metadata->>'source', scope, metadata->'friction'->>'retry_loop'
		  FROM agent_memory_curation_candidate
		 WHERE source_agent_id = $1 AND status = 'pending' AND metadata->>'source' = 'friction_guard'
	`, uuidToString(agentUUID)).Scan(&source, &scope, &retryLoop); err != nil {
		t.Fatal(err)
	}
	if source != "friction_guard" || scope != "agent" || retryLoop != "2" {
		t.Fatalf("candidate source=%q scope=%q retry_loop=%q", source, scope, retryLoop)
	}

	// Same dedupe_key while pending: second call must not queue another one.
	queued, err = testHandler.enqueueMemoryGuardCandidates(
		ctx, wsUUID, agentUUID, pgtype.UUID{}, "帮我修这个迁移测试", "", nil, nil, friction)
	if err != nil {
		t.Fatal(err)
	}
	if queued {
		t.Fatal("pending friction candidate with same dedupe_key must not re-queue")
	}
}

func TestEnqueueMemoryGuardCandidatesDecision(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	wsUUID, agentUUID := seedGuardTestAgent(t, ctx)

	queued, err := testHandler.enqueueMemoryGuardCandidates(
		ctx, wsUUID, agentUUID, pgtype.UUID{}, "行，就用 B 方案", "", nil, nil, memorysignal.FrictionVector{})
	if err != nil {
		t.Fatal(err)
	}
	if !queued {
		t.Fatal("finalized decision with no durable write should queue a candidate")
	}
	var scope string
	if err := testPool.QueryRow(ctx, `
		SELECT scope FROM agent_memory_curation_candidate
		 WHERE source_agent_id = $1 AND status = 'pending' AND metadata->>'source' = 'decision_guard'
	`, uuidToString(agentUUID)).Scan(&scope); err != nil {
		t.Fatal(err)
	}
	if scope != "project" {
		t.Fatalf("decision candidate scope=%q want project", scope)
	}

	// A DECISIONS.md write in the same turn satisfies the guard: nothing queued.
	writes := []memorysignal.WriteEntry{{RelPath: "projects/p1/DECISIONS.md", ScopeType: "project", FileKey: "DECISIONS"}}
	queued, err = testHandler.enqueueMemoryGuardCandidates(
		ctx, wsUUID, agentUUID, pgtype.UUID{}, "定了，统一用行级锁", "", nil, writes, memorysignal.FrictionVector{})
	if err != nil {
		t.Fatal(err)
	}
	if queued {
		t.Fatal("decision written to DECISIONS.md must not queue a candidate")
	}
}

func TestEnqueueMemoryGuardCandidatesCorrectionTrigger(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	wsUUID, agentUUID := seedGuardTestAgent(t, ctx)

	// A correcting trigger with zero daemon-side friction still counts one
	// human_correction episode and queues a friction candidate when the turn
	// ends without a durable write.
	friction := memorysignal.AugmentFrictionFromTrigger(memorysignal.FrictionVector{}, "不对，先停下，换个思路")
	queued, err := testHandler.enqueueMemoryGuardCandidates(
		ctx, wsUUID, agentUUID, pgtype.UUID{}, "不对，先停下，换个思路", "", nil, nil, friction)
	if err != nil {
		t.Fatal(err)
	}
	if !queued {
		t.Fatal("correcting trigger with no durable write should queue a candidate")
	}
	var humanCorrection string
	if err := testPool.QueryRow(ctx, `
		SELECT metadata->'friction'->>'human_correction'
		  FROM agent_memory_curation_candidate
		 WHERE source_agent_id = $1 AND status = 'pending' AND metadata->>'source' = 'friction_guard'
	`, uuidToString(agentUUID)).Scan(&humanCorrection); err != nil {
		t.Fatal(err)
	}
	if humanCorrection != "1" {
		t.Fatalf("human_correction=%q want 1", humanCorrection)
	}
}

func TestEnqueueMemoryGuardCandidatesSmoothTurnQueuesNothing(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	wsUUID, agentUUID := seedGuardTestAgent(t, ctx)

	queued, err := testHandler.enqueueMemoryGuardCandidates(
		ctx, wsUUID, agentUUID, pgtype.UUID{}, "帮我看一下这个 bug", "", nil, nil, memorysignal.FrictionVector{})
	if err != nil {
		t.Fatal(err)
	}
	if queued {
		t.Fatal("smooth turn with plain trigger must not queue any candidate")
	}
	var count int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_memory_curation_candidate WHERE source_agent_id = $1
	`, uuidToString(agentUUID)).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected zero candidates, got %d", count)
	}
}
