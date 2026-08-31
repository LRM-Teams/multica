package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const universalDAGLegacySchema = `
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
  FOREIGN KEY (run_id, run_agent_id) REFERENCES env_dispatch_run_agent(run_id, run_agent_id)
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
CREATE TABLE interaction_dag_diagnosis_run (
  run_id text PRIMARY KEY,
  project_id text NOT NULL,
  status text NOT NULL,
  completed_at timestamptz,
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE interaction_dag_diagnosis_segment (
  run_id text NOT NULL REFERENCES interaction_dag_diagnosis_run(run_id),
  segment_id text NOT NULL,
  ordinal integer NOT NULL,
  expected_reward_seqs jsonb NOT NULL DEFAULT '[]'::jsonb
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
	universalWSA       = "10000000-0000-4000-8000-000000000001"
	universalWSB       = "10000000-0000-4000-8000-000000000002"
	universalProjectA  = "20000000-0000-4000-8000-000000000001"
	universalProjectB  = "20000000-0000-4000-8000-000000000002"
	universalChannelA  = "30000000-0000-4000-8000-000000000001"
	universalChannelB  = "30000000-0000-4000-8000-000000000002"
	universalTaskA     = "40000000-0000-4000-8000-000000000001"
	universalTaskB     = "40000000-0000-4000-8000-000000000002"
	universalRunA      = "50000000-0000-4000-8000-000000000001"
	universalRunB      = "50000000-0000-4000-8000-000000000002"
	universalRunAgentA = "60000000-0000-4000-8000-000000000001"
	universalRunAgentB = "60000000-0000-4000-8000-000000000002"
	universalTurnA     = "70000000-0000-4000-8000-000000000001"
	universalTurnB     = "70000000-0000-4000-8000-000000000002"
)

func TestUniversalDAGSchema(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, conn := openUniversalDAGServiceSchema(t, ctx)
	defer conn.Release()

	if _, err := conn.Exec(ctx, universalDAGLegacySchema); err != nil {
		t.Fatalf("create pre-454 schema: %v", err)
	}
	applyUniversalDAGMigrationIfPresent(t, ctx, conn)

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
		"interaction_dag_segment_generation_sequence": {
			"workspace_id", "agent_run_id", "next_generation", "updated_at",
		},
		"interaction_dag_edge_sequence": {
			"workspace_id", "next_edge_seq", "updated_at",
		},
		"interaction_dag_edge": {
			"workspace_id", "edge_seq", "src_segment_id", "dst_segment_id", "type",
			"trigger_message_id",
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
			assertUniversalDAGColumn(t, ctx, conn, table, column)
		}
	}
	assertUniversalDAGNotNull(t, ctx, conn, "interaction_dag_segment", "workspace_id")
	assertUniversalDAGNotNull(t, ctx, conn, "interaction_dag_segment", "agent_run_id")
	assertUniversalDAGNotNull(t, ctx, conn, "interaction_dag_segment", "generation")

	seedUniversalDAGCanonicalOwners(t, ctx, conn)

	t.Run("legacy unverified exception", func(t *testing.T) {
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatalf("begin legacy segment transaction: %v", err)
		}
		defer tx.Rollback(ctx)
		if err := execUniversalDAGLegacySegment(ctx, tx, "legacy-unverified", 2, false, false); err != nil {
			t.Fatalf("insert legacy_unverified segment: %v", err)
		}
		if _, err := tx.Exec(ctx, `SET CONSTRAINTS ALL IMMEDIATE`); err != nil {
			t.Fatalf("validate legacy_unverified segment without outbox: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit legacy_unverified segment without outbox: %v", err)
		}

		var closeActionKind, canonicalActionID, visibleActionKey, publishStatus *string
		var publishSeq *int64
		var graphEligible, trainableEligible bool
		if err := conn.QueryRow(ctx, `
			SELECT close_action_kind, canonical_action_id::text, visible_action_key,
			       publish_status, publish_seq,
			       graph_projection_eligible_at_event, trainable_eligible
			FROM interaction_dag_segment
			WHERE workspace_id=$1 AND segment_id='legacy-unverified'
		`, universalWSB).Scan(
			&closeActionKind, &canonicalActionID, &visibleActionKey, &publishStatus, &publishSeq,
			&graphEligible, &trainableEligible,
		); err != nil {
			t.Fatalf("read legacy_unverified segment: %v", err)
		}
		if closeActionKind != nil || canonicalActionID != nil || visibleActionKey != nil ||
			publishStatus != nil || publishSeq != nil {
			t.Fatalf("legacy_unverified segment acquired canonical/publish identity: close=%v canonical=%v visible=%v status=%v seq=%v",
				closeActionKind, canonicalActionID, visibleActionKey, publishStatus, publishSeq)
		}
		if graphEligible || trainableEligible {
			t.Fatalf("legacy_unverified segment is eligible: graph=%t trainable=%t", graphEligible, trainableEligible)
		}
		var outboxCount int
		if err := conn.QueryRow(ctx, `
			SELECT count(*) FROM interaction_dag_publish_outbox
			WHERE workspace_id=$1 AND segment_id='legacy-unverified'
		`, universalWSB).Scan(&outboxCount); err != nil {
			t.Fatalf("count legacy_unverified outbox rows: %v", err)
		}
		if outboxCount != 0 {
			t.Fatalf("legacy_unverified segment acquired %d outbox rows", outboxCount)
		}
		if _, err := conn.Exec(ctx, `
			UPDATE interaction_dag_segment
			SET tensor_ref=jsonb_build_object('artifact_ref','opaque')
			WHERE segment_id='legacy-unverified'
		`); err == nil {
			t.Fatal("legacy_unverified task-message source accepted a tensor mutation")
		}

		for _, eligibility := range []struct {
			name             string
			generation       int64
			graph, trainable bool
		}{
			{name: "graph projection", generation: 3, graph: true},
			{name: "training", generation: 4, trainable: true},
		} {
			eligibility := eligibility
			t.Run(eligibility.name, func(t *testing.T) {
				assertUniversalDAGTransactionRejected(t, ctx, conn, func(tx pgx.Tx) error {
					return execUniversalDAGLegacySegment(ctx, tx, "legacy-invalid-"+strings.ReplaceAll(eligibility.name, " ", "-"),
						eligibility.generation, eligibility.graph, eligibility.trainable)
				})
			})
		}
	})

	t.Run("unscoped segment and atomic outbox", func(t *testing.T) {
		insertUniversalDAGSegmentAndOutbox(t, ctx, conn, universalDAGSegment{
			WorkspaceID: universalWSA, SegmentID: "segment-unscoped", AgentRunID: universalTaskA,
			Generation: 1, StartSeq: int32Pointer(1), EndSeq: int32Pointer(2),
			CloseActionKind: "message", CanonicalActionID: "80000000-0000-4000-8000-000000000001",
			VisibleActionKey: "message:unscoped", ContentStatus: "pending", PublishStatus: "pending",
		})
		var projectID, channelID *string
		if err := conn.QueryRow(ctx, `
			SELECT project_id_at_event::text, channel_id_at_event::text
			FROM interaction_dag_segment WHERE workspace_id=$1 AND segment_id='segment-unscoped'
		`, universalWSA).Scan(&projectID, &channelID); err != nil {
			t.Fatalf("read unscoped segment: %v", err)
		}
		if projectID != nil || channelID != nil {
			t.Fatalf("unscoped segment acquired scope: project=%v channel=%v", projectID, channelID)
		}
	})

	t.Run("workspace task project and channel consistency", func(t *testing.T) {
		assertUniversalDAGPairRejected(t, ctx, conn, universalDAGSegment{
			WorkspaceID: universalWSB, SegmentID: "wrong-task-workspace", AgentRunID: universalTaskA,
			Generation: 1, StartSeq: int32Pointer(1), EndSeq: int32Pointer(1),
			CloseActionKind: "terminal", VisibleActionKey: "terminal:wrong-task-workspace",
			ContentStatus: "pending", PublishStatus: "pending",
		})
		assertUniversalDAGPairRejected(t, ctx, conn, universalDAGSegment{
			WorkspaceID: universalWSA, SegmentID: "wrong-project-workspace", AgentRunID: universalTaskA,
			Generation: 2, ProjectID: universalProjectB, StartSeq: int32Pointer(3), EndSeq: int32Pointer(3),
			CloseActionKind: "terminal", VisibleActionKey: "terminal:wrong-project-workspace",
			ContentStatus: "pending", PublishStatus: "pending",
		})
		assertUniversalDAGPairRejected(t, ctx, conn, universalDAGSegment{
			WorkspaceID: universalWSA, SegmentID: "wrong-channel-workspace", AgentRunID: universalTaskA,
			Generation: 2, ChannelID: universalChannelB, StartSeq: int32Pointer(3), EndSeq: int32Pointer(3),
			CloseActionKind: "terminal", VisibleActionKey: "terminal:wrong-channel-workspace",
			ContentStatus: "pending", PublishStatus: "pending",
		})
		assertUniversalDAGPairRejected(t, ctx, conn, universalDAGSegment{
			WorkspaceID: universalWSA, SegmentID: "channel-project-mismatch", AgentRunID: universalTaskA,
			Generation: 2, ProjectID: universalProjectB, ChannelID: universalChannelA,
			StartSeq: int32Pointer(3), EndSeq: int32Pointer(3), CloseActionKind: "terminal",
			VisibleActionKey: "terminal:channel-project-mismatch", ContentStatus: "pending", PublishStatus: "pending",
		})
	})

	t.Run("generation range action and metadata constraints", func(t *testing.T) {
		assertUniversalDAGPairRejected(t, ctx, conn, universalDAGSegment{
			WorkspaceID: universalWSA, SegmentID: "duplicate-generation", AgentRunID: universalTaskA,
			Generation: 1, StartSeq: int32Pointer(3), EndSeq: int32Pointer(3),
			CloseActionKind: "terminal", VisibleActionKey: "terminal:duplicate-generation",
			ContentStatus: "pending", PublishStatus: "pending",
		})
		assertUniversalDAGPairRejected(t, ctx, conn, universalDAGSegment{
			WorkspaceID: universalWSA, SegmentID: "missing-canonical-range", AgentRunID: universalTaskA,
			Generation: 2, StartSeq: int32Pointer(3), EndSeq: int32Pointer(4),
			CloseActionKind: "terminal", VisibleActionKey: "terminal:missing-canonical-range",
			ContentStatus: "pending", PublishStatus: "pending",
		})
		assertUniversalDAGPairRejected(t, ctx, conn, universalDAGSegment{
			WorkspaceID: universalWSA, SegmentID: "message-without-action", AgentRunID: universalTaskA,
			Generation: 2, StartSeq: int32Pointer(3), EndSeq: int32Pointer(3),
			CloseActionKind: "message", VisibleActionKey: "message:without-action",
			ContentStatus: "pending", PublishStatus: "pending",
		})
		assertUniversalDAGPairRejected(t, ctx, conn, universalDAGSegment{
			WorkspaceID: universalWSA, SegmentID: "inverted-range", AgentRunID: universalTaskA,
			Generation: 3, StartSeq: int32Pointer(3), EndSeq: int32Pointer(2),
			CloseActionKind: "terminal", VisibleActionKey: "terminal:inverted-range",
			ContentStatus: "pending", PublishStatus: "pending",
		})
		assertUniversalDAGPairRejected(t, ctx, conn, universalDAGSegment{
			WorkspaceID: universalWSA, SegmentID: "negative-range", AgentRunID: universalTaskA,
			Generation: 3, StartSeq: int32Pointer(-1), EndSeq: int32Pointer(-1),
			CloseActionKind: "terminal", VisibleActionKey: "terminal:negative-range",
			ContentStatus: "pending", PublishStatus: "pending",
		})
		assertUniversalDAGPairRejected(t, ctx, conn, universalDAGSegment{
			WorkspaceID: universalWSA, SegmentID: "zero-terminal-range", AgentRunID: universalTaskA,
			Generation: 3, StartSeq: int32Pointer(0), EndSeq: int32Pointer(0),
			CloseActionKind: "terminal", VisibleActionKey: "terminal:zero-range",
			ContentStatus: "pending", PublishStatus: "pending",
		})
		insertUniversalDAGSegmentAndOutbox(t, ctx, conn, universalDAGSegment{
			WorkspaceID: universalWSA, SegmentID: "metadata-only", AgentRunID: universalTaskA,
			Generation: 2, StartSeq: int32Pointer(0), EndSeq: int32Pointer(0),
			CloseActionKind: "metadata_only", ContentStatus: "empty", PublishStatus: "pending",
		})
		assertUniversalDAGPairRejected(t, ctx, conn, universalDAGSegment{
			WorkspaceID: universalWSA, SegmentID: "invalid-provider-capture-status", AgentRunID: universalTaskA,
			Generation: 3, StartSeq: int32Pointer(3), EndSeq: int32Pointer(3),
			CloseActionKind: "terminal", VisibleActionKey: "terminal:invalid-provider-capture-status",
			ContentStatus: "pending", PublishStatus: "pending", ProviderCaptureStatus: "complete",
		})
		assertUniversalDAGPairRejected(t, ctx, conn, universalDAGSegment{
			WorkspaceID: universalWSA, SegmentID: "duplicate-visible-action", AgentRunID: universalTaskA,
			Generation: 3, StartSeq: int32Pointer(3), EndSeq: int32Pointer(3),
			CloseActionKind: "terminal", VisibleActionKey: "message:unscoped",
			ContentStatus: "pending", PublishStatus: "pending",
		})
	})

	t.Run("canonical initial lifecycle states are constrained", func(t *testing.T) {
		for _, publishStatus := range []string{
			"processing", "retry", "published", "redaction_failed",
			"rejected_scope", "dead_letter", "retracted",
		} {
			assertUniversalDAGPairRejected(t, ctx, conn, universalDAGSegment{
				WorkspaceID: universalWSA, SegmentID: "initial-publish-" + publishStatus,
				AgentRunID: universalTaskA, Generation: 6,
				StartSeq: int32Pointer(3), EndSeq: int32Pointer(3),
				CloseActionKind: "terminal", VisibleActionKey: "initial-publish:" + publishStatus,
				ContentStatus: "pending", PublishStatus: publishStatus,
			})
		}
		for _, contentStatus := range []string{
			"published", "empty", "redaction_failed", "rejected_scope", "dead_letter", "retracted",
		} {
			assertUniversalDAGPairRejected(t, ctx, conn, universalDAGSegment{
				WorkspaceID: universalWSA, SegmentID: "initial-content-" + contentStatus,
				AgentRunID: universalTaskA, Generation: 6,
				StartSeq: int32Pointer(3), EndSeq: int32Pointer(3),
				CloseActionKind: "terminal", VisibleActionKey: "initial-content:" + contentStatus,
				ContentStatus: contentStatus, PublishStatus: "pending",
			})
		}
		for _, metadata := range []struct {
			name                     string
			publishSeq               *int64
			publishedAt, retractedAt bool
		}{
			{name: "publish-sequence", publishSeq: int64Pointer(1)},
			{name: "published-at", publishedAt: true},
			{name: "retracted-at", retractedAt: true},
		} {
			assertUniversalDAGPairRejected(t, ctx, conn, universalDAGSegment{
				WorkspaceID: universalWSA, SegmentID: "initial-metadata-" + metadata.name,
				AgentRunID: universalTaskA, Generation: 6,
				StartSeq: int32Pointer(3), EndSeq: int32Pointer(3),
				CloseActionKind: "terminal", VisibleActionKey: "initial-metadata:" + metadata.name,
				ContentStatus: "pending", PublishStatus: "pending",
				PublishSeq: metadata.publishSeq, PublishedAt: metadata.publishedAt, RetractedAt: metadata.retractedAt,
			})
		}
		for _, captureStatus := range []string{"finalized", "conflict"} {
			assertUniversalDAGPairRejected(t, ctx, conn, universalDAGSegment{
				WorkspaceID: universalWSA, SegmentID: "initial-capture-" + captureStatus,
				AgentRunID: universalTaskA, Generation: 6,
				StartSeq: int32Pointer(3), EndSeq: int32Pointer(3),
				CloseActionKind: "terminal", VisibleActionKey: "initial-capture:" + captureStatus,
				ContentStatus: "pending", PublishStatus: "pending",
				ProviderCaptureStatus: captureStatus, ProviderCaptureID: "capture-initial",
				ProviderCaptureCorrelationKey: "correlation-initial",
			})
		}
	})

	t.Run("canonical publication fields remain mutable", func(t *testing.T) {
		insertUniversalDAGSegmentAndOutbox(t, ctx, conn, universalDAGSegment{
			WorkspaceID: universalWSA, SegmentID: "mutable-publication-segment", AgentRunID: universalTaskA,
			Generation: 20, StartSeq: int32Pointer(3), EndSeq: int32Pointer(3),
			CloseActionKind: "terminal", VisibleActionKey: "terminal:mutable-publication",
			ContentStatus: "pending", PublishStatus: "pending",
		})

		for name, statement := range map[string]string{
			"trajectory payload": `UPDATE interaction_dag_segment SET trajectory=jsonb_build_array(jsonb_build_object('sequence',3,'type','redacted')) WHERE segment_id='mutable-publication-segment'`,
			"tensor reference":   `UPDATE interaction_dag_segment SET tensor_ref=jsonb_build_object('artifact_ref','opaque') WHERE segment_id='mutable-publication-segment'`,
			"sanitizer version":  `UPDATE interaction_dag_segment SET sanitizer_version='dag-redaction-v2' WHERE segment_id='mutable-publication-segment'`,
			"policy version":     `UPDATE interaction_dag_segment SET policy_version='universal-dag-v2' WHERE segment_id='mutable-publication-segment'`,
		} {
			if _, err := conn.Exec(ctx, statement); err != nil {
				t.Fatalf("update committed canonical %s: %v", name, err)
			}
		}

		var allMutable bool
		if err := conn.QueryRow(ctx, `
			SELECT trajectory = jsonb_build_array(jsonb_build_object('sequence',3,'type','redacted'))
			       AND tensor_ref = jsonb_build_object('artifact_ref','opaque')
			       AND sanitizer_version = 'dag-redaction-v2'
			       AND policy_version = 'universal-dag-v2'
			FROM interaction_dag_segment
			WHERE segment_id='mutable-publication-segment'
		`).Scan(&allMutable); err != nil {
			t.Fatalf("verify mutable canonical publication fields: %v", err)
		}
		if !allMutable {
			t.Fatal("canonical publication field updates did not all persist")
		}
	})

	t.Run("segment provenance and lifecycle are immutable", func(t *testing.T) {
		insertUniversalDAGSegmentAndOutbox(t, ctx, conn, universalDAGSegment{
			WorkspaceID: universalWSA, SegmentID: "immutable-segment", AgentRunID: universalTaskA,
			Generation: 6, ProjectID: universalProjectA, ChannelID: universalChannelA,
			StartSeq: int32Pointer(1), EndSeq: int32Pointer(2), CloseActionKind: "message",
			CanonicalActionID: "80000000-0000-4000-8000-000000000006",
			VisibleActionKey:  "message:immutable", ContentStatus: "pending", PublishStatus: "pending",
			RunID: universalRunA, RunAgentID: universalRunAgentA,
			ProviderCaptureStatus: "pending", ProviderCaptureCorrelationKey: "correlation-immutable",
		})

		for name, statement := range map[string]string{
			"segment identity":      `UPDATE interaction_dag_segment SET segment_id='immutable-segment-rewritten' WHERE segment_id='immutable-segment'`,
			"workspace identity":    `UPDATE interaction_dag_segment SET workspace_id='10000000-0000-4000-8000-000000000002' WHERE segment_id='immutable-segment'`,
			"task identity":         `UPDATE interaction_dag_segment SET agent_run_id='40000000-0000-4000-8000-000000000002' WHERE segment_id='immutable-segment'`,
			"generation":            `UPDATE interaction_dag_segment SET generation=7 WHERE segment_id='immutable-segment'`,
			"range":                 `UPDATE interaction_dag_segment SET start_seq=2,end_seq=3 WHERE segment_id='immutable-segment'`,
			"project event scope":   `UPDATE interaction_dag_segment SET project_id_at_event=NULL WHERE segment_id='immutable-segment'`,
			"channel event scope":   `UPDATE interaction_dag_segment SET channel_id_at_event=NULL WHERE segment_id='immutable-segment'`,
			"route event scope":     `UPDATE interaction_dag_segment SET route_generation_at_event=7 WHERE segment_id='immutable-segment'`,
			"issue identity":        `UPDATE interaction_dag_segment SET issue_id='issue-rewritten' WHERE segment_id='immutable-segment'`,
			"source identity":       `UPDATE interaction_dag_segment SET trajectory_source='areal_tensor',trajectory_id=7,tensor_ref=jsonb_build_object('artifact_ref','opaque') WHERE segment_id='immutable-segment'`,
			"close action kind":     `UPDATE interaction_dag_segment SET close_action_kind='reaction' WHERE segment_id='immutable-segment'`,
			"canonical action":      `UPDATE interaction_dag_segment SET canonical_action_id='80000000-0000-4000-8000-000000000007' WHERE segment_id='immutable-segment'`,
			"visible action":        `UPDATE interaction_dag_segment SET visible_action_key='message:rewritten' WHERE segment_id='immutable-segment'`,
			"run identity":          `UPDATE interaction_dag_segment SET run_id=NULL,run_agent_id=NULL WHERE segment_id='immutable-segment'`,
			"memory type":           `UPDATE interaction_dag_segment SET memory_type_at_event='episodic' WHERE segment_id='immutable-segment'`,
			"graph eligibility":     `UPDATE interaction_dag_segment SET graph_projection_eligible_at_event=false WHERE segment_id='immutable-segment'`,
			"training eligibility":  `UPDATE interaction_dag_segment SET trainable_eligible=false WHERE segment_id='immutable-segment'`,
			"derivative provenance": `UPDATE interaction_dag_segment SET derivative=true WHERE segment_id='immutable-segment'`,
			"correlation":           `UPDATE interaction_dag_segment SET provider_capture_correlation_key='correlation-rewritten' WHERE segment_id='immutable-segment'`,
		} {
			if _, err := conn.Exec(ctx, statement); err == nil {
				t.Fatalf("committed segment %s mutation succeeded", name)
			}
		}

		var immutableCount int
		if err := conn.QueryRow(ctx, `
			SELECT count(*) FROM interaction_dag_segment
			WHERE segment_id='immutable-segment'
			  AND generation=6 AND start_seq=1 AND end_seq=2
			  AND route_generation_at_event IS NULL
			  AND issue_id IS NULL AND trajectory_source='task_messages'
			  AND trajectory_id IS NULL AND tensor_ref IS NULL
			  AND canonical_action_id='80000000-0000-4000-8000-000000000006'
			  AND run_id=$1 AND run_agent_id=$2
			  AND provider_capture_correlation_key='correlation-immutable'
		`, universalRunA, universalRunAgentA).Scan(&immutableCount); err != nil {
			t.Fatalf("read immutable segment after rejected updates: %v", err)
		}
		if immutableCount != 1 {
			t.Fatal("rejected segment mutation changed committed provenance")
		}

		if _, err := conn.Exec(ctx, `
			UPDATE interaction_dag_segment
			SET publish_status='processing',updated_at=now()
			WHERE segment_id='segment-unscoped'
		`); err != nil {
			t.Fatalf("start publish lifecycle: %v", err)
		}
		for name, statement := range map[string]string{
			"missing publish sequence":     `UPDATE interaction_dag_segment SET publish_status='published',content_status='published',published_at=now() WHERE segment_id='segment-unscoped'`,
			"nonpositive publish sequence": `UPDATE interaction_dag_segment SET publish_status='published',content_status='published',publish_seq=0,published_at=now() WHERE segment_id='segment-unscoped'`,
			"missing published timestamp":  `UPDATE interaction_dag_segment SET publish_status='published',content_status='published',publish_seq=1 WHERE segment_id='segment-unscoped'`,
		} {
			if _, err := conn.Exec(ctx, statement); err == nil {
				t.Fatalf("published state with %s succeeded", name)
			}
		}
		if _, err := conn.Exec(ctx, `
			UPDATE interaction_dag_segment
			SET publish_status='published',content_status='published',publish_seq=1,
			    published_at=now(),updated_at=now()
			WHERE segment_id='segment-unscoped'
		`); err != nil {
			t.Fatalf("complete publish lifecycle: %v", err)
		}
		for name, statement := range map[string]string{
			"publish status": `UPDATE interaction_dag_segment SET publish_status='retracted',updated_at=now() WHERE segment_id='segment-unscoped'`,
			"content status": `UPDATE interaction_dag_segment SET content_status='retracted',updated_at=now() WHERE segment_id='segment-unscoped'`,
			"both statuses":  `UPDATE interaction_dag_segment SET publish_status='retracted',content_status='retracted',updated_at=now() WHERE segment_id='segment-unscoped'`,
		} {
			if _, err := conn.Exec(ctx, statement); err == nil {
				t.Fatalf("%s reached retracted state without retracted_at", name)
			}
		}
		if _, err := conn.Exec(ctx, `
			UPDATE interaction_dag_segment
			SET publish_status='retracted',content_status='retracted',
			    retracted_at=now(),updated_at=now()
			WHERE segment_id='segment-unscoped'
		`); err != nil {
			t.Fatalf("retract published segment: %v", err)
		}
	})

	t.Run("segment outbox reciprocal transaction invariant", func(t *testing.T) {
		assertUniversalDAGTransactionRejected(t, ctx, conn, func(tx pgx.Tx) error {
			return execUniversalDAGSegment(ctx, tx, universalDAGSegment{
				WorkspaceID: universalWSB, SegmentID: "segment-without-outbox", AgentRunID: universalTaskB,
				Generation: 1, StartSeq: int32Pointer(1), EndSeq: int32Pointer(1),
				CloseActionKind: "terminal", VisibleActionKey: "terminal:without-outbox",
				ContentStatus: "pending", PublishStatus: "pending",
			})
		})
		assertUniversalDAGTransactionRejected(t, ctx, conn, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `
				INSERT INTO interaction_dag_publish_outbox
				  (workspace_id,segment_id,request_hash,status,attempts)
				VALUES ($1,'orphan-outbox','sha256:orphan','pending',0)
			`, universalWSA)
			return err
		})
	})

	t.Run("outbox creation identity is immutable", func(t *testing.T) {
		const (
			segmentA = "outbox-identity-a"
			segmentB = "outbox-identity-b"
		)
		insertUniversalDAGSegmentAndOutbox(t, ctx, conn, universalDAGSegment{
			WorkspaceID: universalWSA, SegmentID: segmentA, AgentRunID: universalTaskA,
			Generation: 30, StartSeq: int32Pointer(3), EndSeq: int32Pointer(3),
			CloseActionKind: "terminal", VisibleActionKey: "terminal:" + segmentA,
			ContentStatus: "pending", PublishStatus: "pending",
		})

		t.Run("reassignment rolls back destination segment", func(t *testing.T) {
			tx, err := conn.Begin(ctx)
			if err != nil {
				t.Fatalf("begin outbox reassignment transaction: %v", err)
			}
			defer tx.Rollback(ctx)
			if err := execUniversalDAGSegment(ctx, tx, universalDAGSegment{
				WorkspaceID: universalWSA, SegmentID: segmentB, AgentRunID: universalTaskA,
				Generation: 31, StartSeq: int32Pointer(3), EndSeq: int32Pointer(3),
				CloseActionKind: "terminal", VisibleActionKey: "terminal:" + segmentB,
				ContentStatus: "pending", PublishStatus: "pending",
			}); err != nil {
				t.Fatalf("insert reassignment destination segment: %v", err)
			}
			if _, err := tx.Exec(ctx, `
				UPDATE interaction_dag_publish_outbox
				SET segment_id=$2,request_hash=$3,updated_at=now()
				WHERE segment_id=$1
			`, segmentA, segmentB, "sha256:"+segmentB); err == nil {
				t.Fatal("outbox reassignment update succeeded")
			}
			if err := tx.Rollback(ctx); err != nil {
				t.Fatalf("roll back rejected outbox reassignment: %v", err)
			}
		})

		for _, mutation := range []struct {
			name      string
			statement string
			argument  any
		}{
			{
				name:      "workspace rewrite",
				statement: `UPDATE interaction_dag_publish_outbox SET workspace_id=$2 WHERE segment_id=$1`,
				argument:  universalWSB,
			},
			{
				name:      "segment rewrite without destination",
				statement: `UPDATE interaction_dag_publish_outbox SET segment_id=$2 WHERE segment_id=$1`,
				argument:  "outbox-identity-missing",
			},
			{
				name:      "request hash rewrite",
				statement: `UPDATE interaction_dag_publish_outbox SET request_hash=$2 WHERE segment_id=$1`,
				argument:  "sha256:rewritten",
			},
		} {
			mutation := mutation
			t.Run(mutation.name, func(t *testing.T) {
				if _, err := conn.Exec(ctx, mutation.statement, segmentA, mutation.argument); err == nil {
					t.Fatalf("outbox %s succeeded", mutation.name)
				}
			})
		}

		if _, err := conn.Exec(ctx, `
			DELETE FROM interaction_dag_publish_outbox WHERE segment_id=$1
		`, segmentA); err == nil {
			t.Fatal("canonical outbox deletion succeeded")
		}

		var originalCount, destinationSegmentCount, destinationOutboxCount int
		if err := conn.QueryRow(ctx, `
			SELECT
			  count(*) FILTER (
			    WHERE workspace_id=$1 AND segment_id=$2 AND request_hash=$3
			  ),
			  (SELECT count(*) FROM interaction_dag_segment WHERE segment_id=$4),
			  count(*) FILTER (WHERE segment_id=$4)
			FROM interaction_dag_publish_outbox
		`, universalWSA, segmentA, "sha256:"+segmentA, segmentB).Scan(
			&originalCount, &destinationSegmentCount, &destinationOutboxCount,
		); err != nil {
			t.Fatalf("verify rejected outbox identity mutations: %v", err)
		}
		if originalCount != 1 || destinationSegmentCount != 0 || destinationOutboxCount != 0 {
			t.Fatalf("rejected outbox mutation changed ownership: original=%d destination_segment=%d destination_outbox=%d",
				originalCount, destinationSegmentCount, destinationOutboxCount)
		}
	})

	t.Run("outbox initial state rejects every forbidden enum", func(t *testing.T) {
		for _, status := range []string{
			"processing", "retry", "published", "redaction_failed",
			"rejected_scope", "dead_letter", "retracted",
		} {
			status := status
			t.Run(status, func(t *testing.T) {
				assertUniversalDAGTransactionRejected(t, ctx, conn, func(tx pgx.Tx) error {
					segmentID := "initial-outbox-" + status
					if err := execUniversalDAGSegment(ctx, tx, universalDAGSegment{
						WorkspaceID: universalWSA, SegmentID: segmentID, AgentRunID: universalTaskA,
						Generation: 7, StartSeq: int32Pointer(3), EndSeq: int32Pointer(3),
						CloseActionKind: "terminal", VisibleActionKey: "terminal:" + segmentID,
						ContentStatus: "pending", PublishStatus: "pending",
					}); err != nil {
						return err
					}
					_, err := tx.Exec(ctx, `
						INSERT INTO interaction_dag_publish_outbox
						  (workspace_id,segment_id,request_hash,status,attempts)
						VALUES ($1,$2,$3,$4,0)
					`, universalWSA, segmentID, "sha256:"+segmentID, status)
					return err
				})
			})
		}

		assertUniversalDAGTransactionRejected(t, ctx, conn, func(tx pgx.Tx) error {
			segmentID := "initial-outbox-nonzero-attempts"
			if err := execUniversalDAGSegment(ctx, tx, universalDAGSegment{
				WorkspaceID: universalWSA, SegmentID: segmentID, AgentRunID: universalTaskA,
				Generation: 7, StartSeq: int32Pointer(3), EndSeq: int32Pointer(3),
				CloseActionKind: "terminal", VisibleActionKey: "terminal:" + segmentID,
				ContentStatus: "pending", PublishStatus: "pending",
			}); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `
				INSERT INTO interaction_dag_publish_outbox
				  (workspace_id,segment_id,request_hash,status,attempts)
				VALUES ($1,$2,$3,'pending',1)
			`, universalWSA, segmentID, "sha256:"+segmentID)
			return err
		})
	})

	t.Run("outbox transition negative and positive matrix", func(t *testing.T) {
		insertOutboxSegment := func(segmentID string, generation int64) {
			t.Helper()
			insertUniversalDAGSegmentAndOutbox(t, ctx, conn, universalDAGSegment{
				WorkspaceID: universalWSA, SegmentID: segmentID, AgentRunID: universalTaskA,
				Generation: generation, StartSeq: int32Pointer(3), EndSeq: int32Pointer(3),
				CloseActionKind: "terminal", VisibleActionKey: "terminal:" + segmentID,
				ContentStatus: "pending", PublishStatus: "pending",
			})
		}
		startProcessing := func(segmentID string) {
			t.Helper()
			if _, err := conn.Exec(ctx, `
				UPDATE interaction_dag_publish_outbox
				SET status='processing',lease_owner='worker-a',
				    lease_expires_at=now()+interval '5 minutes',updated_at=now()
				WHERE segment_id=$1
			`, segmentID); err != nil {
				t.Fatalf("start processing %s: %v", segmentID, err)
			}
		}

		insertOutboxSegment("outbox-direct-negative", 7)
		for _, status := range []string{
			"retry", "published", "redaction_failed", "rejected_scope", "dead_letter", "retracted",
		} {
			if _, err := conn.Exec(ctx, `
				UPDATE interaction_dag_publish_outbox
				SET status=$2,
				    attempts=CASE WHEN $2='retry' THEN attempts+1 ELSE attempts END,
				    next_attempt_at=CASE WHEN $2='retry' THEN now() ELSE next_attempt_at END,
				    completed_at=CASE WHEN $2 IN (
				      'published','redaction_failed','rejected_scope','dead_letter','retracted'
				    ) THEN now() ELSE NULL END
				WHERE segment_id=$1
			`, "outbox-direct-negative", status); err == nil {
				t.Fatalf("direct pending to %s transition succeeded", status)
			}
		}
		if _, err := conn.Exec(ctx, `
			UPDATE interaction_dag_publish_outbox
			SET status='processing' WHERE segment_id='outbox-direct-negative'
		`); err == nil {
			t.Fatal("processing transition without a lease succeeded")
		}
		startProcessing("outbox-direct-negative")

		insertOutboxSegment("outbox-retry-path", 8)
		startProcessing("outbox-retry-path")
		if _, err := conn.Exec(ctx, `
			UPDATE interaction_dag_publish_outbox
			SET status='retry',next_attempt_at=now(),lease_owner=NULL,lease_expires_at=NULL
			WHERE segment_id='outbox-retry-path'
		`); err == nil {
			t.Fatal("retry transition without incrementing attempts succeeded")
		}
		if _, err := conn.Exec(ctx, `
			UPDATE interaction_dag_publish_outbox
			SET status='retry',attempts=attempts+1,next_attempt_at=NULL,
			    lease_owner=NULL,lease_expires_at=NULL
			WHERE segment_id='outbox-retry-path'
		`); err == nil {
			t.Fatal("retry transition without a schedule succeeded")
		}
		if _, err := conn.Exec(ctx, `
			UPDATE interaction_dag_publish_outbox
			SET status='retry',attempts=attempts+1,next_attempt_at=now(),
			    lease_owner=NULL,lease_expires_at=NULL,updated_at=now()
			WHERE segment_id='outbox-retry-path'
		`); err != nil {
			t.Fatalf("enter retry state: %v", err)
		}
		if _, err := conn.Exec(ctx, `
			UPDATE interaction_dag_publish_outbox
			SET status='processing' WHERE segment_id='outbox-retry-path'
		`); err == nil {
			t.Fatal("retry to processing without a lease succeeded")
		}
		startProcessing("outbox-retry-path")

		insertOutboxSegment("outbox-published-path", 9)
		startProcessing("outbox-published-path")
		if _, err := conn.Exec(ctx, `
			UPDATE interaction_dag_publish_outbox
			SET status='published',lease_owner=NULL,lease_expires_at=NULL
			WHERE segment_id='outbox-published-path'
		`); err == nil {
			t.Fatal("published transition without completed_at succeeded")
		}
		if _, err := conn.Exec(ctx, `
			UPDATE interaction_dag_publish_outbox
			SET status='published',lease_owner=NULL,lease_expires_at=NULL,
			    completed_at=now(),updated_at=now()
			WHERE segment_id='outbox-published-path'
		`); err != nil {
			t.Fatalf("publish outbox: %v", err)
		}
		if _, err := conn.Exec(ctx, `
			UPDATE interaction_dag_publish_outbox
			SET status='retracted'
			WHERE segment_id='outbox-published-path'
		`); err == nil {
			t.Fatal("retracted transition without a new completion timestamp succeeded")
		}
		if _, err := conn.Exec(ctx, `
			UPDATE interaction_dag_publish_outbox
			SET status='retracted',completed_at=completed_at+interval '1 microsecond',updated_at=now()
			WHERE segment_id='outbox-published-path'
		`); err != nil {
			t.Fatalf("retract published outbox: %v", err)
		}

		for index, status := range []string{"redaction_failed", "rejected_scope", "dead_letter"} {
			segmentID := "outbox-terminal-" + status
			insertOutboxSegment(segmentID, int64(10+index))
			startProcessing(segmentID)
			if _, err := conn.Exec(ctx, `
				UPDATE interaction_dag_publish_outbox
				SET status=$2,lease_owner=NULL,lease_expires_at=NULL,
				    completed_at=now(),last_error='classified',updated_at=now()
				WHERE segment_id=$1
			`, segmentID, status); err != nil {
				t.Fatalf("complete outbox as %s: %v", status, err)
			}
		}

		var validStates int
		if err := conn.QueryRow(ctx, `
			SELECT count(*) FROM interaction_dag_publish_outbox
			WHERE (segment_id='outbox-direct-negative' AND status='processing' AND attempts=0)
			   OR (segment_id='outbox-retry-path' AND status='processing' AND attempts=1)
			   OR (segment_id='outbox-published-path' AND status='retracted' AND completed_at IS NOT NULL)
			   OR (segment_id LIKE 'outbox-terminal-%'
			       AND status IN ('redaction_failed','rejected_scope','dead_letter')
			       AND completed_at IS NOT NULL)
		`).Scan(&validStates); err != nil {
			t.Fatalf("verify outbox state matrix: %v", err)
		}
		if validStates != 6 {
			t.Fatalf("valid outbox state rows = %d, want 6", validStates)
		}
	})

	t.Run("task message canonical range durability", func(t *testing.T) {
		const (
			protectedTask     = "40000000-0000-4000-8000-000000000003"
			metadataTask      = "40000000-0000-4000-8000-000000000004"
			moveTarget        = "40000000-0000-4000-8000-000000000005"
			segmentFirstTask  = "40000000-0000-4000-8000-000000000006"
			insertFirstTask   = "40000000-0000-4000-8000-000000000007"
			distinctRangeTask = "40000000-0000-4000-8000-000000000008"
		)
		if _, err := conn.Exec(ctx, `
			INSERT INTO agent_inbox_event(id,workspace_id,channel_id)
			VALUES ($1,$7,$8),($2,$7,$8),($3,$7,$8),($4,$7,$8),($5,$7,$8),($6,$7,$8);
			INSERT INTO task_message(task_id,seq,content)
			VALUES ($1,1,'protected-control'),($2,1,'metadata-control'),
			       ($4,1,'segment-first-control'),($5,1,'insert-first-control'),
			       ($6,1,'distinct-range-control');
		`, pgx.QueryExecModeSimpleProtocol, protectedTask, metadataTask, moveTarget,
			segmentFirstTask, insertFirstTask, distinctRangeTask, universalWSA, universalChannelA); err != nil {
			t.Fatalf("seed task-message durability controls: %v", err)
		}
		insertUniversalDAGSegmentAndOutbox(t, ctx, conn, universalDAGSegment{
			WorkspaceID: universalWSA, SegmentID: "canonical-range-control", AgentRunID: protectedTask,
			Generation: 1, StartSeq: int32Pointer(1), EndSeq: int32Pointer(1),
			CloseActionKind: "terminal", VisibleActionKey: "terminal:canonical-range-control",
			ContentStatus: "pending", PublishStatus: "pending",
		})
		t.Run("post-commit duplicate insert aborts and preserves the frozen range", func(t *testing.T) {
			tx, err := conn.Begin(ctx)
			if err != nil {
				t.Fatalf("begin duplicate task-message transaction: %v", err)
			}
			defer tx.Rollback(ctx)
			if _, err := tx.Exec(ctx, `
				INSERT INTO task_message(task_id,seq,content) VALUES ($1,1,'duplicate-control')
			`, protectedTask); err == nil {
				t.Fatal("duplicate task-message identity was accepted after canonical Segment commit")
			}
			if err := tx.Commit(ctx); err == nil {
				t.Fatal("duplicate task-message transaction committed after uniqueness violation")
			}
			assertUniversalDAGRangeIdentity(t, ctx, conn, protectedTask, 1, 1)
		})

		t.Run("distinct sequence insert remains allowed without changing the frozen range", func(t *testing.T) {
			if _, err := conn.Exec(ctx, `
				INSERT INTO task_message(task_id,seq,content) VALUES ($1,2,'distinct-control')
			`, protectedTask); err != nil {
				t.Fatalf("insert distinct task-message sequence: %v", err)
			}
			assertUniversalDAGRangeIdentity(t, ctx, conn, protectedTask, 1, 1)
		})

		t.Run("distinct sequence inside a prospective canonical range succeeds", func(t *testing.T) {
			tx, err := conn.Begin(ctx)
			if err != nil {
				t.Fatalf("begin distinct in-range transaction: %v", err)
			}
			defer tx.Rollback(ctx)
			if _, err := tx.Exec(ctx, `
				INSERT INTO task_message(task_id,seq,content) VALUES ($1,2,'distinct-in-range-control')
			`, distinctRangeTask); err != nil {
				t.Fatalf("insert distinct sequence inside prospective canonical range: %v", err)
			}
			if err := execUniversalDAGSegmentAndOutbox(ctx, tx, universalDAGSegment{
				WorkspaceID: universalWSA, SegmentID: "distinct-in-range-segment", AgentRunID: distinctRangeTask,
				Generation: 1, StartSeq: int32Pointer(1), EndSeq: int32Pointer(2),
				CloseActionKind: "terminal", VisibleActionKey: "terminal:distinct-in-range-segment",
				ContentStatus: "pending", PublishStatus: "pending",
			}); err != nil {
				t.Fatalf("canonicalize range containing distinct insert: %v", err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatalf("commit distinct in-range transaction: %v", err)
			}
			assertUniversalDAGRangeIdentity(t, ctx, conn, distinctRangeTask, 1, 2)
		})

		t.Run("overlapping transactions serialize both Segment and duplicate-insert orderings", func(t *testing.T) {
			var schema string
			if err := conn.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
				t.Fatalf("resolve private schema: %v", err)
			}
			secondConn, err := pool.Acquire(ctx)
			if err != nil {
				t.Fatalf("acquire concurrent private-schema connection: %v", err)
			}
			defer secondConn.Release()
			if _, err := secondConn.Exec(ctx, "SET search_path TO "+pgx.Identifier{schema}.Sanitize()); err != nil {
				t.Fatalf("set concurrent private search_path: %v", err)
			}

			assertDuplicateAborts := func(t *testing.T, tx pgx.Tx, taskID string) {
				t.Helper()
				if _, err := tx.Exec(ctx, `
					INSERT INTO task_message(task_id,seq,content) VALUES ($1,1,'concurrent-duplicate-control')
				`, taskID); err == nil {
					t.Fatal("concurrent duplicate task-message identity was accepted")
				}
				if err := tx.Commit(ctx); err == nil {
					t.Fatal("concurrent duplicate task-message transaction committed")
				}
			}
			segmentFor := func(taskID, segmentID string) universalDAGSegment {
				return universalDAGSegment{
					WorkspaceID: universalWSA, SegmentID: segmentID, AgentRunID: taskID,
					Generation: 1, StartSeq: int32Pointer(1), EndSeq: int32Pointer(1),
					CloseActionKind: "terminal", VisibleActionKey: "terminal:" + segmentID,
					ContentStatus: "pending", PublishStatus: "pending",
				}
			}

			t.Run("Segment transaction first then duplicate INSERT", func(t *testing.T) {
				segmentTx, err := conn.Begin(ctx)
				if err != nil {
					t.Fatalf("begin Segment-first transaction: %v", err)
				}
				defer segmentTx.Rollback(ctx)
				if err := execUniversalDAGSegmentAndOutbox(ctx, segmentTx,
					segmentFor(segmentFirstTask, "concurrent-segment-first")); err != nil {
					t.Fatalf("stage Segment-first canonical range: %v", err)
				}
				duplicateTx, err := secondConn.Begin(ctx)
				if err != nil {
					t.Fatalf("begin Segment-first duplicate transaction: %v", err)
				}
				assertDuplicateAborts(t, duplicateTx, segmentFirstTask)
				if err := segmentTx.Commit(ctx); err != nil {
					t.Fatalf("commit Segment-first canonical range: %v", err)
				}
				assertUniversalDAGRangeIdentity(t, ctx, conn, segmentFirstTask, 1, 1)
			})

			t.Run("duplicate INSERT transaction first then Segment", func(t *testing.T) {
				duplicateTx, err := conn.Begin(ctx)
				if err != nil {
					t.Fatalf("begin INSERT-first duplicate transaction: %v", err)
				}
				if _, err := duplicateTx.Exec(ctx, `
					INSERT INTO task_message(task_id,seq,content) VALUES ($1,1,'concurrent-duplicate-control')
				`, insertFirstTask); err == nil {
					t.Fatal("INSERT-first duplicate task-message identity was accepted")
				}

				segmentTx, err := secondConn.Begin(ctx)
				if err != nil {
					t.Fatalf("begin INSERT-first Segment transaction: %v", err)
				}
				defer segmentTx.Rollback(ctx)
				if err := execUniversalDAGSegmentAndOutbox(ctx, segmentTx,
					segmentFor(insertFirstTask, "concurrent-insert-first")); err != nil {
					t.Fatalf("stage INSERT-first canonical range: %v", err)
				}
				if err := segmentTx.Commit(ctx); err != nil {
					t.Fatalf("commit INSERT-first canonical range: %v", err)
				}
				if err := duplicateTx.Commit(ctx); err == nil {
					t.Fatal("INSERT-first duplicate task-message transaction committed")
				}
				assertUniversalDAGRangeIdentity(t, ctx, conn, insertFirstTask, 1, 1)
			})
		})

		if _, err := conn.Exec(ctx, `
			UPDATE interaction_dag_segment
			SET publish_status='processing',updated_at=now()
			WHERE segment_id='canonical-range-control';
			UPDATE interaction_dag_segment
			SET publish_status='published',content_status='published',publish_seq=101,
			    published_at=now(),updated_at=now()
			WHERE segment_id='canonical-range-control';
			UPDATE interaction_dag_segment
			SET publish_status='retracted',content_status='retracted',retracted_at=now(),updated_at=now()
			WHERE segment_id='canonical-range-control';
		`, pgx.QueryExecModeSimpleProtocol); err != nil {
			t.Fatalf("retract canonical range control: %v", err)
		}
		insertUniversalDAGSegmentAndOutbox(t, ctx, conn, universalDAGSegment{
			WorkspaceID: universalWSA, SegmentID: "metadata-range-control", AgentRunID: metadataTask,
			Generation: 1, StartSeq: int32Pointer(0), EndSeq: int32Pointer(0),
			CloseActionKind: "metadata_only", ContentStatus: "empty", PublishStatus: "pending",
		})

		var nonTriggerMessageID, retractedStatus string
		var edgeReferenceCount int
		if err := conn.QueryRow(ctx, `
			SELECT message.id::text,segment.content_status,
			       (SELECT count(*) FROM interaction_dag_edge WHERE trigger_message_id=message.id)
			FROM task_message AS message
			JOIN interaction_dag_segment AS segment
			  ON segment.agent_run_id=message.task_id
			 AND message.seq BETWEEN segment.start_seq AND segment.end_seq
			WHERE message.task_id=$1 AND message.seq=1
			  AND segment.segment_id='canonical-range-control'
		`, protectedTask).Scan(&nonTriggerMessageID, &retractedStatus, &edgeReferenceCount); err != nil {
			t.Fatalf("resolve non-trigger canonical range message: %v", err)
		}
		if retractedStatus != "retracted" || edgeReferenceCount != 0 {
			t.Fatalf("canonical range control is not a retracted non-trigger: status=%s edges=%d",
				retractedStatus, edgeReferenceCount)
		}

		t.Run("in-range non-trigger delete and move remain rejected after retraction", func(t *testing.T) {
			for name, mutation := range map[string]struct {
				statement string
				argument  any
			}{
				"delete": {
					statement: `DELETE FROM task_message WHERE id=$1`,
				},
				"task move": {
					statement: `UPDATE task_message SET task_id=$2 WHERE id=$1`,
					argument:  moveTarget,
				},
				"sequence move": {
					statement: `UPDATE task_message SET seq=$2 WHERE id=$1`,
					argument:  99,
				},
			} {
				arguments := []any{nonTriggerMessageID}
				if mutation.argument != nil {
					arguments = append(arguments, mutation.argument)
				}
				if _, err := conn.Exec(ctx, mutation.statement, arguments...); err == nil {
					t.Fatalf("in-range non-trigger message %s succeeded", name)
				}
			}
			var durableCount int
			if err := conn.QueryRow(ctx, `
				SELECT count(*) FROM task_message WHERE id=$1 AND task_id=$2 AND seq=1
			`, nonTriggerMessageID, protectedTask).Scan(&durableCount); err != nil {
				t.Fatalf("verify protected non-trigger message: %v", err)
			}
			if durableCount != 1 {
				t.Fatal("rejected non-trigger mutation changed canonical range source")
			}
		})

		assertAllowedThenRollback := func(t *testing.T, statement string, arguments ...any) {
			t.Helper()
			tx, err := conn.Begin(ctx)
			if err != nil {
				t.Fatalf("begin allowed task-message mutation: %v", err)
			}
			defer tx.Rollback(ctx)
			tag, err := tx.Exec(ctx, statement, arguments...)
			if err != nil {
				t.Fatalf("allowed task-message mutation was rejected: %v", err)
			}
			if tag.RowsAffected() != 1 {
				t.Fatalf("allowed task-message mutation affected %d rows, want 1", tag.RowsAffected())
			}
			if err := tx.Rollback(ctx); err != nil {
				t.Fatalf("roll back allowed task-message control: %v", err)
			}
		}

		t.Run("uncovered message mutations and distinct inserts remain allowed with metadata-only segment", func(t *testing.T) {
			if _, err := conn.Exec(ctx, `
				INSERT INTO task_message(task_id,seq,content) VALUES ($1,2,'metadata-distinct-control')
			`, metadataTask); err != nil {
				t.Fatalf("insert metadata-only distinct sequence control: %v", err)
			}
			var messageID string
			if err := conn.QueryRow(ctx, `
				SELECT id::text FROM task_message WHERE task_id=$1 AND seq=1
			`, metadataTask).Scan(&messageID); err != nil {
				t.Fatalf("resolve metadata-only control message: %v", err)
			}
			assertAllowedThenRollback(t, `DELETE FROM task_message WHERE id=$1`, messageID)
			assertAllowedThenRollback(t, `UPDATE task_message SET task_id=$2 WHERE id=$1`, messageID, moveTarget)
			assertAllowedThenRollback(t, `UPDATE task_message SET seq=3 WHERE id=$1`, messageID)
		})

		if _, err := conn.Exec(ctx, `
			INSERT INTO interaction_dag_segment (
			  workspace_id,segment_id,agent_run_id,generation,
			  project_id_at_event,channel_id_at_event,start_seq,end_seq,
			  close_action_kind,canonical_action_id,visible_action_key,
			  memory_type_at_event,graph_projection_eligible_at_event,
			  trajectory_source,derivative,trainable_eligible,
			  publish_status,publish_seq,content_status,
			  sanitizer_version,policy_version,provider_capture_status
			) VALUES (
			  $1,'legacy-range-control',$2,30,
			  NULL,NULL,1,2,
			  NULL,NULL,NULL,
			  'graph',false,'task_messages',false,false,
			  NULL,NULL,'legacy_unverified',
			  'dag-redaction-v1','universal-dag-v1','not_expected'
			)
		`, universalWSB, universalTaskB); err != nil {
			t.Fatalf("insert legacy range control: %v", err)
		}
		t.Run("legacy-unverified range mutations and distinct in-range inserts remain allowed", func(t *testing.T) {
			if _, err := conn.Exec(ctx, `
				INSERT INTO task_message(task_id,seq,content) VALUES ($1,2,'legacy-distinct-control')
			`, universalTaskB); err != nil {
				t.Fatalf("insert distinct sequence inside legacy-unverified range: %v", err)
			}
			var messageID string
			if err := conn.QueryRow(ctx, `
				SELECT id::text FROM task_message WHERE task_id=$1 AND seq=1
			`, universalTaskB).Scan(&messageID); err != nil {
				t.Fatalf("resolve legacy range control message: %v", err)
			}
			assertAllowedThenRollback(t, `DELETE FROM task_message WHERE id=$1`, messageID)
			assertAllowedThenRollback(t, `UPDATE task_message SET task_id=$2 WHERE id=$1`, messageID, moveTarget)
			assertAllowedThenRollback(t, `UPDATE task_message SET seq=3 WHERE id=$1`, messageID)
		})
	})

	t.Run("cursor is workspace scoped non-regressive and coherent", func(t *testing.T) {
		if _, err := conn.Exec(ctx, `
			INSERT INTO interaction_dag_task_cursor
			  (workspace_id,agent_run_id,next_generation,open_start_seq,last_closed_seq,
			   open_generation,open_end_seq)
			VALUES ($1,$2,3,3,2,3,4)
		`, universalWSA, universalTaskA); err != nil {
			t.Fatalf("insert canonical cursor: %v", err)
		}
		if _, err := conn.Exec(ctx, `
			INSERT INTO interaction_dag_task_cursor
			  (workspace_id,agent_run_id,next_generation,open_start_seq,last_closed_seq)
			VALUES ($1,$2,3,NULL,2)
		`, universalWSA, universalTaskA); err == nil {
			t.Fatal("duplicate workspace/task cursor succeeded")
		}
		if _, err := conn.Exec(ctx, `
			INSERT INTO interaction_dag_task_cursor
			  (workspace_id,agent_run_id,next_generation,open_start_seq,last_closed_seq)
			VALUES ($1,$2,1,NULL,0)
		`, universalWSB, universalTaskA); err == nil {
			t.Fatal("cross-workspace task cursor succeeded")
		}
		for name, statement := range map[string]string{
			"next generation regression": `UPDATE interaction_dag_task_cursor SET next_generation=2 WHERE workspace_id=$1 AND agent_run_id=$2`,
			"last closed regression":     `UPDATE interaction_dag_task_cursor SET last_closed_seq=1 WHERE workspace_id=$1 AND agent_run_id=$2`,
			"open end regression":        `UPDATE interaction_dag_task_cursor SET open_end_seq=3 WHERE workspace_id=$1 AND agent_run_id=$2`,
			"open before closed":         `UPDATE interaction_dag_task_cursor SET open_start_seq=2 WHERE workspace_id=$1 AND agent_run_id=$2`,
			"generation mismatch":        `UPDATE interaction_dag_task_cursor SET open_generation=4 WHERE workspace_id=$1 AND agent_run_id=$2`,
			"incomplete close":           `UPDATE interaction_dag_task_cursor SET open_start_seq=NULL,open_generation=NULL,open_end_seq=NULL,last_closed_seq=3,next_generation=4 WHERE workspace_id=$1 AND agent_run_id=$2`,
		} {
			if _, err := conn.Exec(ctx, statement, universalWSA, universalTaskA); err == nil {
				t.Fatalf("cursor update with %s succeeded", name)
			}
		}
		var nextGeneration int64
		var lastClosed, openStart, openEnd int32
		if err := conn.QueryRow(ctx, `
			SELECT next_generation,last_closed_seq,open_start_seq,open_end_seq
			FROM interaction_dag_task_cursor WHERE workspace_id=$1 AND agent_run_id=$2
		`, universalWSA, universalTaskA).Scan(&nextGeneration, &lastClosed, &openStart, &openEnd); err != nil {
			t.Fatalf("read cursor after rejected updates: %v", err)
		}
		if nextGeneration != 3 || lastClosed != 2 || openStart != 3 || openEnd != 4 {
			t.Fatalf("rejected cursor update mutated state: next=%d closed=%d open=%d..%d", nextGeneration, lastClosed, openStart, openEnd)
		}
	})

	t.Run("canonical edge durability shape provenance and workspace", func(t *testing.T) {
		applyUniversalDAGEdgeOnlyLinkageMigration(t, ctx, conn)
		insertUniversalDAGSegmentAndOutbox(t, ctx, conn, universalDAGSegment{
			WorkspaceID: universalWSB, SegmentID: "segment-b", AgentRunID: universalTaskB,
			Generation: 1, StartSeq: int32Pointer(1), EndSeq: int32Pointer(1),
			CloseActionKind: "terminal", VisibleActionKey: "terminal:segment-b",
			ContentStatus: "pending", PublishStatus: "pending",
		})
		var triggerMessageID, outOfRangeTriggerMessageID string
		if err := conn.QueryRow(ctx, `
			SELECT id::text FROM task_message WHERE task_id=$1 AND seq=2
		`, universalTaskA).Scan(&triggerMessageID); err != nil {
			t.Fatalf("resolve canonical trigger message: %v", err)
		}
		if err := conn.QueryRow(ctx, `
			SELECT id::text FROM task_message WHERE task_id=$1 AND seq=3
		`, universalTaskA).Scan(&outOfRangeTriggerMessageID); err != nil {
			t.Fatalf("resolve same-task out-of-range trigger message: %v", err)
		}
		if _, err := conn.Exec(ctx, `
			INSERT INTO interaction_dag_edge
			  (workspace_id,edge_seq,src_segment_id,dst_segment_id,type,trigger_message_id)
			VALUES ($1,1,'segment-unscoped','metadata-only','continues',NULL)
		`, universalWSA); err != nil {
			t.Fatalf("insert continues edge: %v", err)
		}
		for i, edgeType := range []string{"responds_to", "delegates_to", "mentions"} {
			if _, err := conn.Exec(ctx, `
				INSERT INTO interaction_dag_edge
				  (workspace_id,edge_seq,src_segment_id,dst_segment_id,type,trigger_message_id)
				VALUES ($1,$2,'segment-unscoped','metadata-only',$3,$4)
			`, universalWSA, int64(i+2), edgeType, triggerMessageID); err != nil {
				t.Fatalf("insert %s edge: %v", edgeType, err)
			}
		}
		// Approved plan line 232 permits edge-only linkage after both canonical anchors exist.
		for i, edgeType := range []string{"responds_to", "delegates_to", "mentions"} {
			if _, err := conn.Exec(ctx, `
				INSERT INTO interaction_dag_edge
				  (workspace_id,edge_seq,src_segment_id,dst_segment_id,type,trigger_message_id)
				VALUES ($1,$2,'segment-unscoped','metadata-only',$3,NULL)
			`, universalWSA, int64(i+50), edgeType); err != nil {
				t.Fatalf("insert edge-only %s edge: %v", edgeType, err)
			}
		}
		for name, testCase := range map[string]struct {
			statement string
			args      []any
		}{
			"continues with trigger": {
				`INSERT INTO interaction_dag_edge(workspace_id,edge_seq,src_segment_id,dst_segment_id,type,trigger_message_id) VALUES ($1,5,'segment-unscoped','metadata-only','continues',$2)`,
				[]any{universalWSA, triggerMessageID},
			},
			"arbitrary trigger": {
				`INSERT INTO interaction_dag_edge(workspace_id,edge_seq,src_segment_id,dst_segment_id,type,trigger_message_id) VALUES ($1,5,'segment-unscoped','metadata-only','responds_to','90000000-0000-4000-8000-000000000001')`,
				[]any{universalWSA},
			},
			"same task out of range trigger": {
				`INSERT INTO interaction_dag_edge(workspace_id,edge_seq,src_segment_id,dst_segment_id,type,trigger_message_id) VALUES ($1,5,'segment-unscoped','metadata-only','responds_to',$2)`,
				[]any{universalWSA, outOfRangeTriggerMessageID},
			},
			"wrong task trigger": {
				`INSERT INTO interaction_dag_edge(workspace_id,edge_seq,src_segment_id,dst_segment_id,type,trigger_message_id) SELECT $1,5,'segment-unscoped','metadata-only','responds_to',id FROM task_message WHERE task_id='40000000-0000-4000-8000-000000000002' LIMIT 1`,
				[]any{universalWSA},
			},
			"unknown type": {
				`INSERT INTO interaction_dag_edge(workspace_id,edge_seq,src_segment_id,dst_segment_id,type) VALUES ($1,5,'segment-unscoped','metadata-only','completion')`,
				[]any{universalWSA},
			},
			"cross workspace": {
				`INSERT INTO interaction_dag_edge(workspace_id,edge_seq,src_segment_id,dst_segment_id,type) VALUES ($1,5,'segment-unscoped','segment-b','continues')`,
				[]any{universalWSA},
			},
		} {
			if _, err := conn.Exec(ctx, testCase.statement, testCase.args...); err == nil {
				t.Fatalf("edge with %s succeeded", name)
			}
		}

		for name, testCase := range map[string]struct {
			statement string
			args      []any
		}{
			"edge sequence rewrite": {
				`UPDATE interaction_dag_edge SET edge_seq=6 WHERE workspace_id=$1 AND edge_seq=1`,
				[]any{universalWSA},
			},
			"endpoint rewrite": {
				`UPDATE interaction_dag_edge SET dst_segment_id='immutable-segment' WHERE workspace_id=$1 AND edge_seq=1`,
				[]any{universalWSA},
			},
			"type and trigger rewrite": {
				`UPDATE interaction_dag_edge SET type='responds_to',trigger_message_id=$2 WHERE workspace_id=$1 AND edge_seq=1`,
				[]any{universalWSA, triggerMessageID},
			},
			"physical delete": {
				`DELETE FROM interaction_dag_edge WHERE workspace_id=$1 AND edge_seq=1`,
				[]any{universalWSA},
			},
		} {
			if _, err := conn.Exec(ctx, testCase.statement, testCase.args...); err == nil {
				t.Fatalf("committed canonical %s succeeded", name)
			}
		}
		var durableEdgeCount int
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM interaction_dag_edge WHERE workspace_id=$1`, universalWSA).Scan(&durableEdgeCount); err != nil {
			t.Fatalf("count durable canonical edges: %v", err)
		}
		if durableEdgeCount != 7 {
			t.Fatalf("rejected edge mutation changed committed edges: count=%d", durableEdgeCount)
		}

		t.Run("in-range trigger message delete and move remain rejected", func(t *testing.T) {
			for name, statement := range map[string]string{
				"delete":        `DELETE FROM task_message WHERE id=$1`,
				"task move":     `UPDATE task_message SET task_id='40000000-0000-4000-8000-000000000002' WHERE id=$1`,
				"sequence move": `UPDATE task_message SET seq=3 WHERE id=$1`,
			} {
				if _, err := conn.Exec(ctx, statement, triggerMessageID); err == nil {
					t.Fatalf("referenced trigger message %s succeeded", name)
				}
			}
		})

		// Legacy endpoints remain physically deletable only when no durable edge
		// references them; the composite FKs refuse deletion rather than orphaning.
		for generation, segmentID := range []string{"legacy-edge-source", "legacy-edge-target"} {
			if err := func() error {
				tx, err := conn.Begin(ctx)
				if err != nil {
					return err
				}
				defer tx.Rollback(ctx)
				if err := execUniversalDAGLegacySegment(ctx, tx, segmentID, int64(generation+3), false, false); err != nil {
					return err
				}
				return tx.Commit(ctx)
			}(); err != nil {
				t.Fatalf("insert legacy edge endpoint: %v", err)
			}
		}
		if _, err := conn.Exec(ctx, `
			INSERT INTO interaction_dag_edge(workspace_id,edge_seq,src_segment_id,dst_segment_id,type)
			VALUES ($1,1,'legacy-edge-source','legacy-edge-target','continues')
		`, universalWSB); err != nil {
			t.Fatalf("insert legacy endpoint edge: %v", err)
		}
		if _, err := conn.Exec(ctx, `DELETE FROM interaction_dag_segment WHERE segment_id='legacy-edge-source'`); err == nil {
			t.Fatal("deleting an edge endpoint succeeded and could orphan the edge")
		}
		var orphanCount int
		if err := conn.QueryRow(ctx, `
			SELECT count(*) FROM interaction_dag_edge e
			LEFT JOIN interaction_dag_segment s ON s.workspace_id=e.workspace_id AND s.segment_id=e.src_segment_id
			LEFT JOIN interaction_dag_segment d ON d.workspace_id=e.workspace_id AND d.segment_id=e.dst_segment_id
			WHERE s.segment_id IS NULL OR d.segment_id IS NULL
		`).Scan(&orphanCount); err != nil {
			t.Fatalf("count orphan edges: %v", err)
		}
		if orphanCount != 0 {
			t.Fatalf("found %d orphan edges", orphanCount)
		}
	})

	t.Run("provider association roles and identity", func(t *testing.T) {
		insertUniversalDAGSegmentAndOutbox(t, ctx, conn, universalDAGSegment{
			WorkspaceID: universalWSB, SegmentID: "provider-pending-correlation", AgentRunID: universalTaskB,
			Generation: 5, StartSeq: int32Pointer(1), EndSeq: int32Pointer(1),
			CloseActionKind: "terminal", VisibleActionKey: "terminal:provider-pending-correlation",
			ContentStatus: "pending", PublishStatus: "pending", ProviderCaptureStatus: "pending",
			ProviderCaptureCorrelationKey: "correlation-pending",
		})
		var pendingCaptureID *string
		var pendingCorrelation string
		if err := conn.QueryRow(ctx, `
			SELECT provider_capture_id,provider_capture_correlation_key
			FROM interaction_dag_segment WHERE segment_id='provider-pending-correlation'
		`).Scan(&pendingCaptureID, &pendingCorrelation); err != nil {
			t.Fatalf("read pending provider correlation: %v", err)
		}
		if pendingCaptureID != nil || pendingCorrelation != "correlation-pending" {
			t.Fatalf("pending provider correlation was not durable without final capture identity")
		}

		insertUniversalDAGSegmentAndOutbox(t, ctx, conn, universalDAGSegment{
			WorkspaceID: universalWSB, SegmentID: "provider-capture-lifecycle", AgentRunID: universalTaskB,
			Generation: 6, StartSeq: int32Pointer(1), EndSeq: int32Pointer(1),
			CloseActionKind: "terminal", VisibleActionKey: "terminal:provider-capture-lifecycle",
			ContentStatus: "pending", PublishStatus: "pending",
		})
		if _, err := conn.Exec(ctx, `
			UPDATE interaction_dag_segment
			SET provider_capture_status='pending',
			    provider_capture_correlation_key='correlation-lifecycle',updated_at=now()
			WHERE segment_id='provider-capture-lifecycle'
		`); err != nil {
			t.Fatalf("advance provider capture to pending: %v", err)
		}
		q := db.New(conn)
		conflict := db.MarkUniversalDAGProviderCaptureConflictParams{
			WorkspaceID: mustUUID(t, universalWSB), SegmentID: "provider-capture-lifecycle",
			CaptureID: "capture-conflicting", CaptureVersion: 3,
			ProviderCaptureCorrelationKey: "correlation-mismatch",
		}
		if err := q.MarkUniversalDAGProviderCaptureConflict(ctx, conflict); err != nil {
			t.Fatalf("attempt pending conflict with mismatched correlation: %v", err)
		}
		var captureStatus string
		if err := conn.QueryRow(ctx, `SELECT provider_capture_status FROM interaction_dag_segment WHERE segment_id='provider-capture-lifecycle'`).Scan(&captureStatus); err != nil {
			t.Fatalf("read pending capture after mismatched conflict: %v", err)
		}
		if captureStatus != "pending" {
			t.Fatalf("mismatched conflict changed pending capture to %q", captureStatus)
		}

		finalize := db.FinalizeUniversalDAGProviderCaptureParams{
			WorkspaceID: mustUUID(t, universalWSB), SegmentID: "provider-capture-lifecycle",
			CaptureID: "capture-lifecycle", CaptureVersion: 2,
			ProviderCaptureCorrelationKey: "correlation-mismatch",
		}
		if _, err := q.FinalizeUniversalDAGProviderCapture(ctx, finalize); err == nil {
			t.Fatal("provider capture finalized with a mismatched correlation")
		}
		finalize.ProviderCaptureCorrelationKey = "correlation-lifecycle"
		finalized, err := q.FinalizeUniversalDAGProviderCapture(ctx, finalize)
		if err != nil {
			t.Fatalf("finalize provider capture with matching correlation: %v", err)
		}
		if finalized.ProviderCaptureStatus != "finalized" ||
			!finalized.ProviderCaptureID.Valid || finalized.ProviderCaptureID.String != "capture-lifecycle" ||
			!finalized.ProviderCaptureVersion.Valid || finalized.ProviderCaptureVersion.Int64 != 2 {
			t.Fatal("provider capture happy path did not persist final identity")
		}
		if err := q.MarkUniversalDAGProviderCaptureConflict(ctx, conflict); err != nil {
			t.Fatalf("attempt finalized conflict with mismatched correlation: %v", err)
		}
		if err := conn.QueryRow(ctx, `SELECT provider_capture_status FROM interaction_dag_segment WHERE segment_id='provider-capture-lifecycle'`).Scan(&captureStatus); err != nil {
			t.Fatalf("read finalized capture after mismatched conflict: %v", err)
		}
		if captureStatus != "finalized" {
			t.Fatalf("mismatched conflict changed finalized capture to %q", captureStatus)
		}
		conflict.ProviderCaptureCorrelationKey = "correlation-lifecycle"
		if err := q.MarkUniversalDAGProviderCaptureConflict(ctx, conflict); err != nil {
			t.Fatalf("mark matching conflicting replay: %v", err)
		}
		var conflictCaptureID string
		var conflictCaptureVersion int64
		if err := conn.QueryRow(ctx, `
			SELECT provider_capture_status,provider_capture_id,provider_capture_version
			FROM interaction_dag_segment WHERE segment_id='provider-capture-lifecycle'
		`).Scan(&captureStatus, &conflictCaptureID, &conflictCaptureVersion); err != nil {
			t.Fatalf("read correlation-gated conflict: %v", err)
		}
		if captureStatus != "conflict" || conflictCaptureID != "capture-lifecycle" || conflictCaptureVersion != 2 {
			t.Fatalf("matching conflict did not preserve first capture identity: status=%q id=%q version=%d", captureStatus, conflictCaptureID, conflictCaptureVersion)
		}

		for _, segment := range []universalDAGSegment{
			{
				WorkspaceID: universalWSA, SegmentID: "provider-owner", AgentRunID: universalTaskA,
				Generation: 3, StartSeq: int32Pointer(3), EndSeq: int32Pointer(3),
				CloseActionKind: "terminal", VisibleActionKey: "terminal:provider-owner",
				ContentStatus: "pending", PublishStatus: "pending", RunID: universalRunA,
				RunAgentID: universalRunAgentA,
			},
			{
				WorkspaceID: universalWSA, SegmentID: "provider-shared", AgentRunID: universalTaskA,
				Generation: 4, StartSeq: int32Pointer(3), EndSeq: int32Pointer(3),
				CloseActionKind: "terminal", VisibleActionKey: "terminal:provider-shared",
				ContentStatus: "pending", PublishStatus: "pending", RunID: universalRunA,
				RunAgentID: universalRunAgentA,
			},
			{
				WorkspaceID: universalWSA, SegmentID: "provider-second-owner", AgentRunID: universalTaskA,
				Generation: 5, StartSeq: int32Pointer(3), EndSeq: int32Pointer(3),
				CloseActionKind: "terminal", VisibleActionKey: "terminal:provider-second-owner",
				ContentStatus: "pending", PublishStatus: "pending", RunID: universalRunA,
				RunAgentID: universalRunAgentA,
			},
		} {
			insertUniversalDAGSegmentAndOutbox(t, ctx, conn, segment)
			if _, err := conn.Exec(ctx, `
				UPDATE interaction_dag_segment
				SET provider_capture_status='pending',provider_capture_correlation_key='correlation-a'
				WHERE segment_id=$1
			`, segment.SegmentID); err != nil {
				t.Fatalf("start provider capture for %s: %v", segment.SegmentID, err)
			}
			if _, err := q.FinalizeUniversalDAGProviderCapture(ctx, db.FinalizeUniversalDAGProviderCaptureParams{
				WorkspaceID: mustUUID(t, universalWSA), SegmentID: segment.SegmentID,
				CaptureID: "capture-a", CaptureVersion: 1,
				ProviderCaptureCorrelationKey: "correlation-a",
			}); err != nil {
				t.Fatalf("finalize provider capture for %s: %v", segment.SegmentID, err)
			}
		}

		assertUniversalDAGTransactionRejected(t, ctx, conn, func(tx pgx.Tx) error {
			return execUniversalDAGProviderLink(ctx, tx, "provider-shared", "call-a-3", "shared_producer", 3)
		})

		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatalf("begin owned/shared provider transaction: %v", err)
		}
		defer tx.Rollback(ctx)
		for _, link := range []struct {
			segment, role string
		}{
			{segment: "provider-owner", role: "owned"},
			{segment: "provider-shared", role: "shared_producer"},
		} {
			if err := execUniversalDAGProviderLink(ctx, tx, link.segment, "call-a-1", link.role, 1); err != nil {
				t.Fatalf("insert same-transaction provider role %s: %v", link.role, err)
			}
		}
		if _, err := tx.Exec(ctx, `SET CONSTRAINTS ALL IMMEDIATE`); err != nil {
			t.Fatalf("validate same-transaction owned/shared links: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit same-transaction owned/shared links: %v", err)
		}

		assertUniversalDAGTransactionRejected(t, ctx, conn, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `
				UPDATE interaction_dag_universal_provider_call
				SET provider_call_id='call-a-2', ordinal=2
				WHERE segment_id='provider-owner' AND provider_call_id='call-a-1'
			`)
			return err
		})
		var ownedCount, sharedCount int
		if err := conn.QueryRow(ctx, `
			SELECT count(*) FILTER (WHERE role='owned'),
			       count(*) FILTER (WHERE role='shared_producer')
			FROM interaction_dag_universal_provider_call
			WHERE provider_call_id='call-a-1' AND run_id=$1
		`, universalRunA).Scan(&ownedCount, &sharedCount); err != nil {
			t.Fatalf("read provider ownership after rejected key update: %v", err)
		}
		if ownedCount != 1 || sharedCount != 1 {
			t.Fatalf("provider ownership changed after rejected key update: owned=%d shared=%d", ownedCount, sharedCount)
		}

		if _, err := conn.Exec(ctx, `
			INSERT INTO interaction_dag_universal_provider_call
			  (segment_id,provider_call_id,role,ordinal,run_id,run_agent_id,capture_id)
			VALUES ('provider-owner','call-a-2','audit',2,$1,$2,'capture-a')
		`, universalRunA, universalRunAgentA); err != nil {
			t.Fatalf("insert provider role audit: %v", err)
		}
		if _, err := conn.Exec(ctx, `
			UPDATE interaction_dag_universal_provider_call SET role='owned'
			WHERE segment_id='provider-owner' AND provider_call_id='call-a-2'
		`); err == nil {
			t.Fatal("committed provider association role rewrite succeeded")
		}
		assertUniversalDAGTransactionRejected(t, ctx, conn, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `UPDATE pi_provider_call SET call_ordinal=3 WHERE call_id='call-a-2'`); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `
				UPDATE interaction_dag_universal_provider_call SET ordinal=3
				WHERE segment_id='provider-owner' AND provider_call_id='call-a-2'
			`)
			return err
		})
		if _, err := conn.Exec(ctx, `
			DELETE FROM interaction_dag_universal_provider_call
			WHERE segment_id='provider-owner' AND provider_call_id='call-a-2'
		`); err == nil {
			t.Fatal("committed provider association physical deletion succeeded")
		}
		var immutableAuditCount int
		if err := conn.QueryRow(ctx, `
			SELECT count(*) FROM interaction_dag_universal_provider_call
			WHERE segment_id='provider-owner' AND provider_call_id='call-a-2'
			  AND role='audit' AND ordinal=2
		`).Scan(&immutableAuditCount); err != nil {
			t.Fatalf("read immutable provider audit association: %v", err)
		}
		if immutableAuditCount != 1 {
			t.Fatal("rejected provider association mutation changed committed row")
		}

		assertUniversalDAGTransactionRejected(t, ctx, conn, func(tx pgx.Tx) error {
			return execUniversalDAGProviderLink(ctx, tx, "provider-second-owner", "call-a-1", "owned", 1)
		})

		for name, args := range map[string][]any{
			"owner alias":   {"provider-owner", "call-a-3", "owner", int64(3), universalRunA, universalRunAgentA, "capture-a"},
			"wrong ordinal": {"provider-owner", "call-a-3", "audit", int64(99), universalRunA, universalRunAgentA, "capture-a"},
			"wrong run":     {"provider-owner", "call-a-3", "audit", int64(3), universalRunB, universalRunAgentB, "capture-a"},
			"wrong capture": {"provider-owner", "call-a-3", "audit", int64(3), universalRunA, universalRunAgentA, "capture-b"},
		} {
			if _, err := conn.Exec(ctx, `
				INSERT INTO interaction_dag_universal_provider_call
				  (segment_id,provider_call_id,role,ordinal,run_id,run_agent_id,capture_id)
				VALUES ($1,$2,$3,$4,$5,$6,$7)
			`, args...); err == nil {
				t.Fatalf("provider association with %s succeeded", name)
			}
		}
	})

	_ = pool
}

func TestUniversalDAGCanonicalQueriesIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, conn := openUniversalDAGServiceSchema(t, ctx)
	defer conn.Release()
	if _, err := conn.Exec(ctx, universalDAGLegacySchema); err != nil {
		t.Fatalf("create pre-454 schema: %v", err)
	}
	applyUniversalDAGMigrationIfPresent(t, ctx, conn)
	seedUniversalDAGCanonicalOwners(t, ctx, conn)

	for _, segment := range []universalDAGSegment{
		{WorkspaceID: universalWSA, SegmentID: "query-a-source", AgentRunID: universalTaskA, Generation: 1, ProjectID: universalProjectA, StartSeq: int32Pointer(1), EndSeq: int32Pointer(2), CloseActionKind: "terminal", VisibleActionKey: "terminal:query-a-source", ContentStatus: "pending", PublishStatus: "pending"},
		{WorkspaceID: universalWSA, SegmentID: "query-a-target", AgentRunID: universalTaskA, Generation: 2, ProjectID: universalProjectA, StartSeq: int32Pointer(3), EndSeq: int32Pointer(3), CloseActionKind: "terminal", VisibleActionKey: "terminal:query-a-target", ContentStatus: "pending", PublishStatus: "pending"},
		{WorkspaceID: universalWSB, SegmentID: "query-b-source", AgentRunID: universalTaskB, Generation: 1, ProjectID: universalProjectB, StartSeq: int32Pointer(1), EndSeq: int32Pointer(1), CloseActionKind: "terminal", VisibleActionKey: "terminal:query-b-source", ContentStatus: "pending", PublishStatus: "pending"},
		{WorkspaceID: universalWSB, SegmentID: "query-b-target", AgentRunID: universalTaskB, Generation: 2, ProjectID: universalProjectB, StartSeq: int32Pointer(1), EndSeq: int32Pointer(1), CloseActionKind: "terminal", VisibleActionKey: "terminal:query-b-target", ContentStatus: "pending", PublishStatus: "pending"},
		{WorkspaceID: universalWSA, SegmentID: "query-a-run", AgentRunID: universalTaskA, Generation: 3, ProjectID: universalProjectA, StartSeq: int32Pointer(3), EndSeq: int32Pointer(3), CloseActionKind: "terminal", VisibleActionKey: "terminal:query-a-run", ContentStatus: "pending", PublishStatus: "pending", RunID: universalRunA, RunAgentID: universalRunAgentA},
	} {
		insertUniversalDAGSegmentAndOutbox(t, ctx, conn, segment)
	}

	q := db.New(conn)
	workspaceA, err := q.GetUniversalDAGProjectWorkspace(ctx, universalProjectA)
	if err != nil {
		t.Fatalf("resolve project workspace: %v", err)
	}
	workspaceB, err := q.GetUniversalDAGProjectWorkspace(ctx, universalProjectB)
	if err != nil {
		t.Fatalf("resolve second project workspace: %v", err)
	}
	triggerID, err := q.GetUniversalDAGEdgeTriggerMessageID(ctx, db.GetUniversalDAGEdgeTriggerMessageIDParams{WorkspaceID: workspaceA, SegmentID: "query-a-source"})
	if err != nil || !triggerID.Valid {
		t.Fatalf("resolve canonical edge trigger: %v", err)
	}

	// The approved standalone query names remain executable for transactional
	// callers, while the service path below uses the atomic combined statement.
	reservedB, err := q.AllocateUniversalDAGEdgeSeq(ctx, workspaceB)
	if err != nil {
		t.Fatalf("allocate canonical edge sequence: %v", err)
	}
	if _, err := q.InsertUniversalDAGEdge(ctx, db.InsertUniversalDAGEdgeParams{
		WorkspaceID: workspaceB, EdgeSeq: reservedB, SrcSegmentID: "query-b-source",
		DstSegmentID: "query-b-target", EdgeType: EdgeTypeContinues,
	}); err != nil {
		t.Fatalf("insert explicitly allocated canonical edge: %v", err)
	}

	var schema string
	if err := conn.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatalf("read private schema name: %v", err)
	}
	const concurrentEdges = 12
	errCh := make(chan error, concurrentEdges)
	var wg sync.WaitGroup
	for i := 0; i < concurrentEdges; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			worker, err := pool.Acquire(ctx)
			if err != nil {
				errCh <- err
				return
			}
			defer worker.Release()
			if _, err := worker.Exec(ctx, "SET search_path TO "+pgx.Identifier{schema}.Sanitize()); err != nil {
				errCh <- err
				return
			}
			svc := NewInteractionDAGService(db.New(worker), nil, true)
			errCh <- svc.AddEdge(ctx, workspaceA, "query-a-source", "query-a-target", EdgeTypeRespondsTo)
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent canonical AddEdge failed: %v", err)
		}
	}
	var edgeCount, distinctSeq int
	var minimumSeq, maximumSeq int64
	if err := conn.QueryRow(ctx, `
		SELECT count(*),count(DISTINCT edge_seq),min(edge_seq),max(edge_seq)
		FROM interaction_dag_edge WHERE workspace_id=$1
	`, universalWSA).Scan(&edgeCount, &distinctSeq, &minimumSeq, &maximumSeq); err != nil {
		t.Fatalf("read concurrent edge allocation: %v", err)
	}
	if edgeCount != concurrentEdges || distinctSeq != concurrentEdges || minimumSeq != 1 || maximumSeq != concurrentEdges {
		t.Fatalf("concurrent edge allocation was not distinct and monotonic: count=%d distinct=%d range=%d..%d", edgeCount, distinctSeq, minimumSeq, maximumSeq)
	}
	var persistedTrigger pgtype.UUID
	if err := conn.QueryRow(ctx, `
		SELECT trigger_message_id FROM interaction_dag_edge
		WHERE workspace_id=$1 ORDER BY edge_seq LIMIT 1
	`, universalWSA).Scan(&persistedTrigger); err != nil {
		t.Fatalf("read persisted trigger identity: %v", err)
	}
	if persistedTrigger != triggerID {
		t.Fatal("canonical edge did not persist the resolved source-range trigger identity")
	}

	svc := NewInteractionDAGService(q, nil, true)
	if err := svc.AddEdge(ctx, workspaceA, "missing-source", "query-a-target", EdgeTypeContinues); err == nil {
		t.Fatal("canonical edge with a missing endpoint succeeded")
	}
	if err := svc.AddEdge(ctx, workspaceA, "query-a-source", "query-b-target", EdgeTypeContinues); err == nil {
		t.Fatal("canonical edge with a cross-workspace endpoint succeeded")
	}

	// Both retained writers execute against migration 454 and produce only the
	// approved legacy_unverified shape. Their bodies are not returned.
	localSvc := NewInteractionDAGServiceWithMessages(q, q, nil, true)
	localID, localBody, err := localSvc.RecordLocalSegmentForEvent(ctx, universalProjectB, universalTaskB, "", "completion", nil)
	if err != nil {
		t.Fatalf("record local compatibility segment on canonical schema: %v", err)
	}
	if localID == "" || len(localBody) != 0 {
		t.Fatal("local compatibility writer returned an untrusted body")
	}
	if err := localSvc.RecordSessionAgentRun(ctx, universalProjectB, "canonical-compat-session", universalTaskB, ""); err != nil {
		t.Fatalf("record canonical compatibility session: %v", err)
	}
	arealSvc := NewInteractionDAGService(q, &fakeArealSegmentClient{closeSegmentID: 7, exportPayload: []byte(shardExport)}, true)
	arealID, arealBody, err := arealSvc.CloseSegmentForEvent(ctx, universalProjectB, "canonical-compat-session", "proxy", "completion", nil)
	if err != nil {
		t.Fatalf("record AReaL compatibility segment on canonical schema: %v", err)
	}
	if arealID == "" || len(arealBody) != 0 {
		t.Fatal("AReaL compatibility writer returned an untrusted body")
	}

	if _, err := conn.Exec(ctx, `
		INSERT INTO interaction_dag_segment (
		  segment_id,project_id,agent_run_id,trajectory_id,tensor_ref,trainable,trajectory,
		  start_seq,end_seq,trajectory_source,workspace_id,generation,project_id_at_event,
		  memory_type_at_event,graph_projection_eligible_at_event,derivative,
		  trainable_eligible,content_status,provider_capture_status,run_id,run_agent_id
		) VALUES (
		  'legacy-body-resolver',$1,$2,9,'{"opaque":true}'::jsonb,true,
		  '[{"untrusted":true}]'::jsonb,0,0,'areal_tensor',$3,5,$4,
		  'legacy',false,false,false,'legacy_unverified','not_expected',$5,$6
		)
	`, universalProjectB, universalTaskB, universalWSB, universalProjectB,
		universalRunB, universalRunAgentB); err != nil {
		t.Fatalf("insert legacy body resolver fixture: %v", err)
	}
	masked, err := q.GetInteractionDAGSegmentByID(ctx, "legacy-body-resolver")
	if err != nil {
		t.Fatalf("read masked legacy segment: %v", err)
	}
	if masked.TrajectoryID != 0 || len(masked.TensorRef) != 0 || masked.Trainable || string(masked.Trajectory) != "[]" {
		t.Fatal("SQL legacy resolver surfaced body, tensor, or training data")
	}
	if _, err := GetSegmentMessages(ctx, q, q, workspaceB, "legacy-body-resolver"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("diagnosis resolver accepted legacy-unverified body: %v", err)
	}
	runASegments, err := q.ListUniversalDAGSegmentsByRun(ctx, db.ListUniversalDAGSegmentsByRunParams{
		WorkspaceID: workspaceA, RunID: mustUUID(t, universalRunA),
	})
	if err != nil {
		t.Fatalf("list canonical run segments: %v", err)
	}
	if len(runASegments) != 1 || runASegments[0].SegmentID != "query-a-run" {
		t.Fatalf("canonical run reader returned unexpected rows: %+v", runASegments)
	}
	runBSegments, err := q.ListUniversalDAGSegmentsByRun(ctx, db.ListUniversalDAGSegmentsByRunParams{
		WorkspaceID: workspaceB, RunID: mustUUID(t, universalRunB),
	})
	if err != nil {
		t.Fatalf("list run containing legacy-unverified segment: %v", err)
	}
	if len(runBSegments) != 0 {
		t.Fatal("run reader returned a legacy-unverified segment body")
	}

	dagA, err := svc.AssembleAssembledDag(ctx, universalProjectA)
	if err != nil {
		t.Fatalf("assemble canonical project A: %v", err)
	}
	if len(dagA.Segments) != 3 || len(dagA.Edges) != concurrentEdges {
		t.Fatalf("canonical project assembly filtering failed: segments=%d edges=%d", len(dagA.Segments), len(dagA.Edges))
	}
	dagB, err := svc.AssembleAssembledDag(ctx, universalProjectB)
	if err != nil {
		t.Fatalf("assemble canonical project B: %v", err)
	}
	for _, segment := range dagB.Segments {
		if segment.SegmentID != "legacy-body-resolver" {
			continue
		}
		if segment.TrajectoryID != nil || len(segment.TensorRef) != 0 || segment.Trainable || string(segment.Trajectory) != "[]" {
			t.Fatal("assembler surfaced legacy body, tensor, or training eligibility")
		}
		return
	}
	t.Fatal("legacy resolver fixture was not present in its canonical project view")
}

