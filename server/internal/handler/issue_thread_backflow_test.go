package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestIssueThreadBackflowWritesTargetedEventsWithoutWakingOtherAgents(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	creatorID := createHandlerTestAgent(t, "Issue Backflow Creator", nil)
	assigneeID := createHandlerTestAgent(t, "Issue Backflow Assignee", nil)
	unrelatedID := createHandlerTestAgent(t, "Issue Backflow Unrelated", nil)
	channelID := seedChannelForTest(t, "issue-thread-backflow-"+uuid.NewString(), testUserID)
	for _, agentID := range []string{creatorID, assigneeID, unrelatedID} {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
			VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
			t.Fatalf("add channel agent %s: %v", agentID, err)
		}
	}
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "Track this discussion as an issue", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("issue-backflow-root-"+uuid.NewString()), 0)
	if err != nil {
		t.Fatalf("insert source root: %v", err)
	}

	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, number, position)
		VALUES ($1, 'Issue backflow target', 'todo', 'none', 'agent', $2, $3, 0)
		RETURNING id`, testWorkspaceID, creatorID, 900000+int(uuid.New().ID()%100000)).Scan(&issueID); err != nil {
		t.Fatalf("create anchored issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID) })
	if _, err := testPool.Exec(ctx, `
		INSERT INTO issue_source_message (issue_id, workspace_id, channel_id, message_id)
		VALUES ($1, $2, $3, $4)`, issueID, testWorkspaceID, channelID, root.ID); err != nil {
		t.Fatalf("persist source anchor: %v", err)
	}

	updateIssueForBackflowTest(t, issueID, map[string]any{"assignee_type": "agent", "assignee_id": assigneeID})
	updateIssueForBackflowTest(t, issueID, map[string]any{"status": "in_progress"})
	updateIssueForBackflowTest(t, issueID, map[string]any{"status": "done"})

	events := loadIssueThreadBackflowEvents(t, channelID, root.ID)
	if len(events) != 3 {
		t.Fatalf("system event count = %d, want 3 (%+v)", len(events), events)
	}
	assertIssueThreadBackflowEvent(t, events[0], issueThreadAssignedEvent, assigneeID)
	assertIssueThreadBackflowEvent(t, events[1], issueThreadStatusChangedEvent, assigneeID)
	assertIssueThreadBackflowEvent(t, events[2], issueThreadCompletedEvent, creatorID)
	assertIssueThreadBackflowReference(t, events[0], issueID)
	assertIssueThreadBackflowReference(t, events[1], issueID)
	assertIssueThreadBackflowReference(t, events[2], issueID)

	// Directed issue-event wakes are channel-only inbox events with the
	// dedicated product reason (not residual channel chat "mention").
	for _, agentID := range []string{creatorID, assigneeID} {
		var count int
		if err := testPool.QueryRow(ctx, `
			SELECT count(*)
			FROM agent_inbox_event
			WHERE channel_id = $1
			  AND agent_id = $2
			  AND requires_wake = true
			  AND reason = $3`, channelID, agentID, protocol.AgentInboxReasonIssueThreadBackflow).Scan(&count); err != nil {
			t.Fatalf("count target agent wakes: %v", err)
		}
		if count == 0 {
			t.Fatalf("target agent %s was not woken by its directed issue event", agentID)
		}
	}
	var unrelatedWakes int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_inbox_event
		WHERE channel_id = $1
		  AND agent_id = $2
		  AND requires_wake = true`, channelID, unrelatedID).Scan(&unrelatedWakes); err != nil {
		t.Fatalf("count unrelated agent wakes: %v", err)
	}
	if unrelatedWakes != 0 {
		t.Fatalf("unrelated agent received %d issue-event wake(s), want 0", unrelatedWakes)
	}
	var reminders int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_reminder
		WHERE workspace_id = $1
		  AND anchor_channel_id = $2
		  AND agent_id IN ($3, $4)`,
		testWorkspaceID, channelID, creatorID, assigneeID).Scan(&reminders); err != nil {
		t.Fatalf("count reminders created by issue delivery: %v", err)
	}
	if reminders != 0 {
		t.Fatalf("issue delivery created %d reminder(s), want 0", reminders)
	}
}

func TestIssueThreadBackflowLeavesNonMembersUntargeted(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	creatorID := createHandlerTestAgent(t, "Issue Backflow External Creator", nil)
	assigneeID := createHandlerTestAgent(t, "Issue Backflow External Assignee", nil)
	channelID := seedChannelForTest(t, "issue-thread-backflow-external-"+uuid.NewString(), testUserID)
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "Track this discussion as an issue", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("issue-backflow-external-root-"+uuid.NewString()), 0)
	if err != nil {
		t.Fatalf("insert source root: %v", err)
	}

	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, number, position)
		VALUES ($1, 'Issue backflow external target', 'todo', 'none', 'agent', $2, $3, 0)
		RETURNING id`, testWorkspaceID, creatorID, 900000+int(uuid.New().ID()%100000)).Scan(&issueID); err != nil {
		t.Fatalf("create anchored issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID) })
	if _, err := testPool.Exec(ctx, `
		INSERT INTO issue_source_message (issue_id, workspace_id, channel_id, message_id)
		VALUES ($1, $2, $3, $4)`, issueID, testWorkspaceID, channelID, root.ID); err != nil {
		t.Fatalf("persist source anchor: %v", err)
	}

	updateIssueForBackflowTest(t, issueID, map[string]any{"assignee_type": "agent", "assignee_id": assigneeID})
	updateIssueForBackflowTest(t, issueID, map[string]any{"status": "in_progress"})
	updateIssueForBackflowTest(t, issueID, map[string]any{"status": "done"})

	events := loadIssueThreadBackflowEvents(t, channelID, root.ID)
	if len(events) != 3 {
		t.Fatalf("system event count = %d, want 3 (%+v)", len(events), events)
	}
	for _, event := range events {
		if event.Params.TargetID != "" || event.Params.TargetType != "" || event.Params.TargetHandle != "" || event.Params.TargetName != "" {
			t.Fatalf("non-member event retained a target: %#v", event.Params)
		}
		if len(event.Parts) != 2 || event.Parts[0].Type != protocol.MessagePartTypeSystemEvent {
			t.Fatalf("non-member event parts = %+v, want system event plus issue reference", event.Parts)
		}
		if strings.Contains(event.Content, "@") {
			t.Fatalf("non-member event content = %q, want no directed mention", event.Content)
		}
		assertIssueThreadBackflowReference(t, event, issueID)
	}
	for _, agentID := range []string{creatorID, assigneeID} {
		var count int
		if err := testPool.QueryRow(ctx, `
			SELECT count(*)
			FROM agent_inbox_event
			WHERE channel_id = $1
			  AND agent_id = $2
			  AND requires_wake = true`, channelID, agentID).Scan(&count); err != nil {
			t.Fatalf("count non-member agent wakes: %v", err)
		}
		if count != 0 {
			t.Fatalf("non-member agent %s received %d issue-event wake(s), want 0", agentID, count)
		}
	}
}

type issueThreadBackflowEventForTest struct {
	Content string
	Event   string
	Params  issueThreadSystemEventParams
	Parts   []protocol.MessagePart
}

func updateIssueForBackflowTest(t *testing.T, issueID string, body map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodPut, "/api/issues/"+issueID+"?workspace_id="+testWorkspaceID, body), "id", issueID)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue(%v) = %d: %s", body, w.Code, w.Body.String())
	}
}

