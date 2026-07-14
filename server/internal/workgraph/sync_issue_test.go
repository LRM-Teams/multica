package workgraph

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		fmt.Printf("Skipping tests: could not connect to database: %v\n", err)
		os.Exit(0)
	}
	if err := pool.Ping(ctx); err != nil {
		fmt.Printf("Skipping tests: database not reachable: %v\n", err)
		pool.Close()
		os.Exit(0)
	}

	testPool = pool
	code := m.Run()
	pool.Close()
	os.Exit(code)
}

func TestSyncIssueNodesAndWaitsOnEdges(t *testing.T) {
	ctx := t.Context()
	workspaceID := pgUUID(uuid.New())
	agentID := pgUUID(uuid.New())
	createWorkgraphWorkspace(t, ctx, workspaceID)
	t.Cleanup(func() {
		if _, err := testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID); err != nil {
			t.Errorf("clean up workspace: %v", err)
		}
	})

	issueA := createWorkgraphIssue(t, ctx, workspaceID, agentID, 1, "Prerequisite A", "todo")
	issueB := createWorkgraphIssue(t, ctx, workspaceID, agentID, 2, "Prerequisite B", "todo")
	issueC := createWorkgraphIssue(t, ctx, workspaceID, agentID, 3, "Blocked issue C", "todo")
	insertWorkgraphDependency(t, ctx, issueC.ID, issueA.ID, "blocked_by")
	insertWorkgraphDependency(t, ctx, issueC.ID, issueB.ID, "blocked_by")

	store := NewStore(testPool)
	nodeA, err := store.SyncIssueNode(ctx, issueA)
	if err != nil {
		t.Fatalf("sync issue A: %v", err)
	}
	nodeB, err := store.SyncIssueNode(ctx, issueB)
	if err != nil {
		t.Fatalf("sync issue B: %v", err)
	}
	nodeC, err := store.SyncIssueNode(ctx, issueC)
	if err != nil {
		t.Fatalf("sync issue C: %v", err)
	}
	if nodeA.OwnerType != "agent" || nodeA.OwnerID != agentID {
		t.Fatalf("issue A owner = (%q, %v), want agent %v", nodeA.OwnerType, nodeA.OwnerID, agentID)
	}

	if err := store.SyncDependenciesForIssue(ctx, workspaceID, issueC.ID); err != nil {
		t.Fatalf("sync issue C dependencies: %v", err)
	}
	openEdges, err := db.New(testPool).ListOpenWaitsOnFromNode(ctx, db.ListOpenWaitsOnFromNodeParams{
		WorkspaceID: workspaceID,
		FromNodeID:  nodeC.ID,
	})
	if err != nil {
		t.Fatalf("list open waits: %v", err)
	}
	if len(openEdges) != 2 {
		t.Fatalf("open waits = %d, want 2", len(openEdges))
	}
	if err := store.RecomputeIssueNodeStatus(ctx, nodeC.ID); err != nil {
		t.Fatalf("recompute C after dependencies: %v", err)
	}
	assertNodeStatus(t, ctx, workspaceID, issueC.ID, "waiting")

	issueA.Status = "done"
	if _, err := store.SyncIssueNode(ctx, issueA); err != nil {
		t.Fatalf("sync completed issue A: %v", err)
	}
	if err := store.RecomputeIssueNodeStatus(ctx, nodeC.ID); err != nil {
		t.Fatalf("recompute C after A completion: %v", err)
	}
	openEdges, err = db.New(testPool).ListOpenWaitsOnFromNode(ctx, db.ListOpenWaitsOnFromNodeParams{
		WorkspaceID: workspaceID,
		FromNodeID:  nodeC.ID,
	})
	if err != nil {
		t.Fatalf("list open waits after A completion: %v", err)
	}
	if len(openEdges) != 1 || openEdges[0].ToNodeID != nodeB.ID {
		t.Fatalf("open waits after A completion = %#v, want only B", openEdges)
	}
	assertNodeStatus(t, ctx, workspaceID, issueC.ID, "waiting")

	issueB.Status = "done"
	if _, err := store.SyncIssueNode(ctx, issueB); err != nil {
		t.Fatalf("sync completed issue B: %v", err)
	}
	if err := store.RecomputeIssueNodeStatus(ctx, nodeC.ID); err != nil {
		t.Fatalf("recompute C after B completion: %v", err)
	}
	unresolved, err := db.New(testPool).CountOpenUnresolvedWaitsOn(ctx, db.CountOpenUnresolvedWaitsOnParams{
		WorkspaceID: workspaceID,
		FromNodeID:  nodeC.ID,
	})
	if err != nil {
		t.Fatalf("count unresolved waits: %v", err)
	}
	if unresolved != 0 {
		t.Fatalf("unresolved waits = %d, want 0", unresolved)
	}
	assertNodeStatus(t, ctx, workspaceID, issueC.ID, "active")
}

