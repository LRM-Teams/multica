package main

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestResearchArtifactGrantConcurrentWritersKeepSingleRevisionAndWatermark(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	setupConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire setup connection: %v", err)
	}
	defer setupConn.Release()

	schema := fmt.Sprintf("research_artifact_watermark_concurrency_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err = setupConn.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		if _, cleanupErr := pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE"); cleanupErr != nil {
			t.Logf("drop schema %s: %v", schema, cleanupErr)
		}
	})
	if _, err = setupConn.Exec(ctx, "SET search_path TO "+quotedSchema+", public"); err != nil {
		t.Fatalf("set setup search path: %v", err)
	}
	if _, err = setupConn.Exec(ctx, researchArtifactPassportLegacySchema); err != nil {
		t.Fatalf("create legacy research schema: %v", err)
	}

	workspaceID := "10000000-0000-4000-8000-000000000001"
	sessionID := "20000000-0000-4000-8000-000000000001"
	grantID := "50000000-0000-4000-8000-000000000001"
	userID := "60000000-0000-4000-8000-000000000001"
	if _, err = setupConn.Exec(ctx, `INSERT INTO workspace (id) VALUES ($1::uuid)`, workspaceID); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err = setupConn.Exec(ctx, `INSERT INTO research_session (id, workspace_id) VALUES ($1::uuid, $2::uuid)`, sessionID, workspaceID); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	up318, _ := readMigrationPair(t, "318_research_artifact_passport")
	up319, _ := readMigrationPair(t, "319_research_artifact_passport_backfill")
	up320, _ := readMigrationPair(t, "320_research_artifact_reciprocal_guards")
	up321, _ := readMigrationPair(t, "321_research_artifact_policy_coupling_guards")
	for _, upSQL := range []string{up318, up319, up320, up321} {
		if _, err = setupConn.Exec(ctx, upSQL); err != nil {
			t.Fatalf("apply migration: %v", err)
		}
	}
	if _, err = setupConn.Exec(ctx, `
		INSERT INTO research_artifact_policy_state (workspace_id, session_id, policy_version, watermark)
		VALUES ($1::uuid, $2::uuid, 'legacy-v1-v5-compat-v1', 0)
		ON CONFLICT (workspace_id, session_id) DO UPDATE SET watermark = 0
	`, workspaceID, sessionID); err != nil {
		t.Fatalf("seed policy state: %v", err)
	}

	workerConns := make([]*pgxpool.Conn, 0, 2)
	for i := 0; i < 2; i++ {
		conn, acquireErr := pool.Acquire(ctx)
		if acquireErr != nil {
			t.Fatalf("acquire worker %d: %v", i, acquireErr)
		}
		workerConns = append(workerConns, conn)
		if _, execErr := conn.Exec(ctx, "SET search_path TO "+quotedSchema+", public"); execErr != nil {
			t.Fatalf("set worker %d search path: %v", i, execErr)
		}
	}
	defer func() {
		for _, worker := range workerConns {
			worker.Release()
		}
	}()

	start := make(chan struct{})
	results := make(chan grantWriterResult, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for index, worker := range workerConns {
		go func(index int, conn *pgxpool.Conn) {
			ready.Done()
			<-start
			result := grantWriterResult{index: index}
			tx, beginErr := conn.Begin(ctx)
			if beginErr != nil {
				result.err = beginErr
				results <- result
				return
			}
			defer tx.Rollback(ctx)
			if queryErr := tx.QueryRow(ctx, `
				SELECT research_artifact_reserve_policy_watermark($1::uuid, $2::uuid)
			`, workspaceID, sessionID).Scan(&result.watermark); queryErr != nil {
				result.err = queryErr
				results <- result
				return
			}
			if _, execErr := tx.Exec(ctx, `
				INSERT INTO research_artifact_policy_grant (
				  id, workspace_id, session_id, principal_kind, principal_id, purpose,
				  normal_clearance, revision, status
				) VALUES ($1::uuid, $2::uuid, $3::uuid, 'user', $4::uuid, 'ordinary', 'raw', 1, 'active')
			`, grantID, workspaceID, sessionID, userID); execErr != nil {
				result.err = execErr
				results <- result
				return
			}
			if _, execErr := tx.Exec(ctx, `
				INSERT INTO research_artifact_policy_mutation (
				  workspace_id, session_id, watermark, mutation_kind, policy_grant_id,
				  old_grant_revision, new_grant_revision, new_grant_status
				) VALUES ($1::uuid, $2::uuid, $3, 'grant_create', $4::uuid, 0, 1, 'active')
			`, workspaceID, sessionID, result.watermark, grantID); execErr != nil {
				result.err = execErr
				results <- result
				return
			}
			result.err = tx.Commit(ctx)
			results <- result
		}(index, worker)
	}
	ready.Wait()
	close(start)

	got := []grantWriterResult{<-results, <-results}
	sort.Slice(got, func(i, j int) bool { return got[i].index < got[j].index })
	successes := 0
	for _, result := range got {
		if result.err == nil {
			successes++
			if result.watermark != 1 {
				t.Fatalf("winning writer watermark=%d want=1", result.watermark)
			}
		}
	}
	if successes != 1 {
		t.Fatalf("writer results=%+v want exactly one success", got)
	}

	var finalWatermark int64
	var grantCount, mutationCount int
	if err = setupConn.QueryRow(ctx, `
		SELECT watermark FROM research_artifact_policy_state
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
	`, workspaceID, sessionID).Scan(&finalWatermark); err != nil {
		t.Fatalf("read final watermark: %v", err)
	}
	if err = setupConn.QueryRow(ctx, `
		SELECT count(*)::int FROM research_artifact_policy_grant
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
	`, workspaceID, sessionID, grantID).Scan(&grantCount); err != nil {
		t.Fatalf("count grants: %v", err)
	}
	if err = setupConn.QueryRow(ctx, `
		SELECT count(*)::int FROM research_artifact_policy_mutation
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
		  AND policy_grant_id = $3::uuid AND new_grant_revision = 1
	`, workspaceID, sessionID, grantID).Scan(&mutationCount); err != nil {
		t.Fatalf("count grant mutations: %v", err)
	}
	if finalWatermark != 1 || grantCount != 1 || mutationCount != 1 {
		t.Fatalf("final watermark=%d grants=%d mutations=%d want 1/1/1", finalWatermark, grantCount, mutationCount)
	}
}

type grantWriterResult struct {
	index     int
	watermark int64
	err       error
}
