// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Channel ↔ Project binding service (plan Task 16, spec §12): the single
// production writer of channel.project_id. Every binding change runs as one
// PostgreSQL transaction that locks the channel row, verifies the expected
// old binding, captures the source watermark, CAS-advances the binding
// generation, switches the route's single write owner, updates
// channel.project_id, and records the migration the copy worker will drain.
// The migration-470 guard trigger rejects any UPDATE that skips this path.

// ErrChannelBindingConflict is the CAS mismatch: the channel's actual old
// binding differs from what the caller expected (a concurrent rebind won).
var ErrChannelBindingConflict = errors.New("channel project binding conflict")

// ChannelProjectBindingParams is one service-mediated binding change.
// NewProjectID.Valid=false unbinds. ExpectedOldProjectID is the CAS
// expectation; when ExpectedOldSet is false the current binding is taken
// as-is.
type ChannelProjectBindingParams struct {
	WorkspaceID          pgtype.UUID
	ChannelID            pgtype.UUID
	NewProjectID         pgtype.UUID
	ExpectedOldProjectID pgtype.UUID
	ExpectedOldSet       bool
	Actor                string
}

// ChannelProjectBindingResult reports what one binding change did.
type ChannelProjectBindingResult struct {
	Generation       int64 // 0 when the request was a no-op
	OldProjectID     pgtype.UUID
	NewProjectID     pgtype.UUID
	Route            GraphRouteResolution
	RouteOwnerMoved  bool // the write owner changed: a migration was queued
	SourceWatermark  int64
	MigrationPending bool
}

type ChannelProjectBindingService struct {
	pool *pgxpool.Pool
}

func NewChannelProjectBindingService(pool *pgxpool.Pool) *ChannelProjectBindingService {
	return &ChannelProjectBindingService{pool: pool}
}

// SetChannelProject runs one binding change in its own transaction.
func (s *ChannelProjectBindingService) SetChannelProject(ctx context.Context, p ChannelProjectBindingParams) (ChannelProjectBindingResult, error) {
	if s == nil || s.pool == nil {
		return ChannelProjectBindingResult{}, errors.New("channel project binding service not configured")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ChannelProjectBindingResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := s.SetChannelProjectTx(ctx, tx, p)
	if err != nil {
		return ChannelProjectBindingResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ChannelProjectBindingResult{}, err
	}
	return result, nil
}

// SetChannelProjectTx runs the binding change inside a caller-owned
// transaction (the goal bootstrap composes it with its own project and
// resource writes). The channel row is locked here; a caller that already
// holds the lock keeps it (same transaction).
func (s *ChannelProjectBindingService) SetChannelProjectTx(ctx context.Context, tx pgx.Tx, p ChannelProjectBindingParams) (ChannelProjectBindingResult, error) {
	if !p.WorkspaceID.Valid || !p.ChannelID.Valid {
		return ChannelProjectBindingResult{}, errors.New("channel project binding requires workspace and channel")
	}
	q := db.New(tx)

	oldBinding, err := q.GetGraphMemoryChannelBindingForUpdate(ctx, db.GetGraphMemoryChannelBindingForUpdateParams{
		ID: p.ChannelID, WorkspaceID: p.WorkspaceID,
	})
	if err != nil {
		return ChannelProjectBindingResult{}, errors.New("channel not found for binding")
	}
	if p.ExpectedOldSet && p.ExpectedOldProjectID != oldBinding {
		return ChannelProjectBindingResult{}, ErrChannelBindingConflict
	}
	sameBinding := p.NewProjectID == oldBinding
	if sameBinding {
		// A re-save of the identical binding consumes nothing.
		return ChannelProjectBindingResult{OldProjectID: oldBinding, NewProjectID: p.NewProjectID}, nil
	}

	// Route first (pure transition against the NEW binding), then the
	// binding row (the guard trigger's same-transaction marker), then the
	// channel UPDATE the row authorizes, all before commit.
	route, err := resolveChannelRouteLocked(ctx, q, p.WorkspaceID, p.ChannelID, util.UUIDToString(p.NewProjectID))
	if err != nil {
		return ChannelProjectBindingResult{}, err
	}
	ownerUUID, err := util.ParseUUID(route.GraphOwnerID)
	if err != nil {
		return ChannelProjectBindingResult{}, err
	}
	watermark, err := q.GraphMemoryChannelAtomWatermark(ctx, db.GraphMemoryChannelAtomWatermarkParams{
		WorkspaceID: p.WorkspaceID, ChannelID: p.ChannelID,
	})
	if err != nil {
		return ChannelProjectBindingResult{}, err
	}
	generation, err := q.MaxGraphMemoryChannelBindingGeneration(ctx, p.ChannelID)
	if err != nil {
		return ChannelProjectBindingResult{}, err
	}
	generation++
	if _, err := q.InsertGraphMemoryChannelBinding(ctx, db.InsertGraphMemoryChannelBindingParams{
		WorkspaceID: p.WorkspaceID, ChannelID: p.ChannelID, Generation: generation,
		OldProjectID: oldBinding, NewProjectID: p.NewProjectID,
		RouteKind: route.GraphKind, RouteOwnerID: ownerUUID, RouteGeneration: route.Generation,
		SourceWatermark: watermark, Actor: p.Actor,
	}); err != nil {
		return ChannelProjectBindingResult{}, err
	}

	// The authorized UPDATE — the migration-470 guard matches the binding
	// row just inserted in this transaction.
	if _, err := tx.Exec(ctx, `
		UPDATE channel SET project_id = $1, updated_at = now()
		WHERE id = $2 AND workspace_id = $3`,
		p.NewProjectID, p.ChannelID, p.WorkspaceID); err != nil {
		return ChannelProjectBindingResult{}, err
	}

	// A migration is queued whenever the route's write owner actually
	// moved and there is channel-owned content to carry over.
	ownerMoved := routeOwnerMoved(route, oldBinding, p.ChannelID)
	pending := false
	if ownerMoved && watermark > 0 {
		if err := q.InsertGraphMemoryChannelMigrationState(ctx, db.InsertGraphMemoryChannelMigrationStateParams{
			WorkspaceID: p.WorkspaceID, ChannelID: p.ChannelID,
			BindingGeneration: generation, SourceWatermark: watermark,
		}); err != nil {
			return ChannelProjectBindingResult{}, err
		}
		pending = true
	}
	return ChannelProjectBindingResult{
		Generation:       generation,
		OldProjectID:     oldBinding,
		NewProjectID:     p.NewProjectID,
		Route:            route,
		RouteOwnerMoved:  ownerMoved,
		SourceWatermark:  watermark,
		MigrationPending: pending,
	}, nil
}

// routeOwnerMoved reports whether the binding change actually switched the
// route's write owner: the transition table only moves owners on lineage
// changes (standalone routes are permanent, spec §4.2), so a standalone
// channel binding a project keeps its channel graph.
func routeOwnerMoved(route GraphRouteResolution, oldBinding pgtype.UUID, channelID pgtype.UUID) bool {
	if route.RoutingMode == "standalone" {
		return false
	}
	if route.GraphKind == "project" {
		owner, err := util.ParseUUID(route.GraphOwnerID)
		if err != nil {
			return false
		}
		return owner != oldBinding
	}
	// Temporary channel-owned generation after an unbind: the owner is the
	// channel itself; it moved only if the old graph was a project's.
	return oldBinding.Valid
}
