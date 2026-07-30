package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	generalWorkspaceA = "10000000-0000-0000-0000-000000000001"
	generalWorkspaceB = "10000000-0000-0000-0000-000000000002"
	generalOwnerA     = "20000000-0000-0000-0000-000000000001"
	generalHumanA     = "20000000-0000-0000-0000-000000000002"
	generalOwnerB     = "20000000-0000-0000-0000-000000000003"
	generalVisibleA   = "30000000-0000-0000-0000-000000000001"
	generalPrivateA   = "30000000-0000-0000-0000-000000000002"
	generalArchivedA  = "30000000-0000-0000-0000-000000000003"
	generalCollisionA = "11111111-0000-0000-0000-000000000001"
	generalCollisionB = "22222222-0000-0000-0000-000000000002"
	generalLegacyUser = "20000000-0000-0000-0000-000000000005"
	generalLegacyWS   = "10000000-0000-0000-0000-000000000003"
)

type systemGeneralFixture struct {
	pool      *pgxpool.Pool
	conn      *pgxpool.Conn
	schema    string
	quoted    string
	upSQL     string
	downSQL   string
	ctx       context.Context
	ctxCancel context.CancelFunc
}

func newSystemGeneralFixture(t *testing.T) *systemGeneralFixture {
	t.Helper()
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	conn, err := pool.Acquire(ctx)
	if err != nil {
		cancel()
		t.Fatalf("acquire connection: %v", err)
	}

	schema := fmt.Sprintf("system_general_migration_test_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		conn.Release()
		cancel()
		t.Fatalf("create schema: %v", err)
	}
	if _, err := conn.Exec(ctx, "SET search_path TO "+quoted+", public"); err != nil {
		conn.Release()
		cancel()
		t.Fatalf("set search path: %v", err)
	}

	fixture := &systemGeneralFixture{
		pool:      pool,
		conn:      conn,
		schema:    schema,
		quoted:    quoted,
		ctx:       ctx,
		ctxCancel: cancel,
	}
	fixture.upSQL, fixture.downSQL = readSystemGeneralMigrationSQL(t)
	fixture.createSchema(t)
	t.Cleanup(func() {
		// A fail-closed migration deliberately leaves its explicit transaction
		// aborted. Always clear it before returning this pooled connection.
		_, _ = conn.Exec(context.Background(), "ROLLBACK")
		conn.Release()
		cancel()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, err := pool.Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+quoted+" CASCADE"); err != nil {
			t.Logf("drop schema %s: %v", schema, err)
		}
	})
	return fixture
}