func mustGetIssueForBackflowTest(t *testing.T, issueID string) db.Issue {
	t.Helper()
	issue, err := testHandler.Queries.GetIssue(context.Background(), parseUUID(issueID))
	if err != nil {
		t.Fatalf("GetIssue(%s): %v", issueID, err)
	}
	return issue
}

func loadIssueThreadBackflowEvents(t *testing.T, channelID, rootID string) []issueThreadBackflowEventForTest {
	t.Helper()
	rows, err := testPool.Query(context.Background(), `
		SELECT content, parts
		FROM channel_message
		WHERE channel_id = $1 AND thread_root_message_id = $2 AND author_type = 'system'
		ORDER BY seq`, channelID, rootID)
	if err != nil {
		t.Fatalf("load issue thread events: %v", err)
	}
	defer rows.Close()
	var out []issueThreadBackflowEventForTest
	for rows.Next() {
		var raw []byte
		var content string
		if err := rows.Scan(&content, &raw); err != nil {
			t.Fatalf("scan issue thread event: %v", err)
		}
		var parts []protocol.MessagePart
		if err := json.Unmarshal(raw, &parts); err != nil {
			t.Fatalf("decode issue thread parts: %v", err)
		}
		if len(parts) == 0 || parts[0].Type != protocol.MessagePartTypeSystemEvent {
			t.Fatalf("system event parts = %+v", parts)
		}
		var params issueThreadSystemEventParams
		if err := json.Unmarshal(parts[0].EventParams, &params); err != nil {
			t.Fatalf("decode issue event params: %v", err)
		}
		out = append(out, issueThreadBackflowEventForTest{Content: content, Event: parts[0].Event, Params: params, Parts: parts})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate issue thread events: %v", err)
	}
	return out
}

