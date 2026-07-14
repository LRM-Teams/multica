package handler

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/util"
)

func TestWendyUnlockSilentParallelWait(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	fixture := seedWendyUnlockDispatchFixture(t)
	if got := dispatchWendyUnlockHandoffsForTest(t); got != 0 {
		t.Fatalf("dispatched handoffs = %d, want 0", got)
	}
	assertWendyUnlockMessageCount(t, fixture.channelID, fixture.wendyID, 0)
}

func TestWendyUnlockSilentPartialDone(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	fixture := seedWendyUnlockDispatchFixture(t)
	if got := dispatchWendyUnlockHandoffsForTest(t); got != 0 {
		t.Fatalf("dispatched handoffs = %d, want 0", got)
	}
	assertWendyUnlockMessageCount(t, fixture.channelID, fixture.wendyID, 0)
}

func TestWendyUnlockMentionsCWhenReady(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	fixture := seedWendyUnlockDispatchFixture(t)
	waiterNodeID := insertWendyUnlockHandoffForTest(t, fixture, fixture.cID, "Waiting issue C")

	if got := dispatchWendyUnlockHandoffsForTest(t); got != 1 {
		t.Fatalf("dispatched handoffs = %d, want 1", got)
	}
	assertWendyUnlockMessageCount(t, fixture.channelID, fixture.wendyID, 1)
	assertWendyUnlockMessageMentions(t, fixture.channelID, fixture.cID)
	assertWendyUnlockWakeCount(t, fixture.channelID, fixture.cID, 1)
	assertWendyUnlockNudge(t, waiterNodeID)
}

func TestWendyUnlockThenDAfterCDone(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	fixture := seedWendyUnlockDispatchFixture(t)
	insertWendyUnlockHandoffForTest(t, fixture, fixture.cID, "Waiting issue C")
	if got := dispatchWendyUnlockHandoffsForTest(t); got != 1 {
		t.Fatalf("first dispatched handoffs = %d, want 1", got)
	}

	insertWendyUnlockHandoffForTest(t, fixture, fixture.dID, "Waiting issue D")
	if got := dispatchWendyUnlockHandoffsForTest(t); got != 1 {
		t.Fatalf("second dispatched handoffs = %d, want 1", got)
	}
	assertWendyUnlockMessageCount(t, fixture.channelID, fixture.wendyID, 2)
	assertWendyUnlockMessageMentions(t, fixture.channelID, fixture.dID)
	assertWendyUnlockWakeCount(t, fixture.channelID, fixture.dID, 1)
}

type wendyUnlockDispatchFixture struct {
	wendyID   string
	cID       string
	dID       string
	channelID string
}

