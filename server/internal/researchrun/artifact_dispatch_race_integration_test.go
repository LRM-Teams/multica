package researchrun

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type dispatchRaceFixture struct {
	pool    *pgxpool.Pool
	store   *PostgresStore
	fixture researchRunFixture
	run     Run
	task    Task
	claimID string
	input   CreateDispatchIntentInput
}

func setupDispatchRaceFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) dispatchRaceFixture {
	t.Helper()
	fixture := seedResearchRunFixture(t, ctx, pool)
	store := NewPostgresStore(pool)
	run, _, err := store.CreateRun(ctx, StartInput{
		WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID, CreatedBy: fixture.userID,
		LeadAgentID: fixture.agentID, Goal: "Dispatch race", Title: "Dispatch race",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	tasks, err := store.ListTasks(ctx, run.SessionID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("ListTasks: %v len=%d", err, len(tasks))
	}
	task := tasks[0]
	claimID := uuid.NewString()
	seedIntegrationClaimArtifact(
		t, ctx, pool, fixture.workspaceID, run.SessionID,
		claimID, "dispatch-race-claim", "claim for dispatch race",
	)

	input := testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, task.ID, fixture.agentID)
	return dispatchRaceFixture{
		pool: pool, store: store, fixture: fixture, run: run, task: task,
		claimID: claimID, input: input,
	}
}

func invokeDispatchWithBeforeCommitFault(t *testing.T, ctx context.Context, fx dispatchRaceFixture) {
	t.Helper()
	injected := errors.New("injected dispatch before_commit")
	fault := &oneShotResearchTxFault{
		operation: txOpDispatchIntentCreate,
		point:     txBeforeCommit,
		err:       injected,
	}
	fx.store.txFaultHook = fault.hook
	_, _, err := fx.store.CreateDispatchIntent(ctx, fx.input)
	fx.store.txFaultHook = nil
	if !errors.Is(err, injected) {
		t.Fatalf("CreateDispatchIntent err=%v want injected before_commit fault", err)
	}
	if !fault.fired {
		t.Fatal("before_commit fault did not fire")
	}
}

func assertDispatchRolledBack(t *testing.T, ctx context.Context, fx dispatchRaceFixture) {
	t.Helper()
	var attempts, passports, manifests, entries, omissions, grants, outboxes, events int
	if err := fx.pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*)::int FROM research_task_attempt WHERE id=$1::uuid),
		  (SELECT count(*)::int FROM research_artifact_passport
		   WHERE workspace_id=$2::uuid AND session_id=$3::uuid
		     AND (id=$1::uuid OR entity_kind='context_manifest')),
		  (SELECT count(*)::int FROM research_artifact_context_manifest
		   WHERE workspace_id=$2::uuid AND session_id=$3::uuid),
		  (SELECT count(*)::int FROM research_artifact_context_entry
		   WHERE workspace_id=$2::uuid AND session_id=$3::uuid),
		  (SELECT count(*)::int FROM research_artifact_context_omission
		   WHERE workspace_id=$2::uuid AND session_id=$3::uuid),
		  (SELECT count(*)::int FROM research_artifact_policy_grant
		   WHERE workspace_id=$2::uuid AND session_id=$3::uuid),
		  (SELECT count(*)::int FROM research_dispatch_outbox WHERE attempt_id=$1::uuid),
		  (SELECT count(*)::int FROM research_run_event
		   WHERE workspace_id=$2::uuid AND session_id=$3::uuid
		     AND event_type='task_dispatching' AND payload->>'attempt_id'=$1::text)
	`, fx.input.AttemptID, fx.fixture.workspaceID, fx.run.SessionID).Scan(
		&attempts, &passports, &manifests, &entries, &omissions, &grants, &outboxes, &events,
	); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 || passports != 0 || manifests != 0 || entries != 0 || omissions != 0 || grants != 0 || outboxes != 0 || events != 0 {
		t.Fatalf("rolled-back dispatch leaked attempt=%d passports=%d manifest=%d entries=%d omissions=%d grants=%d outbox=%d events=%d",
			attempts, passports, manifests, entries, omissions, grants, outboxes, events)
	}
	var taskStatus string
	if err := fx.pool.QueryRow(ctx, `
		SELECT status FROM research_task WHERE id = $1::uuid
	`, fx.task.ID).Scan(&taskStatus); err != nil {
		t.Fatal(err)
	}
	if taskStatus != string(TaskStatusReady) && taskStatus != string(TaskStatusPending) {
		t.Fatalf("task status=%q want ready or pending after rolled-back dispatch", taskStatus)
	}
}

func assertDispatchCommittedOnceWithFreshFacts(t *testing.T, ctx context.Context, fx dispatchRaceFixture, mutation string) {
	t.Helper()
	var attempts, manifests, outboxes, events int
	if err := fx.pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*)::int FROM research_task_attempt WHERE id=$1::uuid),
		  (SELECT count(*)::int FROM research_artifact_context_manifest
		   WHERE workspace_id=$2::uuid AND session_id=$3::uuid AND attempt_id=$1::uuid),
		  (SELECT count(*)::int FROM research_dispatch_outbox WHERE attempt_id=$1::uuid),
		  (SELECT count(*)::int FROM research_run_event
		   WHERE workspace_id=$2::uuid AND session_id=$3::uuid
		     AND event_type='task_dispatching' AND payload->>'attempt_id'=$1::text)
	`, fx.input.AttemptID, fx.fixture.workspaceID, fx.run.SessionID).Scan(
		&attempts, &manifests, &outboxes, &events,
	); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || manifests != 1 || outboxes != 1 || events != 1 {
		t.Fatalf("fresh dispatch cardinality attempt=%d manifest=%d outbox=%d events=%d want all 1",
			attempts, manifests, outboxes, events)
	}
	var taskStatus string
	if err := fx.pool.QueryRow(ctx, `SELECT status FROM research_task WHERE id=$1::uuid`, fx.task.ID).Scan(&taskStatus); err != nil {
		t.Fatal(err)
	}
	if taskStatus != string(TaskStatusDispatching) {
		t.Fatalf("fresh dispatch task status=%q want %q", taskStatus, TaskStatusDispatching)
	}

	switch mutation {
	case "eligibility_revision":
		var entryRevision, passportRevision int64
		if err := fx.pool.QueryRow(ctx, `
			SELECT e.eligibility_revision, p.eligibility_revision
			FROM research_artifact_context_entry e
			JOIN research_artifact_context_manifest m
			  ON (m.workspace_id,m.session_id,m.id)=(e.workspace_id,e.session_id,e.manifest_id)
			JOIN research_artifact_version v
			  ON (v.workspace_id,v.session_id,v.id)=(e.workspace_id,e.session_id,e.artifact_version_id)
			JOIN research_artifact_passport p
			  ON (p.workspace_id,p.session_id,p.id)=(v.workspace_id,v.session_id,v.artifact_id)
			WHERE m.attempt_id=$1::uuid AND p.id=$2::uuid
		`, fx.input.AttemptID, fx.claimID).Scan(&entryRevision, &passportRevision); err != nil {
			t.Fatal(err)
		}
		if entryRevision <= 1 || entryRevision != passportRevision {
			t.Fatalf("entry eligibility revision=%d passport=%d want matching advanced revision", entryRevision, passportRevision)
		}
	case "version_content_hash":
		wantHash := contentHashFromPayload([]byte("mutated after dispatch preflight"))
		var gotHash string
		if err := fx.pool.QueryRow(ctx, `
			SELECT v.content_hash
			FROM research_artifact_context_entry e
			JOIN research_artifact_context_manifest m
			  ON (m.workspace_id,m.session_id,m.id)=(e.workspace_id,e.session_id,e.manifest_id)
			JOIN research_artifact_version v
			  ON (v.workspace_id,v.session_id,v.id)=(e.workspace_id,e.session_id,e.artifact_version_id)
			WHERE m.attempt_id=$1::uuid AND v.artifact_id=$2::uuid
		`, fx.input.AttemptID, fx.claimID).Scan(&gotHash); err != nil {
			t.Fatal(err)
		}
		if gotHash != wantHash {
			t.Fatalf("manifest version content hash=%q want fresh %q", gotHash, wantHash)
		}
	case "withdrawn_lifecycle":
		var entries, omissions int
		if err := fx.pool.QueryRow(ctx, `
			SELECT
			  (SELECT count(*)::int
			   FROM research_artifact_context_entry e
			   JOIN research_artifact_context_manifest m
			     ON (m.workspace_id,m.session_id,m.id)=(e.workspace_id,e.session_id,e.manifest_id)
			   JOIN research_artifact_version v
			     ON (v.workspace_id,v.session_id,v.id)=(e.workspace_id,e.session_id,e.artifact_version_id)
			   WHERE m.attempt_id=$1::uuid AND v.artifact_id=$2::uuid),
			  (SELECT count(*)::int
			   FROM research_artifact_context_omission o
			   JOIN research_artifact_context_manifest m
			     ON (m.workspace_id,m.session_id,m.id)=(o.workspace_id,o.session_id,o.manifest_id)
			   JOIN research_artifact_version v
			     ON (v.workspace_id,v.session_id,v.id)=(o.workspace_id,o.session_id,o.candidate_version_id)
			   WHERE m.attempt_id=$1::uuid AND v.artifact_id=$2::uuid AND o.reason='lifecycle')
		`, fx.input.AttemptID, fx.claimID).Scan(&entries, &omissions); err != nil {
			t.Fatal(err)
		}
		if entries != 0 || omissions != 1 {
			t.Fatalf("withdrawn claim entries=%d lifecycle omissions=%d want 0/1", entries, omissions)
		}
	default:
		t.Fatalf("unhandled fresh-fact assertion for mutation %q", mutation)
	}
}

