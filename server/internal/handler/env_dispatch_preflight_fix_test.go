package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestReclaimMixedDispatchAReALSessionRemovesExternalSession(t *testing.T) {
	var gotAuth string
	var gotBody struct {
		SessionIDs    []string `json:"session_ids"`
		RemoveSession bool     `json:"remove_session"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/export_trajectories" {
			t.Errorf("cleanup request = %s %s, want POST /export_trajectories", r.Method, r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode cleanup request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"traj":null}`))
	}))
	defer server.Close()

	t.Setenv("AREAL_BRIDGE_STUB_URL", server.URL)
	t.Setenv("AREAL_ADMIN_API_KEY", "admin-cleanup-key")
	if err := reclaimMixedDispatchAReALSession(context.Background(), "fresh-session-1"); err != nil {
		t.Fatalf("reclaim external AReaL session: %v", err)
	}
	if gotAuth != "Bearer admin-cleanup-key" {
		t.Fatalf("cleanup authorization = %q", gotAuth)
	}
	if len(gotBody.SessionIDs) != 1 || gotBody.SessionIDs[0] != "fresh-session-1" || !gotBody.RemoveSession {
		t.Fatalf("cleanup body = %+v, want exact session and remove_session=true", gotBody)
	}
}

// firstSandboxDeleteJobFailDB delegates to the real PostgreSQL pool, persists
// the first sandbox-delete job, then injects a scan failure. The retry reaches
// the real ON CONFLICT/load-active-job path, proving that provisioning cleanup
// is both error-visible and idempotently retryable through the production
// lifecycle adapter.
type firstSandboxDeleteJobFailDB struct {
	inner          dbExecutor
	deleteAttempts int
	instanceID     string
}

func (d *firstSandboxDeleteJobFailDB) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	return d.inner.Exec(ctx, sql, arguments...)
}

func (d *firstSandboxDeleteJobFailDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return d.inner.Query(ctx, sql, args...)
}

func (d *firstSandboxDeleteJobFailDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if strings.Contains(sql, "-- name: CreateSandboxDeleteJob") {
		d.deleteAttempts++
		if len(args) > 3 {
			if instanceID, ok := args[3].(pgtype.UUID); ok {
				d.instanceID = uuidToString(instanceID)
			}
		}
		row := d.inner.QueryRow(ctx, sql, args...)
		if d.deleteAttempts == 1 {
			return scanThenFailRow{inner: row, err: errors.New("injected training sandbox delete failure")}
		}
		return row
	}
	return d.inner.QueryRow(ctx, sql, args...)
}

type scanThenFailRow struct {
	inner pgx.Row
	err   error
}

func (row scanThenFailRow) Scan(dest ...any) error {
	if err := row.inner.Scan(dest...); err != nil {
		return err
	}
	return row.err
}

func TestOnlineProvisionFailureSurfacesAndRetriesSandboxCleanup(t *testing.T) {
	fx := setupSharedChannelFixture(t)
	ctx := context.Background()

	// Lifecycle creation requires a currently available node. Reuse the
	// fixture's node while keeping this test's newly created sandbox distinct.
	var nodeID string
	if err := testPool.QueryRow(ctx, `
		UPDATE sandbox_node
		SET status = 'online', last_seen_at = now()
		WHERE id = (SELECT node_id FROM sandbox_instance WHERE id = $1)
		RETURNING id::text`, fx.sandboxInstanceID).Scan(&nodeID); err != nil {
		t.Fatalf("make sandbox node available: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO sandbox_workspace_binding (workspace_id, node_id, enabled, policy, created_by)
		VALUES ($1, $2, true, '{}'::jsonb, $3)
		ON CONFLICT (workspace_id, node_id) DO UPDATE SET enabled = true, updated_at = now()`,
		testWorkspaceID, nodeID, testUserID); err != nil {
		t.Fatalf("bind sandbox node to workspace: %v", err)
	}

	var removedSessions int
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/rl/start_session":
			_, _ = w.Write([]byte(`{"session_id":"cleanup-session","api_key":"cleanup-proxy-key"}`))
		case "/export_trajectories":
			removedSessions++
			_, _ = w.Write([]byte(`{"traj":null}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer bridge.Close()
	t.Setenv("AREAL_BRIDGE_STUB_URL", bridge.URL)
	t.Setenv("AREAL_PROXY_URL", bridge.URL+"/v1")
	t.Setenv("AREAL_ADMIN_API_KEY", "cleanup-admin-key")
	t.Setenv("MULTICA_PUBLIC_URL", "http://multica.invalid")

	faultDB := &firstSandboxDeleteJobFailDB{inner: testPool}
	isolated := *testHandler
	isolated.DB = faultDB
	isolated.Queries = db.New(faultDB)

	input := fx.provisionInput(fx.memberAID)
	input.TrainingMode = "online_rl"
	input.TargetPolicy = "target-policy"
	input.Tokenizer = "target-tokenizer"
	provisionCtx, cancel := context.WithTimeout(ctx, 350*time.Millisecond)
	defer cancel()
	_, err := isolated.provisionEnvDispatchAgent(provisionCtx, input)
	if err == nil {
		t.Fatal("online provision unexpectedly succeeded without a registered runtime")
	}
	if !strings.Contains(err.Error(), "runtime readiness timeout") || !strings.Contains(err.Error(), "injected training sandbox delete failure") {
		t.Fatalf("online provision error = %v, want initiating readiness and sandbox cleanup failures", err)
	}
	if faultDB.deleteAttempts != 2 {
		t.Fatalf("sandbox delete attempts = %d, want one failed attempt plus one idempotent retry", faultDB.deleteAttempts)
	}
	if faultDB.instanceID == "" {
		t.Fatal("sandbox delete retry did not retain the created instance identity")
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM sandbox_job WHERE instance_id = $1`, faultDB.instanceID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM sandbox_instance WHERE id = $1`, faultDB.instanceID)
	})

	var deleteJobs int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM sandbox_job
		WHERE instance_id = $1 AND type = 'delete' AND status = 'queued'`, faultDB.instanceID).Scan(&deleteJobs); err != nil {
		t.Fatalf("count durable sandbox delete jobs: %v", err)
	}
	if deleteJobs != 1 {
		t.Fatalf("durable sandbox delete jobs = %d, want exactly one after retry", deleteJobs)
	}
	var bindingStatus string
	if err := testPool.QueryRow(ctx, `
		SELECT status FROM environment_agent_sandbox
		WHERE env_id = $1 AND agent_id = $2`, fx.envID, fx.memberAID).Scan(&bindingStatus); err != nil {
		t.Fatalf("load failed online binding: %v", err)
	}
	if bindingStatus != "failed_retryable" {
		t.Fatalf("online binding status = %q, want failed_retryable", bindingStatus)
	}
	if removedSessions != 1 {
		t.Fatalf("AReaL session cleanup calls = %d, want 1", removedSessions)
	}
}
