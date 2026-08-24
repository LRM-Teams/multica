package handler

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNormalizeNotePeriodBriefSourcesDropsNotes(t *testing.T) {
	t.Parallel()
	enabled, skipped := normalizeNotePeriodBriefSources(nil)
	if containsNoteRetrospectiveSource(enabled, noteRetrospectiveSourceNotes) {
		t.Fatalf("default enabled=%v must not include touched_notes", enabled)
	}
	if !containsNoteRetrospectiveSource(skipped, noteRetrospectiveSourceNotes) {
		t.Fatalf("default skipped=%v must include touched_notes", skipped)
	}
	if !containsNoteRetrospectiveSource(enabled, noteRetrospectiveSourceIssue) ||
		!containsNoteRetrospectiveSource(enabled, noteRetrospectiveSourceRuns) {
		t.Fatalf("default enabled=%v want issue_activity + agent_runs", enabled)
	}

	enabled, skipped = normalizeNotePeriodBriefSources([]string{
		noteRetrospectiveSourceIssue,
		noteRetrospectiveSourceNotes,
		noteRetrospectiveSourceRuns,
	})
	if containsNoteRetrospectiveSource(enabled, noteRetrospectiveSourceNotes) {
		t.Fatalf("requested notes still enabled: %v", enabled)
	}
	if !containsNoteRetrospectiveSource(skipped, noteRetrospectiveSourceNotes) {
		t.Fatalf("requested notes not skipped: %v", skipped)
	}
}

func TestFormatNotePeriodBriefFactsOmitsNotesAndPromptBlobs(t *testing.T) {
	t.Parallel()
	got := formatNotePeriodBriefFacts(noteRetrospectiveFacts{
		Notes: []noteRetrospectiveNoteFact{{
			PageID: "page-1",
			Title:  "todo",
		}},
		Runs: []noteRetrospectiveRunFact{
			{
				AgentName: "wendy",
				Summary:   "Write a Period Work Brief for a manager or colleague — a reporting narrative",
				Reason:    "note_worker",
				Outcome:   "no_reply",
			},
			{
				AgentName:       "dev",
				Summary:         "please implement the login fix now",
				Reason:          "issue",
				Outcome:         "completed",
				IssueIdentifier: "MUL-9",
				IssueTitle:      "Fix login",
			},
		},
	})
	if strings.Contains(got, "Touched notes") || strings.Contains(got, "todo") {
		t.Fatalf("notes must not appear in Period Brief facts:\n%s", got)
	}
	if strings.Contains(got, "Write a Period Work Brief") || strings.Contains(got, "please implement") {
		t.Fatalf("trigger_summary must not appear in Period Brief facts:\n%s", got)
	}
	if !strings.Contains(got, "MUL-9") || !strings.Contains(got, "Fix login") {
		t.Fatalf("issue run should use identifier + title:\n%s", got)
	}
	if !strings.Contains(got, "dev: MUL-9 Fix login (completed)") {
		t.Fatalf("issue run line shape:\n%s", got)
	}
}