func TestUniversalDAGRetainedWriterGenerationConcurrency(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, conn := openUniversalDAGServiceSchema(t, ctx)
	defer conn.Release()
	if _, err := conn.Exec(ctx, universalDAGLegacySchema); err != nil {
		t.Fatalf("create pre-454 schema: %v", err)
	}
	applyUniversalDAGMigrationIfPresent(t, ctx, conn)
	seedUniversalDAGCanonicalOwners(t, ctx, conn)

	var schema string
	if err := conn.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatalf("read private schema name: %v", err)
	}
	const writers = 12
	start := make(chan struct{})
	errCh := make(chan error, writers)
	var wg sync.WaitGroup
	taskID := mustUUID(t, universalTaskB)
	for i := 0; i < writers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			worker, err := pool.Acquire(ctx)
			if err != nil {
				errCh <- err
				return
			}
			defer worker.Release()
			if _, err := worker.Exec(ctx, "SET search_path TO "+pgx.Identifier{schema}.Sanitize()); err != nil {
				errCh <- err
				return
			}
			<-start
			_, err = db.New(worker).InsertInteractionDAGSegmentWithSnapshot(ctx, db.InsertInteractionDAGSegmentWithSnapshotParams{
				SandboxIds:       []byte(`[]`),
				EnvState:         []byte(`{}`),
				ProjectID:        universalProjectB,
				AgentRunID:       taskID,
				SegmentID:        fmt.Sprintf("retained-concurrent-%02d", i),
				StartSeq:         0,
				EndSeq:           0,
				TrajectorySource: "task_messages",
				Trainable:        false,
				Trajectory:       []byte(`[]`),
			})
			errCh <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent retained writer failed: %v", err)
		}
	}

	var count, distinctGenerations int
	var minimumGeneration, maximumGeneration int64
	if err := conn.QueryRow(ctx, `
		SELECT count(*),count(DISTINCT generation),min(generation),max(generation)
		FROM interaction_dag_segment
		WHERE workspace_id=$1 AND agent_run_id=$2
		  AND segment_id LIKE 'retained-concurrent-%'
	`, universalWSB, universalTaskB).Scan(
		&count, &distinctGenerations, &minimumGeneration, &maximumGeneration,
	); err != nil {
		t.Fatalf("read retained writer generations: %v", err)
	}
	if count != writers || distinctGenerations != writers ||
		minimumGeneration != 1 || maximumGeneration != writers {
		t.Fatalf("retained generations are not distinct and contiguous: count=%d distinct=%d range=%d..%d",
			count, distinctGenerations, minimumGeneration, maximumGeneration)
	}
}

