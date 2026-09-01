package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestCreateIssueSourceMessageAnchorPersistsRootAndServesDetailRef(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	channelID := seedChannelForTest(t, "issue-source-anchor-"+uuid.NewString(), testUserID)
	rootContent := "Root discussion that should become the anchor @Barry"
	mentionStart := strings.Index(rootContent, "@Barry")
	mentionEnd := mentionStart + len("@Barry")
	mentionStartUTF16, mentionEndUTF16 := contentUTF16Span(rootContent, mentionStart, mentionEnd)
	rootParts, err := json.Marshal([]protocol.MessagePart{{
		Type: protocol.MessagePartTypeReference, RefType: "mention", RefSubType: "agent", RefID: "agent-barry", Label: "@Barry", ContentStartUTF16: &mentionStartUTF16, ContentEndUTF16: &mentionEndUTF16,
	}})
	if err != nil {
		t.Fatalf("marshal root parts: %v", err)
	}
	var rootID, replyID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, thread_id, trigger_depth)
		VALUES ($1, $2, 'user', $3, 'Source User', $4, $5::jsonb, 'multica', $6, 0)
		RETURNING id
	`, channelID, testWorkspaceID, testUserID, rootContent, rootParts, "issue-source-root-"+uuid.NewString()).Scan(&rootID); err != nil {
		t.Fatalf("seed source root: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content, source, thread_root_message_id, thread_id, trigger_depth)
		VALUES ($1, $2, 'user', $3, 'Source User', 'Reply used to ask the agent to create an issue', 'multica', $4, $5, 1)
		RETURNING id
	`, channelID, testWorkspaceID, testUserID, rootID, "issue-source-root-"+uuid.NewString()).Scan(&replyID); err != nil {
		t.Fatalf("seed source reply: %v", err)
	}

	create := httptest.NewRecorder()
	testHandler.CreateIssue(create, newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":  "Anchor an issue to the parent discussion",
		"status": "backlog",
		"source": map[string]string{
			"channel_id": channelID,
			"message_id": replyID,
		},
	}))
	if create.Code != http.StatusCreated {
		t.Fatalf("CreateIssue = %d: %s", create.Code, create.Body.String())
	}
	var created IssueResponse
	if err := json.NewDecoder(create.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, created.ID) })

	var storedChannelID, storedMessageID string
	if err := testPool.QueryRow(ctx, `
		SELECT channel_id::text, message_id::text FROM issue_source_message WHERE issue_id = $1`, created.ID,
	).Scan(&storedChannelID, &storedMessageID); err != nil {
		t.Fatalf("load persisted source anchor: %v", err)
	}
	if storedChannelID != channelID || storedMessageID != rootID {
		t.Fatalf("stored anchor = %s/%s, want source channel/root %s/%s", storedChannelID, storedMessageID, channelID, rootID)
	}
	backflow := loadIssueThreadBackflowEvents(t, channelID, rootID)
	if len(backflow) != 1 {
		t.Fatalf("created issue backflow count = %d, want 1 (%+v)", len(backflow), backflow)
	}
	if backflow[0].Event != issueThreadCreatedEvent {
		t.Fatalf("created issue backflow event = %q, want %q", backflow[0].Event, issueThreadCreatedEvent)
	}
	if backflow[0].Params.ActorType != "human" || backflow[0].Params.ActorID != testUserID {
		t.Fatalf("created issue backflow actor = %#v, want current human %s", backflow[0].Params, testUserID)
	}
	assertIssueThreadBackflowReference(t, backflow[0], created.ID)

	detail := httptest.NewRecorder()
	detailReq := newRequest("GET", "/api/issues/"+created.ID+"?workspace_id="+testWorkspaceID, nil)
	detailReq = withURLParam(detailReq, "id", created.ID)
	testHandler.GetIssue(detail, detailReq)
	if detail.Code != http.StatusOK {
		t.Fatalf("GetIssue = %d: %s", detail.Code, detail.Body.String())
	}
	var got IssueResponse
	if err := json.NewDecoder(detail.Body).Decode(&got); err != nil {
		t.Fatalf("decode detail response: %v", err)
	}
	if got.SourceRefs == nil || got.SourceRefs.Message == nil {
		t.Fatalf("source_refs.message missing from issue detail: %s", detail.Body.String())
	}
	ref := got.SourceRefs.Message
	if ref.ChannelID != channelID || ref.MessageID != rootID || ref.ThreadRootMessageID != rootID {
		t.Fatalf("detail source ref = %#v, want channel/root %s/%s", ref, channelID, rootID)
	}
	if ref.Excerpt != rootContent {
		t.Fatalf("source excerpt = %q", ref.Excerpt)
	}
	if len(ref.ExcerptParts) != 1 || ref.ExcerptParts[0].RefID != "agent-barry" {
		t.Fatalf("source excerpt_parts = %#v, want anchored @Barry reference", ref.ExcerptParts)
	}
}

