package handler

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Default sources for Period Work Synthesis (J2 / J3). Retrospectives still
// default to issue + notes when the client omits sources; Brief always wants
// the three platform Facts channels when the caller does not narrow them.
var notePeriodWorkDefaultSources = []string{
	noteRetrospectiveSourceIssue,
	noteRetrospectiveSourceNotes,
	noteRetrospectiveSourceRuns,
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
