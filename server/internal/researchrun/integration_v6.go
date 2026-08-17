package researchrun

type V6IntegrationSubmission struct {
	ClientRequestID    string          `json:"client_request_id"`
	WorkspaceID        string          `json:"workspace_id"`
	RunID              string          `json:"run_id"`
	WorkItemID         string          `json:"work_item_id"`
	AttemptID          string          `json:"attempt_id"`
	AgentID            string          `json:"agent_id"`
	ManifestID         string          `json:"manifest_id"`
	ManifestHash       string          `json:"manifest_hash"`
	DiscussionID       string          `json:"discussion_id"`
	DiscussionRevision int             `json:"discussion_revision"`
	InputSetHash       string          `json:"input_set_hash"`
	Mode               string          `json:"mode"`
	InputNodes         []V6NodeRef     `json:"input_nodes"`
	OutputTier         V6Tier          `json:"output_tier"`
	OutputContent      V6ContentLayers `json:"output_content"`
	BranchRefs         []V6BranchRef   `json:"branch_refs"`
	SemanticGain       string          `json:"semantic_gain"`
	StewardAgentID     string          `json:"steward_agent_id"`
	ContentHash        string          `json:"content_hash"`
}

type V6IntegrationOutcome struct {
	IntegrationRoundID, InsightID, InsightVersionID, ArtifactVersionID string
	Tier                                                               V6Tier
}
