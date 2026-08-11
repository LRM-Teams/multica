package researchrun

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	lockOrderClaimLowID  = "10000000-0000-4000-8000-000000000001"
	lockOrderClaimHighID = "20000000-0000-4000-8000-000000000002"
)

func TestAcceptResultConcurrentOppositePayloadOrderNoDeadlock(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	fixture := seedResearchRunFixture(t, ctx, pool)
	defer cleanupResearchRunFixture(pool, fixture)
	store := NewPostgresStore(pool)

	run, _, err := store.CreateRun(ctx, StartInput{
		WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID, CreatedBy: fixture.userID,
		LeadAgentID: fixture.agentID, Goal: "Accept lock order", Title: "Accept lock order",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	insertLockOrderSharedClaims(t, ctx, pool, fixture.workspaceID, run.SessionID)

	tasks, err := store.ListTasks(ctx, run.SessionID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("ListTasks: %v len=%d", err, len(tasks))
	}
	planTask := tasks[0]
	planAttempt, _, err := store.CreateDispatchIntent(ctx, testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, planTask.ID, fixture.agentID))
	if err != nil {
		t.Fatalf("CreateDispatchIntent plan: %v", err)
	}
	planInboxID := uuid.NewString()
	if _, _, err = store.AttachInboxTask(ctx, planAttempt.ID, planInboxID); err != nil {
		t.Fatalf("AttachInboxTask plan: %v", err)
	}
	planRaw, err := json.Marshal(validPlanResult(t))
	if err != nil {
		t.Fatal(err)
	}
	planResult, planHash, err := DecodeAndValidateResultForVersion(run.OrchestratorVersion, planRaw, planTask, run.Config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AcceptResult(ctx, AcceptResultInput{
		SessionID: run.SessionID, AttemptID: planAttempt.ID, AgentID: fixture.agentID,
		InboxTaskID: planInboxID, Raw: planRaw, Result: planResult, Hash: planHash,
	}); err != nil {
		t.Fatalf("AcceptResult plan: %v", err)
	}

	discoverTasks, err := listDiscoverTasks(ctx, store, run.SessionID)
	if err != nil || len(discoverTasks) == 0 {
		t.Fatalf("listDiscoverTasks: %v len=%d", err, len(discoverTasks))
	}
	if len(discoverTasks) == 1 {
		secondTaskID := uuid.NewString()
		if _, err = pool.Exec(ctx, `
			INSERT INTO research_task (
			  id, workspace_id, session_id, client_key, kind, objective,
			  required_capability, expected_result, status, goal_version, plan_version,
			  max_attempts, timeout_seconds, ready_at, question_id
			)
			SELECT
			  $1::uuid, t.workspace_id, t.session_id, 'discover-2', 'discover',
			  'Second discover pass', 'scout', 'research_evidence_v1', 'ready',
			  t.goal_version, t.plan_version, 2, 300, now(), t.question_id
			FROM research_task t
			WHERE t.id = $2::uuid
		`, secondTaskID, discoverTasks[0].ID); err != nil {
			t.Fatalf("insert second discover task: %v", err)
		}
		discoverTasks, err = listDiscoverTasks(ctx, store, run.SessionID)
		if err != nil || len(discoverTasks) < 2 {
			t.Fatalf("listDiscoverTasks after insert: %v len=%d", err, len(discoverTasks))
		}
	}

	type acceptJob struct {
		attemptID string
		inboxID   string
		raw       json.RawMessage
		result    ResultEnvelope
		hash      string
	}
	jobs := make([]acceptJob, 0, 2)
	for i, task := range discoverTasks[:2] {
		attempt, _, dispatchErr := store.CreateDispatchIntent(ctx, testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, task.ID, fixture.agentID))
		if dispatchErr != nil {
			t.Fatalf("CreateDispatchIntent discover[%d]: %v", i, dispatchErr)
		}
		inboxID := uuid.NewString()
		if _, _, attachErr := store.AttachInboxTask(ctx, attempt.ID, inboxID); attachErr != nil {
			t.Fatalf("AttachInboxTask discover[%d]: %v", i, attachErr)
		}
		evidence := evidenceResultWithReferenceOrder(i == 0)
		raw, marshalErr := json.Marshal(evidence)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		result, hash, decodeErr := DecodeAndValidateResultForVersion(run.OrchestratorVersion, raw, task, run.Config)
		if decodeErr != nil {
			t.Fatalf("DecodeAndValidateResultForVersion discover[%d]: %v", i, decodeErr)
		}
		jobs = append(jobs, acceptJob{
			attemptID: attempt.ID,
			inboxID:   inboxID,
			raw:       raw,
			result:    result,
			hash:      hash,
		})
	}

	errs := make([]error, len(jobs))
	var wg sync.WaitGroup
	for i, job := range jobs {
		wg.Add(1)
		go func(i int, job acceptJob) {
			defer wg.Done()
			_, errs[i] = store.AcceptResult(ctx, AcceptResultInput{
				SessionID: run.SessionID, AttemptID: job.attemptID, AgentID: fixture.agentID,
				InboxTaskID: job.inboxID, Raw: job.raw, Result: job.result, Hash: job.hash,
			})
		}(i, job)
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(45 * time.Second):
		t.Fatal("concurrent AcceptResult calls did not finish within timeout (possible deadlock)")
	}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("AcceptResult discover[%d]: %v", i, err)
		}
	}

	for i, job := range jobs {
		var attemptStatus string
		var resultArtifacts int
		if err = pool.QueryRow(ctx, `
			SELECT status FROM research_task_attempt WHERE id = $1::uuid
		`, job.attemptID).Scan(&attemptStatus); err != nil {
			t.Fatalf("load attempt[%d]: %v", i, err)
		}
		if attemptStatus != string(AttemptStatusSucceeded) {
			t.Fatalf("attempt[%d] status=%q want succeeded", i, attemptStatus)
		}
		if err = pool.QueryRow(ctx, `
			SELECT count(*)::int FROM research_result_artifact
			WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND attempt_id = $3::uuid
		`, fixture.workspaceID, run.SessionID, job.attemptID).Scan(&resultArtifacts); err != nil {
			t.Fatalf("count result artifacts[%d]: %v", i, err)
		}
		if resultArtifacts != 1 {
			t.Fatalf("attempt[%d] result artifacts=%d want 1", i, resultArtifacts)
		}
	}
}

