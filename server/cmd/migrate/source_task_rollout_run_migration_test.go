package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/pkg/db/generated"
)

var _ interface {
	UpsertSourceTask(context.Context, db.UpsertSourceTaskParams) (db.SourceTask, error)
	GetSourceTaskForWorkspace(context.Context, db.GetSourceTaskForWorkspaceParams) (db.SourceTask, error)
	CreateEnvDispatchRunWithSource(context.Context, db.CreateEnvDispatchRunWithSourceParams) (db.EnvDispatchRun, error)
	SetEnvDispatchRunLocalTargets(context.Context, db.SetEnvDispatchRunLocalTargetsParams) error
	GetEnvDispatchRunSourceTask(context.Context, db.GetEnvDispatchRunSourceTaskParams) (db.SourceTask, error)
	GetSweLegoTemplateCache(context.Context, db.GetSweLegoTemplateCacheParams) (db.SweLegoTemplateCache, error)
	ClaimSweLegoTemplateBuild(context.Context, db.ClaimSweLegoTemplateBuildParams) (db.SweLegoTemplateCache, error)
	SetSweLegoTemplateBuildBuilder(context.Context, db.SetSweLegoTemplateBuildBuilderParams) error
	CompleteSweLegoTemplateBuild(context.Context, db.CompleteSweLegoTemplateBuildParams) (db.SweLegoTemplateCache, error)
	FailSweLegoTemplateBuild(context.Context, db.FailSweLegoTemplateBuildParams) (db.SweLegoTemplateCache, error)
} = (*db.Queries)(nil)

