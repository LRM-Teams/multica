package handler

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestProcessResearchNextSteps_SilentWindowAutoStepsGe3 is the LRM-1076 AC1/AC5
// running evidence: with the user quiet, one scheduler tick emits ≥3 unattended
// graph-append probes and increments unattended_auto_steps accordingly —
// without any chat trigger.
func TestProcessResearchNextSteps_SilentWindowAutoStepsGe3(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	t.Setenv("RESEARCH_UNATTENDED_QUIET_AFTER", "0s")
	t.Setenv("RESEARCH_NEXTSTEP_MAX_PER_TICK", "3")

	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	wsUUID := parseUUID(testWorkspaceID)
	userUUID := parseUUID(testUserID)

	leadID := createHandlerTestAgent(t, "ronaldo-silent-"+suffix, nil)

	var fleetID pgtype.UUID
	if err := testPool.QueryRow(ctx, `
		INSERT INTO research_fleet (workspace_id, lead_agent_id)
		VALUES ($1, $2)
		ON CONFLICT (workspace_id) DO UPDATE
		  SET lead_agent_id = EXCLUDED.lead_agent_id, updated_at = now()
		RETURNING id
	`, testWorkspaceID, leadID).Scan(&fleetID); err != nil {
		t.Fatalf("upsert research fleet: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO research_fleet_member (workspace_id, fleet_id, agent_id, role, status, is_lead)
		VALUES ($1, $2, $3, 'lead', 'active', true)
		ON CONFLICT (fleet_id, agent_id) DO UPDATE
		  SET status = 'active', is_lead = true, role = 'lead', updated_at = now()
	`, testWorkspaceID, fleetID, leadID); err != nil {
		t.Fatalf("upsert fleet lead member: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM research_fleet_member WHERE agent_id = $1`, leadID)
	})

	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin session tx: %v", err)
	}
	var sessionID pgtype.UUID
	if err = tx.QueryRow(ctx, `
		INSERT INTO research_session (
			workspace_id, fleet_id, created_by, title, goal, status, current_stage,
			depth_tier, product_round, product_round_budget
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id
	`, wsUUID, fleetID, userUUID, "silent-evidence-"+suffix,
		"prove unattended nextstep emits ≥3 silent graph-append steps", "running", "s1_plan", "standard", int32(1), int32(5)).Scan(&sessionID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("create research session: %v", err)
	}
	if _, err = tx.Exec(ctx, `SELECT research_ensure_run_session_passport($1, $2)`, wsUUID, sessionID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("ensure run session passport: %v", err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit research session: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM research_session WHERE id = $1`, sessionID)
	})

	// User has been silent (quiet_after=0s means any past activity counts as quiet).
	quietAt := time.Now().UTC().Add(-35 * time.Minute)
	if _, err := testPool.Exec(ctx, `
		UPDATE research_session
		SET unattended_enabled = true,
		    unattended_auto_steps = 0,
		    last_user_activity_at = $2,
		    max_open_branches = 3,
		    updated_at = $2
		WHERE id = $1
	`, sessionID, quietAt); err != nil {
		t.Fatalf("mark session quiet: %v", err)
	}

	goal := createTestGraphNode(t, ctx, db.CreateResearchGraphNodeParams{
		WorkspaceID:  wsUUID,
		SessionID:    sessionID,
		NodeType:     "goal",
		Title:        "silent goal",
		Summary:      "prove unattended nextstep emits ≥3 silent graph-append steps",
		Status:       "active",
		ActorAgentID: parseUUID(leadID),
		Payload:      []byte(`{"seed":true}`),
	})
	for i := 0; i < 3; i++ {
		sq := createTestGraphNode(t, ctx, db.CreateResearchGraphNodeParams{
			WorkspaceID:  wsUUID,
			SessionID:    sessionID,
			NodeType:     "subquestion",
			Title:        fmt.Sprintf("silent-sq-%s-%d", suffix, i),
			Summary:      "childless dimension for nextstep scan",
			Status:       "active",
			ActorAgentID: parseUUID(leadID),
			Payload:      marshalJSONRaw(map[string]any{"seed": true, "index": i}),
		})
		createTestGraphEdge(t, ctx, db.CreateResearchGraphEdgeParams{
			WorkspaceID: wsUUID,
			SessionID:   sessionID,
			FromNodeID:  goal.ID,
			ToNodeID:    sq.ID,
			EdgeType:    "leads_to",
		})
	}

	emitted, err := testHandler.ProcessResearchNextSteps(ctx, 8)
	if err != nil {
		t.Fatalf("ProcessResearchNextSteps: %v", err)
	}
	if emitted < 3 {
		t.Fatalf("expected ≥3 work items enqueued in silent window, got %d", emitted)
	}

	got, err := testHandler.Queries.GetResearchSession(ctx, db.GetResearchSessionParams{
		ID:          sessionID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if got.UnattendedAutoSteps < 3 {
		t.Fatalf("AC1 evidence failed: unattended_auto_steps=%d want ≥3 (no chat trigger)", got.UnattendedAutoSteps)
	}

	nodes, err := testHandler.Queries.ListResearchGraphNodes(ctx, db.ListResearchGraphNodesParams{
		SessionID:   sessionID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	probeCount := 0
	for _, n := range nodes {
		if n.NodeType == "probe" {
			var payload map[string]any
			_ = jsonUnmarshalMap(n.Payload, &payload)
			if unattended, _ := payload["unattended"].(bool); unattended {
				probeCount++
			}
		}
	}
	if probeCount < 3 {
		t.Fatalf("expected ≥3 unattended probe graph-appends, got %d", probeCount)
	}

	var eventCount int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*)::int FROM research_scheduler_event
		WHERE session_id = $1 AND event_type IN ('nextstep_enqueued', 'nextstep_wake_failed', 'unattended_auto_step')
	`, sessionID).Scan(&eventCount); err != nil {
		t.Fatalf("count scheduler events: %v", err)
	}
	if eventCount < 3 {
		t.Fatalf("expected ≥3 scheduler events documenting silent steps, got %d", eventCount)
	}
}
