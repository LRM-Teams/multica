package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestNewCanonicalStateSnapshotNormalizesSectionAndRowOrder(t *testing.T) {
	first, err := NewCanonicalStateSnapshot([]CanonicalStateSection{
		{Name: "tasks", Rows: json.RawMessage(`[{"client_key":"b","payload":{"z":2,"a":1}},{"client_key":"a"}]`)},
		{Name: "run", Rows: json.RawMessage(`[{"status":"running","config":{"b":2,"a":1}}]`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewCanonicalStateSnapshot([]CanonicalStateSection{
		{Name: "run", Rows: json.RawMessage(`[{"config":{"a":1,"b":2},"status":"running"}]`)},
		{Name: "tasks", Rows: json.RawMessage(`[{"client_key":"a"},{"payload":{"a":1,"z":2},"client_key":"b"}]`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	firstHash, err := first.Hash()
	if err != nil {
		t.Fatal(err)
	}
	secondHash, err := second.Hash()
	if err != nil {
		t.Fatal(err)
	}
	if firstHash != secondHash {
		t.Fatalf("hash differs after ordering-only changes: %s != %s", firstHash, secondHash)
	}
	if len(firstHash) != 64 {
		t.Fatalf("hash length=%d want=64", len(firstHash))
	}
}

func TestNewCanonicalStateSnapshotRejectsMalformedOrDuplicateSections(t *testing.T) {
	for _, test := range []struct {
		name     string
		sections []CanonicalStateSection
	}{
		{name: "non array", sections: []CanonicalStateSection{{Name: "run", Rows: json.RawMessage(`{"status":"running"}`)}}},
		{name: "trailing data", sections: []CanonicalStateSection{{Name: "run", Rows: json.RawMessage(`[] {}`)}}},
		{name: "duplicate", sections: []CanonicalStateSection{{Name: "run", Rows: json.RawMessage(`[]`)}, {Name: "run", Rows: json.RawMessage(`[]`)}}},
		{name: "empty name", sections: []CanonicalStateSection{{Rows: json.RawMessage(`[]`)}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewCanonicalStateSnapshot(test.sections); !errors.Is(err, ErrInvalidCanonicalState) {
				t.Fatalf("error=%v want ErrInvalidCanonicalState", err)
			}
		})
	}
}

func TestReplayRunEventsIsIdempotentAndRequiresContinuity(t *testing.T) {
	ctx := context.Background()
	events := []RunEvent{
		{ID: "event-1", Sequence: 1, Type: "run_started", Payload: json.RawMessage(`{"a":1}`)},
		{ID: "event-1", Sequence: 1, Type: "run_started", Payload: json.RawMessage(`{"a":1}`)},
		{ID: "event-2", Sequence: 2, Type: "task_ready", Payload: json.RawMessage(`{"b":2}`)},
	}
	applied := []int64{}
	last, err := ReplayRunEvents(ctx, 0, events, func(_ context.Context, event RunEvent) error {
		applied = append(applied, event.Sequence)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if last != 2 || len(applied) != 2 || applied[0] != 1 || applied[1] != 2 {
		t.Fatalf("last=%d applied=%v", last, applied)
	}

	applied = nil
	last, err = ReplayRunEvents(ctx, 1, events, func(_ context.Context, event RunEvent) error {
		applied = append(applied, event.Sequence)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if last != 2 || len(applied) != 1 || applied[0] != 2 {
		t.Fatalf("resume last=%d applied=%v", last, applied)
	}

	if _, err = ReplayRunEvents(ctx, 1, []RunEvent{{ID: "event-3", Sequence: 3}}, func(context.Context, RunEvent) error { return nil }); !errors.Is(err, ErrRunEventSequenceGap) {
		t.Fatalf("gap error=%v", err)
	}
	if _, err = ReplayRunEvents(ctx, 0, []RunEvent{{ID: "event-a", Sequence: 1}, {ID: "event-b", Sequence: 1}}, func(context.Context, RunEvent) error { return nil }); !errors.Is(err, ErrRunEventSequenceConflict) {
		t.Fatalf("conflict error=%v", err)
	}
}

func TestPostgresCanonicalStateHashAndEventReplay(t *testing.T) {
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
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1::uuid`, fixture.workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1::uuid`, fixture.userID)
	}()
	store := NewPostgresStore(pool)
	input := StartInput{
		SessionID: fixture.sessionID, WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID,
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Hash the canonical run state",
		Title: "Canonical state", DepthTier: "standard", Language: "English",
	}
	if _, _, err = store.InitializeRun(ctx, input, DefaultRunConfig(input.DepthTier)); err != nil {
		t.Fatal(err)
	}

	before, err := store.CanonicalState(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	beforeHash, err := before.Hash()
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err = store.InitializeRun(ctx, input, DefaultRunConfig(input.DepthTier)); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `
		UPDATE research_session
		SET next_reconcile_at = now() + interval '1 hour',
		    reconcile_lease_token = gen_random_uuid(),
		    reconcile_lease_expires_at = now() + interval '1 minute',
		    last_user_activity_at = now() + interval '2 minutes',
		    updated_at = now()
		WHERE id = $1::uuid
	`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `
		UPDATE research_run_event
		SET projection_attempts = projection_attempts + 1,
		    projection_error = 'transient projector error',
		    next_projection_at = now() + interval '1 minute'
		WHERE session_id = $1::uuid
	`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	afterOperationalChange, err := store.CanonicalState(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	afterOperationalHash, err := afterOperationalChange.Hash()
	if err != nil {
		t.Fatal(err)
	}
	if beforeHash != afterOperationalHash {
		t.Fatalf("operational scheduling fields changed canonical hash: %s != %s", beforeHash, afterOperationalHash)
	}

	events, err := store.ListRunEvents(ctx, fixture.sessionID, fixture.workspaceID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	seen := []string{}
	last, err := ReplayRunEvents(ctx, 0, events, func(_ context.Context, event RunEvent) error {
		seen = append(seen, event.Type)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if last == 0 || len(seen) == 0 || seen[0] != "run_started" {
		t.Fatalf("last=%d events=%v", last, seen)
	}

	if _, _, _, err = store.Steer(ctx, SteerInput{
		SessionID: fixture.sessionID, WorkspaceID: fixture.workspaceID, UserID: fixture.userID,
		Goal: "Hash the changed canonical run state", Reason: "verify semantic changes affect the digest",
	}); err != nil {
		t.Fatal(err)
	}
	afterSteer, err := store.CanonicalState(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	afterSteerHash, err := afterSteer.Hash()
	if err != nil {
		t.Fatal(err)
	}
	if beforeHash == afterSteerHash {
		t.Fatal("semantic steering did not change canonical hash")
	}

	if _, err = store.ListRunEvents(ctx, fixture.sessionID, "00000000-0000-0000-0000-000000000000", 0, 100); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("cross-workspace list error=%v want ErrRunNotFound", err)
	}
}
