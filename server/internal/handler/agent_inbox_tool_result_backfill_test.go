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
)

// Task #103: Cursor often emits tool args only on completed. The daemon
// carries that Input on tool_result; Activity must backfill the started
// tool_call row by call_id without overwriting facts already present.
func TestReportAgentInboxMessages_BackfillsToolCallFactsFromToolResult(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentName := "Inbox Tool Backfill Agent " + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentName, nil)
	runtimeID := handlerTestRuntimeID(t)
	channelID := seedChannelForTest(t, "agent-inbox-tool-backfill-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
		ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "[@"+agentName+"](mention://agent/"+agentID+") write and search", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("inbox-tool-backfill"), 0)
	if err != nil {
		t.Fatalf("insert mention trigger: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, trigger, parseUUID(testUserID))

	drainReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "agent-inbox-tool-backfill-daemon")
	drainReq = withURLParam(drainReq, "runtimeId", runtimeID)
	drainRec := httptest.NewRecorder()
	testHandler.DrainAgentInboxByRuntime(drainRec, drainReq)
	if drainRec.Code != http.StatusOK {
		t.Fatalf("drain inbox: status=%d body=%s", drainRec.Code, drainRec.Body.String())
	}
	var drainResp DrainAgentInboxResponse
	if err := json.Unmarshal(drainRec.Body.Bytes(), &drainResp); err != nil {
		t.Fatalf("decode drain response: %v", err)
	}
	if len(drainResp.Events) != 1 {
		t.Fatalf("drain returned %d events, want 1: %s", len(drainResp.Events), drainRec.Body.String())
	}
	got := drainResp.Events[0]

	messagesReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+got.ID+"/messages", ReportAgentInboxMessagesRequest{
		DeliveryID: got.DeliveryID,
		LeaseToken: got.LeaseToken,
		Messages: []TaskMessageRequest{
			// write_file: started empty, completed carries path (Cursor shape)
			{Seq: 1, Type: "tool_use", Tool: "write_file", CallID: "call-write-1", Input: map[string]any{}},
			{Seq: 2, Type: "tool_result", Tool: "write_file", CallID: "call-write-1", Input: map[string]any{"path": "/tmp/cursor-write.txt"}, Output: "ok"},
			// search_files → glob: started empty, completed carries pattern
			{Seq: 3, Type: "tool_use", Tool: "search_files", CallID: "call-search-1", Input: map[string]any{}},
			{Seq: 4, Type: "tool_result", Tool: "search_files", CallID: "call-search-1", Input: map[string]any{"pattern": "**/*.go"}, Output: "a.go"},
			// edit_file: started already has path; completed must not overwrite
			{Seq: 5, Type: "tool_use", Tool: "edit_file", CallID: "call-edit-1", Input: map[string]any{"path": "/tmp/started-edit.go"}},
			{Seq: 6, Type: "tool_result", Tool: "edit_file", CallID: "call-edit-1", Input: map[string]any{"path": "/tmp/completed-edit.go"}, Output: "patched"},
			// grep: started already has query; completed must not overwrite
			{Seq: 7, Type: "tool_use", Tool: "grep", CallID: "call-grep-1", Input: map[string]any{"query": "started-query", "path": "server"}},
			{Seq: 8, Type: "tool_result", Tool: "grep", CallID: "call-grep-1", Input: map[string]any{"query": "completed-query", "path": "apps"}, Output: "hit"},
		},
	}, testWorkspaceID, "agent-inbox-tool-backfill-daemon")
	messagesReq = withURLParam(messagesReq, "eventId", got.ID)
	messagesRec := httptest.NewRecorder()
	testHandler.ReportAgentInboxMessages(messagesRec, messagesReq)
	if messagesRec.Code != http.StatusOK {
		t.Fatalf("report inbox messages: status=%d body=%s", messagesRec.Code, messagesRec.Body.String())
	}

	rows, err := testPool.Query(ctx, `
		SELECT details->>'call_id', details->>'tool', details->>'path', details->>'pattern',
		       details->>'query', details->>'tool_target', details->>'summary_kind'
		FROM agent_activity_event
		WHERE workspace_id = $1
		  AND agent_id = $2
		  AND details->>'inbox_event_id' = $3
		  AND event_kind = $4
		ORDER BY (details->>'seq')::int ASC`,
		testWorkspaceID, agentID, got.ID, activityKindToolCall)
	if err != nil {
		t.Fatalf("query tool_call rows: %v", err)
	}
	defer rows.Close()

	type toolCallRow struct {
		callID, tool, path, pattern, query, toolTarget, summaryKind string
	}
	var gotRows []toolCallRow
	for rows.Next() {
		var row toolCallRow
		var path, pattern, query, toolTarget, summaryKind *string
		if err := rows.Scan(&row.callID, &row.tool, &path, &pattern, &query, &toolTarget, &summaryKind); err != nil {
			t.Fatalf("scan tool_call row: %v", err)
		}
		if path != nil {
			row.path = *path
		}
		if pattern != nil {
			row.pattern = *pattern
		}
		if query != nil {
			row.query = *query
		}
		if toolTarget != nil {
			row.toolTarget = *toolTarget
		}
		if summaryKind != nil {
			row.summaryKind = *summaryKind
		}
		gotRows = append(gotRows, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("tool_call rows error: %v", err)
	}
	if len(gotRows) != 4 {
		t.Fatalf("tool_call rows = %+v, want 4", gotRows)
	}

	write := gotRows[0]
	if write.callID != "call-write-1" || write.tool != "write_file" {
		t.Fatalf("write row identity = %+v", write)
	}
	if write.path != "/tmp/cursor-write.txt" || !strings.HasSuffix(write.toolTarget, "cursor-write.txt") || write.summaryKind != "file_path" {
		t.Fatalf("write backfill = %+v, want path/tool_target from tool_result", write)
	}

	search := gotRows[1]
	if search.callID != "call-search-1" || search.tool != "glob" {
		t.Fatalf("search row identity = %+v", search)
	}
	if search.pattern != "**/*.go" || search.toolTarget != "**/*.go" || search.summaryKind != "pattern" {
		t.Fatalf("search backfill = %+v, want pattern from tool_result", search)
	}

	edit := gotRows[2]
	if edit.path != "/tmp/started-edit.go" || edit.toolTarget != "/tmp/started-edit.go" {
		t.Fatalf("edit overwrite guard = %+v, want started path preserved", edit)
	}

	grep := gotRows[3]
	if grep.query != "started-query" || grep.toolTarget != "started-query" || grep.summaryKind != "query" {
		t.Fatalf("grep overwrite guard = %+v, want started query preserved", grep)
	}
	if grep.path != "server" {
		t.Fatalf("grep path overwrite guard = %+v, want started path preserved", grep)
	}
}
