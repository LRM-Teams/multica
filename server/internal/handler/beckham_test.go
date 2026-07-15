package handler

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/workgraph"
)

// Phase 0+1: a group channel provisions exactly one Beckham group manager, and
// re-invoking is idempotent (one and only one per group).
func TestEnsureGroupManagerForChannelProvisionsOne(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	var rtID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, owner_id, name, runtime_mode, provider, status, device_info, metadata, last_seen_at)
		VALUES ($1, $2, $3, 'cloud', 'beckham_test', 'online', '', '{}'::jsonb, now())
		RETURNING id
	`, testWorkspaceID, testUserID, "beckham-rt-"+uuid.NewString()).Scan(&rtID); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, rtID) })
	channelID := seedChannelForTest(t, "beckham-"+uuid.NewString(), testUserID)

	agent, created, err := testHandler.EnsureGroupManagerForChannel(ctx, parseUUID(testWorkspaceID), parseUUID(channelID), parseUUID(testUserID))
	if err != nil {
		t.Fatalf("ensure group manager: %v", err)
	}
	if !created {
		t.Fatal("expected a group manager to be created")
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agent.ID) })

	var boundID, memberID pgtype.UUID
	var managedRole, visibility string
	if err := testPool.QueryRow(ctx, `
		SELECT c.group_manager_agent_id, a.managed_role, a.visibility
		FROM channel c JOIN agent a ON a.id = c.group_manager_agent_id
		WHERE c.id = $1
	`, channelID).Scan(&boundID, &managedRole, &visibility); err != nil {
		t.Fatalf("load bound manager: %v", err)
	}
	if uuidToString(boundID) != uuidToString(agent.ID) {
		t.Fatalf("channel bound to %s, want %s", uuidToString(boundID), uuidToString(agent.ID))
	}
	if managedRole != managedRoleGroupManager {
		t.Fatalf("managed_role = %q, want %q", managedRole, managedRoleGroupManager)
	}
	if visibility != "workspace" {
		t.Fatalf("visibility = %q, want workspace", visibility)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT member_id FROM channel_member
		WHERE channel_id = $1 AND member_type = 'agent' AND member_id = $2
	`, channelID, agent.ID).Scan(&memberID); err != nil {
		t.Fatalf("group manager is not a channel member: %v", err)
	}

	// Idempotent: exactly one per group.
	again, created2, err := testHandler.EnsureGroupManagerForChannel(ctx, parseUUID(testWorkspaceID), parseUUID(channelID), parseUUID(testUserID))
	if err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if created2 {
		t.Fatal("second ensure created a duplicate group manager")
	}
	if uuidToString(again.ID) != uuidToString(agent.ID) {
		t.Fatalf("second ensure returned %s, want %s", uuidToString(again.ID), uuidToString(agent.ID))
	}

	// Resolver returns the bound manager.
	resolved, ok := testHandler.resolveGroupManagerForChannel(ctx, parseUUID(testWorkspaceID), parseUUID(channelID))
	if !ok || uuidToString(resolved) != uuidToString(agent.ID) {
		t.Fatalf("resolve returned (%s, %v), want %s", uuidToString(resolved), ok, uuidToString(agent.ID))
	}
}

// Phase 3: a group with no bound Beckham gets NO ambient watch — even if a
// Wendy-named agent is a member. Wendy no longer auto-watches groups; only the
// channel's group manager does.
func TestGroupWithoutBeckhamHasNoAmbientWatch(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	wendyLike := createHandlerTestAgent(t, "Wendy", nil)
	channelID := seedChannelForTest(t, "no-beckham-"+uuid.NewString(), testUserID)
	addRadarAgentMembersForExecutorTest(t, channelID, wendyLike) // member, but NOT the group manager

	prev := workgraph.AmbientDebounce
	workgraph.AmbientDebounce = time.Minute
	t.Cleanup(func() { workgraph.AmbientDebounce = prev })

	testHandler.ingestWendyHumanGroupMessage(ctx, ChannelResponse{
		ID: channelID, WorkspaceID: testWorkspaceID, Kind: "group", Name: "no-beckham",
	}, ChannelMessageResponse{Content: "群里聊两句，看看有没有人来监控"})

	var n int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM wendy_channel_ambient WHERE channel_id = $1`, channelID).Scan(&n); err != nil {
		t.Fatalf("count ambient watch: %v", err)
	}
	if n != 0 {
		t.Fatalf("ambient watches = %d, want 0 (no Beckham bound → nobody watches)", n)
	}
}
