package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPolicyWatermarkCASRejectsStaleExpected(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	fixture := seedResearchRunFixture(t, ctx, pool)
	defer cleanupResearchRunFixture(pool, fixture)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err = ensureSessionPolicyStateTx(ctx, tx, fixture.workspaceID, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	expected, err := readPolicyWatermarkTx(ctx, tx, fixture.workspaceID, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reservePolicyWatermarkCASTx(ctx, tx, fixture.workspaceID, fixture.sessionID, expected); err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	if _, err = reservePolicyWatermarkCASTx(ctx, tx, fixture.workspaceID, fixture.sessionID, expected); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("second reserve err=%v want ErrInvalidTransition", err)
	}
}

func TestPassportEligibilityCASRejectsStaleRevision(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	fixture := seedResearchRunFixture(t, ctx, pool)
	defer cleanupResearchRunFixture(pool, fixture)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err = casPassportEligibilityRevisionTx(
		ctx, tx, fixture.workspaceID, fixture.sessionID, fixture.sessionID, 1, 1, ArtifactLifecycleRegistered,
	); err != nil {
		t.Fatalf("valid CAS: %v", err)
	}
	if err = casPassportEligibilityRevisionTx(
		ctx, tx, fixture.workspaceID, fixture.sessionID, fixture.sessionID, 1, 99, ArtifactLifecycleRegistered,
	); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("stale CAS err=%v want ErrInvalidTransition", err)
	}
}

func TestReplayDispatchPromptMatchesOutboxAfterDispatch(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
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
		LeadAgentID: fixture.agentID, Goal: "Prompt replay", Title: "Prompt replay",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	tasks, err := store.ListTasks(ctx, run.SessionID)
	if err != nil || len(tasks) == 0 {
		t.Fatalf("ListTasks: %v len=%d", err, len(tasks))
	}
	input := testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID)
	attempt, _, err := store.CreateDispatchIntent(ctx, input)
	if err != nil {
		t.Fatalf("CreateDispatchIntent: %v", err)
	}
	var outboxPrompt string
	if err = pool.QueryRow(ctx, `
		SELECT request_payload->>'prompt'
		FROM research_dispatch_outbox
		WHERE attempt_id = $1::uuid
	`, attempt.ID).Scan(&outboxPrompt); err != nil {
		t.Fatalf("load outbox prompt: %v", err)
	}
	replayed, err := replayDispatchPromptFromManifest(ctx, store, fixture.workspaceID, attempt.ID)
	if err != nil {
		t.Fatalf("replayDispatchPromptFromManifest: %v", err)
	}
	if replayed != outboxPrompt {
		t.Fatalf("replayed prompt differs from outbox")
	}
}

