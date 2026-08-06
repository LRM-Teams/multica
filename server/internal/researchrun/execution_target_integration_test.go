package researchrun

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestExecutionTargetIsFrozenIntoAttemptAndOutbox(t *testing.T) {
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
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1::uuid`, fixture.workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1::uuid`, fixture.userID)
	}()
	store := NewPostgresStore(pool)
	if _, _, err = store.InitializeRun(ctx, StartInput{
		SessionID: fixture.sessionID, WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID,
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Test frozen execution target",
		Title: "Frozen execution target", DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard")); err != nil {
		t.Fatal(err)
	}
	members, err := store.ListFleetMembers(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil || len(members) == 0 {
		t.Fatalf("members=%+v err=%v", members, err)
	}
	target := selectedExecutionTarget(fixture.agentID, members)
	if target.Adapter != "agent_inbox" || target.RuntimeID == "" || target.Provider != "codex" || target.Model != "test-model" || len(target.ConfigFingerprint) != 64 {
		t.Fatalf("resolved target=%+v", target)
	}
	tasks, err := store.ListTasks(ctx, fixture.sessionID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks=%+v err=%v", tasks, err)
	}
	run, err := store.GetRun(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	attemptID := uuid.NewString()
	request := DispatchRequest{Run: run, Task: tasks[0], AttemptID: attemptID, AgentID: fixture.agentID, Target: target, Prompt: "frozen target", Key: "research-target:" + attemptID}
	request.RequestHash, err = HashDispatchRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	attempt, _, err := store.CreateDispatchIntent(ctx, CreateDispatchIntentInput{
		AttemptID: attemptID, SessionID: fixture.sessionID, TaskID: tasks[0].ID,
		AgentID: fixture.agentID, Target: target, ExpectedStateVersion: run.StateVersion, Request: request,
	})
	if err != nil {
		t.Fatal(err)
	}
	if attempt.ExecutionTarget != target {
		t.Fatalf("attempt target=%+v want=%+v", attempt.ExecutionTarget, target)
	}
	claimed, err := store.ClaimDispatchIntents(ctx, fixture.sessionID, uuid.NewString(), time.Minute, 1)
	if err != nil || len(claimed) != 1 || claimed[0].Request.Target != target {
		t.Fatalf("claimed=%+v err=%v", claimed, err)
	}
	_, mutationErr := pool.Exec(ctx, `UPDATE research_task_attempt SET model = 'other-model' WHERE id = $1::uuid`, attempt.ID)
	var pgErr *pgconn.PgError
	if !errors.As(mutationErr, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "research_task_attempt_execution_target_immutable_check" {
		t.Fatalf("immutable target mutation error=%v", mutationErr)
	}
}
