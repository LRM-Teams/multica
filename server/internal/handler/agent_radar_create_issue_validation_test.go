package handler

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestValidateRadarIssueCreateAssigneeForChannel_AllowsOrdinaryAgent proves
// the narrowed gate (task #903) does not reject a plain agent that was never
// a research_fleet member — the prior `managedRole.Valid` check behaved as a
// blanket "any managed role" gate purely because research_fleet was the only
// value ever written, not by design; this locks the narrowed, explicit scope.
func TestValidateRadarIssueCreateAssigneeForChannel_AllowsOrdinaryAgent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	suffix := uuid.NewString()

	ordinaryAgentID := createHandlerTestAgent(t, "ordinary-radar-"+suffix, nil)
	supervisorID := createHandlerTestAgent(t, "supervisor-ordinary-"+suffix, nil)
	runtimeID := handlerTestRuntimeID(t)
	if _, err := testPool.Exec(ctx, `UPDATE agent SET runtime_id = $1 WHERE id = $2`, runtimeID, ordinaryAgentID); err != nil {
		t.Fatalf("attach runtime: %v", err)
	}

	run := db.AgentRadarRun{WorkspaceID: parseUUID(testWorkspaceID)}
	supervisor := db.Agent{ID: parseUUID(supervisorID)}
	err := testHandler.validateRadarIssueCreateAssigneeForChannel(
		ctx, run, supervisor,
		pgtype.UUID{},
		pgtype.Text{String: "agent", Valid: true},
		parseUUID(ordinaryAgentID),
	)
	if err != nil {
		t.Fatalf("ordinary agent with no research fleet membership should be assignable, got error: %v", err)
	}
}

// TestValidateRadarIssueCreateAssigneeForChannel_RejectsResearchFleetMember
// locks task #903's redirect: the "managed group manager cannot be assigned
// delivery work" gate keys off research_fleet_member table membership, not
// the retired agent.managed_role='research_fleet' value.
func TestValidateRadarIssueCreateAssigneeForChannel_RejectsResearchFleetMember(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	suffix := uuid.NewString()

	fleetAgentID := createHandlerTestAgent(t, "fleet-radar-"+suffix, nil)
	supervisorID := createHandlerTestAgent(t, "supervisor-radar-"+suffix, nil)
	var fleetID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO research_fleet (workspace_id) VALUES ($1)
		ON CONFLICT (workspace_id) DO UPDATE SET workspace_id = EXCLUDED.workspace_id
		RETURNING id
	`, testWorkspaceID).Scan(&fleetID); err != nil {
		t.Fatalf("create research fleet: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO research_fleet_member (workspace_id, fleet_id, agent_id, role, status)
		VALUES ($1, $2, $3, 'scout-'||$4, 'active')
	`, testWorkspaceID, fleetID, fleetAgentID, suffix); err != nil {
		t.Fatalf("create research fleet member: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM research_fleet_member WHERE agent_id = $1`, fleetAgentID)
	})

	run := db.AgentRadarRun{WorkspaceID: parseUUID(testWorkspaceID)}
	supervisor := db.Agent{ID: parseUUID(supervisorID)}
	err := testHandler.validateRadarIssueCreateAssigneeForChannel(
		ctx, run, supervisor,
		pgtype.UUID{}, // no channel membership check for this focused test
		pgtype.Text{String: "agent", Valid: true},
		parseUUID(fleetAgentID),
	)
	if err == nil {
		t.Fatal("expected research fleet member to be rejected as delivery-work assignee")
	}
}

// TestValidateRadarIssueCreateAssigneeForChannel_AllowsArchivedFormerMember
// proves an agent that has left the fleet (status='archived') is no longer
// treated as a managed group manager — the prior agent.managed_role approach
// never cleared on archive, so this is a real behavior fix, not just a
// like-for-like redirect.
func TestValidateRadarIssueCreateAssigneeForChannel_AllowsArchivedFormerMember(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	suffix := uuid.NewString()

	formerAgentID := createHandlerTestAgent(t, "fleet-alumnus-"+suffix, nil)
	supervisorID := createHandlerTestAgent(t, "supervisor-alumnus-"+suffix, nil)
	var fleetID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO research_fleet (workspace_id) VALUES ($1)
		ON CONFLICT (workspace_id) DO UPDATE SET workspace_id = EXCLUDED.workspace_id
		RETURNING id
	`, testWorkspaceID).Scan(&fleetID); err != nil {
		t.Fatalf("create research fleet: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO research_fleet_member (workspace_id, fleet_id, agent_id, role, status)
		VALUES ($1, $2, $3, 'scout-'||$4, 'archived')
	`, testWorkspaceID, fleetID, formerAgentID, suffix); err != nil {
		t.Fatalf("create archived research fleet member: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM research_fleet_member WHERE agent_id = $1`, formerAgentID)
	})
	// Give the former member a runtime so it clears the later
	// archived_at/runtime_id availability check and the fleet-membership gate
	// is what's actually under test.
	runtimeID := handlerTestRuntimeID(t)
	if _, err := testPool.Exec(ctx, `UPDATE agent SET runtime_id = $1 WHERE id = $2`, runtimeID, formerAgentID); err != nil {
		t.Fatalf("attach runtime: %v", err)
	}

	run := db.AgentRadarRun{WorkspaceID: parseUUID(testWorkspaceID)}
	supervisor := db.Agent{ID: parseUUID(supervisorID)}
	err := testHandler.validateRadarIssueCreateAssigneeForChannel(
		ctx, run, supervisor,
		pgtype.UUID{},
		pgtype.Text{String: "agent", Valid: true},
		parseUUID(formerAgentID),
	)
	if err != nil {
		t.Fatalf("archived former fleet member should be assignable, got error: %v", err)
	}
}