func seedWendyUnlockDispatchFixture(t *testing.T) wendyUnlockDispatchFixture {
	t.Helper()
	ctx := context.Background()
	supervisor := createRadarSupervisorForExecutorTest(t)
	targetC := createHandlerTestAgent(t, "Wendy Unlock C "+uuid.NewString(), nil)
	targetD := createHandlerTestAgent(t, "Wendy Unlock D "+uuid.NewString(), nil)
	channelID := seedChannelForTest(t, "wendy-unlock-"+uuid.NewString(), testUserID)
	addRadarAgentMembersForExecutorTest(t, channelID, supervisor.ID.String(), targetC, targetD)

	if _, err := testPool.Exec(ctx, `
		INSERT INTO workspace_radar_state (workspace_id, supervisor_agent_id, enabled, next_due_at)
		VALUES ($1, $2, true, now())
		ON CONFLICT (workspace_id) DO UPDATE
		SET supervisor_agent_id = EXCLUDED.supervisor_agent_id,
		    enabled = true,
		    updated_at = now()
	`, testWorkspaceID, supervisor.ID); err != nil {
		t.Fatalf("bind Wendy supervisor: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `
			DELETE FROM workspace_radar_state
			WHERE workspace_id = $1 AND supervisor_agent_id = $2
		`, testWorkspaceID, supervisor.ID)
	})

	return wendyUnlockDispatchFixture{
		wendyID:   supervisor.ID.String(),
		cID:       targetC,
		dID:       targetD,
		channelID: channelID,
	}
}

func insertWendyUnlockHandoffForTest(t *testing.T, fixture wendyUnlockDispatchFixture, targetID, title string) string {
	t.Helper()
	ctx := context.Background()
	var nodeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO work_node (
			workspace_id, kind, title, owner_type, owner_id, status, primary_channel_id
		)
		VALUES ($1, 'issue', $2, 'agent', $3, 'active', $4)
		RETURNING id
	`, testWorkspaceID, title, targetID, fixture.channelID).Scan(&nodeID); err != nil {
		t.Fatalf("create waiting work node: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM work_node WHERE id = $1`, nodeID)
	})
	if _, err := testPool.Exec(ctx, `
		INSERT INTO pending_handoff (
			workspace_id, urgency, reason_code, target_actor_type, target_actor_id,
			related_node_ids, channel_id, dedupe_key, not_before, status
		)
		VALUES ($1, 'fast', 'unlock', 'agent', $2, ARRAY[$3]::uuid[], $4, $5, now(), 'pending')
	`, testWorkspaceID, targetID, nodeID, fixture.channelID, "unlock-test:"+uuid.NewString()); err != nil {
		t.Fatalf("insert pending unlock handoff: %v", err)
	}
	return nodeID
}

func dispatchWendyUnlockHandoffsForTest(t *testing.T) int {
	t.Helper()
	got, err := testHandler.DispatchDueWendyHandoffs(context.Background(), 10)
	if err != nil {
		t.Fatalf("dispatch due Wendy handoffs: %v", err)
	}
	return got
}

func assertWendyUnlockMessageCount(t *testing.T, channelID, wendyID string, want int) {
	t.Helper()
	var count int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1 AND author_type = 'agent' AND author_id = $2
	`, channelID, wendyID).Scan(&count); err != nil {
		t.Fatalf("count Wendy unlock messages: %v", err)
	}
	if count != want {
		t.Fatalf("Wendy unlock messages = %d, want %d", count, want)
	}
}

func assertWendyUnlockWakeCount(t *testing.T, channelID, targetID string, want int) {
	t.Helper()
	var count int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM agent_inbox_event
		WHERE channel_id = $1 AND agent_id = $2 AND requires_wake
	`, channelID, targetID).Scan(&count); err != nil {
		t.Fatalf("count Wendy unlock wakes: %v", err)
	}
	if count != want {
		t.Fatalf("Wendy unlock wakes = %d, want %d", count, want)
	}
}

func assertWendyUnlockMessageMentions(t *testing.T, channelID, targetID string) {
	t.Helper()
	var content string
	if err := testPool.QueryRow(context.Background(), `
		SELECT content
		FROM channel_message
		WHERE channel_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, channelID).Scan(&content); err != nil {
		t.Fatalf("load Wendy unlock message: %v", err)
	}
	mentions := util.ParseMentions(content)
	if len(mentions) != 1 || mentions[0].Type != "agent" || mentions[0].ID != targetID {
		t.Fatalf("unlock message mentions = %+v, want only agent/%s", mentions, targetID)
	}
}

func assertWendyUnlockNudge(t *testing.T, nodeID string) {
	t.Helper()
	var nudgeKind string
	if err := testPool.QueryRow(context.Background(), `
		SELECT last_wendy_nudge_kind
		FROM work_node
		WHERE id = $1
	`, nodeID).Scan(&nudgeKind); err != nil {
		t.Fatalf("load Wendy nudge: %v", err)
	}
	if nudgeKind != "unlock" {
		t.Fatalf("Wendy nudge kind = %q, want unlock", nudgeKind)
	}
}
