package handler

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLoadNoteRetrospectiveFactsBundleWindowAndAttribution(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	now := time.Now().UTC()
	start := now.Add(-2 * time.Hour)
	end := now.Add(time.Hour)

	inIssue, _ := createIssueForNoteRefTest(t, testWorkspaceID, "In-window issue "+uuid.NewString())
	outIssue, _ := createIssueForNoteRefTest(t, testWorkspaceID, "Out-window issue "+uuid.NewString())
	delegatedIssue, _ := createIssueForNoteRefTest(t, testWorkspaceID, "Delegated issue "+uuid.NewString())
	relatedIssue, _ := createIssueForNoteRefTest(t, testWorkspaceID, "Related issue "+uuid.NewString())

	inNote := createNotePageForAITest(t, "In-window note "+uuid.NewString())
	outNote := createNotePageForAITest(t, "Out-window note "+uuid.NewString())

	agentID := createHandlerTestAgent(t, "Facts Agent "+uuid.NewString()[:8], nil)
	otherUserID := createWorkspaceMemberUser(t, "Facts Other", "facts-other-"+uuid.NewString()+"@multica.test")

	if _, err := testPool.Exec(ctx, `
UPDATE note_page SET updated_at = $2 WHERE id = $1`, inNote, now.Add(-30*time.Minute)); err != nil {
		t.Fatalf("touch in-window note: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
UPDATE note_page SET updated_at = $2 WHERE id = $1`, outNote, now.Add(-48*time.Hour)); err != nil {
		t.Fatalf("touch out-window note: %v", err)
	}

	if _, err := testPool.Exec(ctx, `
INSERT INTO activity_log (workspace_id, issue_id, actor_type, actor_id, action, details, created_at)
VALUES
  ($1, $2, 'member', $3, 'status_changed', '{"from":"todo","to":"done"}'::jsonb, $6),
  ($1, $4, 'member', $3, 'status_changed', '{"from":"todo","to":"done"}'::jsonb, $7),
  ($1, $5, 'member', $3, 'assignee_changed', jsonb_build_object('to_type','agent','to_id',$8::text), $6 + interval '10 seconds'),
  ($1, $9, 'member', $10::uuid, 'status_changed', '{"from":"todo","to":"in_progress"}'::jsonb, $6 + interval '20 seconds')
`, testWorkspaceID, inIssue, testUserID, outIssue, delegatedIssue, now.Add(-time.Hour), now.Add(-72*time.Hour), agentID, relatedIssue, otherUserID); err != nil {
		t.Fatalf("insert activities: %v", err)
	}
	// Related issue must be related to the viewer (creator) so the other member's
	// action is included and bucketed as related.
	if _, err := testPool.Exec(ctx, `
UPDATE issue SET creator_type = 'member', creator_id = $2 WHERE id = $1`, relatedIssue, testUserID); err != nil {
		t.Fatalf("mark related issue creator: %v", err)
	}

	var inRunID, outRunID string
	if err := testPool.QueryRow(ctx, `
INSERT INTO agent_inbox_event (
  workspace_id, agent_id, runtime_id, status, priority, reason, requires_wake,
  initiator_user_id, trigger_summary, terminal_outcome, completed_at, created_at
)
VALUES ($1, $2, $3, 'acked', 0, 'issue', false, $4, $5, 'completed', $6, $6)
RETURNING id`,
		testWorkspaceID, agentID, testRuntimeID, testUserID,
		"In-window run summary", now.Add(-45*time.Minute),
	).Scan(&inRunID); err != nil {
		t.Fatalf("insert in-window run: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
INSERT INTO agent_inbox_event (
  workspace_id, agent_id, runtime_id, status, priority, reason, requires_wake,
  initiator_user_id, trigger_summary, terminal_outcome, completed_at, created_at
)
VALUES ($1, $2, $3, 'acked', 0, 'issue', false, $4, $5, 'completed', $6, $6)
RETURNING id`,
		testWorkspaceID, agentID, testRuntimeID, testUserID,
		"Out-window run summary", now.Add(-96*time.Hour),
	).Scan(&outRunID); err != nil {
		t.Fatalf("insert out-window run: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM agent_inbox_event WHERE id = $1`, inRunID)
		_, _ = testPool.Exec(ctx, `DELETE FROM agent_inbox_event WHERE id = $1`, outRunID)
	})

	ws := parseUUID(testWorkspaceID)
	user := parseUUID(testUserID)
	bundle, err := testHandler.loadNoteRetrospectiveFactsBundle(ctx, ws, user, start, end, notePeriodWorkDefaultSources)
	if err != nil {
		t.Fatalf("load facts: %v", err)
	}
	if bundle.FactCount() < 5 {
		t.Fatalf("fact_count=%d used=%v empty=%v issues=%d notes=%d runs=%d",
			bundle.FactCount(), bundle.SourcesUsed, bundle.SourcesEmpty,
			len(bundle.Facts.Issues), len(bundle.Facts.Notes), len(bundle.Facts.Runs))
	}
	for _, source := range notePeriodWorkDefaultSources {
		if !containsNoteRetrospectiveSource(bundle.SourcesUsed, source) {
			t.Fatalf("sources_used=%v missing %s", bundle.SourcesUsed, source)
		}
	}

	if !noteRetrospectiveIssueIDs(bundle.Facts.Issues).has(inIssue) {
		t.Fatalf("missing in-window issue: %+v", bundle.Facts.Issues)
	}
	if noteRetrospectiveIssueIDs(bundle.Facts.Issues).has(outIssue) {
		t.Fatalf("out-window issue leaked: %+v", bundle.Facts.Issues)
	}
	if !noteRetrospectiveNoteIDs(bundle.Facts.Notes).has(inNote) {
		t.Fatalf("missing in-window note: %+v", bundle.Facts.Notes)
	}
	if noteRetrospectiveNoteIDs(bundle.Facts.Notes).has(outNote) {
		t.Fatalf("out-window note leaked: %+v", bundle.Facts.Notes)
	}
	if !noteRetrospectiveRunIDs(bundle.Facts.Runs).has(inRunID) {
		t.Fatalf("missing in-window run: %+v", bundle.Facts.Runs)
	}
	if noteRetrospectiveRunIDs(bundle.Facts.Runs).has(outRunID) {
		t.Fatalf("out-window run leaked: %+v", bundle.Facts.Runs)
	}

	byIssue := map[string]string{}
	for _, fact := range bundle.Facts.Issues {
		byIssue[fact.IssueID] = fact.Attribution
	}
	if byIssue[inIssue] != noteRetrospectiveAttrHandsOn {
		t.Fatalf("in-window issue attribution=%q want hands_on", byIssue[inIssue])
	}
	if byIssue[delegatedIssue] != noteRetrospectiveAttrDelegated {
		t.Fatalf("delegated issue attribution=%q want delegated", byIssue[delegatedIssue])
	}
	if byIssue[relatedIssue] != noteRetrospectiveAttrRelated {
		t.Fatalf("related issue attribution=%q want related", byIssue[relatedIssue])
	}
	for _, fact := range bundle.Facts.Runs {
		if fact.RunID == inRunID && fact.Attribution != noteRetrospectiveAttrDelegated {
			t.Fatalf("run attribution=%q want delegated", fact.Attribution)
		}
	}
}

func TestLoadNoteRetrospectiveFactsBundleHonorsSourceFilter(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	now := time.Now().UTC()

	bundle, err := testHandler.loadNoteRetrospectiveFactsBundle(
		ctx, parseUUID(testWorkspaceID), parseUUID(testUserID),
		now.Add(-2*time.Hour), now.Add(time.Hour),
		[]string{noteRetrospectiveSourceIssue},
	)
	if err != nil {
		t.Fatalf("load facts: %v", err)
	}
	if len(bundle.Facts.Notes) != 0 || len(bundle.Facts.Runs) != 0 {
		t.Fatalf("disabled sources leaked notes=%d runs=%d", len(bundle.Facts.Notes), len(bundle.Facts.Runs))
	}
	if !containsNoteRetrospectiveSource(bundle.SourcesSkipped, noteRetrospectiveSourceNotes) ||
		!containsNoteRetrospectiveSource(bundle.SourcesSkipped, noteRetrospectiveSourceRuns) {
		t.Fatalf("sources_skipped=%v", bundle.SourcesSkipped)
	}
}

func TestLoadNoteRetrospectiveFactsBundleAttachesLinkedPullRequests(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	now := time.Now().UTC()

	linkedIssue, _ := createIssueForNoteRefTest(t, testWorkspaceID, "PR-linked issue "+uuid.NewString())
	bareIssue, _ := createIssueForNoteRefTest(t, testWorkspaceID, "Bare issue "+uuid.NewString())

	var pullRequestID string
	if err := testPool.QueryRow(ctx, `
INSERT INTO github_pull_request (
  workspace_id, installation_id, repo_owner, repo_name, pr_number,
  title, state, html_url, pr_created_at, pr_updated_at
)
VALUES ($1, 1, 'multica-ai', 'multica', 4242,
        'wire SSO login', 'open', 'https://github.com/multica-ai/multica/pull/4242', now(), now())
RETURNING id`, testWorkspaceID).Scan(&pullRequestID); err != nil {
		t.Fatalf("insert PR: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM issue_pull_request WHERE pull_request_id = $1`, pullRequestID)
		_, _ = testPool.Exec(ctx, `DELETE FROM github_pull_request WHERE id = $1`, pullRequestID)
	})
	if _, err := testPool.Exec(ctx, `
INSERT INTO issue_pull_request (issue_id, pull_request_id) VALUES ($1, $2)`,
		linkedIssue, pullRequestID); err != nil {
		t.Fatalf("link PR: %v", err)
	}

	if _, err := testPool.Exec(ctx, `
INSERT INTO activity_log (workspace_id, issue_id, actor_type, actor_id, action, details, created_at)
VALUES
  ($1, $2, 'member', $3, 'status_changed', '{"from":"todo","to":"done"}'::jsonb, $5),
  ($1, $4, 'member', $3, 'status_changed', '{"from":"todo","to":"done"}'::jsonb, $5 + interval '5 seconds')
`, testWorkspaceID, linkedIssue, testUserID, bareIssue, now.Add(-time.Minute)); err != nil {
		t.Fatalf("insert activities: %v", err)
	}

	bundle, err := testHandler.loadNoteRetrospectiveFactsBundle(
		ctx, parseUUID(testWorkspaceID), parseUUID(testUserID),
		now.Add(-2*time.Hour), now.Add(time.Hour),
		[]string{noteRetrospectiveSourceIssue},
	)
	if err != nil {
		t.Fatalf("load facts: %v", err)
	}

	byIssue := map[string]noteRetrospectiveIssueFact{}
	for _, fact := range bundle.Facts.Issues {
		byIssue[fact.IssueID] = fact
	}
	linked, ok := byIssue[linkedIssue]
	if !ok {
		t.Fatalf("missing linked issue fact: %+v", bundle.Facts.Issues)
	}
	if len(linked.PullRequests) != 1 {
		t.Fatalf("linked PRs = %+v, want 1", linked.PullRequests)
	}
	pr := linked.PullRequests[0]
	if pr.Number != 4242 || pr.State != "open" || pr.Title != "wire SSO login" ||
		pr.URL != "https://github.com/multica-ai/multica/pull/4242" {
		t.Fatalf("PR fact = %+v", pr)
	}
	bare, ok := byIssue[bareIssue]
	if !ok {
		t.Fatalf("missing bare issue fact: %+v", bundle.Facts.Issues)
	}
	if bare.PullRequests == nil || len(bare.PullRequests) != 0 {
		t.Fatalf("bare issue PRs = %#v, want empty slice", bare.PullRequests)
	}
}

type noteRetrospectiveIDSet map[string]struct{}

func (s noteRetrospectiveIDSet) has(id string) bool {
	_, ok := s[id]
	return ok
}

func noteRetrospectiveIssueIDs(facts []noteRetrospectiveIssueFact) noteRetrospectiveIDSet {
	out := noteRetrospectiveIDSet{}
	for _, fact := range facts {
		out[fact.IssueID] = struct{}{}
	}
	return out
}

func noteRetrospectiveNoteIDs(facts []noteRetrospectiveNoteFact) noteRetrospectiveIDSet {
	out := noteRetrospectiveIDSet{}
	for _, fact := range facts {
		out[fact.PageID] = struct{}{}
	}
	return out
}

func noteRetrospectiveRunIDs(facts []noteRetrospectiveRunFact) noteRetrospectiveIDSet {
	out := noteRetrospectiveIDSet{}
	for _, fact := range facts {
		out[fact.RunID] = struct{}{}
	}
	return out
}
