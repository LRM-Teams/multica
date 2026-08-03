package handler

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestUpsertAgentRuntime_FirstOwnerWins locks the FE upgrade-alert contract:
// runtime.owner_id must not flip when a different human PAT re-registers the
// same daemon (admin helping install-service, shared workspace login, etc.).
// First non-null owner sticks; later non-null owners are ignored; NULL on
// mdt_ re-register still preserves the existing owner.
func TestUpsertAgentRuntime_FirstOwnerWins(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	workspaceID := parseUUID(testWorkspaceID)
	daemonID := "owner-first-wins-daemon-" + randomID()
	provider := "claude"

	// Two distinct workspace members as candidate owners.
	ownerA := parseUUID(testUserID)
	// Create a second user membership for the steal attempt.
	var ownerB pgtype.UUID
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (email, name)
		VALUES ($1, 'Owner Steal Candidate')
		RETURNING id
	`, "owner-steal-"+randomID()+"@test.local").Scan(&ownerB); err != nil {
		// user table shape may differ — fall back to a random UUID that is
		// still a valid uuid type for the owner_id column (no FK on steal path).
		ownerB = parseUUID("00000000-0000-4000-8000-0000000000bb")
		_ = err
	}

	// First register with owner A.
	row, err := testHandler.Queries.UpsertAgentRuntime(ctx, db.UpsertAgentRuntimeParams{
		WorkspaceID: workspaceID,
		DaemonID:    pgtype.Text{String: daemonID, Valid: true},
		Name:        "owner-first-wins",
		RuntimeMode: "local",
		Provider:    provider,
		Status:      "online",
		DeviceInfo:  "jianghp3",
		Metadata:    []byte("{}"),
		OwnerID:     ownerA,
	})
	if err != nil {
		t.Fatalf("first UpsertAgentRuntime: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, row.ID)
		if ownerB.Valid && uuidToString(ownerB) != "00000000-0000-4000-8000-0000000000bb" {
			testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, ownerB)
		}
	})
	if !row.OwnerID.Valid || uuidToString(row.OwnerID) != uuidToString(ownerA) {
		t.Fatalf("after first register owner_id = %v, want %s", row.OwnerID, uuidToString(ownerA))
	}

	// Second register with owner B (human PAT re-register) must keep A.
	row2, err := testHandler.Queries.UpsertAgentRuntime(ctx, db.UpsertAgentRuntimeParams{
		WorkspaceID: workspaceID,
		DaemonID:    pgtype.Text{String: daemonID, Valid: true},
		Name:        "owner-first-wins-renamed",
		RuntimeMode: "local",
		Provider:    provider,
		Status:      "online",
		DeviceInfo:  "jianghp3",
		Metadata:    []byte("{}"),
		OwnerID:     ownerB,
	})
	if err != nil {
		t.Fatalf("second UpsertAgentRuntime (steal attempt): %v", err)
	}
	if !row2.OwnerID.Valid || uuidToString(row2.OwnerID) != uuidToString(ownerA) {
		t.Fatalf("after steal attempt owner_id = %v, want first owner %s (must not flip)", row2.OwnerID, uuidToString(ownerA))
	}

	// mdt_ path: null owner still preserves A.
	row3, err := testHandler.Queries.UpsertAgentRuntime(ctx, db.UpsertAgentRuntimeParams{
		WorkspaceID: workspaceID,
		DaemonID:    pgtype.Text{String: daemonID, Valid: true},
		Name:        "owner-first-wins-mdt",
		RuntimeMode: "local",
		Provider:    provider,
		Status:      "online",
		DeviceInfo:  "jianghp3",
		Metadata:    []byte("{}"),
		OwnerID:     pgtype.UUID{},
	})
	if err != nil {
		t.Fatalf("third UpsertAgentRuntime (mdt null owner): %v", err)
	}
	if !row3.OwnerID.Valid || uuidToString(row3.OwnerID) != uuidToString(ownerA) {
		t.Fatalf("after mdt re-register owner_id = %v, want first owner %s", row3.OwnerID, uuidToString(ownerA))
	}
}
