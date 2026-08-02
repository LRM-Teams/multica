package handler

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

// TestInsertAgentActivityEvent_WorkspaceFileKindPersists is the positive
// case for task #95 (migration 268): a workspace_file/file event, inserted
// through the real production write path (insertAgentActivityEvent — the
// same function agent_files.go's GetAgentFileContent/UpdateAgentFileContent
// call), must round-trip through the DB with every field intact. This
// exercises the actual insert function, not a hand-rolled bypass query.
func TestInsertAgentActivityEvent_WorkspaceFileKindPersists(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "workspace-file-audit-persist", nil)

	id, ok := insertAgentActivityEvent(
		ctx, testPool, parseUUID(testWorkspaceID), parseUUID(agentID), pgtype.UUID{}, pgtype.UUID{},
		activityKindWorkspaceFile, agentWorkspaceFileEventRead, "info",
		agentWorkspaceFileTargetKind, pgtype.UUID{}, "memory/MEMORY.md",
		"", "Agent workspace file read",
		map[string]any{
			"actor_type":   "member",
			"actor_id":     testUserID,
			"content_hash": "abc123",
			"truncated":    false,
		},
	)
	if !ok {
		t.Fatal("insertAgentActivityEvent returned ok=false for a workspace_file/file event")
	}

	var (
		eventKind, eventType, targetKind, targetSlug, visibility string
		targetID                                                 pgtype.UUID
		detailsRaw                                               []byte
	)
	if err := testPool.QueryRow(ctx, `
		SELECT event_kind, event_type, target_kind, target_id, target_slug, visibility, details
		  FROM agent_activity_event
		 WHERE id = $1
	`, id).Scan(&eventKind, &eventType, &targetKind, &targetID, &targetSlug, &visibility, &detailsRaw); err != nil {
		t.Fatalf("read back inserted row: %v", err)
	}
	if eventKind != activityKindWorkspaceFile {
		t.Fatalf("event_kind = %q, want %q", eventKind, activityKindWorkspaceFile)
	}
	if eventType != agentWorkspaceFileEventRead {
		t.Fatalf("event_type = %q, want %q", eventType, agentWorkspaceFileEventRead)
	}
	if targetKind != agentWorkspaceFileTargetKind {
		t.Fatalf("target_kind = %q, want %q", targetKind, agentWorkspaceFileTargetKind)
	}
	if targetID.Valid {
		t.Fatalf("target_id = %+v, want invalid/null (path lives in target_slug, not target_id)", targetID)
	}
	if targetSlug != "memory/MEMORY.md" {
		t.Fatalf("target_slug = %q, want the file path", targetSlug)
	}
	if visibility != "user_facing" {
		t.Fatalf("visibility = %q, want user_facing (workspace_file is not in the diagnostic_only kind list)", visibility)
	}
	var details map[string]any
	if err := json.Unmarshal(detailsRaw, &details); err != nil {
		t.Fatalf("unmarshal details: %v", err)
	}
	if details["actor_id"] != testUserID {
		t.Fatalf("details.actor_id = %v, want %q", details["actor_id"], testUserID)
	}
	if details["content_hash"] != "abc123" {
		t.Fatalf("details.content_hash = %v, want abc123", details["content_hash"])
	}
}

// TestInsertAgentActivityEvent_WorkspaceFileWriteEventType covers the write
// event_type — a separate case from read because agent_files.go's
// UpdateAgentFileContent uses agentWorkspaceFileEventWrite, and the two
// must not collide.
func TestInsertAgentActivityEvent_WorkspaceFileWriteEventType(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "workspace-file-audit-write", nil)

	id, ok := insertAgentActivityEvent(
		ctx, testPool, parseUUID(testWorkspaceID), parseUUID(agentID), pgtype.UUID{}, pgtype.UUID{},
		activityKindWorkspaceFile, agentWorkspaceFileEventWrite, "info",
		agentWorkspaceFileTargetKind, pgtype.UUID{}, "notes/todo.md",
		"", "Agent workspace file written",
		map[string]any{"actor_type": "member", "actor_id": testUserID, "content_hash": "def456"},
	)
	if !ok {
		t.Fatal("insertAgentActivityEvent returned ok=false for a workspace_file write event")
	}
	var eventType string
	if err := testPool.QueryRow(ctx, `SELECT event_type FROM agent_activity_event WHERE id = $1`, id).Scan(&eventType); err != nil {
		t.Fatalf("read back inserted row: %v", err)
	}
	if eventType != agentWorkspaceFileEventWrite {
		t.Fatalf("event_type = %q, want %q", eventType, agentWorkspaceFileEventWrite)
	}
}

// TestAgentActivityEventKindCheck_RejectsUnknownKind is the reverse case
// task #95's acceptance criteria explicitly required: the CHECK constraint
// must actually reject values outside the whitelist, not just accept the
// two new ones — otherwise the constraint could be a no-op and this test
// suite would never notice.
func TestAgentActivityEventKindCheck_RejectsUnknownKind(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "workspace-file-audit-reject-kind", nil)

	_, err := testPool.Exec(ctx, `
		INSERT INTO agent_activity_event (workspace_id, agent_id, event_kind, event_type, severity)
		VALUES ($1, $2, 'not_a_real_kind', 'whatever', 'info')
	`, testWorkspaceID, agentID)
	if err == nil {
		t.Fatal("expected the event_kind CHECK constraint to reject an unlisted value, insert succeeded")
	}
}

// TestAgentActivityEventTargetKindCheck_AcceptsFileRejectsUnknown covers the
// second CHECK this migration touched (target_kind): 'file' must now be
// accepted, and the constraint must still reject anything not on the list.
func TestAgentActivityEventTargetKindCheck_AcceptsFileRejectsUnknown(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "workspace-file-audit-target-kind", nil)

	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_activity_event (workspace_id, agent_id, event_kind, event_type, severity, target_kind, target_slug)
		VALUES ($1, $2, 'workspace_file', 'file_read', 'info', 'file', 'ok.md')
	`, testWorkspaceID, agentID); err != nil {
		t.Fatalf("target_kind='file' should be accepted after migration 268: %v", err)
	}

	_, err := testPool.Exec(ctx, `
		INSERT INTO agent_activity_event (workspace_id, agent_id, event_kind, event_type, severity, target_kind)
		VALUES ($1, $2, 'workspace_file', 'file_read', 'info', 'not_a_real_target_kind')
	`, testWorkspaceID, agentID)
	if err == nil {
		t.Fatal("expected the target_kind CHECK constraint to reject an unlisted value, insert succeeded")
	}
}

// TestAgentActivityEventKindCheck_PreExistingKindsStillAccepted guards
// against migration 268 accidentally narrowing the allowlist instead of
// only extending it — every event_kind value the CHECK accepted before this
// migration must still be accepted after it.
func TestAgentActivityEventKindCheck_PreExistingKindsStillAccepted(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "workspace-file-audit-preexisting-kinds", nil)

	preExistingKinds := []string{
		"thinking", "tool_call", "tool_output", "turn_end", "session_init",
		"compaction_started", "compaction_finished", "wake_attempt", "error",
		"text", "system", "transport", "telemetry", "blocked", "custom",
	}
	for _, kind := range preExistingKinds {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO agent_activity_event (workspace_id, agent_id, event_kind, event_type, severity)
			VALUES ($1, $2, $3, 'regression-check', 'info')
		`, testWorkspaceID, agentID, kind); err != nil {
			t.Fatalf("pre-existing event_kind %q was rejected after migration 268: %v", kind, err)
		}
	}
}
