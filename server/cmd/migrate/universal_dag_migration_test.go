package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const universalDAGMigrationLegacySchema = `
CREATE TABLE workspace (
  id uuid PRIMARY KEY
);
CREATE TABLE project (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  UNIQUE (workspace_id, id)
);
CREATE TABLE channel (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  project_id uuid,
  UNIQUE (workspace_id, id),
  UNIQUE (workspace_id, project_id, id)
);
CREATE TABLE agent_inbox_event (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  issue_id uuid,
  channel_id uuid
);
CREATE TABLE task_message (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  task_id uuid NOT NULL REFERENCES agent_inbox_event(id),
  seq integer NOT NULL,
  type text NOT NULL DEFAULT 'message',
  tool text,
  content text,
  input jsonb,
  output text,
  created_at timestamptz NOT NULL DEFAULT now(),
  visibility text NOT NULL DEFAULT 'internal'
);
CREATE INDEX idx_task_message_task_id_seq ON task_message(task_id, seq);
CREATE TABLE env_dispatch_run (
  project_id uuid PRIMARY KEY REFERENCES project(id),
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  run_id uuid NOT NULL UNIQUE
);
CREATE TABLE env_dispatch_run_agent (
  run_agent_id uuid PRIMARY KEY,
  run_id uuid NOT NULL REFERENCES env_dispatch_run(run_id),
  UNIQUE (run_id, run_agent_id)
);
CREATE TABLE env_dispatch_resident_turn (
  turn_id uuid PRIMARY KEY,
  run_id uuid NOT NULL,
  run_agent_id uuid NOT NULL,
  UNIQUE (run_id, run_agent_id, turn_id),
  FOREIGN KEY (run_id, run_agent_id)
    REFERENCES env_dispatch_run_agent(run_id, run_agent_id)
);
CREATE TABLE pi_provider_call (
  call_id text PRIMARY KEY,
  run_id uuid NOT NULL,
  run_agent_id uuid NOT NULL,
  turn_id uuid NOT NULL,
  call_ordinal bigint NOT NULL CHECK (call_ordinal > 0),
  UNIQUE (run_id, call_id),
  UNIQUE (run_id, run_agent_id, call_id),
  FOREIGN KEY (run_id, run_agent_id, turn_id)
    REFERENCES env_dispatch_resident_turn(run_id, run_agent_id, turn_id)
);
CREATE TABLE interaction_dag_segment (
  segment_id text PRIMARY KEY,
  project_id text NOT NULL,
  agent_run_id text NOT NULL,
  issue_id text,
  task_id text,
  trajectory_id bigint,
  tensor_ref jsonb,
  closing_event text,
  closing_event_target_segment text,
  created_at timestamptz NOT NULL DEFAULT now(),
  start_seq integer NOT NULL DEFAULT 0,
  end_seq integer NOT NULL DEFAULT 0,
  trajectory_source text NOT NULL DEFAULT 'areal_tensor',
  trainable boolean NOT NULL DEFAULT true,
  trajectory jsonb NOT NULL DEFAULT '[]'::jsonb,
  CONSTRAINT ck_segment_source_valid CHECK (
    (trajectory_source = 'areal_tensor' AND trajectory_id IS NOT NULL AND tensor_ref IS NOT NULL)
    OR
    (trajectory_source = 'task_messages' AND trajectory_id IS NULL AND tensor_ref IS NULL)
  )
);
CREATE TABLE interaction_dag_step_reward (
  segment_id text NOT NULL REFERENCES interaction_dag_segment(segment_id) ON DELETE CASCADE,
  seq integer NOT NULL,
  score integer NOT NULL,
  rationale text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (segment_id, seq)
);
CREATE TABLE interaction_dag_env_snapshot (
  segment_id text PRIMARY KEY REFERENCES interaction_dag_segment(segment_id) ON DELETE CASCADE,
  sandbox_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
  issue_snapshot_id text,
  env_state jsonb NOT NULL DEFAULT '{}'::jsonb
);
CREATE TABLE interaction_dag_session_run (
  session_id text PRIMARY KEY,
  project_id text NOT NULL,
  agent_run_id text NOT NULL,
  issue_id text,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE interaction_dag_edge (
  id bigserial PRIMARY KEY,
  project_id text NOT NULL,
  src_segment_id text NOT NULL,
  dst_segment_id text NOT NULL,
  type text NOT NULL CHECK (type IN ('delegation', 'mention', 'completion')),
  created_at timestamptz NOT NULL DEFAULT now()
);
`

const (
	migrationWSA       = "10000000-0000-4000-8000-000000000001"
	migrationWSB       = "10000000-0000-4000-8000-000000000002"
	migrationProjectA  = "20000000-0000-4000-8000-000000000001"
	migrationProjectB  = "20000000-0000-4000-8000-000000000002"
	migrationChannelA  = "30000000-0000-4000-8000-000000000001"
	migrationChannelB  = "30000000-0000-4000-8000-000000000002"
	migrationTaskA     = "40000000-0000-4000-8000-000000000001"
	migrationTaskB     = "40000000-0000-4000-8000-000000000002"
	migrationRunA      = "50000000-0000-4000-8000-000000000001"
	migrationRunB      = "50000000-0000-4000-8000-000000000002"
	migrationRunAgentA = "60000000-0000-4000-8000-000000000001"
	migrationRunAgentB = "60000000-0000-4000-8000-000000000002"
	migrationTurnA     = "70000000-0000-4000-8000-000000000001"
	migrationTurnB     = "70000000-0000-4000-8000-000000000002"
)

