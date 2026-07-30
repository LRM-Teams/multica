package workgraph

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func createWorkgraphChannel(t *testing.T, ctx context.Context, workspaceID, channelID pgtype.UUID) {
	t.Helper()
	userID := pgUUID(uuid.New())
	runtimeID := pgUUID(uuid.New())
	managerID := pgUUID(uuid.New())
	if _, err := testPool.Exec(ctx, `
		INSERT INTO "user" (id, name, email)
		VALUES ($1, $2, $3)
	`, userID, "wg-user-"+uuid.NewString(), uuid.NewString()+"@example.com"); err != nil {
		t.Fatalf("create channel user: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'member')
	`, workspaceID, userID); err != nil {
		t.Fatalf("add channel user to workspace: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_runtime (
		  id, workspace_id, name, runtime_mode, provider, metadata
		) VALUES (
		  $1, $2, $3, 'local', 'test',
		  '{"capabilities":["reminder_versioned_cache_v1"]}'::jsonb
		)
	`, runtimeID, workspaceID, "wg-runtime-"+uuid.NewString()); err != nil {
		t.Fatalf("create group manager runtime: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent (
		  id, workspace_id, name, display_name, runtime_mode, runtime_config,
		  runtime_id
		, model) VALUES ($1, $2, $3, 'Manager', 'local', '{}'::jsonb, $4, 'composer-1.5')
	`, managerID, workspaceID, "wg-manager-"+uuid.NewString(), runtimeID); err != nil {
		t.Fatalf("create manager agent: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel (
		  id, workspace_id, name, kind, created_by
		) VALUES ($1, $2, $3, 'group', $4)
	`, channelID, workspaceID, "wg-channel-"+uuid.NewString(), userID); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (
		  channel_id, workspace_id, member_type, member_id,
		  role, added_by_type, added_by_id
		) VALUES ($1, $2, 'agent', $3, 'manager', 'user', $4)
	`, channelID, workspaceID, managerID, userID); err != nil {
		t.Fatalf("add manager to channel: %v", err)
	}
}
