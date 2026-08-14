package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
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
	if err = casPassportSelectionTx(
		ctx, tx, fixture.workspaceID, fixture.sessionID, fixture.sessionID,
		ArtifactKindRunSession, 1, 1, ArtifactLifecycleRegistered, ArtifactProvenancePartial,
	); err != nil {
		t.Fatalf("valid CAS: %v", err)
	}
	if err = casPassportSelectionTx(
		ctx, tx, fixture.workspaceID, fixture.sessionID, fixture.sessionID,
		ArtifactKindRunSession, 1, 99, ArtifactLifecycleRegistered, ArtifactProvenancePartial,
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
	if len(frozenBefore.Questions) == 0 || len(frozenBefore.Tasks) == 0 {
		t.Fatalf("frozen core fixture is incomplete: questions=%d tasks=%d", len(frozenBefore.Questions), len(frozenBefore.Tasks))
	}
	if _, err = pool.Exec(ctx, `UPDATE research_session SET goal='live goal changed after dispatch' WHERE id=$1::uuid`, run.SessionID); err != nil {
		t.Fatalf("mutate live run: %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_question SET question='live question changed after dispatch' WHERE id=$1::uuid`, frozenBefore.Questions[0].ID); err != nil {
		t.Fatalf("mutate live question: %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_task SET objective='live task changed after dispatch' WHERE id=$1::uuid`, frozenBefore.Tasks[0].ID); err != nil {
		t.Fatalf("mutate live task: %v", err)
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
	if live.Run.Goal == frozenBefore.Run.Goal || frozenAfter.Run.Goal != frozenBefore.Run.Goal {
		t.Fatalf("run goal live=%q frozen_before=%q frozen_after=%q", live.Run.Goal, frozenBefore.Run.Goal, frozenAfter.Run.Goal)
	}
	if live.Questions[0].Question == frozenBefore.Questions[0].Question || frozenAfter.Questions[0].Question != frozenBefore.Questions[0].Question {
		t.Fatalf("question live=%q frozen_before=%q frozen_after=%q", live.Questions[0].Question, frozenBefore.Questions[0].Question, frozenAfter.Questions[0].Question)
	}
	if live.Tasks[0].Objective == frozenBefore.Tasks[0].Objective || frozenAfter.Tasks[0].Objective != frozenBefore.Tasks[0].Objective {
		t.Fatalf("task live=%q frozen_before=%q frozen_after=%q", live.Tasks[0].Objective, frozenBefore.Tasks[0].Objective, frozenAfter.Tasks[0].Objective)
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

	readSnapshot := func() RunSnapshot {
		t.Helper()
		snapshot, readErr := store.TaskContextForAttempt(ctx, attempt.ID, fixture.workspaceID)
		if readErr != nil {
			t.Fatalf("TaskContextForAttempt: %v", readErr)
		}
		if snapshot.AttemptContext == nil {
			t.Fatal("expected attempt_context")
		}
		if snapshot.ArtifactProjection == nil {
			t.Fatal("expected bounded artifact projection")
		}
		return snapshot
	}

	beforeSnapshot := readSnapshot()
	var frozenAttemptBefore Attempt
	for _, candidate := range beforeSnapshot.Attempts {
		if candidate.ID == attempt.ID {
			frozenAttemptBefore = candidate
		}
	}
	if frozenAttemptBefore.ID == "" || frozenAttemptBefore.InboxTaskID != "" || frozenAttemptBefore.Status != AttemptStatusDispatching {
		t.Fatalf("frozen attempt before=%+v", frozenAttemptBefore)
	}
	inboxTaskID := seedIntegrationInboxEvent(t, ctx, pool, fixture.workspaceID, fixture.agentID)
	if _, _, err = store.AttachInboxTask(ctx, attempt.ID, inboxTaskID); err != nil {
		t.Fatalf("AttachInboxTask: %v", err)
	}
	replayedSnapshot := readSnapshot()
	before, replayed := *beforeSnapshot.AttemptContext, *replayedSnapshot.AttemptContext
	if before.ManifestID != replayed.ManifestID || before.ManifestHash != replayed.ManifestHash ||
		beforeSnapshot.ArtifactProjection.ProjectionHash != replayedSnapshot.ArtifactProjection.ProjectionHash {
		t.Fatalf("replay drift before=%+v replayed=%+v", before, replayed)
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
		  $1::uuid, $2::uuid, $3::uuid, 'https://example.test/post-live', 'Post live', 'example.test',
		  'primary', '{}'::text[], 'example.test', now(), 'sha256:post-live', 'post live snapshot', '{}'::jsonb,
		  'verified'
		)
	`, sourceID, fixture.workspaceID, run.SessionID); err != nil {
		t.Fatalf("insert source: %v", err)
	}
	backfillIntegrationArtifactPassport(t, ctx, tx, fixture.workspaceID, run.SessionID, sourceID, string(ArtifactKindSourceSnapshot), nil, nil)
	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit live source and passport: %v", err)
	}

	afterSnapshot := readSnapshot()
	afterLive := *afterSnapshot.AttemptContext
	if afterLive.ManifestID != before.ManifestID || afterLive.ManifestHash != before.ManifestHash {
		t.Fatalf("live mutation changed manifest metadata before=%+v after=%+v", before, afterLive)
	}
	if !afterLive.ManifestFiltered {
		t.Fatal("expected manifest-filtered attempt context after live mutation")
	}
	if afterSnapshot.ArtifactProjection.ProjectionHash != beforeSnapshot.ArtifactProjection.ProjectionHash {
		t.Fatalf("live mutation changed frozen artifact projection before=%q after=%q",
			beforeSnapshot.ArtifactProjection.ProjectionHash, afterSnapshot.ArtifactProjection.ProjectionHash)
	}
	var frozenAttemptAfter Attempt
	for _, candidate := range afterSnapshot.Attempts {
		if candidate.ID == attempt.ID {
			frozenAttemptAfter = candidate
		}
	}
	if frozenAttemptAfter.InboxTaskID != frozenAttemptBefore.InboxTaskID || frozenAttemptAfter.Status != frozenAttemptBefore.Status {
		t.Fatalf("live runtime mutation changed frozen attempt before=%+v after=%+v", frozenAttemptBefore, frozenAttemptAfter)
	}
	for _, item := range afterSnapshot.ArtifactProjection.Items {
		if item.EntityID == sourceID {
			t.Fatalf("post-manifest artifact leaked into Attempt projection: %+v", item)
		}
	}
	human, err := newEngine(store, nil, nil).Snapshot(ctx, run.SessionID, fixture.workspaceID)
	if err != nil {
		t.Fatalf("human Snapshot: %v", err)
	}
	if human.ArtifactProjection == nil {
		t.Fatal("expected human artifact projection")
	}
	var liveAttempt Attempt
	for _, candidate := range human.Attempts {
		if candidate.ID == attempt.ID {
			liveAttempt = candidate
		}
	}
	if liveAttempt.InboxTaskID != inboxTaskID {
		t.Fatalf("human attempt inbox=%q want=%q", liveAttempt.InboxTaskID, inboxTaskID)
	}
	var humanSawLive bool
	for _, item := range human.ArtifactProjection.Items {
		humanSawLive = humanSawLive || item.EntityID == sourceID
	}
	if !humanSawLive {
		t.Fatal("human live projection must include the post-manifest artifact")
	}
}

func TestTaskBoundArtifactProjectionUsesSelectionTimeMetadata(t *testing.T) {
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
		LeadAgentID: fixture.agentID, Goal: "Frozen projection metadata", Title: "Frozen projection",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	claimID := uuid.NewString()
	seedIntegrationClaimArtifact(t, ctx, pool, fixture.workspaceID, run.SessionID, claimID, "projection-claim", "freeze projection metadata")
	tasks, err := store.ListTasks(ctx, run.SessionID)
	if err != nil || len(tasks) == 0 {
		t.Fatalf("ListTasks: %v len=%d", err, len(tasks))
	}
	input := testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID)
	attempt, _, err := store.CreateDispatchIntent(ctx, input)
	if err != nil {
		t.Fatalf("CreateDispatchIntent: %v", err)
	}
	before, err := store.TaskContextForAttempt(ctx, attempt.ID, fixture.workspaceID)
	if err != nil || before.ArtifactProjection == nil {
		t.Fatalf("TaskContextForAttempt before: projection=%+v err=%v", before.ArtifactProjection, err)
	}
	beforeItem := artifactProjectionItemByEntityID(t, before.ArtifactProjection.Items, claimID)
	if beforeItem.LifecycleStatus != string(ArtifactLifecycleRegistered) {
		t.Fatalf("before item=%+v", beforeItem)
	}
	_, newRevision, _ := withdrawIntegrationArtifact(t, ctx, pool, fixture.workspaceID, run.SessionID, claimID)
	after, err := store.TaskContextForAttempt(ctx, attempt.ID, fixture.workspaceID)
	if err != nil || after.ArtifactProjection == nil {
		t.Fatalf("TaskContextForAttempt after: projection=%+v err=%v", after.ArtifactProjection, err)
	}
	afterItem := artifactProjectionItemByEntityID(t, after.ArtifactProjection.Items, claimID)
	if after.ArtifactProjection.ProjectionHash != before.ArtifactProjection.ProjectionHash ||
		afterItem.LifecycleStatus != beforeItem.LifecycleStatus ||
		afterItem.EligibilityRevision != beforeItem.EligibilityRevision ||
		afterItem.VersionCount != beforeItem.VersionCount ||
		afterItem.InputReferenceCount != beforeItem.InputReferenceCount ||
		afterItem.OutputReferenceCount != beforeItem.OutputReferenceCount {
		t.Fatalf("task-bound projection drifted before=%+v after=%+v", beforeItem, afterItem)
	}
	human, err := newEngine(store, nil, nil).Snapshot(ctx, run.SessionID, fixture.workspaceID)
	if err != nil || human.ArtifactProjection == nil {
		t.Fatalf("human Snapshot: projection=%+v err=%v", human.ArtifactProjection, err)
	}
	humanItem := artifactProjectionItemByEntityID(t, human.ArtifactProjection.Items, claimID)
	if humanItem.LifecycleStatus != string(ArtifactLifecycleWithdrawn) || humanItem.EligibilityRevision != newRevision || human.ArtifactProjection.ProjectionHash == before.ArtifactProjection.ProjectionHash {
		t.Fatalf("human projection did not advance: item=%+v", humanItem)
	}
	if _, err = pool.Exec(ctx, `
		UPDATE research_artifact_context_entry e
		SET selection_input_reference_count = selection_input_reference_count + 1
		FROM research_artifact_version v
		WHERE e.artifact_version_id=v.id AND e.workspace_id=v.workspace_id AND e.session_id=v.session_id
		  AND e.manifest_id=$1::uuid AND v.artifact_id=$2::uuid
	`, before.AttemptContext.ManifestID, claimID); err != nil {
		if !strings.Contains(err.Error(), "immutable") && !strings.Contains(err.Error(), "append-only") && !strings.Contains(err.Error(), "sealed") {
			t.Fatalf("tamper frozen projection metadata: %v", err)
		}
		return
	}
	if _, err = store.TaskContextForAttempt(ctx, attempt.ID, fixture.workspaceID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("tampered task-bound projection err=%v", err)
	}
}

func artifactProjectionItemByEntityID(t *testing.T, items []ArtifactProjectionItem, entityID string) ArtifactProjectionItem {
	t.Helper()
	for _, item := range items {
		if item.EntityID == entityID {
			return item
		}
	}
	t.Fatalf("artifact projection entity %s not found", entityID)
	return ArtifactProjectionItem{}
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
	err = casPassportSelectionTx(
		ctx, tx, fixture.workspaceID, run.SessionID, claimID,
		entry.Kind, entry.Version, entry.EligibilityRevision, entry.Lifecycle, entry.Provenance,
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
	err = casArtifactVersionSelectionTx(
		ctx, tx, fixture.workspaceID, run.SessionID, entry.VersionRowID,
		entry.ContentHash, entry.AccessLevel, entry.RepresentationBytes, entry.RepresentationHash,
	)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("representation CAS err=%v want ErrInvalidTransition", err)
	}
}

func TestDispatchSelectionCASBindsAllAuthorizationFacts(t *testing.T) {
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

	tests := []struct {
		name       string
		mutation   string
		versionCAS bool
	}{
		{
			name: "entity kind",
			mutation: `UPDATE research_artifact_passport
				SET entity_kind = 'stage_evaluation'
				WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid`,
		},
		{
			name: "provenance completeness",
			mutation: `UPDATE research_artifact_passport
				SET provenance_completeness = 'unknown'
				WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid`,
		},
		{
			name: "access level",
			mutation: `UPDATE research_artifact_version
				SET access_level = 'verified_only'
				WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND artifact_id = $3::uuid`,
			versionCAS: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := seedResearchRunFixture(t, ctx, pool)
			defer cleanupResearchRunFixture(pool, fixture)
			store := NewPostgresStore(pool)
			run, _, createErr := store.CreateRun(ctx, StartInput{
				WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID, CreatedBy: fixture.userID,
				LeadAgentID: fixture.agentID, Goal: "Selection CAS", Title: "Selection CAS",
				DepthTier: "standard", Language: "English",
			}, DefaultRunConfig("standard"))
			if createErr != nil {
				t.Fatalf("CreateRun: %v", createErr)
			}
			claimID := uuid.NewString()
			seedIntegrationClaimArtifact(t, ctx, pool, fixture.workspaceID, run.SessionID, claimID, "selection-cas", "selection fact")

			tx, beginErr := pool.Begin(ctx)
			if beginErr != nil {
				t.Fatal(beginErr)
			}
			defer tx.Rollback(ctx)
			plan, planErr := NewArtifactContextModule().PlanDispatchManifest(ctx, tx, fixture.workspaceID, run.SessionID, run.StateVersion)
			if planErr != nil {
				t.Fatal(planErr)
			}
			entry, ok := manifestEntryForArtifact(plan, claimID)
			if !ok {
				t.Fatalf("claim %s missing from manifest plan", claimID)
			}
			mutateIntegrationArtifactForCASTest(
				t, ctx, pool, tc.mutation, fixture.workspaceID, run.SessionID, claimID,
			)

			if tc.versionCAS {
				err = casArtifactVersionSelectionTx(
					ctx, tx, fixture.workspaceID, run.SessionID, entry.VersionRowID,
					entry.ContentHash, entry.AccessLevel, entry.RepresentationBytes, entry.RepresentationHash,
				)
			} else {
				err = casPassportSelectionTx(
					ctx, tx, fixture.workspaceID, run.SessionID, claimID,
					entry.Kind, entry.Version, entry.EligibilityRevision, entry.Lifecycle, entry.Provenance,
				)
			}
			if !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("selection CAS err=%v want ErrInvalidTransition", err)
			}
		})
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
	// This is the unchanged V1-V5 Gate rubric output before D freezes it. The
	// manifest must authorize and preserve these exact bytes rather than run a
	// second, D-specific rubric.
	rubricGate, err := store.EvaluateGate(ctx, run.SessionID)
	if err != nil {
		t.Fatalf("EvaluateGate before dispatch: %v", err)
	}
	input := testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID)
	attempt, _, err := store.CreateDispatchIntent(ctx, input)
	if err != nil {
		t.Fatalf("CreateDispatchIntent: %v", err)
	}
	var headerBytes []byte
	var headerHash, policyVersion string
	var policyWatermark int64
	if err = pool.QueryRow(ctx, `
		SELECT gate_snapshot_bytes, gate_snapshot_hash, policy_version, policy_watermark
		FROM research_artifact_context_manifest
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND attempt_id = $3::uuid
	`, fixture.workspaceID, run.SessionID, attempt.ID).Scan(
		&headerBytes,
		&headerHash,
		&policyVersion,
		&policyWatermark,
	); err != nil {
		t.Fatalf("load gate snapshot: %v", err)
	}
	if len(headerBytes) == 0 {
		t.Fatal("expected frozen gate snapshot bytes on manifest")
	}
	if headerHash != contentHashFromPayload(headerBytes) {
		t.Fatalf("gate snapshot hash=%q want=%q", headerHash, contentHashFromPayload(headerBytes))
	}
	if policyVersion != LegacyV1V5CompatPolicy {
		t.Fatalf("manifest policy version=%q want=%q", policyVersion, LegacyV1V5CompatPolicy)
	}
	var persistedGate GateResult
	if err := json.Unmarshal(headerBytes, &persistedGate); err != nil {
		t.Fatalf("decode persisted gate snapshot: %v", err)
	}
	if !gateResultsEqual(rubricGate, persistedGate) {
		t.Fatalf("D changed V1-V5 gate rubric before=%+v frozen=%+v", rubricGate, persistedGate)
	}

	frozen, err := store.TaskContextForAttempt(ctx, attempt.ID, fixture.workspaceID)
	if err != nil {
		t.Fatalf("TaskContextForAttempt: %v", err)
	}
	if !gateResultsEqual(persistedGate, frozen.Gate) {
		t.Fatalf("task context did not use manifest gate bytes persisted=%+v context=%+v", persistedGate, frozen.Gate)
	}
	frozenCount, ok := gateFindingCount(frozen.Gate, "tasks_incomplete")
	if !ok {
		t.Fatalf("frozen gate missing tasks_incomplete: %+v", frozen.Gate)
	}

	extraTaskID := uuid.NewString()
	insertIntegrationTasksWithPassports(t, ctx, pool, fixture.workspaceID, run.SessionID, run.GoalVersion, run.PlanVersion, `
		INSERT INTO research_task (
		  id, workspace_id, session_id, client_key, kind, objective,
		  required_capability, expected_result, status, goal_version, plan_version,
		  max_attempts, timeout_seconds, ready_at
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, 'post-dispatch-extra', 'discover', 'extra task',
		  'lead', 'research_evidence_v1', 'pending', $4, $5, 1, 300, NULL
		)
	`, []any{extraTaskID, fixture.workspaceID, run.SessionID, run.GoalVersion, run.PlanVersion}, []string{extraTaskID})

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
	if _, err = pool.Exec(ctx, `
		UPDATE research_artifact_policy_state
		SET watermark = watermark + 1, updated_at = now()
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
	`, fixture.workspaceID, run.SessionID); err != nil {
		t.Fatalf("advance live policy watermark: %v", err)
	}
	var livePolicyWatermark int64
	if err = pool.QueryRow(ctx, `
		SELECT watermark FROM research_artifact_policy_state
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
	`, fixture.workspaceID, run.SessionID).Scan(&livePolicyWatermark); err != nil {
		t.Fatalf("load live policy watermark: %v", err)
	}
	if livePolicyWatermark <= policyWatermark {
		t.Fatalf("live policy watermark=%d frozen=%d", livePolicyWatermark, policyWatermark)
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

	if _, err = pool.Exec(ctx, `
		UPDATE research_artifact_context_manifest
		SET gate_snapshot_bytes = gate_snapshot_bytes || decode('00', 'hex')
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND attempt_id = $3::uuid
	`, fixture.workspaceID, run.SessionID, attempt.ID); err != nil {
		t.Fatalf("corrupt frozen gate bytes: %v", err)
	}
	if _, err = store.TaskContextForAttempt(ctx, attempt.ID, fixture.workspaceID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("corrupt frozen gate error=%v want ErrInvalidTransition", err)
	}
}

func TestTaskContextForAttemptRejectsTamperedFrozenGateSnapshot(t *testing.T) {
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

	cases := []struct {
		name   string
		mutate string
	}{
		{
			name: "bytes",
			mutate: `UPDATE research_artifact_context_manifest
			         SET gate_snapshot_bytes='{"passed":true,"score":1,"findings":[]}'::bytea
			         WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND attempt_id=$3::uuid`,
		},
		{
			name: "hash",
			mutate: `UPDATE research_artifact_context_manifest
			         SET gate_snapshot_hash='sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
			         WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND attempt_id=$3::uuid`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := seedResearchRunFixture(t, ctx, pool)
			defer cleanupResearchRunFixture(pool, fixture)
			store := NewPostgresStore(pool)
			run, _, createErr := store.CreateRun(ctx, StartInput{
				WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID, CreatedBy: fixture.userID,
				LeadAgentID: fixture.agentID, Goal: "Gate snapshot integrity", Title: "Gate integrity",
				DepthTier: "standard", Language: "English",
			}, DefaultRunConfig("standard"))
			if createErr != nil {
				t.Fatal(createErr)
			}
			tasks, listErr := store.ListTasks(ctx, run.SessionID)
			if listErr != nil || len(tasks) == 0 {
				t.Fatalf("ListTasks err=%v len=%d", listErr, len(tasks))
			}
			attempt, _, dispatchErr := store.CreateDispatchIntent(ctx, testDispatchIntentInput(
				t, ctx, store, run.SessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID,
			))
			if dispatchErr != nil {
				t.Fatal(dispatchErr)
			}
			if _, readErr := store.TaskContextForAttempt(ctx, attempt.ID, fixture.workspaceID); readErr != nil {
				t.Fatalf("control frozen read: %v", readErr)
			}
			if _, mutateErr := pool.Exec(ctx, tc.mutate, fixture.workspaceID, run.SessionID, attempt.ID); mutateErr != nil {
				t.Fatal(mutateErr)
			}
			if _, readErr := store.TaskContextForAttempt(ctx, attempt.ID, fixture.workspaceID); !errors.Is(readErr, ErrInvalidTransition) {
				t.Fatalf("tampered %s read err=%v want ErrInvalidTransition", tc.name, readErr)
			}
		})
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
	ctx := context.Background()
	_ = disableResearchArtifactCleanupGuards(ctx, pool)
	_, _ = pool.Exec(ctx, `DELETE FROM workspace WHERE id = $1::uuid`, fixture.workspaceID)
	_, _ = pool.Exec(ctx, `DELETE FROM "user" WHERE id = $1::uuid`, fixture.userID)
	_ = enableResearchArtifactCleanupGuards(ctx, pool)
}

func intPtr(v int) *int { return &v }