func readSystemGeneralMigrationSQL(t *testing.T) (string, string) {
	t.Helper()
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(testFile), "..", "..", "migrations")
	read := func(name string) string {
		body, err := os.ReadFile(filepath.Join(migrationsDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(body)
	}
	return read("204_system_general_channel.up.sql"), read("204_system_general_channel.down.sql")
}

func (fixture *systemGeneralFixture) createSchema(t *testing.T) {
	t.Helper()
	_, err := fixture.conn.Exec(fixture.ctx, `
		CREATE TABLE "user" (
			id UUID PRIMARY KEY,
			name TEXT NOT NULL,
			email TEXT NOT NULL UNIQUE
		);
		CREATE TABLE workspace (
			id UUID PRIMARY KEY,
			name TEXT NOT NULL,
			slug TEXT NOT NULL UNIQUE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
		CREATE TABLE member (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
			user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
			role TEXT NOT NULL CHECK (role IN ('owner', 'admin', 'member')),
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE (workspace_id, user_id)
		);
		CREATE TABLE agent (
			id UUID PRIMARY KEY,
			workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
			visibility TEXT NOT NULL CHECK (visibility IN ('workspace', 'private')),
			archived_at TIMESTAMPTZ
		);
		CREATE TABLE channel (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			description TEXT,
			lark_chat_id TEXT,
			created_by UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			project_id UUID,
			kind TEXT NOT NULL DEFAULT 'group' CHECK (kind IN ('group', 'dm')),
			archived_at TIMESTAMPTZ,
			archived_by UUID,
			group_manager_agent_id UUID,
			UNIQUE (workspace_id, name)
		);
		CREATE TABLE channel_member (
			channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
			workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
			member_type TEXT NOT NULL CHECK (member_type IN ('user', 'agent')),
			member_id UUID NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			pinned_at TIMESTAMPTZ,
			manual_unread_at TIMESTAMPTZ,
			muted_at TIMESTAMPTZ,
			PRIMARY KEY (channel_id, member_type, member_id)
		);
		CREATE TABLE channel_message (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
			workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
			content TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE conversation (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
			kind TEXT NOT NULL,
			channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE UNIQUE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
		CREATE TABLE conversation_member (
			conversation_id UUID NOT NULL REFERENCES conversation(id) ON DELETE CASCADE,
			workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
			member_type TEXT NOT NULL,
			member_id UUID NOT NULL,
			wake_state TEXT NOT NULL DEFAULT 'active',
			followed_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			PRIMARY KEY (conversation_id, member_type, member_id)
		);
		CREATE TABLE agent_radar_run (
			id UUID PRIMARY KEY,
			trigger_kind TEXT,
			cooldown_key TEXT,
			status TEXT
		);
		CREATE TABLE agent_radar_action (
			id UUID PRIMARY KEY,
			radar_run_id UUID REFERENCES agent_radar_run(id),
			workspace_id UUID,
			target_id UUID,
			action_type TEXT,
			status TEXT
		);
		CREATE TABLE radar_call (count BIGINT NOT NULL DEFAULT 0);
		INSERT INTO radar_call DEFAULT VALUES;

		CREATE FUNCTION record_workspace_radar_change(UUID, TEXT, UUID, TIMESTAMPTZ, TEXT, UUID, JSONB)
		RETURNS void LANGUAGE plpgsql AS $$
		BEGIN
			UPDATE radar_call SET count = count + 1;
		END;
		$$;
		CREATE FUNCTION journal_workspace_radar_channel()
		RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			PERFORM record_workspace_radar_change(
				COALESCE(NEW.workspace_id, OLD.workspace_id), 'group_channel',
				COALESCE(NEW.id, OLD.id), clock_timestamp(), 'channel',
				COALESCE(NEW.id, OLD.id), '{}'::jsonb
			);
			RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
		END;
		$$;
		CREATE TRIGGER trg_journal_workspace_radar_channel
		AFTER INSERT OR UPDATE OR DELETE ON channel
		FOR EACH ROW EXECUTE FUNCTION journal_workspace_radar_channel();

		CREATE FUNCTION ensure_channel_conversation()
		RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			INSERT INTO conversation (id, workspace_id, kind, channel_id, created_at, updated_at)
			VALUES (NEW.id, NEW.workspace_id, NEW.kind, NEW.id, NEW.created_at, NEW.updated_at);
			RETURN NEW;
		END;
		$$;
		CREATE TRIGGER trg_channel_conversation
		AFTER INSERT ON channel
		FOR EACH ROW EXECUTE FUNCTION ensure_channel_conversation();

		CREATE FUNCTION activate_conversation_member()
		RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			INSERT INTO conversation_member (
				conversation_id, workspace_id, member_type, member_id, wake_state, followed_at,
				created_at, updated_at
			)
			SELECT id, NEW.workspace_id, NEW.member_type, NEW.member_id, 'active',
				CASE WHEN NEW.member_type = 'user' THEN NEW.created_at ELSE NULL END,
				NEW.created_at, now()
			FROM conversation WHERE channel_id = NEW.channel_id
			ON CONFLICT (conversation_id, member_type, member_id) DO UPDATE
			SET wake_state = 'active', updated_at = now();
			RETURN NEW;
		END;
		$$;
		CREATE FUNCTION remove_conversation_member()
		RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			UPDATE conversation_member cm
			SET wake_state = 'removed', followed_at = NULL, updated_at = now()
			FROM conversation c
			WHERE c.channel_id = OLD.channel_id
			  AND cm.conversation_id = c.id
			  AND cm.member_type = OLD.member_type
			  AND cm.member_id = OLD.member_id;
			RETURN OLD;
		END;
		$$;
		CREATE TRIGGER trg_channel_member_conversation_activate
		AFTER INSERT ON channel_member
		FOR EACH ROW EXECUTE FUNCTION activate_conversation_member();
		CREATE TRIGGER trg_channel_member_conversation_remove
		AFTER DELETE ON channel_member
		FOR EACH ROW EXECUTE FUNCTION remove_conversation_member();
	`)
	if err != nil {
		t.Fatalf("create migration prerequisites: %v", err)
	}
}

func (fixture *systemGeneralFixture) apply(t *testing.T, sql, operation string) {
	t.Helper()
	if _, err := fixture.conn.Exec(fixture.ctx, sql); err != nil {
		_, _ = fixture.conn.Exec(context.Background(), "ROLLBACK")
		t.Fatalf("%s: %v", operation, err)
	}
}

func (fixture *systemGeneralFixture) applyWantError(t *testing.T, sql, message string) {
	t.Helper()
	_, err := fixture.conn.Exec(fixture.ctx, sql)
	if err == nil {
		t.Fatalf("migration unexpectedly succeeded; want %s", message)
	}
	if !strings.Contains(err.Error(), message) || !strings.Contains(err.Error(), "SQLSTATE P0001") {
		t.Fatalf("migration error = %v, want P0001 %s", err, message)
	}
	if _, rollbackErr := fixture.conn.Exec(context.Background(), "ROLLBACK"); rollbackErr != nil {
		t.Fatalf("rollback failed migration: %v", rollbackErr)
	}
}

func seedSystemGeneralUsers(t *testing.T, fixture *systemGeneralFixture) {
	t.Helper()
	if _, err := fixture.conn.Exec(fixture.ctx, `
		INSERT INTO "user" (id, name, email) VALUES
			($1, 'Owner A', 'owner-a@example.test'),
			($2, 'Human A', 'human-a@example.test'),
			($3, 'Owner B', 'owner-b@example.test')
	`, generalOwnerA, generalHumanA, generalOwnerB); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	if _, err := fixture.conn.Exec(fixture.ctx, `
		INSERT INTO workspace (id, name, slug) VALUES
			($1, 'Workspace A', 'workspace-a'),
			($2, 'Workspace B', 'workspace-b')
	`, generalWorkspaceA, generalWorkspaceB); err != nil {
		t.Fatalf("seed workspaces: %v", err)
	}
	if _, err := fixture.conn.Exec(fixture.ctx, `
		INSERT INTO member (workspace_id, user_id, role) VALUES
			($1, $3, 'owner'), ($1, $4, 'member'), ($2, $5, 'owner')
	`, generalWorkspaceA, generalWorkspaceB, generalOwnerA, generalHumanA, generalOwnerB); err != nil {
		t.Fatalf("seed members: %v", err)
	}
}

func TestSystemGeneralMigration204FailsClosedOnActiveCollision(t *testing.T) {
	fixture := newSystemGeneralFixture(t)
	seedSystemGeneralUsers(t, fixture)
	if _, err := fixture.conn.Exec(fixture.ctx, `
		INSERT INTO channel (workspace_id, name, created_by, kind)
		VALUES ($1, 'general', $2, 'group')
	`, generalWorkspaceA, generalOwnerA); err != nil {
		t.Fatalf("seed active collision: %v", err)
	}

	fixture.applyWantError(t, fixture.upSQL, "system_general_active_name_collision")

	var systemKeyExists, triggerEnabled bool
	if err := fixture.conn.QueryRow(fixture.ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema()
			  AND table_name = 'channel'
			  AND column_name = 'system_key'
		)
	`).Scan(&systemKeyExists); err != nil {
		t.Fatalf("check rolled-back column: %v", err)
	}
	if systemKeyExists {
		t.Fatal("active collision left a partial system_key column")
	}
	if err := fixture.conn.QueryRow(fixture.ctx, `
		SELECT tgenabled = 'O'
		FROM pg_trigger
		WHERE tgrelid = 'channel'::regclass
		  AND tgname = 'trg_journal_workspace_radar_channel'
	`).Scan(&triggerEnabled); err != nil || !triggerEnabled {
		t.Fatalf("radar trigger enabled after rollback = %v, err=%v", triggerEnabled, err)
	}
}

func TestSystemGeneralMigration204PreservesCollisionsAndMaintainsRoster(t *testing.T) {
	fixture := newSystemGeneralFixture(t)
	seedSystemGeneralUsers(t, fixture)
	archivedAt := time.Date(2026, 7, 20, 10, 11, 12, 0, time.UTC)
	updatedAt := time.Date(2026, 7, 20, 11, 12, 13, 0, time.UTC)

	if _, err := fixture.conn.Exec(fixture.ctx, `
		INSERT INTO agent (id, workspace_id, visibility, archived_at) VALUES
			($1, $4, 'workspace', NULL),
			($2, $4, 'private', NULL),
			($3, $4, 'workspace', now())
	`, generalVisibleA, generalPrivateA, generalArchivedA, generalWorkspaceA); err != nil {
		t.Fatalf("seed agents: %v", err)
	}
	if _, err := fixture.conn.Exec(fixture.ctx, `
		INSERT INTO channel (
			id, workspace_id, name, description, created_by, kind, archived_at, updated_at
		) VALUES
			($1, $3, 'general', 'old A', $5, 'group', $7, $8),
			($2, $4, 'general', 'old B', $6, 'group', $7, $8)
	`, generalCollisionA, generalCollisionB, generalWorkspaceA, generalWorkspaceB,
		generalOwnerA, generalOwnerB, archivedAt, updatedAt); err != nil {
		t.Fatalf("seed archived collisions: %v", err)
	}
	if _, err := fixture.conn.Exec(fixture.ctx, `
		INSERT INTO channel (workspace_id, name, created_by, kind)
		VALUES ($1, 'general-archived-11111111', $2, 'group')
	`, generalWorkspaceA, generalOwnerA); err != nil {
		t.Fatalf("seed rename fallback collision: %v", err)
	}
	if _, err := fixture.conn.Exec(fixture.ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'user', $3)
	`, generalCollisionA, generalWorkspaceA, generalOwnerA); err != nil {
		t.Fatalf("seed collision roster: %v", err)
	}
	if _, err := fixture.conn.Exec(fixture.ctx, `
		INSERT INTO channel_message (channel_id, workspace_id, content)
		VALUES ($1, $2, 'preserved history')
	`, generalCollisionA, generalWorkspaceA); err != nil {
		t.Fatalf("seed collision message: %v", err)
	}
	if _, err := fixture.conn.Exec(fixture.ctx, `UPDATE radar_call SET count = 0`); err != nil {
		t.Fatalf("reset radar counter: %v", err)
	}

	fixture.apply(t, fixture.upSQL, "apply migration 204 up")

	var renamedA, renamedB string
	var gotArchivedAt, gotUpdatedAt time.Time
	var messageCount, oldMemberCount int
	if err := fixture.conn.QueryRow(fixture.ctx, `
		SELECT name, archived_at, updated_at,
		       (SELECT count(*) FROM channel_message WHERE channel_id = channel.id),
		       (SELECT count(*) FROM channel_member WHERE channel_id = channel.id)
		FROM channel WHERE id = $1
	`, generalCollisionA).Scan(&renamedA, &gotArchivedAt, &gotUpdatedAt, &messageCount, &oldMemberCount); err != nil {
		t.Fatalf("read full-id collision: %v", err)
	}
	if renamedA != "general-archived-"+generalCollisionA || !gotArchivedAt.Equal(archivedAt) ||
		!gotUpdatedAt.Equal(updatedAt) || messageCount != 1 || oldMemberCount != 1 {
		t.Fatalf("collision A changed: name=%q archived=%s updated=%s messages=%d members=%d",
			renamedA, gotArchivedAt, gotUpdatedAt, messageCount, oldMemberCount)
	}
	if err := fixture.conn.QueryRow(fixture.ctx, `SELECT name FROM channel WHERE id = $1`, generalCollisionB).Scan(&renamedB); err != nil {
		t.Fatalf("read short-id collision: %v", err)
	}
	if renamedB != "general-archived-22222222" {
		t.Fatalf("collision B renamed to %q", renamedB)
	}
	var auditOriginal, auditRenamed string
	var auditArchived bool
	var auditMessages, auditMembers int
	if err := fixture.conn.QueryRow(fixture.ctx, `
		SELECT original_name, renamed_name, was_archived, message_count, member_count
		FROM system_channel_collision_audit WHERE channel_id = $1
	`, generalCollisionA).Scan(&auditOriginal, &auditRenamed, &auditArchived, &auditMessages, &auditMembers); err != nil {
		t.Fatalf("read collision audit: %v", err)
	}
	if auditOriginal != "general" || auditRenamed != renamedA || !auditArchived || auditMessages != 1 || auditMembers != 1 {
		t.Fatalf("collision audit = %q/%q/%v/%d/%d", auditOriginal, auditRenamed, auditArchived, auditMessages, auditMembers)
	}

	var systemA, systemB string
	if err := fixture.conn.QueryRow(fixture.ctx, `
		SELECT id::text FROM channel
		WHERE workspace_id = $1 AND name = 'general' AND kind = 'group'
		  AND system_key = 'general' AND archived_at IS NULL
		  AND project_id IS NULL AND lark_chat_id IS NULL AND group_manager_agent_id IS NULL
	`, generalWorkspaceA).Scan(&systemA); err != nil {
		t.Fatalf("read pristine system A: %v", err)
	}
	if err := fixture.conn.QueryRow(fixture.ctx, `
		SELECT id::text FROM channel
		WHERE workspace_id = $1 AND system_key = 'general'
	`, generalWorkspaceB).Scan(&systemB); err != nil {
		t.Fatalf("read system B: %v", err)
	}
	if systemA == generalCollisionA || systemB == generalCollisionB {
		t.Fatal("migration reused an archived collision as the system channel")
	}

	var users, agents, conversations, generatedMessages, radarCalls int
	if err := fixture.conn.QueryRow(fixture.ctx, `
		SELECT
			count(*) FILTER (WHERE member_type = 'user'),
			count(*) FILTER (WHERE member_type = 'agent')
		FROM channel_member WHERE channel_id = $1
	`, systemA).Scan(&users, &agents); err != nil {
		t.Fatalf("read system roster: %v", err)
	}
	if users != 2 || agents != 1 {
		t.Fatalf("system roster users=%d agents=%d, want 2/1", users, agents)
	}
	if err := fixture.conn.QueryRow(fixture.ctx, `
		SELECT count(*) FROM conversation_member WHERE conversation_id = $1 AND wake_state = 'active'
	`, systemA).Scan(&conversations); err != nil || conversations != 3 {
		t.Fatalf("conversation projection count=%d err=%v, want 3", conversations, err)
	}
	if err := fixture.conn.QueryRow(fixture.ctx, `SELECT count(*) FROM channel_message WHERE channel_id = $1`, systemA).Scan(&generatedMessages); err != nil || generatedMessages != 0 {
		t.Fatalf("generated system messages=%d err=%v", generatedMessages, err)
	}
	if err := fixture.conn.QueryRow(fixture.ctx, `SELECT count FROM radar_call`).Scan(&radarCalls); err != nil || radarCalls != 0 {
		t.Fatalf("radar calls during migration=%d err=%v", radarCalls, err)
	}

	// The public ensure function is idempotent under concurrent callers.
	const callers = 8
	var wait sync.WaitGroup
	errCh := make(chan error, callers)
	for range callers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			conn, err := fixture.pool.Acquire(fixture.ctx)
			if err != nil {
				errCh <- err
				return
			}
			defer conn.Release()
			if _, err := conn.Exec(fixture.ctx, "SET search_path TO "+fixture.quoted+", public"); err != nil {
				errCh <- err
				return
			}
			_, err = conn.Exec(fixture.ctx, `SELECT ensure_system_general_channel($1, $2)`, generalWorkspaceA, generalOwnerA)
			errCh <- err
		}()
	}
	wait.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent ensure: %v", err)
		}
	}
	var systemCount int
	if err := fixture.conn.QueryRow(fixture.ctx, `SELECT count(*) FROM channel WHERE workspace_id = $1 AND system_key = 'general'`, generalWorkspaceA).Scan(&systemCount); err != nil || systemCount != 1 {
		t.Fatalf("system channel count=%d err=%v", systemCount, err)
	}

	newHuman := "20000000-0000-0000-0000-000000000004"
	newAgent := "30000000-0000-0000-0000-000000000004"
	if _, err := fixture.conn.Exec(fixture.ctx, `
		INSERT INTO "user" (id, name, email) VALUES ($1, 'New Human', 'new-human@example.test');
	`, newHuman); err != nil {
		t.Fatalf("add eligible human: %v", err)
	}
	if _, err := fixture.conn.Exec(fixture.ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'member')`, generalWorkspaceA, newHuman); err != nil {
		t.Fatalf("add eligible workspace member: %v", err)
	}
	if _, err := fixture.conn.Exec(fixture.ctx, `INSERT INTO agent (id, workspace_id, visibility) VALUES ($1, $2, 'workspace')`, newAgent, generalWorkspaceA); err != nil {
		t.Fatalf("add eligible agent: %v", err)
	}
	assertSystemGeneralRosterMember(t, fixture, systemA, "user", newHuman, true)
	assertSystemGeneralRosterMember(t, fixture, systemA, "agent", newAgent, true)
	if _, err := fixture.conn.Exec(fixture.ctx, `UPDATE agent SET visibility = 'private' WHERE id = $1`, newAgent); err != nil {
		t.Fatalf("make agent private: %v", err)
	}
	assertSystemGeneralRosterMember(t, fixture, systemA, "agent", newAgent, false)
	if _, err := fixture.conn.Exec(fixture.ctx, `UPDATE agent SET visibility = 'workspace' WHERE id = $1`, newAgent); err != nil {
		t.Fatalf("restore agent visibility: %v", err)
	}
	if _, err := fixture.conn.Exec(fixture.ctx, `UPDATE agent SET archived_at = now() WHERE id = $1`, newAgent); err != nil {
		t.Fatalf("archive agent: %v", err)
	}
	assertSystemGeneralRosterMember(t, fixture, systemA, "agent", newAgent, false)
	if _, err := fixture.conn.Exec(fixture.ctx, `UPDATE agent SET archived_at = NULL WHERE id = $1`, newAgent); err != nil {
		t.Fatalf("restore agent: %v", err)
	}
	assertSystemGeneralRosterMember(t, fixture, systemA, "agent", newAgent, true)
	if _, err := fixture.conn.Exec(fixture.ctx, `DELETE FROM member WHERE workspace_id = $1 AND user_id = $2`, generalWorkspaceA, newHuman); err != nil {
		t.Fatalf("remove human member: %v", err)
	}
	assertSystemGeneralRosterMember(t, fixture, systemA, "user", newHuman, false)

	assertSystemGeneralGuard(t, fixture, `UPDATE channel SET name = 'renamed' WHERE id = $1`, systemA)
	assertSystemGeneralGuard(t, fixture, `UPDATE channel SET description = 'changed' WHERE id = $1`, systemA)
	assertSystemGeneralGuard(t, fixture, `UPDATE channel SET archived_at = now() WHERE id = $1`, systemA)
	assertSystemGeneralGuard(t, fixture, `UPDATE channel SET project_id = gen_random_uuid() WHERE id = $1`, systemA)
	assertSystemGeneralGuard(t, fixture, `UPDATE channel SET lark_chat_id = 'lark' WHERE id = $1`, systemA)
	assertSystemGeneralGuard(t, fixture, `UPDATE channel SET group_manager_agent_id = $2 WHERE id = $1`, systemA, generalVisibleA)
	assertSystemGeneralGuard(t, fixture, `DELETE FROM channel WHERE id = $1`, systemA)
	assertSystemGeneralGuard(t, fixture, `DELETE FROM channel_member WHERE channel_id = $1 AND member_type = 'user' AND member_id = $2`, systemA, generalOwnerA)
	assertSystemGeneralGuard(t, fixture, `INSERT INTO channel (workspace_id, name, created_by, kind) VALUES ($1, 'general', $2, 'group')`, generalWorkspaceA, generalOwnerA)

	// Down refuses used system rows and preserves the whole up state on failure.
	if _, err := fixture.conn.Exec(fixture.ctx, `INSERT INTO channel_message (channel_id, workspace_id, content) VALUES ($1, $2, 'used')`, systemA, generalWorkspaceA); err != nil {
		t.Fatalf("seed down message guard: %v", err)
	}
	fixture.applyWantError(t, fixture.downSQL, "system_general_down_blocked_by_messages")
	if _, err := fixture.conn.Exec(fixture.ctx, `DELETE FROM channel_message WHERE channel_id = $1`, systemA); err != nil {
		t.Fatalf("clear down message guard: %v", err)
	}
	if _, err := fixture.conn.Exec(fixture.ctx, `UPDATE channel_member SET muted_at = now() WHERE channel_id = $1 AND member_type = 'user' AND member_id = $2`, systemA, generalOwnerA); err != nil {
		t.Fatalf("seed down member-state guard: %v", err)
	}
	fixture.applyWantError(t, fixture.downSQL, "system_general_down_blocked_by_member_state")
	if _, err := fixture.conn.Exec(fixture.ctx, `UPDATE channel_member SET muted_at = NULL WHERE channel_id = $1`, systemA); err != nil {
		t.Fatalf("clear down member-state guard: %v", err)
	}

	fixture.apply(t, fixture.downSQL, "apply migration 204 down")
	var restoredA, restoredB string
	if err := fixture.conn.QueryRow(fixture.ctx, `SELECT name FROM channel WHERE id = $1`, generalCollisionA).Scan(&restoredA); err != nil {
		t.Fatalf("read restored collision A: %v", err)
	}
	if err := fixture.conn.QueryRow(fixture.ctx, `SELECT name FROM channel WHERE id = $1`, generalCollisionB).Scan(&restoredB); err != nil {
		t.Fatalf("read restored collision B: %v", err)
	}
	if restoredA != "general" || restoredB != "general" {
		t.Fatalf("restored names = %q/%q", restoredA, restoredB)
	}
	var systemKeyExists bool
	if err := fixture.conn.QueryRow(fixture.ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = 'channel' AND column_name = 'system_key'
		)
	`).Scan(&systemKeyExists); err != nil || systemKeyExists {
		t.Fatalf("system_key exists after down=%v err=%v", systemKeyExists, err)
	}
}

