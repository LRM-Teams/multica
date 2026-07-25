package handler

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
)

// lockDaemonRegistration serializes daemon registration with permanent
// computer removal for one workspace-local daemon identity. PostgreSQL holds
// the advisory lock until tx commits or rolls back.
func lockDaemonRegistration(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID string,
	daemonID string,
) error {
	key := strings.TrimSpace(workspaceID) + ":" + strings.ToLower(strings.TrimSpace(daemonID))
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key)
	return err
}