func TestHistoricalTaskContextAdvancesWhileFrozenManifestStable(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
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
		LeadAgentID: fixture.agentID, Goal: "Historical live reads", Title: "Historical live",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	tasks, err := store.ListTasks(ctx, run.SessionID)
	if err != nil || len(tasks) == 0 {
		t.Fatalf("ListTasks: %v len=%d", err, len(tasks))
	}
	input := testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID)
	attempt, _, err := store.CreateDispatchIntent(ctx, input)
	if err != nil {
		t.Fatalf("CreateDispatchIntent: %v", err)
	}
	frozenBefore, err := store.TaskContextForAttempt(ctx, attempt.ID, fixture.workspaceID)
	if err != nil {
		t.Fatalf("TaskContextForAttempt before: %v", err)
	}
	sourceID := uuid.NewString()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_source_snapshot (
		  id, workspace_id, session_id, canonical_url, title, publisher, source_class,
		  evidence_traits, independence_key, retrieved_at, content_hash, snapshot_text, metadata,
		  verification_status
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, 'https://example.test/post-dispatch', 'Post dispatch', 'example.test',
		  'primary', '{}'::text[], 'example.test', now(), 'sha256:post', 'post snapshot', '{}'::jsonb,
		  'verified'
		)
	`, sourceID, fixture.workspaceID, run.SessionID); err != nil {
		t.Fatalf("insert source: %v", err)
	}
	backfillIntegrationArtifactPassport(t, ctx, tx, fixture.workspaceID, run.SessionID, sourceID, string(ArtifactKindSourceSnapshot), nil, nil)
	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit source and passport: %v", err)
	}

	live, err := store.TaskContext(ctx, tasks[0].ID, fixture.workspaceID)
	if err != nil {
		t.Fatalf("TaskContext live: %v", err)
	}
	frozenAfter, err := store.TaskContextForAttempt(ctx, attempt.ID, fixture.workspaceID)
	if err != nil {
		t.Fatalf("TaskContextForAttempt after: %v", err)
	}
	if len(live.Sources) != len(frozenBefore.Sources)+1 {
		t.Fatalf("live sources before=%d after=%d", len(frozenBefore.Sources), len(live.Sources))
	}
	if len(frozenAfter.Sources) != len(frozenBefore.Sources) {
		t.Fatalf("frozen sources changed before=%d after=%d", len(frozenBefore.Sources), len(frozenAfter.Sources))
	}
}

func TestAttemptContextManifestMetadataStableAcrossLiveMutation(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
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
		LeadAgentID: fixture.agentID, Goal: "Stable attempt context", Title: "Stable attempt context",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	tasks, err := store.ListTasks(ctx, run.SessionID)
	if err != nil || len(tasks) == 0 {
		t.Fatalf("ListTasks: %v len=%d", err, len(tasks))
	}
	input := testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID)
	attempt, _, err := store.CreateDispatchIntent(ctx, input)
	if err != nil {
		t.Fatalf("CreateDispatchIntent: %v", err)
	}

	readContext := func() AttemptArtifactContext {
		t.Helper()
		snapshot, readErr := store.TaskContextForAttempt(ctx, attempt.ID, fixture.workspaceID)
		if readErr != nil {
			t.Fatalf("TaskContextForAttempt: %v", readErr)
		}
		if snapshot.AttemptContext == nil {
			t.Fatal("expected attempt_context")
		}
		return *snapshot.AttemptContext
	}

	before := readContext()
	replayed := readContext()
	if before.ManifestID != replayed.ManifestID || before.ManifestHash != replayed.ManifestHash {
		t.Fatalf("replay drift before=%+v replayed=%+v", before, replayed)
	}

	sourceID := uuid.NewString()
	if _, err = pool.Exec(ctx, `
		INSERT INTO research_source_snapshot (
		  id, workspace_id, session_id, canonical_url, title, publisher, source_class,
		  evidence_traits, independence_key, retrieved_at, content_hash, snapshot_text, metadata,
		  verification_status
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, 'https://example.test/post-live', 'Post live', 'example.test',
		  'primary', '{}'::text[], 'example.test', now(), 'sha256:post-live', 'post live snapshot', '{}'::jsonb,
		  'verified'
		)
	`, sourceID, fixture.workspaceID, run.SessionID); err != nil {
		t.Fatalf("insert source: %v", err)
	}
	backfillIntegrationArtifactPassport(t, ctx, pool, fixture.workspaceID, run.SessionID, sourceID, string(ArtifactKindSourceSnapshot), nil, nil)

	afterLive := readContext()
	if afterLive.ManifestID != before.ManifestID || afterLive.ManifestHash != before.ManifestHash {
		t.Fatalf("live mutation changed manifest metadata before=%+v after=%+v", before, afterLive)
	}
	if !afterLive.ManifestFiltered {
		t.Fatal("expected manifest-filtered attempt context after live mutation")
	}
}

func TestDispatchFailsWhenPassportEligibilityAdvances(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
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
		LeadAgentID: fixture.agentID, Goal: "Eligibility CAS gate", Title: "Eligibility CAS",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	claimID := uuid.NewString()
	seedIntegrationClaimArtifact(t, ctx, pool, fixture.workspaceID, run.SessionID, claimID, "cas-claim", "claim for CAS")

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	plan, err := NewArtifactContextModule().PlanDispatchManifest(ctx, tx, fixture.workspaceID, run.SessionID, run.StateVersion)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := manifestEntryForArtifact(plan, claimID)
	if !ok {
		t.Fatalf("claim %s missing from manifest plan", claimID)
	}

	// Advance eligibility after the plan froze revision 1.
	mutateIntegrationArtifactForCASTest(t, ctx, pool, `
		UPDATE research_artifact_passport
		SET eligibility_revision = eligibility_revision + 1
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
	`, fixture.workspaceID, run.SessionID, claimID)
	err = casPassportEligibilityRevisionTx(
		ctx, tx, fixture.workspaceID, run.SessionID, claimID,
		entry.Version, entry.EligibilityRevision, entry.Lifecycle,
	)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("eligibility CAS err=%v want ErrInvalidTransition", err)
	}
}

func TestDispatchFailsWhenArtifactVersionContentHashChanges(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
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
		LeadAgentID: fixture.agentID, Goal: "Representation CAS gate", Title: "Representation CAS",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	claimID := uuid.NewString()
	seedIntegrationClaimArtifact(t, ctx, pool, fixture.workspaceID, run.SessionID, claimID, "repr-cas-claim", "claim for representation CAS")

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	plan, err := NewArtifactContextModule().PlanDispatchManifest(ctx, tx, fixture.workspaceID, run.SessionID, run.StateVersion)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := manifestEntryForArtifact(plan, claimID)
	if !ok {
		t.Fatalf("claim %s missing from manifest plan", claimID)
	}
	mutatedHash := contentHashFromPayload([]byte("mutated after manifest plan"))
	mutateIntegrationArtifactForCASTest(t, ctx, pool, `
		UPDATE research_artifact_version
		SET content_hash = $4
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND artifact_id = $3::uuid
	`, fixture.workspaceID, run.SessionID, claimID, mutatedHash)
	err = casArtifactVersionRepresentationTx(
		ctx, tx, fixture.workspaceID, run.SessionID, entry.VersionRowID,
		entry.ContentHash, entry.RepresentationHash,
	)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("representation CAS err=%v want ErrInvalidTransition", err)
	}
}

func manifestEntryForArtifact(plan dispatchManifestPlan, artifactID string) (artifactVersionCandidate, bool) {
	for _, entry := range plan.Entries {
		if entry.ArtifactID == artifactID {
			return entry, true
		}
	}
	return artifactVersionCandidate{}, false
}

func TestTaskContextForAttemptUsesFrozenGateSnapshot(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
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
		LeadAgentID: fixture.agentID, Goal: "Frozen gate snapshot", Title: "Frozen gate",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	tasks, err := store.ListTasks(ctx, run.SessionID)
	if err != nil || len(tasks) == 0 {
		t.Fatalf("ListTasks: %v len=%d", err, len(tasks))
	}
	input := testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID)
	attempt, _, err := store.CreateDispatchIntent(ctx, input)
	if err != nil {
		t.Fatalf("CreateDispatchIntent: %v", err)
	}
	var headerBytes []byte
	if err = pool.QueryRow(ctx, `
		SELECT principal_header_bytes
		FROM research_artifact_context_manifest
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND attempt_id = $3::uuid
	`, fixture.workspaceID, run.SessionID, attempt.ID).Scan(&headerBytes); err != nil {
		t.Fatalf("load gate snapshot: %v", err)
	}
	if len(headerBytes) == 0 {
		t.Fatal("expected frozen gate snapshot bytes on manifest")
	}

	frozen, err := store.TaskContextForAttempt(ctx, attempt.ID, fixture.workspaceID)
	if err != nil {
		t.Fatalf("TaskContextForAttempt: %v", err)
	}
	frozenCount, ok := gateFindingCount(frozen.Gate, "tasks_incomplete")
	if !ok {
		t.Fatalf("frozen gate missing tasks_incomplete: %+v", frozen.Gate)
	}

	extraTaskID := uuid.NewString()
	if _, err = pool.Exec(ctx, `
		INSERT INTO research_task (
		  id, workspace_id, session_id, client_key, kind, objective,
		  required_capability, expected_result, status, goal_version, plan_version,
		  max_attempts, timeout_seconds, ready_at
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, 'post-dispatch-extra', 'discover', 'extra task',
		  'lead', 'research_evidence_v1', 'pending', $4, $5, 1, 300, NULL
		)
	`, extraTaskID, fixture.workspaceID, run.SessionID, run.GoalVersion, run.PlanVersion); err != nil {
		t.Fatalf("insert extra task: %v", err)
	}

	liveGate, err := store.EvaluateGate(ctx, run.SessionID)
	if err != nil {
		t.Fatalf("EvaluateGate: %v", err)
	}
	liveCount, ok := gateFindingCount(liveGate, "tasks_incomplete")
	if !ok {
		t.Fatalf("live gate missing tasks_incomplete: %+v", liveGate)
	}
	if liveCount <= frozenCount {
		t.Fatalf("live unfinished=%d frozen=%d want live > frozen", liveCount, frozenCount)
	}

	frozenAfter, err := store.TaskContextForAttempt(ctx, attempt.ID, fixture.workspaceID)
	if err != nil {
		t.Fatalf("TaskContextForAttempt after mutation: %v", err)
	}
	if !gateResultsEqual(frozen.Gate, frozenAfter.Gate) {
		t.Fatalf("frozen gate drifted before=%+v after=%+v", frozen.Gate, frozenAfter.Gate)
	}
	if gateResultsEqual(frozenAfter.Gate, liveGate) {
		t.Fatalf("frozen gate should differ from live gate after mutation")
	}
}

func gateFindingCount(gate GateResult, code string) (int, bool) {
	for _, finding := range gate.Findings {
		if finding.Code != code {
			continue
		}
		raw, ok := finding.Metadata["count"]
		if !ok {
			return 0, true
		}
		switch v := raw.(type) {
		case float64:
			return int(v), true
		case int:
			return v, true
		case int64:
			return int(v), true
		default:
			return 0, true
		}
	}
	return 0, false
}

func gateResultsEqual(a, b GateResult) bool {
	left, err := json.Marshal(a)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return string(left) == string(right)
}

func cleanupResearchRunFixture(pool *pgxpool.Pool, fixture researchRunFixture) {
	_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1::uuid`, fixture.workspaceID)
	_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1::uuid`, fixture.userID)
}

func intPtr(v int) *int { return &v }
