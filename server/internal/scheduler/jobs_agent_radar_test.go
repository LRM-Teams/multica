package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/radar"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestAgentRadarScheduleJobSpec(t *testing.T) {
	job := AgentRadarScheduleJob(nil, nil)
	if job.Name != JobNameAgentRadarSchedule {
		t.Fatalf("Name = %q, want %q", job.Name, JobNameAgentRadarSchedule)
	}
	if job.Cadence != time.Minute {
		t.Fatalf("Cadence = %s, want 1m", job.Cadence)
	}
	if agentRadarHourlyBudget != 1 {
		t.Fatalf("hourly budget = %d, want 1", agentRadarHourlyBudget)
	}
	if agentRadarDailyBudget != 24 {
		t.Fatalf("daily budget = %d, want 24", agentRadarDailyBudget)
	}
	if workspaceRadarCheckInterval != 30*time.Minute {
		t.Fatalf("workspace check interval = %s, want 30m", workspaceRadarCheckInterval)
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

func TestAgentRadarScheduleCreatesAtMostTwoWorkspacesPerTick(t *testing.T) {
	agents := []radarTestAgent{
		seedRadarTestAgent(t, "online"),
		seedRadarTestAgent(t, "online"),
		seedRadarTestAgent(t, "online"),
	}
	pool := integrationPool(t)
	for _, agent := range agents {
		bindRadarSupervisor(t, agent)
		if _, err := pool.Exec(t.Context(), `
			UPDATE workspace_radar_state
			SET next_due_at = now() - interval '7 days'
			WHERE workspace_id = $1
		`, agent.workspaceID); err != nil {
			t.Fatalf("make workspace Radar due: %v", err)
		}
	}
	type deferredState struct {
		workspaceID pgtype.UUID
		nextDueAt   time.Time
	}
	var deferred []deferredState
	rows, err := pool.Query(t.Context(), `
		SELECT workspace_id, next_due_at
		FROM workspace_radar_state
		WHERE workspace_id NOT IN ($1, $2, $3)
	`, agents[0].workspaceID, agents[1].workspaceID, agents[2].workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var state deferredState
		if err := rows.Scan(&state.workspaceID, &state.nextDueAt); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		deferred = append(deferred, state)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	if _, err := pool.Exec(t.Context(), `
		UPDATE workspace_radar_state
		SET next_due_at = now() + interval '7 days'
		WHERE workspace_id NOT IN ($1, $2, $3)
	`, agents[0].workspaceID, agents[1].workspaceID, agents[2].workspaceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, state := range deferred {
			_, _ = pool.Exec(context.Background(), `
				UPDATE workspace_radar_state SET next_due_at = $2 WHERE workspace_id = $1
			`, state.workspaceID, state.nextDueAt)
		}
	})

	wake := &radarWakeRecorder{}
	taskSvc := service.NewTaskService(db.New(pool), pool, nil, events.New(), wake)
	result, err := makeAgentRadarScheduleHandler(pool, taskSvc)(t.Context(), HandlerInput{PlanTime: time.Now().UTC()})
	if err != nil {
		t.Fatalf("run workspace Radar scheduler: %v", err)
	}
	if result.RowsAffected != 0 || wake.calls != 0 || result.Result["reason"] != "workgraph_handoffs" {
		t.Fatalf("scheduled Radar result = rows:%d wakes:%d reason:%#v, want 0/0/workgraph_handoffs", result.RowsAffected, wake.calls, result.Result["reason"])
	}

	var runCount, fullReviewCount int
	if err := pool.QueryRow(t.Context(), `
		SELECT
		  count(*),
		  count(*) FILTER (WHERE trigger_ref LIKE 'full_review:%')
		FROM agent_radar_run
		WHERE workspace_id IN ($1, $2, $3)
		  AND cooldown_key = $4
	`, agents[0].workspaceID, agents[1].workspaceID, agents[2].workspaceID, agentRadarCooldownKey).Scan(&runCount, &fullReviewCount); err != nil {
		t.Fatal(err)
	}
	if runCount != 0 || fullReviewCount != 0 {
		t.Fatalf("workspace runs/full-review refs = %d/%d, want 0/0 when workgraph owns supervision", runCount, fullReviewCount)
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
	channelID   pgtype.UUID
	ownerID     pgtype.UUID
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
	if _, err := pool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'owner')
	`, workspace.ID, userID); err != nil {
		t.Fatalf("create workspace owner membership: %v", err)
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

	return radarTestAgent{
		workspaceID: workspace.ID,
		agentID:     agent.ID,
		runtimeID:   runtimeID,
		channelID:   channelID,
		ownerID:     userID,
	}
}

func seedAdditionalRadarTestAgent(t *testing.T, base radarTestAgent, displayName string, addToChannel bool) pgtype.UUID {
	t.Helper()
	pool := integrationPool(t)
	q := db.New(pool)
	baseAgent, err := q.GetAgent(t.Context(), base.agentID)
	if err != nil {
		t.Fatalf("load base agent: %v", err)
	}
	agent, err := q.CreateAgent(t.Context(), db.CreateAgentParams{
		WorkspaceID:        base.workspaceID,
		Name:               "radar-peer-" + uuid.NewString(),
		DisplayName:        displayName,
		Description:        "candidate isolation test",
		RuntimeMode:        "cloud",
		RuntimeConfig:      []byte("{}"),
		RuntimeID:          base.runtimeID,
		Visibility:         "workspace",
		MaxConcurrentTasks: 1,
		OwnerID:            baseAgent.OwnerID,
		Instructions:       "",
		CustomEnv:          []byte("{}"),
		CustomArgs:         []byte("[]"),
	})
	if err != nil {
		t.Fatalf("create %s agent: %v", displayName, err)
	}
	if addToChannel {
		if _, err := pool.Exec(t.Context(), `
			INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
			VALUES ($1, $2, 'agent', $3)
		`, base.channelID, base.workspaceID, agent.ID); err != nil {
			t.Fatalf("add %s agent to channel: %v", displayName, err)
		}
	}
	return agent.ID
}

func seedAdditionalRadarOwnerWendy(t *testing.T, base radarTestAgent, displayName, runtimeStatus string) radarTestAgent {
	t.Helper()
	pool := integrationPool(t)
	q := db.New(pool)
	suffix := uuid.NewString()
	var ownerID pgtype.UUID
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id
	`, "radar-owner-"+suffix, "radar-owner-"+suffix+"@example.test").Scan(&ownerID); err != nil {
		t.Fatalf("create additional Radar owner: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')
	`, base.workspaceID, ownerID); err != nil {
		t.Fatalf("create additional Radar owner membership: %v", err)
	}
	var runtimeID pgtype.UUID
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO agent_runtime (
		  workspace_id, daemon_id, name, runtime_mode, provider, status,
		  device_info, metadata, visibility, last_seen_at
		) VALUES ($1, $2, $3, 'cloud', 'daytona', $4,
		  '', '{}'::jsonb, 'private', now())
		RETURNING id
	`, base.workspaceID, "radar-owner-daemon-"+suffix, "radar-owner-runtime-"+suffix, runtimeStatus).Scan(&runtimeID); err != nil {
		t.Fatalf("create additional Radar owner runtime: %v", err)
	}
	agent, err := q.CreateAgent(t.Context(), db.CreateAgentParams{
		WorkspaceID: base.workspaceID, Name: "radar-owner-wendy-" + suffix, DisplayName: displayName,
		Description: "replacement workspace supervisor", RuntimeMode: "cloud", RuntimeConfig: []byte("{}"),
		RuntimeID: runtimeID, Visibility: "private", MaxConcurrentTasks: 1, OwnerID: ownerID,
		Instructions: "", CustomEnv: []byte("{}"), CustomArgs: []byte("[]"),
	})
	if err != nil {
		t.Fatalf("create additional owner %s: %v", displayName, err)
	}
	t.Cleanup(func() {
		// The base fixture cleanup tolerates an already-deleted workspace. Removing
		// it here releases this additional user's membership before deleting them.
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, base.workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, ownerID)
	})
	return radarTestAgent{
		workspaceID: base.workspaceID,
		agentID:     agent.ID,
		runtimeID:   runtimeID,
		ownerID:     ownerID,
	}
}

