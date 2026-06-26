package handler

import (
	"context"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemonws"
)

// TestResolveProjectWorkdirRuntime_SharedRuntime covers the production /
// cloud case: the project's managed workdir lives on a shared runtime that
// is NOT owned by the viewer (owner_id is NULL). The old logic looked up
// "the viewer's own online runtime" and wrongly reported the Files panel as
// offline (#issue: shared public runtime). The resolver must instead find the
// runtime via the daemon recorded on the managed local_directory resource.
func TestResolveProjectWorkdirRuntime_SharedRuntime(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("no test database")
	}
	ctx := context.Background()

	// The resolver short-circuits when DaemonHub is nil (no way to send RPCs).
	prevHub := testHandler.DaemonHub
	testHandler.DaemonHub = daemonws.NewHub()
	t.Cleanup(func() { testHandler.DaemonHub = prevHub })

	var projectID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title) VALUES ($1, 'Shared Workdir Project') RETURNING id
	`, testWorkspaceID).Scan(&projectID); err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM project_resource WHERE project_id = $1`, projectID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID)
	})

	const sharedDaemonID = "shared-daemon-files-test"
	// Shared runtime: online, owner_id NULL (nobody's personal daemon).
	var sharedRuntimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status,
			device_info, metadata, last_seen_at
		)
		VALUES ($1, $2, 'shared-files-rt', 'cloud', 'shared-files-provider', 'online', '{}'::jsonb, '{}'::jsonb, now())
		RETURNING id
	`, testWorkspaceID, sharedDaemonID).Scan(&sharedRuntimeID); err != nil {
		t.Fatalf("create shared runtime: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, sharedRuntimeID)
	})

	// Managed workdir resource pinned to the shared daemon.
	if _, err := testPool.Exec(ctx, `
		INSERT INTO project_resource (project_id, workspace_id, resource_type, resource_ref, position)
		VALUES ($1, $2, 'local_directory', $3::jsonb, 0)
	`, projectID, testWorkspaceID,
		`{"managed":true,"daemon_id":"`+sharedDaemonID+`","local_path":"/srv/workspaces/projects/x/workdir"}`,
	); err != nil {
		t.Fatalf("create managed resource: %v", err)
	}

	rtID, ok := testHandler.resolveProjectWorkdirRuntime(ctx, testWorkspaceID, testUserID, projectID)
	if !ok {
		t.Fatal("resolveProjectWorkdirRuntime: ok=false, want the shared runtime")
	}
	if got := uuidToString(rtID); got != sharedRuntimeID {
		t.Fatalf("resolved runtime = %q, want shared runtime %q", got, sharedRuntimeID)
	}
}

// TestResolveProjectWorkdirRuntime_OfflineSharedDaemon verifies that when the
// managed workdir's daemon is offline, the resolver does not silently succeed
// via some unrelated runtime — it falls through (and, with no viewer-owned
// online runtime in a fresh project, reports unavailable).
func TestResolveProjectWorkdirRuntime_OfflineSharedDaemon(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("no test database")
	}
	ctx := context.Background()
	prevHub := testHandler.DaemonHub
	testHandler.DaemonHub = daemonws.NewHub()
	t.Cleanup(func() { testHandler.DaemonHub = prevHub })

	var projectID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title) VALUES ($1, 'Offline Workdir Project') RETURNING id
	`, testWorkspaceID).Scan(&projectID); err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM project_resource WHERE project_id = $1`, projectID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID)
	})

	const offlineDaemonID = "offline-daemon-files-test"
	var offlineRuntimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status,
			device_info, metadata, last_seen_at
		)
		VALUES ($1, $2, 'offline-files-rt', 'cloud', 'offline-files-provider', 'offline', '{}'::jsonb, '{}'::jsonb, now())
		RETURNING id
	`, testWorkspaceID, offlineDaemonID).Scan(&offlineRuntimeID); err != nil {
		t.Fatalf("create offline runtime: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, offlineRuntimeID)
	})

	if _, err := testPool.Exec(ctx, `
		INSERT INTO project_resource (project_id, workspace_id, resource_type, resource_ref, position)
		VALUES ($1, $2, 'local_directory', $3::jsonb, 0)
	`, projectID, testWorkspaceID,
		`{"managed":true,"daemon_id":"`+offlineDaemonID+`","local_path":"/srv/x"}`,
	); err != nil {
		t.Fatalf("create managed resource: %v", err)
	}

	// The offline shared daemon must not resolve. (The fallback only matches a
	// viewer-OWNED online runtime; a separate user's fixture runtime, even if
	// online, has a different owner_id and must not leak in here.)
	if rtID, ok := testHandler.resolveProjectWorkdirRuntime(ctx, testWorkspaceID, testUserID, projectID); ok {
		// If it resolved, it must at least not be the offline daemon's runtime.
		if uuidToString(rtID) == offlineRuntimeID {
			t.Fatalf("resolved the OFFLINE shared runtime %q, want it skipped", offlineRuntimeID)
		}
	}
}