func TestUniversalDAGRetainedWriterOwnershipMismatchFailsClosed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	_, conn := openUniversalDAGServiceSchema(t, ctx)
	defer conn.Release()
	if _, err := conn.Exec(ctx, universalDAGLegacySchema); err != nil {
		t.Fatalf("create pre-454 schema: %v", err)
	}
	applyUniversalDAGMigrationIfPresent(t, ctx, conn)
	seedUniversalDAGCanonicalOwners(t, ctx, conn)

	q := db.New(conn)
	svc := NewInteractionDAGServiceWithMessages(q, q, nil, true)
	segmentID, body, err := svc.RecordLocalSegmentForEvent(
		ctx, universalProjectA, universalTaskB, "", "completion", nil,
	)
	if err == nil {
		t.Fatal("retained writer reported success for a project/task ownership mismatch")
	}
	if segmentID != "" || len(body) != 0 {
		t.Fatalf("failed retained writer returned segment identity or body: id=%q body_bytes=%d", segmentID, len(body))
	}
	var segmentCount, snapshotCount int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM interaction_dag_segment WHERE segment_id=$1`, "multica:"+universalTaskB).Scan(&segmentCount); err != nil {
		t.Fatalf("count mismatched retained segment: %v", err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM interaction_dag_env_snapshot WHERE segment_id=$1`, "multica:"+universalTaskB).Scan(&snapshotCount); err != nil {
		t.Fatalf("count mismatched retained snapshot: %v", err)
	}
	if segmentCount != 0 || snapshotCount != 0 {
		t.Fatalf("ownership mismatch persisted rows: segments=%d snapshots=%d", segmentCount, snapshotCount)
	}
}

