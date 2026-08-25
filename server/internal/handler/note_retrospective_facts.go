package handler

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Default sources for Period Work Synthesis (J2 / J3). Notes are never a
// Brief source — even when the client asks for touched_notes.
// Retrospectives still default to issue + notes when the client omits sources.
var notePeriodWorkDefaultSources = []string{
	noteRetrospectiveSourceIssue,
	noteRetrospectiveSourceRuns,
}

func normalizeNotePeriodBriefSources(requested []string) (enabled []string, skipped []string) {
	if len(requested) == 0 {
		requested = append([]string(nil), notePeriodWorkDefaultSources...)
	}
	enabled, skipped = normalizeNoteRetrospectiveSources(requested)
	kept := enabled[:0]
	droppedNotes := false
	for _, source := range enabled {
		if source == noteRetrospectiveSourceNotes {
			droppedNotes = true
			continue
		}
		kept = append(kept, source)
	}
	enabled = kept
	if droppedNotes && !containsNoteRetrospectiveSource(skipped, noteRetrospectiveSourceNotes) {
		skipped = append(skipped, noteRetrospectiveSourceNotes)
	}
	return enabled, skipped
}

// noteRetrospectiveFactsBundle is the reusable window Facts pack: same loaders
// as CreateNoteRetrospective, without writing a note or calling a model.
type noteRetrospectiveFactsBundle struct {
	Facts          noteRetrospectiveFacts
	SourcesUsed    []string
	SourcesEmpty   []string
	SourcesSkipped []string
}

func (b noteRetrospectiveFactsBundle) FactCount() int {
	return len(b.Facts.Issues) + len(b.Facts.Notes) + len(b.Facts.Runs)
}

// loadNoteRetrospectiveFactsBundle loads platform Facts for one half-open
// window. sources follows normalizeNoteRetrospectiveSources (empty → issue +
// notes). Period Work callers should pass notePeriodWorkDefaultSources.
func (h *Handler) loadNoteRetrospectiveFactsBundle(
	ctx context.Context,
	workspaceID, userID pgtype.UUID,
	start, end time.Time,
	sources []string,
) (noteRetrospectiveFactsBundle, error) {
	enabled, skipped := normalizeNoteRetrospectiveSources(sources)
	out := noteRetrospectiveFactsBundle{
		SourcesUsed:    make([]string, 0),
		SourcesEmpty:   make([]string, 0),
		SourcesSkipped: skipped,
	}
	for _, source := range enabled {
		switch source {
		case noteRetrospectiveSourceIssue:
			items, err := h.loadNoteRetrospectiveIssueFacts(ctx, workspaceID, userID, start, end)
			if err != nil {
				return noteRetrospectiveFactsBundle{}, err
			}
			if err := h.attachNoteRetrospectiveIssuePullRequests(ctx, workspaceID, items); err != nil {
				return noteRetrospectiveFactsBundle{}, err
			}
			out.Facts.Issues = items
			if len(items) == 0 {
				out.SourcesEmpty = append(out.SourcesEmpty, source)
			} else {
				out.SourcesUsed = append(out.SourcesUsed, source)
			}
		case noteRetrospectiveSourceNotes:
			items, err := h.loadNoteRetrospectiveNoteFacts(ctx, workspaceID, userID, start, end)
			if err != nil {
				return noteRetrospectiveFactsBundle{}, err
			}
			out.Facts.Notes = items
			if len(items) == 0 {
				out.SourcesEmpty = append(out.SourcesEmpty, source)
			} else {
				out.SourcesUsed = append(out.SourcesUsed, source)
			}
		case noteRetrospectiveSourceRuns:
			items, err := h.loadNoteRetrospectiveRunFacts(ctx, workspaceID, userID, start, end)
			if err != nil {
				return noteRetrospectiveFactsBundle{}, err
			}
			out.Facts.Runs = items
			if len(items) == 0 {
				out.SourcesEmpty = append(out.SourcesEmpty, source)
			} else {
				out.SourcesUsed = append(out.SourcesUsed, source)
			}
		}
	}
	return out, nil
}

// attachNoteRetrospectiveIssuePullRequests folds currently linked PRs onto each
// issue fact. Unlinked issues get an empty slice (never nil). Failure is
// returned — PR evidence is part of the Facts contract, not decorative.
func (h *Handler) attachNoteRetrospectiveIssuePullRequests(
	ctx context.Context,
	workspaceID pgtype.UUID,
	facts []noteRetrospectiveIssueFact,
) error {
	if len(facts) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(facts))
	issueIDs := make([]pgtype.UUID, 0, len(facts))
	for _, fact := range facts {
		if fact.IssueID == "" {
			continue
		}
		if _, ok := seen[fact.IssueID]; ok {
			continue
		}
		seen[fact.IssueID] = struct{}{}
		issueIDs = append(issueIDs, parseUUID(fact.IssueID))
	}
	byIssue := make(map[string][]noteRetrospectivePullRequestFact, len(issueIDs))
	if len(issueIDs) > 0 {
		if h == nil || h.Queries == nil {
			return nil
		}
		rows, err := h.Queries.ListPullRequestsByIssues(ctx, db.ListPullRequestsByIssuesParams{
			IssueIds:    issueIDs,
			WorkspaceID: workspaceID,
		})
		if err != nil {
			return err
		}
		for _, row := range rows {
			key := uuidToString(row.IssueID)
			byIssue[key] = append(byIssue[key], noteRetrospectivePullRequestFact{
				Number: row.PrNumber,
				URL:    row.HtmlUrl,
				State:  row.State,
				Title:  row.Title,
			})
		}
	}
	for i := range facts {
		prs := byIssue[facts[i].IssueID]
		if prs == nil {
			prs = []noteRetrospectivePullRequestFact{}
		}
		facts[i].PullRequests = prs
	}
	return nil
}

