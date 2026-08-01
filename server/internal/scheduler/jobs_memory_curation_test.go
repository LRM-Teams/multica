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

func TestMemoryCurationSchedulerCreatesDefaultSelfReviewRunIntent(t *testing.T) {
	pool := integrationPool(t)
	ctx := t.Context()
	suffix := uuid.NewString()[:8]
	var userID, workspaceID, runtimeID, targetAgentID string
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id::text`, "Default Self Review "+suffix, "default-self-review-"+suffix+"@example.test").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name, slug) VALUES ($1, $2) RETURNING id::text`, "Default Self Review "+suffix, "default-self-review-"+suffix).Scan(&workspaceID); err != nil {
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
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, owner_id, last_seen_at)
		VALUES ($1, $2, 'Default Self Review Runtime', 'local', 'codex', 'online', 'test', $3, now())
		RETURNING id::text
	`, workspaceID, "default-self-review-"+suffix, userID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id, owner_id, model)
		VALUES ($1, $2, 'local', $3, $4, 'composer-1.5') RETURNING id::text
	`, workspaceID, "default_self_review_target_"+suffix, runtimeID, userID).Scan(&targetAgentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id)
		VALUES ($1, 'Default self-review activity', 'todo', 'none', 'member', $2)
	`, workspaceID, userID); err != nil {
		t.Fatal(err)
	}
	var issueID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM issue WHERE workspace_id = $1 ORDER BY created_at DESC LIMIT 1`, workspaceID).Scan(&issueID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO agent_inbox_event (agent_id, issue_id, runtime_id, status, created_at)
		VALUES ($1, $2, $3, 'acked', '2026-07-09 12:00:00+00')
	`, targetAgentID, issueID, runtimeID); err != nil {
		t.Fatal(err)
	}
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	plan := time.Date(2026, 7, 10, defaultAgentSelfReviewScheduleHour, 0, 0, 0, loc).UTC()
	res, err := makeMemoryCurationIntentHandler(pool, memorycuration.StageAgentSelfReview, 0)(ctx, HandlerInput{PlanTime: plan})
	if err != nil {
		t.Fatal(err)
	}
	if res.RowsAffected != 1 {
		t.Fatalf("RowsAffected = %d, want 1 (%#v)", res.RowsAffected, res.Result)
	}
	var runID, status, planDate, mode string
	var confidence float64
	var targets []string
	if err := pool.QueryRow(ctx, `
		SELECT id::text, status, date_from::text, curator_mode, confidence_threshold, target_agent_ids::text[]
		  FROM memory_curation_run
		 WHERE workspace_id = $1 AND profile_id IS NULL AND stage = 'agent_self_review'
	`, workspaceID).Scan(&runID, &status, &planDate, &mode, &confidence, &targets); err != nil {
		t.Fatal(err)
	}
	if status != "queued" || planDate != "2026-07-09" || mode != defaultAgentSelfReviewMode || confidence != defaultAgentSelfReviewConfidence {
		t.Fatalf("run = status:%s date:%s mode:%s confidence:%v", status, planDate, mode, confidence)
	}
	if len(targets) != 1 || targets[0] != targetAgentID {
		t.Fatalf("targets = %v, want [%s]", targets, targetAgentID)
	}
	var childStatus string
	if err := pool.QueryRow(ctx, `
		SELECT status FROM memory_curation_agent_run
		 WHERE parent_run_id = $1 AND agent_id = $2 AND runtime_id = $3
	`, runID, targetAgentID, runtimeID).Scan(&childStatus); err != nil {
		t.Fatal(err)
	}
	if childStatus != "queued" {
		t.Fatalf("child status = %q, want queued", childStatus)
	}
}

func TestMemoryCurationSchedulerSkipsInactiveTargets(t *testing.T) {
	pool := integrationPool(t)
	ctx := t.Context()
	suffix := uuid.NewString()[:8]
	var userID, workspaceID, runtimeID, activeAgentID, inactiveAgentID string
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id::text`, "Default Self Review Inactive "+suffix, "default-self-review-inactive-"+suffix+"@example.test").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name, slug) VALUES ($1, $2) RETURNING id::text`, "Default Self Review Inactive "+suffix, "default-self-review-inactive-"+suffix).Scan(&workspaceID); err != nil {
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
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, owner_id, last_seen_at)
		VALUES ($1, $2, 'Default Self Review Inactive Runtime', 'local', 'codex', 'online', 'test', $3, now())
		RETURNING id::text
	`, workspaceID, "default-self-review-inactive-"+suffix, userID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id, owner_id, model)
		VALUES ($1, $2, 'local', $3, $4, 'composer-1.5') RETURNING id::text
	`, workspaceID, "default_self_review_active_"+suffix, runtimeID, userID).Scan(&activeAgentID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id, owner_id, model)
		VALUES ($1, $2, 'local', $3, $4, 'composer-1.5') RETURNING id::text
	`, workspaceID, "default_self_review_inactive_"+suffix, runtimeID, userID).Scan(&inactiveAgentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id)
		VALUES ($1, 'Default self-review active agent', 'todo', 'none', 'member', $2)
	`, workspaceID, userID); err != nil {
		t.Fatal(err)
	}
	var issueID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM issue WHERE workspace_id = $1 ORDER BY created_at DESC LIMIT 1`, workspaceID).Scan(&issueID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO agent_inbox_event (agent_id, issue_id, runtime_id, status, created_at)
		VALUES ($1, $2, $3, 'acked', '2026-07-09 12:00:00+00')
	`, activeAgentID, issueID, runtimeID); err != nil {
		t.Fatal(err)
	}
	plan := time.Date(2026, 7, 10, defaultAgentSelfReviewScheduleHour, 0, 0, 0, time.FixedZone("CST", 8*60*60)).UTC()
	res, err := makeMemoryCurationIntentHandler(pool, memorycuration.StageAgentSelfReview, 0)(ctx, HandlerInput{PlanTime: plan})
	if err != nil {
		t.Fatal(err)
	}
	if res.RowsAffected != 1 {
		t.Fatalf("RowsAffected = %d, want 1 (%#v)", res.RowsAffected, res.Result)
	}
	var targets []string
	if err := pool.QueryRow(ctx, `
		SELECT target_agent_ids::text[]
		  FROM memory_curation_run
		 WHERE workspace_id = $1 AND profile_id IS NULL AND stage = 'agent_self_review'
	`, workspaceID).Scan(&targets); err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0] != activeAgentID {
		t.Fatalf("targets = %v, want [%s] and not inactive %s", targets, activeAgentID, inactiveAgentID)
	}
}

func TestMemoryCurationSchedulerCountsLegacyTaskQueueActivity(t *testing.T) {
	pool := integrationPool(t)
	ctx := t.Context()
	available, err := relationExists(ctx, pool, "public.agent_task_queue")
	if err != nil {
		t.Fatal(err)
	}
	if !available {
		t.Skip("legacy agent_task_queue table is not present in this schema")
	}
	suffix := uuid.NewString()[:8]
	var userID, workspaceID, runtimeID, targetAgentID string
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id::text`, "Default Self Review Legacy "+suffix, "default-self-review-legacy-"+suffix+"@example.test").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name, slug) VALUES ($1, $2) RETURNING id::text`, "Default Self Review Legacy "+suffix, "default-self-review-legacy-"+suffix).Scan(&workspaceID); err != nil {
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
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, owner_id, last_seen_at)
		VALUES ($1, $2, 'Default Self Review Legacy Runtime', 'local', 'codex', 'online', 'test', $3, now())
		RETURNING id::text
	`, workspaceID, "default-self-review-legacy-"+suffix, userID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id, owner_id, model)
		VALUES ($1, $2, 'local', $3, $4, 'composer-1.5') RETURNING id::text
	`, workspaceID, "default_self_review_legacy_"+suffix, runtimeID, userID).Scan(&targetAgentID); err != nil {
		t.Fatal(err)
	}
	var issueID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id)
		VALUES ($1, 'Default self-review legacy task activity', 'todo', 'none', 'member', $2)
		RETURNING id::text
	`, workspaceID, userID).Scan(&issueID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO agent_task_queue (agent_id, issue_id, status, started_at, completed_at)
		VALUES ($1, $2, 'completed', '2026-07-09 11:00:00+00', '2026-07-09 12:00:00+00')
	`, targetAgentID, issueID); err != nil {
		t.Fatal(err)
	}
	plan := time.Date(2026, 7, 10, defaultAgentSelfReviewScheduleHour, 0, 0, 0, time.FixedZone("CST", 8*60*60)).UTC()
	res, err := makeMemoryCurationIntentHandler(pool, memorycuration.StageAgentSelfReview, 0)(ctx, HandlerInput{PlanTime: plan})
	if err != nil {
		t.Fatal(err)
	}
	if res.RowsAffected != 1 {
		t.Fatalf("RowsAffected = %d, want 1 (%#v)", res.RowsAffected, res.Result)
	}
	var targets []string
	if err := pool.QueryRow(ctx, `
		SELECT target_agent_ids::text[]
		  FROM memory_curation_run
		 WHERE workspace_id = $1 AND profile_id IS NULL AND stage = 'agent_self_review'
	`, workspaceID).Scan(&targets); err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0] != targetAgentID {
		t.Fatalf("targets = %v, want [%s]", targets, targetAgentID)
	}
}

func TestMemoryCurationSchedulerCountsMemoryWriteMaterial(t *testing.T) {
	pool := integrationPool(t)
	ctx := t.Context()
	available, err := relationExists(ctx, pool, "public.agent_memory_write_event")
	if err != nil {
		t.Fatal(err)
	}
	if !available {
		t.Skip("agent_memory_write_event table is not present in this schema")
	}
	suffix := uuid.NewString()[:8]
	var userID, workspaceID, runtimeID, materialAgentID, idleAgentID string
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id::text`, "Default Self Review Memory Write "+suffix, "default-self-review-memory-write-"+suffix+"@example.test").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name, slug) VALUES ($1, $2) RETURNING id::text`, "Default Self Review Memory Write "+suffix, "default-self-review-memory-write-"+suffix).Scan(&workspaceID); err != nil {
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
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, owner_id, last_seen_at)
		VALUES ($1, $2, 'Default Self Review Memory Write Runtime', 'local', 'codex', 'online', 'test', $3, now())
		RETURNING id::text
	`, workspaceID, "default-self-review-memory-write-"+suffix, userID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id, owner_id, model)
		VALUES ($1, $2, 'local', $3, $4, 'composer-1.5') RETURNING id::text
	`, workspaceID, "default_self_review_memory_write_"+suffix, runtimeID, userID).Scan(&materialAgentID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id, owner_id, model)
		VALUES ($1, $2, 'local', $3, $4, 'composer-1.5') RETURNING id::text
	`, workspaceID, "default_self_review_memory_idle_"+suffix, runtimeID, userID).Scan(&idleAgentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO agent_memory_write_event (
		  workspace_id, agent_id, runtime_id, rel_path, scope_type, file_key, content_hash, delta_chars, created_at
		) VALUES ($1, $2, $3, 'memory/daily/2026-07-09.md', 'agent_global', 'daily:2026-07-09', 'hash-2026-07-09', 128, '2026-07-09 10:00:00+00')
	`, workspaceID, materialAgentID, runtimeID); err != nil {
		t.Fatal(err)
	}
	plan := time.Date(2026, 7, 10, defaultAgentSelfReviewScheduleHour, 0, 0, 0, time.FixedZone("CST", 8*60*60)).UTC()
	res, err := makeMemoryCurationIntentHandler(pool, memorycuration.StageAgentSelfReview, 0)(ctx, HandlerInput{PlanTime: plan})
	if err != nil {
		t.Fatal(err)
	}
	if res.RowsAffected != 1 {
		t.Fatalf("RowsAffected = %d, want 1 (%#v)", res.RowsAffected, res.Result)
	}
	var targets []string
	if err := pool.QueryRow(ctx, `
		SELECT target_agent_ids::text[]
		  FROM memory_curation_run
		 WHERE workspace_id = $1 AND profile_id IS NULL AND stage = 'agent_self_review'
	`, workspaceID).Scan(&targets); err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0] != materialAgentID {
		t.Fatalf("targets = %v, want [%s] and not idle %s", targets, materialAgentID, idleAgentID)
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
