package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/multica-ai/multica/server/internal/computer"
)

// bindingStore adapts the computer.BindingStore contract to the
// computer_workspace_bindings table (migration 307) via pgx directly — it
// deliberately bypasses the sqlc-generated layer so this branch does not need
// to regenerate the repo's stale generated code. The execution credential is
// stored only as a SHA-256 hash and never returned in plaintext.
// SQLExecutor is the minimal pgx executor the store needs. It matches the
// repo's dbExecutor / sqlc DBTX shape so either a handler's DB or a raw pgx
// handle satisfies it.
type SQLExecutor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type bindingStore struct{ db SQLExecutor }

// NewBindingStore returns a pgx-backed computer.BindingStore.
func NewBindingStore(db SQLExecutor) computer.BindingStore { return &bindingStore{db: db} }

func hashCredential(cred string) string {
	sum := sha256.Sum256([]byte(cred))
	return hex.EncodeToString(sum[:])
}

const bindingCols = "daemon_id, workspace_id, user_id, execution_token_hash, created_at, revoked_at, active"

func scanBinding(row interface{ Scan(...any) error }) (computer.WorkspaceBinding, error) {
	var (
		b              computer.WorkspaceBinding
		userID         string
		tokenHash      string
		created, revok *time.Time
		active         bool
	)
	if err := row.Scan(&b.ComputerID, &b.WorkspaceID, &userID, &tokenHash, &created, &revok, &active); err != nil {
		return computer.WorkspaceBinding{}, err
	}
	b.Active = active
	b.UserID = userID
	if created != nil {
		b.AcceptedAt = *created
	}
	return b, nil
}

func (s *bindingStore) ListScoped(computerID, userID string) ([]computer.WorkspaceBinding, error) {
	query := "SELECT " + bindingCols + " FROM computer_workspace_bindings WHERE daemon_id=$1 ORDER BY workspace_id"
	args := []any{computerID}
	if userID != "" {
		query = "SELECT " + bindingCols + " FROM computer_workspace_bindings WHERE daemon_id=$1 AND user_id=$2 ORDER BY workspace_id"
		args = append(args, userID)
	}
	rows, err := s.db.Query(context.Background(), query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []computer.WorkspaceBinding
	for rows.Next() {
		b, err := scanBinding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *bindingStore) UpsertScoped(req computer.BindingRequest, b computer.WorkspaceBinding) error {
	var accepted bool
	err := s.db.QueryRow(context.Background(), `
WITH owner AS (
    INSERT INTO computers (id, user_id)
    VALUES ($1, $3)
    ON CONFLICT (id)
    DO UPDATE SET user_id = computers.user_id
    WHERE computers.user_id = EXCLUDED.user_id
    RETURNING id
)
INSERT INTO computer_workspace_bindings (daemon_id, workspace_id, user_id, execution_token_hash, active, revoked_at)
SELECT $1, $2, $3, $4, TRUE, NULL
  FROM owner
ON CONFLICT (daemon_id, workspace_id)
DO UPDATE SET user_id = EXCLUDED.user_id, execution_token_hash = EXCLUDED.execution_token_hash,
              active = TRUE, revoked_at = NULL, created_at = computer_workspace_bindings.created_at
WHERE computer_workspace_bindings.user_id = EXCLUDED.user_id
RETURNING TRUE`, req.TargetComputerID, req.TargetWorkspaceID, req.ActorUserID, hashCredential(b.Credential)).Scan(&accepted)
	if errors.Is(err, pgx.ErrNoRows) {
		return computer.ErrBindingUnauthorized
	}
	return err
}

func (s *bindingStore) RevokeScoped(req computer.BindingRequest) ([]string, error) {
	var revoked bool
	var tokenHashes []string
	err := s.db.QueryRow(context.Background(), `
WITH revoked AS (
    UPDATE computer_workspace_bindings
       SET active = FALSE, revoked_at = now()
     WHERE daemon_id = $1 AND workspace_id = $2 AND user_id = $3 AND active = TRUE
 RETURNING daemon_id, workspace_id
), deleted_tokens AS (
    DELETE FROM daemon_token t
     USING revoked r
     WHERE t.daemon_id = r.daemon_id AND t.workspace_id = r.workspace_id
 RETURNING t.token_hash
)
SELECT EXISTS (SELECT 1 FROM revoked),
	   COALESCE((SELECT array_agg(token_hash) FROM deleted_tokens), ARRAY[]::text[])`, req.TargetComputerID, req.TargetWorkspaceID, req.BindingOwnerUserID).Scan(&revoked, &tokenHashes)
	if err != nil {
		return nil, err
	}
	if !revoked {
		return nil, computer.ErrBindingUnauthorized
	}
	return tokenHashes, nil
}
