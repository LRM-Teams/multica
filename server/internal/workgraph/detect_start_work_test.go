package workgraph

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestDetectStartWorkEnqueuesForAssignedAgentWithoutWaits(t *testing.T) {
	ctx := t.Context()
	workspaceID := pgUUID(uuid.New())
	agentID := pgUUID(uuid.New())
	channelID := pgUUID(uuid.New())
	createWorkgraphWorkspace(t, ctx, workspaceID)
	t.Cleanup(func() {
		if _, err := testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID); err != nil {
			t.Errorf("clean up workspace: %v", err)
		}
	})
	createWorkgraphChannel(t, ctx, workspaceID, channelID)

	issue := createWorkgraphIssue(t, ctx, workspaceID, agentID, 1, "Continue development", "todo")
	store := NewStore(testPool)
	node, err := store.SyncIssueNode(ctx, issue)
	if err != nil {
		t.Fatalf("sync issue: %v", err)
	}
	if err := store.SetPrimaryChannel(ctx, node.ID, channelID); err != nil {
		t.Fatalf("set primary channel: %v", err)
	}

	if err := store.DetectStartWorkForNode(ctx, node.ID); err != nil {
		t.Fatalf("detect start work: %v", err)
	}
	handoff := assertPendingReasonCount(t, ctx, workspaceID, "start_work", 1)
	if handoff.TargetActorType != ownerTypeAgent || handoff.TargetActorID != agentID {
		t.Fatalf("target = (%q, %v), want agent %v", handoff.TargetActorType, handoff.TargetActorID, agentID)
	}
	if handoff.Urgency != "fast" {
		t.Fatalf("urgency = %q, want fast", handoff.Urgency)
	}
	if handoff.DedupeKey != "start_work:"+node.ID.String()+":"+agentID.String() {
		t.Fatalf("dedupe key = %q", handoff.DedupeKey)
	}
}

func TestDetectStartWorkSilentWhenUnresolvedWaits(t *testing.T) {
	ctx := t.Context()
	store, scenario := setupUnlockScenario(t, ctx)
	channelID := pgUUID(uuid.New())
	createWorkgraphChannel(t, ctx, scenario.waiter.WorkspaceID, channelID)
	if err := store.SetPrimaryChannel(ctx, scenario.waiter.ID, channelID); err != nil {
		t.Fatalf("set primary channel: %v", err)
	}

	if err := store.DetectStartWorkForNode(ctx, scenario.waiter.ID); err != nil {
		t.Fatalf("detect start work: %v", err)
	}
	assertPendingReasonCount(t, ctx, scenario.waiter.WorkspaceID, "start_work", 0)
}

func TestDetectStartWorkSilentWithoutChannel(t *testing.T) {
	ctx := t.Context()
	workspaceID := pgUUID(uuid.New())
	agentID := pgUUID(uuid.New())
	createWorkgraphWorkspace(t, ctx, workspaceID)
	t.Cleanup(func() {
		if _, err := testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID); err != nil {
			t.Errorf("clean up workspace: %v", err)
		}
	})

	issue := createWorkgraphIssue(t, ctx, workspaceID, agentID, 1, "No channel issue", "todo")
	store := NewStore(testPool)
	node, err := store.SyncIssueNode(ctx, issue)
	if err != nil {
		t.Fatalf("sync issue: %v", err)
	}
	if err := store.DetectStartWorkForNode(ctx, node.ID); err != nil {
		t.Fatalf("detect start work: %v", err)
	}
	assertPendingReasonCount(t, ctx, workspaceID, "start_work", 0)
}

