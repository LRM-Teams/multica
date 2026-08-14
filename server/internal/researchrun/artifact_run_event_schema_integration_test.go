package researchrun

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRunEventRelationshipSchemaDiagnostics(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	fixture := seedResearchRunFixture(t, ctx, pool)
	defer cleanupResearchRunFixture(pool, fixture)
	store := NewPostgresStore(pool)
	run, _, err := store.CreateRun(ctx, StartInput{
		WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID, CreatedBy: fixture.userID,
		LeadAgentID: fixture.agentID, Goal: "Run Event schema diagnostics",
		Title: "Event schema", DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatal(err)
	}
	var questionID string
	if err = pool.QueryRow(ctx, `
		SELECT id::text FROM research_question
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid ORDER BY created_at,id LIMIT 1
	`, fixture.workspaceID, run.SessionID).Scan(&questionID); err != nil {
		t.Fatal(err)
	}

	appendFixtureEvent := func(eventType, key string, payload map[string]any) string {
		t.Helper()
		tx, beginErr := pool.Begin(ctx)
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		defer tx.Rollback(ctx)
		event, appendErr := appendEvent(
			ctx, tx, fixture.workspaceID, run.SessionID, eventType, key, "system", "", payload,
		)
		if appendErr != nil {
			t.Fatal(appendErr)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			t.Fatal(commitErr)
		}
		return event.ID
	}
	validID := appendFixtureEvent("control_task_created", "schema-valid", map[string]any{"question_id": questionID})
	malformedID := appendFixtureEvent("control_task_created", "schema-malformed", map[string]any{"question_id": "not-a-uuid"})
	unknownID := appendFixtureEvent("future_unregistered_event", "schema-unknown", map[string]any{})

	for _, tc := range []struct {
		name, eventID, reason string
		want                  int
	}{
		{name: "valid", eventID: validID, want: 0},
		{name: "malformed question", eventID: malformedID, reason: "malformed_uuid", want: 1},
		{name: "unknown schema", eventID: unknownID, reason: "unknown_schema", want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var count int
			if err = pool.QueryRow(ctx, `
				SELECT count(*)::int FROM research_artifact_migration_diagnostic
				WHERE workspace_id=$1::uuid AND session_id=$2::uuid
				  AND owner_kind='run_event' AND owner_id=$3::uuid
				  AND ($4='' OR reason_code=$4)
			`, fixture.workspaceID, run.SessionID, tc.eventID, tc.reason).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != tc.want {
				t.Fatalf("diagnostic count=%d want=%d", count, tc.want)
			}
		})
	}
}