func loadIssueChannelBackflowEvents(t *testing.T, channelID string) []issueThreadBackflowEventForTest {
	t.Helper()
	rows, err := testPool.Query(context.Background(), `
		SELECT content, parts
		FROM channel_message
		WHERE channel_id = $1
		  AND thread_root_message_id IS NULL
		  AND author_type = 'system'
		ORDER BY seq`, channelID)
	if err != nil {
		t.Fatalf("load issue channel events: %v", err)
	}
	defer rows.Close()
	var out []issueThreadBackflowEventForTest
	for rows.Next() {
		var raw []byte
		var content string
		if err := rows.Scan(&content, &raw); err != nil {
			t.Fatalf("scan issue channel event: %v", err)
		}
		var parts []protocol.MessagePart
		if err := json.Unmarshal(raw, &parts); err != nil || len(parts) == 0 || parts[0].Type != protocol.MessagePartTypeSystemEvent {
			continue
		}
		var params issueThreadSystemEventParams
		if err := json.Unmarshal(parts[0].EventParams, &params); err != nil {
			t.Fatalf("decode issue event params: %v", err)
		}
		out = append(out, issueThreadBackflowEventForTest{Content: content, Event: parts[0].Event, Params: params, Parts: parts})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate issue channel events: %v", err)
	}
	return out
}

func assertIssueThreadBackflowEvent(t *testing.T, got issueThreadBackflowEventForTest, wantEvent, wantAgentID string) {
	t.Helper()
	if got.Event != wantEvent {
		t.Fatalf("event = %q, want %q", got.Event, wantEvent)
	}
	if got.Params.TargetID != wantAgentID || got.Params.TargetType != "agent" {
		t.Fatalf("event target = %#v, want agent %s", got.Params, wantAgentID)
	}
	if len(got.Parts) != 3 {
		t.Fatalf("parts = %+v, want system event, issue reference, and targeted mention", got.Parts)
	}
	mention := got.Parts[2]
	if mention.Type != protocol.MessagePartTypeReference || mention.RefType != "mention" || mention.RefSubType != "agent" || mention.RefID != wantAgentID {
		t.Fatalf("mention part = %+v, want targeted agent %s", mention, wantAgentID)
	}
}

func assertIssueThreadBackflowReference(t *testing.T, got issueThreadBackflowEventForTest, wantIssueID string) {
	t.Helper()
	var ref *protocol.MessagePart
	var label string
	for i := range got.Parts {
		part := &got.Parts[i]
		if part.Type != protocol.MessagePartTypeReference || part.RefType != "issue-ref" || part.RefSubType != "issue" {
			continue
		}
		if part.RefID == wantIssueID {
			ref = part
			label = part.Label
			break
		}
	}
	if ref == nil {
		t.Fatalf("parts = %+v, want anchored issue-ref for %s", got.Parts, wantIssueID)
	}
	if label == "" {
		t.Fatalf("issue reference part = %+v, want non-empty label", ref)
	}
	if ref.ContentStartUTF16 == nil || ref.ContentEndUTF16 == nil {
		t.Fatalf("issue reference span = %+v, want anchored utf16 range", ref)
	}
	if !strings.Contains(got.Content, label) {
		t.Fatalf("backflow content = %q, want issue identifier %q", got.Content, label)
	}
}

