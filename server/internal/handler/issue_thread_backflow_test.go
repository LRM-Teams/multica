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
			VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
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

	for _, agentID := range []string{creatorID, assigneeID} {
		var count int
		if err := testPool.QueryRow(ctx, `SELECT count(*) FROM channel_agent_session WHERE channel_id = $1 AND agent_id = $2`, channelID, agentID).Scan(&count); err != nil {
			t.Fatalf("count target agent sessions: %v", err)
		}
		if count == 0 {
			t.Fatalf("target agent %s was not woken by its directed issue event", agentID)
		}
	}
	var unrelatedSessions int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM channel_agent_session WHERE channel_id = $1 AND agent_id = $2`, channelID, unrelatedID).Scan(&unrelatedSessions); err != nil {
		t.Fatalf("count unrelated agent sessions: %v", err)
	}
	if unrelatedSessions != 0 {
		t.Fatalf("unrelated agent received %d issue-event wake(s), want 0", unrelatedSessions)
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
		if err := testPool.QueryRow(ctx, `SELECT count(*) FROM channel_agent_session WHERE channel_id = $1 AND agent_id = $2`, channelID, agentID).Scan(&count); err != nil {
			t.Fatalf("count non-member agent sessions: %v", err)
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
	if len(got.Parts) < 2 {
		t.Fatalf("parts = %+v, want anchored issue reference", got.Parts)
	}
	ref := got.Parts[1]
	if ref.Type != protocol.MessagePartTypeReference || ref.RefType != "issue-ref" || ref.RefSubType != "issue" {
		t.Fatalf("issue reference part = %+v, want typed issue-ref", ref)
	}
	if ref.RefID != wantIssueID || ref.Label != got.Params.IssueIdentifier {
		t.Fatalf("issue reference part = %+v, want id=%s label=%s", ref, wantIssueID, got.Params.IssueIdentifier)
	}
	if ref.ContentStartUTF16 == nil || ref.ContentEndUTF16 == nil || *ref.ContentStartUTF16 != 0 || *ref.ContentEndUTF16 != len(got.Params.IssueIdentifier) {
		t.Fatalf("issue reference span = %+v, want exact leading identifier span [0,%d)", ref, len(got.Params.IssueIdentifier))
	}
	if !strings.HasPrefix(got.Content, got.Params.IssueIdentifier) {
		t.Fatalf("backflow content = %q, want issue identifier %q at anchored prefix", got.Content, got.Params.IssueIdentifier)
	}
}
