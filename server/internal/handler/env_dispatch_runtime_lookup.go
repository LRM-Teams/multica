package handler

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// findOnlineSandboxRuntime resolves the daemon-registered, online Pi runtime
// for an env-dispatch binding by immutable identity (workspace, daemon_id,
// sandbox_instance_id). It returns service.ErrSandboxRuntimeNotOnline when no
// matching online runtime has registered yet, so service.WaitForOnlineSandboxRuntime
// can distinguish "keep polling" from a real query error without the service
// layer depending on pgx.
//
// Implemented via raw SQL rather than the sqlc-generated FindOnlineSandboxRuntime
// method: Task 1 deferred the cross-cutting pkg/db/generated regen (~18 files +
// signature changes) and the generated method is currently absent. This mirrors
// the claimProvisioning raw-SQL pattern in env_dispatch_channel_store.go. When
// the regen lands, this can migrate to the generated query without changing
// callers. Matching uses workspace + daemon_id + sandbox_instance_id + provider
// 'pi' + status 'online'; runtime display names are never used as a binding key.
func findOnlineSandboxRuntime(ctx context.Context, exec db.DBTX, workspaceID, daemonID, sandboxInstanceID string) (service.RuntimeRef, error) {
	const q = `
SELECT id::text, workspace_id::text, daemon_id::text, provider, status,
       metadata->>'sandbox_instance_id' AS sandbox_instance_id
FROM agent_runtime
WHERE workspace_id = $1
  AND provider = 'pi'
  AND daemon_id = $2
  AND status = 'online'
  AND metadata->>'sandbox_instance_id' = $3
LIMIT 1`
	var rt service.RuntimeRef
	err := exec.QueryRow(ctx, q, workspaceID, daemonID, sandboxInstanceID).Scan(
		&rt.ID, &rt.WorkspaceID, &rt.DaemonID, &rt.Provider, &rt.Status, &rt.SandboxInstanceID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return service.RuntimeRef{}, service.ErrSandboxRuntimeNotOnline
		}
		return service.RuntimeRef{}, fmt.Errorf("find online sandbox runtime: %w", err)
	}
	return rt, nil
}

// FindOnlineSandboxRuntime satisfies service.RuntimeLookup for
// envSandboxLifecycleDepsAdapter, so the adapter can be passed to
// service.WaitForOnlineSandboxRuntime during first-address provisioning. It
// resolves the daemon-registered online runtime by immutable identity.
func (a *envSandboxLifecycleDepsAdapter) FindOnlineSandboxRuntime(ctx context.Context, workspaceID, daemonID, sandboxInstanceID string) (service.RuntimeRef, error) {
	return findOnlineSandboxRuntime(ctx, a.h.DB, workspaceID, daemonID, sandboxInstanceID)
}
