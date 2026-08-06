package service

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrSandboxRuntimeNotOnline signals that no matching online sandbox runtime
// has registered yet. WaitForOnlineSandboxRuntime treats it as "keep polling"
// (distinct from a real query error) so the service layer stays decoupled from
// the pgx driver: the adapter translates pgx.ErrNoRows to this sentinel when
// wired to the FindOnlineSandboxRuntime query.
var ErrSandboxRuntimeNotOnline = errors.New("sandbox runtime not online yet")

// RuntimeRef is the resolved identity of a daemon-registered, online agent
// runtime discovered for an env-dispatch sandbox. It carries the immutable
// identity fields provisioning verifies before binding a derived agent. A
// runtime is matched by workspace, provider, daemon_id, and sandbox_instance_id
// - never by mutable display name.
type RuntimeRef struct {
	ID                string
	WorkspaceID       string
	Provider          string
	DaemonID          string
	SandboxInstanceID string
	Status            string
}

// RuntimeLookup resolves a daemon-registered runtime by immutable identity
// (workspace, daemon_id, sandbox_instance_id). It returns ErrSandboxRuntimeNotOnline
// when no matching online runtime has registered yet. The production adapter is
// wired to the FindOnlineSandboxRuntime sqlc query once the env-dispatch branch
// regenerates generated/ (deferred cross-cutting sqlc sync); until then this
// interface lets the discovery logic be built and tested in isolation.
type RuntimeLookup interface {
	FindOnlineSandboxRuntime(ctx context.Context, workspaceID, daemonID, sandboxInstanceID string) (RuntimeRef, error)
}

// WaitForOnlineSandboxRuntime polls until a Pi runtime registered by the sandbox
// daemon reports online with the binding's exact workspace, daemon_id, and
// sandbox_instance_id, then returns it. It fails closed: a runtime whose
// identity does not match all of workspace / provider=pi / daemon_id /
// sandbox_instance_id / status=online is rejected as a mismatch and never bound
// by display name. A non-ErrSandboxRuntimeNotOnline query error surfaces
// immediately. On deadline it returns a sanitized "runtime readiness timeout"
// so the caller can compensate owned resources instead of leaving the DAG
// indefinitely in progress. No secret ever enters this path, so errors are safe
// to return verbatim.
//
// See openspec change env-dispatch-agent-runtime-config Task 3 (runtime
// discovery and readiness). Polling is bounded by timeout; the 100ms interval
// bounds poll frequency so a tight deadline still resolves promptly.
func WaitForOnlineSandboxRuntime(ctx context.Context, q RuntimeLookup, workspaceID, daemonID, sandboxInstanceID string, timeout time.Duration) (RuntimeRef, error) {
	deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		rt, err := q.FindOnlineSandboxRuntime(deadlineCtx, workspaceID, daemonID, sandboxInstanceID)
		if err == nil {
			if rt.WorkspaceID != workspaceID ||
				rt.Provider != "pi" ||
				rt.DaemonID != daemonID ||
				rt.SandboxInstanceID != sandboxInstanceID ||
				rt.Status != "online" {
				return RuntimeRef{}, fmt.Errorf("runtime identity mismatch")
			}
			return rt, nil
		}
		if !errors.Is(err, ErrSandboxRuntimeNotOnline) {
			return RuntimeRef{}, fmt.Errorf("resolve sandbox runtime: %w", err)
		}
		select {
		case <-deadlineCtx.Done():
			return RuntimeRef{}, fmt.Errorf("runtime readiness timeout")
		case <-time.After(100 * time.Millisecond):
		}
	}
}
