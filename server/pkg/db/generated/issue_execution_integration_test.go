package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestIssueExecutionFoundationContracts(t *testing.T) {
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

	workspaceID, agentID, issueID := seedIssueExecutionFixture(t, ctx, pool, "primary")
	foreignWorkspaceID, foreignAgentID, _ := seedIssueExecutionFixture(t, ctx, pool, "foreign")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id IN ($1, $2)`, workspaceID, foreignWorkspaceID)
	})
	queries := New(pool)

	attemptOne, err := queries.AllocateIssueExecutionAttempt(ctx, AllocateIssueExecutionAttemptParams{
		IssueID:                   issueID,
		WorkspaceID:               workspaceID,
		ExpectedExecutionRevision: 0,
	})
	if err != nil {
		t.Fatalf("allocate first attempt: %v", err)
	}
	if attemptOne.AttemptNumber != 1 || attemptOne.ExecutionRevision != 0 {
		t.Fatalf("first attempt = %+v, want revision 0 attempt 1", attemptOne)
	}

	runIDs := []pgtype.UUID{newPGUUID(), newPGUUID()}
	results := make(chan error, len(runIDs))
	var start sync.WaitGroup
	start.Add(1)
	for _, runID := range runIDs {
		runID := runID
		go func() {
			start.Wait()
			_, createErr := queries.CreateActiveIssueExecution(ctx, CreateActiveIssueExecutionParams{
				WorkspaceID:            workspaceID,
				IssueID:                issueID,
				RunID:                  runID,
				AgentID:                agentID,
				IssueExecutionRevision: 0,
				AttemptNumber:          1,
			})
			results <- createErr
		}()
	}
	start.Done()

	var successCount int
	for range runIDs {
		if createErr := <-results; createErr == nil {
			successCount++
		}
	}
	if successCount != 1 {
		t.Fatalf("concurrent active claims succeeded=%d, want exactly 1", successCount)
	}
	active, err := queries.GetActiveIssueExecution(ctx, GetActiveIssueExecutionParams{
		WorkspaceID: workspaceID,
		IssueID:     issueID,
	})
	if err != nil {
		t.Fatalf("get active claim: %v", err)
	}

	if _, err = queries.CreateActiveIssueExecution(ctx, CreateActiveIssueExecutionParams{
		WorkspaceID:            workspaceID,
		IssueID:                issueID,
		RunID:                  newPGUUID(),
		AgentID:                foreignAgentID,
		IssueExecutionRevision: 0,
		AttemptNumber:          2,
	}); err == nil {
		t.Fatal("cross-workspace agent acquired an Issue execution claim")
	}

	payload := []byte(`{"reason":"assignment"}`)
	hash := sha256.Sum256(payload)
	firstOutbox, err := queries.CreateIssueDispatchOutbox(ctx, CreateIssueDispatchOutboxParams{
		WorkspaceID:            workspaceID,
		IssueID:                issueID,
		RunID:                  active.RunID,
		AgentID:                agentID,
		IssueExecutionRevision: 0,
		AttemptNumber:          1,
		DispatchKey:            "issue-run:" + uuid.NewString(),
		TriggerKind:            "assignment",
		RequestPayload:         payload,
		RequestHash:            hex.EncodeToString(hash[:]),
	})
	if err != nil {
		t.Fatalf("create first outbox intent: %v", err)
	}
	idempotent, err := queries.CreateIssueDispatchOutbox(ctx, CreateIssueDispatchOutboxParams{
		WorkspaceID:            firstOutbox.WorkspaceID,
		IssueID:                firstOutbox.IssueID,
		RunID:                  firstOutbox.RunID,
		AgentID:                firstOutbox.AgentID,
		IssueExecutionRevision: firstOutbox.IssueExecutionRevision,
		AttemptNumber:          firstOutbox.AttemptNumber,
		DispatchKey:            firstOutbox.DispatchKey,
		TriggerKind:            firstOutbox.TriggerKind,
		RequestPayload:         payload,
		RequestHash:            firstOutbox.RequestHash,
	})
	if err != nil || idempotent.ID != firstOutbox.ID {
		t.Fatalf("idempotent outbox replay = %+v, err=%v", idempotent, err)
	}
	conflictingHash := sha256.Sum256([]byte(`{"reason":"different"}`))
	_, err = queries.CreateIssueDispatchOutbox(ctx, CreateIssueDispatchOutboxParams{
		WorkspaceID:            firstOutbox.WorkspaceID,
		IssueID:                firstOutbox.IssueID,
		RunID:                  firstOutbox.RunID,
		AgentID:                firstOutbox.AgentID,
		IssueExecutionRevision: firstOutbox.IssueExecutionRevision,
		AttemptNumber:          firstOutbox.AttemptNumber,
		DispatchKey:            firstOutbox.DispatchKey,
		TriggerKind:            firstOutbox.TriggerKind,
		RequestPayload:         []byte(`{"reason":"different"}`),
		RequestHash:            hex.EncodeToString(conflictingHash[:]),
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("conflicting outbox replay err=%v, want pgx.ErrNoRows", err)
	}

	if _, err = pool.Exec(ctx, `
		INSERT INTO agent_inbox_event (id, workspace_id, agent_id, issue_id, reason)
		VALUES ($1, $2, $3, $4, 'issue')
	`, active.RunID, workspaceID, agentID, issueID); err != nil {
		t.Fatalf("create logical Run intent: %v", err)
	}
	boundEvent, err := queries.BindCanonicalIssueRunEvent(ctx, BindCanonicalIssueRunEventParams{
		RunID:       active.RunID,
		WorkspaceID: workspaceID,
	})
	if err != nil || !boundEvent.IssueRunKind.Valid || boundEvent.IssueRunKind.String != "canonical" {
		t.Fatalf("bind canonical Run = %+v, err=%v", boundEvent, err)
	}
	leaseToken := newPGUUID()
	claimed, err := queries.ClaimIssueDispatchOutbox(ctx, ClaimIssueDispatchOutboxParams{
		LeaseToken:     leaseToken,
		LeaseExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Minute), Valid: true},
		WorkspaceID:    workspaceID,
		ClaimLimit:     10,
	})
	if err != nil || len(claimed) != 1 || claimed[0].ID != firstOutbox.ID {
		t.Fatalf("claim outbox = %+v, err=%v", claimed, err)
	}
	_, err = queries.MarkIssueDispatchOutboxDelivered(ctx, MarkIssueDispatchOutboxDeliveredParams{
		WorkspaceID: workspaceID,
		OutboxID:    firstOutbox.ID,
		RunID:       active.RunID,
		LeaseToken:  newPGUUID(),
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("stale outbox lease err=%v, want pgx.ErrNoRows", err)
	}
	delivered, err := queries.MarkIssueDispatchOutboxDelivered(ctx, MarkIssueDispatchOutboxDeliveredParams{
		WorkspaceID: workspaceID,
		OutboxID:    firstOutbox.ID,
		RunID:       active.RunID,
		LeaseToken:  leaseToken,
	})
	if err != nil || delivered.Status != "delivered" || delivered.DeliveredEventID != active.RunID {
		t.Fatalf("deliver outbox = %+v, err=%v", delivered, err)
	}
	if _, err = pool.Exec(ctx, `
		UPDATE agent_inbox_event
		SET status = 'acked', terminal_outcome = 'completed', terminal_at = now(), acked_at = now()
		WHERE id = $1
	`, active.RunID); err != nil {
		t.Fatalf("terminalize first logical Run: %v", err)
	}

	if _, err = queries.BeginReleaseIssueExecution(ctx, BeginReleaseIssueExecutionParams{
		WorkspaceID: workspaceID,
		IssueID:     issueID,
		RunID:       active.RunID,
	}); err != nil {
		t.Fatalf("begin release: %v", err)
	}
	if rows, deleteErr := queries.DeleteReleasedIssueExecution(ctx, DeleteReleasedIssueExecutionParams{
		WorkspaceID: workspaceID,
		IssueID:     issueID,
		RunID:       active.RunID,
	}); deleteErr != nil || rows != 1 {
		t.Fatalf("delete released claim rows=%d err=%v", rows, deleteErr)
	}

	attemptTwo, err := queries.AllocateIssueExecutionAttempt(ctx, AllocateIssueExecutionAttemptParams{
		IssueID:                   issueID,
		WorkspaceID:               workspaceID,
		ExpectedExecutionRevision: 0,
	})
	if err != nil || attemptTwo.AttemptNumber != 2 {
		t.Fatalf("same-revision retry attempt = %+v, err=%v", attemptTwo, err)
	}
	runTwo := newPGUUID()
	if _, err = queries.CreateActiveIssueExecution(ctx, CreateActiveIssueExecutionParams{
		WorkspaceID:            workspaceID,
		IssueID:                issueID,
		RunID:                  runTwo,
		AgentID:                agentID,
		IssueExecutionRevision: 0,
		AttemptNumber:          2,
	}); err != nil {
		t.Fatalf("create same-revision retry claim: %v", err)
	}
	if _, err = queries.CreateIssueDispatchOutbox(ctx, CreateIssueDispatchOutboxParams{
		WorkspaceID:            workspaceID,
		IssueID:                issueID,
		RunID:                  runTwo,
		AgentID:                agentID,
		IssueExecutionRevision: 0,
		AttemptNumber:          2,
		DispatchKey:            "issue-run:" + uuid.NewString(),
		TriggerKind:            "retry",
		RequestPayload:         payload,
		RequestHash:            hex.EncodeToString(hash[:]),
	}); err != nil {
		t.Fatalf("same revision with a new attempt was rejected: %v", err)
	}
}

func TestCanonicalIssueRunFenceDoesNotCaptureLegacyIssueTraffic(t *testing.T) {
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

	workspaceID, agentID, issueID := seedIssueExecutionFixture(t, ctx, pool, "inbox")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID)
	})

	canonicalRunID := newPGUUID()
	if _, err = pool.Exec(ctx, `
		INSERT INTO agent_inbox_event (
			id, workspace_id, agent_id, issue_id, reason,
			issue_run_kind, issue_execution_revision, issue_execution_attempt_number
		) VALUES ($1, $2, $3, $4, 'issue', 'canonical', 0, 1)
	`, canonicalRunID, workspaceID, agentID, issueID); err != nil {
		t.Fatalf("insert canonical Issue Run: %v", err)
	}
	if _, err = pool.Exec(ctx, `
		INSERT INTO agent_inbox_event (workspace_id, agent_id, issue_id, reason)
		VALUES ($1, $2, $3, 'issue'), ($1, $2, $3, 'mention')
	`, workspaceID, agentID, issueID); err != nil {
		t.Fatalf("legacy Issue traffic did not coexist with canonical Run: %v", err)
	}
	if _, err = pool.Exec(ctx, `
		INSERT INTO agent_inbox_event (
			workspace_id, agent_id, issue_id, reason,
			issue_run_kind, issue_execution_revision, issue_execution_attempt_number
		) VALUES ($1, $2, $3, 'issue', 'canonical', 0, 2)
	`, workspaceID, agentID, issueID); err == nil {
		t.Fatal("second active canonical Issue Run bypassed the inbox fence")
	}

	for range 2 {
		if _, err = pool.Exec(ctx, `
			INSERT INTO agent_execution (
				id, source_kind, source_event_id, source, workspace_id, agent_id, issue_id
			) VALUES ($1, 'inbox', $2, 'issue', $3, $4, $5)
		`, newPGUUID(), canonicalRunID, workspaceID, agentID, issueID); err != nil {
			t.Fatalf("one logical Run rejected a provider execution attempt: %v", err)
		}
	}
	var executionCount int
	if err = pool.QueryRow(ctx, `SELECT count(*)::int FROM agent_execution WHERE source_event_id = $1`, canonicalRunID).Scan(&executionCount); err != nil {
		t.Fatal(err)
	}
	if executionCount != 2 {
		t.Fatalf("provider execution attempts=%d, want 2", executionCount)
	}
}

func TestIssueGoalScopeIsWorkspaceBoundAndDetachesAtomically(t *testing.T) {
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

	suffix := uuid.NewString()
	var userID pgtype.UUID
	if err = pool.QueryRow(ctx,
		`INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id`,
		"Issue goal scope", "issue-goal-scope-"+suffix+"@example.test",
	).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID)
	})

	workspaceOne := insertGoalScopeWorkspace(t, ctx, pool, userID, suffix+"-one")
	workspaceTwo := insertGoalScopeWorkspace(t, ctx, pool, userID, suffix+"-two")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id IN ($1, $2)`, workspaceOne, workspaceTwo)
	})
	goalOne := insertGoalScopeGoal(t, ctx, pool, workspaceOne, userID, suffix+"-one")
	goalTwo := insertGoalScopeGoal(t, ctx, pool, workspaceTwo, userID, suffix+"-two")
	var issueID pgtype.UUID
	if err = pool.QueryRow(ctx, `
		INSERT INTO issue (
			workspace_id, title, creator_type, creator_id, channel_goal_id, goal_required
		) VALUES ($1, 'Goal scoped Issue', 'member', $2, $3, true)
		RETURNING id
	`, workspaceOne, userID, goalOne).Scan(&issueID); err != nil {
		t.Fatalf("create Goal-scoped Issue: %v", err)
	}
	if _, err = pool.Exec(ctx,
		`UPDATE issue SET channel_goal_id = $1 WHERE id = $2`,
		goalTwo, issueID,
	); err == nil {
		t.Fatal("cross-workspace Goal was attached to Issue")
	}
	if _, err = pool.Exec(ctx, `DELETE FROM channel_goal WHERE id = $1`, goalOne); err != nil {
		t.Fatalf("delete Goal: %v", err)
	}
	var detachedGoalID pgtype.UUID
	var goalRequired pgtype.Bool
	var revision int64
	if err = pool.QueryRow(ctx, `
		SELECT channel_goal_id, goal_required, execution_revision
		FROM issue
		WHERE id = $1
	`, issueID).Scan(&detachedGoalID, &goalRequired, &revision); err != nil {
		t.Fatal(err)
	}
	if detachedGoalID.Valid || goalRequired.Valid || revision != 1 {
		t.Fatalf("detached scope goal=%+v required=%+v revision=%d", detachedGoalID, goalRequired, revision)
	}
}