func TestDispatchRaceReevaluatesFactsAfterRolledBackIntent(t *testing.T) {
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

	cases := []struct {
		name        string
		mutate      func(context.Context, dispatchRaceFixture) error
		wantInvalid bool
	}{
		{
			name: "eligibility_revision",
			mutate: func(ctx context.Context, fx dispatchRaceFixture) error {
				mutateIntegrationArtifactForCASTest(t, ctx, fx.pool, `
					UPDATE research_artifact_passport
					SET eligibility_revision = eligibility_revision + 1
					WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
				`, fx.fixture.workspaceID, fx.run.SessionID, fx.claimID)
				return nil
			},
		},
		{
			name:        "stale_run_state_version",
			wantInvalid: true,
			mutate: func(ctx context.Context, fx dispatchRaceFixture) error {
				_, err := fx.pool.Exec(ctx, `
					UPDATE research_session
					SET state_version = state_version + 1, updated_at = now()
					WHERE workspace_id = $1::uuid AND id = $2::uuid
				`, fx.fixture.workspaceID, fx.run.SessionID)
				return err
			},
		},
		{
			name: "version_content_hash",
			mutate: func(ctx context.Context, fx dispatchRaceFixture) error {
				mutatedHash := contentHashFromPayload([]byte("mutated after dispatch preflight"))
				mutateIntegrationArtifactForCASTest(t, ctx, fx.pool, `
					UPDATE research_artifact_version
					SET content_hash = $4
					WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND artifact_id = $3::uuid
				`, fx.fixture.workspaceID, fx.run.SessionID, fx.claimID, mutatedHash)
				return nil
			},
		},
		{
			name: "withdrawn_lifecycle",
			mutate: func(ctx context.Context, fx dispatchRaceFixture) error {
				_, err := fx.pool.Exec(ctx, `
					UPDATE research_artifact_passport
					SET lifecycle_status = 'withdrawn'
					WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
				`, fx.fixture.workspaceID, fx.run.SessionID, fx.claimID)
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := setupDispatchRaceFixture(t, ctx, pool)
			defer cleanupResearchRunFixture(pool, fx.fixture)
			invokeDispatchWithBeforeCommitFault(t, ctx, fx)
			assertDispatchRolledBack(t, ctx, fx)
			if err := tc.mutate(ctx, fx); err != nil {
				t.Fatalf("mutate: %v", err)
			}
			_, _, err = fx.store.CreateDispatchIntent(ctx, fx.input)
			if tc.wantInvalid {
				if !errors.Is(err, ErrInvalidTransition) {
					t.Fatalf("CreateDispatchIntent after mutation err=%v want ErrInvalidTransition", err)
				}
				assertDispatchRolledBack(t, ctx, fx)
				return
			}
			if err != nil {
				t.Fatalf("CreateDispatchIntent after fresh candidate mutation: %v", err)
			}
			assertDispatchCommittedOnceWithFreshFacts(t, ctx, fx, tc.name)
		})
	}
}

