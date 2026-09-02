package researchrun

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	v6DirectorBriefBranchesPerPage = 64
	v6DirectorBriefWorkPerPage     = 128
)

type DirectorBriefFacts struct {
	WorkspaceID, RunID, AssignmentID string
	DirectorGeneration               int
	StateVersion, ThroughSequence    int64
	Goal                             map[string]any
	DirectorState                    string
	Team                             []any
	Branches, TerminalSummaries      []any
	WorkItems, Discussions, Reports  []any
	UnresolvedDisputes, Steering     []any
	ReportPlan                       map[string]any
	// BackgroundKnowledge carries the recalled memory-graph entries
	// (unification spec §5): maps with node_id, graph, epistemic,
	// observed_at_date and a bounded summary. Empty means no background.
	BackgroundKnowledge []any
}

type CompiledDirectorBrief struct {
	BriefID, BriefHash string
	Pages              []json.RawMessage
	PageHashes         []string
	PageKeys           []string
}

type contextCompilerModule struct{}

func (contextCompilerModule) CompileDirectorBrief(facts DirectorBriefFacts, now time.Time) (CompiledDirectorBrief, error) {
	if len(facts.Team) == 0 {
		return CompiledDirectorBrief{}, fmt.Errorf("%w: Director Brief requires its Director membership", ErrInvalidContract)
	}
	pageCount := max(1, (len(facts.Branches)+v6DirectorBriefBranchesPerPage-1)/v6DirectorBriefBranchesPerPage, (len(facts.WorkItems)+v6DirectorBriefWorkPerPage-1)/v6DirectorBriefWorkPerPage)
	briefID := uuid.NewString()
	createdAt := now.UTC().Format(time.RFC3339Nano)
	pages := make([]map[string]any, pageCount)
	hashes := make([]string, pageCount)
	keys := make([]string, pageCount)
	for index := 0; index < pageCount; index++ {
		branchStart := index * v6DirectorBriefBranchesPerPage
		workStart := index * v6DirectorBriefWorkPerPage
		branches := sliceV6Any(facts.Branches, branchStart, v6DirectorBriefBranchesPerPage)
		work := sliceV6Any(facts.WorkItems, workStart, v6DirectorBriefWorkPerPage)
		key := fmt.Sprintf("brief:%d:%d", facts.ThroughSequence, index)
		keys[index] = key
		pageDescriptor := map[string]any{"page_key": key, "page_kind": "overview", "page_ordinal": index, "page_count": pageCount, "has_more": index+1 < pageCount}
		if index+1 < pageCount {
			pageDescriptor["next_cursor"] = fmt.Sprintf("%d", index+1)
		}
		reportPlan := facts.ReportPlan
		if reportPlan == nil {
			reportPlan = map[string]any{"reporter_agent_id": "", "selection_hash": "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", "maturity": "interim", "inputs": []any{}, "directions": []any{}, "active_report_work": false, "needs_refresh": false}
		}
		page := map[string]any{
			"contract_kind": "director_brief", "schema_version": 6, "brief_id": briefID,
			"workspace_id": facts.WorkspaceID, "run_id": facts.RunID, "director_assignment_id": facts.AssignmentID,
			"director_generation": facts.DirectorGeneration, "state_version": facts.StateVersion, "through_event_sequence": facts.ThroughSequence,
			"page": pageDescriptor, "goal": facts.Goal, "created_at": createdAt,
			"background_knowledge": compileV6BackgroundKnowledgeBlock(facts),
			"research":             map[string]any{"overview": "Current fresh Branch Frontier summaries.", "branch_count": len(facts.Branches), "branches": branches, "terminal_summaries": facts.TerminalSummaries, "unresolved_disputes": facts.UnresolvedDisputes},
			"control":              map[string]any{"director_state": facts.DirectorState, "active_team_count": len(facts.Team), "team_hard_cap": 50, "creation_threshold": 20, "team": facts.Team, "work_items": work, "discussions": facts.Discussions, "reports": facts.Reports, "report_plan": reportPlan, "latest_steering": facts.Steering, "changes": []any{}},
		}
		canonical, err := marshalV6CanonicalJSON(page)
		if err != nil {
			return CompiledDirectorBrief{}, err
		}
		hashes[index] = ArtifactContentHashFromCanonicalJSON(canonical)
		pages[index] = page
	}
	manifest := make([]any, pageCount)
	for index := range pages {
		manifest[index] = map[string]any{"page_key": keys[index], "page_hash": hashes[index]}
	}
	briefCanonical, err := marshalV6CanonicalJSON(map[string]any{"brief_id": briefID, "workspace_id": facts.WorkspaceID, "run_id": facts.RunID, "director_assignment_id": facts.AssignmentID, "director_generation": facts.DirectorGeneration, "state_version": facts.StateVersion, "through_event_sequence": facts.ThroughSequence, "goal_version": facts.Goal["goal_version"], "pages": manifest})
	if err != nil {
		return CompiledDirectorBrief{}, err
	}
	briefHash := ArtifactContentHashFromCanonicalJSON(briefCanonical)
	encoded := make([]json.RawMessage, pageCount)
	for index, page := range pages {
		page["brief_hash"] = briefHash
		page["page"].(map[string]any)["page_hash"] = hashes[index]
		raw, marshalErr := json.Marshal(page)
		if marshalErr != nil {
			return CompiledDirectorBrief{}, marshalErr
		}
		if _, decodeErr := DecodeV6Contract(raw, V6ContractDirectorBrief, nil); decodeErr != nil {
			return CompiledDirectorBrief{}, decodeErr
		}
		encoded[index] = raw
	}
	return CompiledDirectorBrief{BriefID: briefID, BriefHash: briefHash, Pages: encoded, PageHashes: hashes, PageKeys: keys}, nil
}

// v6BackgroundKnowledgeGuidance is the fixed header of the background block:
// recalled memory is planning reference, never evidence, and the Director
// folds anything an agent needs into work item descriptions (spec §5).
const v6BackgroundKnowledgeGuidance = "背景知识来自工作区记忆图的召回，仅供规划参考，不作为证据；每条标注认识论状态与观察日期。需要传递给 Agent 的背景请融入 work item 描述，不要作为任务证据下发。"

// compileV6BackgroundKnowledgeBlock renders the stable background block for
// every brief page: the guidance plus the bounded entry list. An empty fact
// list still renders the empty block so the brief shape never varies by
// workspace memory mode.
func compileV6BackgroundKnowledgeBlock(facts DirectorBriefFacts) map[string]any {
	entries := facts.BackgroundKnowledge
	if entries == nil {
		entries = []any{}
	}
	return map[string]any{"guidance": v6BackgroundKnowledgeGuidance, "entries": entries}
}

func sliceV6Any(values []any, start, limit int) []any {
	if start >= len(values) {
		return []any{}
	}
	end := min(len(values), start+limit)
	return append([]any(nil), values[start:end]...)
}
