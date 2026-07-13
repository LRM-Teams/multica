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
