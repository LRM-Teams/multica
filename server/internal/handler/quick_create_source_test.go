package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/pkg/agent"
)

func TestQuickCreateIssueSourceTrustBoundary(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	var runtimeID, agentID string
	if err := testPool.QueryRow(ctx,
		`SELECT a.runtime_id, a.id
		   FROM agent a
		  WHERE a.workspace_id = $1
		    AND a.runtime_id IS NOT NULL
		  LIMIT 1`,
		testWorkspaceID,
	).Scan(&runtimeID, &agentID); err != nil {
		t.Fatalf("fetch agent runtime: %v", err)
	}
	if _, err := testPool.Exec(ctx,
		`UPDATE agent_runtime SET metadata = jsonb_build_object('cli_version', $1::text) WHERE id = $2`,
		agent.MinQuickCreateCLIVersion, runtimeID,
	); err != nil {
		t.Fatalf("bump runtime cli_version: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`UPDATE agent_runtime SET metadata = '{}'::jsonb WHERE id = $1`, runtimeID)
	})

	channelName := "quick-create-source-" + uuid.NewString()
	channelID := seedChannelForTest(t, channelName, testUserID)
	var rootID, replyID string
	threadID := "qc-source-thread-" + uuid.NewString()
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content, source, thread_id, trigger_depth)
		VALUES ($1, $2, 'user', $3, 'Source User', 'root source message', 'multica', $4, 0)
		RETURNING id
	`, channelID, testWorkspaceID, testUserID, threadID).Scan(&rootID); err != nil {
		t.Fatalf("seed source root: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content, source, thread_root_message_id, thread_id, trigger_depth)
		VALUES ($1, $2, 'user', $3, 'Source User', 'reply source message with important quote', 'multica', $4, $5, 1)
		RETURNING id
	`, channelID, testWorkspaceID, testUserID, rootID, threadID).Scan(&replyID); err != nil {
		t.Fatalf("seed source reply: %v", err)
	}
	var attachmentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO attachment (workspace_id, channel_id, uploader_type, uploader_id, filename, url, content_type, size_bytes)
		VALUES ($1, $2, 'member', $3, 'source.txt', 'https://example.invalid/source.txt', 'text/plain', 12)
		RETURNING id
	`, testWorkspaceID, channelID, testUserID).Scan(&attachmentID); err != nil {
		t.Fatalf("seed source attachment: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_message_attachment (workspace_id, channel_message_id, attachment_id)
		VALUES ($1, $2, $3)
	`, testWorkspaceID, replyID, attachmentID); err != nil {
		t.Fatalf("seed source attachment reference: %v", err)
	}

	countQuickCreateTasks := func(t *testing.T) int {
		t.Helper()
		var count int
		if err := testPool.QueryRow(context.Background(),
			`SELECT COUNT(*) FROM agent_inbox_event WHERE agent_id = $1 AND context->>'type' = 'quick_create'`,
			agentID,
		).Scan(&count); err != nil {
			t.Fatalf("count quick-create tasks: %v", err)
		}
		return count
	}

	t.Run("same workspace source enqueues with bounded context", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues/quick-create", map[string]any{
			"agent_id": agentID,
			"prompt":   "Create an issue from this thread",
			"source": map[string]any{
				"channel_id":             channelID,
				"message_id":             replyID,
				"thread_root_message_id": rootID,
			},
		})
		testHandler.QuickCreateIssue(w, req)
		if w.Code != http.StatusAccepted {
			t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
		}
		var resp QuickCreateIssueResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		t.Cleanup(func() {
			testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, resp.TaskID)
		})

		var contextJSON []byte
		if err := testPool.QueryRow(context.Background(),
			`SELECT context FROM agent_inbox_event WHERE id = $1`, resp.TaskID,
		).Scan(&contextJSON); err != nil {
			t.Fatalf("load task context: %v", err)
		}
		var qc service.QuickCreateContext
		if err := json.Unmarshal(contextJSON, &qc); err != nil {
			t.Fatalf("unmarshal context: %v", err)
		}
		if qc.Source == nil {
			t.Fatal("expected source context in quick-create task")
		}
		if qc.Source.ChannelID != channelID {
			t.Fatalf("source channel_id = %q, want %q", qc.Source.ChannelID, channelID)
		}
		if qc.Source.ChannelKind != "group" || qc.Source.ChannelName != channelName {
			t.Fatalf("source channel = %q/%q, want group/%q", qc.Source.ChannelKind, qc.Source.ChannelName, channelName)
		}
		if qc.Source.ThreadRootMessageID != rootID || qc.Source.SourceMessageID != replyID {
			t.Fatalf("source ids root/source = %q/%q, want %q/%q", qc.Source.ThreadRootMessageID, qc.Source.SourceMessageID, rootID, replyID)
		}
		if qc.Source.SourceAuthorID != testUserID || qc.Source.SourceAuthorType != "user" {
			t.Fatalf("source author = %q/%q, want user %q", qc.Source.SourceAuthorType, qc.Source.SourceAuthorID, testUserID)
		}
		if !strings.Contains(qc.Source.SourceExcerpt, "reply source message") {
			t.Fatalf("source excerpt did not carry source content: %q", qc.Source.SourceExcerpt)
		}
		if !strings.Contains(qc.Source.Summary, rootID) || !strings.Contains(qc.Source.Summary, "reply source message") {
			t.Fatalf("source summary missing root/reply context:\n%s", qc.Source.Summary)
		}
		if len(qc.Source.AttachmentIDs) != 1 || qc.Source.AttachmentIDs[0] != attachmentID {
			t.Fatalf("source attachment ids = %#v, want [%q]", qc.Source.AttachmentIDs, attachmentID)
		}
	})

	t.Run("non member source is rejected before enqueue", func(t *testing.T) {
		ownerID := createWorkspaceMemberUser(t, "QC Private Owner", "qc-private-owner-"+uuid.NewString()[:8]+"@multica.test")
		var privateChannelID, privateRootID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO channel (workspace_id, name, created_by, kind)
			VALUES ($1, $2, $3, 'group')
			RETURNING id
		`, testWorkspaceID, "quick-create-private-"+uuid.NewString(), ownerID).Scan(&privateChannelID); err != nil {
			t.Fatalf("seed private source channel: %v", err)
		}
		t.Cleanup(func() {
			testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, privateChannelID)
		})
		if err := testPool.QueryRow(ctx, `
			INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content, source, trigger_depth)
			VALUES ($1, $2, 'user', $3, 'Private User', 'private source', 'multica', 0)
			RETURNING id
		`, privateChannelID, testWorkspaceID, testUserID).Scan(&privateRootID); err != nil {
			t.Fatalf("seed private source message: %v", err)
		}

		before := countQuickCreateTasks(t)
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues/quick-create", map[string]any{
			"agent_id": agentID,
			"prompt":   "Try to smuggle a private source",
			"source": map[string]any{
				"channel_id": privateChannelID,
				"message_id": privateRootID,
			},
		})
		testHandler.QuickCreateIssue(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for non-member source, got %d: %s", w.Code, w.Body.String())
		}
		if got := countQuickCreateTasks(t); got != before {
			t.Fatalf("non-member source must not enqueue: expected %d tasks, got %d", before, got)
		}
	})
}