type universalDAGMigrationFixture struct {
	ctx    context.Context
	pool   *pgxpool.Pool
	conn   *pgxpool.Conn
	schema string
}

// TestUniversalDAGMigration intentionally treats a missing migration pair as a
// hard failure. The RED phase must never turn an absent migration into a skip.
func TestUniversalDAGMigration(t *testing.T) {
	upSQL, downSQL := readUniversalDAGMigration454(t)

	t.Run("real agent inbox key shape round trips up down up", func(t *testing.T) {
		fixture := newUniversalDAGMigrationFixture(t)
		baselineCatalog := fixture.constraintAndIndexDefinitions(t)
		if fixture.constraintExists(t, "agent_inbox_event_workspace_id_id_454_key") {
			t.Fatal("real pre-454 agent_inbox_event fixture unexpectedly has the migration-owned candidate key")
		}
		if fixture.relationExists(t, "task_message_task_id_seq_454_uidx") {
			t.Fatal("real pre-454 task_message fixture unexpectedly has migration-owned uniqueness")
		}
		fixture.apply(t, "up", upSQL)
		assertUniversalDAGMigrationSchema(t, fixture)
		if !fixture.constraintExists(t, "agent_inbox_event_workspace_id_id_454_key") {
			t.Fatal("up migration did not create the agent inbox workspace/id candidate key")
		}

		fixture.apply(t, "down", downSQL)
		for _, relation := range []string{
			"interaction_dag_task_cursor",
			"interaction_dag_edge_sequence",
			"interaction_dag_publish_outbox",
			"interaction_dag_universal_provider_call",
			"interaction_dag_segment_canonical_range_guard_idx",
			"task_message_task_id_seq_454_uidx",
		} {
			if fixture.relationExists(t, relation) {
				t.Fatalf("down migration left canonical relation %s behind", relation)
			}
		}
		if !fixture.relationExists(t, "interaction_dag_segment") ||
			!fixture.relationExists(t, "interaction_dag_edge") {
			t.Fatal("down migration did not restore the legacy DAG tables")
		}
		if fixture.constraintExists(t, "agent_inbox_event_workspace_id_id_454_key") {
			t.Fatal("down migration left the migration-owned agent inbox candidate key")
		}
		if restoredCatalog := fixture.constraintAndIndexDefinitions(t); restoredCatalog != baselineCatalog {
			t.Fatalf("down migration constraint/index catalog mismatch: baseline=%q restored=%q", baselineCatalog, restoredCatalog)
		}

		fixture.apply(t, "second up", upSQL)
		assertUniversalDAGMigrationSchema(t, fixture)
		if !fixture.constraintExists(t, "agent_inbox_event_workspace_id_id_454_key") {
			t.Fatal("second up migration did not restore the agent inbox candidate key")
		}
		if !fixture.relationExists(t, "task_message_task_id_seq_454_uidx") {
			t.Fatal("second up migration did not restore task-message identity uniqueness")
		}
	})

	t.Run("source check round trips to the exact migration 205 baseline", func(t *testing.T) {
		fixture := newUniversalDAGMigrationFixture(t)
		baseline := fixture.constraintDefinition(t, "ck_segment_source_valid")
		fixture.apply(t, "up", upSQL)
		canonical := fixture.constraintDefinition(t, "ck_segment_source_valid")
		if canonical == baseline || !strings.Contains(canonical, "content_status") {
			t.Fatalf("up migration did not install the canonical-aware source check: %s", canonical)
		}
		fixture.apply(t, "down", downSQL)
		restored := fixture.constraintDefinition(t, "ck_segment_source_valid")
		if restored != baseline {
			t.Fatalf("down migration source check mismatch: baseline=%s restored=%s", baseline, restored)
		}
	})

	t.Run("verified legacy ownership backfills deterministic generations", func(t *testing.T) {
		fixture := newUniversalDAGMigrationFixture(t)
		fixture.seedCanonicalOwners(t)
		fixture.insertMessages(t, migrationTaskA, 1, 2, 3)
		fixture.insertLegacySegment(t, "legacy-a", migrationProjectA, migrationTaskA, 1, 2, "task_messages", `[]`, nil)
		fixture.insertLegacySegment(t, "legacy-b", migrationProjectA, migrationTaskA, 3, 3, "task_messages", `[]`, nil)

		fixture.apply(t, "up", upSQL)

		rows, err := fixture.conn.Query(fixture.ctx, `
			SELECT segment_id,workspace_id::text,project_id_at_event::text,
			       agent_run_id::text,generation,content_status,
			       close_action_kind,canonical_action_id::text,visible_action_key,
			       publish_status,publish_seq,
			       graph_projection_eligible_at_event,trainable_eligible
			FROM interaction_dag_segment
			ORDER BY generation
		`)
		if err != nil {
			t.Fatalf("query migrated legacy segments: %v", err)
		}
		defer rows.Close()
		for generation, segmentID := range []string{"legacy-a", "legacy-b"} {
			if !rows.Next() {
				t.Fatalf("missing migrated legacy segment %s", segmentID)
			}
			var gotSegment, workspaceID, projectID, taskID, contentStatus string
			var gotGeneration int64
			var closeAction, canonicalAction, visibleAction, publishStatus *string
			var publishSeq *int64
			var graphEligible, trainableEligible bool
			if err := rows.Scan(
				&gotSegment, &workspaceID, &projectID, &taskID, &gotGeneration, &contentStatus,
				&closeAction, &canonicalAction, &visibleAction, &publishStatus, &publishSeq,
				&graphEligible, &trainableEligible,
			); err != nil {
				t.Fatalf("scan migrated legacy segment: %v", err)
			}
			if gotSegment != segmentID || workspaceID != migrationWSA ||
				projectID != migrationProjectA || taskID != migrationTaskA ||
				gotGeneration != int64(generation+1) {
				t.Fatalf("legacy backfill = segment=%s workspace=%s project=%s task=%s generation=%d",
					gotSegment, workspaceID, projectID, taskID, gotGeneration)
			}
			if contentStatus != "legacy_unverified" || closeAction != nil || canonicalAction != nil ||
				visibleAction != nil || publishStatus != nil || publishSeq != nil {
				t.Fatalf("legacy row acquired trusted identity: content=%q close=%v canonical=%v visible=%v publish=%v/%v",
					contentStatus, closeAction, canonicalAction, visibleAction, publishStatus, publishSeq)
			}
			if graphEligible || trainableEligible {
				t.Fatalf("legacy row became eligible: graph=%t trainable=%t", graphEligible, trainableEligible)
			}
		}
		if rows.Next() {
			t.Fatal("migration created an unexpected legacy segment")
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("read migrated legacy segments: %v", err)
		}

		var outboxCount, providerLinkCount int
		if err := fixture.conn.QueryRow(fixture.ctx, `SELECT count(*) FROM interaction_dag_publish_outbox`).Scan(&outboxCount); err != nil {
			t.Fatalf("count legacy outbox rows: %v", err)
		}
		if err := fixture.conn.QueryRow(fixture.ctx, `SELECT count(*) FROM interaction_dag_universal_provider_call`).Scan(&providerLinkCount); err != nil {
			t.Fatalf("count legacy provider links: %v", err)
		}
		if outboxCount != 0 || providerLinkCount != 0 {
			t.Fatalf("legacy migration invented outbox/provider ownership: outbox=%d provider_links=%d", outboxCount, providerLinkCount)
		}
	})

	t.Run("global duplicate task message identity gate fails closed without payload disclosure", func(t *testing.T) {
		fixture := newUniversalDAGMigrationFixture(t)
		fixture.seedCanonicalOwners(t)
		fixture.insertMessages(t, migrationTaskA, 1, 1)
		before := fixture.fingerprint(t)

		_, migrationErr := fixture.conn.Exec(fixture.ctx, upSQL)
		if migrationErr == nil {
			t.Fatal("migration 454 accepted a duplicate task-message identity outside every legacy Segment")
		}
		_, _ = fixture.conn.Exec(context.Background(), "ROLLBACK")
		if !strings.Contains(migrationErr.Error(), "migration 454 refused duplicate task message identity") {
			t.Fatalf("duplicate identity gate returned an unclear error: %v", migrationErr)
		}
		for _, secret := range []string{
			"task-message-payload-secret",
			"task-input-payload-secret",
			"task-output-payload-secret",
		} {
			if strings.Contains(migrationErr.Error(), secret) {
				t.Fatalf("duplicate identity gate disclosed payload %q: %v", secret, migrationErr)
			}
		}
		if after := fixture.fingerprint(t); after != before {
			t.Fatalf("duplicate identity refusal changed catalog or data: before=%s after=%s", before, after)
		}
	})

	t.Run("dirty legacy rows fail closed atomically without payload disclosure", func(t *testing.T) {
		cases := []struct {
			name string
			seed func(*testing.T, *universalDAGMigrationFixture)
		}{
			{
				name: "malformed project id",
				seed: func(t *testing.T, f *universalDAGMigrationFixture) {
					f.seedCanonicalOwners(t)
					f.insertMessages(t, migrationTaskA, 1)
					f.insertLegacySegment(t, "bad-project", "not-a-uuid", migrationTaskA, 1, 1, "task_messages", `[]`, nil)
				},
			},
			{
				name: "orphan project id",
				seed: func(t *testing.T, f *universalDAGMigrationFixture) {
					f.seedCanonicalOwners(t)
					f.insertMessages(t, migrationTaskA, 1)
					f.insertLegacySegment(t, "orphan-project", "20000000-0000-4000-8000-000000000099", migrationTaskA, 1, 1, "task_messages", `[]`, nil)
				},
			},
			{
				name: "malformed task id",
				seed: func(t *testing.T, f *universalDAGMigrationFixture) {
					f.seedCanonicalOwners(t)
					f.insertLegacySegment(t, "bad-task", migrationProjectA, "not-a-uuid", 1, 1, "task_messages", `[]`, nil)
				},
			},
			{
				name: "orphan task id",
				seed: func(t *testing.T, f *universalDAGMigrationFixture) {
					f.seedCanonicalOwners(t)
					f.insertLegacySegment(t, "orphan-task", migrationProjectA, "40000000-0000-4000-8000-000000000099", 1, 1, "task_messages", `[]`, nil)
				},
			},
			{
				name: "duplicate run range",
				seed: func(t *testing.T, f *universalDAGMigrationFixture) {
					f.seedCanonicalOwners(t)
					f.insertMessages(t, migrationTaskA, 1, 2)
					f.insertLegacySegment(t, "duplicate-a", migrationProjectA, migrationTaskA, 1, 2, "task_messages", `[]`, nil)
					f.insertLegacySegment(t, "duplicate-b", migrationProjectA, migrationTaskA, 1, 2, "task_messages", `[]`, nil)
				},
			},
			{
				name: "zero range",
				seed: func(t *testing.T, f *universalDAGMigrationFixture) {
					f.seedCanonicalOwners(t)
					f.insertLegacySegment(t, "zero-range", migrationProjectA, migrationTaskA, 0, 0, "task_messages", `[]`, nil)
				},
			},
			{
				name: "negative range",
				seed: func(t *testing.T, f *universalDAGMigrationFixture) {
					f.seedCanonicalOwners(t)
					f.insertLegacySegment(t, "negative-range", migrationProjectA, migrationTaskA, -2, -1, "task_messages", `[]`, nil)
				},
			},
			{
				name: "inverted range",
				seed: func(t *testing.T, f *universalDAGMigrationFixture) {
					f.seedCanonicalOwners(t)
					f.insertLegacySegment(t, "inverted-range", migrationProjectA, migrationTaskA, 2, 1, "task_messages", `[]`, nil)
				},
			},
			{
				name: "missing task message sequence",
				seed: func(t *testing.T, f *universalDAGMigrationFixture) {
					f.seedCanonicalOwners(t)
					f.insertMessages(t, migrationTaskA, 1)
					f.insertLegacySegment(t, "missing-sequence", migrationProjectA, migrationTaskA, 1, 2, "task_messages", `[]`, nil)
				},
			},
			{
				name: "task message sequence gap",
				seed: func(t *testing.T, f *universalDAGMigrationFixture) {
					f.seedCanonicalOwners(t)
					f.insertMessages(t, migrationTaskA, 1, 3)
					f.insertLegacySegment(t, "sequence-gap", migrationProjectA, migrationTaskA, 1, 3, "task_messages", `[]`, nil)
				},
			},
			{
				name: "duplicate task message sequence",
				seed: func(t *testing.T, f *universalDAGMigrationFixture) {
					f.seedCanonicalOwners(t)
					f.insertMessages(t, migrationTaskA, 1, 1)
					f.insertLegacySegment(t, "duplicate-sequence", migrationProjectA, migrationTaskA, 1, 1, "task_messages", `[]`, nil)
				},
			},
			{
				name: "readable legacy trajectory",
				seed: func(t *testing.T, f *universalDAGMigrationFixture) {
					f.seedCanonicalOwners(t)
					f.insertMessages(t, migrationTaskA, 1)
					f.insertLegacySegment(t, "readable-trajectory", migrationProjectA, migrationTaskA, 1, 1,
						"areal_tensor", `[{"content":"trajectory-payload-secret"}]`, `{"tensor":"tensor-payload-secret"}`)
				},
			},
			{
				name: "legacy completion edge",
				seed: func(t *testing.T, f *universalDAGMigrationFixture) {
					f.seedCanonicalOwners(t)
					f.insertMessages(t, migrationTaskA, 1, 2)
					f.insertLegacySegment(t, "edge-source", migrationProjectA, migrationTaskA, 1, 1, "task_messages", `[]`, nil)
					f.insertLegacySegment(t, "edge-target", migrationProjectA, migrationTaskA, 2, 2, "task_messages", `[]`, nil)
					f.insertLegacyEdge(t, migrationProjectA, "edge-source", "edge-target", "completion")
				},
			},
			{
				name: "cross workspace legacy edge",
				seed: func(t *testing.T, f *universalDAGMigrationFixture) {
					f.seedCanonicalOwners(t)
					f.insertMessages(t, migrationTaskA, 1)
					f.insertMessages(t, migrationTaskB, 1)
					f.insertLegacySegment(t, "edge-source-a", migrationProjectA, migrationTaskA, 1, 1, "task_messages", `[]`, nil)
					f.insertLegacySegment(t, "edge-target-b", migrationProjectB, migrationTaskB, 1, 1, "task_messages", `[]`, nil)
					f.insertLegacyEdge(t, migrationProjectA, "edge-source-a", "edge-target-b", "delegation")
				},
			},
			{
				name: "orphan legacy edge endpoint",
				seed: func(t *testing.T, f *universalDAGMigrationFixture) {
					f.seedCanonicalOwners(t)
					f.insertMessages(t, migrationTaskA, 1)
					f.insertLegacySegment(t, "edge-source-only", migrationProjectA, migrationTaskA, 1, 1, "task_messages", `[]`, nil)
					f.insertLegacyEdge(t, migrationProjectA, "edge-source-only", "missing-target", "mention")
				},
			},
		}

		for _, testCase := range cases {
			testCase := testCase
			t.Run(testCase.name, func(t *testing.T) {
				fixture := newUniversalDAGMigrationFixture(t)
				testCase.seed(t, fixture)
				before := fixture.fingerprint(t)

				_, migrationErr := fixture.conn.Exec(fixture.ctx, upSQL)
				if migrationErr == nil {
					t.Fatalf("migration 454 accepted unsafe fixture %q", testCase.name)
				}
				// A migration that opened a transaction may leave the session aborted.
				// ROLLBACK is harmless when no transaction remains active.
				_, _ = fixture.conn.Exec(context.Background(), "ROLLBACK")

				for _, secret := range []string{
					"task-message-payload-secret",
					"task-input-payload-secret",
					"task-output-payload-secret",
					"trajectory-payload-secret",
					"tensor-payload-secret",
				} {
					if strings.Contains(migrationErr.Error(), secret) {
						t.Fatalf("migration error disclosed payload %q: %v", secret, migrationErr)
					}
				}
				after := fixture.fingerprint(t)
				if after != before {
					t.Fatalf("failed migration changed catalog or data: before=%s after=%s error=%v", before, after, migrationErr)
				}
			})
		}
	})

	t.Run("down refuses irreversible canonical live data", func(t *testing.T) {
		fixture := newUniversalDAGMigrationFixture(t)
		fixture.seedCanonicalOwners(t)
		fixture.insertMessages(t, migrationTaskA, 1, 2)
		fixture.insertLegacySegment(t, "legacy-before-live", migrationProjectA, migrationTaskA, 1, 1, "task_messages", `[]`, nil)
		fixture.apply(t, "up", upSQL)
		fixture.insertCanonicalSegmentAndOutbox(t)

		beforeDelete := fixture.fingerprint(t)
		if _, err := fixture.conn.Exec(fixture.ctx, `
			DELETE FROM interaction_dag_segment WHERE segment_id='canonical-live'
		`); err == nil {
			t.Fatal("canonical segment physical deletion succeeded")
		}
		afterDelete := fixture.fingerprint(t)
		if afterDelete != beforeDelete {
			t.Fatalf("rejected canonical delete changed data: before=%s after=%s", beforeDelete, afterDelete)
		}

		// A canonical row cannot be relabeled as a migration-created legacy row,
		// have its outbox removed, and then pass the down-migration gate.
		tx, err := fixture.conn.Begin(fixture.ctx)
		if err != nil {
			t.Fatalf("begin canonical reclassification bypass: %v", err)
		}
		_, bypassErr := tx.Exec(fixture.ctx, `
			UPDATE interaction_dag_segment
			SET content_status='legacy_unverified',close_action_kind=NULL,
			    canonical_action_id=NULL,visible_action_key=NULL,publish_status=NULL,
			    publish_seq=NULL,graph_projection_eligible_at_event=false,
			    trainable_eligible=false
			WHERE segment_id='canonical-live'
		`)
		if bypassErr == nil {
			_, bypassErr = tx.Exec(fixture.ctx, `
				DELETE FROM interaction_dag_publish_outbox WHERE segment_id='canonical-live'
			`)
		}
		if bypassErr == nil {
			bypassErr = tx.Commit(fixture.ctx)
		} else {
			_ = tx.Rollback(fixture.ctx)
		}
		if bypassErr == nil {
			t.Fatal("canonical row was reclassified as legacy_unverified")
		}

		before := fixture.fingerprint(t)
		_, downErr := fixture.conn.Exec(fixture.ctx, downSQL)
		if downErr == nil {
			t.Fatal("down migration deleted canonical live data instead of refusing rollback")
		}
		_, _ = fixture.conn.Exec(context.Background(), "ROLLBACK")
		after := fixture.fingerprint(t)
		if after != before {
			t.Fatalf("refused down migration changed catalog or data: before=%s after=%s error=%v", before, after, downErr)
		}
	})
}