func TestCreateIssueWithGroupChannelDoesNotInferProjectAndCanClearAnchor(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	channelID := seedChannelForTest(t, "issue-channel-anchor-"+uuid.NewString(), testUserID)
	replacementChannelID := seedChannelForTest(t, "issue-channel-anchor-replacement-"+uuid.NewString(), testUserID)
	var projectID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title) VALUES ($1, $2) RETURNING id`,
		testWorkspaceID, "Source channel project "+uuid.NewString(),
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID) })
	bindChannelProjectStrings(t, ctx, channelID, projectID)

	create := httptest.NewRecorder()
	testHandler.CreateIssue(create, newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":      "Group-only issue anchor",
		"status":     "backlog",
		"channel_id": channelID,
	}))
	if create.Code != http.StatusCreated {
		t.Fatalf("CreateIssue = %d: %s", create.Code, create.Body.String())
	}
	var created IssueResponse
	if err := json.NewDecoder(create.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, created.ID) })
	if created.ProjectID != nil {
		t.Fatalf("project_id = %v, want nil without an explicit project", created.ProjectID)
	}

	withBoth := httptest.NewRecorder()
	testHandler.CreateIssue(withBoth, newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":      "Explicit group and project issue",
		"status":     "backlog",
		"channel_id": channelID,
		"project_id": projectID,
	}))
	if withBoth.Code != http.StatusCreated {
		t.Fatalf("CreateIssue with channel and project = %d: %s", withBoth.Code, withBoth.Body.String())
	}
	var bothCreated IssueResponse
	if err := json.NewDecoder(withBoth.Body).Decode(&bothCreated); err != nil {
		t.Fatalf("decode explicit create response: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, bothCreated.ID) })
	if bothCreated.ProjectID == nil || *bothCreated.ProjectID != projectID {
		t.Fatalf("explicit project_id = %v, want %s", bothCreated.ProjectID, projectID)
	}
	var bothAnchorChannelID string
	if err := testPool.QueryRow(ctx, `SELECT channel_id::text FROM issue_source_message WHERE issue_id = $1`, bothCreated.ID).Scan(&bothAnchorChannelID); err != nil {
		t.Fatalf("load explicit channel anchor: %v", err)
	}
	if bothAnchorChannelID != channelID {
		t.Fatalf("explicit channel anchor = %s, want %s", bothAnchorChannelID, channelID)
	}
	assertIssueChannelEventCount(t, channelID, bothCreated.ID, issueThreadCreatedEvent, 1)

	projectOnly := httptest.NewRecorder()
	testHandler.CreateIssue(projectOnly, newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":      "Project-only issue",
		"status":     "backlog",
		"project_id": projectID,
	}))
	if projectOnly.Code != http.StatusCreated {
		t.Fatalf("CreateIssue with project only = %d: %s", projectOnly.Code, projectOnly.Body.String())
	}
	var projectOnlyCreated IssueResponse
	if err := json.NewDecoder(projectOnly.Body).Decode(&projectOnlyCreated); err != nil {
		t.Fatalf("decode project-only create response: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, projectOnlyCreated.ID) })
	if projectOnlyCreated.ProjectID == nil || *projectOnlyCreated.ProjectID != projectID {
		t.Fatalf("project-only project_id = %v, want %s", projectOnlyCreated.ProjectID, projectID)
	}
	var projectOnlyAnchorCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM issue_source_message WHERE issue_id = $1`, projectOnlyCreated.ID).Scan(&projectOnlyAnchorCount); err != nil {
		t.Fatalf("load project-only anchor: %v", err)
	}
	if projectOnlyAnchorCount != 0 {
		t.Fatalf("project-only anchor count = %d, want 0", projectOnlyAnchorCount)
	}
	// `created` is the group-only issue anchored directly in channelID, so its
	// created event still lands in that channel (direct source is unchanged).
	assertIssueChannelEvent(t, channelID, created.ID, issueThreadCreatedEvent)
	// LRM-638: the project-projection fan-out was removed. projectOnlyCreated
	// has NO direct-source channel anchor, so its system events must NOT echo
	// into any channel feed — not even the project-bound group channel. The
	// issue stays queryable via issue detail / Activity; only the in-feed
	// projection is gone.
	assertIssueChannelEventCount(t, channelID, projectOnlyCreated.ID, issueThreadCreatedEvent, 0)
	updateIssueForBackflowTest(t, projectOnlyCreated.ID, map[string]any{"status": "in_progress"})
	assertIssueChannelEventCount(t, channelID, projectOnlyCreated.ID, issueThreadStatusChangedEvent, 0)

	var storedChannelID string
	var storedMessageID *string
	if err := testPool.QueryRow(ctx, `
		SELECT channel_id::text, message_id::text FROM issue_source_message WHERE issue_id = $1`, created.ID,
	).Scan(&storedChannelID, &storedMessageID); err != nil {
		t.Fatalf("load group-only anchor: %v", err)
	}
	if storedChannelID != channelID || storedMessageID != nil {
		t.Fatalf("stored group anchor = %s/%v, want %s/<nil>", storedChannelID, storedMessageID, channelID)
	}

	detail := httptest.NewRecorder()
	detailReq := newRequest("GET", "/api/issues/"+created.ID+"?workspace_id="+testWorkspaceID, nil)
	detailReq = withURLParam(detailReq, "id", created.ID)
	testHandler.GetIssue(detail, detailReq)
	if detail.Code != http.StatusOK {
		t.Fatalf("GetIssue = %d: %s", detail.Code, detail.Body.String())
	}
	var got IssueResponse
	if err := json.NewDecoder(detail.Body).Decode(&got); err != nil {
		t.Fatalf("decode detail response: %v", err)
	}
	if got.SourceRefs == nil || got.SourceRefs.Channel == nil || got.SourceRefs.Channel.ChannelID != channelID || got.SourceRefs.Message != nil {
		t.Fatalf("source_refs = %#v, want group-only channel", got.SourceRefs)
	}
	if got.Channel == nil || got.Channel.ChannelID != channelID {
		t.Fatalf("channel = %#v, want top-level associated group %s", got.Channel, channelID)
	}

	replace := httptest.NewRecorder()
	replaceReq := newRequest("PUT", "/api/issues/"+created.ID+"/channel?workspace_id="+testWorkspaceID, map[string]any{"channel_id": replacementChannelID})
	replaceReq = withURLParam(replaceReq, "id", created.ID)
	testHandler.SetIssueSourceChannel(replace, replaceReq)
	if replace.Code != http.StatusOK {
		t.Fatalf("SetIssueSourceChannel replace = %d: %s", replace.Code, replace.Body.String())
	}
	if err := testPool.QueryRow(ctx, `SELECT channel_id::text FROM issue_source_message WHERE issue_id = $1`, created.ID).Scan(&storedChannelID); err != nil {
		t.Fatalf("load replaced anchor: %v", err)
	}
	if storedChannelID != replacementChannelID {
		t.Fatalf("replaced channel_id = %s, want %s", storedChannelID, replacementChannelID)
	}

	missing := httptest.NewRecorder()
	missingReq := newRequest("PUT", "/api/issues/"+created.ID+"/channel?workspace_id="+testWorkspaceID, map[string]any{})
	missingReq = withURLParam(missingReq, "id", created.ID)
	testHandler.SetIssueSourceChannel(missing, missingReq)
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("SetIssueSourceChannel missing channel_id = %d: %s", missing.Code, missing.Body.String())
	}
	if err := testPool.QueryRow(ctx, `SELECT channel_id::text FROM issue_source_message WHERE issue_id = $1`, created.ID).Scan(&storedChannelID); err != nil {
		t.Fatalf("load anchor after missing update: %v", err)
	}
	if storedChannelID != replacementChannelID {
		t.Fatalf("channel_id after missing update = %s, want %s", storedChannelID, replacementChannelID)
	}

	clear := httptest.NewRecorder()
	clearReq := newRequest("PUT", "/api/issues/"+created.ID+"/channel?workspace_id="+testWorkspaceID, map[string]any{"channel_id": ""})
	clearReq = withURLParam(clearReq, "id", created.ID)
	testHandler.SetIssueSourceChannel(clear, clearReq)
	if clear.Code != http.StatusOK {
		t.Fatalf("SetIssueSourceChannel empty clear = %d: %s", clear.Code, clear.Body.String())
	}
	var count int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM issue_source_message WHERE issue_id = $1`, created.ID).Scan(&count); err != nil {
		t.Fatalf("count cleared anchors: %v", err)
	}
	if count != 0 {
		t.Fatalf("anchor count = %d, want 0 after clear", count)
	}

	reset := httptest.NewRecorder()
	resetReq := newRequest("PUT", "/api/issues/"+created.ID+"/channel?workspace_id="+testWorkspaceID, map[string]any{"channel_id": replacementChannelID})
	resetReq = withURLParam(resetReq, "id", created.ID)
	testHandler.SetIssueSourceChannel(reset, resetReq)
	if reset.Code != http.StatusOK {
		t.Fatalf("SetIssueSourceChannel reset = %d: %s", reset.Code, reset.Body.String())
	}

	nullClear := httptest.NewRecorder()
	nullClearReq := newRequest("PUT", "/api/issues/"+created.ID+"/channel?workspace_id="+testWorkspaceID, map[string]any{"channel_id": nil})
	nullClearReq = withURLParam(nullClearReq, "id", created.ID)
	testHandler.SetIssueSourceChannel(nullClear, nullClearReq)
	if nullClear.Code != http.StatusOK {
		t.Fatalf("SetIssueSourceChannel null clear = %d: %s", nullClear.Code, nullClear.Body.String())
	}
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM issue_source_message WHERE issue_id = $1`, created.ID).Scan(&count); err != nil {
		t.Fatalf("count null-cleared anchors: %v", err)
	}
	if count != 0 {
		t.Fatalf("anchor count = %d, want 0 after null clear", count)
	}
}

