package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
)

// LRM-984: claim-time injected + successful-complete used must be auditable
// by unit_id + execution_id (inbox event id).
func TestEvolutionMemoryInjectionAndUsedAttribution(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "memory-usage-"+randomID(), nil)
	syncKey := "memory/MEMORY.md"
	var memoryID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_memory (
			workspace_id, agent_id, name, content, config, sync_key, created_by
		)
		VALUES (
			$1, $2, '复用偏好', '同类任务先检索再动手。',
			jsonb_build_object('scope', 'agent'),
			$3, $4
		)
		RETURNING id
	`, testWorkspaceID, agentID, syncKey, testUserID).Scan(&memoryID); err != nil {
		t.Fatalf("seed agent memory: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM evolution_unit_feedback_event WHERE agent_id=$1`, agentID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_memory WHERE id=$1`, memoryID)
	})

	executionID := parseUUID(uuid.NewString())
	memories := []service.AgentMemoryData{{
		ID:      memoryID,
		Name:    "复用偏好",
		Content: "同类任务先检索再动手。",
		Scope:   "agent",
		SyncKey: syncKey,
	}}
	testHandler.TaskService.RecordMemoryInjections(ctx, parseUUID(testWorkspaceID), parseUUID(agentID), executionID, memories)
	// Idempotent: second claim for same execution must not duplicate injected.
	testHandler.TaskService.RecordMemoryInjections(ctx, parseUUID(testWorkspaceID), parseUUID(agentID), executionID, memories)
	testHandler.TaskService.RecordEvolutionUnitUsed(ctx, executionID)
	testHandler.TaskService.RecordEvolutionUnitUsed(ctx, executionID)

	var injected, used int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE event='injected'),
		       count(*) FILTER (WHERE event='used')
		FROM evolution_unit_feedback_event
		WHERE agent_id=$1 AND unit_type='memory' AND unit_id=$2
		  AND metadata->>'execution_id'=$3
	`, agentID, memoryID, uuidToString(executionID)).Scan(&injected, &used); err != nil {
		t.Fatalf("load attribution: %v", err)
	}
	if injected != 1 || used != 1 {
		t.Fatalf("memory attribution injected=%d used=%d, want 1/1", injected, used)
	}
}

// LRM-984: skill injected at load + used on successful complete share execution_id.
func TestEvolutionSkillInjectionAndUsedAttribution(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fixture := seedEvolutionVersionFixture(t)
	agentID := createHandlerTestAgent(t, "skill-usage-"+randomID(), nil)
	if _, err := testPool.Exec(context.Background(), `INSERT INTO agent_skill (agent_id, skill_id) VALUES ($1, $2)`, agentID, fixture.skillID); err != nil {
		t.Fatalf("assign skill: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM evolution_unit_feedback_event WHERE agent_id=$1`, agentID)
	})

	executionID := parseUUID(uuid.NewString())
	skills := testHandler.TaskService.LoadAgentSkillsForInbox(context.Background(), parseUUID(agentID), executionID)
	if len(skills) != 1 {
		t.Fatalf("loaded skills=%d, want 1", len(skills))
	}
	testHandler.TaskService.RecordEvolutionUnitUsed(context.Background(), executionID)
	testHandler.TaskService.RecordEvolutionSkillOutcome(context.Background(), executionID, "success", "success")

	var injected, used, succeeded int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FILTER (WHERE event='injected'),
		       count(*) FILTER (WHERE event='used'),
		       count(*) FILTER (WHERE event='success')
		FROM evolution_unit_feedback_event
		WHERE agent_id=$1 AND unit_id=$2
		  AND metadata->>'execution_id'=$3
	`, agentID, fixture.unitID, uuidToString(executionID)).Scan(&injected, &used, &succeeded); err != nil {
		t.Fatalf("load skill attribution: %v", err)
	}
	if injected != 1 || used != 1 || succeeded != 1 {
		t.Fatalf("skill attribution injected=%d used=%d success=%d, want 1/1/1", injected, used, succeeded)
	}
}

// LRM-985: Goal reinjection / post-compaction anchor events are auditable by
// event_type + goal_id (claim path and compaction_finished path).
func TestChannelGoalAnchorActivityEventsAreQueryable(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	channel := createGoalTestChannel(t)
	created := httptest.NewRecorder()
	testHandler.CreateChannelGoal(created, goalRequest(t, testUserID, http.MethodPost, channel.ID, map[string]any{
		"title":            "Resume after compaction",
		"objective":        "Stay on channel goal across wakes",
		"success_criteria": []string{"goal_injected", "anchor_after_compaction"},
	}))
	if created.Code != http.StatusCreated {
		t.Fatalf("CreateChannelGoal = %d: %s", created.Code, created.Body.String())
	}
	goal := decodeGoalEnvelope(t, created).Goal
	if goal == nil || goal.ID == "" {
		t.Fatalf("created goal = %#v", goal)
	}

	agentID := createHandlerTestAgent(t, "goal-anchor-"+randomID(), nil)
	// task_id FK → agent_task_queue; leave null in unit test (claim path uses a real inbox id).
	inboxEventID := uuid.NewString()
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_activity_event WHERE agent_id=$1`, agentID)
	})

	testHandler.recordAgentActivityEvent(ctx, testHandler.DB,
		parseUUID(testWorkspaceID), parseUUID(agentID), pgtype.UUID{}, pgtype.UUID{},
		activityKindCustom, "channel_goal_injected", "info",
		"channel", parseUUID(channel.ID), "",
		"", "Channel goal reinjected for this wake",
		map[string]any{
			"goal_id":        goal.ID,
			"goal_version":   goal.Version,
			"inbox_event_id": inboxEventID,
			"channel_id":     channel.ID,
			"trigger":        "claim",
		},
	)
	testHandler.recordAgentActivityEvent(ctx, testHandler.DB,
		parseUUID(testWorkspaceID), parseUUID(agentID), pgtype.UUID{}, pgtype.UUID{},
		activityKindCustom, "channel_goal_anchor_after_compaction", "info",
		"channel", parseUUID(channel.ID), "",
		"", "Channel goal still anchored after context compaction",
		map[string]any{
			"goal_id":      goal.ID,
			"goal_version": goal.Version,
			"channel_id":   channel.ID,
		},
	)

	var injected, anchored int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE event_type='channel_goal_injected'),
		       count(*) FILTER (WHERE event_type='channel_goal_anchor_after_compaction')
		FROM agent_activity_event
		WHERE agent_id=$1 AND details->>'goal_id'=$2
	`, agentID, goal.ID).Scan(&injected, &anchored); err != nil {
		t.Fatalf("load goal activity: %v", err)
	}
	if injected != 1 || anchored != 1 {
		t.Fatalf("goal activity injected=%d anchored=%d, want 1/1", injected, anchored)
	}
}