func TestSystemGeneralMigration204DownFailsClosedWhenCollisionChanged(t *testing.T) {
	fixture := newSystemGeneralFixture(t)
	seedSystemGeneralUsers(t, fixture)
	if _, err := fixture.conn.Exec(fixture.ctx, `
		INSERT INTO channel (id, workspace_id, name, created_by, kind, archived_at)
		VALUES ($1, $2, 'general', $3, 'group', now())
	`, generalCollisionA, generalWorkspaceA, generalOwnerA); err != nil {
		t.Fatalf("seed collision: %v", err)
	}
	fixture.apply(t, fixture.upSQL, "apply migration 204 up")
	if _, err := fixture.conn.Exec(fixture.ctx, `ALTER TABLE channel DISABLE TRIGGER trg_guard_system_general_channel`); err != nil {
		t.Fatalf("disable channel guard: %v", err)
	}
	if _, err := fixture.conn.Exec(fixture.ctx, `UPDATE channel SET name = 'changed-after-migration' WHERE id = $1`, generalCollisionA); err != nil {
		t.Fatalf("simulate changed archived collision: %v", err)
	}
	if _, err := fixture.conn.Exec(fixture.ctx, `ALTER TABLE channel ENABLE TRIGGER trg_guard_system_general_channel`); err != nil {
		t.Fatalf("enable channel guard: %v", err)
	}
	fixture.applyWantError(t, fixture.downSQL, "system_general_down_collision_changed")

	var systemCount int
	if err := fixture.conn.QueryRow(fixture.ctx, `SELECT count(*) FROM channel WHERE system_key = 'general'`).Scan(&systemCount); err != nil || systemCount != 2 {
		t.Fatalf("failed down preserved system rows=%d err=%v, want 2", systemCount, err)
	}
}

