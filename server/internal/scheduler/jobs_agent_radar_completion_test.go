package scheduler

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestCompleteDaemonTaskAtomicallyClaimsRadarAndIgnoresRetryOutput(t *testing.T) {
	agent := seedRadarTestAgent(t, "online")
	bindRadarSupervisor(t, agent)
	pool := integrationPool(t)
	q := db.New(pool)
	taskSvc := service.NewTaskService(q, pool, nil, events.New())

	run, task, err := taskSvc.EnqueueAgentRadarRun(t.Context(), service.EnqueueAgentRadarRunParams{
		WorkspaceID:    agent.workspaceID,
		AgentID:        agent.agentID,
		TriggerKind:    "scheduled",
		TriggerRef:     "atomic-completion",
		CooldownKey:    agentRadarCooldownKey,
		ContextSummary: "atomic completion test",
		ScheduledFor:   time.Now(),
		Prompt:         "inspect",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE agent_task_queue SET status = 'dispatched' WHERE id = $1`, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := taskSvc.StartTask(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}

	firstResult := []byte(`{"output":"first persisted plan"}`)
	first, err := taskSvc.CompleteDaemonTask(t.Context(), task.ID, firstResult, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !first.CompletedNow || first.RadarRun == nil || first.RadarRun.Status != "executing" {
		t.Fatalf("first completion = completed:%v run:%+v, want true/executing", first.CompletedNow, first.RadarRun)
	}

	secondResult := []byte(`{"output":"different retry plan"}`)
	second, err := taskSvc.CompleteDaemonTask(t.Context(), task.ID, secondResult, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if second.CompletedNow || second.RadarRun != nil {
		t.Fatalf("retry completion = completed:%v run:%+v, want false/nil", second.CompletedNow, second.RadarRun)
	}
	stored, err := q.GetAgentTask(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	var storedPayload map[string]string
	if err := json.Unmarshal(stored.Result, &storedPayload); err != nil {
		t.Fatalf("decode stored result: %v", err)
	}
	if storedPayload["output"] != "first persisted plan" {
		t.Fatalf("stored output = %q, want first persisted plan", storedPayload["output"])
	}

	// Losing the executing lease (for example through a Wendy rebind) must
	// make a late success finalization fail instead of reviving the run.
	if _, err := pool.Exec(t.Context(), `UPDATE agent_radar_run SET status = 'cancelled' WHERE id = $1`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := q.UpdateAgentRadarRunStatus(t.Context(), db.UpdateAgentRadarRunStatusParams{
		ID:     run.ID,
		Status: "succeeded",
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("late success finalization error = %v, want pgx.ErrNoRows", err)
	}
	var runStatus string
	if err := pool.QueryRow(t.Context(), `SELECT status FROM agent_radar_run WHERE id = $1`, run.ID).Scan(&runStatus); err != nil {
		t.Fatal(err)
	}
	if runStatus != "cancelled" {
		t.Fatalf("late finalization revived run to %q", runStatus)
	}
}

func TestCompleteDaemonTaskAfterCancellationHasNoRadarExecutionAuthority(t *testing.T) {
	agent := seedRadarTestAgent(t, "online")
	bindRadarSupervisor(t, agent)
	pool := integrationPool(t)
	taskSvc := service.NewTaskService(db.New(pool), pool, nil, events.New())

	run, task, err := taskSvc.EnqueueAgentRadarRun(t.Context(), service.EnqueueAgentRadarRunParams{
		WorkspaceID:    agent.workspaceID,
		AgentID:        agent.agentID,
		TriggerKind:    "scheduled",
		TriggerRef:     "cancel-wins",
		CooldownKey:    agentRadarCooldownKey,
		ContextSummary: "cancel wins test",
		ScheduledFor:   time.Now(),
		Prompt:         "inspect",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE agent_task_queue SET status = 'dispatched' WHERE id = $1`, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := taskSvc.StartTask(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := taskSvc.CancelTask(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}

	outcome, err := taskSvc.CompleteDaemonTask(t.Context(), task.ID, []byte(`{"output":"late plan"}`), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if outcome.CompletedNow || outcome.RadarRun != nil {
		t.Fatalf("late completion = completed:%v run:%+v, want false/nil", outcome.CompletedNow, outcome.RadarRun)
	}
	var taskStatus, runStatus string
	if err := pool.QueryRow(t.Context(), `
		SELECT task.status, run.status
		FROM agent_task_queue task
		JOIN agent_radar_run run ON run.task_id = task.id
		WHERE run.id = $1
	`, run.ID).Scan(&taskStatus, &runStatus); err != nil {
		t.Fatal(err)
	}
	if taskStatus != "cancelled" || runStatus != "cancelled" {
		t.Fatalf("cancelled pair = task:%q run:%q", taskStatus, runStatus)
	}
}

func TestReconcileLeavesFreshCompletedUnclaimedRadarForCompatibilityWindow(t *testing.T) {
	agent := seedRadarTestAgent(t, "online")
	bindRadarSupervisor(t, agent)
	pool := integrationPool(t)
	taskSvc := service.NewTaskService(db.New(pool), pool, nil, events.New())

	run, task, err := taskSvc.EnqueueAgentRadarRun(t.Context(), service.EnqueueAgentRadarRunParams{
		WorkspaceID:    agent.workspaceID,
		AgentID:        agent.agentID,
		TriggerKind:    "scheduled",
		TriggerRef:     "completion-compatibility-window",
		CooldownKey:    agentRadarCooldownKey,
		ContextSummary: "compatibility window test",
		ScheduledFor:   time.Now(),
		Prompt:         "inspect",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `
		UPDATE agent_task_queue SET status = 'completed', completed_at = now() WHERE id = $1
	`, task.ID); err != nil {
		t.Fatal(err)
	}
	if repaired, err := reconcileTerminalRadarRuns(t.Context(), pool); err != nil {
		t.Fatal(err)
	} else if repaired != 0 {
		t.Fatalf("fresh completed run repaired = %d, want 0", repaired)
	}
	if _, err := pool.Exec(t.Context(), `
		UPDATE agent_task_queue SET completed_at = now() - interval '6 minutes' WHERE id = $1
	`, task.ID); err != nil {
		t.Fatal(err)
	}
	if repaired, err := reconcileTerminalRadarRuns(t.Context(), pool); err != nil {
		t.Fatal(err)
	} else if repaired != 1 {
		t.Fatalf("stale completed run repaired = %d, want 1", repaired)
	}
	var status string
	if err := pool.QueryRow(t.Context(), `SELECT status FROM agent_radar_run WHERE id = $1`, run.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "failed" {
		t.Fatalf("stale completed run status = %q, want failed", status)
	}
}
