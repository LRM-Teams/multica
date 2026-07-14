package workgraph

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestDetectUnlockSilentWhilePrereqsOpen(t *testing.T) {
	ctx := t.Context()
	store, scenario := setupUnlockScenario(t, ctx)

	if err := store.DetectUnlockForNode(ctx, scenario.waiter.ID); err != nil {
		t.Fatalf("detect unlock: %v", err)
	}
	assertPendingUnlockCount(t, ctx, scenario.waiter.WorkspaceID, 0)
}

func TestDetectUnlockEnqueuesWhenAllPrereqsDone(t *testing.T) {
	ctx := t.Context()
	store, scenario := setupUnlockScenario(t, ctx)
	resolveUnlockPrerequisites(t, ctx, store, scenario.prerequisites)

	if err := store.DetectUnlockForNode(ctx, scenario.waiter.ID); err != nil {
		t.Fatalf("detect unlock: %v", err)
	}
	handoff := assertPendingUnlockCount(t, ctx, scenario.waiter.WorkspaceID, 1)
	if handoff.TargetActorType != ownerTypeAgent || handoff.TargetActorID != scenario.waiter.OwnerID {
		t.Fatalf("handoff target = (%q, %v), want agent %v", handoff.TargetActorType, handoff.TargetActorID, scenario.waiter.OwnerID)
	}
	if handoff.Urgency != "fast" || handoff.ReasonCode != "unlock" {
		t.Fatalf("handoff = (%q, %q), want (fast, unlock)", handoff.Urgency, handoff.ReasonCode)
	}
	if len(handoff.RelatedNodeIds) != 3 {
		t.Fatalf("related node ids = %d, want waiter plus two prerequisites", len(handoff.RelatedNodeIds))
	}
	prerequisiteNodeIDs := []string{
		handoff.RelatedNodeIds[1].String(),
		handoff.RelatedNodeIds[2].String(),
	}
	wantDedupeKey := "unlock:" + scenario.waiter.ID.String() + ":" + strings.Join(prerequisiteNodeIDs, ",")
	if handoff.DedupeKey != wantDedupeKey {
		t.Fatalf("dedupe key = %q, want %q", handoff.DedupeKey, wantDedupeKey)
	}
}

func TestDetectUnlockDedupe(t *testing.T) {
	ctx := t.Context()
	store, scenario := setupUnlockScenario(t, ctx)
	resolveUnlockPrerequisites(t, ctx, store, scenario.prerequisites)

	if err := store.DetectUnlockForNode(ctx, scenario.waiter.ID); err != nil {
		t.Fatalf("first detect unlock: %v", err)
	}
	if err := store.DetectUnlockForNode(ctx, scenario.waiter.ID); err != nil {
		t.Fatalf("second detect unlock: %v", err)
	}
	assertPendingUnlockCount(t, ctx, scenario.waiter.WorkspaceID, 1)
}

