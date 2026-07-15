package handler

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/messageparts"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/internal/workgraph"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestWendyUnlockSilentParallelWait(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	fixture := seedWendyUnlockDispatchFixture(t)
	fixture.detectUnlock(t, fixture.issueC)
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
	fixture.markDone(t, fixture.issueA)
	fixture.detectUnlock(t, fixture.issueC)
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
	fixture.markDone(t, fixture.issueA)
	fixture.markDone(t, fixture.issueB)
	fixture.detectUnlock(t, fixture.issueC)
	assertWendyUnlockHandoffChannel(t, fixture.channelID)
	fixture.makeUnlockDue(t)

	if got := dispatchWendyUnlockHandoffsForTest(t); got != 1 {
		t.Fatalf("dispatched handoffs = %d, want 1", got)
	}
	assertWendyUnlockMessageCount(t, fixture.channelID, fixture.wendyID, 1)
	assertWendyUnlockMessageMentions(t, fixture.channelID, fixture.cID)
	assertWendyUnlockWakeCount(t, fixture.channelID, fixture.cID, 1)
	assertWendyUnlockNudge(t, fixture.nodeForIssue(t, fixture.issueC).ID.String())
}

func TestWendyUnlockThenDAfterCDone(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	fixture := seedWendyUnlockDispatchFixture(t)
	fixture.markDone(t, fixture.issueA)
	fixture.markDone(t, fixture.issueB)
	fixture.detectUnlock(t, fixture.issueC)
	assertWendyUnlockHandoffChannel(t, fixture.channelID)
	fixture.makeUnlockDue(t)
	if got := dispatchWendyUnlockHandoffsForTest(t); got != 1 {
		t.Fatalf("first dispatched handoffs = %d, want 1", got)
	}

	fixture.markDone(t, fixture.issueC)
	fixture.createWaitingD(t)
	fixture.detectUnlock(t, fixture.issueD)
	fixture.makeUnlockDue(t)
	if got := dispatchWendyUnlockHandoffsForTest(t); got != 1 {
		t.Fatalf("second dispatched handoffs = %d, want 1", got)
	}
	assertWendyUnlockMessageCount(t, fixture.channelID, fixture.wendyID, 2)
	assertWendyUnlockMessageMentions(t, fixture.channelID, fixture.dID)
	assertWendyUnlockWakeCount(t, fixture.channelID, fixture.dID, 1)
}

func TestWendyHumanReworkInterruptsActiveDownstream(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fixture := seedWendyUnlockDispatchFixture(t)
	fixture.createWaitingD(t)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE work_node
		SET status = 'active'
		WHERE workspace_id = $1 AND linked_issue_id IN ($2, $3)
	`, testWorkspaceID, fixture.issueC.ID, fixture.issueD.ID); err != nil {
		t.Fatalf("activate rework scenario nodes: %v", err)
	}

	previous := testHandler.WorkGraph
	testHandler.WorkGraph = fixture.store
	t.Cleanup(func() { testHandler.WorkGraph = previous })
	mentionStart, mentionEnd := 0, 2
	testHandler.ingestWendyHumanGroupMessage(context.Background(), ChannelResponse{
		ID: fixture.channelID, WorkspaceID: testWorkspaceID, Kind: "group",
	}, ChannelMessageResponse{
		ID:      "rework-" + uuid.NewString(),
		Content: "@C 这个不对，先修改返工",
		Parts: []protocol.MessagePart{{
			Type: protocol.MessagePartTypeReference, RefType: "mention", RefSubType: "agent", RefID: fixture.cID, Label: "@C",
			ContentStartUTF16: &mentionStart, ContentEndUTF16: &mentionEnd,
		}},
	})

	var cStatus string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM work_node WHERE linked_issue_id = $1`, fixture.issueC.ID).Scan(&cStatus); err != nil {
		t.Fatal(err)
	}
	if cStatus != "needs_rework" {
		t.Fatalf("C status = %q, want needs_rework", cStatus)
	}
	var interruptTarget, nudgeTarget string
	if err := testPool.QueryRow(context.Background(), `
		SELECT target_actor_id::text FROM pending_handoff
		WHERE workspace_id = $1 AND reason_code = 'interrupt_stop' AND status = 'pending'
	`, testWorkspaceID).Scan(&interruptTarget); err != nil {
		t.Fatalf("load interrupt handoff: %v", err)
	}
	if err := testPool.QueryRow(context.Background(), `
		SELECT target_actor_id::text FROM pending_handoff
		WHERE workspace_id = $1 AND reason_code = 'progress_nudge' AND status = 'pending'
	`, testWorkspaceID).Scan(&nudgeTarget); err != nil {
		t.Fatalf("load rework nudge: %v", err)
	}
	if interruptTarget != fixture.dID || nudgeTarget != fixture.cID {
		t.Fatalf("rework targets interrupt/nudge = %s/%s, want D/C", interruptTarget, nudgeTarget)
	}
}