type universalDAGSegment struct {
	WorkspaceID, SegmentID, AgentRunID, ProjectID, ChannelID string
	CloseActionKind, CanonicalActionID, VisibleActionKey     string
	ContentStatus, PublishStatus, RunID, RunAgentID          string
	ProviderCaptureStatus, ProviderCaptureID                 string
	ProviderCaptureCorrelationKey                            string
	Generation                                               int64
	StartSeq, EndSeq                                         *int32
	PublishSeq                                               *int64
	PublishedAt, RetractedAt                                 bool
}

func openUniversalDAGServiceSchema(t *testing.T, ctx context.Context) (*pgxpool.Pool, *pgxpool.Conn) {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		databaseURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("PostgreSQL required at %s: %v", databaseURL, err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("PostgreSQL required at %s: %v", databaseURL, err)
	}
	t.Cleanup(pool.Close)
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire PostgreSQL connection: %v", err)
	}
	schema := fmt.Sprintf("universal_dag_schema_test_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		conn.Release()
		t.Fatalf("create private schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if _, err := pool.Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+quoted+" CASCADE"); err != nil {
			t.Logf("drop private schema %s: %v", schema, err)
		}
	})
	if _, err := conn.Exec(ctx, "SET search_path TO "+quoted); err != nil {
		conn.Release()
		t.Fatalf("set private search_path: %v", err)
	}
	return pool, conn
}