func assertIssueChannelEvent(t *testing.T, channelID, issueID, wantEvent string) {
	t.Helper()
	for _, event := range loadIssueChannelBackflowEvents(t, channelID) {
		if event.Event == wantEvent && issueThreadEventContainsIssue(event, issueID) {
			assertIssueThreadBackflowReference(t, event, issueID)
			return
		}
	}
	t.Fatalf("channel %s has no %s system event for issue %s", channelID, wantEvent, issueID)
}

func assertIssueChannelEventCount(t *testing.T, channelID, issueID, wantEvent string, wantCount int) {
	t.Helper()
	count := 0
	for _, event := range loadIssueChannelBackflowEvents(t, channelID) {
		if event.Event == wantEvent && issueThreadEventContainsIssue(event, issueID) {
			count++
		}
	}
	if count != wantCount {
		t.Fatalf("channel %s has %d %s system events for issue %s, want %d", channelID, count, wantEvent, issueID, wantCount)
	}
}

func issueThreadEventContainsIssue(event issueThreadBackflowEventForTest, issueID string) bool {
	if event.Params.IssueID == issueID {
		return true
	}
	for _, item := range event.Params.Items {
		if item.IssueID == issueID {
			return true
		}
	}
	return false
}

func TestIssueSourceChannelAgentTaskTokenSeesOwnAnchorAndPublishesAgentActor(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "issue-source-anchor-agent-"+uuid.NewString(), nil)
	// The host user owns the issue but is deliberately not a member of this
	// private group. Only the agent is a group member, which makes the actor
	// boundary observable in both the response and issue:updated event.
	channelID := seedChannelForTest(t, "issue-source-anchor-agent-group-"+uuid.NewString())
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent channel member: %v", err)
	}

	create := httptest.NewRecorder()
	testHandler.CreateIssue(create, newRequest(http.MethodPost, "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":  "Agent-owned group anchor",
		"status": "backlog",
	}))
	if create.Code != http.StatusCreated {
		t.Fatalf("CreateIssue = %d: %s", create.Code, create.Body.String())
	}
	var issue IssueResponse
	if err := json.NewDecoder(create.Body).Decode(&issue); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issue.ID) })

	var updated *events.Event
	testHandler.Bus.Subscribe(protocol.EventIssueUpdated, func(event events.Event) {
		if event.ActorType == "agent" && event.ActorID == agentID {
			copy := event
			updated = &copy
		}
	})

	setReq := newRequest(http.MethodPut, "/api/issues/"+issue.ID+"/channel?workspace_id="+testWorkspaceID, map[string]any{"channel_id": channelID})
	setReq = withURLParam(setReq, "id", issue.ID)
	setReq.Header.Set("X-Actor-Source", "task_token")
	setReq.Header.Set("X-Agent-ID", agentID)
	set := httptest.NewRecorder()
	testHandler.SetIssueSourceChannel(set, setReq)
	if set.Code != http.StatusOK {
		t.Fatalf("SetIssueSourceChannel as task-token agent = %d: %s", set.Code, set.Body.String())
	}
	var setResponse IssueResponse
	if err := json.NewDecoder(set.Body).Decode(&setResponse); err != nil {
		t.Fatalf("decode agent set response: %v", err)
	}
	if setResponse.SourceRefs == nil || setResponse.SourceRefs.Channel == nil || setResponse.SourceRefs.Channel.ChannelID != channelID {
		t.Fatalf("agent set source_refs = %#v, want private group %s", setResponse.SourceRefs, channelID)
	}
	if setResponse.Channel == nil || setResponse.Channel.ChannelID != channelID {
		t.Fatalf("agent set channel = %#v, want top-level associated group %s", setResponse.Channel, channelID)
	}
	if updated == nil || updated.ActorType != "agent" || updated.ActorID != agentID {
		t.Fatalf("issue:updated actor = %#v, want agent/%s", updated, agentID)
	}

	getReq := newRequest(http.MethodGet, "/api/issues/"+issue.ID+"?workspace_id="+testWorkspaceID, nil)
	getReq = withURLParam(getReq, "id", issue.ID)
	getReq.Header.Set("X-Actor-Source", "task_token")
	getReq.Header.Set("X-Agent-ID", agentID)
	get := httptest.NewRecorder()
	testHandler.GetIssue(get, getReq)
	if get.Code != http.StatusOK {
		t.Fatalf("GetIssue as task-token agent = %d: %s", get.Code, get.Body.String())
	}
	var getResponse IssueResponse
	if err := json.NewDecoder(get.Body).Decode(&getResponse); err != nil {
		t.Fatalf("decode agent get response: %v", err)
	}
	if getResponse.SourceRefs == nil || getResponse.SourceRefs.Channel == nil || getResponse.SourceRefs.Channel.ChannelID != channelID {
		t.Fatalf("agent get source_refs = %#v, want private group %s", getResponse.SourceRefs, channelID)
	}
	if getResponse.Channel == nil || getResponse.Channel.ChannelID != channelID {
		t.Fatalf("agent get channel = %#v, want top-level associated group %s", getResponse.Channel, channelID)
	}
}

