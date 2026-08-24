package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSortManifestEntryCandidatesCanonicalizesInverseUUIDOrder(t *testing.T) {
	forward := []artifactVersionCandidate{
		{Kind: ArtifactKindClaim, ArtifactID: lockOrderClaimLowID},
		{Kind: ArtifactKindClaim, ArtifactID: lockOrderClaimHighID},
	}
	reverse := []artifactVersionCandidate{forward[1], forward[0]}
	sortManifestEntryCandidates(forward)
	sortManifestEntryCandidates(reverse)
	for i := range forward {
		if forward[i].Kind != reverse[i].Kind || forward[i].ArtifactID != reverse[i].ArtifactID {
			t.Fatalf("normalized[%d] differs: forward=%+v reverse=%+v", i, forward[i], reverse[i])
		}
	}
	if forward[0].ArtifactID != lockOrderClaimLowID || forward[1].ArtifactID != lockOrderClaimHighID {
		t.Fatalf("canonical claim order=%q,%q", forward[0].ArtifactID, forward[1].ArtifactID)
	}
}

func TestDispatchCandidateLocksNormalizeInverseUUIDOrder(t *testing.T) {
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
	run, _, err := store.CreateRun(ctx, StartInput{
		WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID, CreatedBy: fixture.userID,
		LeadAgentID: fixture.agentID, Goal: "Inverse dispatch lock order", Title: "Inverse dispatch locks",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	insertLockOrderSharedClaims(t, ctx, pool, fixture.workspaceID, run.SessionID)

	candidates := make([]artifactVersionCandidate, 0, 2)
	rows, err := pool.Query(ctx, `
		SELECT passport.id::text, version.id::text, passport.entity_kind
		FROM research_artifact_passport passport
		JOIN research_artifact_version version
		  ON (version.workspace_id,version.session_id,version.artifact_id,version.version)=
		     (passport.workspace_id,passport.session_id,passport.id,passport.current_version)
		WHERE passport.workspace_id=$1::uuid AND passport.session_id=$2::uuid
		  AND passport.id IN ($3::uuid,$4::uuid)
		ORDER BY passport.id::text
	`, fixture.workspaceID, run.SessionID, lockOrderClaimLowID, lockOrderClaimHighID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var candidate artifactVersionCandidate
		var kind string
		if err = rows.Scan(&candidate.ArtifactID, &candidate.VersionRowID, &kind); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		candidate.Kind = ArtifactEntityKind(kind)
		candidates = append(candidates, candidate)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	if len(candidates) != 2 || candidates[0].ArtifactID >= candidates[1].ArtifactID {
		t.Fatalf("candidate fixture=%+v", candidates)
	}
	inverse := []artifactVersionCandidate{candidates[1], candidates[0]}

	start := make(chan struct{})
	errs := make(chan error, 2)
	lock := func(input []artifactVersionCandidate) {
		tx, beginErr := pool.BeginTx(ctx, pgx.TxOptions{})
		if beginErr != nil {
			errs <- beginErr
			return
		}
		defer tx.Rollback(ctx)
		<-start
		if lockErr := lockDispatchManifestCandidateRowsTx(ctx, tx, fixture.workspaceID, run.SessionID, input); lockErr != nil {
			errs <- lockErr
			return
		}
		errs <- tx.Commit(ctx)
	}
	go lock(candidates)
	go lock(inverse)
	close(start)
	for range 2 {
		select {
		case lockErr := <-errs:
			if lockErr != nil {
				t.Fatalf("inverse candidate lock: %v", lockErr)
			}
		case <-ctx.Done():
			t.Fatal("inverse candidate locks did not finish (possible deadlock)")
		}
	}
}

func TestDispatchConcurrentSharedCandidatesNoDeadlock(t *testing.T) {
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
		LeadAgentID: fixture.agentID, Goal: "Dispatch lock order", Title: "Dispatch lock order",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	insertLockOrderSharedClaims(t, ctx, pool, fixture.workspaceID, run.SessionID)

	tasks, err := store.ListTasks(ctx, run.SessionID, run.WorkspaceID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("ListTasks: %v len=%d", err, len(tasks))
	}
	planTask := tasks[0]
	planAttempt, _, err := store.CreateDispatchIntent(ctx, testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, planTask.ID, fixture.agentID))
	if err != nil {
		t.Fatalf("CreateDispatchIntent plan: %v", err)
	}
	planInboxID := seedIntegrationInboxEvent(t, ctx, pool, fixture.workspaceID, fixture.agentID)
	if _, _, err = store.AttachInboxTask(ctx, planAttempt.ID, planInboxID); err != nil {
		t.Fatalf("AttachInboxTask plan: %v", err)
	}
	planRaw, err := json.Marshal(upgradeResultToV5(validV4PlanResult(t)))
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

	discoverTasks, err := listDiscoverTasks(ctx, store, run.SessionID, run.WorkspaceID)
	if err != nil || len(discoverTasks) == 0 {
		t.Fatalf("listDiscoverTasks: %v len=%d", err, len(discoverTasks))
	}
	if len(discoverTasks) == 1 {
		secondTaskID := uuid.NewString()
		insertIntegrationTasksWithPassports(t, ctx, pool, fixture.workspaceID, run.SessionID, run.GoalVersion, run.PlanVersion, `
			INSERT INTO research_task (
			  id, workspace_id, session_id, client_key, kind, objective,
			  required_capability, expected_result, status, goal_version, plan_version,
			  max_attempts, timeout_seconds, ready_at, question_id
			)
			SELECT
			  $1::uuid, t.workspace_id, t.session_id, 'discover-2', 'discover',
			  'Second discover dispatch', 'scout', t.expected_result, 'ready',
			  t.goal_version, t.plan_version, 2, 300, now(), t.question_id
			FROM research_task t
			WHERE t.id = $2::uuid
		`, []any{secondTaskID, discoverTasks[0].ID}, []string{secondTaskID})
		discoverTasks, err = listDiscoverTasks(ctx, store, run.SessionID, run.WorkspaceID)
		if err != nil || len(discoverTasks) < 2 {
			t.Fatalf("listDiscoverTasks after insert: %v len=%d", err, len(discoverTasks))
		}
	}

	attempts := make([]Attempt, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i, task := range discoverTasks[:2] {
		wg.Add(1)
		go func(i int, task Task) {
			defer wg.Done()
			for try := 0; try < 3; try++ {
				attempts[i], _, errs[i] = store.CreateDispatchIntent(ctx, testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, task.ID, fixture.agentID))
				if !errors.Is(errs[i], ErrInvalidTransition) {
					return
				}
			}
		}(i, task)
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(45 * time.Second):
		t.Fatal("concurrent CreateDispatchIntent calls did not finish within timeout (possible deadlock)")
	}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("CreateDispatchIntent discover[%d]: %v", i, err)
		}
	}

	for i, attempt := range attempts {
		var manifestID string
		if err = pool.QueryRow(ctx, `
			SELECT id::text
			FROM research_artifact_context_manifest
			WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND attempt_id = $3::uuid
		`, fixture.workspaceID, run.SessionID, attempt.ID).Scan(&manifestID); err != nil {
			t.Fatalf("load manifest[%d]: %v", i, err)
		}
		if manifestID == "" {
			t.Fatalf("manifest[%d] missing", i)
		}
		assertManifestEntryCanonicalOrder(t, ctx, pool, fixture.workspaceID, run.SessionID, manifestID)
	}
}