func readUniversalDAGMigration454(t *testing.T) (upSQL, downSQL string) {
	t.Helper()
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate universal DAG migration test")
	}
	migrationsDir := filepath.Join(filepath.Dir(testFile), "..", "..", "migrations")
	readRequired := func(name string) string {
		t.Helper()
		path := filepath.Join(migrationsDir, name)
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("migration 454 is required; read %s: %v", path, err)
		}
		return string(contents)
	}
	return readRequired("476_universal_interaction_dag.up.sql"),
		readRequired("476_universal_interaction_dag.down.sql")
}

func newUniversalDAGMigrationFixture(t *testing.T) *universalDAGMigrationFixture {
	t.Helper()
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)

	schema := fmt.Sprintf("universal_dag_migration_test_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create private migration schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		if _, err := pool.Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE"); err != nil {
			t.Logf("drop private migration schema %s: %v", schema, err)
		}
	})

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire private migration connection: %v", err)
	}
	t.Cleanup(conn.Release)
	if _, err := conn.Exec(ctx, "SET search_path TO "+quotedSchema); err != nil {
		t.Fatalf("set private migration search_path: %v", err)
	}
	if _, err := conn.Exec(ctx, universalDAGMigrationLegacySchema); err != nil {
		t.Fatalf("create pre-454 schema: %v", err)
	}
	return &universalDAGMigrationFixture{ctx: ctx, pool: pool, conn: conn, schema: schema}
}