func TestWendyDispatchMentionsMemberWithoutAgentWake(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fixture := seedWendyUnlockDispatchFixture(t)
	node := fixture.nodeForIssue(t, fixture.issueC)
	var memberID, memberName string
	if err := testPool.QueryRow(context.Background(), `
		SELECT m.id, COALESCE(NULLIF(u.display_name, ''), NULLIF(u.name, ''), '成员')
		FROM member m
		JOIN "user" u ON u.id = m.user_id
		WHERE m.workspace_id = $1 AND m.user_id = $2
	`, testWorkspaceID, testUserID).Scan(&memberID, &memberName); err != nil {
		t.Fatalf("load member target: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO pending_handoff (
			workspace_id, urgency, reason_code, target_actor_type, target_actor_id,
			related_node_ids, channel_id, issue_id, dedupe_key, not_before, status
		) VALUES ($1, 'fast', 'progress_nudge', 'member', $2, ARRAY[$3]::uuid[], $4, $5, $6, now() - interval '1 second', 'pending')
	`, testWorkspaceID, memberID, node.ID, fixture.channelID, fixture.issueC.ID, "member-dispatch:"+uuid.NewString()); err != nil {
		t.Fatalf("insert member handoff: %v", err)
	}
	fixture.bindWendy(t)

	if got := dispatchWendyUnlockHandoffsForTest(t); got != 1 {
		t.Fatalf("dispatched member handoffs = %d, want 1", got)
	}
	var status string
	if err := testPool.QueryRow(context.Background(), `
		SELECT status FROM pending_handoff WHERE workspace_id = $1 AND target_actor_id = $2 ORDER BY created_at DESC LIMIT 1
	`, testWorkspaceID, memberID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "done" {
		t.Fatalf("member handoff status = %q, want done", status)
	}
	var content string
	var rawParts []byte
	if err := testPool.QueryRow(context.Background(), `
		SELECT content, parts
		FROM channel_message
		WHERE channel_id = $1 AND author_type = 'agent' AND author_id = $2
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, fixture.channelID, fixture.wendyID).Scan(&content, &rawParts); err != nil {
		t.Fatalf("load Wendy member handoff message: %v", err)
	}
	parts := messageparts.Decode(rawParts)
	if len(parts) != 1 {
		t.Fatalf("member handoff parts = %+v, want one destination-scoped reference", parts)
	}
	label := directedAgentMentionLabel(memberName)
	start, end := contentUTF16Span(content, 0, len(label))
	part := parts[0]
	if part.Type != protocol.MessagePartTypeReference || part.RefType != "mention" || part.RefSubType != "member" || part.RefID != testUserID || part.Label != label || part.ContentStartUTF16 == nil || *part.ContentStartUTF16 != start || part.ContentEndUTF16 == nil || *part.ContentEndUTF16 != end {
		t.Fatalf("member handoff reference = %+v, want member/%s label %q span [%d,%d)", part, testUserID, label, start, end)
	}
	if err := validateWendyHandoffContent(content, parts, "member", testUserID); err != nil {
		t.Fatalf("validate persisted member handoff: %v", err)
	}
	foreignParts := append([]protocol.MessagePart(nil), parts...)
	foreignParts[0].RefID = uuid.NewString()
	if err := validateWendyHandoffContent(content, foreignParts, "member", testUserID); err == nil {
		t.Fatal("validate Wendy member handoff accepted a non-target member reference")
	}
	var wakeCount int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM agent_inbox_event WHERE channel_id = $1`, fixture.channelID).Scan(&wakeCount); err != nil {
		t.Fatal(err)
	}
	if wakeCount != 0 {
		t.Fatalf("member handoff created %d agent wakes, want 0", wakeCount)
	}
}

type wendyUnlockDispatchFixture struct {
	wendyID   string
	cID       string
	dID       string
	channelID string
	store     *workgraph.Store
	issueA    db.Issue
	issueB    db.Issue
	issueC    db.Issue
	issueD    db.Issue
}

func seedWendyUnlockDispatchFixture(t *testing.T) wendyUnlockDispatchFixture {
	t.Helper()
	ctx := context.Background()
	supervisor := createRadarSupervisorForExecutorTest(t)
	targetC := createHandlerTestAgent(t, "Wendy Unlock C "+uuid.NewString(), nil)
	targetD := createHandlerTestAgent(t, "Wendy Unlock D "+uuid.NewString(), nil)
	channelID := seedChannelForTest(t, "wendy-unlock-"+uuid.NewString(), testUserID)
	addRadarAgentMembersForExecutorTest(t, channelID, supervisor.ID.String(), targetC, targetD)

	bindWendySupervisorForHandoffTest(t, supervisor.ID.String())
	// Beckham owns group handoffs now: the channel's group manager is the speaker.
	bindChannelGroupManagerForTest(t, channelID, supervisor.ID.String())

	fixture := wendyUnlockDispatchFixture{
		wendyID:   supervisor.ID.String(),
		cID:       targetC,
		dID:       targetD,
		channelID: channelID,
		store:     workgraph.NewStore(testPool),
	}
	fixture.issueA = fixture.createIssue(t, "Prerequisite A", targetC)
	fixture.issueB = fixture.createIssue(t, "Prerequisite B", targetC)
	fixture.issueC = fixture.createIssue(t, "Waiting issue C", targetC)
	fixture.syncIssue(t, fixture.issueA)
	fixture.syncIssue(t, fixture.issueB)
	fixture.syncIssue(t, fixture.issueC)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO issue_dependency (issue_id, depends_on_issue_id, type)
		VALUES ($1, $2, 'blocked_by'), ($1, $3, 'blocked_by')
	`, fixture.issueC.ID, fixture.issueA.ID, fixture.issueB.ID); err != nil {
		t.Fatalf("create C dependencies: %v", err)
	}
	if err := fixture.store.SyncDependenciesForIssue(ctx, fixture.issueC.WorkspaceID, fixture.issueC.ID); err != nil {
		t.Fatalf("sync C dependencies: %v", err)
	}
	fixture.setPrimaryChannel(t, fixture.issueC)
	return fixture
}