func insertGoalScopeWorkspace(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID pgtype.UUID, suffix string) pgtype.UUID {
	t.Helper()
	var workspaceID pgtype.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug) VALUES ($1, $2) RETURNING id`,
		"Goal scope "+suffix, "goal-scope-"+suffix,
	).Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`,
		workspaceID, userID,
	); err != nil {
		t.Fatal(err)
	}
	return workspaceID
}

func insertGoalScopeGoal(t *testing.T, ctx context.Context, pool *pgxpool.Pool, workspaceID, userID pgtype.UUID, suffix string) pgtype.UUID {
	t.Helper()
	var channelID, goalID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, created_by, kind)
		VALUES ($1, $2, $3, 'dm')
		RETURNING id
	`, workspaceID, "goal-scope-channel-"+suffix, userID).Scan(&channelID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO channel_goal (
			workspace_id, channel_id, title, objective, success_criteria,
			created_by_type, created_by_id, updated_by_type, updated_by_id
		) VALUES ($1, $2, 'Goal scope', 'Verify Goal scope', '["verified"]', 'user', $3, 'user', $3)
		RETURNING id
	`, workspaceID, channelID, userID).Scan(&goalID); err != nil {
		t.Fatal(err)
	}
	return goalID
}

func seedIssueExecutionFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, label string) (pgtype.UUID, pgtype.UUID, pgtype.UUID) {
	t.Helper()
	suffix := uuid.NewString()
	var workspaceID, runtimeID, agentID, issueID pgtype.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug) VALUES ($1, $2) RETURNING id`,
		"Issue execution "+label, "issue-execution-"+suffix,
	).Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider)
		VALUES ($1, $2, 'local', 'test')
		RETURNING id
	`, workspaceID, "issue-execution-runtime-"+suffix).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id)
		VALUES ($1, $2, 'local', $3)
		RETURNING id
	`, workspaceID, "issue-execution-"+label+"-"+suffix, runtimeID).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO issue (
			workspace_id, title, status, assignee_type, assignee_id, creator_type, creator_id
		) VALUES ($1, $2, 'todo', 'agent', $3, 'agent', $3)
		RETURNING id
	`, workspaceID, fmt.Sprintf("Issue execution %s", label), agentID).Scan(&issueID); err != nil {
		t.Fatal(err)
	}
	return workspaceID, agentID, issueID
}

func newPGUUID() pgtype.UUID {
	return pgtype.UUID{Bytes: uuid.New(), Valid: true}
}
