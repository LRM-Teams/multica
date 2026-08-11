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