// bindChannelGroupManagerForTest binds an agent as a channel's Beckham (group
// manager) — the per-channel role that now owns ambient review and handoffs.
func bindChannelGroupManagerForTest(t *testing.T, channelID, agentID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `UPDATE channel SET group_manager_agent_id = $2 WHERE id = $1`, channelID, agentID); err != nil {
		t.Fatalf("bind channel group manager: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE agent SET managed_role = 'group_manager' WHERE id = $1`, agentID); err != nil {
		t.Fatalf("mark group manager role: %v", err)
	}
}

func bindWendySupervisorForHandoffTest(t *testing.T, supervisorID string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO workspace_radar_state (
			workspace_id, supervisor_agent_id, enabled, next_due_at, change_version
		)
		VALUES (
			$1, $2, true, now(),
			COALESCE((
				SELECT max(change_version)
				FROM workspace_radar_change
				WHERE workspace_id = $1
			), 0)
		)
		ON CONFLICT (workspace_id) DO UPDATE
		SET supervisor_agent_id = EXCLUDED.supervisor_agent_id,
		    enabled = true,
		    next_due_at = now(),
		    change_version = GREATEST(
		        workspace_radar_state.change_version,
		        EXCLUDED.change_version
		    ),
		    updated_at = now()
	`, testWorkspaceID, supervisorID); err != nil {
		t.Fatalf("bind Wendy supervisor: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `
			DELETE FROM workspace_radar_state
			WHERE workspace_id = $1 AND supervisor_agent_id = $2
		`, testWorkspaceID, supervisorID)
	})
}

func (f *wendyUnlockDispatchFixture) createWaitingD(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	f.issueD = f.createIssue(t, "Waiting issue D", f.dID)
	f.syncIssue(t, f.issueD)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO issue_dependency (issue_id, depends_on_issue_id, type)
		VALUES ($1, $2, 'blocked_by')
	`, f.issueD.ID, f.issueC.ID); err != nil {
		t.Fatalf("create D dependency: %v", err)
	}
	if err := f.store.SyncDependenciesForIssue(ctx, f.issueD.WorkspaceID, f.issueD.ID); err != nil {
		t.Fatalf("sync D dependencies: %v", err)
	}
	f.setPrimaryChannel(t, f.issueD)
}