func (h *Handler) loadNotePeriodBriefFactsBundle(
	ctx context.Context,
	workspaceID, userID pgtype.UUID,
	start, end time.Time,
	sources []string,
) (noteRetrospectiveFactsBundle, error) {
	enabled, skipped := normalizeNotePeriodBriefSources(sources)
	out := noteRetrospectiveFactsBundle{
		SourcesUsed:    make([]string, 0),
		SourcesEmpty:   make([]string, 0),
		SourcesSkipped: skipped,
	}
	for _, source := range enabled {
		switch source {
		case noteRetrospectiveSourceIssue:
			items, err := h.loadNoteRetrospectiveIssueFacts(ctx, workspaceID, userID, start, end)
			if err != nil {
				return noteRetrospectiveFactsBundle{}, err
			}
			if err := h.attachNoteRetrospectiveIssuePullRequests(ctx, workspaceID, items); err != nil {
				return noteRetrospectiveFactsBundle{}, err
			}
			out.Facts.Issues = items
			if len(items) == 0 {
				out.SourcesEmpty = append(out.SourcesEmpty, source)
			} else {
				out.SourcesUsed = append(out.SourcesUsed, source)
			}
		case noteRetrospectiveSourceRuns:
			items, err := h.loadNotePeriodBriefRunFacts(ctx, workspaceID, userID, start, end)
			if err != nil {
				return noteRetrospectiveFactsBundle{}, err
			}
			out.Facts.Runs = items
			if len(items) == 0 {
				out.SourcesEmpty = append(out.SourcesEmpty, source)
			} else {
				out.SourcesUsed = append(out.SourcesUsed, source)
			}
		}
	}
	return out, nil
}

func (h *Handler) loadNotePeriodBriefRunFacts(
	ctx context.Context,
	workspaceID, userID pgtype.UUID,
	start, end time.Time,
) ([]noteRetrospectiveRunFact, error) {
	// Filter in SQL so a week of 写汇报 wakes cannot crowd the LIMIT 200
	// and hide real issue/channel runs.
	rows, err := h.DB.Query(ctx, `
SELECT e.id, e.agent_id, COALESCE(a.name, ''), COALESCE(e.reason, ''), COALESCE(e.trigger_summary, ''),
       COALESCE(e.terminal_outcome, ''), e.status,
       e.issue_id, i.number, COALESCE(i.title, ''), COALESCE(w.issue_prefix, ''),
       COALESCE(e.completed_at, e.terminal_at, e.created_at) AS happened_at
FROM agent_inbox_event e
JOIN agent a ON a.id = e.agent_id AND a.workspace_id = e.workspace_id
JOIN workspace w ON w.id = e.workspace_id
LEFT JOIN issue i ON i.id = e.issue_id AND i.workspace_id = e.workspace_id
WHERE e.workspace_id = $1
  AND a.archived_at IS NULL
  AND (e.initiator_user_id = $2 OR a.owner_id = $2)
  AND COALESCE(e.completed_at, e.terminal_at, e.created_at) >= $3
  AND COALESCE(e.completed_at, e.terminal_at, e.created_at) < $4
  AND e.reason <> 'note_worker'
  AND lower(a.name) NOT IN ('notes-assistant', 'weekly-report')
  AND a.name NOT LIKE 'period-collect-%'
  AND (
    e.terminal_outcome IS NOT NULL
    OR e.completed_at IS NOT NULL
    OR e.status IN ('completed', 'acked', 'failed', 'cancelled', 'suppressed')
  )
ORDER BY COALESCE(e.completed_at, e.terminal_at, e.created_at) ASC, e.id ASC
LIMIT 200`, workspaceID, userID, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items, err := scanNoteRetrospectiveRunFacts(rows)
	if err != nil {
		return nil, err
	}
	out := make([]noteRetrospectiveRunFact, 0, len(items))
	for _, fact := range items {
		if isPeriodBriefMachineryRun(fact) {
			continue
		}
		out = append(out, fact)
	}
	return out, nil
}

func isPeriodBriefMachineryRun(fact noteRetrospectiveRunFact) bool {
	if strings.TrimSpace(fact.Reason) == "note_worker" {
		return true
	}
	return isPeriodBriefMachineryAgentName(fact.AgentName)
}

func isPeriodBriefMachineryAgentName(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == notesAssistantAgentName || n == "weekly-report" {
		return true
	}
	return strings.HasPrefix(n, periodBriefCollectorNamePrefix)
}