func bindRadarSupervisor(t *testing.T, agent radarTestAgent) {
	t.Helper()
	pool := integrationPool(t)
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO workspace_radar_state (workspace_id, supervisor_agent_id)
		VALUES ($1, $2)
	`, agent.workspaceID, agent.agentID); err != nil {
		t.Fatalf("bind workspace Radar supervisor: %v", err)
	}
}

func TestRepairWorkspaceRadarSupervisorBindingsIsDeterministicAndConcurrentSafe(t *testing.T) {
	base := seedRadarTestAgent(t, "online")
	pool := integrationPool(t)
	if _, err := pool.Exec(t.Context(), `UPDATE agent SET display_name = 'Joe' WHERE id = $1`, base.agentID); err != nil {
		t.Fatalf("make base agent an eligible legacy Wendy: %v", err)
	}
	olderCanonical := seedAdditionalRadarOwnerWendy(t, base, "Wendy", "online")
	newerCanonical := seedAdditionalRadarOwnerWendy(t, base, "Wendy", "online")
	offlineCanonical := seedAdditionalRadarOwnerWendy(t, base, "Wendy", "offline")
	if _, err := pool.Exec(t.Context(), `
		UPDATE agent
		SET updated_at = CASE
		  WHEN id = $1 THEN now() - interval '2 hours'
		  WHEN id = $2 THEN now() - interval '1 hour'
		  ELSE now()
		END
		WHERE id IN ($1, $2, $3)
	`, olderCanonical.agentID, newerCanonical.agentID, offlineCanonical.agentID); err != nil {
		t.Fatalf("rank canonical Wendy candidates: %v", err)
	}

	// repairWorkspaceRadarSupervisorBindings scans the whole DB (LIMIT 200).
	// In shared CI databases leftover repairable workspaces from other tests can
	// be claimed via SKIP LOCKED by the losing goroutine, inflating the sum to 2.
	// Fence foreign eligible supervisors so this assertion stays workspace-local.
	if _, err := pool.Exec(t.Context(), `
		UPDATE agent
		SET display_name = COALESCE(NULLIF(display_name, ''), name) || ' (fenced)'
		WHERE workspace_id IS DISTINCT FROM $1
		  AND archived_at IS NULL
		  AND COALESCE(NULLIF(display_name, ''), name) IN ('Wendy', 'Windy', 'Joe')
	`, base.workspaceID); err != nil {
		t.Fatalf("fence foreign Radar supervisor candidates: %v", err)
	}

	start := make(chan struct{})
	results := make(chan int64, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			repaired, err := repairWorkspaceRadarSupervisorBindings(t.Context(), pool)
			results <- repaired
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	var repairedTotal int64
	for err := range errs {
		if err != nil {
			t.Fatalf("repair workspace Radar binding: %v", err)
		}
	}
	for repaired := range results {
		repairedTotal += repaired
	}
	if repairedTotal != 1 {
		t.Fatalf("concurrent repaired bindings = %d, want exactly 1", repairedTotal)
	}
	var boundAgentID pgtype.UUID
	if err := pool.QueryRow(t.Context(), `
		SELECT supervisor_agent_id FROM workspace_radar_state WHERE workspace_id = $1
	`, base.workspaceID).Scan(&boundAgentID); err != nil {
		t.Fatalf("load repaired workspace supervisor: %v", err)
	}
	if uuidString(boundAgentID) != uuidString(newerCanonical.agentID) {
		t.Fatalf("repaired supervisor = %s, want deterministic newer online canonical Wendy %s", uuidString(boundAgentID), uuidString(newerCanonical.agentID))
	}
	if repaired, err := repairWorkspaceRadarSupervisorBindings(t.Context(), pool); err != nil || repaired != 0 {
		t.Fatalf("repeat binding repair = %d, %v; want valid binding preserved", repaired, err)
	}
}

func TestAgentRadarScheduleReplacesInvalidSupervisorAfterCancellingOldWork(t *testing.T) {
	oldSupervisor := seedRadarTestAgent(t, "online")
	pool := integrationPool(t)
	q := db.New(pool)
	if _, err := pool.Exec(t.Context(), `UPDATE agent SET display_name = 'Wendy' WHERE id = $1`, oldSupervisor.agentID); err != nil {
		t.Fatalf("name old supervisor Wendy: %v", err)
	}
	bindRadarSupervisor(t, oldSupervisor)
	replacement := seedAdditionalRadarOwnerWendy(t, oldSupervisor, "Wendy", "online")
	taskSvc := service.NewTaskService(q, pool, nil, events.New())
	run, task, err := taskSvc.EnqueueAgentRadarRun(t.Context(), service.EnqueueAgentRadarRunParams{
		WorkspaceID: oldSupervisor.workspaceID, AgentID: oldSupervisor.agentID,
		TriggerKind: "scheduled", TriggerRef: "invalid-supervisor-repair",
		CooldownKey: agentRadarCooldownKey, ContextSummary: "old supervisor work",
		ScheduledFor: time.Now(), Prompt: "inspect workspace",
	})
	if err != nil {
		t.Fatalf("enqueue old supervisor Radar: %v", err)
	}
	watermark := time.Now().Add(-3 * time.Hour).UTC()
	if _, err := pool.Exec(t.Context(), `
		UPDATE workspace_radar_state
		SET last_success_at = $2, last_full_review_at = $2,
		    last_applied_scheduled_for = $2,
		    change_version = 52, change_cursor_version = 45,
		    static_scan_cursors = '{"issues":5,"tasks":2}'::jsonb,
		    static_cycle_seen = '{"issues":true,"tasks":true}'::jsonb,
		    consecutive_failures = 4
		WHERE workspace_id = $1
	`, oldSupervisor.workspaceID, watermark); err != nil {
		t.Fatalf("seed old supervisor workspace progress: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		UPDATE member SET role = 'member' WHERE workspace_id = $1 AND user_id = $2
	`, oldSupervisor.workspaceID, oldSupervisor.ownerID); err != nil {
		t.Fatalf("downgrade old supervisor owner: %v", err)
	}

	result, err := makeAgentRadarScheduleHandler(pool, taskSvc)(t.Context(), HandlerInput{PlanTime: time.Now().UTC()})
	if err != nil {
		t.Fatalf("run supervisor repair tick: %v", err)
	}
	gotRepaired, ok := result.Result["repaired_bindings"].(int64)
	if !ok || gotRepaired < 1 {
		t.Fatalf("repaired_bindings = %#v, want at least 1", result.Result["repaired_bindings"])
	}
	var boundAgentID pgtype.UUID
	var storedSuccess, storedFullReview, storedApplied time.Time
	var storedFailures, storedChangeVersion, storedChangeCursorVersion int
	var storedStaticScanCursors, storedStaticCycleSeen bool
	if err := pool.QueryRow(t.Context(), `
		SELECT supervisor_agent_id, last_success_at, last_full_review_at,
		       last_applied_scheduled_for, consecutive_failures,
		       change_version, change_cursor_version,
		       static_scan_cursors = '{"issues":5,"tasks":2}'::jsonb,
		       static_cycle_seen = '{"issues":true,"tasks":true}'::jsonb
		FROM workspace_radar_state WHERE workspace_id = $1
	`, oldSupervisor.workspaceID).Scan(
		&boundAgentID, &storedSuccess, &storedFullReview, &storedApplied,
		&storedFailures, &storedChangeVersion, &storedChangeCursorVersion,
		&storedStaticScanCursors, &storedStaticCycleSeen,
	); err != nil {
		t.Fatalf("load replacement supervisor: %v", err)
	}
	if uuidString(boundAgentID) != uuidString(replacement.agentID) {
		t.Fatalf("replacement supervisor = %s, want %s", uuidString(boundAgentID), uuidString(replacement.agentID))
	}
	for name, got := range map[string]time.Time{
		"last_success_at":            storedSuccess,
		"last_full_review_at":        storedFullReview,
		"last_applied_scheduled_for": storedApplied,
	} {
		if got.Sub(watermark) > time.Second || watermark.Sub(got) > time.Second {
			t.Fatalf("%s = %s, want preserved %s", name, got, watermark)
		}
	}
	if storedFailures != 0 {
		t.Fatalf("consecutive_failures = %d, want reset 0 for immediate replacement recovery", storedFailures)
	}
	if storedChangeVersion != 52 || storedChangeCursorVersion != 45 ||
		!storedStaticScanCursors || !storedStaticCycleSeen {
		t.Fatalf("replacement scan state = version:%d cursor:%d static:%t cycle:%t; want preserved",
			storedChangeVersion, storedChangeCursorVersion, storedStaticScanCursors, storedStaticCycleSeen)
	}
	storedRun, err := q.GetAgentRadarRun(t.Context(), run.ID)
	if err != nil || storedRun.Status != "cancelled" {
		t.Fatalf("old supervisor run = %+v, %v; want cancelled", storedRun, err)
	}
	storedTask, err := q.GetAgentTask(t.Context(), task.ID)
	if err != nil || storedTask.Status != "cancelled" {
		t.Fatalf("old supervisor task = %+v, %v; want cancelled", storedTask, err)
	}
	command, err := pool.Exec(t.Context(), `
		UPDATE agent_inbox_event SET status = 'dispatched', dispatched_at = now()
		WHERE id = $1 AND status = 'queued'
	`, task.ID)
	if err != nil {
		t.Fatalf("probe old supervisor redispatch: %v", err)
	}
	if command.RowsAffected() != 0 {
		t.Fatal("old supervisor task regained dispatch authority after replacement")
	}
}

