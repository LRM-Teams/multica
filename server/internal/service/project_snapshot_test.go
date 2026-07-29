// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type fakeSnapshotQueries struct {
	issues   []db.Issue
	sessions []db.ChatSession
	messages map[string][]db.ChatMessage

	issueArgs   []db.ListIssuesByProjectParams
	sessionArgs []db.ListChatSessionsByProjectParams
	messageArgs []pgtype.UUID

	issueErr   error
	sessionErr error
	messageErr error
}

func (f *fakeSnapshotQueries) ListIssuesByProject(_ context.Context, arg db.ListIssuesByProjectParams) ([]db.Issue, error) {
	f.issueArgs = append(f.issueArgs, arg)
	return f.issues, f.issueErr
}

func (f *fakeSnapshotQueries) ListChatSessionsByProject(_ context.Context, arg db.ListChatSessionsByProjectParams) ([]db.ChatSession, error) {
	f.sessionArgs = append(f.sessionArgs, arg)
	return f.sessions, f.sessionErr
}

func (f *fakeSnapshotQueries) ListChatMessages(_ context.Context, sessionID pgtype.UUID) ([]db.ChatMessage, error) {
	f.messageArgs = append(f.messageArgs, sessionID)
	if f.messageErr != nil {
		return nil, f.messageErr
	}
	return f.messages[uuidText(sessionID)], nil
}

func captureSnapshot(t *testing.T, q projectSnapshotQueries) map[string]any {
	t.Helper()
	raw, err := NewProjectSnapshotReader(q).CaptureProjectSnapshot(
		context.Background(), testWorkspaceUUID, testProjectUUID)
	if err != nil {
		t.Fatalf("capture snapshot: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("snapshot is not valid json: %v", err)
	}
	return got
}

func TestSnapshotCapturesIssuesSessionsAndTheirMessages(t *testing.T) {
	session := db.ChatSession{ID: mustUUIDValue(testSessionUUID), Title: "s1"}
	q := &fakeSnapshotQueries{
		issues:   []db.Issue{{ID: mustUUIDValue(testIssueUUID), Title: "i1"}},
		sessions: []db.ChatSession{session},
		messages: map[string][]db.ChatMessage{
			testSessionUUID: {{ID: mustUUIDValue(testTaskUUID), Content: "hello"}},
		},
	}

	got := captureSnapshot(t, q)
	if got["version"] != float64(projectSnapshotVersion) || got["project_id"] != testProjectUUID {
		t.Fatalf("envelope = %v", got)
	}
	issues, _ := got["issues"].([]any)
	if len(issues) != 1 {
		t.Fatalf("issues = %v, want one", got["issues"])
	}
	sessions, _ := got["chat_sessions"].([]any)
	if len(sessions) != 1 {
		t.Fatalf("chat_sessions = %v, want one", got["chat_sessions"])
	}
	// A session without its messages is the failure that matters: the snapshot
	// would look complete while the conversation it exists to record is missing.
	first, _ := sessions[0].(map[string]any)
	messages, _ := first["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("session messages = %v, want the session's one message", first["messages"])
	}
	if first["title"] != "s1" {
		t.Fatalf("session fields must be inlined alongside messages, got %v", first)
	}
}

// TestSnapshotStaysWorkspaceScoped pins the tenancy filter. Both list queries
// take a workspace, and dropping it would let a checkpoint capture another
// tenant's project subtree into a row this tenant can read back over the API.
func TestSnapshotStaysWorkspaceScoped(t *testing.T) {
	q := &fakeSnapshotQueries{}
	captureSnapshot(t, q)

	ws := mustUUIDValue(testWorkspaceUUID)
	project := mustUUIDValue(testProjectUUID)
	if len(q.issueArgs) != 1 || q.issueArgs[0].WorkspaceID != ws || q.issueArgs[0].ProjectID != project {
		t.Fatalf("issue args = %+v", q.issueArgs)
	}
	if len(q.sessionArgs) != 1 || q.sessionArgs[0].WorkspaceID != ws || q.sessionArgs[0].ProjectID != project {
		t.Fatalf("session args = %+v", q.sessionArgs)
	}
}

// An empty project must serialize as empty arrays, not null: the snapshot is
// handed straight back out over the checkpoint API and a null there forces every
// reader to special-case it.
func TestSnapshotOfAnEmptyProjectHasEmptyArrays(t *testing.T) {
	raw, err := NewProjectSnapshotReader(&fakeSnapshotQueries{}).CaptureProjectSnapshot(
		context.Background(), testWorkspaceUUID, testProjectUUID)
	if err != nil {
		t.Fatalf("capture snapshot: %v", err)
	}
	var got struct {
		Issues       *[]json.RawMessage `json:"issues"`
		ChatSessions *[]json.RawMessage `json:"chat_sessions"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Issues == nil || got.ChatSessions == nil {
		t.Fatalf("empty collections must be [] not null: %s", raw)
	}
	if len(*got.Issues) != 0 || len(*got.ChatSessions) != 0 {
		t.Fatalf("snapshot = %s, want empty collections", raw)
	}
}

func TestSnapshotFailsRatherThanRecordingAPartialSubtree(t *testing.T) {
	sentinel := errors.New("boom")
	session := db.ChatSession{ID: mustUUIDValue(testSessionUUID)}
	for name, q := range map[string]*fakeSnapshotQueries{
		"issues":   {issueErr: sentinel},
		"sessions": {sessionErr: sentinel},
		"messages": {sessions: []db.ChatSession{session}, messageErr: sentinel},
	} {
		if _, err := NewProjectSnapshotReader(q).CaptureProjectSnapshot(
			context.Background(), testWorkspaceUUID, testProjectUUID); !errors.Is(err, sentinel) {
			t.Fatalf("%s: err = %v, want the query error", name, err)
		}
	}
}

func TestSnapshotRejectsMalformedIDsBeforeQuerying(t *testing.T) {
	q := &fakeSnapshotQueries{}
	r := NewProjectSnapshotReader(q)
	if _, err := r.CaptureProjectSnapshot(context.Background(), "nope", testProjectUUID); err == nil {
		t.Fatal("a malformed workspace id must be rejected")
	}
	if _, err := r.CaptureProjectSnapshot(context.Background(), testWorkspaceUUID, "nope"); err == nil {
		t.Fatal("a malformed project id must be rejected")
	}
	if len(q.issueArgs) != 0 || len(q.sessionArgs) != 0 {
		t.Fatalf("nothing may be queried for malformed ids: %+v %+v", q.issueArgs, q.sessionArgs)
	}
}