func (f *universalDAGMigrationFixture) apply(t *testing.T, direction, sql string) {
	t.Helper()
	if _, err := f.conn.Exec(f.ctx, sql); err != nil {
		_, _ = f.conn.Exec(context.Background(), "ROLLBACK")
		t.Fatalf("apply migration 454 %s: %v", direction, err)
	}
}

func (f *universalDAGMigrationFixture) seedCanonicalOwners(t *testing.T) {
	t.Helper()
	if _, err := f.conn.Exec(f.ctx, `
		INSERT INTO workspace(id) VALUES ($1),($2);
		INSERT INTO project(id,workspace_id) VALUES ($3,$1),($4,$2);
		INSERT INTO channel(id,workspace_id,project_id) VALUES ($5,$1,$3),($6,$2,$4);
		INSERT INTO agent_inbox_event(id,workspace_id,channel_id) VALUES ($7,$1,$5),($8,$2,$6);
		INSERT INTO env_dispatch_run(project_id,workspace_id,run_id) VALUES ($3,$1,$9),($4,$2,$10);
		INSERT INTO env_dispatch_run_agent(run_agent_id,run_id) VALUES ($11,$9),($12,$10);
		INSERT INTO env_dispatch_resident_turn(turn_id,run_id,run_agent_id) VALUES ($13,$9,$11),($14,$10,$12);
		INSERT INTO pi_provider_call(call_id,run_id,run_agent_id,turn_id,call_ordinal) VALUES
		  ('call-a-1',$9,$11,$13,1),('call-a-2',$9,$11,$13,2),('call-b-1',$10,$12,$14,1)
	`, pgx.QueryExecModeSimpleProtocol, migrationWSA, migrationWSB, migrationProjectA, migrationProjectB,
		migrationChannelA, migrationChannelB, migrationTaskA, migrationTaskB,
		migrationRunA, migrationRunB, migrationRunAgentA, migrationRunAgentB,
		migrationTurnA, migrationTurnB); err != nil {
		t.Fatalf("seed canonical owners: %v", err)
	}
}