func TestSourceTaskRolloutRunMigration(t *testing.T) {
	upSQL, downSQL := readTaskTemplateMigrationPair(t, "274_source_task_rollout_run")
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn := taskTemplateMigrationSchema(t, ctx, pool, "source_task_rollout_run")
	defer conn.Release()

	if _, err := conn.Exec(ctx, `
		CREATE TABLE workspace (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
		CREATE TABLE project (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			workspace_id UUID NOT NULL REFERENCES workspace(id)
		);
		CREATE TABLE issue (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
		CREATE TABLE channel (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
		CREATE TABLE env_dispatch_run (
			project_id UUID PRIMARY KEY REFERENCES project(id) ON DELETE CASCADE,
			workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
			training_mode BOOLEAN NOT NULL,
			root_task_id UUID,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
	`); err != nil {
		t.Fatalf("create pre-274 schema: %v", err)
	}

	if _, err := conn.Exec(ctx, upSQL); err != nil {
		t.Fatalf("apply 274 up: %v", err)
	}

	var workspaceID, firstProjectID, secondProjectID string
	if err := conn.QueryRow(ctx, `INSERT INTO workspace DEFAULT VALUES RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	for _, destination := range []*string{&firstProjectID, &secondProjectID} {
		if err := conn.QueryRow(ctx, `INSERT INTO project (workspace_id) VALUES ($1) RETURNING id`, workspaceID).Scan(destination); err != nil {
			t.Fatalf("seed project: %v", err)
		}
	}

	var sourceTaskID string
	if err := conn.QueryRow(ctx, `
		INSERT INTO source_task (workspace_id, type, payload, content_hash)
		VALUES ($1, 'issue', '{"title":"task"}'::jsonb, repeat('a', 64))
		RETURNING id
	`, workspaceID).Scan(&sourceTaskID); err != nil {
		t.Fatalf("insert issue source task: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO source_task (workspace_id, type, payload, content_hash)
		VALUES ($1, 'message', '{"content":"task"}'::jsonb, repeat('a', 64))
	`, workspaceID); err == nil {
		t.Fatal("duplicate workspace/content hash insert succeeded")
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO source_task (workspace_id, type, payload, content_hash)
		VALUES ($1, 'message', '{"content":"task"}'::jsonb, repeat('b', 64))
	`, workspaceID); err != nil {
		t.Fatalf("insert message source task: %v", err)
	}

	var firstRunID, secondRunID string
	for _, run := range []struct {
		projectID string
		runID     *string
	}{
		{firstProjectID, &firstRunID},
		{secondProjectID, &secondRunID},
	} {
		if err := conn.QueryRow(ctx, `
			INSERT INTO env_dispatch_run (project_id, workspace_id, training_mode, source_task_id)
			VALUES ($1, $2, false, $3)
			RETURNING run_id
		`, run.projectID, workspaceID, sourceTaskID).Scan(run.runID); err != nil {
			t.Fatalf("insert rollout run for project %s: %v", run.projectID, err)
		}
	}
	if firstRunID == secondRunID {
		t.Fatalf("run_id reused across rollouts: %q", firstRunID)
	}

	if _, err := conn.Exec(ctx, downSQL); err != nil {
		t.Fatalf("apply 274 down: %v", err)
	}
}

func TestSweLegoTemplateCacheMigration(t *testing.T) {
	upSQL, downSQL := readTaskTemplateMigrationPair(t, "275_swe_lego_template_cache")
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn := taskTemplateMigrationSchema(t, ctx, pool, "template_cache")
	defer conn.Release()

	if _, err := conn.Exec(ctx, `
		CREATE TABLE sandbox_node (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
		CREATE TABLE sandbox_instance (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			node_id UUID NOT NULL REFERENCES sandbox_node(id)
		);
	`); err != nil {
		t.Fatalf("create pre-275 schema: %v", err)
	}

	if _, err := conn.Exec(ctx, upSQL); err != nil {
		t.Fatalf("apply 275 up: %v", err)
	}

	var firstNodeID, secondNodeID string
	if err := conn.QueryRow(ctx, `INSERT INTO sandbox_node DEFAULT VALUES RETURNING id`).Scan(&firstNodeID); err != nil {
		t.Fatalf("seed first sandbox node: %v", err)
	}
	if err := conn.QueryRow(ctx, `INSERT INTO sandbox_node DEFAULT VALUES RETURNING id`).Scan(&secondNodeID); err != nil {
		t.Fatalf("seed second sandbox node: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO swe_lego_template_cache (node_id, cache_key, parent_template_id, status)
		VALUES ($1, repeat('d', 64), 'parent-template', 'ready')
	`, firstNodeID); err == nil {
		t.Fatal("ready cache row without task_template_id succeeded")
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO swe_lego_template_cache (node_id, cache_key, parent_template_id, status)
		VALUES ($1, repeat('c', 64), 'parent-template', 'building')
	`, firstNodeID); err != nil {
		t.Fatalf("insert pending cache row: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO swe_lego_template_cache (node_id, cache_key, parent_template_id, status)
		VALUES ($1, repeat('c', 64), 'other-parent', 'building')
	`, firstNodeID); err == nil {
		t.Fatal("duplicate node/cache key insert succeeded")
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO swe_lego_template_cache (node_id, cache_key, parent_template_id, status)
		VALUES ($1, repeat('c', 64), 'parent-template', 'building')
	`, secondNodeID); err != nil {
		t.Fatalf("cache key was incorrectly global rather than node-local: %v", err)
	}

	if _, err := conn.Exec(ctx, downSQL); err != nil {
		t.Fatalf("apply 275 down: %v", err)
	}
}

func taskTemplateMigrationSchema(t *testing.T, ctx context.Context, pool *pgxpool.Pool, prefix string) *pgxpool.Conn {
	t.Helper()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	schema := fmt.Sprintf("%s_migration_test_%d", prefix, time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		conn.Release()
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE"); err != nil {
			t.Logf("drop schema %s: %v", schema, err)
		}
	})
	if _, err := conn.Exec(ctx, "SET search_path TO "+quotedSchema); err != nil {
		conn.Release()
		t.Fatalf("set search path: %v", err)
	}
	return conn
}

func readTaskTemplateMigrationPair(t *testing.T, name string) (upSQL, downSQL string) {
	t.Helper()
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(testFile), "..", "..", "migrations")
	up, err := os.ReadFile(filepath.Join(migrationsDir, name+".up.sql"))
	if err != nil {
		t.Fatalf("read %s up: %v", name, err)
	}
	down, err := os.ReadFile(filepath.Join(migrationsDir, name+".down.sql"))
	if err != nil {
		t.Fatalf("read %s down: %v", name, err)
	}
	return string(up), string(down)
}

func TestSweLegoTemplateCacheClaimReclaim(t *testing.T) {
	upSQL, _ := readTaskTemplateMigrationPair(t, "275_swe_lego_template_cache")
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn := taskTemplateMigrationSchema(t, ctx, pool, "template_cache_claim")
	defer conn.Release()

	if _, err := conn.Exec(ctx, `
		CREATE TABLE sandbox_node (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
		CREATE TABLE sandbox_instance (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
	`); err != nil {
		t.Fatalf("create pre-275 schema: %v", err)
	}
	if _, err := conn.Exec(ctx, upSQL); err != nil {
		t.Fatalf("apply 275 up: %v", err)
	}

	var nodeID string
	if err := conn.QueryRow(ctx, `INSERT INTO sandbox_node DEFAULT VALUES RETURNING id`).Scan(&nodeID); err != nil {
		t.Fatalf("seed sandbox node: %v", err)
	}
	nodeUUID := pgtype.UUID{}
	if err := nodeUUID.Scan(nodeID); err != nil {
		t.Fatalf("parse node id: %v", err)
	}
	queries := db.New(conn)
	cacheKey := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	// First claim on an empty key succeeds.
	if _, err := queries.ClaimSweLegoTemplateBuild(ctx, db.ClaimSweLegoTemplateBuildParams{
		NodeID: nodeUUID, CacheKey: cacheKey, ParentTemplateID: "parent-template",
	}); err != nil {
		t.Fatalf("initial claim: %v", err)
	}
	// A fresh building row holds the claim: a concurrent claim gets no row.
	if _, err := queries.ClaimSweLegoTemplateBuild(ctx, db.ClaimSweLegoTemplateBuildParams{
		NodeID: nodeUUID, CacheKey: cacheKey, ParentTemplateID: "parent-template",
	}); err == nil {
		t.Fatal("claim over a fresh building row succeeded")
	}
	// A stale building row (older than the materializer build timeout) is reclaimable.
	if _, err := conn.Exec(ctx, `
		UPDATE swe_lego_template_cache SET updated_at = now() - interval '30 minutes'
		WHERE node_id = $1 AND cache_key = $2
	`, nodeID, cacheKey); err != nil {
		t.Fatalf("backdate building row: %v", err)
	}
	if _, err := queries.ClaimSweLegoTemplateBuild(ctx, db.ClaimSweLegoTemplateBuildParams{
		NodeID: nodeUUID, CacheKey: cacheKey, ParentTemplateID: "parent-template",
	}); err != nil {
		t.Fatalf("stale building row was not reclaimed: %v", err)
	}
	// A failed row is always reclaimable.
	if _, err := conn.Exec(ctx, `
		UPDATE swe_lego_template_cache SET status = 'failed', error = 'boom', updated_at = now()
		WHERE node_id = $1 AND cache_key = $2
	`, nodeID, cacheKey); err != nil {
		t.Fatalf("fail cache row: %v", err)
	}
	if _, err := queries.ClaimSweLegoTemplateBuild(ctx, db.ClaimSweLegoTemplateBuildParams{
		NodeID: nodeUUID, CacheKey: cacheKey, ParentTemplateID: "parent-template",
	}); err != nil {
		t.Fatalf("failed row was not reclaimable: %v", err)
	}
}