func assertManifestEntryCanonicalOrder(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	workspaceID, sessionID, manifestID string,
) {
	t.Helper()
	rows, err := pool.Query(ctx, `
		SELECT p.entity_kind, v.artifact_id::text
		FROM research_artifact_context_entry e
		JOIN research_artifact_version v
		  ON v.workspace_id = e.workspace_id
		 AND v.session_id = e.session_id
		 AND v.id = e.artifact_version_id
		JOIN research_artifact_passport p
		  ON p.workspace_id = v.workspace_id
		 AND p.session_id = v.session_id
		 AND p.id = v.artifact_id
		WHERE e.workspace_id = $1::uuid
		  AND e.session_id = $2::uuid
		  AND e.manifest_id = $3::uuid
		ORDER BY e.ordinal
	`, workspaceID, sessionID, manifestID)
	if err != nil {
		t.Fatalf("query manifest entries: %v", err)
	}
	defer rows.Close()

	var prevKind ArtifactEntityKind
	var prevArtifactID string
	first := true
	for rows.Next() {
		var kind ArtifactEntityKind
		var artifactID string
		if err = rows.Scan(&kind, &artifactID); err != nil {
			t.Fatal(err)
		}
		if !first {
			if kind < prevKind || (kind == prevKind && artifactID < prevArtifactID) {
				t.Fatalf("manifest entry order not canonical: prev=%s:%s current=%s:%s", prevKind, prevArtifactID, kind, artifactID)
			}
		}
		prevKind = kind
		prevArtifactID = artifactID
		first = false
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
}
