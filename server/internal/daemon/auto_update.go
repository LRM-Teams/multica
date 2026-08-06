package daemon

import (
	"context"
)

// autoUpdateLoop is retained as a compatibility seam for older embeddings,
// but performs no release discovery or mutation. Machine Upgrade is now
// explicit-only: callers must create the canonical operation themselves.
func (d *Daemon) autoUpdateLoop(ctx context.Context) {
	if ctx.Err() == nil && d.logger != nil {
		d.logger.Info("auto-update settings are deprecated and ignored; use an explicit machine upgrade")
	}
}

// tryAutoUpdate remains private compatibility for older tests/in-process
// callers. It is intentionally a no-op: elapsed time must never mutate a
// machine's release state without a canonical Machine Upgrade operation.
func (d *Daemon) tryAutoUpdate(ctx context.Context) {
	_ = ctx
}