func TestIssueThreadParamsFromPartsRejectsAggregatedItemsWithoutTitle(t *testing.T) {
	raw := json.RawMessage(`{"issue_id":"i1","issue_identifier":"LRM-1","issue_status":"done","items":[{"issue_id":"i1","issue_identifier":"LRM-1","issue_status":"done"}]}`)
	_, ok := issueThreadParamsFromParts([]protocol.MessagePart{{
		Type:        protocol.MessagePartTypeSystemEvent,
		Event:       issueThreadCompletedEvent,
		EventParams: raw,
	}}, issueThreadCompletedEvent)
	if ok {
		t.Fatal("aggregated event without issue_title was accepted; want rejected so new rows do not silently merge into legacy payloads")
	}
}

func TestMergeIssueThreadAggregationParamsKeepsAllIssues(t *testing.T) {
	existing := issueThreadSystemEventParams{
		IssueID:         "a",
		IssueIdentifier: "LRM-1",
		IssueStatus:     "done",
		ActorID:         "actor-1",
		ActorType:       "agent",
		Items: []issueThreadSystemEventItem{{
			IssueID: "a", IssueIdentifier: "LRM-1", IssueTitle: "First issue", IssueStatus: "done",
		}},
	}
	incoming := issueThreadSystemEventParams{
		IssueID:         "b",
		IssueIdentifier: "LRM-2",
		IssueStatus:     "done",
		ActorID:         "actor-1",
		ActorType:       "agent",
		Items: []issueThreadSystemEventItem{{
			IssueID: "b", IssueIdentifier: "LRM-2", IssueTitle: "Second issue", IssueStatus: "done", PreviousStatus: "in_review",
		}},
	}
	merged := mergeIssueThreadAggregationParams(existing, incoming)
	if len(merged.Items) != 2 {
		t.Fatalf("items = %+v, want 2", merged.Items)
	}
	if merged.IssueID != "b" || merged.IssueIdentifier != "LRM-2" || merged.IssueTitle != "Second issue" {
		t.Fatalf("top-level = %#v, want latest issue b/LRM-2 with title", merged)
	}
	if merged.Items[0].IssueID != "a" || merged.Items[1].IssueID != "b" {
		t.Fatalf("item order = %+v, want a then b", merged.Items)
	}

	// Same issue_id replaces in place (no silent duplicate / drop).
	again := mergeIssueThreadAggregationParams(merged, issueThreadSystemEventParams{
		Items: []issueThreadSystemEventItem{{
			IssueID: "a", IssueIdentifier: "LRM-1", IssueTitle: "First issue", IssueStatus: "done", PreviousStatus: "todo",
		}},
	})
	if len(again.Items) != 2 {
		t.Fatalf("after replace items = %+v, want still 2", again.Items)
	}
	if again.Items[0].PreviousStatus != "todo" {
		t.Fatalf("replaced item = %+v, want previous_status=todo", again.Items[0])
	}
}

