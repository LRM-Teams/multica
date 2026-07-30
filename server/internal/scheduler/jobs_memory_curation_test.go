package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/memorycuration"
)

func TestMemoryCurationJobsUseStableNames(t *testing.T) {
	jobs := MemoryCurationJobs(nil)
	want := []string{
		JobNameAgentMemorySelfReview,
		JobNameTeamMemoryCuration,
	}
	if len(jobs) != len(want) {
		t.Fatalf("len(jobs) = %d, want %d", len(jobs), len(want))
	}
	for i, job := range jobs {
		if job.Name != want[i] {
			t.Fatalf("job[%d].Name = %q, want %q", i, job.Name, want[i])
		}
		if job.Cadence != time.Hour {
			t.Fatalf("job[%d].Cadence = %s, want 1h", i, job.Cadence)
		}
		if err := job.validate(); err != nil {
			t.Fatalf("job[%d] did not validate: %v", i, err)
		}
	}
}

func TestMemoryCurationJobsRequireDatabaseForProfileScheduling(t *testing.T) {
	jobs := MemoryCurationJobs(nil)
	res, err := jobs[0].Handler(t.Context(), HandlerInput{PlanTime: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Result["reason"] != "database_unavailable" {
		t.Fatalf("handler result = %#v, want database_unavailable", res.Result)
	}
}

func TestMemoryCurationStageCycleHandlesMidnight(t *testing.T) {
	loc, err := time.LoadLocation(memorycuration.DefaultTimezone)
	if err != nil {
		t.Fatal(err)
	}
	localPlan := time.Date(2026, 7, 10, 0, 0, 0, 0, loc)
	cycleLocal := localPlan.Add(-3 * time.Hour)
	if cycleLocal.Hour() != 21 || cycleLocal.Format("2006-01-02") != "2026-07-09" {
		t.Fatalf("cycleLocal = %s, want 2026-07-09 21:00", cycleLocal)
	}
}

func TestMemoryCurationPlanDateUsesBeijingYesterday(t *testing.T) {
	loc, err := time.LoadLocation(memorycuration.DefaultTimezone)
	if err != nil {
		t.Fatal(err)
	}
	planLocal := time.Date(2026, 7, 10, 1, 0, 0, 0, loc)
	planDate := time.Date(planLocal.Year(), planLocal.Month(), planLocal.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
	if got := planDate.Format("2006-01-02"); got != "2026-07-09" {
		t.Fatalf("planDate = %s, want 2026-07-09", got)
	}
}

func TestMemoryCurationSchedulerCreatesProfileRunIntent(t *testing.T) {
	pool := integrationPool(t)
	ctx := t.Context()
	suffix := uuid.NewString()[:8]
	var userID, workspaceID, runtimeID, curatorAgentID, targetAgentID, profileID string
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id::text`, "Curator Scheduler "+suffix, "curator-scheduler-"+suffix+"@example.test").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name, slug) VALUES ($1, $2) RETURNING id::text`, "Curator Scheduler "+suffix, "curator-scheduler-"+suffix).Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID)
	})
	if _, err := pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, workspaceID, userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, owner_id)
		VALUES ($1, $2, 'Curator Scheduler Runtime', 'local', 'codex', 'online', 'test', $3)
		RETURNING id::text
	`, workspaceID, "curator-scheduler-"+suffix, userID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id, owner_id, model)
		VALUES ($1, $2, 'local', $3, $4, 'composer-1.5') RETURNING id::text
	`, workspaceID, "curator_scheduler_"+suffix, runtimeID, userID).Scan(&curatorAgentID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id, owner_id, model)
		VALUES ($1, $2, 'local', $3, $4, 'composer-1.5') RETURNING id::text
	`, workspaceID, "curator_target_"+suffix, runtimeID, userID).Scan(&targetAgentID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO memory_curator_profile (
		  workspace_id, user_id, enabled, self_review_enabled, team_curation_enabled,
		  mode, runtime_id, curator_agent_id, target_scope, timezone,
		  schedule_hour, catch_up_enabled, confidence_threshold
		) VALUES ($1,$2,true,true,true,'auto_safe',$3,$4,'selected','Asia/Shanghai',23,true,0.9)
		RETURNING id::text
	`, workspaceID, userID, runtimeID, curatorAgentID).Scan(&profileID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO memory_curator_target (profile_id, agent_id) VALUES ($1, $2)`, profileID, targetAgentID); err != nil {
		t.Fatal(err)
	}

	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id)
		VALUES ($1, 'Memory curation activity', 'todo', 'none', 'member', $2)
	`, workspaceID, userID); err != nil {
		t.Fatal(err)
	}
	var issueID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM issue WHERE workspace_id = $1 ORDER BY created_at DESC LIMIT 1`, workspaceID).Scan(&issueID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO agent_inbox_event (agent_id, issue_id, runtime_id, status, created_at)
		VALUES ($1, $2, $3, 'completed', '2026-07-09 12:00:00+00')
	`, targetAgentID, issueID, runtimeID); err != nil {
		t.Fatal(err)
	}
	plan := time.Date(2026, 7, 11, 0, 0, 0, 0, loc).UTC()
	res, err := makeMemoryCurationIntentHandler(pool, memorycuration.StageAgentSelfReview, 1)(ctx, HandlerInput{PlanTime: plan})
	if err != nil {
		t.Fatal(err)
	}
	if res.RowsAffected != 1 {
		t.Fatalf("RowsAffected = %d, want 1 (%#v)", res.RowsAffected, res.Result)
	}
	var status, planDate, mode string
	var confidence float64
	var targets []string
	if err := pool.QueryRow(ctx, `
		SELECT status, date_from::text, curator_mode, confidence_threshold, target_agent_ids::text[]
		  FROM memory_curation_run WHERE profile_id = $1 AND stage = 'agent_self_review'
	`, profileID).Scan(&status, &planDate, &mode, &confidence, &targets); err != nil {
		t.Fatal(err)
	}
	if status != "queued" || planDate != "2026-07-09" || mode != "auto_safe" || confidence != 0.9 {
		t.Fatalf("run = status:%s date:%s mode:%s confidence:%v", status, planDate, mode, confidence)
	}
	if len(targets) != 1 || targets[0] != targetAgentID {
		t.Fatalf("targets = %v, want [%s]", targets, targetAgentID)
	}
}

