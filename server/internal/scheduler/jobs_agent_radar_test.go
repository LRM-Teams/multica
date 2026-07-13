package scheduler

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestAgentRadarScheduleJobSpec(t *testing.T) {
	job := AgentRadarScheduleJob(nil, nil)
	if job.Name != JobNameAgentRadarSchedule {
		t.Fatalf("Name = %q, want %q", job.Name, JobNameAgentRadarSchedule)
	}
	if job.Cadence != 10*time.Minute {
		t.Fatalf("Cadence = %s, want 10m", job.Cadence)
	}
	if job.CatchUpMode != CatchUpLatestOnly {
		t.Fatalf("CatchUpMode = %q, want latest_only", job.CatchUpMode)
	}
	if err := job.validate(); err != nil {
		t.Fatalf("job did not validate: %v", err)
	}
	res, err := job.Handler(t.Context(), HandlerInput{PlanTime: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Result["reason"] != "db_unavailable" {
		t.Fatalf("nil-db handler result = %#v, want db_unavailable skip", res.Result)
	}
}

type radarWakeRecorder struct {
	runtimeID string
	taskID    string
	calls     int
}

func (r *radarWakeRecorder) NotifyTaskAvailable(runtimeID, taskID string) {
	r.runtimeID = runtimeID
	r.taskID = taskID
	r.calls++
}

type radarTestAgent struct {
	workspaceID pgtype.UUID
	agentID     pgtype.UUID
	runtimeID   pgtype.UUID
}

func seedRadarTestAgent(t *testing.T, runtimeStatus string) radarTestAgent {
	t.Helper()
	pool := integrationPool(t)
	ctx := context.Background()
	q := db.New(pool)
	suffix := uuid.NewString()

	workspace, err := q.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name:        "radar-" + suffix,
		Slug:        "radar-" + suffix,
		IssuePrefix: "RDR",
	})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	var userID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO "user" (name, email)
		VALUES ($1, $2)
		RETURNING id
	`, "radar-user-"+suffix, fmt.Sprintf("radar-%s@example.test", suffix)).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspace.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID)
	})

	var runtimeID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider,
			status, device_info, metadata, visibility, last_seen_at
		) VALUES ($1, $2, $3, 'cloud', 'daytona', $4, '', '{}'::jsonb, 'private', now())
		RETURNING id
	`, workspace.ID, "radar-daemon-"+suffix, "radar-runtime", runtimeStatus).Scan(&runtimeID); err != nil {
		t.Fatalf("create runtime: %v", err)
	}

	agent, err := q.CreateAgent(ctx, db.CreateAgentParams{
		WorkspaceID:        workspace.ID,
		Name:               "radar-agent-" + suffix,
		DisplayName:        "Radar Agent",
		Description:        "test radar scheduling",
		RuntimeMode:        "cloud",
		RuntimeConfig:      []byte("{}"),
		RuntimeID:          runtimeID,
		Visibility:         "workspace",
		MaxConcurrentTasks: 1,
		OwnerID:            userID,
		Instructions:       "",
		CustomEnv:          []byte("{}"),
		CustomArgs:         []byte("[]"),
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	var channelID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, created_by)
		VALUES ($1, $2, $3)
		RETURNING id
	`, workspace.ID, "radar-channel-"+suffix, userID).Scan(&channelID); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
	`, channelID, workspace.ID, agent.ID); err != nil {
		t.Fatalf("add channel member: %v", err)
	}

	return radarTestAgent{workspaceID: workspace.ID, agentID: agent.ID, runtimeID: runtimeID}
}

func hasRadarCandidate(candidates []radarCandidate, agentID pgtype.UUID) bool {
	want := uuidString(agentID)
	for _, candidate := range candidates {
		if uuidString(candidate.AgentID) == want {
			return true
		}
	}
	return false
}