func TestIssueThreadBackflowAggregatesCrossIssueCompletedWithinWindow(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	channelID := seedChannelForTest(t, "issue-backflow-agg-"+uuid.NewString(), testUserID)

	createAnchoredIssue := func(title string) string {
		t.Helper()
		var issueID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, number, position)
			VALUES ($1, $2, 'in_review', 'none', 'member', $3, $4, 0)
			RETURNING id`, testWorkspaceID, title, testUserID, 900000+int(uuid.New().ID()%100000)).Scan(&issueID); err != nil {
			t.Fatalf("create issue %q: %v", title, err)
		}
		t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID) })
		if _, err := testPool.Exec(ctx, `
			INSERT INTO issue_source_message (issue_id, workspace_id, channel_id, message_id)
			VALUES ($1, $2, $3, NULL)`, issueID, testWorkspaceID, channelID); err != nil {
			t.Fatalf("anchor issue %s: %v", issueID, err)
		}
		return issueID
	}

	issueA := createAnchoredIssue("agg A")
	issueB := createAnchoredIssue("agg B")
	issueC := createAnchoredIssue("agg C")

	updateIssueForBackflowTest(t, issueA, map[string]any{"status": "done"})
	updateIssueForBackflowTest(t, issueB, map[string]any{"status": "done"})
	updateIssueForBackflowTest(t, issueC, map[string]any{"status": "done"})

	events := loadIssueChannelBackflowEvents(t, channelID)
	var completed []issueThreadBackflowEventForTest
	for _, event := range events {
		if event.Event == issueThreadCompletedEvent {
			completed = append(completed, event)
		}
	}
	if len(completed) != 1 {
		t.Fatalf("completed system rows = %d (%+v), want 1 aggregated bubble", len(completed), completed)
	}
	got := completed[0]
	if len(got.Params.Items) != 3 {
		t.Fatalf("aggregated items = %+v, want 3", got.Params.Items)
	}
	seen := map[string]bool{}
	for _, item := range got.Params.Items {
		seen[item.IssueID] = true
		if item.IssueStatus != "done" || item.IssueIdentifier == "" || item.IssueTitle == "" {
			t.Fatalf("item = %+v, want done + identifier + title", item)
		}
		if item.OccurredAt == "" {
			t.Fatalf("item = %+v, want occurred_at for FE expansion contract", item)
		}
	}
	for _, id := range []string{issueA, issueB, issueC} {
		if !seen[id] {
			t.Fatalf("aggregated items missing issue %s: %+v", id, got.Params.Items)
		}
		assertIssueThreadBackflowReference(t, got, id)
	}
	if got.Params.IssueID != issueC {
		t.Fatalf("top-level issue_id = %s, want latest %s", got.Params.IssueID, issueC)
	}
	if !strings.Contains(got.Content, "agg A") || !strings.Contains(got.Content, "completed") {
		t.Fatalf("content = %q, want title-first completed summary", got.Content)
	}
}

func TestIssueThreadBackflowAggregatesCrossIssueCreatedAndStartedWithinWindow(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	channelID := seedChannelForTest(t, "issue-backflow-created-started-agg-"+uuid.NewString(), testUserID)

	createAnchoredIssue := func(title, status string) string {
		t.Helper()
		var issueID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, number, position)
			VALUES ($1, $2, $3, 'none', 'member', $4, $5, 0)
			RETURNING id`, testWorkspaceID, title, status, testUserID, 900000+int(uuid.New().ID()%100000)).Scan(&issueID); err != nil {
			t.Fatalf("create issue %q: %v", title, err)
		}
		t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID) })
		if _, err := testPool.Exec(ctx, `
			INSERT INTO issue_source_message (issue_id, workspace_id, channel_id, message_id)
			VALUES ($1, $2, $3, NULL)`, issueID, testWorkspaceID, channelID); err != nil {
			t.Fatalf("anchor issue %s: %v", issueID, err)
		}
		return issueID
	}

	createdA := createAnchoredIssue("created agg A", "todo")
	createdB := createAnchoredIssue("created agg B", "todo")
	testHandler.emitIssueThreadBackflow(ctx, mustGetIssueForBackflowTest(t, createdA), "member", testUserID, issueThreadCreatedEvent, "", issueThreadBackflowTarget{})
	testHandler.emitIssueThreadBackflow(ctx, mustGetIssueForBackflowTest(t, createdB), "member", testUserID, issueThreadCreatedEvent, "", issueThreadBackflowTarget{})

	startedA := createAnchoredIssue("started agg A", "todo")
	startedB := createAnchoredIssue("started agg B", "todo")
	updateIssueForBackflowTest(t, startedA, map[string]any{"status": "in_progress"})
	updateIssueForBackflowTest(t, startedB, map[string]any{"status": "in_progress"})

	events := loadIssueChannelBackflowEvents(t, channelID)
	var created, started []issueThreadBackflowEventForTest
	for _, event := range events {
		switch event.Event {
		case issueThreadCreatedEvent:
			created = append(created, event)
		case issueThreadStatusChangedEvent:
			started = append(started, event)
		}
	}
	if len(created) != 1 {
		t.Fatalf("created system rows = %d (%+v), want 1 aggregated bubble", len(created), created)
	}
	if len(started) != 1 {
		t.Fatalf("started system rows = %d (%+v), want 1 aggregated bubble", len(started), started)
	}
	for _, group := range [][]issueThreadBackflowEventForTest{created, started} {
		got := group[0]
		if len(got.Params.Items) != 2 {
			t.Fatalf("%s items = %+v, want 2", got.Event, got.Params.Items)
		}
		for _, item := range got.Params.Items {
			if item.IssueTitle == "" || item.IssueIdentifier == "" || item.OccurredAt == "" {
				t.Fatalf("%s item = %+v, want title + identifier + occurred_at", got.Event, item)
			}
			assertIssueThreadBackflowReference(t, got, item.IssueID)
		}
	}
}

