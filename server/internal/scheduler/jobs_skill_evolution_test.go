package scheduler

// Skill evolution reconciliation job (plan Slice 3.3): the sweep fails
// closed without a pool, applies only the safety terminals to active
// runs, and stays inert (no run creation, no auto-trigger) by design.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/service"
)

var (
	skillEvolutionSchemaOnce sync.Once
	skillEvolutionPool       *pgxpool.Pool
	skillEvolutionSchemaErr  error
)

// bootstrapSkillEvolutionSchema mirrors the service package's faithful
// per-run schema bootstrap: a private schema with the full migration
// chain, so the shared dev database is never touched.
func bootstrapSkillEvolutionSchema(t *testing.T) *pgxpool.Pool {
	t.Helper()
	skillEvolutionSchemaOnce.Do(func() {
		databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
		if databaseURL == "" {
			databaseURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
		}
		separator := "?"
		if strings.Contains(databaseURL, "?") {
			separator = "&"
		}
		admin, err := pgxpool.New(context.Background(), databaseURL)
		if err != nil {
			skillEvolutionSchemaErr = err
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		conn, err := admin.Acquire(ctx)
		if err != nil {
			admin.Close()
			skillEvolutionSchemaErr = err
			return
		}
		schema := fmt.Sprintf("skill_evolution_sched_test_%d", time.Now().UnixNano())
		quoted := pgx.Identifier{schema}.Sanitize()
		if _, err := conn.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
			conn.Release()
			admin.Close()
			skillEvolutionSchemaErr = err
			return
		}
		// Migration 314 needs its fixture dummies outside public.
		for _, fn := range []string{"test_agent_inbox_fixture_defaults", "test_server_agent_inbox_fixture_defaults"} {
			if _, err := conn.Exec(ctx, fmt.Sprintf(
				"CREATE FUNCTION %s.%s() RETURNS void LANGUAGE sql AS 'SELECT NULL'", quoted, fn)); err != nil {
				conn.Release()
				admin.Close()
				skillEvolutionSchemaErr = err
				return
			}
		}
		conn.Release()

		dir, err := os.Getwd()
		if err != nil {
			admin.Close()
			skillEvolutionSchemaErr = err
			return
		}
		for {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				admin.Close()
				skillEvolutionSchemaErr = fmt.Errorf("find server module root")
				return
			}
			dir = parent
		}
		cmd := exec.CommandContext(ctx, "go", "run", "./cmd/migrate", "up")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "DATABASE_URL="+databaseURL+separator+"search_path="+schema+",public")
		if output, err := cmd.CombinedOutput(); err != nil {
			admin.Close()
			skillEvolutionSchemaErr = fmt.Errorf("apply migrations: %w: %s", err, output)
			return
		}

		scoped, err := pgxpool.New(context.Background(), databaseURL+separator+"search_path="+schema+",public")
		if err != nil {
			admin.Close()
			skillEvolutionSchemaErr = err
			return
		}
		admin.Close()
		skillEvolutionPool = scoped
	})
	if skillEvolutionPool == nil {
		t.Skipf("skill evolution scheduler tests require a bootstrapable Postgres: %v", skillEvolutionSchemaErr)
	}
	return skillEvolutionPool
}

type skillEvolutionSweepFixture struct {
	pool        *pgxpool.Pool
	workspaceID string
	agentID     string
}