func TestMemoryCurationSchedulerSkipsInactiveTargets(t *testing.T) {
	pool := integrationPool(t)
	ctx := t.Context()
	suffix := uuid.NewString()[:8]
	var userID, workspaceID, runtimeID, curatorAgentID, activeAgentID, inactiveAgentID, profileID string
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id::text`, "Curator Scheduler Inactive "+suffix, "curator-scheduler-inactive-"+suffix+"@example.test").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name, slug) VALUES ($1, $2) RETURNING id::text`, "Curator Scheduler Inactive "+suffix, "curator-scheduler-inactive-"+suffix).Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID)
	})
	if _, err := pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, workspaceID, userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, owner_id)
		VALUES ($1, $2, 'Curator Scheduler Runtime Inactive', 'local', 'codex', 'online', 'test', $3)
		RETURNING id::text
	`, workspaceID, "curator-scheduler-inactive-"+suffix, userID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id, owner_id, model)
		VALUES ($1, $2, 'local', $3, $4, 'composer-1.5') RETURNING id::text
	`, workspaceID, "curator_scheduler_inactive_"+suffix, runtimeID, userID).Scan(&curatorAgentID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id, owner_id, model)
		VALUES ($1, $2, 'local', $3, $4, 'composer-1.5') RETURNING id::text
	`, workspaceID, "curator_active_"+suffix, runtimeID, userID).Scan(&activeAgentID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id, owner_id, model)
		VALUES ($1, $2, 'local', $3, $4, 'composer-1.5') RETURNING id::text
	`, workspaceID, "curator_inactive_"+suffix, runtimeID, userID).Scan(&inactiveAgentID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO memory_curator_profile (
		  workspace_id, user_id, enabled, self_review_enabled, team_curation_enabled,
		  mode, runtime_id, curator_agent_id, target_scope, timezone,
		  schedule_hour, catch_up_enabled, confidence_threshold
		) VALUES ($1,$2,true,true,true,'auto_safe',$3,$4,'selected','Asia/Shanghai',23,true,0.9)
		RETURNING id::text
	`, workspaceID, userID, runtimeID, curatorAgentID).Scan(&profileID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO memory_curator_target (profile_id, agent_id) VALUES ($1, $2), ($1, $3)`, profileID, activeAgentID, inactiveAgentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id)
		VALUES ($1, 'Memory curation active agent', 'todo', 'none', 'member', $2)
	`, workspaceID, userID); err != nil {
		t.Fatal(err)
	}
	var issueID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM issue WHERE workspace_id = $1 ORDER BY created_at DESC LIMIT 1`, workspaceID).Scan(&issueID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO agent_inbox_event (agent_id, issue_id, runtime_id, status, created_at)
		VALUES ($1, $2, $3, 'completed', '2026-07-09 12:00:00+00')
	`, activeAgentID, issueID, runtimeID); err != nil {
		t.Fatal(err)
	}
	plan := time.Date(2026, 7, 11, 0, 0, 0, 0, time.FixedZone("CST", 8*60*60)).UTC()
	res, err := makeMemoryCurationIntentHandler(pool, memorycuration.StageAgentSelfReview, 1)(ctx, HandlerInput{PlanTime: plan})
	if err != nil {
		t.Fatal(err)
	}
	if res.RowsAffected != 1 {
		t.Fatalf("RowsAffected = %d, want 1 (%#v)", res.RowsAffected, res.Result)
	}
	var targets []string
	if err := pool.QueryRow(ctx, `
		SELECT target_agent_ids::text[]
		  FROM memory_curation_run WHERE profile_id = $1 AND stage = 'agent_self_review'
	`, profileID).Scan(&targets); err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0] != activeAgentID {
		t.Fatalf("targets = %v, want [%s]", targets, activeAgentID)
	}
}

func TestMemoryCurationStageNormalization(t *testing.T) {
	cases := map[string]memorycuration.Stage{
		"agent_self_review": memorycuration.StageAgentSelfReview,
		"self_review":       memorycuration.StageAgentSelfReview,
		"team_curation":     memorycuration.StageTeamCuration,
		"curator":           memorycuration.StageTeamCuration,
		"all":               memorycuration.StageAll,
	}
	for input, want := range cases {
		got, err := memorycuration.NormalizeStage(input)
		if err != nil {
			t.Fatalf("NormalizeStage(%q) error: %v", input, err)
		}
		if got != want {
			t.Fatalf("NormalizeStage(%q) = %q, want %q", input, got, want)
		}
	}
}