func TestIssueThreadBackflowDoesNotAggregateDifferentActorsOrTypes(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	otherUserID := createChannelPlainMember(t)
	channelID := seedChannelForTest(t, "issue-backflow-no-mix-"+uuid.NewString(), testUserID, otherUserID)

	createAnchoredIssue := func(title string) string {
		t.Helper()
		var issueID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, number, position)
			VALUES ($1, $2, 'todo', 'none', 'member', $3, $4, 0)
			RETURNING id`, testWorkspaceID, title, testUserID, 900000+int(uuid.New().ID()%100000)).Scan(&issueID); err != nil {
			t.Fatalf("create issue %q: %v", title, err)
		}
		t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID) })
		if _, err := testPool.Exec(ctx, `
			INSERT INTO issue_source_message (issue_id, workspace_id, channel_id, message_id)
			VALUES ($1, $2, $3, NULL)`, issueID, testWorkspaceID, channelID); err != nil {
			t.Fatalf("anchor issue %s: %v", issueID, err)
		}
		return issueID
	}

	issueA := createAnchoredIssue("no-mix A")
	issueB := createAnchoredIssue("no-mix B")
	issueC := createAnchoredIssue("no-mix C")

	// Same actor, different event types — must not merge.
	updateIssueForBackflowTest(t, issueA, map[string]any{"status": "in_progress"})
	updateIssueForBackflowTest(t, issueB, map[string]any{"status": "done"})

	// Different actor, same completed type — must not merge into B's bubble.
	w := httptest.NewRecorder()
	req := withURLParam(newRequestAs(otherUserID, http.MethodPut, "/api/issues/"+issueC+"?workspace_id="+testWorkspaceID, map[string]any{"status": "done"}), "id", issueC)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue as other user = %d: %s", w.Code, w.Body.String())
	}

	events := loadIssueChannelBackflowEvents(t, channelID)
	var statusChanged, completed int
	for _, event := range events {
		switch event.Event {
		case issueThreadStatusChangedEvent:
			statusChanged++
		case issueThreadCompletedEvent:
			completed++
		}
	}
	if statusChanged != 1 {
		t.Fatalf("status_changed rows = %d, want 1 (different type from completed)", statusChanged)
	}
	if completed != 2 {
		t.Fatalf("completed rows = %d, want 2 (different actors)", completed)
	}
}

func TestIssueThreadBackflowAggregatesAssigneeAcrossIssues(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	assigneeID := createHandlerTestAgent(t, "Agg Assignee", nil)
	channelID := seedChannelForTest(t, "issue-backflow-assign-agg-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, assigneeID); err != nil {
		t.Fatalf("add assignee member: %v", err)
	}

	createAnchoredIssue := func(title string) string {
		t.Helper()
		var issueID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, number, position)
			VALUES ($1, $2, 'todo', 'none', 'member', $3, $4, 0)
			RETURNING id`, testWorkspaceID, title, testUserID, 900000+int(uuid.New().ID()%100000)).Scan(&issueID); err != nil {
			t.Fatalf("create issue %q: %v", title, err)
		}
		t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID) })
		if _, err := testPool.Exec(ctx, `
			INSERT INTO issue_source_message (issue_id, workspace_id, channel_id, message_id)
			VALUES ($1, $2, $3, NULL)`, issueID, testWorkspaceID, channelID); err != nil {
			t.Fatalf("anchor issue %s: %v", issueID, err)
		}
		return issueID
	}

	issueA := createAnchoredIssue("assign agg A")
	issueB := createAnchoredIssue("assign agg B")
	updateIssueForBackflowTest(t, issueA, map[string]any{"assignee_type": "agent", "assignee_id": assigneeID})
	updateIssueForBackflowTest(t, issueB, map[string]any{"assignee_type": "agent", "assignee_id": assigneeID})

	var assigned []issueThreadBackflowEventForTest
	for _, event := range loadIssueChannelBackflowEvents(t, channelID) {
		if event.Event == issueThreadAssignedEvent {
			assigned = append(assigned, event)
		}
	}
	if len(assigned) != 1 {
		t.Fatalf("assigned system rows = %d, want 1 aggregated bubble", len(assigned))
	}
	if len(assigned[0].Params.Items) != 2 {
		t.Fatalf("assigned items = %+v, want 2", assigned[0].Params.Items)
	}
	for _, item := range assigned[0].Params.Items {
		if item.IssueTitle == "" {
			t.Fatalf("assigned item = %+v, want issue_title", item)
		}
	}
}

