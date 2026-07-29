package main

import (
	"context"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/migrations"
)

func TestChannelMemberActorProvenanceMigration245BackfillsAndSurvivesDownUp(t *testing.T) {
	pool := openAgentWakeCutoverDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	upFiles, err := migrations.Files("up")
	if err != nil {
		t.Fatalf("list up migrations: %v", err)
	}
	var before245 []string
	var up245 string
	for _, file := range upFiles {
		if migrations.ExtractVersion(file) == "245_channel_member_actor_provenance" {
			up245 = file
			break
		}
		before245 = append(before245, file)
	}
	if up245 == "" {
		t.Fatal("245_channel_member_actor_provenance up migration not found")
	}
	downFiles, err := migrations.Files("down")
	if err != nil {
		t.Fatalf("list down migrations: %v", err)
	}
	var down245 string
	for _, file := range downFiles {
		if migrations.ExtractVersion(file) == "245_channel_member_actor_provenance" {
			down245 = file
			break
		}
	}
	if down245 == "" {
		t.Fatal("245_channel_member_actor_provenance down migration not found")
	}

	lockKey := int64(rand.Uint64()&0x7fffffffffffffff) | 1
	if err := runMigrations(ctx, pool, runOptions{
		Direction:       "up",
		Files:           before245,
		AdvisoryLockKey: lockKey,
		Hooks:           preMigrationHooks,
	}); err != nil {
		t.Fatalf("apply migrations before 245: %v", err)
	}

	if _, err := pool.Exec(ctx, channelMemberActorProvenanceLegacyFixture); err != nil {
		t.Fatalf("seed pre-245 provenance fixture: %v", err)
	}
	if err := runMigrations(ctx, pool, runOptions{
		Direction:       "up",
		Files:           []string{up245},
		AdvisoryLockKey: lockKey,
		Hooks:           preMigrationHooks,
	}); err != nil {
		t.Fatalf("apply migration 245 up: %v", err)
	}
	assertMigratedChannelMemberActor(t, ctx, pool,
		"7a000000-0000-4000-8000-000000000005", "user",
		"7a000000-0000-4000-8000-000000000001", "user",
		"7a000000-0000-4000-8000-000000000001")
	assertMigratedChannelMemberActor(t, ctx, pool,
		"7a000000-0000-4000-8000-000000000005", "agent",
		"7a000000-0000-4000-8000-000000000007", "system", "")
	assertMigratedOnboardingActor(t, ctx, pool,
		"7a000000-0000-4000-8000-000000000005",
		"7a000000-0000-4000-8000-000000000006", "user",
		"7a000000-0000-4000-8000-000000000001")
	assertMigratedOnboardingActor(t, ctx, pool,
		"7a000000-0000-4000-8000-000000000005",
		"7a000000-0000-4000-8000-000000000007", "system", "")
	assertMigration245ActorExistenceTriggers(t, ctx, pool)

	// Introduce membership and onboarding actor shapes that the old user-only
	// schema cannot represent. Down must explicitly collapse both to NULL; the
	// following up must then classify both historical NULLs as system rather
	// than inventing a user.
	if _, err := pool.Exec(ctx, `
		UPDATE channel_member
		SET added_by_type = 'agent',
		    added_by_id = '7a000000-0000-4000-8000-000000000006'
		WHERE channel_id = '7a000000-0000-4000-8000-000000000005'
		  AND member_type = 'agent'
		  AND member_id = '7a000000-0000-4000-8000-000000000007';

		UPDATE channel_agent_onboarding
		SET source_actor_type = 'agent',
		    source_actor_id = '7a000000-0000-4000-8000-000000000006'
		WHERE agent_id = '7a000000-0000-4000-8000-000000000006'`); err != nil {
		t.Fatalf("seed agent-authored rows before lossy down: %v", err)
	}
	if err := runMigrations(ctx, pool, runOptions{
		Direction:       "down",
		Files:           []string{down245},
		AdvisoryLockKey: lockKey,
		Hooks:           preMigrationHooks,
	}); err != nil {
		t.Fatalf("apply migration 245 down: %v", err)
	}
	var legacyAddedBy *string
	if err := pool.QueryRow(ctx, `
		SELECT added_by::text
		FROM channel_member
		WHERE channel_id = '7a000000-0000-4000-8000-000000000005'
		  AND member_type = 'agent'
		  AND member_id = '7a000000-0000-4000-8000-000000000007'`).Scan(&legacyAddedBy); err != nil {
		t.Fatalf("load lossy down row: %v", err)
	}
	if legacyAddedBy != nil {
		t.Fatalf("agent provenance down-converted to added_by=%v, want NULL", *legacyAddedBy)
	}
	var legacySourceActorID *string
	if err := pool.QueryRow(ctx, `
		SELECT source_actor_id::text
		FROM channel_agent_onboarding
		WHERE agent_id = '7a000000-0000-4000-8000-000000000006'`).Scan(&legacySourceActorID); err != nil {
		t.Fatalf("load lossy down onboarding row: %v", err)
	}
	if legacySourceActorID != nil {
		t.Fatalf(
			"agent onboarding provenance down-converted to source_actor_id=%v, want NULL",
			*legacySourceActorID,
		)
	}

	if err := runMigrations(ctx, pool, runOptions{
		Direction:       "up",
		Files:           []string{up245},
		AdvisoryLockKey: lockKey,
		Hooks:           preMigrationHooks,
	}); err != nil {
		t.Fatalf("reapply migration 245 up: %v", err)
	}
	assertMigratedChannelMemberActor(t, ctx, pool,
		"7a000000-0000-4000-8000-000000000005", "agent",
		"7a000000-0000-4000-8000-000000000007", "system", "")
	assertMigratedOnboardingActor(t, ctx, pool,
		"7a000000-0000-4000-8000-000000000005",
		"7a000000-0000-4000-8000-000000000006", "system", "")
}