func (f *universalDAGMigrationFixture) insertMessages(t *testing.T, taskID string, sequences ...int) {
	t.Helper()
	for _, sequence := range sequences {
		if _, err := f.conn.Exec(f.ctx, `
			INSERT INTO task_message(task_id,seq,content,input,output)
			VALUES ($1,$2,'task-message-payload-secret',
			        '{"secret":"task-input-payload-secret"}'::jsonb,
			        'task-output-payload-secret')
		`, taskID, sequence); err != nil {
			t.Fatalf("insert task message %s/%d: %v", taskID, sequence, err)
		}
	}
}

func (f *universalDAGMigrationFixture) insertLegacySegment(
	t *testing.T,
	segmentID, projectID, taskID string,
	startSeq, endSeq int,
	source, trajectory string,
	tensorRef any,
) {
	t.Helper()
	var trajectoryID any
	if source == "areal_tensor" {
		trajectoryID = int64(1)
	}
	if _, err := f.conn.Exec(f.ctx, `
		INSERT INTO interaction_dag_segment(
		  segment_id,project_id,agent_run_id,start_seq,end_seq,
		  trajectory_id,tensor_ref,trajectory_source,trajectory
		) VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8,$9::jsonb)
	`, segmentID, projectID, taskID, startSeq, endSeq,
		trajectoryID, tensorRef, source, trajectory); err != nil {
		t.Fatalf("insert legacy segment %s: %v", segmentID, err)
	}
}

