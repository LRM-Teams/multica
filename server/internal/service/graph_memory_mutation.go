package service

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// GraphMutationCoordinator serializes daily updates, consolidation, version
// switching, and GC per physical graph via a PostgreSQL advisory lock keyed
// by graph identity (spec §9.3). Recall never takes this lock.
type GraphMutationCoordinator struct {
	pool *pgxpool.Pool
}

func NewGraphMutationCoordinator(pool *pgxpool.Pool) *GraphMutationCoordinator {
	return &GraphMutationCoordinator{pool: pool}
}

// WithGraphLock runs fn holding the transaction-scoped advisory lock for
// one graph. Lock identity is the canonical graph triple.
func (c *GraphMutationCoordinator) WithGraphLock(ctx context.Context, workspaceID, kind, ownerID string, fn func(ctx context.Context) error) error {
	if c.pool == nil {
		return fmt.Errorf("graph_store_unavailable: mutation coordinator requires a database pool")
	}
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	key := fmt.Sprintf("graph:%s:%s:%s", workspaceID, kind, ownerID)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, key); err != nil {
		return fmt.Errorf("graph_mutation_busy: %w", err)
	}
	if err := fn(ctx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
