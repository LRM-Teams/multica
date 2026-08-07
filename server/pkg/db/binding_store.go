package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/multica-ai/multica/server/internal/computer"
	dbgen "github.com/multica-ai/multica/server/pkg/db/generated"
)

// bindingStore adapts the computer.BindingStore contract to the
// computer_workspace_bindings table (migration 307) via pgx directly — it
// deliberately bypasses the sqlc-generated layer so this branch does not need
// to regenerate the repo's stale generated code. The execution credential is
// stored only as a SHA-256 hash and never returned in plaintext.
type bindingStore struct{ db dbgen.DBTX }

// NewBindingStore returns a pgx-backed computer.BindingStore.
func NewBindingStore(db dbgen.DBTX) computer.BindingStore { return &bindingStore{db: db} }

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
	if created != nil {
		b.AcceptedAt = *created
	}
	return b, nil
}

func (s *bindingStore) All() ([]computer.WorkspaceBinding, error) {
	rows, err := s.db.Query(context.Background(), "SELECT "+bindingCols+" FROM computer_workspace_bindings ORDER BY workspace_id")
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

func (s *bindingStore) Get(workspaceID string) (computer.WorkspaceBinding, bool, error) {
	row := s.db.QueryRow(context.Background(),
		"SELECT "+bindingCols+" FROM computer_workspace_bindings WHERE daemon_id=$1 AND workspace_id=$2",
		"", workspaceID)
	b, err := scanBinding(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return computer.WorkspaceBinding{}, false, nil
		}
		return computer.WorkspaceBinding{}, false, err
	}
	return b, true, nil
}

func (s *bindingStore) AddOrRepair(b computer.WorkspaceBinding) error {
	_, err := s.db.Exec(context.Background(), `
INSERT INTO computer_workspace_bindings (daemon_id, workspace_id, user_id, execution_token_hash, active, revoked_at)
VALUES ($1, $2, $3, $4, TRUE, NULL)
ON CONFLICT (daemon_id, workspace_id)
DO UPDATE SET user_id = EXCLUDED.user_id, execution_token_hash = EXCLUDED.execution_token_hash,
              active = TRUE, revoked_at = NULL, created_at = computer_workspace_bindings.created_at`,
		b.ComputerID, b.WorkspaceID, "", hashCredential(b.Credential))
	return err
}

func (s *bindingStore) Remove(workspaceID string) error {
	_, err := s.db.Exec(context.Background(),
		"UPDATE computer_workspace_bindings SET active = FALSE, revoked_at = now() WHERE workspace_id = $1",
		workspaceID)
	return err
}