func assertMigration245ActorExistenceTriggers(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) {
	t.Helper()
	if _, err := pool.Exec(ctx, channelMemberActorProvenanceInvalidActorFixture); err != nil {
		t.Fatalf("seed migration 245 invalid-actor controls: %v", err)
	}

	invalidActors := []struct {
		name      string
		actorType string
		actorID   string
	}{
		{
			name:      "nonexistent user",
			actorType: "user",
			actorID:   "7a000000-0000-4000-8000-000000000013",
		},
		{
			name:      "cross workspace user",
			actorType: "user",
			actorID:   "7a000000-0000-4000-8000-000000000011",
		},
		{
			name:      "nonexistent agent",
			actorType: "agent",
			actorID:   "7a000000-0000-4000-8000-000000000014",
		},
		{
			name:      "cross workspace agent",
			actorType: "agent",
			actorID:   "7a000000-0000-4000-8000-000000000012",
		},
	}

	t.Run("membership insert and update reject invalid actors", func(t *testing.T) {
		for _, actor := range invalidActors {
			t.Run(actor.name, func(t *testing.T) {
				if _, err := pool.Exec(ctx, `
					INSERT INTO channel_member (
					  channel_id, workspace_id, member_type, member_id, role,
					  added_by_type, added_by_id, join_source
					)
					VALUES (
					  '7a000000-0000-4000-8000-000000000005',
					  '7a000000-0000-4000-8000-000000000002',
					  'user',
					  '7a000000-0000-4000-8000-000000000010',
					  'member',
					  $1,
					  $2,
					  'manual'
					)`,
					actor.actorType,
					actor.actorID,
				); err == nil {
					t.Fatalf("membership insert accepted invalid actor %s/%s", actor.actorType, actor.actorID)
				}

				if _, err := pool.Exec(ctx, `
					UPDATE channel_member
					SET added_by_type = $1, added_by_id = $2
					WHERE channel_id = '7a000000-0000-4000-8000-000000000005'
					  AND member_type = 'agent'
					  AND member_id = '7a000000-0000-4000-8000-000000000007'`,
					actor.actorType,
					actor.actorID,
				); err == nil {
					t.Fatalf("membership update accepted invalid actor %s/%s", actor.actorType, actor.actorID)
				}
			})
		}
	})

	t.Run("onboarding update rejects invalid actors through shared assertion", func(t *testing.T) {
		for _, actor := range invalidActors {
			t.Run(actor.name, func(t *testing.T) {
				if _, err := pool.Exec(ctx, `
					UPDATE channel_agent_onboarding
					SET source_actor_type = $1, source_actor_id = $2
					WHERE channel_id = '7a000000-0000-4000-8000-000000000005'
					  AND agent_id = '7a000000-0000-4000-8000-000000000006'`,
					actor.actorType,
					actor.actorID,
				); err == nil {
					t.Fatalf("onboarding update accepted invalid actor %s/%s", actor.actorType, actor.actorID)
				}
			})
		}
	})
}

func assertMigratedChannelMemberActor(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	channelID, memberType, memberID, wantType, wantID string,
) {
	t.Helper()
	var actorType, actorID string
	if err := pool.QueryRow(ctx, `
		SELECT added_by_type, COALESCE(added_by_id::text, '')
		FROM channel_member
		WHERE channel_id = $1 AND member_type = $2 AND member_id = $3`,
		channelID, memberType, memberID).Scan(&actorType, &actorID); err != nil {
		t.Fatalf("load migrated channel member actor: %v", err)
	}
	if actorType != wantType || actorID != wantID {
		t.Fatalf("migrated channel member actor=%s/%s, want %s/%s", actorType, actorID, wantType, wantID)
	}
}

func assertMigratedOnboardingActor(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	channelID, agentID, wantType, wantID string,
) {
	t.Helper()
	var actorType, actorID string
	if err := pool.QueryRow(ctx, `
		SELECT source_actor_type, COALESCE(source_actor_id::text, '')
		FROM channel_agent_onboarding
		WHERE channel_id = $1 AND agent_id = $2`,
		channelID, agentID).Scan(&actorType, &actorID); err != nil {
		t.Fatalf("load migrated onboarding actor: %v", err)
	}
	if actorType != wantType || actorID != wantID {
		t.Fatalf("migrated onboarding actor=%s/%s, want %s/%s", actorType, actorID, wantType, wantID)
	}
}