func applyUniversalDAGMigrationIfPresent(t *testing.T, ctx context.Context, conn *pgxpool.Conn) {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate universal DAG schema test")
	}
	path := filepath.Join(filepath.Dir(filename), "..", "..", "migrations", "454_universal_interaction_dag.up.sql")
	migration, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatalf("read migration 454: %v", err)
	}
	if _, err := conn.Exec(ctx, string(migration)); err != nil {
		t.Fatalf("apply migration 454 in private schema: %v", err)
	}
}

func assertUniversalDAGColumn(t *testing.T, ctx context.Context, conn *pgxpool.Conn, table, column string) {
	t.Helper()
	var exists bool
	if err := conn.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1 FROM information_schema.columns
		  WHERE table_schema=current_schema() AND table_name=$1 AND column_name=$2
		)
	`, table, column).Scan(&exists); err != nil {
		t.Fatalf("inspect %s.%s: %v", table, column, err)
	}
	if !exists {
		t.Fatalf("missing canonical column/table %s.%s", table, column)
	}
}

func assertUniversalDAGNotNull(t *testing.T, ctx context.Context, conn *pgxpool.Conn, table, column string) {
	t.Helper()
	var nullable string
	if err := conn.QueryRow(ctx, `
		SELECT is_nullable FROM information_schema.columns
		WHERE table_schema=current_schema() AND table_name=$1 AND column_name=$2
	`, table, column).Scan(&nullable); err != nil {
		t.Fatalf("inspect nullability %s.%s: %v", table, column, err)
	}
	if nullable != "NO" {
		t.Fatalf("%s.%s is nullable", table, column)
	}
}

func seedUniversalDAGCanonicalOwners(t *testing.T, ctx context.Context, conn *pgxpool.Conn) {
	t.Helper()
	if _, err := conn.Exec(ctx, `
		INSERT INTO workspace(id) VALUES ($1),($2);
		INSERT INTO project(id,workspace_id) VALUES ($3,$1),($4,$2);
		INSERT INTO channel(id,workspace_id,project_id) VALUES ($5,$1,$3),($6,$2,$4);
		INSERT INTO agent_inbox_event(id,workspace_id,channel_id) VALUES ($7,$1,$5),($8,$2,$6);
		INSERT INTO task_message(task_id,seq,content) VALUES
		  ($7,1,'one'),($7,2,'two'),($7,3,'three'),($8,1,'other');
		INSERT INTO env_dispatch_run(project_id,workspace_id,run_id) VALUES ($3,$1,$9),($4,$2,$10);
		INSERT INTO env_dispatch_run_agent(run_agent_id,run_id) VALUES ($11,$9),($12,$10);
		INSERT INTO env_dispatch_resident_turn(turn_id,run_id,run_agent_id) VALUES ($13,$9,$11),($14,$10,$12);
		INSERT INTO pi_provider_call(call_id,run_id,run_agent_id,turn_id,call_ordinal) VALUES
		  ('call-a-1',$9,$11,$13,1),('call-a-2',$9,$11,$13,2),('call-a-3',$9,$11,$13,3),
		  ('call-b-1',$10,$12,$14,1);
	`, pgx.QueryExecModeSimpleProtocol, universalWSA, universalWSB, universalProjectA, universalProjectB,
		universalChannelA, universalChannelB, universalTaskA, universalTaskB,
		universalRunA, universalRunB, universalRunAgentA, universalRunAgentB,
		universalTurnA, universalTurnB); err != nil {
		t.Fatalf("seed canonical owners: %v", err)
	}
}

func int32Pointer(value int32) *int32 { return &value }
func int64Pointer(value int64) *int64 { return &value }

func execUniversalDAGLegacySegment(
	ctx context.Context,
	tx pgx.Tx,
	segmentID string,
	generation int64,
	graphEligible, trainableEligible bool,
) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO interaction_dag_segment (
		  workspace_id,segment_id,agent_run_id,generation,
		  project_id_at_event,channel_id_at_event,start_seq,end_seq,
		  close_action_kind,canonical_action_id,visible_action_key,
		  memory_type_at_event,graph_projection_eligible_at_event,
		  trajectory_source,derivative,trainable_eligible,
		  publish_status,publish_seq,content_status,
		  sanitizer_version,policy_version,provider_capture_status
		) VALUES (
		  $1,$2,$3,$4,
		  NULL,NULL,0,0,
		  NULL,NULL,NULL,
		  'graph',$5,
		  'task_messages',false,$6,
		  NULL,NULL,'legacy_unverified',
		  'dag-redaction-v1','universal-dag-v1','not_expected'
		)
	`, universalWSB, segmentID, universalTaskB, generation, graphEligible, trainableEligible)
	return err
}

