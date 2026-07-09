package memorycuration

import (
	"context"
	"time"
)

type Stage string

const (
	StageL1  Stage = "l1"
	StageL2  Stage = "l2"
	StageL3  Stage = "l3"
	StageL4  Stage = "l4"
	StageAll Stage = "all"
)

type Options struct {
	Context        context.Context
	DB             EvidenceDB
	WorkspacesRoot string
	WorkspaceID    string
	AgentIDs       []string
	AllAgents      bool
	Stage          Stage
	Since          time.Time
	Until          time.Time
	IncludeHistory bool
	DryRun         bool
	Force          bool
	Now            time.Time
	Timezone       string
}

type Result struct {
	Stage                 Stage            `json:"stage"`
	WorkspacesRoot        string           `json:"workspaces_root"`
	WorkspaceID           string           `json:"workspace_id,omitempty"`
	DateFrom              string           `json:"date_from,omitempty"`
	DateTo                string           `json:"date_to,omitempty"`
	DryRun                bool             `json:"dry_run"`
	Force                 bool             `json:"force"`
	AgentsScanned         int              `json:"agents_scanned"`
	AgentsChanged         int              `json:"agents_changed"`
	DailyFilesWritten     int              `json:"daily_files_written"`
	ReviewCandidatesAdded int              `json:"review_candidates_added"`
	EntriesPromoted       int              `json:"entries_promoted"`
	EntriesArchived       int              `json:"entries_archived"`
	DuplicatesMerged      int              `json:"duplicates_merged"`
	ConflictsFound        int              `json:"conflicts_found"`
	EvidenceCollected     int              `json:"evidence_collected"`
	Timezone              string           `json:"timezone,omitempty"`
	Errors                []AgentError     `json:"errors,omitempty"`
	AgentResults          []AgentRunResult `json:"agent_results,omitempty"`
}

type AgentError struct {
	WorkspaceID string `json:"workspace_id"`
	AgentID     string `json:"agent_id"`
	Stage       Stage  `json:"stage"`
	Error       string `json:"error"`
}

type AgentRunResult struct {
	WorkspaceID           string `json:"workspace_id"`
	AgentID               string `json:"agent_id"`
	Root                  string `json:"root"`
	Changed               bool   `json:"changed"`
	DailyFilesWritten     int    `json:"daily_files_written"`
	ReviewCandidatesAdded int    `json:"review_candidates_added"`
	EntriesPromoted       int    `json:"entries_promoted"`
	EntriesArchived       int    `json:"entries_archived"`
	DuplicatesMerged      int    `json:"duplicates_merged"`
	ConflictsFound        int    `json:"conflicts_found"`
	EvidenceCollected     int    `json:"evidence_collected"`
}
