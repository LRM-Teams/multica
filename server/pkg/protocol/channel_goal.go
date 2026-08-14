package protocol

// ChannelGoalContext is the compact, server-claimed goal state attached to a
// channel task. It is intentionally provider-neutral so every runtime receives
// the same current objective after resume or context compaction.
type ChannelGoalContext struct {
	ID                string   `json:"id"`
	Title             string   `json:"title"`
	Objective         string   `json:"objective"`
	SuccessCriteria   []string `json:"success_criteria"`
	Version           int64    `json:"version"`
	ProgressSummary   string   `json:"progress_summary,omitempty"`
	CurrentStep       string   `json:"current_step,omitempty"`
	Blocker           string   `json:"blocker,omitempty"`
	EvidenceRefs      []string `json:"evidence_refs,omitempty"`
	CompletedCriteria []string `json:"completed_criteria,omitempty"`
	// Subgoals is the bounded list of sub-goals relevant to the claiming agent
	// (LRM-1004). Never a dump of other agents' full threads.
	Subgoals  []ChannelSubgoalContext  `json:"subgoals,omitempty"`
	WorkGraph *ChannelWorkGraphContext `json:"work_graph,omitempty"`
	// Coordination is computed by the server from the channel's durable
	// Project/Git/Issue control plane. Agents must not infer execution authority
	// from the free-form Goal text alone.
	Coordination *ChannelGoalCoordinationContext `json:"coordination,omitempty"`
}

type ChannelGoalCoordinationContext struct {
	ProjectID                 string `json:"project_id,omitempty"`
	GitRepositoryBound        bool   `json:"git_repository_bound"`
	AgentMemberCount          int    `json:"agent_member_count"`
	ChannelIssueTotal         int    `json:"channel_issue_total"`
	ChannelProjectIssueTotal  int    `json:"channel_project_issue_total"`
	ProjectIssueTotal         int    `json:"project_issue_total"`
	OpenProjectIssueTotal     int    `json:"open_project_issue_total"`
	InReviewProjectIssueTotal int    `json:"in_review_project_issue_total"`
	SubgoalTotal              int    `json:"subgoal_total"`
	OpenSubgoalTotal          int    `json:"open_subgoal_total"`
	ExecutionAdmission        string `json:"execution_admission"`
}

type ChannelWorkGraphContext struct {
	ID        string `json:"id"`
	Version   int64  `json:"version"`
	Status    string `json:"status"`
	Completed int    `json:"completed"`
	Running   int    `json:"running"`
	Waiting   int    `json:"waiting"`
	Stale     int    `json:"stale"`
}