func TestDetectUnlockSkipsWendyOwner(t *testing.T) {
	ctx := t.Context()
	store, scenario := setupUnlockScenario(t, ctx)
	resolveUnlockPrerequisites(t, ctx, store, scenario.prerequisites)

	if _, err := testPool.Exec(ctx, `
		WITH runtime AS (
			INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider)
			VALUES ($2, 'Wendy runtime', 'local', 'test')
			RETURNING id
		)
		INSERT INTO agent (id, workspace_id, name, runtime_mode, runtime_config, runtime_id)
		SELECT $1, $2, 'Wendy', 'local', '{}'::jsonb, runtime.id
		FROM runtime
	`, scenario.waiter.OwnerID, scenario.waiter.WorkspaceID); err != nil {
		t.Fatalf("create Wendy supervisor: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO workspace_radar_state (workspace_id, supervisor_agent_id)
		VALUES ($1, $2)
	`, scenario.waiter.WorkspaceID, scenario.waiter.OwnerID); err != nil {
		t.Fatalf("bind Wendy supervisor: %v", err)
	}

	if err := store.DetectUnlockForNode(ctx, scenario.waiter.ID); err != nil {
		t.Fatalf("detect unlock: %v", err)
	}
	assertPendingUnlockCount(t, ctx, scenario.waiter.WorkspaceID, 0)
}

func TestDetectUnlockSkipsNodeWithoutWaitsOnEdges(t *testing.T) {
	ctx := t.Context()
	workspaceID := pgUUID(uuid.New())
	agentID := pgUUID(uuid.New())
	createWorkgraphWorkspace(t, ctx, workspaceID)
	t.Cleanup(func() {
		if _, err := testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID); err != nil {
			t.Errorf("clean up workspace: %v", err)
		}
	})

	issue := createWorkgraphIssue(t, ctx, workspaceID, agentID, 1, "Independent issue", "todo")
	store := NewStore(testPool)
	node, err := store.SyncIssueNode(ctx, issue)
	if err != nil {
		t.Fatalf("sync independent issue: %v", err)
	}

	if err := store.DetectUnlockForNode(ctx, node.ID); err != nil {
		t.Fatalf("detect unlock: %v", err)
	}
	assertPendingUnlockCount(t, ctx, workspaceID, 0)
}

func TestDetectUnlockSkipsNonAgentOwner(t *testing.T) {
	for _, ownerType := range []string{ownerTypeMember, ownerTypeUnassigned} {
		t.Run(ownerType, func(t *testing.T) {
			ctx := t.Context()
			store, scenario := setupUnlockScenario(t, ctx)
			resolveUnlockPrerequisites(t, ctx, store, scenario.prerequisites)
			if _, err := testPool.Exec(ctx, `
				UPDATE work_node
				SET owner_type = $1,
				    owner_id = CASE WHEN $1 = 'unassigned' THEN NULL ELSE owner_id END
				WHERE id = $2
			`, ownerType, scenario.waiter.ID); err != nil {
				t.Fatalf("change waiter owner to %s: %v", ownerType, err)
			}

			if err := store.DetectUnlockForNode(ctx, scenario.waiter.ID); err != nil {
				t.Fatalf("detect unlock: %v", err)
			}
			assertPendingUnlockCount(t, ctx, scenario.waiter.WorkspaceID, 0)
		})
	}
}

type unlockScenario struct {
	waiter        db.WorkNode
	prerequisites []db.Issue
}

func setupUnlockScenario(t *testing.T, ctx context.Context) (*Store, unlockScenario) {
	t.Helper()
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
	issueC := createWorkgraphIssue(t, ctx, workspaceID, agentID, 3, "Waiting issue C", "todo")
	insertWorkgraphDependency(t, ctx, issueC.ID, issueA.ID, issueDependencyBlockedBy)
	insertWorkgraphDependency(t, ctx, issueC.ID, issueB.ID, issueDependencyBlockedBy)

	store := NewStore(testPool)
	for _, issue := range []db.Issue{issueA, issueB, issueC} {
		if _, err := store.SyncIssueNode(ctx, issue); err != nil {
			t.Fatalf("sync issue %q: %v", issue.Title, err)
		}
	}
	if err := store.SyncDependenciesForIssue(ctx, workspaceID, issueC.ID); err != nil {
		t.Fatalf("sync dependencies: %v", err)
	}
	waiter, err := store.queries.GetWorkNodeByIssue(ctx, db.GetWorkNodeByIssueParams{
		WorkspaceID:   workspaceID,
		LinkedIssueID: issueC.ID,
	})
	if err != nil {
		t.Fatalf("load waiter node: %v", err)
	}
	return store, unlockScenario{
		waiter:        waiter,
		prerequisites: []db.Issue{issueA, issueB},
	}
}

func resolveUnlockPrerequisites(t *testing.T, ctx context.Context, store *Store, prerequisites []db.Issue) {
	t.Helper()
	for _, prerequisite := range prerequisites {
		prerequisite.Status = workNodeStatusDone
		if _, err := store.SyncIssueNode(ctx, prerequisite); err != nil {
			t.Fatalf("sync completed prerequisite %q: %v", prerequisite.Title, err)
		}
	}
}

func assertPendingUnlockCount(t *testing.T, ctx context.Context, workspaceID pgtype.UUID, want int) db.PendingHandoff {
	t.Helper()
	var count int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM pending_handoff
		WHERE workspace_id = $1
		  AND status IN ('pending', 'claimed')
		  AND reason_code = 'unlock'
	`, workspaceID).Scan(&count); err != nil {
		t.Fatalf("count pending unlock handoffs: %v", err)
	}
	if count != want {
		t.Fatalf("pending unlock handoffs = %d, want %d", count, want)
	}
	if want == 0 {
		return db.PendingHandoff{}
	}

	var handoff db.PendingHandoff
	if err := testPool.QueryRow(ctx, `
		SELECT target_actor_type, target_actor_id, urgency, reason_code, related_node_ids, dedupe_key
		FROM pending_handoff
		WHERE workspace_id = $1
		  AND status IN ('pending', 'claimed')
		  AND reason_code = 'unlock'
	`, workspaceID).Scan(
		&handoff.TargetActorType,
		&handoff.TargetActorID,
		&handoff.Urgency,
		&handoff.ReasonCode,
		&handoff.RelatedNodeIds,
		&handoff.DedupeKey,
	); err != nil {
		t.Fatalf("load pending unlock handoff: %v", err)
	}
	return handoff
}