func (f wendyUnlockDispatchFixture) createIssue(t *testing.T, title, agentID string) db.Issue {
	t.Helper()
	ctx := context.Background()
	issue := db.Issue{
		ID:           pgUUIDForWendyTest(uuid.New()),
		WorkspaceID:  parseUUID(testWorkspaceID),
		Title:        title,
		Description:  pgTextForWendyTest(title + " description"),
		Status:       "todo",
		AssigneeType: pgTextForWendyTest("agent"),
		AssigneeID:   parseUUID(agentID),
	}
	var number int
	if err := testPool.QueryRow(ctx, `SELECT COALESCE(max(number), 0) + 1 FROM issue WHERE workspace_id = $1`, testWorkspaceID).Scan(&number); err != nil {
		t.Fatalf("allocate issue number: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO issue (
			id, workspace_id, number, title, description, status, priority,
			assignee_type, assignee_id, creator_type, creator_id, position
		) VALUES ($1, $2, $3, $4, $5, $6, 'none', 'agent', $7, 'agent', $7, 0)
	`, issue.ID, issue.WorkspaceID, number, issue.Title, issue.Description, issue.Status, issue.AssigneeID); err != nil {
		t.Fatalf("create %q: %v", title, err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issue.ID)
	})
	return issue
}

func pgUUIDForWendyTest(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

func pgTextForWendyTest(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: true}
}

func (f wendyUnlockDispatchFixture) syncIssue(t *testing.T, issue db.Issue) {
	t.Helper()
	if _, err := f.store.SyncIssueNode(context.Background(), issue); err != nil {
		t.Fatalf("sync issue %q: %v", issue.Title, err)
	}
}

func (f wendyUnlockDispatchFixture) markDone(t *testing.T, issue db.Issue) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `UPDATE issue SET status = 'done' WHERE id = $1`, issue.ID); err != nil {
		t.Fatalf("mark issue %q done: %v", issue.Title, err)
	}
	issue.Status = "done"
	f.syncIssue(t, issue)
}

func (f wendyUnlockDispatchFixture) detectUnlock(t *testing.T, issue db.Issue) {
	t.Helper()
	if err := f.store.DetectUnlockForNode(context.Background(), f.nodeForIssue(t, issue).ID); err != nil {
		t.Fatalf("detect unlock for %q: %v", issue.Title, err)
	}
}

func (f wendyUnlockDispatchFixture) nodeForIssue(t *testing.T, issue db.Issue) db.WorkNode {
	t.Helper()
	node, err := testHandler.Queries.GetWorkNodeByIssue(context.Background(), db.GetWorkNodeByIssueParams{
		WorkspaceID:   issue.WorkspaceID,
		LinkedIssueID: issue.ID,
	})
	if err != nil {
		t.Fatalf("load work node for %q: %v", issue.Title, err)
	}
	return node
}

func (f wendyUnlockDispatchFixture) setPrimaryChannel(t *testing.T, issue db.Issue) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `
		UPDATE work_node
		SET primary_channel_id = $1
		WHERE workspace_id = $2 AND linked_issue_id = $3
	`, f.channelID, issue.WorkspaceID, issue.ID); err != nil {
		t.Fatalf("set primary channel for %q: %v", issue.Title, err)
	}
}

func (f wendyUnlockDispatchFixture) makeUnlockDue(t *testing.T) {
	t.Helper()
	f.bindWendy(t)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE pending_handoff
		SET not_before = now() - interval '1 second'
		WHERE workspace_id = $1
		  AND urgency = 'fast'
		  AND reason_code = 'unlock'
		  AND status = 'pending'
	`, testWorkspaceID); err != nil {
		t.Fatalf("make unlock handoff due: %v", err)
	}
}

func (f wendyUnlockDispatchFixture) bindWendy(t *testing.T) {
	t.Helper()
	bindWendySupervisorForHandoffTest(t, f.wendyID)
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
	var rawParts []byte
	if err := testPool.QueryRow(context.Background(), `
		SELECT content, parts
		FROM channel_message
		WHERE channel_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, channelID).Scan(&content, &rawParts); err != nil {
		t.Fatalf("load Wendy unlock message: %v", err)
	}
	mentions := util.ParseMentionsFromContentAndParts(content, messageparts.Decode(rawParts))
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

func assertWendyUnlockHandoffChannel(t *testing.T, wantChannelID string) {
	t.Helper()
	var channelID, targetID pgtype.UUID
	var relatedNodeIDs []pgtype.UUID
	if err := testPool.QueryRow(context.Background(), `
		SELECT channel_id, target_actor_id, related_node_ids
		FROM pending_handoff
		WHERE workspace_id = $1
		  AND status = 'pending'
		  AND urgency = 'fast'
		  AND reason_code = 'unlock'
		ORDER BY created_at DESC
		LIMIT 1
	`, testWorkspaceID).Scan(&channelID, &targetID, &relatedNodeIDs); err != nil {
		t.Fatalf("load pending unlock handoff channel: %v", err)
	}
	if channelID.String() != wantChannelID {
		t.Fatalf("pending unlock channel = %s, want %s", channelID.String(), wantChannelID)
	}
	if !channelID.Valid || !targetID.Valid || len(relatedNodeIDs) == 0 {
		t.Fatalf("pending unlock target/channel/related = %+v/%+v/%d, want valid values", targetID, channelID, len(relatedNodeIDs))
	}
}
