package researchrun

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type integrationRetrievalAdapter struct{ document RetrievalDocument }

func (a integrationRetrievalAdapter) Search(context.Context, RetrievalSearchRequest) (RetrievalSearchPage, error) {
	return RetrievalSearchPage{}, fmt.Errorf("search is not used by screened ingestion")
}

func (a integrationRetrievalAdapter) Fetch(context.Context, RetrievalFetchRequest) (RetrievalDocument, error) {
	return a.document, nil
}

func TestAcceptV6PlanMaterializesInquiryTasksAndTargetsAtomically(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	fixture := seedResearchRunFixture(t, ctx, pool)
	defer cleanupResearchRunFixture(pool, fixture)
	store := NewPostgresStore(pool)
	run, _, err := store.CreateRun(ctx, StartInput{WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID, CreatedBy: fixture.userID,
		LeadAgentID: fixture.agentID, Goal: "Build a real V6 inquiry plan", Title: "V6 plan acceptance", DepthTier: "standard", Language: "English"}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := store.ListTasks(ctx, run.SessionID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks=%+v err=%v", tasks, err)
	}
	attempt, _, err := store.CreateDispatchIntent(ctx, testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID))
	if err != nil {
		t.Fatal(err)
	}
	inboxID := seedIntegrationInboxEvent(t, ctx, pool, fixture.workspaceID, fixture.agentID)
	if _, _, err = store.AttachInboxTask(ctx, attempt.ID, inboxID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_session SET orchestrator_version=$2 WHERE id=$1::uuid`, run.SessionID, OrchestratorVersionV6); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_task SET expected_result='research_plan_v6',status='running',started_at=now() WHERE id=$1::uuid`, tasks[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_task_attempt SET status='running',started_at=now() WHERE id=$1::uuid`, attempt.ID); err != nil {
		t.Fatal(err)
	}

	raw := encodeResearchV6PlanFixture(t, validResearchV6PlanFixture())
	plan, hash, err := DecodeAndValidateResearchV6PlanResult(raw)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := store.AcceptResult(ctx, AcceptResultInput{SessionID: run.SessionID, AttemptID: attempt.ID, AgentID: fixture.agentID,
		InboxTaskID: inboxID, Raw: raw, Result: researchV6PlanEnvelope(plan), V6Plan: &plan, Hash: hash})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.QuestionsCreated != 1 || outcome.TasksCreated != 1 {
		t.Fatalf("outcome=%+v", outcome)
	}
	var questions, hypotheses, branches, edges, executionTasks, targets, graphEvents int
	if err = pool.QueryRow(ctx, `SELECT
		(SELECT count(*)::int FROM research_question WHERE session_id=$1::uuid AND client_key='q.root'),
		(SELECT count(*)::int FROM research_hypothesis WHERE session_id=$1::uuid AND client_key='h.primary'),
		(SELECT count(*)::int FROM research_branch WHERE session_id=$1::uuid AND client_key='b.primary'),
		(SELECT count(*)::int FROM research_inquiry_edge WHERE session_id=$1::uuid AND client_key='edge.tests'),
		(SELECT count(*)::int FROM research_task WHERE session_id=$1::uuid AND client_key='task.discover'),
		(SELECT count(*)::int FROM research_task_inquiry_target WHERE session_id=$1::uuid),
		(SELECT count(*)::int FROM research_run_event WHERE session_id=$1::uuid AND event_type='v6_plan_materialized')`, run.SessionID).Scan(
		&questions, &hypotheses, &branches, &edges, &executionTasks, &targets, &graphEvents); err != nil {
		t.Fatal(err)
	}
	if questions != 1 || hypotheses != 1 || branches != 1 || edges != 1 || executionTasks != 1 || targets != 1 || graphEvents != 1 {
		t.Fatalf("q=%d h=%d b=%d e=%d tasks=%d targets=%d events=%d", questions, hypotheses, branches, edges, executionTasks, targets, graphEvents)
	}

	// Dispatch is still production-gated at V5 until F-N exits are complete;
	// use the frozen V5 dispatcher only to create the real Attempt/Manifest,
	// then prove the immutable V6 result adapter and transaction.
	if _, err = pool.Exec(ctx, `UPDATE research_session SET orchestrator_version=$2 WHERE id=$1::uuid`, run.SessionID, OrchestratorVersionV5); err != nil {
		t.Fatal(err)
	}
	tasks, err = store.ListTasks(ctx, run.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	var evidenceTask Task
	for _, task := range tasks {
		if task.ClientKey == "task.discover" {
			evidenceTask = task
		}
	}
	if evidenceTask.ID == "" {
		t.Fatal("V6 evidence task is missing")
	}
	evidenceAttempt, _, err := store.CreateDispatchIntent(ctx, testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, evidenceTask.ID, fixture.agentID))
	if err != nil {
		t.Fatal(err)
	}
	evidenceInboxID := seedIntegrationInboxEvent(t, ctx, pool, fixture.workspaceID, fixture.agentID)
	if _, _, err = store.AttachInboxTask(ctx, evidenceAttempt.ID, evidenceInboxID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_session SET orchestrator_version=$2 WHERE id=$1::uuid`, run.SessionID, OrchestratorVersionV6); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_task SET status='running',started_at=now() WHERE id=$1::uuid`, evidenceTask.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_task_attempt SET status='running',started_at=now() WHERE id=$1::uuid`, evidenceAttempt.ID); err != nil {
		t.Fatal(err)
	}
	evidenceFixture := validV6EvidenceResultFixture()
	fetchedContent := []byte("Fetched immutable source content")
	fetchedHash := fmt.Sprintf("sha256:%x", sha256.Sum256(fetchedContent))
	evidenceFixture["source_candidates"].([]any)[0].(map[string]any)["content_hash"] = fetchedHash
	evidenceFixture["status_updates"] = []any{map[string]any{
		"target": map[string]any{"kind": "hypothesis", "key": "h.primary"}, "before": "proposed", "after": "investigating",
		"reason": "The screened query establishes a concrete verification path", "evidence_refs": []any{map[string]any{"kind": "task", "key": "task.discover"}},
	}}
	evidenceRaw := encodeV6EvidenceFixture(t, evidenceFixture)
	evidenceResult, evidenceHash, err := DecodeAndValidateV6EvidenceResult(evidenceRaw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AcceptResult(ctx, AcceptResultInput{SessionID: run.SessionID, AttemptID: evidenceAttempt.ID, AgentID: fixture.agentID,
		InboxTaskID: evidenceInboxID, Raw: evidenceRaw, Result: researchV6EvidenceEnvelope(evidenceResult), V6Evidence: &evidenceResult, Hash: evidenceHash}); err != nil {
		t.Fatal(err)
	}
	var searchPlans, queries, candidates, decisions, searchPassports, transitions int
	var hypothesisStatus string
	if err = pool.QueryRow(ctx, `SELECT
		(SELECT count(*)::int FROM research_search_plan WHERE session_id=$1::uuid),
		(SELECT count(*)::int FROM research_query_execution WHERE session_id=$1::uuid),
		(SELECT count(*)::int FROM research_source_candidate WHERE session_id=$1::uuid),
		(SELECT count(*)::int FROM research_screening_decision WHERE session_id=$1::uuid),
		(SELECT count(*)::int FROM research_artifact_passport WHERE session_id=$1::uuid AND entity_kind IN ('search_plan','query_execution','source_candidate','screening_decision')),
		(SELECT count(*)::int FROM research_inquiry_status_transition WHERE session_id=$1::uuid),
		(SELECT status FROM research_hypothesis WHERE session_id=$1::uuid AND client_key='h.primary')`, run.SessionID).Scan(
		&searchPlans, &queries, &candidates, &decisions, &searchPassports, &transitions, &hypothesisStatus); err != nil {
		t.Fatal(err)
	}
	if searchPlans != 1 || queries != 1 || candidates != 1 || decisions != 1 || searchPassports != 4 || transitions != 1 || hypothesisStatus != "investigating" {
		t.Fatalf("plans=%d queries=%d candidates=%d decisions=%d passports=%d transitions=%d hypothesis=%s", searchPlans, queries, candidates, decisions, searchPassports, transitions, hypothesisStatus)
	}
	var candidateID string
	if err = pool.QueryRow(ctx, `SELECT id::text FROM research_source_candidate WHERE session_id=$1::uuid AND client_key='source.primary'`, run.SessionID).Scan(&candidateID); err != nil {
		t.Fatal(err)
	}
	document := RetrievalDocument{Adapter: "web", CanonicalURL: "https://example.com/evidence", CanonicalIdentity: "https://example.com/evidence",
		MIME: "text/plain", Content: fetchedContent, ContentHash: fetchedHash,
		Safety: RetrievalSafety{RequestedURL: "https://example.com/evidence", FinalURL: "https://example.com/evidence", ResolvedAddresses: []string{"93.184.216.34"}, ScanDisposition: "safe", ResponseBytes: int64(len(fetchedContent))}}
	ingested, err := store.FetchAndIngestScreenedSource(ctx, FetchScreenedSourceInput{WorkspaceID: fixture.workspaceID, SessionID: run.SessionID,
		CandidateID: candidateID, MaximumContentSize: 1 << 20}, integrationRetrievalAdapter{document: document})
	if err != nil {
		t.Fatal(err)
	}
	lineage, err := store.SourceSearchLineage(ctx, fixture.workspaceID, run.SessionID, ingested.SourceSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if lineage.IngestionKind != string(SourceIngestionScreenedRetrieval) || lineage.Disposition != "accepted" || lineage.SourceCandidateID != candidateID {
		t.Fatalf("source lineage=%+v", lineage)
	}
}
