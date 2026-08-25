package researchrun

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDirectorRunEventActorPersists(t *testing.T) {
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
	defer cleanupResearchRunFixture(pool, fixture)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	event, err := appendEvent(
		ctx,
		tx,
		fixture.workspaceID,
		fixture.sessionID,
		"v6_director_brief_page_acknowledged",
		"test-v6-director-actor",
		"director",
		fixture.agentID,
		map[string]any{"brief_id": "00000000-0000-0000-0000-000000000001"},
	)
	if err != nil {
		t.Fatalf("append Director Run Event: %v", err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	var actorType, actorID string
	if err = pool.QueryRow(ctx, `
		SELECT actor_type, actor_id::text
		FROM research_run_event
		WHERE id=$1::uuid
	`, event.ID).Scan(&actorType, &actorID); err != nil {
		t.Fatal(err)
	}
	if actorType != "director" || actorID != fixture.agentID {
		t.Fatalf("Director actor=(%q,%q), want (%q,%q)", actorType, actorID, "director", fixture.agentID)
	}
}
