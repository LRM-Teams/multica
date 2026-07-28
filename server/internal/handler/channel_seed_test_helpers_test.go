package handler

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// seedOrdinaryGroupWithOwner inserts an ordinary group + human owner in one
// transaction so the deferred owner invariant (237/239) can commit.
// Prefer CreateChannel in product-path tests; use this only for raw SQL fixtures.
func seedOrdinaryGroupWithOwner(t *testing.T, workspaceID, ownerUserID, name string) string {
	t.Helper()
	if testPool == nil {
		t.Fatal("testPool nil")
	}
	if name == "" {
		name = "seed-group-" + uuid.NewString()[:8]
	}
	if workspaceID == "" {
		workspaceID = testWorkspaceID
	}
	if ownerUserID == "" {
		ownerUserID = testUserID
	}
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin seed group: %v", err)
	}
	defer tx.Rollback(ctx)

	var channelID string
	if err := tx.QueryRow(ctx, `
INSERT INTO channel (workspace_id, name, created_by, kind)
VALUES ($1, $2, $3, 'group')
RETURNING id`, workspaceID, name, ownerUserID).Scan(&channelID); err != nil {
		t.Fatalf("seed ordinary channel: %v", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
VALUES ($1, $2, 'user', $3, 'owner')
ON CONFLICT (channel_id, member_type, member_id) DO UPDATE SET role = 'owner'`,
		channelID, workspaceID, ownerUserID); err != nil {
		t.Fatalf("seed ordinary owner: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit seed ordinary group: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID)
	})
	return channelID
}