// LRM-638: an issue bound to a project must NOT have its system events fanned
// out to every group channel that shares the project. Only the direct-source
// channel (where the issue was anchored) should get the in-feed echo; sibling
// project channels must stay clean. Queryability (Activity / issue detail) is
// unaffected — this only restricts the in-feed projection.
func TestIssueThreadBackflowDoesNotLeakAcrossProjectChannels(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	// Project P with two sibling group channels.
	var projectID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title) VALUES ($1, $2) RETURNING id`,
		testWorkspaceID, "backflow-leak-proj-"+uuid.NewString()).Scan(&projectID); err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID) })

	channelA := seedChannelForTest(t, "backflow-leak-A-"+uuid.NewString(), testUserID)
	channelB := seedChannelForTest(t, "backflow-leak-B-"+uuid.NewString(), testUserID)
	for _, ch := range []string{channelA, channelB} {
		if _, err := testPool.Exec(ctx, `UPDATE channel SET project_id = $1 WHERE id = $2`, projectID, ch); err != nil {
			t.Fatalf("bind channel %s to project: %v", ch, err)
		}
	}

	// Issue anchored ONLY in channelA, bound to project P.
	issueNumber := 910000 + int(uuid.New().ID()%100000)
	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, number, position, project_id)
		VALUES ($1, $2, 'todo', 'none', 'member', $3, $4, 0, $5)
		RETURNING id`, testWorkspaceID, "leak guard issue", testUserID, issueNumber, projectID).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID) })
	if _, err := testPool.Exec(ctx, `
		INSERT INTO issue_source_message (issue_id, workspace_id, channel_id, message_id)
		VALUES ($1, $2, $3, NULL)`, issueID, testWorkspaceID, channelA); err != nil {
		t.Fatalf("anchor issue to channelA: %v", err)
	}

	testHandler.emitIssueThreadBackflow(ctx, mustGetIssueForBackflowTest(t, issueID), "member", testUserID, issueThreadCreatedEvent, "", issueThreadBackflowTarget{})

	// channelA (direct source) must see the created system row.
	aEvents := loadIssueChannelBackflowEvents(t, channelA)
	var aCreated int
	for _, e := range aEvents {
		if e.Event == issueThreadCreatedEvent && e.Params.IssueID == issueID {
			aCreated++
		}
	}
	if aCreated != 1 {
		t.Fatalf("channelA created events = %d (%+v), want 1", aCreated, aEvents)
	}

	// channelB (sibling project channel) must NOT see any system event for the
	// issue — this is the regression that previously leaked via the project
	// projection fan-out.
	bEvents := loadIssueChannelBackflowEvents(t, channelB)
	for _, e := range bEvents {
		if e.Params.IssueID == issueID {
			t.Fatalf("LRM-638 leak: channelB received issue %s event = %+v (project projection should be removed)", issueID, e)
		}
	}
}