func execUniversalDAGProviderLink(
	ctx context.Context,
	tx pgx.Tx,
	segmentID, providerCallID, role string,
	ordinal int64,
) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO interaction_dag_universal_provider_call
		  (segment_id,provider_call_id,role,ordinal,run_id,run_agent_id,capture_id)
		VALUES ($1,$2,$3,$4,$5,$6,'capture-a')
	`, segmentID, providerCallID, role, ordinal, universalRunA, universalRunAgentA)
	return err
}

func execUniversalDAGSegment(ctx context.Context, tx pgx.Tx, segment universalDAGSegment) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO interaction_dag_segment (
		  workspace_id,segment_id,agent_run_id,generation,
		  project_id_at_event,channel_id_at_event,start_seq,end_seq,
		  close_action_kind,canonical_action_id,visible_action_key,
		  memory_type_at_event,graph_projection_eligible_at_event,
		  trajectory_source,derivative,trainable_eligible,publish_status,content_status,
		  publish_seq,published_at,retracted_at,
		  sanitizer_version,policy_version,provider_capture_status,provider_capture_id,
		  provider_capture_version,provider_capture_correlation_key,run_id,run_agent_id
		) VALUES (
		  $1,$2,$3,$4,NULLIF($5,'')::uuid,NULLIF($6,'')::uuid,$7,$8,
		  $9,NULLIF($10,'')::uuid,NULLIF($11,''),
		  'graph',true,'task_messages',false,true,NULLIF($12,''),$13,
		  $19,CASE WHEN $20::boolean THEN now() ELSE NULL END,
		  CASE WHEN $21::boolean THEN now() ELSE NULL END,
		  'dag-redaction-v1','universal-dag-v1',COALESCE(NULLIF($14,''),'not_expected'),
		  NULLIF($15,''),CASE WHEN NULLIF($15,'') IS NULL THEN NULL ELSE 1 END,
		  NULLIF($16,''),NULLIF($17,'')::uuid,NULLIF($18,'')::uuid
		)
	`, segment.WorkspaceID, segment.SegmentID, segment.AgentRunID, segment.Generation,
		segment.ProjectID, segment.ChannelID, segment.StartSeq, segment.EndSeq,
		segment.CloseActionKind, segment.CanonicalActionID, segment.VisibleActionKey,
		segment.PublishStatus, segment.ContentStatus, segment.ProviderCaptureStatus,
		segment.ProviderCaptureID, segment.ProviderCaptureCorrelationKey,
		segment.RunID, segment.RunAgentID, segment.PublishSeq,
		segment.PublishedAt, segment.RetractedAt)
	return err
}