func TestSystemGeneralMigration204AllowsWorkspaceCascade(t *testing.T) {
	fixture := newSystemGeneralFixture(t)
	seedSystemGeneralUsers(t, fixture)
	fixture.apply(t, fixture.upSQL, "apply migration 204 up")

	if _, err := fixture.conn.Exec(fixture.ctx, `DELETE FROM workspace WHERE id = $1`, generalWorkspaceB); err != nil {
		t.Fatalf("delete workspace with system channel: %v", err)
	}
	var channelCount int
	if err := fixture.conn.QueryRow(fixture.ctx, `SELECT count(*) FROM channel WHERE workspace_id = $1`, generalWorkspaceB).Scan(&channelCount); err != nil || channelCount != 0 {
		t.Fatalf("workspace cascade left channels=%d err=%v", channelCount, err)
	}
}

func TestSystemGeneralMigration204CoversLegacyWorkspaceCreateAfterUp(t *testing.T) {
	fixture := newSystemGeneralFixture(t)
	seedSystemGeneralUsers(t, fixture)
	fixture.apply(t, fixture.upSQL, "apply migration 204 up")

	if _, err := fixture.conn.Exec(fixture.ctx, `
		INSERT INTO "user" (id, name, email)
		VALUES ($1, 'Legacy Owner', 'legacy-owner@example.test')
	`, generalLegacyUser); err != nil {
		t.Fatalf("seed legacy owner: %v", err)
	}

	// Simulate the complete workspace-create write set from a pre-204 server:
	// it creates the workspace and first member, but never calls the new ensure
	// function. The member trigger is the database-level rollout boundary.
	tx, err := fixture.conn.Begin(fixture.ctx)
	if err != nil {
		t.Fatalf("begin legacy workspace create: %v", err)
	}
	if _, err = tx.Exec(fixture.ctx, `
		INSERT INTO workspace (id, name, slug)
		VALUES ($1, 'Legacy Workspace', 'legacy-workspace')
	`, generalLegacyWS); err != nil {
		_ = tx.Rollback(fixture.ctx)
		t.Fatalf("insert legacy workspace: %v", err)
	}
	if _, err = tx.Exec(fixture.ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'owner')
	`, generalLegacyWS, generalLegacyUser); err != nil {
		_ = tx.Rollback(fixture.ctx)
		t.Fatalf("insert legacy first member: %v", err)
	}
	if err = tx.Commit(fixture.ctx); err != nil {
		t.Fatalf("commit legacy workspace create: %v", err)
	}

	var channelID, name, kind, systemKey string
	var archived, projectBound, larkBound, managerBound bool
	if err := fixture.conn.QueryRow(fixture.ctx, `
		SELECT id::text, name, kind, system_key,
		       archived_at IS NOT NULL,
		       project_id IS NOT NULL,
		       lark_chat_id IS NOT NULL,
		       group_manager_agent_id IS NOT NULL
		FROM channel
		WHERE workspace_id = $1
	`, generalLegacyWS).Scan(
		&channelID, &name, &kind, &systemKey,
		&archived, &projectBound, &larkBound, &managerBound,
	); err != nil {
		t.Fatalf("read legacy workspace general: %v", err)
	}
	if name != "general" || kind != "group" || systemKey != "general" ||
		archived || projectBound || larkBound || managerBound {
		t.Fatalf(
			"legacy general identity = name=%q kind=%q key=%q archived=%v project=%v lark=%v manager=%v",
			name, kind, systemKey, archived, projectBound, larkBound, managerBound,
		)
	}

	var users, agents, conversations, messages int
	if err := fixture.conn.QueryRow(fixture.ctx, `
		SELECT
			count(*) FILTER (WHERE member_type = 'user'),
			count(*) FILTER (WHERE member_type = 'agent')
		FROM channel_member
		WHERE channel_id = $1
	`, channelID).Scan(&users, &agents); err != nil {
		t.Fatalf("read legacy general roster: %v", err)
	}
	if users != 1 || agents != 0 {
		t.Fatalf("legacy general roster users=%d agents=%d, want 1/0", users, agents)
	}
	assertSystemGeneralRosterMember(t, fixture, channelID, "user", generalLegacyUser, true)
	if err := fixture.conn.QueryRow(fixture.ctx, `
		SELECT count(*) FROM conversation_member
		WHERE conversation_id = $1 AND wake_state = 'active'
	`, channelID).Scan(&conversations); err != nil || conversations != 1 {
		t.Fatalf("legacy general conversation projection=%d err=%v, want 1", conversations, err)
	}
	if err := fixture.conn.QueryRow(fixture.ctx, `
		SELECT count(*) FROM channel_message WHERE channel_id = $1
	`, channelID).Scan(&messages); err != nil || messages != 0 {
		t.Fatalf("legacy general generated messages=%d err=%v, want 0", messages, err)
	}
}

func TestSystemGeneralMigration204SerializesWithLegacyWorkspaceCreate(t *testing.T) {
	fixture := newSystemGeneralFixture(t)
	seedSystemGeneralUsers(t, fixture)

	legacyConn, err := fixture.pool.Acquire(fixture.ctx)
	if err != nil {
		t.Fatalf("acquire legacy connection: %v", err)
	}
	defer legacyConn.Release()
	if _, err := legacyConn.Exec(fixture.ctx, "SET search_path TO "+fixture.quoted+", public"); err != nil {
		t.Fatalf("set legacy search path: %v", err)
	}
	if _, err := legacyConn.Exec(fixture.ctx, `
		INSERT INTO "user" (id, name, email)
		VALUES ($1, 'Legacy Owner', 'legacy-owner@example.test')
	`, generalLegacyUser); err != nil {
		t.Fatalf("seed legacy owner: %v", err)
	}

	legacyTx, err := legacyConn.Begin(fixture.ctx)
	if err != nil {
		t.Fatalf("begin legacy writer: %v", err)
	}
	if _, err = legacyTx.Exec(fixture.ctx, `
		INSERT INTO workspace (id, name, slug)
		VALUES ($1, 'Legacy Workspace', 'legacy-workspace')
	`, generalLegacyWS); err != nil {
		_ = legacyTx.Rollback(fixture.ctx)
		t.Fatalf("legacy insert workspace: %v", err)
	}
	if _, err = legacyTx.Exec(fixture.ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'owner')
	`, generalLegacyWS, generalLegacyUser); err != nil {
		_ = legacyTx.Rollback(fixture.ctx)
		t.Fatalf("legacy insert member: %v", err)
	}

	// The migration's SHARE ROW EXCLUSIVE member lock must wait for this old
	// writer. Once the writer commits, the migration's backfill sees the whole
	// workspace and creates general before releasing its rollout boundary.
	migrationDone := make(chan error, 1)
	go func() {
		_, migrationErr := fixture.conn.Exec(fixture.ctx, fixture.upSQL)
		migrationDone <- migrationErr
	}()

	migrationPID := fixture.conn.Conn().PgConn().PID()
	lockDeadline := time.NewTimer(5 * time.Second)
	defer lockDeadline.Stop()
	lockPoll := time.NewTicker(10 * time.Millisecond)
	defer lockPoll.Stop()
	waitingOnLock := false
	for !waitingOnLock {
		select {
		case migrationErr := <-migrationDone:
			_ = legacyTx.Rollback(fixture.ctx)
			t.Fatalf("migration completed before legacy writer committed: %v", migrationErr)
		case <-lockPoll.C:
			if err := fixture.pool.QueryRow(fixture.ctx, `
				SELECT wait_event_type = 'Lock'
				FROM pg_stat_activity
				WHERE pid = $1
			`, migrationPID).Scan(&waitingOnLock); err != nil {
				_ = legacyTx.Rollback(fixture.ctx)
				t.Fatalf("read migration lock state: %v", err)
			}
		case <-lockDeadline.C:
			_ = legacyTx.Rollback(fixture.ctx)
			t.Fatal("migration did not wait on the legacy member writer")
		}
	}

	if err := legacyTx.Commit(fixture.ctx); err != nil {
		t.Fatalf("commit legacy writer: %v", err)
	}
	select {
	case migrationErr := <-migrationDone:
		if migrationErr != nil {
			_, _ = fixture.conn.Exec(context.Background(), "ROLLBACK")
			t.Fatalf("migration after legacy writer: %v", migrationErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("migration did not finish after legacy writer committed")
	}

	var channels, users int
	if err := fixture.conn.QueryRow(fixture.ctx, `
		SELECT count(*)
		FROM channel
		WHERE workspace_id = $1 AND name = 'general' AND kind = 'group'
		  AND system_key = 'general' AND archived_at IS NULL
	`, generalLegacyWS).Scan(&channels); err != nil || channels != 1 {
		t.Fatalf("serialized legacy general channels=%d err=%v, want 1", channels, err)
	}
	if err := fixture.conn.QueryRow(fixture.ctx, `
		SELECT count(*)
		FROM channel_member channel_roster
		JOIN channel ON channel.id = channel_roster.channel_id
		WHERE channel.workspace_id = $1 AND channel.system_key = 'general'
		  AND channel_roster.member_type = 'user'
		  AND channel_roster.member_id = $2
	`, generalLegacyWS, generalLegacyUser).Scan(&users); err != nil || users != 1 {
		t.Fatalf("serialized legacy owner memberships=%d err=%v, want 1", users, err)
	}
}

func TestSystemGeneralMigration204DownFailsClosedWhenOriginalNameOccupied(t *testing.T) {
	fixture := newSystemGeneralFixture(t)
	seedSystemGeneralUsers(t, fixture)
	if _, err := fixture.conn.Exec(fixture.ctx, `
		INSERT INTO channel (id, workspace_id, name, created_by, kind, archived_at)
		VALUES ($1, $2, 'general', $3, 'group', now())
	`, generalCollisionA, generalWorkspaceA, generalOwnerA); err != nil {
		t.Fatalf("seed collision: %v", err)
	}
	fixture.apply(t, fixture.upSQL, "apply migration 204 up")

	var systemA string
	if err := fixture.conn.QueryRow(fixture.ctx, `SELECT id::text FROM channel WHERE workspace_id = $1 AND system_key = 'general'`, generalWorkspaceA).Scan(&systemA); err != nil {
		t.Fatalf("load system channel: %v", err)
	}
	if _, err := fixture.conn.Exec(fixture.ctx, `ALTER TABLE channel DISABLE TRIGGER trg_guard_system_general_channel`); err != nil {
		t.Fatalf("disable channel guard: %v", err)
	}
	if _, err := fixture.conn.Exec(fixture.ctx, `UPDATE channel SET name = 'tampered-system-name' WHERE id = $1`, systemA); err != nil {
		t.Fatalf("move system visible name: %v", err)
	}
	if _, err := fixture.conn.Exec(fixture.ctx, `INSERT INTO channel (workspace_id, name, created_by, kind) VALUES ($1, 'general', $2, 'group')`, generalWorkspaceA, generalOwnerA); err != nil {
		t.Fatalf("occupy original name: %v", err)
	}
	if _, err := fixture.conn.Exec(fixture.ctx, `ALTER TABLE channel ENABLE TRIGGER trg_guard_system_general_channel`); err != nil {
		t.Fatalf("enable channel guard: %v", err)
	}

	fixture.applyWantError(t, fixture.downSQL, "system_general_down_original_name_occupied")
	var systemCount int
	if err := fixture.conn.QueryRow(fixture.ctx, `SELECT count(*) FROM channel WHERE system_key = 'general'`).Scan(&systemCount); err != nil || systemCount != 2 {
		t.Fatalf("failed down preserved system rows=%d err=%v, want 2", systemCount, err)
	}
}

func assertSystemGeneralRosterMember(t *testing.T, fixture *systemGeneralFixture, channelID, memberType, memberID string, want bool) {
	t.Helper()
	var got bool
	if err := fixture.conn.QueryRow(fixture.ctx, `
		SELECT EXISTS (
			SELECT 1 FROM channel_member
			WHERE channel_id = $1 AND member_type = $2 AND member_id = $3
		)
	`, channelID, memberType, memberID).Scan(&got); err != nil {
		t.Fatalf("check roster member %s/%s: %v", memberType, memberID, err)
	}
	if got != want {
		t.Fatalf("roster member %s/%s exists=%v, want %v", memberType, memberID, got, want)
	}
}

func assertSystemGeneralGuard(t *testing.T, fixture *systemGeneralFixture, sql string, args ...any) {
	t.Helper()
	_, err := fixture.conn.Exec(fixture.ctx, sql, args...)
	if err == nil {
		t.Fatalf("protected operation unexpectedly succeeded: %s", sql)
	}
	if !strings.Contains(err.Error(), "system_general_") || !strings.Contains(err.Error(), "SQLSTATE P0001") {
		t.Fatalf("protected operation error=%v, want stable system_general P0001", err)
	}
}
