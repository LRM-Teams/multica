package service

import (
	"context"
	"encoding/json"
	"fmt"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	issueAssignmentSnapshotKey     = "issue_assignment_snapshot"
	issueAssignmentSnapshotVersion = 1
)

// issueAssignmentSnapshotContext contains only fields that are frozen when an
// assignment wake is enqueued. Status is deliberately absent: the claim path
// already loads the current issue and adds its current status to the wire
// response without another query.
type issueAssignmentSnapshotContext struct {
	Version            int            `json:"version"`
	Title              string         `json:"title"`
	Description        *string        `json:"description"`
	AcceptanceCriteria []string       `json:"acceptance_criteria"`
	Metadata           map[string]any `json:"metadata"`
	CommentCount       int64          `json:"comment_count"`
}

func (s *TaskService) buildIssueAssignmentSnapshot(ctx context.Context, issue db.Issue) (issueAssignmentSnapshotContext, error) {
	return buildIssueAssignmentSnapshotWithQueries(ctx, s.Queries, issue)
}

func buildIssueAssignmentSnapshotWithQueries(ctx context.Context, q *db.Queries, issue db.Issue) (issueAssignmentSnapshotContext, error) {
	commentCount, err := q.CountComments(ctx, db.CountCommentsParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		return issueAssignmentSnapshotContext{}, fmt.Errorf("count assignment comments: %w", err)
	}

	acceptanceCriteria := []string{}
	if len(issue.AcceptanceCriteria) > 0 {
		if err := json.Unmarshal(issue.AcceptanceCriteria, &acceptanceCriteria); err != nil || acceptanceCriteria == nil {
			return issueAssignmentSnapshotContext{}, fmt.Errorf("decode assignment acceptance criteria")
		}
	}
	metadata := map[string]any{}
	if len(issue.Metadata) > 0 {
		if err := json.Unmarshal(issue.Metadata, &metadata); err != nil || metadata == nil {
			return issueAssignmentSnapshotContext{}, fmt.Errorf("decode assignment metadata")
		}
	}

	var description *string
	if issue.Description.Valid {
		value := issue.Description.String
		description = &value
	}
	return issueAssignmentSnapshotContext{
		Version:            issueAssignmentSnapshotVersion,
		Title:              issue.Title,
		Description:        description,
		AcceptanceCriteria: acceptanceCriteria,
		Metadata:           metadata,
		CommentCount:       commentCount,
	}, nil
}

// withIssueAssignmentSnapshot preserves every existing task context key while
// adding the immutable issue read-model used by assignment wakes.
func withIssueAssignmentSnapshot(contextJSON []byte, snapshot issueAssignmentSnapshotContext) ([]byte, error) {
	if err := validateIssueAssignmentSnapshot(snapshot); err != nil {
		return nil, err
	}
	contextMap := map[string]json.RawMessage{}
	if len(contextJSON) > 0 {
		if err := json.Unmarshal(contextJSON, &contextMap); err != nil {
			return nil, err
		}
	}
	if contextMap == nil {
		contextMap = map[string]json.RawMessage{}
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	contextMap[issueAssignmentSnapshotKey] = raw
	return json.Marshal(contextMap)
}

// IssueAssignmentSnapshotFromContext distinguishes an absent historical
// snapshot from a present-but-invalid new snapshot. Callers may preserve the
// old API-boundary behavior for absence, but must fail closed on invalid data.
func IssueAssignmentSnapshotFromContext(contextJSON []byte) (protocol.IssueAssignmentSnapshot, bool, error) {
	var contextMap map[string]json.RawMessage
	if len(contextJSON) == 0 {
		return protocol.IssueAssignmentSnapshot{}, false, nil
	}
	if err := json.Unmarshal(contextJSON, &contextMap); err != nil {
		return protocol.IssueAssignmentSnapshot{}, false, err
	}
	raw, ok := contextMap[issueAssignmentSnapshotKey]
	if !ok {
		return protocol.IssueAssignmentSnapshot{}, false, nil
	}
	var snapshot issueAssignmentSnapshotContext
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return protocol.IssueAssignmentSnapshot{}, true, err
	}
	if err := validateIssueAssignmentSnapshot(snapshot); err != nil {
		return protocol.IssueAssignmentSnapshot{}, true, err
	}
	return protocol.IssueAssignmentSnapshot{
		Version:            snapshot.Version,
		Title:              snapshot.Title,
		Description:        snapshot.Description,
		AcceptanceCriteria: snapshot.AcceptanceCriteria,
		Metadata:           snapshot.Metadata,
		CommentCount:       snapshot.CommentCount,
	}, true, nil
}

func validateIssueAssignmentSnapshot(snapshot issueAssignmentSnapshotContext) error {
	if snapshot.Version != issueAssignmentSnapshotVersion {
		return fmt.Errorf("unsupported issue assignment snapshot version %d", snapshot.Version)
	}
	if snapshot.Title == "" {
		return fmt.Errorf("issue assignment snapshot title is empty")
	}
	if snapshot.AcceptanceCriteria == nil {
		return fmt.Errorf("issue assignment snapshot acceptance criteria is null")
	}
	if snapshot.Metadata == nil {
		return fmt.Errorf("issue assignment snapshot metadata is null")
	}
	if snapshot.CommentCount < 0 {
		return fmt.Errorf("issue assignment snapshot comment count is negative")
	}
	return nil
}
