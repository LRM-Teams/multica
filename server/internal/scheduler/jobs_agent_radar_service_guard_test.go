package scheduler

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestEnqueueScheduledRadarRejectsStaleSupervisorCandidate(t *testing.T) {
	oldSupervisor := seedRadarTestAgent(t, "online")
	bindRadarSupervisor(t, oldSupervisor)
	newSupervisorID := seedAdditionalRadarTestAgent(t, oldSupervisor, "Replacement Wendy", false)
	pool := integrationPool(t)
	q := db.New(pool)
	taskSvc := service.NewTaskService(q, pool, nil, events.New())

	if _, err := pool.Exec(t.Context(), `
		UPDATE workspace_radar_state
		SET supervisor_agent_id = $2, next_due_at = now(), updated_at = now()
		WHERE workspace_id = $1
	`, oldSupervisor.workspaceID, newSupervisorID); err != nil {
		t.Fatalf("rebind workspace supervisor: %v", err)
	}

	_, _, err := taskSvc.EnqueueAgentRadarRun(t.Context(), service.EnqueueAgentRadarRunParams{
		WorkspaceID:    oldSupervisor.workspaceID,
		AgentID:        oldSupervisor.agentID,
		TriggerKind:    "scheduled",
		TriggerRef:     "stale-candidate",
		CooldownKey:    agentRadarCooldownKey,
		ContextSummary: "stale candidate must be rejected",
		ScheduledFor:   time.Now(),
		Prompt:         "inspect",
	})
	if err == nil {
		t.Fatal("stale scheduled supervisor candidate was enqueued")
	}
	var runCount int
	if queryErr := pool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM agent_radar_run
		WHERE workspace_id = $1 AND trigger_ref = 'stale-candidate'
	`, oldSupervisor.workspaceID).Scan(&runCount); queryErr != nil {
		t.Fatal(queryErr)
	}
	if runCount != 0 {
		t.Fatalf("stale candidate created %d runs, want 0", runCount)
	}
}

func TestCompleteDaemonTaskDoesNotTreatRadarClaimMissAsIdempotent(t *testing.T) {
	agent := seedRadarTestAgent(t, "online")
	bindRadarSupervisor(t, agent)
	pool := integrationPool(t)
	q := db.New(pool)
	taskSvc := service.NewTaskService(q, pool, nil, events.New())

	run, task, err := taskSvc.EnqueueAgentRadarRun(t.Context(), service.EnqueueAgentRadarRunParams{
		WorkspaceID:    agent.workspaceID,
		AgentID:        agent.agentID,
		TriggerKind:    "scheduled",
		TriggerRef:     "claim-miss",
		CooldownKey:    agentRadarCooldownKey,
		ContextSummary: "claim miss must remain an error",
		ScheduledFor:   time.Now(),
		Prompt:         "inspect",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE agent_inbox_event SET status = 'draining', claimed_at = now() WHERE id = $1`, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := taskSvc.StartTask(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE agent_radar_run SET status = 'failed', finished_at = now() WHERE id = $1`, run.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := taskSvc.CompleteDaemonTask(t.Context(), task.ID, []byte(`{"output":"late"}`), "", ""); err == nil || !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("claim-miss completion error = %v, want wrapped pgx.ErrNoRows", err)
	}
	stored, err := q.GetAgentTask(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "draining" {
		t.Fatalf("claim-miss task status = %q, want rolled-back draining", stored.Status)
	}
}