// LRM-638 (end-to-end via the public API): drive issue transitions through the
// real UpdateIssue HTTP handler and assert that no system event lands in a
// sibling project channel. This complements the direct emitIssueThreadBackflow
// test above by exercising the same scope computation through the path Frank
// actually observed leaking (issue status/assignment events echoing into other
// groups' feeds). Queryability via Activity / issue detail is untouched.
func TestIssueThreadBackflowHTTPPathDoesNotLeakAcrossProjectChannels(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	// Project P shared by two sibling group channels. channelA anchors the issue;
	// channelB is the sibling that previously received the project projection.
	var projectID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title) VALUES ($1, $2) RETURNING id`,
		testWorkspaceID, "backflow-leak-http-proj-"+uuid.NewString()).Scan(&projectID); err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID) })

	channelA := seedChannelForTest(t, "backflow-leak-http-A-"+uuid.NewString(), testUserID)
	channelB := seedChannelForTest(t, "backflow-leak-http-B-"+uuid.NewString(), testUserID)
	for _, ch := range []string{channelA, channelB} {
		if _, err := testPool.Exec(ctx, `UPDATE channel SET project_id = $1 WHERE id = $2`, projectID, ch); err != nil {
			t.Fatalf("bind channel %s to project: %v", ch, err)
		}
	}

	// Real source root message in channelA so the thread-backflow path is live.
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(channelA), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "Track this discussion as an issue", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("backflow-leak-http-root-"+uuid.NewString()), 0)
	if err != nil {
		t.Fatalf("insert source root: %v", err)
	}

	assigneeID := createHandlerTestAgent(t, "Backflow Leak HTTP Assignee", nil)

	// Issue anchored ONLY in channelA, bound to project P (shared with channelB).
	issueNumber := 920000 + int(uuid.New().ID()%100000)
	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, number, position, project_id)
		VALUES ($1, $2, 'todo', 'none', 'member', $3, $4, 0, $5)
		RETURNING id`, testWorkspaceID, "leak guard http issue", testUserID, issueNumber, projectID).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID) })
	if _, err := testPool.Exec(ctx, `
		INSERT INTO issue_source_message (issue_id, workspace_id, channel_id, message_id)
		VALUES ($1, $2, $3, $4)`, issueID, testWorkspaceID, channelA, root.ID); err != nil {
		t.Fatalf("anchor issue to channelA: %v", err)
	}

	// Drive the transitions a user/PM would through the public API. Each
	// UpdateIssue call invokes emitIssueThreadBackflow internally.
	updateIssueForBackflowTest(t, issueID, map[string]any{"assignee_type": "agent", "assignee_id": assigneeID})
	updateIssueForBackflowTest(t, issueID, map[string]any{"status": "in_progress"})
	updateIssueForBackflowTest(t, issueID, map[string]any{"status": "done"})

	// channelA (direct source thread) must have accumulated the system rows.
	aThreadEvents := loadIssueThreadBackflowEvents(t, channelA, root.ID)
	if len(aThreadEvents) == 0 {
		t.Fatalf("channelA thread has no system events for issue %s", issueID)
	}
	for _, e := range aThreadEvents {
		if e.Params.IssueID != issueID {
			t.Fatalf("channelA thread event tied to wrong issue: %+v", e)
		}
	}

	// channelB (sibling project channel) must remain clean across every event
	// type and both projection surfaces (thread + channel timeline). This is the
	// LRM-638 regression guard through the real HTTP path.
	for _, e := range loadIssueThreadBackflowEvents(t, channelB, root.ID) {
		if e.Params.IssueID == issueID {
			t.Fatalf("LRM-638 HTTP leak: channelB thread received issue %s event = %+v", issueID, e)
		}
	}
	for _, e := range loadIssueChannelBackflowEvents(t, channelB) {
		if e.Params.IssueID == issueID {
			t.Fatalf("LRM-638 HTTP leak: channelB timeline received issue %s event = %+v", issueID, e)
		}
	}
}
