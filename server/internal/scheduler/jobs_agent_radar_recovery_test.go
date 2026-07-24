package scheduler

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type radarReplayRecorder struct {
	mu    sync.Mutex
	tasks []db.AgentInboxEvent
	runs  []db.AgentRadarRun
	err   error
}

func (r *radarReplayRecorder) ReplayCompletedAgentRadarTask(_ context.Context, task db.AgentInboxEvent, run db.AgentRadarRun) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasks = append(r.tasks, task)
	r.runs = append(r.runs, run)
	return r.err
}

func TestRecoverStaleCompletedRadarRunReplaysPersistedResultWithoutNewTask(t *testing.T) {
	agent := seedRadarTestAgent(t, "online")
	bindRadarSupervisor(t, agent)
	pool := integrationPool(t)
	q := db.New(pool)
	taskSvc := service.NewTaskService(q, pool, nil, events.New())

	run, task, err := taskSvc.EnqueueAgentRadarRun(t.Context(), service.EnqueueAgentRadarRunParams{
		WorkspaceID:    agent.workspaceID,
		AgentID:        agent.agentID,
		TriggerKind:    "scheduled",
		TriggerRef:     "rolling-old-completion",
		CooldownKey:    agentRadarCooldownKey,
		ContextSummary: "recover persisted completion",
		ScheduledFor:   time.Now(),
		Prompt:         "inspect",
	})
	if err != nil {
		t.Fatal(err)
	}
	persisted := []byte(`{"output":"{\"actions\":[{\"type\":\"no_action\"}]}"}`)
	if _, err := pool.Exec(t.Context(), `
		UPDATE agent_task_queue
		SET status = 'completed', completed_at = now() - interval '6 minutes', result = $2
		WHERE id = $1
	`, task.ID, persisted); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `
		UPDATE agent_radar_run
		SET status = 'running', updated_at = now() - interval '6 minutes'
		WHERE id = $1
	`, run.ID); err != nil {
		t.Fatal(err)
	}

	replayer := &radarReplayRecorder{}
	recovered, err := recoverStaleCompletedRadarRuns(t.Context(), pool, replayer)
	if err != nil {
		t.Fatal(err)
	}
	if recovered != 1 || len(replayer.tasks) != 1 || len(replayer.runs) != 1 {
		t.Fatalf("recovery = count:%d tasks:%d runs:%d, want 1/1/1", recovered, len(replayer.tasks), len(replayer.runs))
	}
	var gotResult, wantResult map[string]any
	if err := json.Unmarshal(replayer.tasks[0].Result, &gotResult); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(persisted, &wantResult); err != nil {
		t.Fatal(err)
	}
	if gotResult["output"] != wantResult["output"] || replayer.runs[0].Status != "executing" {
		t.Fatalf("replayed task/run = result:%s status:%s, want persisted/executing", replayer.tasks[0].Result, replayer.runs[0].Status)
	}
	var taskCount int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM agent_task_queue WHERE agent_id = $1`, agent.agentID).Scan(&taskCount); err != nil {
		t.Fatal(err)
	}
	if taskCount != 1 {
		t.Fatalf("recovery created %d tasks, want original task only", taskCount)
	}

	recovered, err = recoverStaleCompletedRadarRuns(t.Context(), pool, replayer)
	if err != nil {
		t.Fatal(err)
	}
	if recovered != 0 || len(replayer.tasks) != 1 {
		t.Fatalf("fresh execution lease replayed again: count:%d calls:%d", recovered, len(replayer.tasks))
	}
	stored, err := q.GetAgentRadarRun(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "executing" || time.Since(stored.UpdatedAt.Time) > time.Minute {
		t.Fatalf("stored recovered run = status:%s updated:%v", stored.Status, stored.UpdatedAt)
	}
}

func TestRecoverStaleCompletedRadarRunSkipsReboundSupervisor(t *testing.T) {
	oldSupervisor := seedRadarTestAgent(t, "online")
	bindRadarSupervisor(t, oldSupervisor)
	newSupervisorID := seedAdditionalRadarTestAgent(t, oldSupervisor, "Replacement Wendy", false)
	pool := integrationPool(t)
	taskSvc := service.NewTaskService(db.New(pool), pool, nil, events.New())

	run, task, err := taskSvc.EnqueueAgentRadarRun(t.Context(), service.EnqueueAgentRadarRunParams{
		WorkspaceID:    oldSupervisor.workspaceID,
		AgentID:        oldSupervisor.agentID,
		TriggerKind:    "scheduled",
		TriggerRef:     "rebound-stale-completion",
		CooldownKey:    agentRadarCooldownKey,
		ContextSummary: "must not replay after rebind",
		ScheduledFor:   time.Now(),
		Prompt:         "inspect",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `
		UPDATE agent_task_queue
		SET status = 'completed', completed_at = now() - interval '6 minutes', result = '{"output":"stale"}'::jsonb
		WHERE id = $1
	`, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `
		UPDATE agent_radar_run
		SET status = 'executing', updated_at = now() - interval '6 minutes'
		WHERE id = $1
	`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `
		UPDATE workspace_radar_state
		SET supervisor_agent_id = $2, updated_at = now()
		WHERE workspace_id = $1
	`, oldSupervisor.workspaceID, newSupervisorID); err != nil {
		t.Fatal(err)
	}

	replayer := &radarReplayRecorder{}
	recovered, err := recoverStaleCompletedRadarRuns(t.Context(), pool, replayer)
	if err != nil {
		t.Fatal(err)
	}
	if recovered != 0 || len(replayer.tasks) != 0 {
		t.Fatalf("rebound stale run replayed: count:%d calls:%d", recovered, len(replayer.tasks))
	}
}
