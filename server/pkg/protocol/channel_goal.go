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
	Subgoals []ChannelSubgoalContext `json:"subgoals,omitempty"`
}