func (f *universalDAGMigrationFixture) insertLegacyEdge(
	t *testing.T,
	projectID, sourceSegmentID, targetSegmentID, edgeType string,
) {
	t.Helper()
	if _, err := f.conn.Exec(f.ctx, `
		INSERT INTO interaction_dag_edge(project_id,src_segment_id,dst_segment_id,type)
		VALUES ($1,$2,$3,$4)
	`, projectID, sourceSegmentID, targetSegmentID, edgeType); err != nil {
		t.Fatalf("insert legacy edge: %v", err)
	}
}

func (f *universalDAGMigrationFixture) insertCanonicalSegmentAndOutbox(t *testing.T) {
	t.Helper()
	tx, err := f.conn.Begin(f.ctx)
	if err != nil {
		t.Fatalf("begin canonical segment transaction: %v", err)
	}
	defer tx.Rollback(f.ctx)
	if _, err := tx.Exec(f.ctx, `
		INSERT INTO interaction_dag_segment (
		  workspace_id,segment_id,agent_run_id,generation,
		  project_id_at_event,channel_id_at_event,start_seq,end_seq,
		  close_action_kind,canonical_action_id,visible_action_key,
		  memory_type_at_event,graph_projection_eligible_at_event,
		  trajectory_source,derivative,trainable_eligible,publish_status,content_status,
		  sanitizer_version,policy_version,provider_capture_status
		) VALUES (
		  $1,'canonical-live',$2,2,
		  $3,$4,2,2,
		  'message',$5,'message:canonical-live',
		  'graph',true,'task_messages',false,true,'pending','pending',
		  'dag-redaction-v1','universal-dag-v1','not_expected'
		)
	`, migrationWSA, migrationTaskA, migrationProjectA, migrationChannelA,
		"80000000-0000-4000-8000-000000000001"); err != nil {
		t.Fatalf("insert canonical live segment: %v", err)
	}
	if _, err := tx.Exec(f.ctx, `
		INSERT INTO interaction_dag_publish_outbox
		  (workspace_id,segment_id,request_hash,status,attempts)
		VALUES ($1,'canonical-live','sha256:canonical-live','pending',0)
	`, migrationWSA); err != nil {
		t.Fatalf("insert canonical live outbox: %v", err)
	}
	if _, err := tx.Exec(f.ctx, `SET CONSTRAINTS ALL IMMEDIATE`); err != nil {
		t.Fatalf("validate canonical live segment/outbox: %v", err)
	}
	if err := tx.Commit(f.ctx); err != nil {
		t.Fatalf("commit canonical live segment/outbox: %v", err)
	}
}