func TestDBRejectsAutomaticRadarOutsideWorkspaceWendy(t *testing.T) {
	agent := seedRadarTestAgent(t, "online")
	pool := integrationPool(t)

	_, err := pool.Exec(t.Context(), `
		INSERT INTO agent_radar_run (
		  workspace_id, agent_id, runtime_id, trigger_kind, trigger_ref,
		  status, cooldown_key, context_summary, scheduled_for
		) VALUES ($1, $2, $3, 'scheduled', 'old-pod',
		  'planned', 'periodic_project_radar', 'legacy scheduler', now())
	`, agent.workspaceID, agent.agentID, agent.runtimeID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "agent_radar_run_active_scheduled_workspace_check" {
		t.Fatalf("active legacy scheduled insert error = %v, want check violation from workspace gate", err)
	}

	_, err = pool.Exec(t.Context(), `
		INSERT INTO agent_radar_run (
		  workspace_id, agent_id, runtime_id, trigger_kind, trigger_ref,
		  status, cooldown_key, context_summary, scheduled_for
		) VALUES ($1, $2, $3, 'event', 'old-event-pod',
		  'planned', 'event-radar', 'legacy event scheduler', now())
	`, agent.workspaceID, agent.agentID, agent.runtimeID)
	pgErr = nil
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "agent_radar_run_active_scheduled_workspace_check" {
		t.Fatalf("active event insert error = %v, want check violation from workspace gate", err)
	}

	var manualRunID pgtype.UUID
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO agent_radar_run (
		  workspace_id, agent_id, runtime_id, trigger_kind, trigger_ref,
		  status, cooldown_key, context_summary, scheduled_for
		) VALUES ($1, $2, $3, 'manual', 'explicit-user-run',
		  'planned', 'manual-radar', 'explicit user request', now())
		RETURNING id
	`, agent.workspaceID, agent.agentID, agent.runtimeID).Scan(&manualRunID); err != nil {
		t.Fatalf("active manual run should remain allowed: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		UPDATE agent_radar_run
		SET status = 'cancelled', finished_at = now(), updated_at = now()
		WHERE id = $1
	`, manualRunID); err != nil {
		t.Fatalf("finish manual control run: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO agent_radar_run (
		  workspace_id, agent_id, runtime_id, trigger_kind, trigger_ref,
		  status, cooldown_key, context_summary, scheduled_for, finished_at
		) VALUES ($1, $2, $3, 'event', 'historical-event',
		  'succeeded', 'event-radar', 'historical event run', now(), now())
	`, agent.workspaceID, agent.agentID, agent.runtimeID); err != nil {
		t.Fatalf("terminal event history should remain insertable: %v", err)
	}

	var legacyHistoryID pgtype.UUID
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO agent_radar_run (
		  workspace_id, agent_id, runtime_id, trigger_kind, trigger_ref,
		  status, cooldown_key, context_summary, scheduled_for, finished_at
		) VALUES ($1, $2, $3, 'scheduled', 'legacy-history',
		  'succeeded', 'periodic_project_radar', 'historical legacy run', now(), now())
		RETURNING id
	`, agent.workspaceID, agent.agentID, agent.runtimeID).Scan(&legacyHistoryID); err != nil {
		t.Fatalf("terminal legacy history should remain insertable: %v", err)
	}
	result, err := pool.Exec(t.Context(), `
		INSERT INTO agent_radar_action (
		  radar_run_id, workspace_id, agent_id, action_type, status,
		  risk_level, confidence, dedupe_key, target_kind, reason, evidence, payload
		) VALUES ($1, $2, $3, 'post_channel_message', 'approved',
		  'low', 'high', $4, 'channel', 'old pod replay', '[]'::jsonb, '{}'::jsonb)
	`, legacyHistoryID, agent.workspaceID, agent.agentID, "old-pod-action-"+uuid.NewString())
	if err != nil {
		t.Fatalf("legacy action insert: %v", err)
	}
	if result.RowsAffected() != 0 {
		t.Fatalf("legacy scheduled action rows affected = %d, want 0 from DB action gate", result.RowsAffected())
	}
}

