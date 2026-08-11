package handler

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func createTestGraphNode(t *testing.T, ctx context.Context, params db.CreateResearchGraphNodeParams) db.ResearchGraphNode {
	t.Helper()
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin graph node tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := testHandler.Queries.WithTx(tx)
	node, err := qtx.CreateResearchGraphNode(ctx, params)
	if err != nil {
		t.Fatalf("create graph node: %v", err)
	}
	if err := ensureGraphNodePassportTx(ctx, tx, params.WorkspaceID, params.SessionID, node.ID); err != nil {
		t.Fatalf("ensure graph node passport: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit graph node tx: %v", err)
	}
	return node
}

func createTestGraphNodeTyped(t *testing.T, ctx context.Context, params db.CreateResearchGraphNodeTypedParams) db.ResearchGraphNode {
	t.Helper()
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin typed graph node tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := testHandler.Queries.WithTx(tx)
	node, err := qtx.CreateResearchGraphNodeTyped(ctx, params)
	if err != nil {
		t.Fatalf("create typed graph node: %v", err)
	}
	if err := ensureGraphNodePassportTx(ctx, tx, params.WorkspaceID, params.SessionID, node.ID); err != nil {
		t.Fatalf("ensure typed graph node passport: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit typed graph node tx: %v", err)
	}
	return node
}

func createTestGraphEdge(t *testing.T, ctx context.Context, params db.CreateResearchGraphEdgeParams) db.ResearchGraphEdge {
	t.Helper()
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin graph edge tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := testHandler.Queries.WithTx(tx)
	edge, err := qtx.CreateResearchGraphEdge(ctx, params)
	if err != nil {
		t.Fatalf("create graph edge: %v", err)
	}
	if err := ensureGraphEdgePassportTx(ctx, tx, params.WorkspaceID, params.SessionID, edge.ID); err != nil {
		t.Fatalf("ensure graph edge passport: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit graph edge tx: %v", err)
	}
	return edge
}

func createForeignWorkspaceFindingNode(t *testing.T, ctx context.Context, workspaceID pgtype.UUID) pgtype.UUID {
	t.Helper()
	// research_session.fleet_id is NOT NULL — seed a throwaway fleet (lead may
	// live in the test workspace; only workspace_id scopes the foreign session).
	leadAgentID := createHandlerTestAgent(t, "foreign-ws-lead", nil)
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin foreign workspace tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var fleetID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO research_fleet (workspace_id, lead_agent_id)
		VALUES ($1, $2)
		RETURNING id
	`, workspaceID, leadAgentID).Scan(&fleetID); err != nil {
		t.Fatalf("create foreign research fleet: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM research_fleet WHERE id = $1`, fleetID)
	})

	var sessionID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO research_session (workspace_id, fleet_id, created_by, title, goal, status)
		VALUES ($1, $2, $3, 'foreign session', 'foreign goal', 'running')
		RETURNING id
	`, workspaceID, fleetID, parseUUID(testUserID)).Scan(&sessionID); err != nil {
		t.Fatalf("create foreign research session: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT research_ensure_run_session_passport($1, $2)`, workspaceID, sessionID); err != nil {
		t.Fatalf("ensure foreign run session passport: %v", err)
	}

	qtx := testHandler.Queries.WithTx(tx)
	node, err := qtx.CreateResearchGraphNodeTyped(ctx, db.CreateResearchGraphNodeTypedParams{
		WorkspaceID:     workspaceID,
		SessionID:       sessionID,
		NodeType:        "finding",
		Title:           "foreign-ws",
		Summary:         "foreign",
		Status:          "active",
		ActorAgentID:    pgtype.UUID{},
		Level:           "M",
		Round:           1,
		ClusterID:       pgtype.UUID{},
		Confidence:      pgtype.Float8{Float64: 0.7, Valid: true},
		DocumentCount:   1,
		ConclusionCount: 0,
		GoalVersionID:   pgtype.UUID{},
		DerivedFrom:     pgtype.UUID{},
		MergedFrom:      []pgtype.UUID{},
		SupersededBy:    pgtype.UUID{},
		RestartOf:       pgtype.UUID{},
		InvalidatedBy:   pgtype.UUID{},
		Payload:         []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("create foreign graph node: %v", err)
	}
	if err := ensureGraphNodePassportTx(ctx, tx, workspaceID, sessionID, node.ID); err != nil {
		t.Fatalf("ensure foreign graph node passport: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit foreign workspace graph node: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		for _, statement := range []string{
			`ALTER TABLE research_artifact_policy_mutation DISABLE TRIGGER research_artifact_policy_mutation_append_only_guard`,
			`ALTER TABLE research_artifact_lifecycle_event DISABLE TRIGGER research_artifact_lifecycle_event_append_only_guard`,
		} {
			if _, cleanupErr := testPool.Exec(cleanupCtx, statement); cleanupErr != nil {
				t.Errorf("disable research append-only cleanup guard: %v", cleanupErr)
				return
			}
		}
		_, cleanupErr := testPool.Exec(cleanupCtx, `DELETE FROM workspace WHERE id = $1`, workspaceID)
		for _, statement := range []string{
			`ALTER TABLE research_artifact_policy_mutation ENABLE TRIGGER research_artifact_policy_mutation_append_only_guard`,
			`ALTER TABLE research_artifact_lifecycle_event ENABLE TRIGGER research_artifact_lifecycle_event_append_only_guard`,
		} {
			if _, err := testPool.Exec(cleanupCtx, statement); err != nil {
				t.Errorf("restore research append-only cleanup guard: %v", err)
				return
			}
		}
		if cleanupErr != nil {
			t.Errorf("delete foreign research workspace fixture: %v", cleanupErr)
		}
	})
	return node.ID
}

func insertTestGraphNodeRaw(t *testing.T, ctx context.Context, id pgtype.UUID, workspaceID, sessionID pgtype.UUID, nodeType, title, summary string) {
	t.Helper()
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin raw graph node tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		INSERT INTO research_graph_node (id, workspace_id, session_id, node_type, title, summary, status, payload)
		VALUES ($1, $2, $3, $4, $5, $6, 'active', '{}'::jsonb)
	`, id, workspaceID, sessionID, nodeType, title, summary); err != nil {
		t.Fatalf("insert raw graph node: %v", err)
	}
	if err := ensureGraphNodePassportTx(ctx, tx, workspaceID, sessionID, id); err != nil {
		t.Fatalf("ensure raw graph node passport: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit raw graph node tx: %v", err)
	}
}