const channelMemberActorProvenanceLegacyFixture = `
INSERT INTO "user" (id, name, email)
VALUES (
  '7a000000-0000-4000-8000-000000000001',
  'Actor Provenance Owner',
  'actor-provenance-owner@example.test'
);
INSERT INTO workspace (id, name, slug, description, issue_prefix)
VALUES (
  '7a000000-0000-4000-8000-000000000002',
  'Actor Provenance Workspace',
  'actor-provenance-workspace',
  'Migration fixture',
  'AP'
);
INSERT INTO member (workspace_id, user_id, role)
VALUES (
  '7a000000-0000-4000-8000-000000000002',
  '7a000000-0000-4000-8000-000000000001',
  'owner'
);
INSERT INTO agent_runtime (
  id, workspace_id, name, runtime_mode, provider, status,
  device_info, metadata, last_seen_at
) VALUES (
  '7a000000-0000-4000-8000-000000000003',
  '7a000000-0000-4000-8000-000000000002',
  'Actor Provenance Runtime',
  'cloud',
  'migration_test',
  'online',
  'Migration fixture',
  '{}'::jsonb,
  now()
);
INSERT INTO agent (
  id, workspace_id, name, display_name, description, runtime_mode,
  runtime_config, runtime_id, visibility, max_concurrent_tasks, owner_id
) VALUES
(
  '7a000000-0000-4000-8000-000000000006',
  '7a000000-0000-4000-8000-000000000002',
  'actor_provenance_user_added',
  'User-added Agent',
  '',
  'cloud',
  '{}'::jsonb,
  '7a000000-0000-4000-8000-000000000003',
  'workspace',
  1,
  '7a000000-0000-4000-8000-000000000001'
),
(
  '7a000000-0000-4000-8000-000000000007',
  '7a000000-0000-4000-8000-000000000002',
  'actor_provenance_system_added',
  'System-added Agent',
  '',
  'cloud',
  '{}'::jsonb,
  '7a000000-0000-4000-8000-000000000003',
  'workspace',
  1,
  '7a000000-0000-4000-8000-000000000001'
);
INSERT INTO channel (
  id, workspace_id, name, kind, created_by
) VALUES (
  '7a000000-0000-4000-8000-000000000005',
  '7a000000-0000-4000-8000-000000000002',
  'actor-provenance-migration',
  'group',
  '7a000000-0000-4000-8000-000000000001'
);
INSERT INTO channel_member (
  channel_id, workspace_id, member_type, member_id, role, added_by, join_source
) VALUES
(
  '7a000000-0000-4000-8000-000000000005',
  '7a000000-0000-4000-8000-000000000002',
  'agent',
  '7a000000-0000-4000-8000-000000000006',
  'member',
  '7a000000-0000-4000-8000-000000000001',
  'manual'
),
(
  '7a000000-0000-4000-8000-000000000005',
  '7a000000-0000-4000-8000-000000000002',
  'agent',
  '7a000000-0000-4000-8000-000000000007',
  'member',
  NULL,
  'system'
);
`

const channelMemberActorProvenanceInvalidActorFixture = `
INSERT INTO "user" (id, name, email)
VALUES (
  '7a000000-0000-4000-8000-000000000010',
  'Actor Provenance Target',
  'actor-provenance-target@example.test'
), (
  '7a000000-0000-4000-8000-000000000011',
  'Actor Provenance Foreign User',
  'actor-provenance-foreign@example.test'
);
INSERT INTO member (workspace_id, user_id, role)
VALUES (
  '7a000000-0000-4000-8000-000000000002',
  '7a000000-0000-4000-8000-000000000010',
  'member'
);
INSERT INTO workspace (id, name, slug, description, issue_prefix)
VALUES (
  '7a000000-0000-4000-8000-000000000009',
  'Actor Provenance Foreign Workspace',
  'actor-provenance-foreign-workspace',
  'Migration fixture',
  'AF'
);
INSERT INTO member (workspace_id, user_id, role)
VALUES (
  '7a000000-0000-4000-8000-000000000009',
  '7a000000-0000-4000-8000-000000000011',
  'owner'
);
INSERT INTO agent_runtime (
  id, workspace_id, name, runtime_mode, provider, status,
  device_info, metadata, last_seen_at
) VALUES (
  '7a000000-0000-4000-8000-000000000008',
  '7a000000-0000-4000-8000-000000000009',
  'Actor Provenance Foreign Runtime',
  'cloud',
  'migration_test',
  'online',
  'Migration fixture',
  '{}'::jsonb,
  now()
);
INSERT INTO agent (
  id, workspace_id, name, display_name, description, runtime_mode,
  runtime_config, runtime_id, visibility, max_concurrent_tasks, owner_id
) VALUES (
  '7a000000-0000-4000-8000-000000000012',
  '7a000000-0000-4000-8000-000000000009',
  'actor_provenance_foreign_agent',
  'Foreign Agent',
  '',
  'cloud',
  '{}'::jsonb,
  '7a000000-0000-4000-8000-000000000008',
  'workspace',
  1,
  '7a000000-0000-4000-8000-000000000011'
);
`