func newSkillEvolutionSweepFixture(t *testing.T) *skillEvolutionSweepFixture {
	t.Helper()
	pool := bootstrapSkillEvolutionSchema(t)
	ctx := context.Background()
	f := &skillEvolutionSweepFixture{pool: pool}
	unique := fmt.Sprintf("%d", time.Now().UnixNano())
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug) VALUES ('evo sweep test', 'evo-sweep-`+unique+`')
		RETURNING id::text`).Scan(&f.workspaceID))
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM workspace WHERE id=$1::uuid`, f.workspaceID) })

	var ownerID string
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ('sweep-owner', 'sweep-owner-`+unique+`@multica.ai')
		RETURNING id::text`).Scan(&ownerID))
	var runtimeID, agentID string
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO agent_runtime(workspace_id,daemon_id,name,runtime_mode,provider,status,device_info,metadata,visibility,last_seen_at)
		VALUES($1::uuid,$2,'sweep-runtime','local','pi','online','','{}','private',now()) RETURNING id::text`,
		f.workspaceID, "sweep-daemon-"+unique).Scan(&runtimeID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO agent(workspace_id,name,display_name,runtime_mode,runtime_config,runtime_id,owner_id,managed_role,instructions)
		VALUES($1::uuid,$2,'Sweep agent','local','{}',$3::uuid,$4::uuid,'graph_memory_channel','managed memory') RETURNING id::text`,
		f.workspaceID, "sweep-agent-"+unique, runtimeID, ownerID).Scan(&agentID))
	f.agentID = agentID
	return f
}

// addIdleRun inserts a queued run nobody drives (owner actor column is
// the curator; the run simply has no lease).
func (f *skillEvolutionSweepFixture) addIdleRun(t *testing.T, taskType string) string {
	t.Helper()
	var runID string
	require.NoError(t, f.pool.QueryRow(context.Background(), `
		INSERT INTO skill_evolution_run (workspace_id, target_agent_id, task_type, environment_major_version, created_by_actor)
		VALUES ($1::uuid, $2::uuid, $3, 'v1', 'curator:test')
		RETURNING id::text`, f.workspaceID, f.agentID, taskType).Scan(&runID))
	return runID
}

// The sweep requires a pool and never creates runs: an empty fleet is an
// empty report, not an error or a manufactured run.
func TestSkillEvolutionOrchestratorReconciliationJobNeedsPoolAndStaysIdle(t *testing.T) {
	_, err := RunSkillEvolutionReconciliation(context.Background(), nil, service.SkillEvolutionFeatureGates{}, 0)
	assert.Error(t, err, "no pool fails closed")

	pool := bootstrapSkillEvolutionSchema(t)
	reports, err := RunSkillEvolutionReconciliation(context.Background(), pool, service.SkillEvolutionFeatureGates{}, time.Minute)
	require.NoError(t, err)
	assert.Empty(t, reports, "no active runs means no reports")

	var runs int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT count(*) FROM skill_evolution_run`).Scan(&runs))
	assert.Zero(t, runs, "reconciliation must never create runs")
}

// The sweep fails idle phases past their deadline, leaves leased runs to
// their owners, and works with the pattern gate either way.
func TestSkillEvolutionOrchestratorReconciliationJobSweepsSafetyTerminals(t *testing.T) {
	f := newSkillEvolutionSweepFixture(t)
	idleID := f.addIdleRun(t, "spreadsheet_export")

	// A second, live workspace run under a lease must be awaited: seed it
	// through the orchestrator service itself (the job shares the store).
	ledger := service.NewPostgresSkillEvolutionLedger(f.pool)
	orchestrator := service.NewSkillEvolutionOrchestratorService(f.pool, ledger)
	ctx := context.Background()

	reports, err := RunSkillEvolutionReconciliation(ctx, f.pool, service.SkillEvolutionFeatureGates{}, time.Nanosecond)
	require.NoError(t, err)
	require.Len(t, reports, 1)
	assert.Equal(t, f.workspaceID, reports[0].WorkspaceID)
	assert.Equal(t, 1, reports[0].Runs.Examined)
	assert.Equal(t, 1, reports[0].Runs.Failed, "the idle phase fails at a nanosecond deadline")
	assert.Zero(t, reports[0].OutboxEventsDispatched, "pattern gate off means no drain")

	var status string
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT status FROM skill_evolution_run WHERE id=$1::uuid`, idleID).Scan(&status))
	assert.Equal(t, "failed", status)

	// A live lease is awaited, not failed: a fresh run under a live lease
	// survives a nanosecond-deadline sweep.
	leasedID := f.addIdleRun(t, "sheet_formula_audit")
	_, err = orchestrator.AcquireLease(ctx, f.workspaceID, leasedID, "worker-a", 5*time.Minute)
	require.NoError(t, err)
	reports, err = RunSkillEvolutionReconciliation(ctx, f.pool, service.SkillEvolutionFeatureGates{}, time.Nanosecond)
	require.NoError(t, err)
	require.Len(t, reports, 1)
	assert.Equal(t, 1, reports[0].Runs.Awaited, "a live lease is never disturbed")
	assert.Zero(t, reports[0].Runs.Failed)
	var leasedStatus string
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT status FROM skill_evolution_run WHERE id=$1::uuid`, leasedID).Scan(&leasedStatus))
	assert.Equal(t, "queued", leasedStatus)

	var lane string
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT lane FROM skill_evolution_reconciliation WHERE workspace_id=$1::uuid`, f.workspaceID).Scan(&lane))
	assert.Equal(t, "orchestrator", lane)

	// The sweep is idempotent: nothing changes on a re-run.
	reports, err = RunSkillEvolutionReconciliation(ctx, f.pool, service.SkillEvolutionFeatureGates{}, time.Nanosecond)
	require.NoError(t, err)
	assert.Equal(t, 1, reports[0].Runs.Awaited)
}
