// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// projectSnapshotVersion is carried in every snapshot so a reader can tell which
// shape it is looking at. A snapshot outlives the code that wrote it -- it sits
// in a checkpoint row until someone resumes or inspects it -- so the shape has to
// be self-describing rather than inferred from the server version.
const projectSnapshotVersion = 1

// projectSnapshotQueries reads the same subtree CopyProjectSubtree copies:
// issues, chat sessions, and each session's messages. Keeping the two definitions
// aligned is what makes the snapshot a faithful record of what a branch would
// have reproduced.
type projectSnapshotQueries interface {
	ListIssuesByProject(ctx context.Context, arg db.ListIssuesByProjectParams) ([]db.Issue, error)
	ListChatSessionsByProject(ctx context.Context, arg db.ListChatSessionsByProjectParams) ([]db.ChatSession, error)
	ListChatMessages(ctx context.Context, chatSessionID pgtype.UUID) ([]db.ChatMessage, error)
}

// projectSnapshot is the stored envelope. The slices are always non-nil so an
// empty project serializes as `[]` rather than `null`: the snapshot is handed
// straight back out over the checkpoint API, and a null there forces every reader
// to special-case it.
type projectSnapshot struct {
	Version      int                   `json:"version"`
	ProjectID    string                `json:"project_id"`
	Issues       []db.Issue            `json:"issues"`
	ChatSessions []projectChatSnapshot `json:"chat_sessions"`
}

type projectChatSnapshot struct {
	db.ChatSession
	Messages []db.ChatMessage `json:"messages"`
}

type projectSnapshotReader struct {
	q projectSnapshotQueries
}

// NewProjectSnapshotReader builds the production ProjectSnapshotReader.
func NewProjectSnapshotReader(q projectSnapshotQueries) ProjectSnapshotReader {
	return &projectSnapshotReader{q: q}
}

func (r *projectSnapshotReader) CaptureProjectSnapshot(ctx context.Context, workspaceID, projectID string) (json.RawMessage, error) {
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("invalid workspace_id: %w", err)
	}
	projectUUID, err := util.ParseUUID(projectID)
	if err != nil {
		return nil, fmt.Errorf("invalid project_id: %w", err)
	}
	issues, err := r.q.ListIssuesByProject(ctx, db.ListIssuesByProjectParams{
		ProjectID: projectUUID, WorkspaceID: wsUUID,
	})
	if err != nil {
		return nil, fmt.Errorf("list issues for project %s: %w", projectID, err)
	}
	sessions, err := r.q.ListChatSessionsByProject(ctx, db.ListChatSessionsByProjectParams{
		ProjectID: projectUUID, WorkspaceID: wsUUID,
	})
	if err != nil {
		return nil, fmt.Errorf("list chat sessions for project %s: %w", projectID, err)
	}
	snap := projectSnapshot{
		Version:      projectSnapshotVersion,
		ProjectID:    projectID,
		Issues:       issues,
		ChatSessions: make([]projectChatSnapshot, 0, len(sessions)),
	}
	if snap.Issues == nil {
		snap.Issues = []db.Issue{}
	}
	for _, session := range sessions {
		messages, err := r.q.ListChatMessages(ctx, session.ID)
		if err != nil {
			return nil, fmt.Errorf("list messages for chat session %s: %w",
				util.UUIDToString(session.ID), err)
		}
		if messages == nil {
			messages = []db.ChatMessage{}
		}
		snap.ChatSessions = append(snap.ChatSessions, projectChatSnapshot{
			ChatSession: session, Messages: messages,
		})
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return nil, fmt.Errorf("marshal project snapshot: %w", err)
	}
	return raw, nil
}