func assertUniversalDAGRangeIdentity(
	t *testing.T,
	ctx context.Context,
	conn *pgxpool.Conn,
	taskID string,
	startSeq, endSeq int,
) {
	t.Helper()
	var count, distinct, minimum, maximum int
	if err := conn.QueryRow(ctx, `
		SELECT count(*)::integer,count(DISTINCT seq)::integer,
		       min(seq)::integer,max(seq)::integer
		FROM task_message
		WHERE task_id=$1 AND seq BETWEEN $2 AND $3
	`, taskID, startSeq, endSeq).Scan(&count, &distinct, &minimum, &maximum); err != nil {
		t.Fatalf("read canonical task-message range identity: %v", err)
	}
	want := endSeq - startSeq + 1
	if count != want || distinct != want || minimum != startSeq || maximum != endSeq {
		t.Fatalf("canonical task-message range identity changed: count=%d distinct=%d min=%d max=%d",
			count, distinct, minimum, maximum)
	}
}

func execUniversalDAGSegmentAndOutbox(ctx context.Context, tx pgx.Tx, segment universalDAGSegment) error {
	if err := execUniversalDAGSegment(ctx, tx, segment); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO interaction_dag_publish_outbox
		  (workspace_id,segment_id,request_hash,status,attempts)
		VALUES ($1,$2,$3,'pending',0)
	`, segment.WorkspaceID, segment.SegmentID, "sha256:"+segment.SegmentID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `SET CONSTRAINTS ALL IMMEDIATE`)
	return err
}

func insertUniversalDAGSegmentAndOutbox(t *testing.T, ctx context.Context, conn *pgxpool.Conn, segment universalDAGSegment) {
	t.Helper()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin segment/outbox transaction: %v", err)
	}
	defer tx.Rollback(ctx)
	if err := execUniversalDAGSegmentAndOutbox(ctx, tx, segment); err != nil {
		t.Fatalf("insert and validate segment/outbox %s: %v", segment.SegmentID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit segment/outbox %s: %v", segment.SegmentID, err)
	}
}

func assertUniversalDAGPairRejected(t *testing.T, ctx context.Context, conn *pgxpool.Conn, segment universalDAGSegment) {
	t.Helper()
	assertUniversalDAGTransactionRejected(t, ctx, conn, func(tx pgx.Tx) error {
		if err := execUniversalDAGSegment(ctx, tx, segment); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO interaction_dag_publish_outbox
			  (workspace_id,segment_id,request_hash,status,attempts)
			VALUES ($1,$2,$3,'pending',0)
		`, segment.WorkspaceID, segment.SegmentID, "sha256:"+segment.SegmentID)
		return err
	})
}

func assertUniversalDAGTransactionRejected(t *testing.T, ctx context.Context, conn *pgxpool.Conn, body func(pgx.Tx) error) {
	t.Helper()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin rejected transaction: %v", err)
	}
	defer tx.Rollback(ctx)
	if err := body(tx); err != nil {
		return
	}
	if _, err := tx.Exec(ctx, `SET CONSTRAINTS ALL IMMEDIATE`); err != nil {
		return
	}
	if err := tx.Commit(ctx); err == nil {
		t.Fatal("invalid universal DAG transaction committed")
	}
}
