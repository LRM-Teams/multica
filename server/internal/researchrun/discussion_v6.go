package researchrun

import (
	"context"
	"encoding/json"
)

type OpenV6DiscussionInput struct {
	WorkspaceID, RunID, Kind, ScopeHash, InputSetHash, BranchScopeHash string
	GoalVersion                                                        int
	ThroughEventSequence                                               int64
	DirectorAssignmentID                                               string
	Inputs                                                             []V6NodeRef
	BranchRefs                                                         []V6BranchRef
}

type V6Discussion struct {
	ID, Kind, ScopeHash, InputSetHash, BranchScopeHash, Status string
	GoalVersion, Revision                                      int
	ThroughEventSequence                                       int64
}

type V6DiscussionTurnSubmission struct {
	ClientRequestID    string          `json:"client_request_id"`
	WorkspaceID        string          `json:"workspace_id"`
	RunID              string          `json:"run_id"`
	WorkItemID         string          `json:"work_item_id"`
	AttemptID          string          `json:"attempt_id"`
	ManifestID         string          `json:"manifest_id"`
	ManifestHash       string          `json:"manifest_hash"`
	DiscussionID       string          `json:"discussion_id"`
	DiscussionRevision int             `json:"discussion_revision"`
	InputSetHash       string          `json:"input_set_hash"`
	AgentID            string          `json:"agent_id"`
	VisibleMessage     string          `json:"visible_message"`
	Contribution       json.RawMessage `json:"contribution"`
	EvidenceRefs       json.RawMessage `json:"evidence_refs"`
}

type discussionV6Store interface {
	OpenV6Discussion(context.Context, OpenV6DiscussionInput) (V6Discussion, error)
}

type discussionV6Module struct{ store discussionV6Store }

func (m discussionV6Module) Open(ctx context.Context, in OpenV6DiscussionInput) (V6Discussion, error) {
	if m.store == nil || len(in.Inputs) < 2 || len(in.BranchRefs) == 0 || in.InputSetHash != v6InputSetHash(in.Inputs) ||
		in.BranchScopeHash != v6BranchScopeHash(in.BranchRefs) {
		return V6Discussion{}, ErrInvalidContract
	}
	return m.store.OpenV6Discussion(ctx, in)
}