func TestWorkspaceRadarActionDBGateRequiresClaimedVisibleAction(t *testing.T) {
	agent := seedRadarTestAgent(t, "online")
	bindRadarSupervisor(t, agent)
	pool := integrationPool(t)
	otherAgentID := seedAdditionalRadarTestAgent(t, agent, "Other agent", false)

	var runID pgtype.UUID
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO agent_radar_run (
		  workspace_id, agent_id, runtime_id, trigger_kind, trigger_ref,
		  status, cooldown_key, context_summary, scheduled_for
		) VALUES ($1, $2, $3, 'scheduled', 'action-gate',
		  'running', 'workspace_supervisor_radar', 'action gate test', now())
		RETURNING id
	`, agent.workspaceID, agent.agentID, agent.runtimeID).Scan(&runID); err != nil {
		t.Fatalf("create running workspace Radar: %v", err)
	}
	for _, terminalStatus := range []string{"succeeded", "no_action"} {
		var updatedID pgtype.UUID
		err := pool.QueryRow(t.Context(), `
			UPDATE agent_radar_run
			SET status = $2, finished_at = now(), updated_at = now()
			WHERE id = $1
			RETURNING id
		`, runID, terminalStatus).Scan(&updatedID)
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("old-pod running -> %s error = %v, want pgx.ErrNoRows", terminalStatus, err)
		}
	}
	var stillRunning string
	if err := pool.QueryRow(t.Context(), `SELECT status FROM agent_radar_run WHERE id = $1`, runID).Scan(&stillRunning); err != nil {
		t.Fatal(err)
	}
	if stillRunning != "running" {
		t.Fatalf("run status after suppressed old-pod success = %q, want running", stillRunning)
	}

	insert := func(actionType, status string, workspaceID, actionAgentID pgtype.UUID) int64 {
		t.Helper()
		result, err := pool.Exec(t.Context(), `
			INSERT INTO agent_radar_action (
			  radar_run_id, workspace_id, agent_id, action_type, status,
			  risk_level, confidence, dedupe_key, target_kind, reason, evidence, payload
			) VALUES ($1, $2, $3, $4, $5,
			  'low', 'high', $6, 'none', 'DB gate test', '[]'::jsonb, '{}'::jsonb)
		`, runID, workspaceID, actionAgentID, actionType, status, "action-gate-"+uuid.NewString())
		if err != nil {
			t.Fatalf("insert %s/%s action: %v", actionType, status, err)
		}
		return result.RowsAffected()
	}

	if got := insert("no_action", "approved", agent.workspaceID, agent.agentID); got != 0 {
		t.Fatalf("running run action rows = %d, want 0", got)
	}
	if _, err := pool.Exec(t.Context(), `
		UPDATE agent_radar_run SET status = 'executing', updated_at = now() WHERE id = $1
	`, runID); err != nil {
		t.Fatalf("claim workspace Radar run: %v", err)
	}
	if got := insert("create_issue", "executing", agent.workspaceID, agent.agentID); got != 0 {
		t.Fatalf("hidden scheduled action rows = %d, want 0", got)
	}
	if got := insert("mention_agent", "approved", agent.workspaceID, agent.agentID); got != 0 {
		t.Fatalf("old-pod approved mention rows = %d, want 0", got)
	}
	if got := insert("mention_agent", "executing", agent.workspaceID, otherAgentID); got != 0 {
		t.Fatalf("mismatched action agent rows = %d, want 0", got)
	}
	if got := insert("no_action", "approved", agent.workspaceID, agent.agentID); got != 1 {
		t.Fatalf("claimed no_action rows = %d, want 1", got)
	}
	if got := insert("comment_issue", "executing", agent.workspaceID, agent.agentID); got != 1 {
		t.Fatalf("claimed visible comment rows = %d, want 1", got)
	}
	if got := insert("mention_agent", "executing", agent.workspaceID, agent.agentID); got != 1 {
		t.Fatalf("claimed visible mention rows = %d, want 1", got)
	}
	if _, err := pool.Exec(t.Context(), `
		UPDATE agent_radar_run SET status = 'cancelled', finished_at = now(), updated_at = now() WHERE id = $1
	`, runID); err != nil {
		t.Fatalf("cancel workspace Radar run: %v", err)
	}
	if got := insert("no_action", "approved", agent.workspaceID, agent.agentID); got != 0 {
		t.Fatalf("cancelled run action rows = %d, want 0", got)
	}
}

func TestWorkspaceRadarActionDedupeSurvivesWendyRebind(t *testing.T) {
	agent := seedRadarTestAgent(t, "online")
	bindRadarSupervisor(t, agent)
	pool := integrationPool(t)
	q := db.New(pool)
	replacementID := seedAdditionalRadarTestAgent(t, agent, "Replacement Wendy", false)
	dedupeKey := "workspace-supervisor:comment_issue:" + uuid.NewString()

	createRun := func(agentID pgtype.UUID, ref string) pgtype.UUID {
		t.Helper()
		var runID pgtype.UUID
		if err := pool.QueryRow(t.Context(), `
			INSERT INTO agent_radar_run (
			  workspace_id, agent_id, runtime_id, trigger_kind, trigger_ref,
			  status, cooldown_key, context_summary, scheduled_for
			) VALUES ($1, $2, $3, 'scheduled', $4,
			  'executing', 'workspace_supervisor_radar', 'dedupe rebind test', now())
			RETURNING id
		`, agent.workspaceID, agentID, agent.runtimeID, ref).Scan(&runID); err != nil {
			t.Fatalf("create %s run: %v", ref, err)
		}
		return runID
	}
	createAction := func(runID, actionAgentID pgtype.UUID) (db.AgentRadarAction, error) {
		return q.CreateAgentRadarAction(t.Context(), db.CreateAgentRadarActionParams{
			RadarRunID: runID, WorkspaceID: agent.workspaceID, AgentID: actionAgentID,
			ActionType: "comment_issue", Status: "executing", RiskLevel: "low",
			Confidence: "high", DedupeKey: dedupeKey, TargetKind: "issue",
			Reason: "dedupe rebind test", Evidence: []byte("[]"), Payload: []byte("{}"),
		})
	}

	firstRunID := createRun(agent.agentID, "first-wendy")
	firstAction, err := createAction(firstRunID, agent.agentID)
	if err != nil {
		t.Fatalf("create first Wendy action: %v", err)
	}
	if _, err := q.UpdateAgentRadarActionStatus(t.Context(), db.UpdateAgentRadarActionStatusParams{
		ID: firstAction.ID, Status: "executed", Result: []byte("{}"),
	}); err != nil {
		t.Fatalf("finish first Wendy action: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		UPDATE agent_radar_run SET status = 'succeeded', finished_at = now(), updated_at = now() WHERE id = $1
	`, firstRunID); err != nil {
		t.Fatalf("finish first Wendy run: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		UPDATE workspace_radar_state SET supervisor_agent_id = $2, next_due_at = now() WHERE workspace_id = $1
	`, agent.workspaceID, replacementID); err != nil {
		t.Fatalf("rebind workspace Radar supervisor: %v", err)
	}
	secondRunID := createRun(replacementID, "replacement-wendy")
	if _, err := createAction(secondRunID, replacementID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("replacement Wendy duplicate error = %v, want pgx.ErrNoRows", err)
	}
	skipped, err := q.CreateAgentRadarAction(t.Context(), db.CreateAgentRadarActionParams{
		RadarRunID: secondRunID, WorkspaceID: agent.workspaceID, AgentID: replacementID,
		ActionType: "comment_issue", Status: "skipped", RiskLevel: "low",
		Confidence: "high", DedupeKey: dedupeKey, TargetKind: "issue",
		Reason: "duplicate dedupe key", Evidence: []byte("[]"), Payload: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("record replacement Wendy skipped receipt: %v", err)
	}
	if skipped.Status != "skipped" {
		t.Fatalf("replacement Wendy receipt status = %q, want skipped", skipped.Status)
	}
	var visibleCount, skippedCount int
	if err := pool.QueryRow(t.Context(), `
		SELECT
		  count(*) FILTER (WHERE status IN ('executing', 'executed')),
		  count(*) FILTER (WHERE status = 'skipped')
		FROM agent_radar_action
		WHERE workspace_id = $1 AND dedupe_key = $2
	`, agent.workspaceID, dedupeKey).Scan(&visibleCount, &skippedCount); err != nil {
		t.Fatalf("count deduped Wendy receipts: %v", err)
	}
	if visibleCount != 1 || skippedCount != 1 {
		t.Fatalf("deduped Wendy receipts = visible:%d skipped:%d, want 1/1", visibleCount, skippedCount)
	}
}

func TestListRadarCandidatesUsesWorkspaceWideActiveGuard(t *testing.T) {
	oldSupervisor := seedRadarTestAgent(t, "online")
	bindRadarSupervisor(t, oldSupervisor)
	newSupervisorID := seedAdditionalRadarTestAgent(t, oldSupervisor, "Replacement Wendy", false)
	pool := integrationPool(t)
	taskSvc := service.NewTaskService(db.New(pool), pool, nil, events.New())

	if _, _, err := taskSvc.EnqueueAgentRadarRun(t.Context(), service.EnqueueAgentRadarRunParams{
		WorkspaceID:    oldSupervisor.workspaceID,
		AgentID:        oldSupervisor.agentID,
		TriggerKind:    "scheduled",
		TriggerRef:     "old-supervisor-active",
		CooldownKey:    agentRadarCooldownKey,
		ContextSummary: "old supervisor active",
		ScheduledFor:   time.Now(),
		Prompt:         "inspect",
	}); err != nil {
		t.Fatalf("enqueue old supervisor Radar: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		UPDATE workspace_radar_state
		SET supervisor_agent_id = $2, next_due_at = now()
		WHERE workspace_id = $1
	`, oldSupervisor.workspaceID, newSupervisorID); err != nil {
		t.Fatalf("simulate supervisor rebind: %v", err)
	}

	candidates, err := listRadarCandidates(t.Context(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if hasRadarCandidate(candidates, newSupervisorID) {
		t.Fatal("replacement supervisor was selected while the workspace still had an active supervisor Radar")
	}
	if _, _, err := taskSvc.EnqueueAgentRadarRun(t.Context(), service.EnqueueAgentRadarRunParams{
		WorkspaceID:    oldSupervisor.workspaceID,
		AgentID:        newSupervisorID,
		TriggerKind:    "scheduled",
		TriggerRef:     "replacement-conflict",
		CooldownKey:    agentRadarCooldownKey,
		ContextSummary: "replacement conflicts with active workspace run",
		ScheduledFor:   time.Now(),
		Prompt:         "inspect",
	}); !errors.Is(err, service.ErrAgentRadarRunActive) {
		t.Fatalf("replacement enqueue error = %v, want ErrAgentRadarRunActive", err)
	}
}

func TestWorkspaceRadarDispatchGateAndUnauthorizedCleanup(t *testing.T) {
	t.Run("old pod claim is suppressed", func(t *testing.T) {
		agent, _, task := seedQueuedWorkspaceRadar(t)
		pool := integrationPool(t)
		downgradeRadarOwner(t, agent)

		var taskID pgtype.UUID
		err := pool.QueryRow(t.Context(), `
			UPDATE agent_inbox_event
			SET status = 'dispatched', dispatched_at = now()
			WHERE id = $1 AND status = 'queued'
			RETURNING id
		`, task.ID).Scan(&taskID)
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("legacy claim error = %v, want no rows from DB authorization trigger", err)
		}
	})

	t.Run("old pod reclaim is suppressed", func(t *testing.T) {
		agent, _, task := seedQueuedWorkspaceRadar(t)
		pool := integrationPool(t)
		if _, err := pool.Exec(t.Context(), `
			UPDATE agent_inbox_event
			SET status = 'dispatched', dispatched_at = now() - interval '5 minutes'
			WHERE id = $1
		`, task.ID); err != nil {
			t.Fatalf("dispatch authorized Radar task: %v", err)
		}
		downgradeRadarOwner(t, agent)

		var taskID pgtype.UUID
		err := pool.QueryRow(t.Context(), `
			UPDATE agent_inbox_event
			SET dispatched_at = now()
			WHERE id = $1 AND status = 'dispatched'
			RETURNING id
		`, task.ID).Scan(&taskID)
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("legacy reclaim error = %v, want no rows from DB authorization trigger", err)
		}
	})

	t.Run("old pod start is suppressed and scheduler cancels only Radar", func(t *testing.T) {
		agent, run, task := seedQueuedWorkspaceRadar(t)
		pool := integrationPool(t)
		q := db.New(pool)
		if _, err := pool.Exec(t.Context(), `
			UPDATE agent_inbox_event
			SET status = 'dispatched', dispatched_at = now()
			WHERE id = $1
		`, task.ID); err != nil {
			t.Fatalf("dispatch authorized Radar task: %v", err)
		}
		ordinary, err := q.CreateQuickCreateTask(t.Context(), db.CreateQuickCreateTaskParams{
			AgentID: agent.agentID, RuntimeID: agent.runtimeID, Priority: 0,
			Context: []byte(`{"type":"quick_create","prompt":"ordinary work"}`),
		})
		if err != nil {
			t.Fatalf("create ordinary task: %v", err)
		}
		downgradeRadarOwner(t, agent)

		var taskID pgtype.UUID
		err = pool.QueryRow(t.Context(), `
			UPDATE agent_inbox_event
			SET status = 'running', started_at = now()
			WHERE id = $1 AND status = 'dispatched'
			RETURNING id
		`, task.ID).Scan(&taskID)
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("legacy start error = %v, want no rows from DB authorization trigger", err)
		}

		taskSvc := service.NewTaskService(q, pool, nil, events.New())
		result, err := makeAgentRadarScheduleHandler(pool, taskSvc)(t.Context(), HandlerInput{PlanTime: time.Now().UTC()})
		if err != nil {
			t.Fatalf("run unauthorized Radar cleanup: %v", err)
		}
		if got := result.Result["repaired_unauthorized"]; got != int64(1) {
			t.Fatalf("repaired_unauthorized = %#v, want 1", got)
		}
		storedTask, err := q.GetAgentTask(t.Context(), task.ID)
		if err != nil || storedTask.Status != "cancelled" {
			t.Fatalf("Radar task after cleanup = %+v, %v; want cancelled", storedTask, err)
		}
		storedRun, err := q.GetAgentRadarRun(t.Context(), run.ID)
		if err != nil || storedRun.Status != "cancelled" {
			t.Fatalf("Radar run after cleanup = %+v, %v; want cancelled", storedRun, err)
		}
		ordinary, err = q.GetAgentTask(t.Context(), ordinary.ID)
		if err != nil || ordinary.Status != "queued" {
			t.Fatalf("ordinary task after Radar cleanup = %+v, %v; want queued", ordinary, err)
		}
	})
}