func TestDispatchRaceAcceptsAfterRolledBackIntentWhenOnlyUnrelatedWatermarkAdvances(t *testing.T) {
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

	fx := setupDispatchRaceFixture(t, ctx, pool)
	defer cleanupResearchRunFixture(pool, fx.fixture)
	invokeDispatchWithBeforeCommitFault(t, ctx, fx)
	assertDispatchRolledBack(t, ctx, fx)
	if _, err = pool.Exec(ctx, `
		UPDATE research_artifact_policy_state
		SET watermark = watermark + 1, updated_at = now()
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
	`, fx.fixture.workspaceID, fx.run.SessionID); err != nil {
		t.Fatalf("advance unrelated watermark: %v", err)
	}
	attempt, _, err := fx.store.CreateDispatchIntent(ctx, fx.input)
	if err != nil {
		t.Fatalf("CreateDispatchIntent retry: %v", err)
	}
	var manifestCount int
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int FROM research_artifact_context_manifest
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND attempt_id = $3::uuid
	`, fx.fixture.workspaceID, fx.run.SessionID, attempt.ID).Scan(&manifestCount); err != nil {
		t.Fatal(err)
	}
	if manifestCount != 1 {
		t.Fatalf("manifest count=%d want 1", manifestCount)
	}
}

func TestDispatchRaceConvergesAfterRolledBackIntentWithoutMutation(t *testing.T) {
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

	fx := setupDispatchRaceFixture(t, ctx, pool)
	defer cleanupResearchRunFixture(pool, fx.fixture)
	invokeDispatchWithBeforeCommitFault(t, ctx, fx)
	assertDispatchRolledBack(t, ctx, fx)
	attempt, _, err := fx.store.CreateDispatchIntent(ctx, fx.input)
	if err != nil {
		t.Fatalf("CreateDispatchIntent retry: %v", err)
	}
	replayed, _, err := fx.store.CreateDispatchIntent(ctx, fx.input)
	if err != nil {
		t.Fatalf("CreateDispatchIntent replay: %v", err)
	}
	if replayed.ID != attempt.ID {
		t.Fatalf("replay attempt=%q want=%q", replayed.ID, attempt.ID)
	}
}
