package handler

import (
	"context"

	"github.com/multica-ai/multica/server/internal/daemonws"
)

// dispatchPendingRunnerLaunches is the compatibility call-site used by
// heartbeat and Attachment receipts. All capable Workspace Runners now share
// the same desired-vs-observed reconcile used by ready/setup/reconnect.
func (h *Handler) dispatchPendingRunnerLaunches(ctx context.Context, identity daemonws.ClientIdentity) error {
	return h.reconcileWorkspaceRunnerLaunches(ctx, identity)
}