func seedQueuedWorkspaceRadar(t *testing.T) (radarTestAgent, db.AgentRadarRun, db.AgentInboxEvent) {
	t.Helper()
	agent := seedRadarTestAgent(t, "online")
	bindRadarSupervisor(t, agent)
	pool := integrationPool(t)
	taskSvc := service.NewTaskService(db.New(pool), pool, nil, events.New())
	run, task, err := taskSvc.EnqueueAgentRadarRun(t.Context(), service.EnqueueAgentRadarRunParams{
		WorkspaceID:    agent.workspaceID,
		AgentID:        agent.agentID,
		TriggerKind:    "scheduled",
		TriggerRef:     "dispatch-gate",
		CooldownKey:    agentRadarCooldownKey,
		ContextSummary: "dispatch gate test",
		ScheduledFor:   time.Now(),
		Prompt:         "private workspace context",
	})
	if err != nil {
		t.Fatalf("enqueue workspace Radar: %v", err)
	}
	return agent, run, task
}

func downgradeRadarOwner(t *testing.T, agent radarTestAgent) {
	t.Helper()
	if _, err := integrationPool(t).Exec(t.Context(), `
		UPDATE member SET role = 'member'
		WHERE workspace_id = $1 AND user_id = $2
	`, agent.workspaceID, agent.ownerID); err != nil {
		t.Fatalf("downgrade Radar supervisor owner: %v", err)
	}
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

func TestListRadarCandidatesReturnsOnlyBoundReadySupervisor(t *testing.T) {
	bound := seedRadarTestAgent(t, "online")
	offline := seedRadarTestAgent(t, "offline")
	archived := seedRadarTestAgent(t, "online")
	disabled := seedRadarTestAgent(t, "online")
	pool := integrationPool(t)

	for _, agent := range []radarTestAgent{bound, offline, archived, disabled} {
		if _, err := pool.Exec(t.Context(), `UPDATE agent SET display_name = 'Wendy' WHERE id = $1`, agent.agentID); err != nil {
			t.Fatalf("name bound supervisor Wendy: %v", err)
		}
		bindRadarSupervisor(t, agent)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE agent SET archived_at = now() WHERE id = $1`, archived.agentID); err != nil {
		t.Fatalf("archive bound supervisor: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE workspace_radar_state SET enabled = false WHERE workspace_id = $1`, disabled.workspaceID); err != nil {
		t.Fatalf("disable workspace Radar: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		DELETE FROM channel_member
		WHERE channel_id = $1 AND workspace_id = $2
		  AND member_type = 'agent' AND member_id = $3
	`, bound.channelID, bound.workspaceID, bound.agentID); err != nil {
		t.Fatalf("remove bound supervisor from ordinary channel: %v", err)
	}
	unboundOrdinary := seedAdditionalRadarTestAgent(t, bound, "Ordinary Agent", true)
	unboundWendy := seedAdditionalRadarTestAgent(t, bound, "Wendy", true)

	candidates, err := listRadarCandidates(t.Context(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if !hasRadarCandidate(candidates, bound.agentID) {
		t.Fatal("bound online supervisor without ordinary channel membership was not selected")
	}
	if hasRadarCandidate(candidates, offline.agentID) {
		t.Fatal("bound supervisor with offline runtime was selected")
	}
	if hasRadarCandidate(candidates, archived.agentID) {
		t.Fatal("bound archived supervisor was selected")
	}
	if hasRadarCandidate(candidates, disabled.agentID) {
		t.Fatal("supervisor from disabled workspace Radar state was selected")
	}
	if hasRadarCandidate(candidates, unboundOrdinary) {
		t.Fatal("unbound ordinary agent was selected")
	}
	if hasRadarCandidate(candidates, unboundWendy) {
		t.Fatal("unbound Wendy was selected instead of the explicit supervisor")
	}
	if _, err := pool.Exec(t.Context(), `
		UPDATE member
		SET role = 'member'
		WHERE workspace_id = $1 AND user_id = $2
	`, bound.workspaceID, bound.ownerID); err != nil {
		t.Fatalf("downgrade supervisor owner: %v", err)
	}
	candidates, err = listRadarCandidates(t.Context(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if hasRadarCandidate(candidates, bound.agentID) {
		t.Fatal("supervisor whose owner was downgraded remained schedulable")
	}
	if _, err := pool.Exec(t.Context(), `
		UPDATE member
		SET role = 'owner'
		WHERE workspace_id = $1 AND user_id = $2
	`, bound.workspaceID, bound.ownerID); err != nil {
		t.Fatalf("restore supervisor owner: %v", err)
	}

	wake := &radarWakeRecorder{}
	taskSvc := service.NewTaskService(db.New(pool), pool, nil, events.New(), wake)
	run, _, err := taskSvc.EnqueueAgentRadarRun(t.Context(), service.EnqueueAgentRadarRunParams{
		WorkspaceID:    bound.workspaceID,
		AgentID:        bound.agentID,
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
	if wake.calls != 1 || wake.runtimeID != uuidString(bound.runtimeID) || wake.taskID == "" {
		t.Fatalf("wake = %+v, want one directed runtime wake", wake)
	}
	task, err := db.New(pool).GetAgentTask(t.Context(), parseUUIDForSchedulerTest(t, wake.taskID))
	if err != nil {
		t.Fatalf("load scheduled workspace Radar task: %v", err)
	}
	if task.MaxAttempts != 1 {
		t.Fatalf("workspace Radar task max_attempts = %d, want 1 scheduler-owned retry budget", task.MaxAttempts)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE agent_radar_run SET status = 'executing' WHERE id = $1`, run.ID); err != nil {
		t.Fatalf("mark Radar action execution active: %v", err)
	}

	candidates, err = listRadarCandidates(t.Context(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if hasRadarCandidate(candidates, bound.agentID) {
		t.Fatal("bound supervisor with an active radar run was selected")
	}

	_, _, err = taskSvc.EnqueueAgentRadarRun(t.Context(), service.EnqueueAgentRadarRunParams{
		WorkspaceID:    bound.workspaceID,
		AgentID:        bound.agentID,
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

func parseUUIDForSchedulerTest(t *testing.T, value string) pgtype.UUID {
	t.Helper()
	parsed, err := uuid.Parse(value)
	if err != nil {
		t.Fatalf("parse UUID %q: %v", value, err)
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}
}

func TestWorkspaceRadarNeedsReviewSkipsUnchangedButKeepsPeriodicFullReview(t *testing.T) {
	agent := seedRadarTestAgent(t, "online")
	bindRadarSupervisor(t, agent)
	pool := integrationPool(t)
	watermark := time.Now().UTC()
	if _, err := pool.Exec(t.Context(), `
		UPDATE workspace_radar_state
		SET last_success_at = $2,
		    last_full_review_at = $2,
		    next_due_at = now()
		WHERE workspace_id = $1
	`, agent.workspaceID, watermark); err != nil {
		t.Fatalf("set workspace Radar watermark: %v", err)
	}
	candidate := radarCandidate{
		WorkspaceID:      agent.workspaceID,
		AgentID:          agent.agentID,
		LastSuccessAt:    pgtype.Timestamptz{Time: watermark, Valid: true},
		LastFullReviewAt: pgtype.Timestamptz{Time: watermark, Valid: true},
	}
	needsReview, fullReview, err := workspaceRadarNeedsReview(t.Context(), pool, candidate, watermark.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if needsReview || fullReview {
		t.Fatalf("unchanged workspace review decision = needs:%t full:%t, want false/false", needsReview, fullReview)
	}
	if err := deferUnchangedWorkspaceRadar(t.Context(), pool, candidate, watermark.Add(time.Minute)); err != nil {
		t.Fatalf("defer unchanged workspace: %v", err)
	}
	var nextDue time.Time
	if err := pool.QueryRow(t.Context(), `SELECT next_due_at FROM workspace_radar_state WHERE workspace_id = $1`, agent.workspaceID).Scan(&nextDue); err != nil {
		t.Fatal(err)
	}
	wantDue := watermark.Add(time.Minute).Add(workspaceRadarCheckInterval)
	if nextDue.Sub(wantDue) > time.Second || wantDue.Sub(nextDue) > time.Second {
		t.Fatalf("unchanged next_due_at = %s, want %s", nextDue, wantDue)
	}

	candidate.LastFullReviewAt = pgtype.Timestamptz{Time: watermark.Add(-agentRadarFullReviewPeriod), Valid: true}
	needsReview, fullReview, err = workspaceRadarNeedsReview(t.Context(), pool, candidate, watermark.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !needsReview || !fullReview {
		t.Fatalf("periodic review decision = needs:%t full:%t, want true/true", needsReview, fullReview)
	}
}

func TestWorkspaceRadarChangeDetectionIgnoresSupervisorSelfUpdateAndSeesPeerRuntime(t *testing.T) {
	agent := seedRadarTestAgent(t, "online")
	bindRadarSupervisor(t, agent)
	pool := integrationPool(t)
	peerAgentID := seedAdditionalRadarTestAgent(t, agent, "Runtime Peer", false)
	var peerRuntimeID pgtype.UUID
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO agent_runtime (
			workspace_id, owner_id, daemon_id, name, runtime_mode, provider,
			status, device_info, metadata, visibility, last_seen_at
		) VALUES ($1, $2, $3, 'peer-runtime', 'cloud', 'daytona',
		          'online', '', '{}'::jsonb, 'private', now())
		RETURNING id
	`, agent.workspaceID, agent.ownerID, "peer-daemon-"+uuid.NewString()).Scan(&peerRuntimeID); err != nil {
		t.Fatalf("create peer runtime: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE agent SET runtime_id = $2 WHERE id = $1`, peerAgentID, peerRuntimeID); err != nil {
		t.Fatalf("bind peer runtime: %v", err)
	}

	watermark := time.Now().UTC()
	if _, err := pool.Exec(t.Context(), `
		UPDATE workspace_radar_state
		SET last_success_at = $2, last_full_review_at = $2, next_due_at = now(),
		    change_cursor_version = change_version
		WHERE workspace_id = $1
	`, agent.workspaceID, watermark); err != nil {
		t.Fatalf("set workspace Radar watermark: %v", err)
	}
	candidate := radarCandidate{
		WorkspaceID:      agent.workspaceID,
		AgentID:          agent.agentID,
		LastSuccessAt:    pgtype.Timestamptz{Time: watermark, Valid: true},
		LastFullReviewAt: pgtype.Timestamptz{Time: watermark, Valid: true},
	}
	if _, err := pool.Exec(t.Context(), `UPDATE agent SET updated_at = $2 WHERE id = $1`, agent.agentID, watermark.Add(time.Minute)); err != nil {
		t.Fatalf("touch supervisor status row: %v", err)
	}
	needsReview, fullReview, err := workspaceRadarNeedsReview(t.Context(), pool, candidate, watermark.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if needsReview || fullReview {
		t.Fatalf("supervisor self-update decision = needs:%t full:%t, want false/false", needsReview, fullReview)
	}

	if _, err := pool.Exec(t.Context(), `UPDATE agent_runtime SET last_seen_at = $2, updated_at = $2 WHERE id = $1`, peerRuntimeID, watermark.Add(3*time.Minute)); err != nil {
		t.Fatalf("touch peer runtime: %v", err)
	}
	needsReview, fullReview, err = workspaceRadarNeedsReview(t.Context(), pool, candidate, watermark.Add(4*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if needsReview || fullReview {
		t.Fatalf("heartbeat-only peer runtime decision = needs:%t full:%t, want false/false", needsReview, fullReview)
	}

	if _, err := pool.Exec(t.Context(), `UPDATE agent_runtime SET status = 'offline', updated_at = $2 WHERE id = $1`, peerRuntimeID, watermark.Add(5*time.Minute)); err != nil {
		t.Fatalf("change peer runtime status: %v", err)
	}
	needsReview, fullReview, err = workspaceRadarNeedsReview(t.Context(), pool, candidate, watermark.Add(6*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !needsReview || fullReview {
		t.Fatalf("peer runtime status decision = needs:%t full:%t, want true/false", needsReview, fullReview)
	}

	wake := &radarWakeRecorder{}
	taskSvc := service.NewTaskService(db.New(pool), pool, nil, events.New(), wake)
	result, err := makeAgentRadarScheduleHandler(pool, taskSvc)(t.Context(), HandlerInput{PlanTime: watermark.Add(6 * time.Minute)})
	if err != nil {
		t.Fatalf("schedule runtime-change Radar: %v", err)
	}
	if result.Result["reason"] != "workgraph_handoffs" || wake.calls != 0 {
		t.Fatalf("runtime-change scheduler result = %#v wakes=%d, want workgraph skip with no wake", result.Result, wake.calls)
	}
	var runCount int
	if err := pool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM agent_radar_run
		WHERE workspace_id = $1
	`, agent.workspaceID).Scan(&runCount); err != nil {
		t.Fatal(err)
	}
	if runCount != 0 {
		t.Fatalf("runtime-change scheduled %d Radar runs, want 0", runCount)
	}
}

func TestWorkspaceRadarChangeCursorAdvancesOnlyThroughReviewedPage(t *testing.T) {
	agent := seedRadarTestAgent(t, "online")
	bindRadarSupervisor(t, agent)
	pool := integrationPool(t)
	if _, err := pool.Exec(t.Context(), `
		UPDATE workspace_radar_state
		SET change_cursor_version = change_version,
		    last_success_at = now(),
		    last_full_review_at = now()
		WHERE workspace_id = $1
	`, agent.workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO issue (
		  workspace_id, number, title, status, priority,
		  creator_type, creator_id, position
		)
		SELECT $1, 5000 + series, 'cursor issue ' || series::text,
		       'todo', 'none', 'agent', $2, series
		FROM generate_series(1, 25) series
	`, agent.workspaceID, agent.agentID); err != nil {
		t.Fatal(err)
	}

	observedAt := time.Now().UTC()
	contextPage, err := radar.NewContextBuilder(pool, "").BuildAt(
		t.Context(), uuidString(agent.workspaceID), uuidString(agent.agentID), observedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !contextPage.Scan.ChangesHasMore {
		t.Fatal("first change page has_more = false, want true")
	}
	if contextPage.Scan.ChangeCursorThroughVersion >= contextPage.Scan.ObservedChangeVersion {
		t.Fatalf("first cursor=%d observed=%d, want cursor before observed", contextPage.Scan.ChangeCursorThroughVersion, contextPage.Scan.ObservedChangeVersion)
	}

	taskSvc := service.NewTaskService(db.New(pool), pool, nil, events.New())
	run, _, err := taskSvc.EnqueueAgentRadarRun(t.Context(), service.EnqueueAgentRadarRunParams{
		WorkspaceID:    agent.workspaceID,
		AgentID:        agent.agentID,
		TriggerKind:    "scheduled",
		TriggerRef:     "cursor-page",
		CooldownKey:    agentRadarCooldownKey,
		ContextSummary: "cursor page test",
		ScheduledFor:   observedAt,
		Prompt:         radar.BuildPrompt(contextPage),
		Scan:           &contextPage.Scan,
	})
	if err != nil {
		t.Fatal(err)
	}
	q := db.New(pool)
	if _, err := q.UpdateAgentRadarRunStatus(t.Context(), db.UpdateAgentRadarRunStatusParams{ID: run.ID, Status: "executing"}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.UpdateAgentRadarRunStatus(t.Context(), db.UpdateAgentRadarRunStatusParams{ID: run.ID, Status: "no_action"}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.MarkWorkspaceRadarSucceeded(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	var cursor, version int64
	var nextDue time.Time
	if err := pool.QueryRow(t.Context(), `
		SELECT change_cursor_version, change_version, next_due_at
		FROM workspace_radar_state WHERE workspace_id = $1
	`, agent.workspaceID).Scan(&cursor, &version, &nextDue); err != nil {
		t.Fatal(err)
	}
	if cursor != contextPage.Scan.ChangeCursorThroughVersion || cursor >= version {
		t.Fatalf("persisted cursor=%d version=%d, want cursor=%d before version", cursor, version, contextPage.Scan.ChangeCursorThroughVersion)
	}
	if nextDue.After(time.Now().Add(time.Minute)) {
		t.Fatalf("next_due_at=%s, want immediate continuation", nextDue)
	}
}

func TestWorkspaceRadarDetectsActiveTaskCrossingStaleThreshold(t *testing.T) {
	agent := seedRadarTestAgent(t, "online")
	bindRadarSupervisor(t, agent)
	pool := integrationPool(t)
	peerAgentID := seedAdditionalRadarTestAgent(t, agent, "Slow Peer", false)
	watermark := time.Now().UTC()
	var taskID pgtype.UUID
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO agent_inbox_event (
			agent_id, runtime_id, status, priority, context, trigger_summary, created_at
		) VALUES ($1, $2, 'queued', 1, '{"type":"issue"}'::jsonb, 'long-running work', $3)
		RETURNING id
	`, peerAgentID, agent.runtimeID, watermark.Add(-59*time.Minute)).Scan(&taskID); err != nil {
		t.Fatalf("create nearly stale task: %v", err)
	}
	candidate := radarCandidate{
		WorkspaceID:      agent.workspaceID,
		AgentID:          agent.agentID,
		LastSuccessAt:    pgtype.Timestamptz{Time: watermark, Valid: true},
		LastFullReviewAt: pgtype.Timestamptz{Time: watermark, Valid: true},
	}
	needsReview, fullReview, err := workspaceRadarNeedsReview(t.Context(), pool, candidate, watermark.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !needsReview || fullReview {
		var createdAt, threshold, effectiveThreshold time.Time
		var progressAt pgtype.Timestamptz
		var crossed, workspaceCrossed bool
		if err := pool.QueryRow(t.Context(), `
			SELECT task.created_at,
			       task.created_at + interval '60 minutes',
			       progress.updated_at,
			       GREATEST(task.created_at, COALESCE(task.started_at, task.created_at), COALESCE(progress.updated_at, task.created_at)) + interval '60 minutes',
			       GREATEST(task.created_at, COALESCE(task.started_at, task.created_at), COALESCE(progress.updated_at, task.created_at)) + interval '60 minutes' > $2
			         AND GREATEST(task.created_at, COALESCE(task.started_at, task.created_at), COALESCE(progress.updated_at, task.created_at)) + interval '60 minutes' <= $3
			FROM agent_inbox_event task
			LEFT JOIN agent_task_progress_snapshot progress ON progress.task_id = task.id
			WHERE task.id = $1
		`, taskID, watermark, watermark.Add(2*time.Minute)).Scan(&createdAt, &threshold, &progressAt, &effectiveThreshold, &crossed); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(t.Context(), `
			SELECT EXISTS (
			  SELECT 1
			  FROM agent_inbox_event task
			  JOIN agent task_agent ON task_agent.id = task.agent_id
			  LEFT JOIN agent_task_progress_snapshot progress ON progress.task_id = task.id
			  WHERE task_agent.workspace_id = $1
			    AND COALESCE(task.context->>'type', '') <> 'agent_radar'
			    AND task.status IN ('queued', 'dispatched', 'running', 'waiting_local_directory')
			    AND GREATEST(task.created_at, COALESCE(task.started_at, task.created_at), COALESCE(progress.updated_at, task.created_at)) + interval '60 minutes' > $2
			    AND GREATEST(task.created_at, COALESCE(task.started_at, task.created_at), COALESCE(progress.updated_at, task.created_at)) + interval '60 minutes' <= $3
			)
		`, agent.workspaceID, watermark, watermark.Add(2*time.Minute)).Scan(&workspaceCrossed); err != nil {
			t.Fatal(err)
		}
		t.Fatalf(
			"stale-threshold decision = needs:%t full:%t, want true/false (created=%s threshold=%s progress=%v effective=%s crossed=%t workspace_crossed=%t watermark=%s plan=%s)",
			needsReview, fullReview, createdAt, threshold, progressAt, effectiveThreshold, crossed, workspaceCrossed, watermark, watermark.Add(2*time.Minute),
		)
	}
}

func TestWorkspaceRadarProviderFailureStartsSchedulerBackoff(t *testing.T) {
	agent := seedRadarTestAgent(t, "online")
	bindRadarSupervisor(t, agent)
	pool := integrationPool(t)
	taskSvc := service.NewTaskService(db.New(pool), pool, nil, events.New())
	_, task, err := taskSvc.EnqueueAgentRadarRun(t.Context(), service.EnqueueAgentRadarRunParams{
		WorkspaceID:    agent.workspaceID,
		AgentID:        agent.agentID,
		TriggerKind:    "scheduled",
		TriggerRef:     "provider-backoff",
		CooldownKey:    agentRadarCooldownKey,
		ContextSummary: "provider backoff test",
		ScheduledFor:   time.Now(),
		Prompt:         "inspect",
	})
	if err != nil {
		t.Fatalf("enqueue workspace Radar: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE agent_inbox_event SET status = 'dispatched' WHERE id = $1`, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := taskSvc.StartTask(t.Context(), task.ID); err != nil {
		t.Fatalf("start workspace Radar task: %v", err)
	}
	failedAt := time.Now()
	if _, err := taskSvc.FailTask(
		t.Context(),
		task.ID,
		"provider rate limit",
		"",
		"",
		"agent_error.provider_capacity_or_rate_limit",
	); err != nil {
		t.Fatalf("fail workspace Radar task: %v", err)
	}
	var failures int
	var nextDue time.Time
	if err := pool.QueryRow(t.Context(), `
		SELECT consecutive_failures, next_due_at
		FROM workspace_radar_state
		WHERE workspace_id = $1
	`, agent.workspaceID).Scan(&failures, &nextDue); err != nil {
		t.Fatal(err)
	}
	if failures != 1 {
		t.Fatalf("consecutive provider failures = %d, want 1", failures)
	}
	if nextDue.Before(failedAt.Add(14*time.Minute)) || nextDue.After(failedAt.Add(16*time.Minute)) {
		t.Fatalf("first provider failure next_due_at = %s, want about 15 minutes after %s", nextDue, failedAt)
	}
}

func TestEnqueueAgentRadarRunRejectsOfflineRuntimeWithoutRows(t *testing.T) {
	offline := seedRadarTestAgent(t, "offline")
	bindRadarSupervisor(t, offline)
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
		SELECT count(*) FROM agent_inbox_event
		WHERE agent_id = $1 AND context->>'type' = 'agent_radar'
	`, offline.agentID).Scan(&tasks); err != nil {
		t.Fatal(err)
	}
	if runs != 0 || tasks != 0 {
		t.Fatalf("offline enqueue persisted rows: runs=%d tasks=%d", runs, tasks)
	}
}

func TestAgentRadarScheduleDefersWorkspaceUntilRollingBudgetRecovers(t *testing.T) {
	tests := []struct {
		name       string
		attempts   func(time.Time) []time.Time
		wantResume func([]time.Time) time.Time
	}{
		{
			name: "hourly",
			attempts: func(now time.Time) []time.Time {
				return []time.Time{now.Add(-30 * time.Minute)}
			},
			wantResume: func(attempts []time.Time) time.Time { return attempts[0].Add(time.Hour) },
		},
		{
			name: "daily",
			attempts: func(now time.Time) []time.Time {
				attempts := make([]time.Time, agentRadarDailyBudget)
				for i := range attempts {
					attempts[i] = now.Add(-90*time.Minute - time.Duration(i)*30*time.Minute)
				}
				return attempts
			},
			wantResume: func(attempts []time.Time) time.Time {
				return attempts[len(attempts)-1].Add(24 * time.Hour)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := seedRadarTestAgent(t, "online")
			bindRadarSupervisor(t, agent)
			pool := integrationPool(t)
			planTime := time.Now().UTC().Truncate(time.Second)
			attempts := tt.attempts(planTime)
			for i, createdAt := range attempts {
				var runID pgtype.UUID
				if err := pool.QueryRow(t.Context(), `
					INSERT INTO agent_radar_run (
					  workspace_id, agent_id, runtime_id, trigger_kind, trigger_ref,
					  status, cooldown_key, context_summary, scheduled_for,
					  finished_at, created_at, updated_at
					) VALUES ($1, $2, $3, 'scheduled', $4,
					  'no_action', 'workspace_supervisor_radar', 'budget attempt', $5,
					  $5, $5, $5)
					RETURNING id
				`, agent.workspaceID, agent.agentID, agent.runtimeID, fmt.Sprintf("budget-%s-%d", tt.name, i), createdAt).Scan(&runID); err != nil {
					t.Fatalf("insert %s budget attempt %d: %v", tt.name, i, err)
				}
				if _, err := pool.Exec(t.Context(), `
					INSERT INTO workspace_radar_run_state_ack (radar_run_id, outcome)
					VALUES ($1, 'succeeded')
				`, runID); err != nil {
					t.Fatalf("ack %s budget attempt %d: %v", tt.name, i, err)
				}
			}
			if _, err := pool.Exec(t.Context(), `
				UPDATE workspace_radar_state SET next_due_at = now() - interval '1 minute'
				WHERE workspace_id = $1
			`, agent.workspaceID); err != nil {
				t.Fatalf("make budget workspace due: %v", err)
			}
			taskSvc := service.NewTaskService(db.New(pool), pool, nil, events.New())
			result, err := makeAgentRadarScheduleHandler(pool, taskSvc)(t.Context(), HandlerInput{PlanTime: planTime})
			if err != nil {
				t.Fatalf("run %s budget tick: %v", tt.name, err)
			}
			if result.RowsAffected != 0 || result.Result["reason"] != "workgraph_handoffs" {
				t.Fatalf("%s budget result = rows:%d reason:%#v, want 0/workgraph_handoffs", tt.name, result.RowsAffected, result.Result["reason"])
			}
			var runCount int
			if err := pool.QueryRow(t.Context(), `
				SELECT count(*) FROM agent_radar_run
				WHERE workspace_id = $1 AND trigger_kind = 'scheduled'
				  AND cooldown_key = 'workspace_supervisor_radar'
			`, agent.workspaceID).Scan(&runCount); err != nil {
				t.Fatalf("count %s budget attempts: %v", tt.name, err)
			}
			if runCount != len(attempts) {
				t.Fatalf("%s budget created attempt %d, want existing %d only", tt.name, runCount, len(attempts))
			}
			var nextDue time.Time
			if err := pool.QueryRow(t.Context(), `
				SELECT next_due_at FROM workspace_radar_state WHERE workspace_id = $1
			`, agent.workspaceID).Scan(&nextDue); err != nil {
				t.Fatalf("load %s budget next due: %v", tt.name, err)
			}
			if !nextDue.Before(planTime) {
				t.Fatalf("%s budget next_due_at = %s, want unchanged past due time after workgraph skip", tt.name, nextDue)
			}
		})
	}
}

func TestCountRecentWorkspaceScheduledRadarRunsUsesWorkspaceBudgetAndExcludesRepairArtifacts(t *testing.T) {
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
		UPDATE agent_inbox_event
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
			'failed', 'workspace_supervisor_radar', 'repair artifact',
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
			'failed', 'workspace_supervisor_radar', 'orphan repair artifact',
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
		UPDATE agent_inbox_event
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
			'failed', 'workspace_supervisor_radar', 'stale dispatch repair artifact',
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
		UPDATE agent_inbox_event
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
			'failed', 'workspace_supervisor_radar', 'real attempted run',
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
			'failed', 'workspace_supervisor_radar', 'real attempted run without error text',
			$4, $5, $6
		)
	`, agent.workspaceID, agent.agentID, agent.runtimeID,
		now.Add(-90*time.Second), now.Add(-45*time.Second), now.Add(-2*time.Minute)); err != nil {
		t.Fatalf("create genuine failed run without error text: %v", err)
	}

	otherWendyID := seedAdditionalRadarTestAgent(t, agent, "Replacement Wendy", false)
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO agent_radar_run (
			workspace_id, agent_id, runtime_id, trigger_kind, trigger_ref,
			status, cooldown_key, context_summary, finished_at, created_at
		) VALUES (
			$1, $2, $3, 'scheduled', 'replacement-wendy-attempt',
			'no_action', 'workspace_supervisor_radar', 'replacement attempted run', $4, $5
		)
	`, agent.workspaceID, otherWendyID, agent.runtimeID, now.Add(-20*time.Second), now.Add(-time.Minute)); err != nil {
		t.Fatalf("create replacement Wendy run: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO agent_radar_run (
			workspace_id, agent_id, runtime_id, trigger_kind, trigger_ref,
			status, cooldown_key, context_summary, finished_at, created_at
		) VALUES
			($1, $2, $3, 'manual', 'manual-does-not-spend-workspace-budget',
			 'succeeded', 'manual-radar', 'manual run', $4, $5),
			($1, $2, $3, 'event', 'historical-event-does-not-spend-workspace-budget',
			 'succeeded', 'event-radar', 'event run', $4, $5)
	`, agent.workspaceID, agent.agentID, agent.runtimeID, now.Add(-15*time.Second), now.Add(-30*time.Second)); err != nil {
		t.Fatalf("create non-scheduled budget controls: %v", err)
	}

	recent, err := q.CountRecentWorkspaceScheduledRadarRuns(t.Context(), db.CountRecentWorkspaceScheduledRadarRunsParams{
		WorkspaceID: agent.workspaceID,
		CreatedAt:   pgtype.Timestamptz{Time: now.Add(-time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("count recent Radar runs: %v", err)
	}
	if recent != 3 {
		t.Fatalf("recent workspace budget runs = %d, want 3 scheduled attempts across both Wendys", recent)
	}
}

func TestRepairStaleDispatchedRadarTasksPreservesFreshAndRunningTasks(t *testing.T) {
	staleAgent := seedRadarTestAgent(t, "online")
	freshAgent := seedRadarTestAgent(t, "online")
	runningAgent := seedRadarTestAgent(t, "online")
	bindRadarSupervisor(t, staleAgent)
	bindRadarSupervisor(t, freshAgent)
	bindRadarSupervisor(t, runningAgent)
	pool := integrationPool(t)
	q := db.New(pool)
	taskSvc := service.NewTaskService(q, pool, nil, events.New())
	now := time.Now().UTC()

	enqueue := func(agent radarTestAgent, ref string) (db.AgentRadarRun, db.AgentInboxEvent) {
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
		UPDATE agent_inbox_event
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
		UPDATE agent_inbox_event
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
		UPDATE agent_inbox_event
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
	bindRadarSupervisor(t, mismatchAgent)
	bindRadarSupervisor(t, nullRuntimeAgent)
	pool := integrationPool(t)
	q := db.New(pool)
	taskSvc := service.NewTaskService(q, pool, nil, events.New())
	now := time.Now().UTC()

	enqueueStaleDispatched := func(agent radarTestAgent, ref string) (db.AgentRadarRun, db.AgentInboxEvent) {
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
			UPDATE agent_inbox_event
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
		UPDATE agent_inbox_event SET runtime_id = $2 WHERE id = $1
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
		task db.AgentInboxEvent
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
	bindRadarSupervisor(t, agent)
	pool := integrationPool(t)
	taskSvc := service.NewTaskService(db.New(pool), pool, nil, events.New())

	enqueue := func(ref string) (db.AgentRadarRun, db.AgentInboxEvent) {
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
	if _, err := pool.Exec(t.Context(), `UPDATE agent_inbox_event SET status = 'dispatched' WHERE id = $1`, failedTask.ID); err != nil {
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
		UPDATE agent_inbox_event
		SET status = 'failed', completed_at = now(), error = 'queued expired', failure_reason = 'queued_expired'
		WHERE id = $1
	`, sweptTask.ID); err != nil {
		t.Fatal(err)
	}
	sweptTask, err := db.New(pool).GetAgentTask(t.Context(), sweptTask.ID)
	if err != nil {
		t.Fatal(err)
	}
	taskSvc.HandleFailedTasks(t.Context(), []db.AgentInboxEvent{sweptTask})
	if got := loadStatus(sweptRun.ID); got != "failed" {
		t.Fatalf("run after sweeper failure = %q, want failed", got)
	}

	interruptedRun, interruptedTask := enqueue("interrupted-completion")
	if _, err := pool.Exec(t.Context(), `
		UPDATE agent_inbox_event SET status = 'completed', completed_at = now() WHERE id = $1
	`, interruptedTask.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `
		UPDATE agent_radar_run
		SET status = 'executing', updated_at = now() - interval '6 minutes'
		WHERE id = $1
	`, interruptedRun.ID); err != nil {
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

func TestReconcileTerminalRadarRunsAppliesNewestUnackedOutcomePerWorkspace(t *testing.T) {
	agent := seedRadarTestAgent(t, "online")
	bindRadarSupervisor(t, agent)
	pool := integrationPool(t)
	formerSupervisorID := seedAdditionalRadarTestAgent(t, agent, "Former Wendy", false)
	newerScheduledFor := time.Now().UTC().Add(-10 * time.Minute)
	olderScheduledFor := newerScheduledFor.Add(-10 * time.Minute)
	formerScheduledFor := newerScheduledFor.Add(5 * time.Minute)
	var newerRunID, olderRunID, formerRunID pgtype.UUID
	insertRun := func(agentID pgtype.UUID, ref, status string, scheduledFor time.Time, runID *pgtype.UUID) {
		t.Helper()
		if err := pool.QueryRow(t.Context(), `
			INSERT INTO agent_radar_run (
				workspace_id, agent_id, runtime_id, trigger_kind, trigger_ref,
				status, cooldown_key, context_summary, scheduled_for, finished_at
			) VALUES ($1, $2, $3, 'scheduled', $4, $5, $6, 'reconcile test', $7, now())
			RETURNING id
		`, agent.workspaceID, agentID, agent.runtimeID, ref, status, agentRadarCooldownKey, scheduledFor).Scan(runID); err != nil {
			t.Fatalf("insert %s run: %v", status, err)
		}
	}
	// Insert in reverse scheduled order so row creation order cannot choose the outcome.
	insertRun(agent.agentID, "newer-success", "succeeded", newerScheduledFor, &newerRunID)
	insertRun(agent.agentID, "older-failure", "failed", olderScheduledFor, &olderRunID)
	// A former Wendy's later timestamp must not shadow and ACK away the current
	// Wendy outcome before that outcome updates workspace state.
	insertRun(formerSupervisorID, "former-wendy-later-cancel", "cancelled", formerScheduledFor, &formerRunID)

	if _, err := reconcileTerminalRadarRuns(t.Context(), pool); err != nil {
		t.Fatalf("reconcile pending terminal outcomes: %v", err)
	}
	var ackCount int
	if err := pool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM workspace_radar_run_state_ack
		WHERE radar_run_id IN ($1, $2, $3)
	`, newerRunID, olderRunID, formerRunID).Scan(&ackCount); err != nil {
		t.Fatal(err)
	}
	if ackCount != 3 {
		t.Fatalf("acknowledged terminal runs = %d, want 3", ackCount)
	}
	var lastSuccess, lastApplied time.Time
	var failures int
	if err := pool.QueryRow(t.Context(), `
		SELECT last_success_at, last_applied_scheduled_for, consecutive_failures
		FROM workspace_radar_state
		WHERE workspace_id = $1
	`, agent.workspaceID).Scan(&lastSuccess, &lastApplied, &failures); err != nil {
		t.Fatal(err)
	}
	if lastSuccess.Sub(newerScheduledFor) > time.Second || newerScheduledFor.Sub(lastSuccess) > time.Second {
		t.Fatalf("last_success_at = %s, want newest scheduled outcome %s", lastSuccess, newerScheduledFor)
	}
	if lastApplied.Sub(newerScheduledFor) > time.Second || newerScheduledFor.Sub(lastApplied) > time.Second {
		t.Fatalf("last_applied_scheduled_for = %s, want %s", lastApplied, newerScheduledFor)
	}
	if failures != 0 {
		t.Fatalf("consecutive_failures = %d, older failed outcome overrode newer success", failures)
	}
}

func TestAgentTaskMetricsExcludeRadarHousekeepingRuns(t *testing.T) {
	agent := seedRadarTestAgent(t, "online")
	bindRadarSupervisor(t, agent)
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
		UPDATE agent_inbox_event SET status = 'completed', completed_at = now() WHERE id = $1
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
		UPDATE agent_inbox_event SET status = 'completed', completed_at = now() WHERE id = $1
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
