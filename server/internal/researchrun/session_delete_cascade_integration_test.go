package researchrun

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Session delete must cascade the integration/dispute rows from migration 350;
// before migration 437 their plain session FKs made DELETE FROM
// research_session fail with a foreign key violation once a V6 run had
// integrated results.
func TestSessionDeleteCascadesIntegrationRows(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	fixture := seedResearchRunFixture(t, ctx, pool)

	roundID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO research_integration_round (
			id, workspace_id, session_id, trigger_kind, input_event_sequence,
			input_state_hash, goal_version, plan_version
		) VALUES ($1::uuid, $2::uuid, $3::uuid, 'manual', 0,
			'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 1, 1)
	`, roundID, fixture.workspaceID, fixture.sessionID); err != nil {
		t.Fatalf("insert integration round: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO research_integration_contribution (
			id, workspace_id, session_id, integration_round_id, author_agent_id, compared_artifact_ids
		) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, '["placeholder"]'::jsonb)
	`, uuid.NewString(), fixture.workspaceID, fixture.sessionID, roundID, fixture.agentID); err != nil {
		t.Fatalf("insert integration contribution: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		DELETE FROM research_session WHERE id = $1::uuid AND workspace_id = $2::uuid
	`, fixture.sessionID, fixture.workspaceID); err != nil {
		t.Fatalf("delete session with integration rows: %v", err)
	}

	var remaining int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM research_integration_round WHERE session_id = $1::uuid
	`, fixture.sessionID).Scan(&remaining); err != nil {
		t.Fatalf("count integration rounds: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("integration rounds remaining after session delete: %d", remaining)
	}
}