func TestListRadarCandidatesExcludesOfflineAndActiveRuns(t *testing.T) {
	online := seedRadarTestAgent(t, "online")
	offline := seedRadarTestAgent(t, "offline")
	pool := integrationPool(t)

	candidates, err := listRadarCandidates(t.Context(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if !hasRadarCandidate(candidates, online.agentID) {
		t.Fatal("online agent was not selected")
	}
	if hasRadarCandidate(candidates, offline.agentID) {
		t.Fatal("offline agent was selected")
	}

	wake := &radarWakeRecorder{}
	taskSvc := service.NewTaskService(db.New(pool), pool, nil, events.New(), wake)
	_, _, err = taskSvc.EnqueueAgentRadarRun(t.Context(), service.EnqueueAgentRadarRunParams{
		WorkspaceID:    online.workspaceID,
		AgentID:        online.agentID,
		TriggerKind:    "scheduled",
		TriggerRef:     time.Now().UTC().Format(time.RFC3339),
		CooldownKey:    agentRadarCooldownKey,
		ContextSummary: "test radar",
		ScheduledFor:   time.Now(),
		Prompt:         "inspect the workspace",
	})
	if err != nil {
		t.Fatalf("enqueue radar: %v", err)
	}
	if wake.calls != 1 || wake.runtimeID != uuidString(online.runtimeID) || wake.taskID == "" {
		t.Fatalf("wake = %+v, want one directed runtime wake", wake)
	}

	candidates, err = listRadarCandidates(t.Context(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if hasRadarCandidate(candidates, online.agentID) {
		t.Fatal("agent with an active radar run was selected")
	}

	_, _, err = taskSvc.EnqueueAgentRadarRun(t.Context(), service.EnqueueAgentRadarRunParams{
		WorkspaceID:    online.workspaceID,
		AgentID:        online.agentID,
		TriggerKind:    "scheduled",
		TriggerRef:     "duplicate",
		CooldownKey:    agentRadarCooldownKey,
		ContextSummary: "duplicate radar",
		ScheduledFor:   time.Now(),
		Prompt:         "duplicate",
	})
	if !errors.Is(err, service.ErrAgentRadarRunActive) {
		t.Fatalf("duplicate enqueue error = %v, want ErrAgentRadarRunActive", err)
	}
	if wake.calls != 1 {
		t.Fatalf("duplicate enqueue emitted wake: calls=%d", wake.calls)
	}
}

func TestEnqueueAgentRadarRunRejectsOfflineRuntimeWithoutRows(t *testing.T) {
	offline := seedRadarTestAgent(t, "offline")
	pool := integrationPool(t)
	taskSvc := service.NewTaskService(db.New(pool), pool, nil, events.New())

	_, _, err := taskSvc.EnqueueAgentRadarRun(t.Context(), service.EnqueueAgentRadarRunParams{
		WorkspaceID:    offline.workspaceID,
		AgentID:        offline.agentID,
		TriggerKind:    "scheduled",
		TriggerRef:     "offline",
		CooldownKey:    agentRadarCooldownKey,
		ContextSummary: "offline radar",
		ScheduledFor:   time.Now(),
		Prompt:         "should not run",
	})
	if !errors.Is(err, service.ErrAgentRadarNotReady) {
		t.Fatalf("offline enqueue error = %v, want ErrAgentRadarNotReady", err)
	}

	var runs, tasks int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM agent_radar_run WHERE agent_id = $1`, offline.agentID).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `
		SELECT count(*) FROM agent_task_queue
		WHERE agent_id = $1 AND context->>'type' = 'agent_radar'
	`, offline.agentID).Scan(&tasks); err != nil {
		t.Fatal(err)
	}
	if runs != 0 || tasks != 0 {
		t.Fatalf("offline enqueue persisted rows: runs=%d tasks=%d", runs, tasks)
	}
}

func TestCountRecentAgentRadarRunsExcludesActiveGuardRepairArtifacts(t *testing.T) {
	agent := seedRadarTestAgent(t, "online")
	pool := integrationPool(t)
	q := db.New(pool)
	now := time.Now().UTC()

	repairTask, err := q.CreateQuickCreateTask(t.Context(), db.CreateQuickCreateTaskParams{
		AgentID:   agent.agentID,
		RuntimeID: agent.runtimeID,
		Priority:  1,
		Context:   []byte(`{"type":"agent_radar"}`),
	})
	if err != nil {
		t.Fatalf("create repair task: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		UPDATE agent_task_queue
		SET status = 'cancelled',
		    completed_at = $2,
		    error = 'Radar run invalidated during active-run repair',
		    failure_reason = 'radar_active_run_repair'
		WHERE id = $1
	`, repairTask.ID, now.Add(-2*time.Minute)); err != nil {
		t.Fatalf("mark repair task cancelled: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO agent_radar_run (
			workspace_id, agent_id, runtime_id, task_id, trigger_kind, trigger_ref,
			status, cooldown_key, context_summary, error, finished_at, created_at
		) VALUES (
			$1, $2, $3, $4, 'scheduled', 'active-guard-repair',
			'failed', 'periodic_project_radar', 'repair artifact',
			'migration: duplicate active Radar run', $5, $6
		)
	`, agent.workspaceID, agent.agentID, agent.runtimeID, repairTask.ID, now.Add(-2*time.Minute), now.Add(-3*time.Minute)); err != nil {
		t.Fatalf("create linked repair run: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO agent_radar_run (
			workspace_id, agent_id, runtime_id, trigger_kind, trigger_ref,
			status, cooldown_key, context_summary, error, finished_at, created_at
		) VALUES (
			$1, $2, $3, 'scheduled', 'orphan-active-guard-repair',
			'failed', 'periodic_project_radar', 'orphan repair artifact',
			'migration: active Radar run had no linked task', $4, $5
		)
	`, agent.workspaceID, agent.agentID, agent.runtimeID, now.Add(-90*time.Second), now.Add(-2*time.Minute)); err != nil {
		t.Fatalf("create orphan repair run: %v", err)
	}
	staleDispatchRepairTask, err := q.CreateQuickCreateTask(t.Context(), db.CreateQuickCreateTaskParams{
		AgentID:   agent.agentID,
		RuntimeID: agent.runtimeID,
		Priority:  1,
		Context:   []byte(`{"type":"agent_radar"}`),
	})
	if err != nil {
		t.Fatalf("create stale-dispatch repair task: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		UPDATE agent_task_queue
		SET status = 'failed',
		    completed_at = $2,
		    error = 'Radar task remained dispatched without starting',
		    failure_reason = 'radar_stale_dispatch_repair'
		WHERE id = $1
	`, staleDispatchRepairTask.ID, now.Add(-time.Minute)); err != nil {
		t.Fatalf("mark stale-dispatch repair task failed: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO agent_radar_run (
			workspace_id, agent_id, runtime_id, task_id, trigger_kind, trigger_ref,
			status, cooldown_key, context_summary, error, finished_at, created_at
		) VALUES (
			$1, $2, $3, $4, 'scheduled', 'stale-dispatch-repair',
			'failed', 'periodic_project_radar', 'stale dispatch repair artifact',
			'radar_stale_dispatch_repair', $5, $6
		)
	`, agent.workspaceID, agent.agentID, agent.runtimeID, staleDispatchRepairTask.ID,
		now.Add(-time.Minute), now.Add(-4*time.Minute)); err != nil {
		t.Fatalf("create stale-dispatch repair run: %v", err)
	}

	failedTask, err := q.CreateQuickCreateTask(t.Context(), db.CreateQuickCreateTaskParams{
		AgentID:   agent.agentID,
		RuntimeID: agent.runtimeID,
		Priority:  1,
		Context:   []byte(`{"type":"agent_radar"}`),
	})
	if err != nil {
		t.Fatalf("create genuine failed task: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		UPDATE agent_task_queue
		SET status = 'failed',
		    started_at = $2,
		    completed_at = $3,
		    error = 'agent execution failed',
		    failure_reason = 'agent_error'
		WHERE id = $1
	`, failedTask.ID, now.Add(-time.Minute), now.Add(-30*time.Second)); err != nil {
		t.Fatalf("mark genuine task failed: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO agent_radar_run (
			workspace_id, agent_id, runtime_id, task_id, trigger_kind, trigger_ref,
			status, cooldown_key, context_summary, error,
			started_at, finished_at, created_at
		) VALUES (
			$1, $2, $3, $4, 'scheduled', 'genuine-failure',
			'failed', 'periodic_project_radar', 'real attempted run',
			'agent execution failed', $5, $6, $7
		)
	`, agent.workspaceID, agent.agentID, agent.runtimeID, failedTask.ID,
		now.Add(-time.Minute), now.Add(-30*time.Second), now.Add(-2*time.Minute)); err != nil {
		t.Fatalf("create genuine failed run: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO agent_radar_run (
			workspace_id, agent_id, runtime_id, trigger_kind, trigger_ref,
			status, cooldown_key, context_summary,
			started_at, finished_at, created_at
		) VALUES (
			$1, $2, $3, 'scheduled', 'genuine-failure-without-error-text',
			'failed', 'periodic_project_radar', 'real attempted run without error text',
			$4, $5, $6
		)
	`, agent.workspaceID, agent.agentID, agent.runtimeID,
		now.Add(-90*time.Second), now.Add(-45*time.Second), now.Add(-2*time.Minute)); err != nil {
		t.Fatalf("create genuine failed run without error text: %v", err)
	}

	recent, err := q.CountRecentAgentRadarRuns(t.Context(), db.CountRecentAgentRadarRunsParams{
		WorkspaceID: agent.workspaceID,
		AgentID:     agent.agentID,
		CreatedAt:   pgtype.Timestamptz{Time: now.Add(-time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("count recent Radar runs: %v", err)
	}
	if recent != 2 {
		t.Fatalf("recent budget runs = %d, want 2 genuine attempted failures", recent)
	}
}

func TestRepairStaleDispatchedRadarTasksPreservesFreshAndRunningTasks(t *testing.T) {
	staleAgent := seedRadarTestAgent(t, "online")
	freshAgent := seedRadarTestAgent(t, "online")
	runningAgent := seedRadarTestAgent(t, "online")
	pool := integrationPool(t)
	q := db.New(pool)
	taskSvc := service.NewTaskService(q, pool, nil, events.New())
	now := time.Now().UTC()

	enqueue := func(agent radarTestAgent, ref string) (db.AgentRadarRun, db.AgentTaskQueue) {
		t.Helper()
		run, task, err := taskSvc.EnqueueAgentRadarRun(t.Context(), service.EnqueueAgentRadarRunParams{
			WorkspaceID:    agent.workspaceID,
			AgentID:        agent.agentID,
			TriggerKind:    "scheduled",
			TriggerRef:     ref,
			CooldownKey:    agentRadarCooldownKey,
			ContextSummary: ref,
			ScheduledFor:   now,
			Prompt:         "inspect",
		})
		if err != nil {
			t.Fatalf("enqueue %s: %v", ref, err)
		}
		return run, task
	}

	staleRun, staleTask := enqueue(staleAgent, "stale-dispatched")
	if _, err := pool.Exec(t.Context(), `
		UPDATE agent_task_queue
		SET status = 'dispatched', created_at = $2, dispatched_at = $3
		WHERE id = $1
	`, staleTask.ID, now.Add(-2*time.Hour), now); err != nil {
		t.Fatalf("age stale task: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		UPDATE agent_radar_run SET created_at = $2 WHERE id = $1
	`, staleRun.ID, now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("age stale run: %v", err)
	}

	freshRun, freshTask := enqueue(freshAgent, "fresh-dispatched")
	if _, err := pool.Exec(t.Context(), `
		UPDATE agent_task_queue
		SET status = 'dispatched', created_at = $2, dispatched_at = $3
		WHERE id = $1
	`, freshTask.ID, now.Add(-5*time.Minute), now); err != nil {
		t.Fatalf("mark fresh task dispatched: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		UPDATE agent_radar_run SET created_at = $2 WHERE id = $1
	`, freshRun.ID, now.Add(-5*time.Minute)); err != nil {
		t.Fatalf("age fresh run: %v", err)
	}

	runningRun, runningTask := enqueue(runningAgent, "old-running")
	if _, err := pool.Exec(t.Context(), `
		UPDATE agent_task_queue
		SET status = 'running', created_at = $2, dispatched_at = $2, started_at = $2
		WHERE id = $1
	`, runningTask.ID, now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("mark old task running: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		UPDATE agent_radar_run SET status = 'running', created_at = $2, started_at = $2 WHERE id = $1
	`, runningRun.ID, now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("mark old run running: %v", err)
	}

	repaired, err := repairStaleDispatchedRadarTasks(t.Context(), taskSvc)
	if err != nil {
		t.Fatalf("repair stale dispatched Radar tasks: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired = %d, want 1", repaired)
	}

	staleTask, err = q.GetAgentTask(t.Context(), staleTask.ID)
	if err != nil {
		t.Fatal(err)
	}
	staleRun, err = q.GetAgentRadarRun(t.Context(), staleRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	if staleTask.Status != "failed" || !staleTask.FailureReason.Valid || staleTask.FailureReason.String != "radar_stale_dispatch_repair" {
		t.Fatalf("stale task = status %q reason %#v, want failed repair", staleTask.Status, staleTask.FailureReason)
	}
	if staleRun.Status != "failed" || staleRun.Error != "Radar task remained dispatched without starting" {
		t.Fatalf("stale run = status %q error %q, want failed stale-dispatch repair", staleRun.Status, staleRun.Error)
	}

	freshTask, err = q.GetAgentTask(t.Context(), freshTask.ID)
	if err != nil {
		t.Fatal(err)
	}
	freshRun, err = q.GetAgentRadarRun(t.Context(), freshRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	if freshTask.Status != "dispatched" || freshRun.Status != "queued" {
		t.Fatalf("fresh pair changed: task=%q run=%q", freshTask.Status, freshRun.Status)
	}

	runningTask, err = q.GetAgentTask(t.Context(), runningTask.ID)
	if err != nil {
		t.Fatal(err)
	}
	runningRun, err = q.GetAgentRadarRun(t.Context(), runningRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	if runningTask.Status != "running" || runningRun.Status != "running" {
		t.Fatalf("running pair changed: task=%q run=%q", runningTask.Status, runningRun.Status)
	}
}

func TestRepairStaleDispatchedRadarTasksSurvivesRuntimeMetadataDrift(t *testing.T) {
	mismatchAgent := seedRadarTestAgent(t, "online")
	nullRuntimeAgent := seedRadarTestAgent(t, "online")
	pool := integrationPool(t)
	q := db.New(pool)
	taskSvc := service.NewTaskService(q, pool, nil, events.New())
	now := time.Now().UTC()

	enqueueStaleDispatched := func(agent radarTestAgent, ref string) (db.AgentRadarRun, db.AgentTaskQueue) {
		t.Helper()
		run, task, err := taskSvc.EnqueueAgentRadarRun(t.Context(), service.EnqueueAgentRadarRunParams{
			WorkspaceID:    agent.workspaceID,
			AgentID:        agent.agentID,
			TriggerKind:    "scheduled",
			TriggerRef:     ref,
			CooldownKey:    agentRadarCooldownKey,
			ContextSummary: ref,
			ScheduledFor:   now,
			Prompt:         "inspect",
		})
		if err != nil {
			t.Fatalf("enqueue %s: %v", ref, err)
		}
		if _, err := pool.Exec(t.Context(), `
			UPDATE agent_task_queue
			SET status = 'dispatched', created_at = $2, dispatched_at = $3
			WHERE id = $1
		`, task.ID, now.Add(-2*time.Hour), now); err != nil {
			t.Fatalf("age %s task: %v", ref, err)
		}
		if _, err := pool.Exec(t.Context(), `
			UPDATE agent_radar_run SET created_at = $2 WHERE id = $1
		`, run.ID, now.Add(-2*time.Hour)); err != nil {
			t.Fatalf("age %s run: %v", ref, err)
		}
		return run, task
	}

	mismatchRun, mismatchTask := enqueueStaleDispatched(mismatchAgent, "runtime-mismatch")
	var replacementRuntimeID pgtype.UUID
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider,
			status, device_info, metadata, visibility, last_seen_at
		) VALUES ($1, $2, 'replacement-runtime', 'cloud', 'daytona',
		          'online', '', '{}'::jsonb, 'private', now())
		RETURNING id
	`, mismatchAgent.workspaceID, "replacement-"+uuid.NewString()).Scan(&replacementRuntimeID); err != nil {
		t.Fatalf("create replacement runtime: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		UPDATE agent_task_queue SET runtime_id = $2 WHERE id = $1
	`, mismatchTask.ID, replacementRuntimeID); err != nil {
		t.Fatalf("reassign stale task runtime: %v", err)
	}

	nullRuntimeRun, nullRuntimeTask := enqueueStaleDispatched(nullRuntimeAgent, "null-run-runtime")
	if _, err := pool.Exec(t.Context(), `
		UPDATE agent_radar_run SET runtime_id = NULL WHERE id = $1
	`, nullRuntimeRun.ID); err != nil {
		t.Fatalf("clear stale run runtime: %v", err)
	}

	repaired, err := repairStaleDispatchedRadarTasks(t.Context(), taskSvc)
	if err != nil {
		t.Fatalf("repair stale Radar tasks with runtime drift: %v", err)
	}
	if repaired != 2 {
		t.Fatalf("repaired = %d, want 2 runtime-drifted stale tasks", repaired)
	}

	for name, pair := range map[string]struct {
		run  db.AgentRadarRun
		task db.AgentTaskQueue
	}{
		"mismatched runtime": {run: mismatchRun, task: mismatchTask},
		"null run runtime":   {run: nullRuntimeRun, task: nullRuntimeTask},
	} {
		task, err := q.GetAgentTask(t.Context(), pair.task.ID)
		if err != nil {
			t.Fatalf("load %s task: %v", name, err)
		}
		run, err := q.GetAgentRadarRun(t.Context(), pair.run.ID)
		if err != nil {
			t.Fatalf("load %s run: %v", name, err)
		}
		if task.Status != "failed" || run.Status != "failed" {
			t.Fatalf("%s pair = task:%q run:%q, want failed/failed", name, task.Status, run.Status)
		}
	}
}

func TestAgentRadarRunFollowsTaskRunningFailureAndCancellation(t *testing.T) {
	agent := seedRadarTestAgent(t, "online")
	pool := integrationPool(t)
	taskSvc := service.NewTaskService(db.New(pool), pool, nil, events.New())

	enqueue := func(ref string) (db.AgentRadarRun, db.AgentTaskQueue) {
		t.Helper()
		run, task, err := taskSvc.EnqueueAgentRadarRun(t.Context(), service.EnqueueAgentRadarRunParams{
			WorkspaceID:    agent.workspaceID,
			AgentID:        agent.agentID,
			TriggerKind:    "scheduled",
			TriggerRef:     ref,
			CooldownKey:    agentRadarCooldownKey,
			ContextSummary: "state sync test",
			ScheduledFor:   time.Now(),
			Prompt:         "inspect",
		})
		if err != nil {
			t.Fatalf("enqueue %s: %v", ref, err)
		}
		return run, task
	}
	loadStatus := func(runID pgtype.UUID) string {
		t.Helper()
		var status string
		if err := pool.QueryRow(t.Context(), `SELECT status FROM agent_radar_run WHERE id = $1`, runID).Scan(&status); err != nil {
			t.Fatal(err)
		}
		return status
	}

	failedRun, failedTask := enqueue("failure")
	if _, err := pool.Exec(t.Context(), `UPDATE agent_task_queue SET status = 'dispatched' WHERE id = $1`, failedTask.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := taskSvc.StartTask(t.Context(), failedTask.ID); err != nil {
		t.Fatalf("start radar task: %v", err)
	}
	if got := loadStatus(failedRun.ID); got != "running" {
		t.Fatalf("run after task start = %q, want running", got)
	}
	if _, err := taskSvc.FailTask(t.Context(), failedTask.ID, "radar failed", "", "", "agent_error"); err != nil {
		t.Fatalf("fail radar task: %v", err)
	}
	if got := loadStatus(failedRun.ID); got != "failed" {
		t.Fatalf("run after task failure = %q, want failed", got)
	}

	cancelledRun, cancelledTask := enqueue("cancel")
	if _, err := taskSvc.CancelTask(t.Context(), cancelledTask.ID); err != nil {
		t.Fatalf("cancel radar task: %v", err)
	}
	if got := loadStatus(cancelledRun.ID); got != "cancelled" {
		t.Fatalf("run after task cancellation = %q, want cancelled", got)
	}

	sweptRun, sweptTask := enqueue("sweeper")
	if _, err := pool.Exec(t.Context(), `
		UPDATE agent_task_queue
		SET status = 'failed', completed_at = now(), error = 'queued expired', failure_reason = 'queued_expired'
		WHERE id = $1
	`, sweptTask.ID); err != nil {
		t.Fatal(err)
	}
	sweptTask, err := db.New(pool).GetAgentTask(t.Context(), sweptTask.ID)
	if err != nil {
		t.Fatal(err)
	}
	taskSvc.HandleFailedTasks(t.Context(), []db.AgentTaskQueue{sweptTask})
	if got := loadStatus(sweptRun.ID); got != "failed" {
		t.Fatalf("run after sweeper failure = %q, want failed", got)
	}

	interruptedRun, interruptedTask := enqueue("interrupted-completion")
	if _, err := pool.Exec(t.Context(), `
		UPDATE agent_task_queue SET status = 'completed', completed_at = now() WHERE id = $1
	`, interruptedTask.ID); err != nil {
		t.Fatal(err)
	}
	if repaired, err := reconcileTerminalRadarRuns(t.Context(), pool); err != nil {
		t.Fatalf("reconcile interrupted completion: %v", err)
	} else if repaired != 1 {
		t.Fatalf("repaired runs = %d, want 1", repaired)
	}
	if got := loadStatus(interruptedRun.ID); got != "failed" {
		t.Fatalf("run after interrupted completion repair = %q, want failed", got)
	}
}

func TestAgentTaskMetricsExcludeRadarHousekeepingRuns(t *testing.T) {
	agent := seedRadarTestAgent(t, "online")
	pool := integrationPool(t)
	q := db.New(pool)

	normalTask, err := q.CreateQuickCreateTask(t.Context(), db.CreateQuickCreateTaskParams{
		AgentID:   agent.agentID,
		RuntimeID: agent.runtimeID,
		Priority:  1,
		Context:   []byte(`{"type":"quick_create"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `
		UPDATE agent_task_queue SET status = 'completed', completed_at = now() WHERE id = $1
	`, normalTask.ID); err != nil {
		t.Fatal(err)
	}

	taskSvc := service.NewTaskService(q, pool, nil, events.New())
	_, radarTask, err := taskSvc.EnqueueAgentRadarRun(t.Context(), service.EnqueueAgentRadarRunParams{
		WorkspaceID:    agent.workspaceID,
		AgentID:        agent.agentID,
		TriggerKind:    "scheduled",
		TriggerRef:     "metrics",
		CooldownKey:    agentRadarCooldownKey,
		ContextSummary: "metrics test",
		ScheduledFor:   time.Now(),
		Prompt:         "inspect",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `
		UPDATE agent_task_queue SET status = 'completed', completed_at = now() WHERE id = $1
	`, radarTask.ID); err != nil {
		t.Fatal(err)
	}

	runCounts, err := q.GetWorkspaceAgentRunCounts(t.Context(), agent.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runCounts) != 1 || uuidString(runCounts[0].AgentID) != uuidString(agent.agentID) || runCounts[0].RunCount != 1 {
		t.Fatalf("run counts = %#v, want one non-Radar run", runCounts)
	}

	activity, err := q.GetWorkspaceAgentActivity30d(t.Context(), agent.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(activity) != 1 || activity[0].TaskCount != 1 || activity[0].FailedCount != 0 {
		t.Fatalf("activity = %#v, want one successful non-Radar task", activity)
	}
}
