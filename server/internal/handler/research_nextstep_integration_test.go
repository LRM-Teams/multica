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

	session, err := testHandler.Queries.CreateResearchSession(ctx, db.CreateResearchSessionParams{
		WorkspaceID:        wsUUID,
		FleetID:            fleetID,
		CreatedBy:          userUUID,
		Title:              "silent-evidence-" + suffix,
		Goal:               "prove unattended nextstep emits ≥3 silent graph-append steps",
		Status:             "running",
		CurrentStage:       "s1_plan",
		DepthTier:           "standard",
		ProductRound:       1,
		ProductRoundBudget: 5,
	})
	if err != nil {
		t.Fatalf("create research session: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM research_session WHERE id = $1`, session.ID)
	})

	// User has been silent (quiet_after=0s means any past activity counts as quiet).
	quietAt := time.Now().UTC().Add(-35 * time.Minute)
	if _, err := testPool.Exec(ctx, `
		UPDATE research_session
		SET unattended_enabled = true,
		    unattended_auto_steps = 0,
		    last_user_activity_at = $2,
		    max_open_branches = 3
		WHERE id = $1
	`, session.ID, quietAt); err != nil {
		t.Fatalf("mark session quiet: %v", err)
	}

	goal, err := testHandler.Queries.CreateResearchGraphNode(ctx, db.CreateResearchGraphNodeParams{
		WorkspaceID:  wsUUID,
		SessionID:    session.ID,
		NodeType:     "goal",
		Title:        "silent goal",
		Summary:      session.Goal,
		Status:       "active",
		ActorAgentID: parseUUID(leadID),
		Payload:      []byte(`{"seed":true}`),
	})
	if err != nil {
		t.Fatalf("create goal node: %v", err)
	}
	for i := 0; i < 3; i++ {
		sq, err := testHandler.Queries.CreateResearchGraphNode(ctx, db.CreateResearchGraphNodeParams{
			WorkspaceID:  wsUUID,
			SessionID:    session.ID,
			NodeType:     "subquestion",
			Title:        fmt.Sprintf("silent-sq-%s-%d", suffix, i),
			Summary:      "childless dimension for nextstep scan",
			Status:       "active",
			ActorAgentID: parseUUID(leadID),
			Payload:      marshalJSONRaw(map[string]any{"seed": true, "index": i}),
		})
		if err != nil {
			t.Fatalf("create subquestion %d: %v", i, err)
		}
		if _, err := testHandler.Queries.CreateResearchGraphEdge(ctx, db.CreateResearchGraphEdgeParams{
			WorkspaceID: wsUUID,
			SessionID:   session.ID,
			FromNodeID:  goal.ID,
			ToNodeID:    sq.ID,
			EdgeType:    "leads_to",
		}); err != nil {
			t.Fatalf("create edge %d: %v", i, err)
		}
	}

	emitted, err := testHandler.ProcessResearchNextSteps(ctx, 8)
	if err != nil {
		t.Fatalf("ProcessResearchNextSteps: %v", err)
	}
	if emitted < 3 {
		t.Fatalf("expected ≥3 work items enqueued in silent window, got %d", emitted)
	}

	got, err := testHandler.Queries.GetResearchSession(ctx, db.GetResearchSessionParams{
		ID:          session.ID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if got.UnattendedAutoSteps < 3 {
		t.Fatalf("AC1 evidence failed: unattended_auto_steps=%d want ≥3 (no chat trigger)", got.UnattendedAutoSteps)
	}

	nodes, err := testHandler.Queries.ListResearchGraphNodes(ctx, db.ListResearchGraphNodesParams{
		SessionID:   session.ID,
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
	`, session.ID).Scan(&eventCount); err != nil {
		t.Fatalf("count scheduler events: %v", err)
	}
	if eventCount < 3 {
		t.Fatalf("expected ≥3 scheduler events documenting silent steps, got %d", eventCount)
	}

	// fmt so CI (no -v) still prints the evidence numbers Beckham/AC5 need.
	fmt.Printf("LRM-1076 silent-window evidence: emitted=%d unattended_auto_steps=%d unattended_probes=%d scheduler_events=%d (user quiet since %s, no chat)\n",
		emitted, got.UnattendedAutoSteps, probeCount, eventCount, quietAt.Format(time.RFC3339))
}
