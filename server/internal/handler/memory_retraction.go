// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
)

// beginMemoryRetractionTx begins the business transaction a delete path uses
// to fence memory sources: tombstone, fence, and quarantine commit together
// or all roll back (Task 8A Step 3).
func (h *Handler) beginMemoryRetractionTx(ctx context.Context) (pgx.Tx, error) {
	if h.TxStarter == nil {
		return nil, fmt.Errorf("memory retraction requires the database pool")
	}
	return h.TxStarter.Begin(ctx)
}

// fenceMemorySourcesTx fences canonical memory sources inside the caller's
// business transaction. Fencing failure fails the whole deletion: no HTTP
// success is returned with an unfenced source left behind.
func (h *Handler) fenceMemorySourcesTx(
	ctx context.Context, tx pgx.Tx, workspaceID pgtype.UUID,
	refs []service.MemorySourceRef, actor, reason string,
) error {
	return service.NewMemoryRetractionService().RetractSourcesTx(ctx, tx, refs, actor, reason)
}

// memorySourceRef builds one canonical ref.
func memorySourceRef(workspaceID pgtype.UUID, kind string, id pgtype.UUID) service.MemorySourceRef {
	return service.MemorySourceRef{WorkspaceID: workspaceID, Kind: kind, ID: id}
}