func insertLockOrderSharedClaims(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	workspaceID, sessionID string,
) {
	t.Helper()
	for _, spec := range []struct {
		id, clientKey, text string
	}{
		{lockOrderClaimLowID, "lock-order-claim-low", "low-order shared claim"},
		{lockOrderClaimHighID, "lock-order-claim-high", "high-order shared claim"},
	} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO research_claim (
			  id, workspace_id, session_id, client_key, evidence_standard_key, claim_text,
			  significance, confidence, status, goal_version, plan_version, resolution
			) VALUES (
			  $1::uuid, $2::uuid, $3::uuid, $4, '', $5,
			  0.5, 0.5, 'proposed', 1, 1, ''
			)
		`, spec.id, workspaceID, sessionID, spec.clientKey, spec.text); err != nil {
			t.Fatalf("insert claim %s: %v", spec.clientKey, err)
		}
		backfillIntegrationArtifactPassport(t, ctx, pool, workspaceID, sessionID, spec.id, string(ArtifactKindClaim), intPtr(1), intPtr(1))
	}
	if lockOrderClaimLowID >= lockOrderClaimHighID {
		t.Fatalf("fixture UUID order invalid: low=%q high=%q", lockOrderClaimLowID, lockOrderClaimHighID)
	}
}

func listDiscoverTasks(ctx context.Context, store *PostgresStore, sessionID string) ([]Task, error) {
	tasks, err := store.ListTasks(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]Task, 0)
	for _, task := range tasks {
		if task.Kind == TaskKindDiscover {
			out = append(out, task)
		}
	}
	return out, nil
}

func evidenceResultWithReferenceOrder(lowBeforeHigh bool) ResultEnvelope {
	sourceLow := SourceProposal{
		ClientKey: "source-low", URL: "https://example.test/low", Title: "Low source",
		Publisher: "example.test", SourceClass: "primary", IndependenceKey: "example-low",
		RetrievedAt: time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC), SnapshotText: "Low source reports 1.",
	}
	sourceHigh := SourceProposal{
		ClientKey: "source-high", URL: "https://example.test/high", Title: "High source",
		Publisher: "example.test", SourceClass: "primary", IndependenceKey: "example-high",
		RetrievedAt: time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC), SnapshotText: "High source reports 2.",
	}
	observationLow := ObservationProposal{
		ClientKey: "observation-low", SourceKey: "source-low", Quote: "reports 1",
		Locator: "paragraph 1", Interpretation: "Low reading.",
	}
	observationHigh := ObservationProposal{
		ClientKey: "observation-high", SourceKey: "source-high", Quote: "reports 2",
		Locator: "paragraph 1", Interpretation: "High reading.",
	}
	claimLow := ClaimProposal{
		ClientKey: "claim-low", Text: "Low-order claim.", Significance: "medium",
		Confidence: 0.7, Status: ClaimStatusSupported,
		Evidence: []EvidenceProposal{{ObservationKey: "observation-low", Relation: "supports", Strength: 0.8}},
	}
	claimHigh := ClaimProposal{
		ClientKey: "claim-high", Text: "High-order claim.", Significance: "medium",
		Confidence: 0.7, Status: ClaimStatusSupported,
		Evidence: []EvidenceProposal{{ObservationKey: "observation-high", Relation: "supports", Strength: 0.8}},
	}
	result := ResultEnvelope{
		SchemaVersion:   1,
		ClientRequestID: uuid.NewString(),
		Summary:         "evidence with ordered references",
		CoverageDelta:   0.3,
		Confidence:      0.7,
	}
	if lowBeforeHigh {
		result.Sources = []SourceProposal{sourceLow, sourceHigh}
		result.Observations = []ObservationProposal{observationLow, observationHigh}
		result.Claims = []ClaimProposal{claimLow, claimHigh}
	} else {
		result.Sources = []SourceProposal{sourceHigh, sourceLow}
		result.Observations = []ObservationProposal{observationHigh, observationLow}
		result.Claims = []ClaimProposal{claimHigh, claimLow}
	}
	return result
}