func assertUniversalDAGMigrationSchema(t *testing.T, f *universalDAGMigrationFixture) {
	t.Helper()
	for table, columns := range map[string][]string{
		"interaction_dag_segment": {
			"workspace_id", "agent_run_id", "generation", "project_id_at_event",
			"channel_id_at_event", "route_generation_at_event", "memory_type_at_event",
			"graph_projection_eligible_at_event", "close_action_kind", "canonical_action_id",
			"visible_action_key", "derivative", "trainable_eligible", "publish_status",
			"content_status", "publish_seq", "sanitizer_version", "policy_version",
			"provider_capture_status", "provider_capture_id", "provider_capture_version",
			"provider_capture_correlation_key",
			"run_id", "run_agent_id",
		},
		"interaction_dag_edge_sequence": {
			"workspace_id", "next_edge_seq", "updated_at",
		},
		"interaction_dag_edge": {
			"workspace_id", "edge_seq", "src_segment_id", "dst_segment_id", "type", "trigger_message_id",
		},
		"interaction_dag_task_cursor": {
			"workspace_id", "agent_run_id", "next_generation", "open_start_seq", "last_closed_seq",
		},
		"interaction_dag_publish_outbox": {
			"workspace_id", "segment_id", "request_hash", "status", "attempts",
			"next_attempt_at", "lease_owner", "lease_expires_at", "last_error",
		},
		"interaction_dag_universal_provider_call": {
			"segment_id", "provider_call_id", "role", "ordinal", "run_id", "run_agent_id", "capture_id",
		},
	} {
		for _, column := range columns {
			var exists bool
			if err := f.conn.QueryRow(f.ctx, `
				SELECT EXISTS (
				  SELECT 1 FROM information_schema.columns
				  WHERE table_schema=current_schema() AND table_name=$1 AND column_name=$2
				)
			`, table, column).Scan(&exists); err != nil {
				t.Fatalf("inspect canonical column %s.%s: %v", table, column, err)
			}
			if !exists {
				t.Fatalf("migration 454 did not create canonical column %s.%s", table, column)
			}
		}
	}

	if !f.relationExists(t, "interaction_dag_segment_canonical_range_guard_idx") {
		t.Fatal("migration 454 did not create the canonical range guard index")
	}
	if !f.relationExists(t, "task_message_task_id_seq_454_uidx") {
		t.Fatal("migration 454 did not create task-message identity uniqueness")
	}

	definitions := strings.ToLower(f.catalogDefinitions(t))
	for _, required := range []string{
		"legacy_unverified",
		"metadata_only",
		"not_expected", "pending", "finalized", "conflict",
		"continues", "responds_to", "delegates_to", "mentions",
		"shared_producer", "owned", "audit",
		"redaction_failed", "rejected_scope", "dead_letter", "retracted",
	} {
		if !strings.Contains(definitions, required) {
			t.Fatalf("migration 454 catalog does not enforce %q", required)
		}
	}
}

func (f *universalDAGMigrationFixture) relationExists(t *testing.T, relation string) bool {
	t.Helper()
	var exists bool
	if err := f.conn.QueryRow(f.ctx, `
		SELECT to_regclass(format('%I.%I', current_schema(), $1)) IS NOT NULL
	`, pgx.QueryExecModeSimpleProtocol, relation).Scan(&exists); err != nil {
		t.Fatalf("inspect relation %s: %v", relation, err)
	}
	return exists
}

func (f *universalDAGMigrationFixture) constraintExists(t *testing.T, constraint string) bool {
	t.Helper()
	var exists bool
	if err := f.conn.QueryRow(f.ctx, `
		SELECT EXISTS (
		  SELECT 1 FROM pg_constraint
		  WHERE connamespace = current_schema()::regnamespace
		    AND conname = $1
		)
	`, constraint).Scan(&exists); err != nil {
		t.Fatalf("inspect constraint %s: %v", constraint, err)
	}
	return exists
}

func (f *universalDAGMigrationFixture) constraintDefinition(t *testing.T, constraint string) string {
	t.Helper()
	var definition string
	if err := f.conn.QueryRow(f.ctx, `
		SELECT pg_get_constraintdef(oid)
		FROM pg_constraint
		WHERE connamespace = current_schema()::regnamespace
		  AND conname = $1
	`, constraint).Scan(&definition); err != nil {
		t.Fatalf("read constraint %s: %v", constraint, err)
	}
	return definition
}

