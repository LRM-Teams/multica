package memorycuration

import (
	"context"
	"time"
)

type Stage string

const (
	StageAgentSelfReview Stage = "agent_self_review"
	StageTeamCuration    Stage = "team_curation"
	StageL1              Stage = "l1"
	StageL2              Stage = "l2"
	StageL3              Stage = "l3"
	StageL4              Stage = "l4"
	StageAll             Stage = "all"
)

type Options struct {
	Context             context.Context
	DB                  EvidenceDB
	DBEvidence          map[string][]EvidenceItem
	StageAgent          StageAgent
	WorkspacesRoot      string
	WorkspaceID         string
	AgentIDs            []string
	AllAgents           bool
	Stage               Stage
	Since               time.Time
	Until               time.Time
	IncludeHistory      bool
	DryRun              bool
	Force               bool
	Now                 time.Time
	Timezone            string
	Mode                string
	ConfidenceThreshold float64
}

type StageAgent interface {
	RunStage(context.Context, StageAgentInput) (StageAgentOutput, error)
}

type StageAgentInput struct {
	Stage         Stage             `json:"stage"`
	WorkspaceID   string            `json:"workspace_id"`
	AgentID       string            `json:"agent_id"`
	AgentRoot     string            `json:"agent_root"`
	DateFrom      string            `json:"date_from"`
	DateTo        string            `json:"date_to"`
	Timezone      string            `json:"timezone"`
	Mode          string            `json:"mode,omitempty"`
	DryRun        bool              `json:"dry_run"`
	LocalFiles    map[string]string `json:"local_files"`
	DBEvidence    []EvidenceItem    `json:"db_evidence"`
	ReviewEntries []L3ReviewEntry   `json:"review_entries,omitempty"`
}

type StageAgentOutput struct {
	Provider string        `json:"provider,omitempty"`
	Model    string        `json:"model,omitempty"`
	Duration time.Duration `json:"duration,omitempty"`
	Content  string        `json:"content,omitempty"`
}

type Result struct {
	Stage                  Stage            `json:"stage"`
	WorkspacesRoot         string           `json:"workspaces_root"`
	WorkspaceID            string           `json:"workspace_id,omitempty"`
	DateFrom               string           `json:"date_from,omitempty"`
	DateTo                 string           `json:"date_to,omitempty"`
	DryRun                 bool             `json:"dry_run"`
	Force                  bool             `json:"force"`
	AgentsScanned          int              `json:"agents_scanned"`
	AgentsChanged          int              `json:"agents_changed"`
	DailyFilesWritten      int              `json:"daily_files_written"`
	ReviewCandidatesAdded  int              `json:"review_candidates_added"`
	EntriesReviewed        int              `json:"entries_reviewed"`
	MemoryRoutes           int              `json:"memory_routes"`
	SkillRoutes            int              `json:"skill_routes"`
	SplitRoutes            int              `json:"split_routes"`
	DiscardRoutes          int              `json:"discard_routes"`
	ReviewDeferred         int              `json:"review_deferred"`
	EntriesPromoted        int              `json:"entries_promoted"`
	SkillCandidatesAdded   int              `json:"skill_candidates_added"`
	SharedCandidatesAdded  int              `json:"shared_candidates_added"`
	SharedCandidatesSynced int              `json:"shared_candidates_synced"`
	EntriesArchived        int              `json:"entries_archived"`
	DuplicatesMerged       int              `json:"duplicates_merged"`
	ConflictsFound         int              `json:"conflicts_found"`
	EvidenceCollected      int              `json:"evidence_collected"`
	Timezone               string           `json:"timezone,omitempty"`
	Errors                 []AgentError     `json:"errors,omitempty"`
	Events                 []RunEvent       `json:"events,omitempty"`
	ReviewTraces           []L3ReviewTrace  `json:"review_traces,omitempty"`
	AgentResults           []AgentRunResult `json:"agent_results,omitempty"`
}

// RunEvent records coarse curation progress so UI can explain where a run spent time.
type RunEvent struct {
	Key       string `json:"key"`
	AgentID   string `json:"agent_id,omitempty"`
	Status    string `json:"status"`
	Message   string `json:"message,omitempty"`
	CreatedAt string `json:"created_at"`
}

type AgentError struct {
	WorkspaceID string `json:"workspace_id"`
	AgentID     string `json:"agent_id"`
	Stage       Stage  `json:"stage"`
	Error       string `json:"error"`
}

type AgentRunResult struct {
	WorkspaceID            string          `json:"workspace_id"`
	AgentID                string          `json:"agent_id"`
	Root                   string          `json:"root"`
	Changed                bool            `json:"changed"`
	DailyFilesWritten      int             `json:"daily_files_written"`
	ReviewCandidatesAdded  int             `json:"review_candidates_added"`
	EntriesReviewed        int             `json:"entries_reviewed"`
	MemoryRoutes           int             `json:"memory_routes"`
	SkillRoutes            int             `json:"skill_routes"`
	SplitRoutes            int             `json:"split_routes"`
	DiscardRoutes          int             `json:"discard_routes"`
	ReviewDeferred         int             `json:"review_deferred"`
	EntriesPromoted        int             `json:"entries_promoted"`
	SkillCandidatesAdded   int             `json:"skill_candidates_added"`
	ReviewTraces           []L3ReviewTrace `json:"review_traces,omitempty"`
	SharedCandidatesAdded  int             `json:"shared_candidates_added"`
	SharedCandidatesSynced int             `json:"shared_candidates_synced"`
	EntriesArchived        int             `json:"entries_archived"`
	DuplicatesMerged       int             `json:"duplicates_merged"`
	ConflictsFound         int             `json:"conflicts_found"`
	EvidenceCollected      int             `json:"evidence_collected"`
	CuratorOutput          string          `json:"curator_output,omitempty"`
}

type L3ReviewTrace struct {
	EntryID       string  `json:"entry_id"`
	EntryHash     string  `json:"entry_hash"`
	Route         L3Route `json:"route,omitempty"`
	Outcome       string  `json:"outcome"`
	Confidence    float64 `json:"confidence,omitempty"`
	Sensitivity   string  `json:"sensitivity,omitempty"`
	Provider      string  `json:"provider,omitempty"`
	Model         string  `json:"model,omitempty"`
	PromptVersion string  `json:"prompt_version"`
	DurationMS    int64   `json:"duration_ms,omitempty"`
	ReasonCode    string  `json:"reason_code,omitempty"`
}
