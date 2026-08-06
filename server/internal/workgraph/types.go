package workgraph

import (
	"encoding/json"
	"time"
)

const (
	workNodeKindIssue = "issue"

	ownerTypeAgent      = "agent"
	ownerTypeMember     = "member"
	ownerTypeUnassigned = "unassigned"

	workNodeStatusActive      = "active"
	workNodeStatusWaiting     = "waiting"
	workNodeStatusBlocked     = "blocked"
	workNodeStatusNeedsRework = "needs_rework"
	workNodeStatusDone        = "done"
	workNodeStatusCancelled   = "cancelled"

	issueDependencyBlockedBy = "blocked_by"
	issueDependencyBlocks    = "blocks"
)

type AnchorKind string

const (
	AnchorChannelGoal AnchorKind = "channel_goal"
	AnchorIssue       AnchorKind = "issue"
	AnchorResearchRun AnchorKind = "research_run"
)

type AdmissionDecision string

const (
	AdmissionGraph        AdmissionDecision = "GRAPH"
	AdmissionProposeGraph AdmissionDecision = "PROPOSE_GRAPH"
)

type NodeSpec struct {
	TempID             string          `json:"temp_id"`
	IssueID            string          `json:"issue_id,omitempty"`
	Title              string          `json:"title,omitempty"`
	Description        string          `json:"description,omitempty"`
	AssigneeID         string          `json:"assignee_id,omitempty"`
	Role               string          `json:"role"`
	ContextPolicy      string          `json:"context_policy,omitempty"`
	Objective          string          `json:"objective,omitempty"`
	CompletionContract []string        `json:"completion_contract,omitempty"`
	DependsOn          []string        `json:"depends_on,omitempty"`
	Budget             json.RawMessage `json:"budget,omitempty"`
}

type CreateInput struct {
	WorkspaceID    string            `json:"-"`
	AnchorKind     AnchorKind        `json:"anchor_kind"`
	AnchorID       string            `json:"anchor_id"`
	Admission      AdmissionDecision `json:"admission_decision"`
	BudgetPolicy   json.RawMessage   `json:"budget_policy,omitempty"`
	Reason         string            `json:"reason"`
	ActorType      string            `json:"-"`
	ActorID        string            `json:"-"`
	IdempotencyKey string            `json:"idempotency_key"`
	Nodes          []NodeSpec        `json:"nodes"`
}

type Node struct {
	ID              string          `json:"id"`
	IssueID         string          `json:"issue_id"`
	Role            string          `json:"role"`
	ContextPolicy   string          `json:"context_policy"`
	ExecutionStatus string          `json:"execution_status"`
	ValidityStatus  string          `json:"validity_status"`
	ReviewStatus    string          `json:"review_status"`
	Objective       string          `json:"objective"`
	Completion      []string        `json:"completion_contract"`
	Budget          json.RawMessage `json:"budget"`
	BasedOnVersion  int64           `json:"based_on_graph_version"`
	CreatedAt       time.Time       `json:"created_at"`
}

type Edge struct {
	ID         string `json:"id"`
	FromNodeID string `json:"from_node_id"`
	ToNodeID   string `json:"to_node_id"`
	Type       string `json:"edge_type"`
	Required   bool   `json:"required"`
}

type Graph struct {
	ID                string            `json:"id"`
	WorkspaceID       string            `json:"workspace_id"`
	AnchorKind        AnchorKind        `json:"anchor_kind"`
	AnchorID          string            `json:"anchor_id"`
	Status            string            `json:"status"`
	CurrentVersion    int64             `json:"current_version"`
	AdmissionDecision AdmissionDecision `json:"admission_decision"`
	BudgetPolicy      json.RawMessage   `json:"budget_policy"`
	Nodes             []Node            `json:"nodes"`
	Edges             []Edge            `json:"edges"`
}

type CreateResult struct {
	Graph    Graph             `json:"graph"`
	NodeIDs  map[string]string `json:"node_ids"`
	IssueIDs map[string]string `json:"issue_ids"`
	Replayed bool              `json:"replayed"`
}

type NodeUpdateInput struct {
	WorkspaceID          string `json:"-"`
	GraphID              string `json:"-"`
	NodeID               string `json:"-"`
	ExpectedGraphVersion int64  `json:"expected_graph_version"`
	ExecutionStatus      string `json:"execution_status,omitempty"`
	ValidityStatus       string `json:"validity_status,omitempty"`
	ReviewStatus         string `json:"review_status,omitempty"`
	Reason               string `json:"reason"`
}

type ArtifactInput struct {
	WorkspaceID    string          `json:"-"`
	GraphID        string          `json:"-"`
	ProducerNodeID string          `json:"producer_node_id"`
	ArtifactID     string          `json:"artifact_id,omitempty"`
	Digest         string          `json:"digest"`
	Kind           string          `json:"kind"`
	Locator        string          `json:"locator"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
}

type ArtifactRevision struct {
	ID             string          `json:"id"`
	ArtifactID     string          `json:"artifact_id"`
	ProducerNodeID string          `json:"producer_node_id"`
	Revision       int64           `json:"revision"`
	Digest         string          `json:"digest"`
	Kind           string          `json:"kind"`
	Locator        string          `json:"locator"`
	Metadata       json.RawMessage `json:"metadata"`
	ValidityStatus string          `json:"validity_status"`
	CreatedAt      time.Time       `json:"created_at"`
}

type VerificationInput struct {
	WorkspaceID        string          `json:"-"`
	GraphID            string          `json:"-"`
	VerifierNodeID     string          `json:"verifier_node_id"`
	ArtifactRevisionID string          `json:"artifact_revision_id"`
	ScopeDigest        string          `json:"scope_digest"`
	Verdict            string          `json:"verdict"`
	Findings           json.RawMessage `json:"findings,omitempty"`
	EvidenceRefs       json.RawMessage `json:"evidence_refs,omitempty"`
}

type ReviseInput struct {
	WorkspaceID          string          `json:"-"`
	GraphID              string          `json:"-"`
	ExpectedGraphVersion int64           `json:"expected_graph_version"`
	Reason               string          `json:"reason"`
	ExpectedCostDelta    json.RawMessage `json:"expected_cost_delta,omitempty"`
	ActorType            string          `json:"-"`
	ActorID              string          `json:"-"`
	Nodes                []NodeSpec      `json:"nodes"`
}