func (f *universalDAGMigrationFixture) constraintAndIndexDefinitions(t *testing.T) string {
	t.Helper()
	var definitions string
	if err := f.conn.QueryRow(f.ctx, `
		WITH definitions AS (
		  SELECT 'constraint:' || relation.relname || ':' || con.conname || ':' ||
		         pg_get_constraintdef(con.oid) AS value
		  FROM pg_constraint AS con
		  JOIN pg_class AS relation ON relation.oid=con.conrelid
		  JOIN pg_namespace AS namespace ON namespace.oid=relation.relnamespace
		  WHERE namespace.nspname=current_schema()
		  UNION ALL
		  SELECT 'index:' || relation.relname || ':' || pg_get_indexdef(idx.indexrelid)
		  FROM pg_index AS idx
		  JOIN pg_class AS relation ON relation.oid=idx.indrelid
		  JOIN pg_namespace AS namespace ON namespace.oid=relation.relnamespace
		  WHERE namespace.nspname=current_schema()
		)
		SELECT COALESCE(string_agg(value,E'\n' ORDER BY value),'') FROM definitions
	`).Scan(&definitions); err != nil {
		t.Fatalf("read private schema constraint/index catalog: %v", err)
	}
	return definitions
}

func (f *universalDAGMigrationFixture) catalogDefinitions(t *testing.T) string {
	t.Helper()
	var definitions string
	if err := f.conn.QueryRow(f.ctx, `
		WITH definitions AS (
		  SELECT 'relation:' || c.relname || ':' || c.relkind::text AS value
		  FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
		  WHERE n.nspname=current_schema()
		  UNION ALL
		  SELECT 'column:' || c.relname || ':' || a.attname || ':' ||
		         format_type(a.atttypid,a.atttypmod) || ':' || a.attnotnull::text || ':' ||
		         COALESCE(pg_get_expr(d.adbin,d.adrelid),'')
		  FROM pg_attribute a
		  JOIN pg_class c ON c.oid=a.attrelid
		  JOIN pg_namespace n ON n.oid=c.relnamespace
		  LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum
		  WHERE n.nspname=current_schema() AND a.attnum>0 AND NOT a.attisdropped
		  UNION ALL
		  SELECT 'constraint:' || c.relname || ':' || con.conname || ':' || pg_get_constraintdef(con.oid)
		  FROM pg_constraint con
		  JOIN pg_class c ON c.oid=con.conrelid
		  JOIN pg_namespace n ON n.oid=c.relnamespace
		  WHERE n.nspname=current_schema()
		  UNION ALL
		  SELECT 'index:' || c.relname || ':' || pg_get_indexdef(i.indexrelid)
		  FROM pg_index i
		  JOIN pg_class c ON c.oid=i.indrelid
		  JOIN pg_namespace n ON n.oid=c.relnamespace
		  WHERE n.nspname=current_schema()
		  UNION ALL
		  SELECT 'trigger:' || c.relname || ':' || pg_get_triggerdef(t.oid)
		  FROM pg_trigger t
		  JOIN pg_class c ON c.oid=t.tgrelid
		  JOIN pg_namespace n ON n.oid=c.relnamespace
		  WHERE n.nspname=current_schema() AND NOT t.tgisinternal
		  UNION ALL
		  SELECT 'function:' || p.proname || ':' || pg_get_functiondef(p.oid)
		  FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
		  WHERE n.nspname=current_schema()
		)
		SELECT COALESCE(string_agg(value,E'\n' ORDER BY value),'') FROM definitions
	`).Scan(&definitions); err != nil {
		t.Fatalf("read private schema catalog: %v", err)
	}
	return definitions
}

func (f *universalDAGMigrationFixture) fingerprint(t *testing.T) string {
	t.Helper()
	var tableNames []string
	rows, err := f.conn.Query(f.ctx, `
		SELECT c.relname
		FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
		WHERE n.nspname=current_schema() AND c.relkind IN ('r','p')
		ORDER BY c.relname
	`)
	if err != nil {
		t.Fatalf("list private schema tables: %v", err)
	}
	for rows.Next() {
		var tableName string
		if err := rows.Scan(&tableName); err != nil {
			rows.Close()
			t.Fatalf("scan private schema table: %v", err)
		}
		tableNames = append(tableNames, tableName)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("read private schema tables: %v", err)
	}
	rows.Close()

	var snapshot strings.Builder
	snapshot.WriteString(f.catalogDefinitions(t))
	for _, tableName := range tableNames {
		quotedTable := pgx.Identifier{tableName}.Sanitize()
		var data string
		query := `SELECT COALESCE(jsonb_agg(row_data ORDER BY row_data::text),'[]'::jsonb)::text
		          FROM (SELECT to_jsonb(t) AS row_data FROM ` + quotedTable + ` AS t) rows`
		if err := f.conn.QueryRow(f.ctx, query).Scan(&data); err != nil {
			t.Fatalf("snapshot private table %s: %v", tableName, err)
		}
		snapshot.WriteString("\ntable:")
		snapshot.WriteString(tableName)
		snapshot.WriteByte(':')
		snapshot.WriteString(data)
	}
	digest := sha256.Sum256([]byte(snapshot.String()))
	return hex.EncodeToString(digest[:])
}