func TestDetectStartWorkSilentForUnassigned(t *testing.T) {
	ctx := t.Context()
	workspaceID := pgUUID(uuid.New())
	agentID := pgUUID(uuid.New())
	channelID := pgUUID(uuid.New())
	createWorkgraphWorkspace(t, ctx, workspaceID)
	t.Cleanup(func() {
		if _, err := testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID); err != nil {
			t.Errorf("clean up workspace: %v", err)
		}
	})
	createWorkgraphChannel(t, ctx, workspaceID, channelID)

	issue := createWorkgraphIssue(t, ctx, workspaceID, agentID, 1, "Unassigned later", "todo")
	if _, err := testPool.Exec(ctx, `UPDATE issue SET assignee_type = NULL, assignee_id = NULL WHERE id = $1`, issue.ID); err != nil {
		t.Fatalf("clear assignee: %v", err)
	}
	issue.AssigneeType = pgtype.Text{}
	issue.AssigneeID = pgtype.UUID{}
	store := NewStore(testPool)
	node, err := store.SyncIssueNode(ctx, issue)
	if err != nil {
		t.Fatalf("sync issue: %v", err)
	}
	if err := store.SetPrimaryChannel(ctx, node.ID, channelID); err != nil {
		t.Fatalf("set primary channel: %v", err)
	}
	if err := store.DetectStartWorkForNode(ctx, node.ID); err != nil {
		t.Fatalf("detect start work: %v", err)
	}
	assertPendingReasonCount(t, ctx, workspaceID, "start_work", 0)
}

func TestDetectStartWorkDedupe(t *testing.T) {
	ctx := t.Context()
	workspaceID := pgUUID(uuid.New())
	agentID := pgUUID(uuid.New())
	channelID := pgUUID(uuid.New())
	createWorkgraphWorkspace(t, ctx, workspaceID)
	t.Cleanup(func() {
		if _, err := testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID); err != nil {
			t.Errorf("clean up workspace: %v", err)
		}
	})
	createWorkgraphChannel(t, ctx, workspaceID, channelID)

	issue := createWorkgraphIssue(t, ctx, workspaceID, agentID, 1, "Dedupe start", "todo")
	store := NewStore(testPool)
	node, err := store.SyncIssueNode(ctx, issue)
	if err != nil {
		t.Fatalf("sync issue: %v", err)
	}
	if err := store.SetPrimaryChannel(ctx, node.ID, channelID); err != nil {
		t.Fatalf("set primary channel: %v", err)
	}
	if err := store.DetectStartWorkForNode(ctx, node.ID); err != nil {
		t.Fatalf("first detect: %v", err)
	}
	if err := store.DetectStartWorkForNode(ctx, node.ID); err != nil {
		t.Fatalf("second detect: %v", err)
	}
	assertPendingReasonCount(t, ctx, workspaceID, "start_work", 1)
}

func createWorkgraphChannel(t *testing.T, ctx context.Context, workspaceID, channelID pgtype.UUID) {
	t.Helper()
	userID := pgUUID(uuid.New())
	if _, err := testPool.Exec(ctx, `
		INSERT INTO "user" (id, name, email)
		VALUES ($1, $2, $3)
	`, userID, "wg-user-"+uuid.NewString(), uuid.NewString()+"@example.com"); err != nil {
		t.Fatalf("create channel user: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel (id, workspace_id, name, kind, created_by)
		VALUES ($1, $2, $3, 'group', $4)
	`, channelID, workspaceID, "wg-channel-"+uuid.NewString(), userID); err != nil {
		t.Fatalf("create channel: %v", err)
	}
}

func assertPendingReasonCount(t *testing.T, ctx context.Context, workspaceID pgtype.UUID, reason string, want int) db.PendingHandoff {
	t.Helper()
	var count int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM pending_handoff
		WHERE workspace_id = $1
		  AND status IN ('pending', 'claimed')
		  AND reason_code = $2
	`, workspaceID, reason).Scan(&count); err != nil {
		t.Fatalf("count pending %s handoffs: %v", reason, err)
	}
	if count != want {
		t.Fatalf("pending %s handoffs = %d, want %d", reason, count, want)
	}
	if want == 0 {
		return db.PendingHandoff{}
	}
	var handoff db.PendingHandoff
	if err := testPool.QueryRow(ctx, `
		SELECT target_actor_type, target_actor_id, urgency, reason_code, related_node_ids, dedupe_key, channel_id
		FROM pending_handoff
		WHERE workspace_id = $1
		  AND status IN ('pending', 'claimed')
		  AND reason_code = $2
	`, workspaceID, reason).Scan(
		&handoff.TargetActorType,
		&handoff.TargetActorID,
		&handoff.Urgency,
		&handoff.ReasonCode,
		&handoff.RelatedNodeIds,
		&handoff.DedupeKey,
		&handoff.ChannelID,
	); err != nil {
		t.Fatalf("load pending %s handoff: %v", reason, err)
	}
	return handoff
}