func TestSourceExcerptReferencePartsKeepsOnlyWholeVisibleAnchors(t *testing.T) {
	content := "@Barry " + strings.Repeat("a", channelHistoryMessageMaxChars-9) + "@Barry"
	firstStart := strings.Index(content, "@Barry")
	firstEnd := firstStart + len("@Barry")
	lastStart := strings.LastIndex(content, "@Barry")
	lastEnd := lastStart + len("@Barry")
	firstStartUTF16, firstEndUTF16 := contentUTF16Span(content, firstStart, firstEnd)
	lastStartUTF16, lastEndUTF16 := contentUTF16Span(content, lastStart, lastEnd)

	parts := []protocol.MessagePart{
		{Type: protocol.MessagePartTypeReference, RefType: "mention", RefSubType: "agent", RefID: "first", Label: "@Barry", ContentStartUTF16: &firstStartUTF16, ContentEndUTF16: &firstEndUTF16},
		{Type: protocol.MessagePartTypeReference, RefType: "mention", RefSubType: "agent", RefID: "cut", Label: "@Barry", ContentStartUTF16: &lastStartUTF16, ContentEndUTF16: &lastEndUTF16},
	}
	excerpt := truncateChannelHistoryContent(content)
	got := sourceExcerptReferenceParts(content, excerpt, parts)
	if len(got) != 1 || got[0].RefID != "first" {
		t.Fatalf("excerpt references = %#v, want only intact first reference", got)
	}
	if got[0].ContentStartUTF16 == nil || *got[0].ContentStartUTF16 != firstStartUTF16 {
		t.Fatalf("excerpt reference start = %#v, want %d", got[0].ContentStartUTF16, firstStartUTF16)
	}
}

func TestSourceExcerptReferencePartsSkipsNormalizedContent(t *testing.T) {
	start, end := 0, 6
	parts := []protocol.MessagePart{{
		Type: protocol.MessagePartTypeReference, RefType: "mention", RefSubType: "agent", RefID: "agent-1", Label: "@Barry", ContentStartUTF16: &start, ContentEndUTF16: &end,
	}}
	if got := sourceExcerptReferenceParts("@Barry\r\nhello", "@Barry\nhello", parts); len(got) != 0 {
		t.Fatalf("normalized content references = %#v, want none", got)
	}
}
