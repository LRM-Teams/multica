package researchrun

import "encoding/json"

type V6BranchRef struct {
	ID           string `json:"id"`
	StateVersion int64  `json:"state_version"`
}

type V6EvidenceRef struct {
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	VersionID   string `json:"version_id"`
	ContentHash string `json:"content_hash"`
}

type V6ContentLayers struct {
	CatalogSummary string          `json:"catalog_summary"`
	BriefSummary   string          `json:"brief_summary"`
	Objective      string          `json:"objective"`
	Conclusion     string          `json:"conclusion"`
	Content        string          `json:"content"`
	Scope          json.RawMessage `json:"scope"`
	Uncertainties  json.RawMessage `json:"uncertainties"`
	Conflicts      json.RawMessage `json:"conflicts"`
	OpenQuestions  json.RawMessage `json:"open_questions"`
}

type V6ResultStateProposal struct {
	ConclusionState  string          `json:"conclusion_state"`
	IntegrationState string          `json:"integration_state"`
	Termination      json.RawMessage `json:"termination,omitempty"`
}

type V6AtomicResultSubmission struct {
	ClientRequestID string                `json:"client_request_id"`
	WorkspaceID     string                `json:"workspace_id"`
	RunID           string                `json:"run_id"`
	WorkItemID      string                `json:"work_item_id"`
	TaskID          string                `json:"task_id"`
	AttemptID       string                `json:"attempt_id"`
	AgentID         string                `json:"agent_id"`
	ManifestID      string                `json:"manifest_id"`
	ManifestHash    string                `json:"manifest_hash"`
	GoalVersion     int                   `json:"goal_version"`
	BranchRefs      []V6BranchRef         `json:"branch_refs"`
	ContentLayers   V6ContentLayers       `json:"content_layers"`
	EvidenceRefs    []V6EvidenceRef       `json:"evidence_refs"`
	StateProposal   V6ResultStateProposal `json:"state_proposal"`
	ContentHash     string                `json:"content_hash"`
}

type V6AcceptedResultNode struct {
	ID, ResultArtifactID, ArtifactVersionID, WorkItemAttemptID string
	ContentHash                                                string
}