func TestSyncDependenciesForIssueResolvesStaleWaitsOnEdges(t *testing.T) {
	ctx := t.Context()
	workspaceID := pgUUID(uuid.New())
	agentID := pgUUID(uuid.New())
	createWorkgraphWorkspace(t, ctx, workspaceID)
	t.Cleanup(func() {
		if _, err := testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID); err != nil {
			t.Errorf("clean up workspace: %v", err)
		}
	})

	issueA := createWorkgraphIssue(t, ctx, workspaceID, agentID, 1, "Prerequisite A", "todo")
	issueC := createWorkgraphIssue(t, ctx, workspaceID, agentID, 2, "Blocked issue C", "todo")
	insertWorkgraphDependency(t, ctx, issueC.ID, issueA.ID, "blocked_by")

	store := NewStore(testPool)
	if _, err := store.SyncIssueNode(ctx, issueA); err != nil {
		t.Fatalf("sync issue A: %v", err)
	}
	nodeC, err := store.SyncIssueNode(ctx, issueC)
	if err != nil {
		t.Fatalf("sync issue C: %v", err)
	}
	if err := store.SyncDependenciesForIssue(ctx, workspaceID, issueC.ID); err != nil {
		t.Fatalf("sync initial dependency: %v", err)
	}
	assertNodeStatus(t, ctx, workspaceID, issueC.ID, "waiting")

	if _, err := testPool.Exec(ctx, `
		DELETE FROM issue_dependency
		WHERE issue_id = $1 AND depends_on_issue_id = $2
	`, issueC.ID, issueA.ID); err != nil {
		t.Fatalf("delete dependency: %v", err)
	}
	if err := store.SyncDependenciesForIssue(ctx, workspaceID, issueC.ID); err != nil {
		t.Fatalf("sync removed dependency: %v", err)
	}

	openEdges, err := db.New(testPool).ListOpenWaitsOnFromNode(ctx, db.ListOpenWaitsOnFromNodeParams{
		WorkspaceID: workspaceID,
		FromNodeID:  nodeC.ID,
	})
	if err != nil {
		t.Fatalf("list open waits: %v", err)
	}
	if len(openEdges) != 0 {
		t.Fatalf("open waits after removing dependency = %d, want 0", len(openEdges))
	}
	assertNodeStatus(t, ctx, workspaceID, issueC.ID, "active")
}

func createWorkgraphWorkspace(t *testing.T, ctx context.Context, id pgtype.UUID) {
	t.Helper()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO workspace (id, name, slug, issue_prefix)
		VALUES ($1, 'Workgraph Test', $2, 'WGR')
	`, id, "workgraph-"+uuid.NewString()); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
}

func createWorkgraphIssue(t *testing.T, ctx context.Context, workspaceID, agentID pgtype.UUID, number int, title, status string) db.Issue {
	t.Helper()
	issue := db.Issue{
		ID:           pgUUID(uuid.New()),
		WorkspaceID:  workspaceID,
		Title:        title,
		Description:  pgtype.Text{String: title + " description", Valid: true},
		Status:       status,
		AssigneeType: pgtype.Text{String: "agent", Valid: true},
		AssigneeID:   agentID,
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO issue (
			id, workspace_id, number, title, description, status, priority,
			assignee_type, assignee_id, creator_type, creator_id, position
		) VALUES ($1, $2, $3, $4, $5, $6, 'none', 'agent', $7, 'agent', $7, 0)
	`, issue.ID, issue.WorkspaceID, number, issue.Title, issue.Description, issue.Status, agentID); err != nil {
		t.Fatalf("create issue %q: %v", title, err)
	}
	return issue
}

func insertWorkgraphDependency(t *testing.T, ctx context.Context, issueID, dependsOnIssueID pgtype.UUID, dependencyType string) {
	t.Helper()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO issue_dependency (issue_id, depends_on_issue_id, type)
		VALUES ($1, $2, $3)
	`, issueID, dependsOnIssueID, dependencyType); err != nil {
		t.Fatalf("create dependency: %v", err)
	}
}

func assertNodeStatus(t *testing.T, ctx context.Context, workspaceID, issueID pgtype.UUID, want string) {
	t.Helper()
	node, err := db.New(testPool).GetWorkNodeByIssue(ctx, db.GetWorkNodeByIssueParams{
		WorkspaceID:   workspaceID,
		LinkedIssueID: issueID,
	})
	if err != nil {
		t.Fatalf("load node: %v", err)
	}
	if node.Status != want {
		t.Fatalf("node status = %q, want %q", node.Status, want)
	}
}

func pgUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}
