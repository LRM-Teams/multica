package researchrun

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// A repeated identical failure must reuse one repair decision. Before the
// repair record existed, every recomputed failure of the same cause was free to
// open a fresh remediation path, which is how one broken target turned into a
// growing pile of duplicate work.
func TestTargetRepairIsIdempotentPerCanonicalFailure(t *testing.T) {
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
	defer cleanupResearchCircuitFixture(pool, fixture)
	store := NewPostgresStore(pool)
	target, tasks := initializeCircuitFixture(t, ctx, store, fixture, 3)

	first := createCircuitAttempt(t, ctx, store, fixture, tasks[0], target)
	if _, err = store.FailAttempt(ctx, AttemptFailure{
		AttemptID: first.ID, FailureClass: string(FailureTimeout),
		SourceReason: "agent_timeout", Diagnostics: "first timeout", Retryable: true,
	}); err != nil {
		t.Fatal(err)
	}
	repairs, err := store.ListTargetRepairs(ctx, fixture.sessionID)
	if err != nil || len(repairs) != 1 {
		t.Fatalf("repairs after first failure=%d err=%v", len(repairs), err)
	}
	decision := repairs[0]
	if decision.RepairKind != RepairRetryTarget || decision.FailureClass != FailureTimeout ||
		decision.OccurrenceCount != 1 || decision.TaskID != tasks[0].ID {
		t.Fatalf("first repair decision=%+v", decision)
	}
	if decision.FirstAttemptID != first.ID || decision.LastAttemptID != first.ID {
		t.Fatalf("first repair attempt binding=%+v", decision)
	}

	// The same canonical failure of the same Task at the same state version.
	// The Task is retryable, so a second Attempt exists for the same cause.
	second := createCircuitAttempt(t, ctx, store, fixture, tasks[0], target)
	if _, err = store.FailAttempt(ctx, AttemptFailure{
		AttemptID: second.ID, FailureClass: string(FailureTimeout),
		SourceReason: "agent_timeout", Diagnostics: "second timeout", Retryable: true,
	}); err != nil {
		t.Fatal(err)
	}
	repairs, err = store.ListTargetRepairs(ctx, fixture.sessionID)
	if err != nil || len(repairs) != 1 {
		t.Fatalf("recomputed identical failure created %d repairs (err=%v)", len(repairs), err)
	}
	reused := repairs[0]
	if reused.ID != decision.ID || reused.RepairKey != decision.RepairKey {
		t.Fatalf("recomputed failure did not reuse the repair: %+v vs %+v", reused, decision)
	}
	if reused.OccurrenceCount != 2 {
		t.Fatalf("reused repair occurrence_count=%d, want 2", reused.OccurrenceCount)
	}
	if reused.FirstAttemptID != first.ID || reused.LastAttemptID != second.ID {
		t.Fatalf("reused repair attempt binding=%+v", reused)
	}
	if !reused.LastObservedAt.After(reused.FirstObservedAt) && !reused.LastObservedAt.Equal(reused.FirstObservedAt) {
		t.Fatalf("reused repair observation window=%+v", reused)
	}

	// One decision projects exactly once, however often the cause recurs.
	events, err := store.ListRunEvents(ctx, fixture.sessionID, fixture.workspaceID, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	decided := 0
	var payload map[string]any
	for _, event := range events {
		if event.Type != "target_repair_decided" {
			continue
		}
		decided++
		if err = json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
	}
	if decided != 1 {
		t.Fatalf("target_repair_decided events=%d, want 1", decided)
	}
	if payload["repair_kind"] != string(RepairRetryTarget) ||
		payload["failure_class"] != string(FailureTimeout) ||
		payload["repair_key"] != decision.RepairKey ||
		payload["source_failure_reason"] != "agent_timeout" {
		t.Fatalf("repair event payload=%+v", payload)
	}

	// Control group: a different failure class on the same Task is a different
	// cause and must produce its own decision, so the reuse assertions above
	// cannot pass merely because nothing new is ever recorded.
	third := createCircuitAttempt(t, ctx, store, fixture, tasks[1], target)
	if _, err = store.FailAttempt(ctx, AttemptFailure{
		AttemptID: third.ID, FailureClass: string(FailureCredential),
		SourceReason: "agent_provider_auth_or_access", Diagnostics: "auth rejected",
	}); err != nil {
		t.Fatal(err)
	}
	repairs, err = store.ListTargetRepairs(ctx, fixture.sessionID)
	if err != nil || len(repairs) != 2 {
		t.Fatalf("repairs after distinct cause=%d err=%v", len(repairs), err)
	}
	for _, repair := range repairs {
		if repair.FailureClass == FailureCredential && repair.RepairKind != RepairRequestConfiguration {
			t.Fatalf("credential failure repaired with %q", repair.RepairKind)
		}
	}
}

// A changed frozen target configuration is a genuinely new cause: reusing the
// old decision would apply a repair that was chosen against a target which no
// longer exists.
func TestTargetRepairSplitsOnTargetConfigurationChange(t *testing.T) {
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
	defer cleanupResearchCircuitFixture(pool, fixture)
	store := NewPostgresStore(pool)
	target, tasks := initializeCircuitFixture(t, ctx, store, fixture, 2)

	first := createCircuitAttempt(t, ctx, store, fixture, tasks[0], target)
	if _, err = store.FailAttempt(ctx, AttemptFailure{
		AttemptID: first.ID, FailureClass: string(FailureTimeout),
		SourceReason: "agent_timeout", Diagnostics: "timeout on config A", Retryable: true,
	}); err != nil {
		t.Fatal(err)
	}

	// A later Attempt freezes a different configuration. The execution target is
	// immutable per Attempt, so the change is expressed by the next Attempt.
	changed := target
	changed.ConfigFingerprint = ExecutionTargetFingerprint("changed", target.AgentID)
	second := createCircuitAttempt(t, ctx, store, fixture, tasks[0], changed)
	if _, err = store.FailAttempt(ctx, AttemptFailure{
		AttemptID: second.ID, FailureClass: string(FailureTimeout),
		SourceReason: "agent_timeout", Diagnostics: "timeout on config B", Retryable: true,
	}); err != nil {
		t.Fatal(err)
	}

	repairs, err := store.ListTargetRepairs(ctx, fixture.sessionID)
	if err != nil || len(repairs) != 2 {
		t.Fatalf("configuration change repairs=%d err=%v", len(repairs), err)
	}
	if repairs[0].RepairKey == repairs[1].RepairKey {
		t.Fatal("configuration change reused the same repair key")
	}
	for _, repair := range repairs {
		if repair.OccurrenceCount != 1 {
			t.Fatalf("configuration-split repair occurrence_count=%d, want 1: %+v", repair.OccurrenceCount, repair)
		}
	}
}

// A research outcome is not an execution failure. Recording a repair for it
// would let a refuted hypothesis be retried as if the runtime had broken.
func TestResearchOutcomeFailureRecordsNoTargetRepair(t *testing.T) {
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
	defer cleanupResearchCircuitFixture(pool, fixture)
	store := NewPostgresStore(pool)
	target, tasks := initializeCircuitFixture(t, ctx, store, fixture, 2)

	outcome := createCircuitAttempt(t, ctx, store, fixture, tasks[0], target)
	if _, err = store.FailAttempt(ctx, AttemptFailure{
		AttemptID: outcome.ID, FailureClass: string(FailureResearchNegative),
		SourceReason: "no supporting evidence", Diagnostics: "hypothesis refuted",
	}); err != nil {
		t.Fatal(err)
	}
	repairs, err := store.ListTargetRepairs(ctx, fixture.sessionID)
	if err != nil || len(repairs) != 0 {
		t.Fatalf("research outcome recorded %d repairs (err=%v)", len(repairs), err)
	}

	// Control group: the same settlement path does record an execution failure,
	// so the assertion above is not passing because recording never happens.
	execution := createCircuitAttempt(t, ctx, store, fixture, tasks[1], target)
	if _, err = store.FailAttempt(ctx, AttemptFailure{
		AttemptID: execution.ID, FailureClass: string(FailureConfiguration),
		SourceReason: "agent_missing_config", Diagnostics: "model not configured",
	}); err != nil {
		t.Fatal(err)
	}
	repairs, err = store.ListTargetRepairs(ctx, fixture.sessionID)
	if err != nil || len(repairs) != 1 || repairs[0].RepairKind != RepairRequestConfiguration {
		t.Fatalf("execution failure repairs=%+v err=%v", repairs, err)
	}
}

// The allowed action matrix is a database judgement, so a Handler, background
// job, or manual SQL statement cannot persist an action the failure class does
// not license. The Go matrix and the SQL matrix must agree on every pair.
func TestRepairActionMatrixIsEnforcedByDatabaseAndMatchesExecutor(t *testing.T) {
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

	classes := []FailureClass{
		FailureResearchNegative, FailureMethodInvalid, FailureContractBlocked,
		FailureResultInvalid, FailurePermission, FailureCredential,
		FailureRateLimited, FailureNetwork, FailureTimeout, FailureTool,
		FailureProvider, FailureRuntimeLost, FailureConfiguration,
		FailureCapability, FailureTargetChanged, FailureInternal, FailureUnknown,
	}
	kinds := []RepairKind{
		RepairWaitForTarget, RepairRetryTarget, RepairRerouteTarget,
		RepairFreshSession, RepairRequestConfiguration, RepairRequestDecision,
	}
	mismatches := []string{}
	for _, class := range classes {
		for _, kind := range kinds {
			var allowedBySQL bool
			if err = pool.QueryRow(ctx,
				`SELECT research_repair_action_allowed($1, $2)`, string(class), string(kind),
			).Scan(&allowedBySQL); err != nil {
				t.Fatalf("evaluate matrix for (%s, %s): %v", class, kind, err)
			}
			if allowedBySQL != RepairActionAllowed(class, kind) {
				mismatches = append(mismatches, string(class)+"/"+string(kind))
			}
		}
	}
	if len(mismatches) > 0 {
		t.Fatalf("executor and database repair matrices disagree on: %v", mismatches)
	}

	fixture := seedResearchRunFixture(t, ctx, pool)
	defer cleanupResearchCircuitFixture(pool, fixture)
	store := NewPostgresStore(pool)
	target, tasks := initializeCircuitFixture(t, ctx, store, fixture, 2)
	attempt := createCircuitAttempt(t, ctx, store, fixture, tasks[0], target)

	// A disallowed pair must be rejected by the constraint, not merely avoided
	// by the executor.
	_, err = pool.Exec(ctx, `
		INSERT INTO research_target_repair (
		  workspace_id, session_id, task_id, goal_version, plan_version,
		  failure_class, failure_fingerprint, repair_kind, repair_key,
		  first_attempt_id, last_attempt_id
		) VALUES ($1::uuid, $2::uuid, $3::uuid, 1, 1,
		  'credential', 'fingerprint-illegal', 'reroute_target', 'illegal-key',
		  $4::uuid, $4::uuid)
	`, fixture.workspaceID, fixture.sessionID, tasks[0].ID, attempt.ID)
	if err == nil {
		t.Fatal("database accepted a repair action the failure class does not allow")
	}

	// Control group: the allowed pair for the same class is accepted, so the
	// rejection above is about the matrix and not about the statement shape.
	if _, err = pool.Exec(ctx, `
		INSERT INTO research_target_repair (
		  workspace_id, session_id, task_id, goal_version, plan_version,
		  failure_class, failure_fingerprint, repair_kind, repair_key,
		  first_attempt_id, last_attempt_id
		) VALUES ($1::uuid, $2::uuid, $3::uuid, 1, 1,
		  'credential', 'fingerprint-legal', 'request_configuration', 'legal-key',
		  $4::uuid, $4::uuid)
	`, fixture.workspaceID, fixture.sessionID, tasks[0].ID, attempt.ID); err != nil {
		t.Fatalf("database rejected an allowed repair action: %v", err)
	}

	// The decision itself is append-only: a different action needs a different
	// key, which is a different row.
	if _, err = pool.Exec(ctx, `
		UPDATE research_target_repair SET repair_kind = 'reroute_target' WHERE repair_key = 'legal-key'
	`); err == nil {
		t.Fatal("database allowed a recorded repair decision to be rewritten in place")
	}
	if _, err = pool.Exec(ctx, `
		UPDATE research_target_repair SET occurrence_count = occurrence_count + 1 WHERE repair_key = 'legal-key'
	`); err != nil {
		t.Fatalf("database rejected a legitimate observation update: %v", err)
	}
}

// Two workers settling the same canonical failure concurrently must converge on
// one decision. The unique repair key is the arbiter; neither worker may create
// a second remediation path for the same cause.
func TestConcurrentWorkersConvergeOnOneTargetRepair(t *testing.T) {
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
	defer cleanupResearchCircuitFixture(pool, fixture)
	store := NewPostgresStore(pool)
	target, tasks := initializeCircuitFixture(t, ctx, store, fixture, 4)

	attempts := make([]Attempt, 0, 3)
	for i := 0; i < 3; i++ {
		attempts = append(attempts, createCircuitAttempt(t, ctx, store, fixture, tasks[0], target))
	}

	var waitGroup sync.WaitGroup
	errs := make([]error, len(attempts))
	start := make(chan struct{})
	for i, attempt := range attempts {
		waitGroup.Add(1)
		go func(index int, attemptID string) {
			defer waitGroup.Done()
			<-start
			_, errs[index] = store.FailAttempt(context.Background(), AttemptFailure{
				AttemptID: attemptID, FailureClass: string(FailureRateLimited),
				SourceReason: "agent_provider_capacity_or_rate_limit",
				Diagnostics:  "concurrent rate limit", Retryable: true,
			})
		}(i, attempt.ID)
	}
	close(start)
	waitGroup.Wait()

	settled := 0
	for _, failErr := range errs {
		if failErr == nil {
			settled++
		}
	}
	if settled == 0 {
		t.Fatalf("no concurrent worker settled its attempt: %v", errs)
	}
	repairs, err := store.ListTargetRepairs(ctx, fixture.sessionID)
	if err != nil || len(repairs) != 1 {
		t.Fatalf("concurrent settlement produced %d repairs (err=%v)", len(repairs), err)
	}
	if repairs[0].OccurrenceCount != settled {
		t.Fatalf("repair occurrence_count=%d, settled attempts=%d", repairs[0].OccurrenceCount, settled)
	}
	if repairs[0].RepairKind != RepairWaitForTarget {
		t.Fatalf("rate-limit repair kind=%q", repairs[0].RepairKind)
	}
}

// Migration 299 must roll back and forward cleanly so an image rollback is not
// blocked by the new schema.
func TestMigration299DownUpRestoresTargetRepairSchema(t *testing.T) {
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
	downSQL, err := os.ReadFile(filepath.Join("..", "..", "migrations", "299_research_target_repair.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	upSQL, err := os.ReadFile(filepath.Join("..", "..", "migrations", "299_research_target_repair.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	reapplied := false
	defer func() {
		if !reapplied {
			_, _ = pool.Exec(context.Background(), string(upSQL))
		}
	}()
	if _, err = pool.Exec(ctx, string(downSQL)); err != nil {
		t.Fatalf("apply migration 299 down: %v", err)
	}
	var tables, functions int
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_name = 'research_target_repair'
	`).Scan(&tables); err != nil || tables != 0 {
		t.Fatalf("target repair table after down=%d err=%v", tables, err)
	}
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname = current_schema() AND p.proname = 'research_repair_action_allowed'
	`).Scan(&functions); err != nil || functions != 0 {
		t.Fatalf("matrix function after down=%d err=%v", functions, err)
	}
	if _, err = pool.Exec(ctx, string(upSQL)); err != nil {
		t.Fatalf("reapply migration 299 up: %v", err)
	}
	reapplied = true
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_name = 'research_target_repair'
	`).Scan(&tables); err != nil || tables != 1 {
		t.Fatalf("target repair table after up=%d err=%v", tables, err)
	}
	var allowed bool
	if err = pool.QueryRow(ctx,
		`SELECT research_repair_action_allowed('credential', 'request_configuration')`,
	).Scan(&allowed); err != nil || !allowed {
		t.Fatalf("matrix function after up allowed=%v err=%v", allowed, err)
	}
}
