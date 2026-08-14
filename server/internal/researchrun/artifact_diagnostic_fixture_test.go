package researchrun

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// seedDiagnosticArtifact creates both reciprocal halves required by diagnostic
// fixtures that intentionally insert legacy-shaped domain rows.
func seedDiagnosticArtifact(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	workspaceID, sessionID, artifactID string,
	kind ArtifactEntityKind,
	domainSQL string,
	domainArgs ...any,
) {
	t.Helper()
	if err := execDiagnosticArtifact(ctx, pool, workspaceID, sessionID, artifactID, kind, domainSQL, domainArgs...); err != nil {
		t.Fatalf("seed %s diagnostic artifact: %v", kind, err)
	}
}

func execDiagnosticArtifact(
	ctx context.Context,
	pool *pgxpool.Pool,
	workspaceID, sessionID, artifactID string,
	kind ArtifactEntityKind,
	domainSQL string,
	domainArgs ...any,
) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, domainSQL, domainArgs...); err != nil {
		return err
	}
	if err = registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{
		WorkspaceID: workspaceID,
		SessionID:   sessionID,
		EntityID:    artifactID,
		Kind:        kind,
		AccessLevel: ArtifactAccessRaw,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