func TestLoadNotePeriodBriefFactsExcludesMachineryAndNotes(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	now := time.Now().UTC()
	start := now.Add(-2 * time.Hour)
	end := now.Add(time.Hour)

	keepAgent := createHandlerTestAgent(t, "Facts Keep "+uuid.NewString()[:8], nil)
	collectorID := createPeriodBriefCollectorTestAgent(t, "Facts Collector")
	issueID, _ := createIssueForNoteRefTest(t, testWorkspaceID, "Period brief fact issue "+uuid.NewString())
	noteID := createNotePageForAITest(t, "Period brief fact note "+uuid.NewString())
	if _, err := testPool.Exec(ctx, `UPDATE note_page SET updated_at = $2 WHERE id = $1`, noteID, now.Add(-20*time.Minute)); err != nil {
		t.Fatalf("touch note: %v", err)
	}

	var keepRun, workerRun, collectorRun string
	if err := testPool.QueryRow(ctx, `
INSERT INTO agent_inbox_event (
  workspace_id, agent_id, runtime_id, status, priority, reason, requires_wake,
  initiator_user_id, trigger_summary, terminal_outcome, completed_at, created_at, issue_id
)
VALUES ($1, $2, $3, 'acked', 0, 'issue', false, $4, $5, 'completed', $6, $6, $7)
RETURNING id`,
		testWorkspaceID, keepAgent, testRuntimeID, testUserID,
		"please implement the login fix now", now.Add(-30*time.Minute), issueID,
	).Scan(&keepRun); err != nil {
		t.Fatalf("insert keep run: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
INSERT INTO agent_inbox_event (
  workspace_id, agent_id, runtime_id, status, priority, reason, requires_wake,
  initiator_user_id, trigger_summary, terminal_outcome, completed_at, created_at
)
VALUES ($1, $2, $3, 'acked', 0, 'note_worker', false, $4, $5, 'no_reply', $6, $6)
RETURNING id`,
		testWorkspaceID, keepAgent, testRuntimeID, testUserID,
		"Write a Period Work Brief for a manager or colleague — a reporting narrative",
		now.Add(-25*time.Minute),
	).Scan(&workerRun); err != nil {
		t.Fatalf("insert note_worker run: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
INSERT INTO agent_inbox_event (
  workspace_id, agent_id, runtime_id, status, priority, reason, requires_wake,
  initiator_user_id, trigger_summary, terminal_outcome, completed_at, created_at
)
VALUES ($1, $2, $3, 'acked', 0, 'note_worker', false, $4, $5, 'no_reply', $6, $6)
RETURNING id`,
		testWorkspaceID, collectorID, testRuntimeID, testUserID,
		"Collect recent work on the OS where this runtime runs for 2026-W34",
		now.Add(-20*time.Minute),
	).Scan(&collectorRun); err != nil {
		t.Fatalf("insert collector run: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM agent_inbox_event WHERE id = $1`, keepRun)
		_, _ = testPool.Exec(ctx, `DELETE FROM agent_inbox_event WHERE id = $1`, workerRun)
		_, _ = testPool.Exec(ctx, `DELETE FROM agent_inbox_event WHERE id = $1`, collectorRun)
	})

	bundle, err := testHandler.loadNotePeriodBriefFactsBundle(
		ctx, parseUUID(testWorkspaceID), parseUUID(testUserID),
		start, end,
		[]string{noteRetrospectiveSourceIssue, noteRetrospectiveSourceNotes, noteRetrospectiveSourceRuns},
	)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if containsNoteRetrospectiveSource(bundle.SourcesUsed, noteRetrospectiveSourceNotes) {
		t.Fatalf("touched_notes used: %v", bundle.SourcesUsed)
	}
	if !containsNoteRetrospectiveSource(bundle.SourcesSkipped, noteRetrospectiveSourceNotes) {
		t.Fatalf("touched_notes not skipped: %v", bundle.SourcesSkipped)
	}
	if len(bundle.Facts.Notes) != 0 {
		t.Fatalf("notes leaked: %+v", bundle.Facts.Notes)
	}
	ids := noteRetrospectiveRunIDs(bundle.Facts.Runs)
	if !ids.has(keepRun) {
		t.Fatalf("kept issue run missing: %+v", bundle.Facts.Runs)
	}
	if ids.has(workerRun) || ids.has(collectorRun) {
		t.Fatalf("machinery runs leaked: %+v", bundle.Facts.Runs)
	}

	factsText := formatNotePeriodBriefFacts(bundle.Facts)
	if strings.Contains(factsText, "Write a Period Work Brief") ||
		strings.Contains(factsText, "Collect recent work") ||
		strings.Contains(factsText, "please implement") ||
		strings.Contains(factsText, "Touched notes") {
		t.Fatalf("dirty fact text:\n%s", factsText)
	}
}
