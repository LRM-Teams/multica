package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/migrations"
)

// TestAgentGeneralChannelVisibilityRetiredMigration proves migration 251
// (task #908): before it, agent.visibility='workspace' is required for an
// agent to join a workspace's system general channel — both via the
// sync_system_general_agent trigger and the ensure_system_general_channel
// function. After it, only agent.archived_at IS NULL matters; a private
// agent joins general exactly like a workspace-visible one.
func TestAgentGeneralChannelVisibilityRetiredMigration(t *testing.T) {
	pool := openAgentWakeCutoverDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	files, err := migrations.Files("up")
	if err != nil {
		t.Fatalf("list migrations: %v", err)
	}
	var beforeCut []string
	var cutFile string
	for _, file := range files {
		if migrations.ExtractVersion(file) == "251_agent_general_channel_visibility_retired" {
			cutFile = file
			break
		}
		beforeCut = append(beforeCut, file)
	}
	if cutFile == "" {
		t.Fatal("251_agent_general_channel_visibility_retired migration not found")
	}
	if err := runMigrations(ctx, pool, runOptions{
		Direction:       "up",
		Files:           beforeCut,
		AdvisoryLockKey: int64(rand.Uint64()&0x7fffffffffffffff) | 1,
		Hooks:           preMigrationHooks,
	}); err != nil {
		t.Fatalf("apply migrations before 251: %v", err)
	}

	const (
		ownerID      = "72000000-0000-4000-8000-000000000001"
		workspaceID  = "72000000-0000-4000-8000-000000000002"
		runtimeID    = "72000000-0000-4000-8000-000000000003"
		privateAgent = "72000000-0000-4000-8000-000000000004"
		visibleAgent = "72000000-0000-4000-8000-000000000005"
		lateAgent    = "72000000-0000-4000-8000-000000000006"
	)
	// Inserting the owner's member row fires trg_sync_system_general_human,
	// which auto-creates the workspace's system general channel via
	// ensure_system_general_channel — do not also create it explicitly here,
	// or the two collide on the (workspace_id, system_key) unique constraint.
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO "user" (id, name, email)
		VALUES ('%[1]s', 'General Retired Owner', 'general-retired-owner@example.test');
		INSERT INTO workspace (id, name, slug, description, issue_prefix)
		VALUES ('%[2]s', 'General Retired Workspace', 'general-retired-workspace', 'Migration fixture', 'GRW');
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ('%[2]s', '%[1]s', 'owner');
		INSERT INTO agent_runtime (
			id, workspace_id, daemon_id, name, runtime_mode, provider,
			status, device_info, metadata, last_seen_at
		) VALUES ('%[3]s', '%[2]s', NULL, 'General Retired Runtime', 'cloud', 'general_retired_test',
			'online', 'Migration fixture', '{}'::jsonb, now());
		INSERT INTO agent (
			id, workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id
		) VALUES
			('%[4]s', '%[2]s', 'general-retired-private-agent', '', 'cloud', '{}'::jsonb, '%[3]s', 'private', 1, '%[1]s'),
			('%[5]s', '%[2]s', 'general-retired-visible-agent', '', 'cloud', '{}'::jsonb, '%[3]s', 'workspace', 1, '%[1]s');
	`, ownerID, workspaceID, runtimeID, privateAgent, visibleAgent)); err != nil {
		t.Fatalf("seed pre-251 fixture: %v", err)
	}

	var generalChanID string
	if err := pool.QueryRow(ctx, `
		SELECT id FROM channel WHERE workspace_id = $1 AND system_key = 'general'
	`, workspaceID).Scan(&generalChanID); err != nil {
		t.Fatalf("look up auto-created general channel: %v", err)
	}

	// Before 251: the trigger only reacts to eligible agents — updating the
	// PRIVATE agent's archived_at should NOT touch general membership, since
	// it was never eligible in the first place (visibility='private').
	if _, err := pool.Exec(ctx, `UPDATE agent SET archived_at = now() WHERE id = $1`, privateAgent); err != nil {
		t.Fatalf("archive private agent pre-251: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE agent SET archived_at = NULL WHERE id = $1`, privateAgent); err != nil {
		t.Fatalf("unarchive private agent pre-251: %v", err)
	}
	assertGeneralRosterHasAgent(t, ctx, pool, generalChanID, privateAgent, false)
	assertGeneralRosterHasAgent(t, ctx, pool, generalChanID, visibleAgent, true)

	// Apply 251.
	if err := runMigrations(ctx, pool, runOptions{
		Direction:       "up",
		Files:           []string{cutFile},
		AdvisoryLockKey: int64(rand.Uint64()&0x7fffffffffffffff) | 1,
		Hooks:           preMigrationHooks,
	}); err != nil {
		t.Fatalf("apply 251: %v", err)
	}

	// No backfill: the private agent seeded before 251 stays out of the
	// roster until something re-fires the trigger for it (task #908 —
	// "历史数据不用管").
	assertGeneralRosterHasAgent(t, ctx, pool, generalChanID, privateAgent, false)

	// Trigger path: touching the (still-private, now archived_at-watched
	// only) agent row now makes it eligible regardless of visibility.
	if _, err := pool.Exec(ctx, `UPDATE agent SET archived_at = now() WHERE id = $1`, privateAgent); err != nil {
		t.Fatalf("archive private agent post-251: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE agent SET archived_at = NULL WHERE id = $1`, privateAgent); err != nil {
		t.Fatalf("unarchive private agent post-251: %v", err)
	}
	assertGeneralRosterHasAgent(t, ctx, pool, generalChanID, privateAgent, true)

	// ensure_system_general_channel path: a freshly created private agent is
	// picked up by the full-sync function without needing visibility='workspace'.
	if _, err := pool.Exec(ctx, `
		INSERT INTO agent (
			id, workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id
		) VALUES ($1, $2, 'general-retired-late-private-agent', '', 'cloud', '{}'::jsonb, $3, 'private', 1, $4)
	`, lateAgent, workspaceID, runtimeID, ownerID); err != nil {
		t.Fatalf("seed late private agent: %v", err)
	}
	// The INSERT above already fires the trigger (INSERT is in the trigger's
	// event list), so the late agent should already be in the roster from
	// that alone — confirm, then also confirm ensure_system_general_channel
	// (the lazy full-sync path) is consistent and idempotent.
	assertGeneralRosterHasAgent(t, ctx, pool, generalChanID, lateAgent, true)
	if _, err := pool.Exec(ctx, `SELECT ensure_system_general_channel($1, $2)`, workspaceID, ownerID); err != nil {
		t.Fatalf("ensure_system_general_channel post-251: %v", err)
	}
	assertGeneralRosterHasAgent(t, ctx, pool, generalChanID, lateAgent, true)
	assertGeneralRosterHasAgent(t, ctx, pool, generalChanID, privateAgent, true)
	assertGeneralRosterHasAgent(t, ctx, pool, generalChanID, visibleAgent, true)

	// Archiving still removes membership, regardless of visibility.
	if _, err := pool.Exec(ctx, `UPDATE agent SET archived_at = now() WHERE id = $1`, lateAgent); err != nil {
		t.Fatalf("archive late agent: %v", err)
	}
	assertGeneralRosterHasAgent(t, ctx, pool, generalChanID, lateAgent, false)
}

func assertGeneralRosterHasAgent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, channelID, agentID string, want bool) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM channel_member
		WHERE channel_id = $1 AND member_type = 'agent' AND member_id = $2
	`, channelID, agentID).Scan(&count); err != nil {
		t.Fatalf("query general roster for agent %s: %v", agentID, err)
	}
	got := count > 0
	if got != want {
		t.Fatalf("agent %s in general roster = %v, want %v", agentID, got, want)
	}
}
