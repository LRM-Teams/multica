// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var (
	mixedRLProjectUUID   = util.MustParseUUID(testutil.MixedRLProjectID)
	mixedRLWorkspaceUUID = util.MustParseUUID(testutil.MixedRLWorkspaceID)
	mixedRLRunUUID       = util.MustParseUUID(testutil.MixedRLRunID)
)

type mixedRLRepositoryHarness struct {
	ctx    context.Context
	tx     pgx.Tx
	runs   *EnvDispatchRunStore
	ledger *ProviderCallLedger
}

func newMixedRLRepositoryHarness(t *testing.T) mixedRLRepositoryHarness {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("integration test requires PostgreSQL at DATABASE_URL")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	schema := fmt.Sprintf("mixed_rl_%d", time.Now().UnixNano())
	_, err = tx.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, "SET LOCAL search_path TO "+schema)
	require.NoError(t, err)

	createMixedRLBaseSchema(t, ctx, tx)
	applyMixedRLMigrations(t, ctx, tx)
	queries := db.New(tx)
	return mixedRLRepositoryHarness{
		ctx:    ctx,
		tx:     tx,
		runs:   NewEnvDispatchRunStore(queries),
		ledger: NewProviderCallLedger(queries, tx),
	}
}

func createMixedRLBaseSchema(t *testing.T, ctx context.Context, tx pgx.Tx) {
	t.Helper()
	_, err := tx.Exec(ctx, `
CREATE TABLE workspace (id uuid PRIMARY KEY);
CREATE TABLE project (id uuid PRIMARY KEY);
CREATE TABLE source_task (id uuid PRIMARY KEY);
CREATE TABLE issue (id uuid PRIMARY KEY);
CREATE TABLE channel (id uuid PRIMARY KEY);
CREATE TABLE agent (id uuid PRIMARY KEY);
CREATE TABLE agent_runtime (id uuid PRIMARY KEY);
CREATE TABLE channel_message (id uuid PRIMARY KEY);
CREATE TABLE channel_message_reaction (id uuid PRIMARY KEY);
CREATE TABLE env_dispatch_run (
  project_id uuid PRIMARY KEY REFERENCES project(id) ON DELETE CASCADE,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  training_mode boolean NOT NULL,
  root_task_id uuid,
  created_at timestamptz NOT NULL DEFAULT now(),
  run_id uuid NOT NULL DEFAULT gen_random_uuid(),
  source_task_id uuid REFERENCES source_task(id) ON DELETE RESTRICT,
  sample_index integer NOT NULL DEFAULT 0,
  local_issue_id uuid REFERENCES issue(id) ON DELETE SET NULL,
  local_channel_id uuid REFERENCES channel(id) ON DELETE SET NULL,
  UNIQUE (run_id)
);`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, "INSERT INTO workspace (id) VALUES ($1)", mixedRLWorkspaceUUID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, "INSERT INTO project (id) VALUES ($1)", mixedRLProjectUUID)
	require.NoError(t, err)
}

func applyMixedRLMigrations(t *testing.T, ctx context.Context, tx pgx.Tx) {
	t.Helper()
	for _, name := range []string{
		"313_env_dispatch_mixed_rl_run.up.sql",
		"314_pi_provider_call_ledger.up.sql",
		"315_interaction_dag_frozen_snapshot.up.sql",
	} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", name))
		require.NoError(t, err)
		_, err = tx.Exec(ctx, string(raw))
		require.NoError(t, err, "apply %s", name)
	}
}

func expectMixedRLConstraintError(t *testing.T, h mixedRLRepositoryHarness, fn func() error) {
	t.Helper()
	_, err := h.tx.Exec(h.ctx, "SAVEPOINT mixed_rl_expected_error")
	require.NoError(t, err)
	err = fn()
	require.Error(t, err)
	_, rollbackErr := h.tx.Exec(h.ctx, "ROLLBACK TO SAVEPOINT mixed_rl_expected_error")
	require.NoError(t, rollbackErr)
	_, releaseErr := h.tx.Exec(h.ctx, "RELEASE SAVEPOINT mixed_rl_expected_error")
	require.NoError(t, releaseErr)
}

func createMixedRLRun(t *testing.T, h mixedRLRepositoryHarness) EnvDispatchRunRecord {
	t.Helper()
	run, err := h.runs.CreateRun(h.ctx, CreateEnvDispatchRunInput{
		RunID:               mixedRLRunUUID,
		ProjectID:           mixedRLProjectUUID,
		WorkspaceID:         mixedRLWorkspaceUUID,
		QuietWindowMS:       2_000,
		TotalTimeoutSeconds: 3_300,
	})
	require.NoError(t, err)
	return run
}

func bindMixedRLAgent(t *testing.T, h mixedRLRepositoryHarness, ordinal int, mode string) EnvDispatchRunAgentRecord {
	t.Helper()
	fixture := testutil.MixedRLRunAgentFixture(ordinal, mode)
	for _, id := range []string{fixture.SourceAgentID, fixture.ExecutionAgentID, fixture.RuntimeID} {
		var table string
		switch id {
		case fixture.RuntimeID:
			table = "agent_runtime"
		default:
			table = "agent"
		}
		_, err := h.tx.Exec(h.ctx, fmt.Sprintf("INSERT INTO %s (id) VALUES ($1) ON CONFLICT DO NOTHING", table), util.MustParseUUID(id))
		require.NoError(t, err)
	}
	record, err := h.runs.BindRunAgent(h.ctx, BindEnvDispatchRunAgentInput{
		RunID:            mixedRLRunUUID,
		SourceAgentID:    util.MustParseUUID(fixture.SourceAgentID),
		ExecutionAgentID: util.MustParseUUID(fixture.ExecutionAgentID),
		RuntimeID:        util.MustParseUUID(fixture.RuntimeID),
		PiSessionID:      fixture.PiSessionID,
		TrainingMode:     mode,
		AReALSessionID:   fixture.AReALSessionID,
		CaptureBoundary:  "boundary-synthetic",
	})
	require.NoError(t, err)
	return record
}

func createMixedRLTurn(t *testing.T, h mixedRLRepositoryHarness, agent EnvDispatchRunAgentRecord) ResidentTurnRecord {
	t.Helper()
	turn, err := h.runs.CreateResidentTurn(h.ctx, CreateResidentTurnInput{
		TurnID:     util.MustParseUUID("70000000-0000-4000-8000-000000000080"),
		RunID:      mixedRLRunUUID,
		RunAgentID: agent.RunAgentID,
		Status:     "active",
	})
	require.NoError(t, err)
	return turn
}

func TestEnvDispatchRunStore_LifecycleTimeoutAndTerminalSnapshot(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	assert.Equal(t, "provisioning", run.Status)
	assert.Equal(t, int32(2_000), run.QuietWindowMS)

	run, err := h.runs.TransitionStatus(h.ctx, run.RunID, "provisioning", "preflight")
	require.NoError(t, err)
	assert.Equal(t, "preflight", run.Status)

	submittedAt := time.Date(2026, time.August, 10, 2, 11, 0, 0, time.UTC)
	run, err = h.runs.StartTimeout(h.ctx, run.RunID, submittedAt)
	require.NoError(t, err)
	assert.Equal(t, "running", run.Status)
	assert.WithinDuration(t, submittedAt.Add(3_300*time.Second), run.TimeoutDeadlineAt, time.Microsecond)

	_, err = h.runs.TransitionStatus(h.ctx, run.RunID, "running", "completed")
	require.Error(t, err, "terminal status without an immutable snapshot must fail")
}

func TestEnvDispatchRunStore_RunAgentUniqueness(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	createMixedRLRun(t, h)
	first := bindMixedRLAgent(t, h, 1, "online_rl")

	fixture := testutil.MixedRLRunAgentFixture(2, "offline_rl")
	_, err := h.tx.Exec(h.ctx, "INSERT INTO agent (id) VALUES ($1), ($2)", util.MustParseUUID(fixture.SourceAgentID), util.MustParseUUID(fixture.ExecutionAgentID))
	require.NoError(t, err)
	_, err = h.tx.Exec(h.ctx, "INSERT INTO agent_runtime (id) VALUES ($1)", util.MustParseUUID(fixture.RuntimeID))
	require.NoError(t, err)

	base := BindEnvDispatchRunAgentInput{
		RunID:            mixedRLRunUUID,
		SourceAgentID:    util.MustParseUUID(fixture.SourceAgentID),
		ExecutionAgentID: util.MustParseUUID(fixture.ExecutionAgentID),
		RuntimeID:        util.MustParseUUID(fixture.RuntimeID),
		PiSessionID:      fixture.PiSessionID,
		TrainingMode:     "offline_rl",
		CaptureBoundary:  "boundary-two",
	}

	duplicateSource := base
	duplicateSource.SourceAgentID = first.SourceAgentID
	expectMixedRLConstraintError(t, h, func() error {
		_, err := h.runs.BindRunAgent(h.ctx, duplicateSource)
		return err
	})

	duplicateExecution := base
	duplicateExecution.ExecutionAgentID = first.ExecutionAgentID
	expectMixedRLConstraintError(t, h, func() error {
		_, err := h.runs.BindRunAgent(h.ctx, duplicateExecution)
		return err
	})

	duplicateSession := base
	duplicateSession.PiSessionID = first.PiSessionID
	expectMixedRLConstraintError(t, h, func() error {
		_, err := h.runs.BindRunAgent(h.ctx, duplicateSession)
		return err
	})
}

func TestProviderCallLedger_CallOrdinalAndOneOwnerConstraints(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 1, "online_rl")
	turn := createMixedRLTurn(t, h, agent)

	call := ProviderCallInput{
		CallID:                "call-synthetic-1",
		RunID:                 mixedRLRunUUID,
		RunAgentID:            agent.RunAgentID,
		TurnID:                turn.TurnID,
		PiSessionID:           agent.PiSessionID,
		CallOrdinal:           1,
		Provider:              "synthetic-provider",
		Model:                 "synthetic-model",
		APIKind:               "messages",
		RawProviderRequest:    []byte(`{"messages":[]}`),
		FinalAssistantMessage: []byte(`{"role":"assistant","blocks":[{"type":"text","text":"synthetic"}]}`),
		Status:                "completed",
		StopReason:            "stop",
		ResponseComplete:      true,
		TrainingEligible:      true,
		AReALSessionID:        "areal-session-1",
		AReALCallID:           "areal-call-1",
		RequestHash:           "sha256:request-1",
		ResponseHash:          "sha256:response-1",
	}
	created, err := h.ledger.InsertProviderCall(h.ctx, call)
	require.NoError(t, err)

	duplicateOrdinal := call
	duplicateOrdinal.CallID = "call-synthetic-2"
	duplicateOrdinal.AReALCallID = "areal-call-2"
	expectMixedRLConstraintError(t, h, func() error {
		_, err := h.ledger.InsertProviderCall(h.ctx, duplicateOrdinal)
		return err
	})

	for ordinal, segmentID := range []string{"message:synthetic-1", "message:synthetic-2"} {
		_, err = h.ledger.InsertSegment(h.ctx, SegmentInput{
			SegmentID:         segmentID,
			RunID:             mixedRLRunUUID,
			RunAgentID:        agent.RunAgentID,
			Kind:              "message",
			CanonicalActionID: fmt.Sprintf("70000000-0000-4000-8000-00000000009%d", ordinal),
			SegmentOrdinal:    int64(ordinal + 1),
		})
		require.NoError(t, err)
	}

	require.NoError(t, h.ledger.AssociateProviderCall(h.ctx, SegmentCallAssociationInput{
		SegmentID: "message:synthetic-1", ProviderCallID: created.CallID,
		CallOrdinal: 1, AssociationKind: "owned",
	}))
	expectMixedRLConstraintError(t, h, func() error {
		return h.ledger.AssociateProviderCall(h.ctx, SegmentCallAssociationInput{
			SegmentID: "message:synthetic-2", ProviderCallID: created.CallID,
			CallOrdinal: 1, AssociationKind: "owned",
		})
	})
}

func TestEnvDispatchRunStore_ActivityCountersNeverBecomeNegative(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)

	run, err := h.runs.AdjustActivity(h.ctx, run.RunID, ActivityCounterDelta{ActiveTurns: 1, PendingDeliveries: 1})
	require.NoError(t, err)
	assert.Equal(t, int64(1), run.ActiveTurnCount)
	assert.Equal(t, int64(1), run.PendingDeliveryCount)

	run, err = h.runs.AdjustActivity(h.ctx, run.RunID, ActivityCounterDelta{ActiveTurns: -1, PendingDeliveries: -1})
	require.NoError(t, err)
	assert.Zero(t, run.ActiveTurnCount)
	assert.Zero(t, run.PendingDeliveryCount)

	_, err = h.runs.AdjustActivity(h.ctx, run.RunID, ActivityCounterDelta{ActiveTurns: -1})
	require.Error(t, err)
}

func TestProviderCallLedger_FrozenSnapshotCaptureGapAndLateEvent(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 2, "offline_rl")
	turn := createMixedRLTurn(t, h, agent)

	require.NoError(t, h.runs.RecordCaptureGap(h.ctx, CaptureGapInput{
		EventID:    util.MustParseUUID("70000000-0000-4000-8000-0000000000a1"),
		RunID:      run.RunID,
		RunAgentID: agent.RunAgentID,
		TurnID:     turn.TurnID,
		Reason:     "turn_batch_missing",
		Summary:    []byte(`{"call_count":0}`),
	}))

	advanceMixedRLRunToQuietCandidate(t, h, run.RunID)
	snapshot, run, err := h.ledger.FreezeAndComplete(h.ctx, FrozenSnapshotInput{
		SnapshotID:           testutil.MixedRLSnapshotID,
		RunID:                run.RunID,
		RunStatus:            "completed",
		SchemaVersion:        "1",
		NormalizationVersion: "1",
		CanonicalManifest:    []byte(`{"calls":[],"segments":[],"edges":[]}`),
		SnapshotHash:         "sha256:synthetic-mixed-rl-snapshot",
	})
	require.NoError(t, err)
	assert.Equal(t, "completed", run.Status)
	assert.Equal(t, int64(1), run.CaptureGapCount)

	expectMixedRLConstraintError(t, h, func() error {
		_, err := h.tx.Exec(h.ctx, `UPDATE interaction_dag_frozen_snapshot SET snapshot_hash = 'sha256:changed' WHERE snapshot_id = $1`, snapshot.SnapshotID)
		return err
	})

	require.NoError(t, h.runs.RecordLateEvent(h.ctx, LateEventInput{
		EventID:    util.MustParseUUID("70000000-0000-4000-8000-0000000000a2"),
		RunID:      run.RunID,
		RunAgentID: agent.RunAgentID,
		TurnID:     turn.TurnID,
		Reason:     "capture_after_freeze",
		Summary:    []byte(`{"provider_call_count":1}`),
		SnapshotID: snapshot.SnapshotID,
	}))

	got, err := h.ledger.GetFrozenSnapshot(h.ctx, run.RunID)
	require.NoError(t, err)
	assert.Equal(t, snapshot.SnapshotHash, got.SnapshotHash)

	events, err := h.runs.ListAuditEvents(h.ctx, run.RunID)
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, "capture_gap", events[0].Kind)
	assert.Equal(t, "late_event", events[1].Kind)
}

type frozenCaptureGapTestFixture struct {
	run      EnvDispatchRunRecord
	agent    EnvDispatchRunAgentRecord
	turn     ResidentTurnRecord
	eventID  pgtype.UUID
	snapshot FrozenSnapshotRecord
	before   FrozenDAGRecord
}

func createFrozenCaptureGapTestFixture(
	t *testing.T,
	h mixedRLRepositoryHarness,
	terminalStatus string,
) frozenCaptureGapTestFixture {
	t.Helper()
	run := createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 2, "offline_rl")
	turn := createMixedRLTurn(t, h, agent)
	eventID := util.MustParseUUID("70000000-0000-4000-8000-0000000000a3")
	require.NoError(t, h.runs.RecordCaptureGap(h.ctx, CaptureGapInput{
		EventID: eventID, RunID: run.RunID, RunAgentID: agent.RunAgentID,
		TurnID: turn.TurnID, Reason: "turn_batch_missing",
		Summary: []byte(`{"call_count":0}`),
	}))

	if terminalStatus == "completed" {
		advanceMixedRLRunToQuietCandidate(t, h, run.RunID)
	} else {
		require.Equal(t, "failed_timeout", terminalStatus)
		advanceMixedRLRunToRunning(t, h, run.RunID)
	}
	snapshotID := "sha256:frozen-capture-gap-" + terminalStatus
	snapshot, terminal, err := h.ledger.FreezeAndComplete(h.ctx, FrozenSnapshotInput{
		SnapshotID: snapshotID, RunID: run.RunID, RunStatus: terminalStatus,
		SchemaVersion: "1", NormalizationVersion: "1",
		CanonicalManifest: []byte(`{"calls":[],"segments":[],"edges":[]}`),
		SnapshotHash:      snapshotID,
	})
	require.NoError(t, err)
	before, err := h.ledger.GetFrozenDAG(h.ctx, run.RunID, snapshot.SnapshotID)
	require.NoError(t, err)
	expectedGaps := 1
	if terminalStatus == "failed_timeout" {
		// Timeout freeze settles every still-active turn with its own durable
		// run_timeout gap in addition to any gap recorded before freezing.
		expectedGaps++
	}
	require.Len(t, before.CaptureGaps, expectedGaps)

	return frozenCaptureGapTestFixture{
		run: terminal, agent: agent, turn: turn, eventID: eventID,
		snapshot: snapshot, before: before,
	}
}

func TestProviderCallLedger_FrozenCaptureGapRejectsDirectDeleteAndPreservesDAG(t *testing.T) {
	for _, terminalStatus := range []string{"completed", "failed_timeout"} {
		t.Run(terminalStatus, func(t *testing.T) {
			h := newMixedRLRepositoryHarness(t)
			fixture := createFrozenCaptureGapTestFixture(t, h, terminalStatus)

			expectMixedRLConstraintError(t, h, func() error {
				_, err := h.tx.Exec(h.ctx, `
					DELETE FROM env_dispatch_run_audit_event
					WHERE event_id = $1
				`, fixture.eventID)
				return err
			})

			after, err := h.ledger.GetFrozenDAG(
				h.ctx, fixture.run.RunID, fixture.snapshot.SnapshotID,
			)
			require.NoError(t, err)
			assert.Equal(t, fixture.before, after)
		})
	}
}

func TestProviderCallLedger_FrozenCaptureGapRejectsReparentAndKindConversion(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	fixture := createFrozenCaptureGapTestFixture(t, h, "completed")

	targetRunID := util.MustParseUUID("70000000-0000-4000-8000-0000000000a4")
	targetProjectID := util.MustParseUUID("70000000-0000-4000-8000-0000000000a5")
	createMixedRLRunWithIDs(t, h, targetRunID, targetProjectID)
	advanceMixedRLRunToQuietCandidate(t, h, targetRunID)
	targetSnapshot, _, err := h.ledger.FreezeAndComplete(h.ctx, FrozenSnapshotInput{
		SnapshotID: "sha256:capture-gap-reparent-target", RunID: targetRunID,
		RunStatus: "completed", SchemaVersion: "1", NormalizationVersion: "1",
		CanonicalManifest: []byte(`{"calls":[],"segments":[],"edges":[]}`),
		SnapshotHash:      "sha256:capture-gap-reparent-target",
	})
	require.NoError(t, err)

	mutations := []struct {
		name string
		sql  string
		args []any
	}{
		{
			name: "run_id",
			sql:  `UPDATE env_dispatch_run_audit_event SET run_id = $2, run_agent_id = NULL, turn_id = NULL WHERE event_id = $1`,
			args: []any{fixture.eventID, targetRunID},
		},
		{
			name: "kind_and_snapshot",
			sql:  `UPDATE env_dispatch_run_audit_event SET kind = 'late_event', snapshot_id = $2 WHERE event_id = $1`,
			args: []any{fixture.eventID, fixture.snapshot.SnapshotID},
		},
		{
			name: "snapshot",
			sql:  `UPDATE env_dispatch_run_audit_event SET snapshot_id = $2 WHERE event_id = $1`,
			args: []any{fixture.eventID, fixture.snapshot.SnapshotID},
		},
		{
			name: "turn",
			sql:  `UPDATE env_dispatch_run_audit_event SET turn_id = NULL WHERE event_id = $1`,
			args: []any{fixture.eventID},
		},
		{
			name: "summary",
			sql:  `UPDATE env_dispatch_run_audit_event SET summary = '{"mutated":true}'::jsonb WHERE event_id = $1`,
			args: []any{fixture.eventID},
		},
		{
			name: "reason",
			sql:  `UPDATE env_dispatch_run_audit_event SET reason = 'mutated_after_freeze' WHERE event_id = $1`,
			args: []any{fixture.eventID},
		},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			expectMixedRLConstraintError(t, h, func() error {
				_, updateErr := h.tx.Exec(h.ctx, mutation.sql, mutation.args...)
				return updateErr
			})
		})
	}

	expectMixedRLConstraintError(t, h, func() error {
		_, updateErr := h.tx.Exec(h.ctx, `
			UPDATE env_dispatch_run_audit_event
			SET run_id = $1,
			    run_agent_id = NULL,
			    turn_id = NULL,
			    kind = 'late_event',
			    snapshot_id = $2,
			    reason = 'converted_after_freeze',
			    summary = '{"converted":true}'::jsonb
			WHERE event_id = $3
		`, targetRunID, targetSnapshot.SnapshotID, fixture.eventID)
		return updateErr
	})

	after, err := h.ledger.GetFrozenDAG(
		h.ctx, fixture.run.RunID, fixture.snapshot.SnapshotID,
	)
	require.NoError(t, err)
	assert.Equal(t, fixture.before, after)
}

func TestProviderCallLedger_TerminalLateEventInsertAndWholeRunCascadeRemainValid(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	fixture := createFrozenCaptureGapTestFixture(t, h, "completed")

	require.NoError(t, h.runs.RecordLateEvent(h.ctx, LateEventInput{
		EventID: util.MustParseUUID("70000000-0000-4000-8000-0000000000a6"),
		RunID:   fixture.run.RunID, RunAgentID: fixture.agent.RunAgentID,
		TurnID: fixture.turn.TurnID, Reason: "capture_after_freeze",
		Summary: []byte(`{"provider_call_count":1}`), SnapshotID: fixture.snapshot.SnapshotID,
	}))
	afterLateEvent, err := h.ledger.GetFrozenDAG(
		h.ctx, fixture.run.RunID, fixture.snapshot.SnapshotID,
	)
	require.NoError(t, err)
	assert.Equal(t, fixture.before, afterLateEvent)

	deleted, err := h.runs.DeleteRun(h.ctx, fixture.run.RunID, fixture.run.WorkspaceID)
	require.NoError(t, err)
	require.True(t, deleted)
	_, err = h.tx.Exec(h.ctx, "SET CONSTRAINTS ALL IMMEDIATE")
	require.NoError(t, err, "whole-run cascade must satisfy deferred snapshot references")

	for _, table := range []string{
		"env_dispatch_run", "env_dispatch_run_audit_event",
		"interaction_dag_frozen_snapshot",
	} {
		var count int
		require.NoError(t, h.tx.QueryRow(h.ctx,
			fmt.Sprintf("SELECT count(*) FROM %s", table),
		).Scan(&count))
		assert.Zero(t, count, table)
	}
}

func TestMixedRLMigrations_RollBackInReverseOrder(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	for _, name := range []string{
		"315_interaction_dag_frozen_snapshot.down.sql",
		"314_pi_provider_call_ledger.down.sql",
		"313_env_dispatch_mixed_rl_run.down.sql",
	} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", name))
		require.NoError(t, err)
		_, err = h.tx.Exec(h.ctx, string(raw))
		require.NoError(t, err, "roll back %s", name)
	}

	var exists bool
	err := h.tx.QueryRow(h.ctx, `SELECT to_regclass('env_dispatch_run_agent') IS NOT NULL`).Scan(&exists)
	require.NoError(t, err)
	assert.False(t, exists)
}

func createMixedRLRunWithIDs(t *testing.T, h mixedRLRepositoryHarness, runID, projectID pgtype.UUID) EnvDispatchRunRecord {
	t.Helper()
	_, err := h.tx.Exec(h.ctx, "INSERT INTO project (id) VALUES ($1)", projectID)
	require.NoError(t, err)
	run, err := h.runs.CreateRun(h.ctx, CreateEnvDispatchRunInput{
		RunID:               runID,
		ProjectID:           projectID,
		WorkspaceID:         mixedRLWorkspaceUUID,
		QuietWindowMS:       2_000,
		TotalTimeoutSeconds: 3_300,
	})
	require.NoError(t, err)
	return run
}

func bindMixedRLAgentForRun(t *testing.T, h mixedRLRepositoryHarness, runID pgtype.UUID, ordinal int, mode string) EnvDispatchRunAgentRecord {
	t.Helper()
	fixture := testutil.MixedRLRunAgentFixture(ordinal, mode)
	_, err := h.tx.Exec(h.ctx, "INSERT INTO agent (id) VALUES ($1), ($2)", util.MustParseUUID(fixture.SourceAgentID), util.MustParseUUID(fixture.ExecutionAgentID))
	require.NoError(t, err)
	_, err = h.tx.Exec(h.ctx, "INSERT INTO agent_runtime (id) VALUES ($1)", util.MustParseUUID(fixture.RuntimeID))
	require.NoError(t, err)
	record, err := h.runs.BindRunAgent(h.ctx, BindEnvDispatchRunAgentInput{
		RunID:            runID,
		SourceAgentID:    util.MustParseUUID(fixture.SourceAgentID),
		ExecutionAgentID: util.MustParseUUID(fixture.ExecutionAgentID),
		RuntimeID:        util.MustParseUUID(fixture.RuntimeID),
		PiSessionID:      fixture.PiSessionID,
		TrainingMode:     mode,
		AReALSessionID:   fixture.AReALSessionID,
		CaptureBoundary:  fmt.Sprintf("boundary-%d", ordinal),
	})
	require.NoError(t, err)
	return record
}

func createMixedRLTurnWithID(t *testing.T, h mixedRLRepositoryHarness, runID pgtype.UUID, agent EnvDispatchRunAgentRecord, turnID string) ResidentTurnRecord {
	t.Helper()
	turn, err := h.runs.CreateResidentTurn(h.ctx, CreateResidentTurnInput{
		TurnID:     util.MustParseUUID(turnID),
		RunID:      runID,
		RunAgentID: agent.RunAgentID,
		Status:     "active",
	})
	require.NoError(t, err)
	return turn
}

func mixedRLProviderCallInput(runID pgtype.UUID, agent EnvDispatchRunAgentRecord, turn ResidentTurnRecord, callID string, ordinal int64) ProviderCallInput {
	fixture := testutil.MixedRLProviderCallFixture(testutil.MixedRLAgent{
		RunAgentID:  fmt.Sprintf("%x", agent.RunAgentID.Bytes),
		PiSessionID: agent.PiSessionID, AReALSessionID: agent.AReALSessionID,
		TrainingMode: agent.TrainingMode,
	}, ordinal)
	return ProviderCallInput{
		CallID: callID, RunID: runID, RunAgentID: agent.RunAgentID, TurnID: turn.TurnID,
		PiSessionID: fixture.PiSessionID, CallOrdinal: fixture.CallOrdinal,
		Provider: fixture.Provider, Model: fixture.Model, APIKind: fixture.APIKind,
		RawProviderRequest:    fixture.RawProviderRequest,
		FinalAssistantMessage: fixture.FinalAssistantMessage,
		NormalizedTrajectory:  fixture.NormalizedTrajectory,
		NormalizationVersion:  fixture.NormalizationVersion,
		Status:                fixture.Status, StopReason: fixture.StopReason,
		ResponseComplete: fixture.ResponseComplete, TrainingEligible: fixture.TrainingEligible,
		AReALSessionID: fixture.AReALSessionID, AReALCallID: fixture.AReALCallID,
		RequestHash:  fmt.Sprintf("sha256:request-%s", callID),
		ResponseHash: fmt.Sprintf("sha256:response-%s", callID),
		StartedAt:    fixture.StartedAt, CompletedAt: fixture.CompletedAt,
	}
}

func advanceMixedRLRunToRunning(t *testing.T, h mixedRLRepositoryHarness, runID pgtype.UUID) {
	t.Helper()
	_, err := h.runs.TransitionStatus(h.ctx, runID, "provisioning", "preflight")
	require.NoError(t, err)
	_, err = h.runs.StartTimeout(h.ctx, runID, time.Date(2026, time.August, 10, 3, 0, 0, 0, time.UTC))
	require.NoError(t, err)
}

func advanceMixedRLRunToQuietCandidate(t *testing.T, h mixedRLRepositoryHarness, runID pgtype.UUID) {
	t.Helper()
	advanceMixedRLRunToRunning(t, h, runID)
	_, err := h.runs.TransitionStatus(h.ctx, runID, "running", "quiet_candidate")
	require.NoError(t, err)
}

func advanceMixedRLRunToFreezing(t *testing.T, h mixedRLRepositoryHarness, runID pgtype.UUID) {
	t.Helper()
	advanceMixedRLRunToRunning(t, h, runID)
	_, err := h.runs.TransitionStatus(h.ctx, runID, "running", "freezing")
	require.NoError(t, err)
}

func TestEnvDispatchRunStore_RejectsInvalidLifecycleTransitions(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)

	_, err := h.runs.TransitionStatus(h.ctx, run.RunID, "provisioning", "running")
	require.Error(t, err)

	got, err := h.runs.queries.GetMixedRLRun(h.ctx, run.RunID)
	require.NoError(t, err)
	assert.Equal(t, "provisioning", got.Status)
}

func TestProviderCallLedger_RejectsOrdinalGapAndRegression(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 1, "online_rl")
	turn := createMixedRLTurn(t, h, agent)

	first := mixedRLProviderCallInput(mixedRLRunUUID, agent, turn, "call-ordinal-1", 1)
	_, err := h.ledger.InsertProviderCall(h.ctx, first)
	require.NoError(t, err)

	gap := mixedRLProviderCallInput(mixedRLRunUUID, agent, turn, "call-ordinal-3", 3)
	_, err = h.ledger.InsertProviderCall(h.ctx, gap)
	require.Error(t, err)

	second := mixedRLProviderCallInput(mixedRLRunUUID, agent, turn, "call-ordinal-2", 2)
	created, err := h.ledger.InsertProviderCall(h.ctx, second)
	require.NoError(t, err)
	assert.Equal(t, int64(2), created.CallOrdinal)

	regression := mixedRLProviderCallInput(mixedRLRunUUID, agent, turn, "call-ordinal-regression", 1)
	_, err = h.ledger.InsertProviderCall(h.ctx, regression)
	require.Error(t, err)
}

func TestProviderCallLedger_RejectsCallerDeclaredInvalidEligibility(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 1, "online_rl")
	turn := createMixedRLTurn(t, h, agent)

	call := mixedRLProviderCallInput(mixedRLRunUUID, agent, turn, "call-invalid-eligibility", 1)
	call.Status = "error"
	call.StopReason = ""
	call.ResponseComplete = false
	call.TrainingEligible = true

	_, err := h.ledger.InsertProviderCall(h.ctx, call)
	require.Error(t, err)
}

func TestProviderCallLedger_RejectsCrossScopeAndMissingAssociations(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	createMixedRLRun(t, h)
	agentOne := bindMixedRLAgent(t, h, 1, "online_rl")
	turnOne := createMixedRLTurnWithID(t, h, mixedRLRunUUID, agentOne, "70000000-0000-4000-8000-000000000080")
	callOne, err := h.ledger.InsertProviderCall(h.ctx, mixedRLProviderCallInput(mixedRLRunUUID, agentOne, turnOne, "call-association-one", 1))
	require.NoError(t, err)
	_, err = h.ledger.InsertSegment(h.ctx, SegmentInput{
		SegmentID:         "message:association-one",
		RunID:             mixedRLRunUUID,
		RunAgentID:        agentOne.RunAgentID,
		Kind:              "message",
		CanonicalActionID: "70000000-0000-4000-8000-000000000091",
		SegmentOrdinal:    1,
	})
	require.NoError(t, err)

	agentTwo := bindMixedRLAgentForRun(t, h, mixedRLRunUUID, 2, "offline_rl")
	turnTwo := createMixedRLTurnWithID(t, h, mixedRLRunUUID, agentTwo, "70000000-0000-4000-8000-000000000081")
	callTwo, err := h.ledger.InsertProviderCall(h.ctx, mixedRLProviderCallInput(mixedRLRunUUID, agentTwo, turnTwo, "call-association-two", 1))
	require.NoError(t, err)

	otherRunID := util.MustParseUUID("70000000-0000-4000-8000-000000000004")
	otherProjectID := util.MustParseUUID("70000000-0000-4000-8000-000000000005")
	createMixedRLRunWithIDs(t, h, otherRunID, otherProjectID)
	agentThree := bindMixedRLAgentForRun(t, h, otherRunID, 3, "none")
	turnThree := createMixedRLTurnWithID(t, h, otherRunID, agentThree, "70000000-0000-4000-8000-000000000082")
	callThree, err := h.ledger.InsertProviderCall(h.ctx, mixedRLProviderCallInput(otherRunID, agentThree, turnThree, "call-association-three", 1))
	require.NoError(t, err)

	for name, input := range map[string]SegmentCallAssociationInput{
		"wrong agent":     {SegmentID: "message:association-one", ProviderCallID: callTwo.CallID, CallOrdinal: 1, AssociationKind: "owned"},
		"cross run":       {SegmentID: "message:association-one", ProviderCallID: callThree.CallID, CallOrdinal: 1, AssociationKind: "owned"},
		"missing call":    {SegmentID: "message:association-one", ProviderCallID: "call-missing", CallOrdinal: 1, AssociationKind: "owned"},
		"missing segment": {SegmentID: "message:missing", ProviderCallID: callOne.CallID, CallOrdinal: 1, AssociationKind: "owned"},
	} {
		t.Run(name, func(t *testing.T) {
			err := h.ledger.AssociateProviderCall(h.ctx, input)
			require.Error(t, err)
		})
	}
}

func TestEnvDispatchRunStore_RejectsCrossRunTerminalSnapshot(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	advanceMixedRLRunToFreezing(t, h, run.RunID)

	otherRunID := util.MustParseUUID("70000000-0000-4000-8000-000000000006")
	otherProjectID := util.MustParseUUID("70000000-0000-4000-8000-000000000007")
	createMixedRLRunWithIDs(t, h, otherRunID, otherProjectID)
	advanceMixedRLRunToQuietCandidate(t, h, otherRunID)
	otherSnapshot, _, err := h.ledger.FreezeAndComplete(h.ctx, FrozenSnapshotInput{
		SnapshotID:           "sha256:other-run-snapshot",
		RunID:                otherRunID,
		RunStatus:            "completed",
		SchemaVersion:        "1",
		NormalizationVersion: "1",
		CanonicalManifest:    []byte(`{"calls":[],"segments":[],"edges":[]}`),
		SnapshotHash:         "sha256:other-run-snapshot",
	})
	require.NoError(t, err)

	_, err = h.runs.queries.CompleteMixedRLRunWithSnapshot(h.ctx, db.CompleteMixedRLRunWithSnapshotParams{
		TerminalStatus: "completed", RunID: run.RunID,
		SnapshotID: otherSnapshot.SnapshotID, SnapshotHash: otherSnapshot.SnapshotHash,
	})
	require.Error(t, err)
}

func TestProviderCallLedger_FrozenRunRejectsGraphMutationButAcceptsLateAudit(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 1, "online_rl")
	turn := createMixedRLTurn(t, h, agent)
	call, err := h.ledger.InsertProviderCall(h.ctx, mixedRLProviderCallInput(run.RunID, agent, turn, "call-before-freeze", 1))
	require.NoError(t, err)
	_, err = h.ledger.InsertSegment(h.ctx, SegmentInput{
		SegmentID:         "message:before-freeze",
		RunID:             run.RunID,
		RunAgentID:        agent.RunAgentID,
		Kind:              "message",
		CanonicalActionID: "70000000-0000-4000-8000-000000000093",
		SegmentOrdinal:    1,
	})
	require.NoError(t, err)
	_, err = h.ledger.InsertSegment(h.ctx, SegmentInput{
		SegmentID:         "message:before-freeze-sibling",
		RunID:             run.RunID,
		RunAgentID:        agent.RunAgentID,
		Kind:              "message",
		CanonicalActionID: "70000000-0000-4000-8000-000000000095",
		SegmentOrdinal:    2,
	})
	require.NoError(t, err)
	require.NoError(t, h.ledger.AssociateProviderCall(h.ctx, SegmentCallAssociationInput{
		SegmentID: "message:before-freeze", ProviderCallID: call.CallID,
		CallOrdinal: 1, AssociationKind: "owned",
	}))
	advanceMixedRLRunToQuietCandidate(t, h, run.RunID)
	snapshot, _, err := h.ledger.FreezeAndComplete(h.ctx, FrozenSnapshotInput{
		SnapshotID:           testutil.MixedRLSnapshotID,
		RunID:                run.RunID,
		RunStatus:            "completed",
		SchemaVersion:        "1",
		NormalizationVersion: "1",
		CanonicalManifest:    []byte(`{"calls":["call-before-freeze"],"segments":["message:before-freeze","message:before-freeze-sibling"],"edges":[]}`),
		SnapshotHash:         "sha256:synthetic-mixed-rl-snapshot",
	})
	require.NoError(t, err)

	postFreezeCall := mixedRLProviderCallInput(run.RunID, agent, turn, "call-after-freeze", 2)
	expectMixedRLConstraintError(t, h, func() error {
		_, err := h.ledger.InsertProviderCall(h.ctx, postFreezeCall)
		return err
	})
	expectMixedRLConstraintError(t, h, func() error {
		_, err := h.ledger.InsertSegment(h.ctx, SegmentInput{
			SegmentID:         "message:after-freeze",
			RunID:             run.RunID,
			RunAgentID:        agent.RunAgentID,
			Kind:              "message",
			CanonicalActionID: "70000000-0000-4000-8000-000000000094",
			SegmentOrdinal:    2,
		})
		return err
	})
	expectMixedRLConstraintError(t, h, func() error {
		return h.ledger.AssociateProviderCall(h.ctx, SegmentCallAssociationInput{
			SegmentID: "message:before-freeze-sibling", ProviderCallID: call.CallID,
			CallOrdinal: 1, AssociationKind: "shared_producer",
		})
	})
	expectMixedRLConstraintError(t, h, func() error {
		_, err := h.ledger.InsertCausalEdge(h.ctx, CausalEdgeInput{
			EdgeID:               util.MustParseUUID("70000000-0000-4000-8000-0000000000b2"),
			RunID:                run.RunID,
			SourceSegmentID:      "message:before-freeze",
			DestinationSegmentID: "message:before-freeze-sibling",
			Type:                 "channel_message",
			TriggerMessageID:     util.MustParseUUID("70000000-0000-4000-8000-000000000093"),
			DestinationCallID:    call.CallID,
			EdgeOrdinal:          1,
		})
		return err
	})
	expectMixedRLConstraintError(t, h, func() error {
		_, err := h.tx.Exec(h.ctx, `UPDATE interaction_dag_run_segment SET reward = 99 WHERE run_id = $1`, run.RunID)
		return err
	})

	require.NoError(t, h.runs.RecordLateEvent(h.ctx, LateEventInput{
		EventID:    util.MustParseUUID("70000000-0000-4000-8000-0000000000b1"),
		RunID:      run.RunID,
		RunAgentID: agent.RunAgentID,
		TurnID:     turn.TurnID,
		Reason:     "capture_after_freeze",
		Summary:    []byte(`{"provider_call_count":1}`),
		SnapshotID: snapshot.SnapshotID,
	}))
}

func TestProviderCallLedger_FreezeAndCompleteRollsBackOnSnapshotConflict(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	_, err := h.runs.TransitionStatus(h.ctx, run.RunID, "provisioning", "preflight")
	require.NoError(t, err)
	_, err = h.runs.StartTimeout(h.ctx, run.RunID, time.Date(2026, time.August, 10, 4, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	_, err = h.runs.TransitionStatus(h.ctx, run.RunID, "running", "quiet_candidate")
	require.NoError(t, err)

	otherRunID := util.MustParseUUID("70000000-0000-4000-8000-000000000008")
	otherProjectID := util.MustParseUUID("70000000-0000-4000-8000-000000000009")
	createMixedRLRunWithIDs(t, h, otherRunID, otherProjectID)
	_, err = h.runs.TransitionStatus(h.ctx, otherRunID, "provisioning", "preflight")
	require.NoError(t, err)
	_, err = h.runs.StartTimeout(h.ctx, otherRunID, time.Date(2026, time.August, 10, 4, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	_, err = h.runs.TransitionStatus(h.ctx, otherRunID, "running", "quiet_candidate")
	require.NoError(t, err)

	input := FrozenSnapshotInput{
		SnapshotID:           "sha256:conflicting-snapshot",
		RunID:                otherRunID,
		RunStatus:            "completed",
		SchemaVersion:        "1",
		NormalizationVersion: "1",
		CanonicalManifest:    []byte(`{"calls":[],"segments":[],"edges":[]}`),
		SnapshotHash:         "sha256:conflicting-snapshot",
	}
	_, _, err = h.ledger.FreezeAndComplete(h.ctx, input)
	require.NoError(t, err)

	input.RunID = run.RunID
	_, _, err = h.ledger.FreezeAndComplete(h.ctx, input)
	require.Error(t, err)

	persisted, err := h.runs.queries.GetMixedRLRun(h.ctx, run.RunID)
	require.NoError(t, err)
	assert.Equal(t, "quiet_candidate", persisted.Status)
	assert.False(t, persisted.FrozenSnapshotID.Valid)
	_, err = h.ledger.GetFrozenSnapshot(h.ctx, run.RunID)
	require.ErrorIs(t, err, pgx.ErrNoRows)
}

func TestProviderCallLedger_RejectsAssociationOrdinalMismatch(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 1, "online_rl")
	turn := createMixedRLTurn(t, h, agent)
	call, err := h.ledger.InsertProviderCall(h.ctx, mixedRLProviderCallInput(
		mixedRLRunUUID, agent, turn, "call-association-ordinal", 1,
	))
	require.NoError(t, err)
	_, err = h.ledger.InsertSegment(h.ctx, SegmentInput{
		SegmentID:         "message:association-ordinal",
		RunID:             mixedRLRunUUID,
		RunAgentID:        agent.RunAgentID,
		Kind:              "message",
		CanonicalActionID: "70000000-0000-4000-8000-000000000096",
		SegmentOrdinal:    1,
	})
	require.NoError(t, err)

	err = h.ledger.AssociateProviderCall(h.ctx, SegmentCallAssociationInput{
		SegmentID: "message:association-ordinal", ProviderCallID: call.CallID,
		CallOrdinal: 99, AssociationKind: "owned",
	})
	require.Error(t, err)

	var associationCount int
	err = h.tx.QueryRow(h.ctx, `
		SELECT count(*)
		FROM interaction_dag_segment_provider_call
		WHERE segment_id = $1 AND provider_call_id = $2
	`, "message:association-ordinal", call.CallID).Scan(&associationCount)
	require.NoError(t, err)
	assert.Zero(t, associationCount)

	require.NoError(t, h.ledger.AssociateProviderCall(h.ctx, SegmentCallAssociationInput{
		SegmentID: "message:association-ordinal", ProviderCallID: call.CallID,
		CallOrdinal: call.CallOrdinal, AssociationKind: "owned",
	}))
	var canonicalOrdinal int64
	err = h.tx.QueryRow(h.ctx, `
		SELECT call_ordinal
		FROM interaction_dag_segment_provider_call
		WHERE segment_id = $1 AND provider_call_id = $2
	`, "message:association-ordinal", call.CallID).Scan(&canonicalOrdinal)
	require.NoError(t, err)
	assert.Equal(t, call.CallOrdinal, canonicalOrdinal)
}

func TestEnvDispatchRunStore_AuditEventsRequireValidTurnProvenance(t *testing.T) {
	t.Run("cross-run turn", func(t *testing.T) {
		h := newMixedRLRepositoryHarness(t)
		createMixedRLRun(t, h)
		agent := bindMixedRLAgent(t, h, 1, "online_rl")
		createMixedRLTurn(t, h, agent)

		otherRunID := util.MustParseUUID("70000000-0000-4000-8000-000000000010")
		otherProjectID := util.MustParseUUID("70000000-0000-4000-8000-000000000011")
		createMixedRLRunWithIDs(t, h, otherRunID, otherProjectID)
		otherAgent := bindMixedRLAgentForRun(t, h, otherRunID, 2, "offline_rl")
		otherTurn := createMixedRLTurnWithID(t, h, otherRunID, otherAgent, "70000000-0000-4000-8000-000000000083")

		expectMixedRLConstraintError(t, h, func() error {
			return h.runs.RecordCaptureGap(h.ctx, CaptureGapInput{
				EventID: util.MustParseUUID("70000000-0000-4000-8000-0000000000c1"),
				RunID:   mixedRLRunUUID, RunAgentID: agent.RunAgentID,
				TurnID: otherTurn.TurnID, Reason: "cross_run_turn",
				Summary: []byte(`{"scope":"synthetic"}`),
			})
		})
	})

	t.Run("missing turn", func(t *testing.T) {
		h := newMixedRLRepositoryHarness(t)
		createMixedRLRun(t, h)
		agent := bindMixedRLAgent(t, h, 1, "online_rl")
		expectMixedRLConstraintError(t, h, func() error {
			return h.runs.RecordCaptureGap(h.ctx, CaptureGapInput{
				EventID: util.MustParseUUID("70000000-0000-4000-8000-0000000000c2"),
				RunID:   mixedRLRunUUID, RunAgentID: agent.RunAgentID,
				TurnID: util.MustParseUUID("70000000-0000-4000-8000-000000000084"),
				Reason: "missing_turn", Summary: []byte(`{"scope":"synthetic"}`),
			})
		})
	})

	t.Run("turn without run agent", func(t *testing.T) {
		h := newMixedRLRepositoryHarness(t)
		createMixedRLRun(t, h)
		expectMixedRLConstraintError(t, h, func() error {
			_, err := h.tx.Exec(h.ctx, `
				INSERT INTO env_dispatch_run_audit_event (
					event_id, run_id, turn_id, kind, reason, summary
				) VALUES ($1, $2, $3, 'capture_gap', 'missing_run_agent', '{}')
			`, util.MustParseUUID("70000000-0000-4000-8000-0000000000c3"),
				mixedRLRunUUID, util.MustParseUUID("70000000-0000-4000-8000-000000000085"))
			return err
		})
	})
}

func TestEnvDispatchRunStore_LateEventRequiresFrozenSnapshot(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 1, "online_rl")
	turn := createMixedRLTurn(t, h, agent)

	expectMixedRLConstraintError(t, h, func() error {
		return h.runs.RecordLateEvent(h.ctx, LateEventInput{
			EventID: util.MustParseUUID("70000000-0000-4000-8000-0000000000c4"),
			RunID:   run.RunID, RunAgentID: agent.RunAgentID, TurnID: turn.TurnID,
			Reason: "late_without_snapshot", Summary: []byte(`{"scope":"synthetic"}`),
		})
	})
}

func TestEnvDispatchRunStore_RunningRequiresTimeoutOrigin(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	_, err := h.runs.TransitionStatus(h.ctx, run.RunID, "provisioning", "preflight")
	require.NoError(t, err)

	expectMixedRLConstraintError(t, h, func() error {
		_, err := h.tx.Exec(h.ctx, `
			UPDATE env_dispatch_run
			SET status = 'running'
			WHERE run_id = $1
		`, run.RunID)
		return err
	})

	submittedAt := time.Date(2026, time.August, 10, 3, 45, 0, 0, time.UTC)
	running, err := h.runs.StartTimeout(h.ctx, run.RunID, submittedAt)
	require.NoError(t, err)
	assert.Equal(t, "running", running.Status)
	assert.WithinDuration(t, submittedAt, running.InitialMessageSubmittedAt, time.Microsecond)
	assert.WithinDuration(t, submittedAt.Add(time.Duration(running.TotalTimeoutSeconds)*time.Second), running.TimeoutDeadlineAt, time.Microsecond)
}

func TestEnvDispatchRunStore_ProvisioningAndPreflightRejectTimeoutOrigin(t *testing.T) {
	for _, status := range []string{"provisioning", "preflight"} {
		t.Run(status, func(t *testing.T) {
			h := newMixedRLRepositoryHarness(t)
			run := createMixedRLRun(t, h)
			if status == "preflight" {
				_, err := h.runs.TransitionStatus(h.ctx, run.RunID, "provisioning", "preflight")
				require.NoError(t, err)
			}
			expectMixedRLConstraintError(t, h, func() error {
				_, err := h.tx.Exec(h.ctx, `
					UPDATE env_dispatch_run
					SET initial_message_submitted_at = $2::timestamptz,
					    timeout_deadline_at = $2::timestamptz + interval '30 seconds'
					WHERE run_id = $1
				`, run.RunID, time.Date(2026, time.August, 10, 3, 30, 0, 0, time.UTC))
				return err
			})
		})
	}
}

func TestProviderCallLedger_RejectsCredentialBearingRawRequests(t *testing.T) {
	cases := map[string]string{
		"authorization": `{"messages":[],"headers":{"Authorization":"synthetic-value"}}`,
		"api key":       `{"messages":[],"provider":{"API_Key":"synthetic-value"}}`,
		"nested secret": `{"messages":[],"transport":{"auth":{"client-secret":"synthetic-value"}}}`,
	}
	for name, rawRequest := range cases {
		t.Run(name, func(t *testing.T) {
			h := newMixedRLRepositoryHarness(t)
			createMixedRLRun(t, h)
			agent := bindMixedRLAgent(t, h, 1, "online_rl")
			turn := createMixedRLTurn(t, h, agent)
			input := mixedRLProviderCallInput(mixedRLRunUUID, agent, turn, "call-credential-bearing", 1)
			input.RawProviderRequest = []byte(rawRequest)

			_, err := h.ledger.InsertProviderCall(h.ctx, input)
			require.Error(t, err)

			var callCount int
			err = h.tx.QueryRow(h.ctx, `SELECT count(*) FROM pi_provider_call WHERE run_id = $1`, mixedRLRunUUID).Scan(&callCount)
			require.NoError(t, err)
			assert.Zero(t, callCount)
		})
	}
}

func TestEnvDispatchRunStore_RecordCaptureGapPreservesFrozenGapSet(t *testing.T) {
	t.Run("terminal arrival becomes snapshot-bound late event", func(t *testing.T) {
		h := newMixedRLRepositoryHarness(t)
		run := createMixedRLRun(t, h)
		agent := bindMixedRLAgent(t, h, 2, "offline_rl")
		turn := createMixedRLTurn(t, h, agent)

		require.NoError(t, h.runs.RecordCaptureGap(h.ctx, CaptureGapInput{
			EventID: util.MustParseUUID("70000000-0000-4000-8000-0000000000d1"),
			RunID:   run.RunID, RunAgentID: agent.RunAgentID, TurnID: turn.TurnID,
			Reason: "turn_batch_missing", Summary: []byte(`{"call_count":0}`),
		}))
		advanceMixedRLRunToQuietCandidate(t, h, run.RunID)
		snapshot, frozenRun, err := h.ledger.FreezeAndComplete(h.ctx, FrozenSnapshotInput{
			SnapshotID:           testutil.MixedRLSnapshotID,
			RunID:                run.RunID,
			RunStatus:            "completed",
			SchemaVersion:        "1",
			NormalizationVersion: "1",
			CanonicalManifest:    []byte(`{"calls":[],"segments":[],"edges":[]}`),
			SnapshotHash:         "sha256:synthetic-mixed-rl-snapshot",
		})
		require.NoError(t, err)
		require.Equal(t, int64(1), frozenRun.CaptureGapCount)

		require.NoError(t, h.runs.RecordCaptureGap(h.ctx, CaptureGapInput{
			EventID: util.MustParseUUID("70000000-0000-4000-8000-0000000000d2"),
			RunID:   run.RunID, RunAgentID: agent.RunAgentID, TurnID: turn.TurnID,
			Reason: "capture_after_freeze", Summary: []byte(`{"provider_call_count":1}`),
		}))

		persisted, err := h.runs.queries.GetMixedRLRun(h.ctx, run.RunID)
		require.NoError(t, err)
		assert.Equal(t, int64(1), persisted.CaptureGapCount)
		events, err := h.runs.ListAuditEvents(h.ctx, run.RunID)
		require.NoError(t, err)
		require.Len(t, events, 2)
		assert.Equal(t, "capture_gap", events[0].Kind)
		assert.Equal(t, "late_event", events[1].Kind)
		assert.Equal(t, snapshot.SnapshotID, events[1].SnapshotID)
	})

	t.Run("freezing arrival is rejected without mutation", func(t *testing.T) {
		h := newMixedRLRepositoryHarness(t)
		run := createMixedRLRun(t, h)
		agent := bindMixedRLAgent(t, h, 2, "offline_rl")
		turn := createMixedRLTurn(t, h, agent)
		require.NoError(t, h.runs.RecordCaptureGap(h.ctx, CaptureGapInput{
			EventID: util.MustParseUUID("70000000-0000-4000-8000-0000000000d3"),
			RunID:   run.RunID, RunAgentID: agent.RunAgentID, TurnID: turn.TurnID,
			Reason: "turn_batch_missing", Summary: []byte(`{"call_count":0}`),
		}))
		advanceMixedRLRunToFreezing(t, h, run.RunID)

		err := h.runs.RecordCaptureGap(h.ctx, CaptureGapInput{
			EventID: util.MustParseUUID("70000000-0000-4000-8000-0000000000d4"),
			RunID:   run.RunID, RunAgentID: agent.RunAgentID, TurnID: turn.TurnID,
			Reason: "capture_during_freeze", Summary: []byte(`{"provider_call_count":1}`),
		})
		require.Error(t, err)
		require.ErrorIs(t, err, ErrCaptureGapWindowClosed)

		persisted, getErr := h.runs.queries.GetMixedRLRun(h.ctx, run.RunID)
		require.NoError(t, getErr)
		assert.Equal(t, int64(1), persisted.CaptureGapCount)
		events, listErr := h.runs.ListAuditEvents(h.ctx, run.RunID)
		require.NoError(t, listErr)
		require.Len(t, events, 1)
		assert.Equal(t, "capture_gap", events[0].Kind)
	})
}

func TestEnvDispatchRunStore_RejectsOnlineActivationWithoutAReaLSession(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	fixture := testutil.MixedRLRunAgentFixture(1, "online_rl")
	for _, id := range []string{fixture.SourceAgentID, fixture.ExecutionAgentID} {
		_, err := h.tx.Exec(h.ctx, "INSERT INTO agent (id) VALUES ($1)", util.MustParseUUID(id))
		require.NoError(t, err)
	}
	_, err := h.tx.Exec(h.ctx, "INSERT INTO agent_runtime (id) VALUES ($1)", util.MustParseUUID(fixture.RuntimeID))
	require.NoError(t, err)
	_, err = h.runs.BindRunAgent(h.ctx, BindEnvDispatchRunAgentInput{
		RunID: run.RunID, SourceAgentID: util.MustParseUUID(fixture.SourceAgentID),
		ExecutionAgentID: util.MustParseUUID(fixture.ExecutionAgentID),
		RuntimeID:        fixtureUUID(fixture.RuntimeID), PiSessionID: fixture.PiSessionID,
		TrainingMode: "online_rl", CaptureBoundary: "boundary-missing-areal",
	})
	require.NoError(t, err, "setup may bind the run-agent before online routing is complete")
	_, err = h.runs.TransitionStatus(h.ctx, run.RunID, "provisioning", "preflight")
	require.NoError(t, err)

	expectMixedRLConstraintError(t, h, func() error {
		_, err = h.runs.StartTimeout(h.ctx, run.RunID, time.Date(2026, time.August, 10, 5, 0, 0, 0, time.UTC))
		return err
	})
	persisted, getErr := h.runs.queries.GetMixedRLRun(h.ctx, run.RunID)
	require.NoError(t, getErr)
	assert.Equal(t, "preflight", persisted.Status)
}

func fixtureUUID(value string) pgtype.UUID {
	return util.MustParseUUID(value)
}

func TestProviderCallLedger_CaptureBatchBoundaryMustMatchRunAgent(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 2, "offline_rl")
	turn := createMixedRLTurn(t, h, agent)

	_, err := h.ledger.InsertCaptureBatch(h.ctx, TurnCaptureBatchInput{
		CaptureBatchID: util.MustParseUUID("70000000-0000-4000-8000-0000000000e1"),
		TurnID:         turn.TurnID, CaptureBoundary: "wrong-boundary",
		PayloadHash: "sha256:mismatched-boundary",
	})
	require.Error(t, err)

	var count int
	require.NoError(t, h.tx.QueryRow(h.ctx, `SELECT count(*) FROM env_dispatch_turn_capture_batch WHERE turn_id = $1`, turn.TurnID).Scan(&count))
	assert.Zero(t, count)

	accepted, err := h.ledger.InsertCaptureBatch(h.ctx, TurnCaptureBatchInput{
		CaptureBatchID: util.MustParseUUID("70000000-0000-4000-8000-0000000000e2"),
		TurnID:         turn.TurnID, CaptureBoundary: agent.CaptureBoundary,
		PayloadHash: "sha256:matching-boundary",
	})
	require.NoError(t, err)
	assert.Equal(t, agent.CaptureBoundary, accepted.CaptureBoundary)
}

func TestProviderCallLedger_SharedProducerRequiresSameRunOwner(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 2, "offline_rl")
	turn := createMixedRLTurn(t, h, agent)
	call, err := h.ledger.InsertProviderCall(h.ctx, mixedRLProviderCallInput(
		mixedRLRunUUID, agent, turn, "call-shared-owner", 1,
	))
	require.NoError(t, err)
	for ordinal, segmentID := range []string{"message:shared-owner", "message:shared-sibling"} {
		_, err = h.ledger.InsertSegment(h.ctx, SegmentInput{
			SegmentID: segmentID, RunID: mixedRLRunUUID, RunAgentID: agent.RunAgentID,
			Kind: "message", CanonicalActionID: fmt.Sprintf("70000000-0000-4000-8000-0000000001%02d", ordinal),
			SegmentOrdinal: int64(ordinal + 1),
		})
		require.NoError(t, err)
	}

	err = h.ledger.AssociateProviderCall(h.ctx, SegmentCallAssociationInput{
		SegmentID: "message:shared-sibling", ProviderCallID: call.CallID,
		CallOrdinal: call.CallOrdinal, AssociationKind: "shared_producer",
	})
	require.Error(t, err)

	require.NoError(t, h.ledger.AssociateProviderCall(h.ctx, SegmentCallAssociationInput{
		SegmentID: "message:shared-owner", ProviderCallID: call.CallID,
		CallOrdinal: call.CallOrdinal, AssociationKind: "owned",
	}))
	require.NoError(t, h.ledger.AssociateProviderCall(h.ctx, SegmentCallAssociationInput{
		SegmentID: "message:shared-sibling", ProviderCallID: call.CallID,
		CallOrdinal: call.CallOrdinal, AssociationKind: "shared_producer",
	}))
}

func TestProviderCallLedger_ConsumptionEffectiveCallMustFollowSameAgentConsumption(t *testing.T) {
	t.Run("cross-agent effective call", func(t *testing.T) {
		h := newMixedRLRepositoryHarness(t)
		createMixedRLRun(t, h)
		agentOne := bindMixedRLAgent(t, h, 1, "online_rl")
		turnOne := createMixedRLTurnWithID(t, h, mixedRLRunUUID, agentOne, "70000000-0000-4000-8000-000000000180")
		agentTwo := bindMixedRLAgentForRun(t, h, mixedRLRunUUID, 2, "offline_rl")
		turnTwo := createMixedRLTurnWithID(t, h, mixedRLRunUUID, agentTwo, "70000000-0000-4000-8000-000000000181")
		callTwo, err := h.ledger.InsertProviderCall(h.ctx, mixedRLProviderCallInput(
			mixedRLRunUUID, agentTwo, turnTwo, "call-consumption-other-agent", 1,
		))
		require.NoError(t, err)
		messageID := util.MustParseUUID("70000000-0000-4000-8000-000000000182")
		_, err = h.tx.Exec(h.ctx, "INSERT INTO channel_message (id) VALUES ($1)", messageID)
		require.NoError(t, err)

		expectMixedRLConstraintError(t, h, func() error {
			_, err := h.ledger.InsertMessageConsumption(h.ctx, MessageConsumptionInput{
				ConsumptionID: util.MustParseUUID("70000000-0000-4000-8000-000000000183"),
				RunID:         mixedRLRunUUID, RunAgentID: agentOne.RunAgentID, TurnID: turnOne.TurnID,
				ChannelMessageID: messageID, Source: "message_check",
				EffectiveFromCallID: callTwo.CallID,
				ConsumedAt:          time.Date(2026, time.August, 10, 3, 0, 0, 0, time.UTC),
			})
			return err
		})
	})

	t.Run("effective call started before consumption", func(t *testing.T) {
		h := newMixedRLRepositoryHarness(t)
		createMixedRLRun(t, h)
		agent := bindMixedRLAgent(t, h, 2, "offline_rl")
		turn := createMixedRLTurn(t, h, agent)
		call, err := h.ledger.InsertProviderCall(h.ctx, mixedRLProviderCallInput(
			mixedRLRunUUID, agent, turn, "call-consumption-too-early", 1,
		))
		require.NoError(t, err)
		messageID := util.MustParseUUID("70000000-0000-4000-8000-000000000184")
		_, err = h.tx.Exec(h.ctx, "INSERT INTO channel_message (id) VALUES ($1)", messageID)
		require.NoError(t, err)

		_, err = h.ledger.InsertMessageConsumption(h.ctx, MessageConsumptionInput{
			ConsumptionID: util.MustParseUUID("70000000-0000-4000-8000-000000000185"),
			RunID:         mixedRLRunUUID, RunAgentID: agent.RunAgentID, TurnID: turn.TurnID,
			ChannelMessageID: messageID, Source: "message_check",
			EffectiveFromCallID: call.CallID, ConsumedAt: call.StartedAt.Add(time.Second),
		})
		require.Error(t, err)

		equalMessageID := util.MustParseUUID("70000000-0000-4000-8000-000000000188")
		_, err = h.tx.Exec(h.ctx, "INSERT INTO channel_message (id) VALUES ($1)", equalMessageID)
		require.NoError(t, err)
		_, err = h.ledger.InsertMessageConsumption(h.ctx, MessageConsumptionInput{
			ConsumptionID: util.MustParseUUID("70000000-0000-4000-8000-000000000189"),
			RunID:         mixedRLRunUUID, RunAgentID: agent.RunAgentID, TurnID: turn.TurnID,
			ChannelMessageID: equalMessageID, Source: "message_check",
			EffectiveFromCallID: call.CallID, ConsumedAt: call.StartedAt,
		})
		require.Error(t, err)

		validMessageID := util.MustParseUUID("70000000-0000-4000-8000-000000000186")
		_, err = h.tx.Exec(h.ctx, "INSERT INTO channel_message (id) VALUES ($1)", validMessageID)
		require.NoError(t, err)
		_, err = h.ledger.InsertMessageConsumption(h.ctx, MessageConsumptionInput{
			ConsumptionID: util.MustParseUUID("70000000-0000-4000-8000-000000000187"),
			RunID:         mixedRLRunUUID, RunAgentID: agent.RunAgentID, TurnID: turn.TurnID,
			ChannelMessageID: validMessageID, Source: "accept_message_batch",
			EffectiveFromCallID: call.CallID, ConsumedAt: call.StartedAt.Add(-time.Second),
		})
		require.NoError(t, err)
	})
}

func TestProviderCallLedger_FreezeRevalidatesCrossEntityInvariants(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 2, "offline_rl")
	turn := createMixedRLTurn(t, h, agent)
	call, err := h.ledger.InsertProviderCall(h.ctx, mixedRLProviderCallInput(
		run.RunID, agent, turn, "call-freeze-invalid-shared", 1,
	))
	require.NoError(t, err)
	_, err = h.ledger.InsertSegment(h.ctx, SegmentInput{
		SegmentID: "message:freeze-invalid-shared", RunID: run.RunID,
		RunAgentID: agent.RunAgentID, Kind: "message",
		CanonicalActionID: "70000000-0000-4000-8000-000000000190", SegmentOrdinal: 1,
	})
	require.NoError(t, err)
	_, err = h.tx.Exec(h.ctx, `
		INSERT INTO interaction_dag_segment_provider_call (
			segment_id, provider_call_id, run_id, run_agent_id,
			call_ordinal, association_kind
		) VALUES ($1, $2, $3, $4, $5, 'shared_producer')
	`, "message:freeze-invalid-shared", call.CallID, run.RunID, agent.RunAgentID, call.CallOrdinal)
	require.NoError(t, err, "deferred constraint permits assembling related rows in one transaction")
	advanceMixedRLRunToQuietCandidate(t, h, run.RunID)

	_, _, err = h.ledger.FreezeAndComplete(h.ctx, FrozenSnapshotInput{
		SnapshotID: "sha256:must-not-publish", RunID: run.RunID, RunStatus: "completed",
		SchemaVersion: "1", NormalizationVersion: "1",
		CanonicalManifest: []byte(`{"calls":["call-freeze-invalid-shared"],"segments":["message:freeze-invalid-shared"],"edges":[]}`),
		SnapshotHash:      "sha256:must-not-publish",
	})
	require.Error(t, err)
	persisted, getErr := h.runs.queries.GetMixedRLRun(h.ctx, run.RunID)
	require.NoError(t, getErr)
	assert.Equal(t, "quiet_candidate", persisted.Status)
	_, getErr = h.ledger.GetFrozenSnapshot(h.ctx, run.RunID)
	require.ErrorIs(t, getErr, pgx.ErrNoRows)
}

func TestProviderCallLedger_GetFrozenDAGIsOwnedCanonicalSanitizedAndStable(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 2, "offline_rl")
	turn := createMixedRLTurn(t, h, agent)
	first, err := h.ledger.InsertProviderCall(h.ctx, mixedRLProviderCallInput(
		run.RunID, agent, turn, "call-frozen-read-1", 1,
	))
	require.NoError(t, err)
	second, err := h.ledger.InsertProviderCall(h.ctx, mixedRLProviderCallInput(
		run.RunID, agent, turn, "call-frozen-read-2", 2,
	))
	require.NoError(t, err)
	for _, input := range []SegmentInput{
		{SegmentID: "message:frozen-read-2", RunID: run.RunID, RunAgentID: agent.RunAgentID, Kind: "message", CanonicalActionID: "70000000-0000-4000-8000-000000000192", SegmentOrdinal: 2},
		{SegmentID: "message:frozen-read-1", RunID: run.RunID, RunAgentID: agent.RunAgentID, Kind: "message", CanonicalActionID: "70000000-0000-4000-8000-000000000191", SegmentOrdinal: 1},
	} {
		_, err = h.ledger.InsertSegment(h.ctx, input)
		require.NoError(t, err)
	}
	require.NoError(t, h.ledger.AssociateProviderCall(h.ctx, SegmentCallAssociationInput{
		SegmentID: "message:frozen-read-1", ProviderCallID: first.CallID,
		CallOrdinal: first.CallOrdinal, AssociationKind: "owned",
	}))
	require.NoError(t, h.ledger.AssociateProviderCall(h.ctx, SegmentCallAssociationInput{
		SegmentID: "message:frozen-read-2", ProviderCallID: second.CallID,
		CallOrdinal: second.CallOrdinal, AssociationKind: "owned",
	}))
	_, err = h.ledger.InsertCausalEdge(h.ctx, CausalEdgeInput{
		EdgeID: util.MustParseUUID("70000000-0000-4000-8000-000000000193"),
		RunID:  run.RunID, SourceSegmentID: "message:frozen-read-1",
		DestinationSegmentID: "message:frozen-read-2", Type: "channel_message",
		TriggerMessageID:  util.MustParseUUID("70000000-0000-4000-8000-000000000191"),
		DestinationCallID: second.CallID, EdgeOrdinal: 1,
	})
	require.NoError(t, err)
	require.NoError(t, h.runs.RecordCaptureGap(h.ctx, CaptureGapInput{
		EventID: util.MustParseUUID("70000000-0000-4000-8000-000000000194"),
		RunID:   run.RunID, RunAgentID: agent.RunAgentID, TurnID: turn.TurnID,
		Reason: "turn_batch_missing", Summary: []byte(`{"private_detail":"must-not-be-returned"}`),
	}))
	advanceMixedRLRunToQuietCandidate(t, h, run.RunID)
	snapshot, _, err := h.ledger.FreezeAndComplete(h.ctx, FrozenSnapshotInput{
		SnapshotID: testutil.MixedRLSnapshotID, RunID: run.RunID, RunStatus: "completed",
		SchemaVersion: "1", NormalizationVersion: "1",
		CanonicalManifest: []byte(`{"calls":["call-frozen-read-1","call-frozen-read-2"],"segments":["message:frozen-read-1","message:frozen-read-2"],"edges":["70000000-0000-4000-8000-000000000193"]}`),
		SnapshotHash:      "sha256:synthetic-mixed-rl-snapshot",
	})
	require.NoError(t, err)

	got, err := h.ledger.GetFrozenDAG(h.ctx, run.RunID, snapshot.SnapshotID)
	require.NoError(t, err)
	assert.Equal(t, "completed", got.Run.Status)
	assert.Equal(t, snapshot.SnapshotID, got.Snapshot.SnapshotID)
	require.Len(t, got.RunAgents, 1)
	require.Len(t, got.ProviderCalls, 2)
	assert.Equal(t, []string{first.CallID, second.CallID}, []string{got.ProviderCalls[0].CallID, got.ProviderCalls[1].CallID})
	require.Len(t, got.Segments, 2)
	assert.Equal(t, []string{"message:frozen-read-1", "message:frozen-read-2"}, []string{got.Segments[0].SegmentID, got.Segments[1].SegmentID})
	require.Len(t, got.Associations, 2)
	require.Len(t, got.Edges, 1)
	require.Len(t, got.CaptureGaps, 1)
	assert.Equal(t, "turn_batch_missing", got.CaptureGaps[0].Reason)

	encoded, err := json.Marshal(got)
	require.NoError(t, err)
	for _, forbidden := range []string{"raw_provider_request", "final_assistant_message", "normalized_trajectory", "private_detail", "must-not-be-returned"} {
		assert.NotContains(t, string(encoded), forbidden)
	}

	require.NoError(t, h.runs.RecordCaptureGap(h.ctx, CaptureGapInput{
		EventID: util.MustParseUUID("70000000-0000-4000-8000-000000000195"),
		RunID:   run.RunID, RunAgentID: agent.RunAgentID, TurnID: turn.TurnID,
		Reason: "capture_after_freeze", Summary: []byte(`{"private_detail":"late"}`),
	}))
	afterLate, err := h.ledger.GetFrozenDAG(h.ctx, run.RunID, snapshot.SnapshotID)
	require.NoError(t, err)
	assert.Equal(t, got, afterLate)

	otherRunID := util.MustParseUUID("70000000-0000-4000-8000-000000000196")
	otherProjectID := util.MustParseUUID("70000000-0000-4000-8000-000000000197")
	createMixedRLRunWithIDs(t, h, otherRunID, otherProjectID)
	advanceMixedRLRunToQuietCandidate(t, h, otherRunID)
	otherSnapshot, _, err := h.ledger.FreezeAndComplete(h.ctx, FrozenSnapshotInput{
		SnapshotID: "sha256:other-frozen-read", RunID: otherRunID, RunStatus: "completed",
		SchemaVersion: "1", NormalizationVersion: "1",
		CanonicalManifest: []byte(`{"calls":[],"segments":[],"edges":[]}`),
		SnapshotHash:      "sha256:other-frozen-read",
	})
	require.NoError(t, err)
	_, err = h.ledger.GetFrozenDAG(h.ctx, run.RunID, otherSnapshot.SnapshotID)
	require.Error(t, err)
	_, err = h.ledger.GetFrozenDAG(h.ctx, otherRunID, snapshot.SnapshotID)
	require.Error(t, err)
}

func TestProviderCallLedger_FailedTimeoutSettlesOutstandingCaptureActivity(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	advanceMixedRLRunToRunning(t, h, run.RunID)
	_, err := h.runs.AdjustActivity(h.ctx, run.RunID, ActivityCounterDelta{
		ActiveTurns: 1, InflightTools: 1, UnfinishedCapture: 1,
	})
	require.NoError(t, err)

	_, terminal, err := h.ledger.FreezeAndComplete(h.ctx, FrozenSnapshotInput{
		SnapshotID: "sha256:unsafe-timeout", RunID: run.RunID, RunStatus: "failed_timeout",
		SchemaVersion: "1", NormalizationVersion: "1",
		CanonicalManifest: []byte(`{"calls":[],"segments":[],"edges":[]}`),
		SnapshotHash:      "sha256:unsafe-timeout",
	})
	require.NoError(t, err)
	assert.Equal(t, "failed_timeout", terminal.Status)
	assert.Zero(t, terminal.ActiveTurnCount)
	assert.Zero(t, terminal.InflightToolCount)
	assert.Zero(t, terminal.UnfinishedCaptureBatchCount)
}

func TestProviderCallLedger_FailedTimeoutFrozenDAGIsReadable(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	advanceMixedRLRunToRunning(t, h, run.RunID)
	snapshot, terminal, err := h.ledger.FreezeAndComplete(h.ctx, FrozenSnapshotInput{
		SnapshotID: "sha256:safe-timeout", RunID: run.RunID, RunStatus: "failed_timeout",
		SchemaVersion: "1", NormalizationVersion: "1",
		CanonicalManifest: []byte(`{"calls":[],"segments":[],"edges":[]}`),
		SnapshotHash:      "sha256:safe-timeout",
	})
	require.NoError(t, err)
	assert.Equal(t, "failed_timeout", terminal.Status)
	got, err := h.ledger.GetFrozenDAG(h.ctx, run.RunID, snapshot.SnapshotID)
	require.NoError(t, err)
	assert.Equal(t, "failed_timeout", got.Run.Status)
}

func TestProviderCallLedger_RejectsDuplicateTerminalSegmentsPerRunAgent(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 2, "offline_rl")
	_, err := h.ledger.InsertSegment(h.ctx, SegmentInput{
		SegmentID: "terminal:first", RunID: mixedRLRunUUID, RunAgentID: agent.RunAgentID,
		Kind: "terminal", SegmentOrdinal: 1,
	})
	require.NoError(t, err)
	expectMixedRLConstraintError(t, h, func() error {
		_, err := h.ledger.InsertSegment(h.ctx, SegmentInput{
			SegmentID: "terminal:second", RunID: mixedRLRunUUID, RunAgentID: agent.RunAgentID,
			Kind: "terminal", SegmentOrdinal: 2,
		})
		return err
	})
}

func TestProviderCallLedger_FreezeRejectsSettledTurnWithoutBatchOrGap(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 2, "offline_rl")
	turn := createMixedRLTurn(t, h, agent)
	_, err := h.runs.CompleteResidentTurn(h.ctx, turn.TurnID, "settled", time.Date(2026, time.August, 10, 6, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	advanceMixedRLRunToQuietCandidate(t, h, run.RunID)

	_, _, err = h.ledger.FreezeAndComplete(h.ctx, FrozenSnapshotInput{
		SnapshotID: "sha256:missing-turn-coverage", RunID: run.RunID, RunStatus: "completed",
		SchemaVersion: "1", NormalizationVersion: "1",
		CanonicalManifest: []byte(`{"calls":[],"segments":[],"edges":[]}`),
		SnapshotHash:      "sha256:missing-turn-coverage",
	})
	require.Error(t, err)
}

func TestEnvDispatchRunStore_RecordCaptureGapDistinguishesMissingRun(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	err := h.runs.RecordCaptureGap(h.ctx, CaptureGapInput{
		EventID: util.MustParseUUID("70000000-0000-4000-8000-000000000198"),
		RunID:   util.MustParseUUID("70000000-0000-4000-8000-000000000199"),
		Reason:  "missing_run", Summary: []byte(`{"scope":"synthetic"}`),
	})
	require.ErrorIs(t, err, ErrMixedRLRunNotFound)
}

func TestProviderCallLedger_FrozenDAGRejectsRunAgentMutationAndRemainsStable(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 2, "offline_rl")
	advanceMixedRLRunToQuietCandidate(t, h, run.RunID)
	snapshot, _, err := h.ledger.FreezeAndComplete(h.ctx, FrozenSnapshotInput{
		SnapshotID: testutil.MixedRLSnapshotID, RunID: run.RunID, RunStatus: "completed",
		SchemaVersion: "1", NormalizationVersion: "1",
		CanonicalManifest: []byte(`{"calls":[],"segments":[],"edges":[]}`),
		SnapshotHash:      "sha256:synthetic-mixed-rl-snapshot",
	})
	require.NoError(t, err)

	before, err := h.ledger.GetFrozenDAG(h.ctx, run.RunID, snapshot.SnapshotID)
	require.NoError(t, err)
	require.Len(t, before.RunAgents, 1)
	require.Equal(t, agent.PiSessionID, before.RunAgents[0].PiSessionID)

	expectMixedRLConstraintError(t, h, func() error {
		_, err := h.tx.Exec(h.ctx, `
			UPDATE env_dispatch_run_agent
			SET pi_session_id = 'mutated-after-freeze'
			WHERE run_id = $1 AND run_agent_id = $2
		`, run.RunID, agent.RunAgentID)
		return err
	})

	after, err := h.ledger.GetFrozenDAG(h.ctx, run.RunID, snapshot.SnapshotID)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestProviderCallLedger_FrozenGraphRejectsReparentingToProvisioningRun(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	frozenRun := createMixedRLRun(t, h)
	frozenAgent := bindMixedRLAgent(t, h, 2, "offline_rl")
	_, err := h.ledger.InsertSegment(h.ctx, SegmentInput{
		SegmentID: "message:reparent-source", RunID: frozenRun.RunID,
		RunAgentID: frozenAgent.RunAgentID, Kind: "message",
		CanonicalActionID: "70000000-0000-4000-8000-00000000019a", SegmentOrdinal: 1,
	})
	require.NoError(t, err)
	advanceMixedRLRunToQuietCandidate(t, h, frozenRun.RunID)
	snapshot, _, err := h.ledger.FreezeAndComplete(h.ctx, FrozenSnapshotInput{
		SnapshotID: "sha256:reparent-source", RunID: frozenRun.RunID, RunStatus: "completed",
		SchemaVersion: "1", NormalizationVersion: "1",
		CanonicalManifest: []byte(`{"calls":[],"segments":["message:reparent-source"],"edges":[]}`),
		SnapshotHash:      "sha256:reparent-source",
	})
	require.NoError(t, err)
	before, err := h.ledger.GetFrozenDAG(h.ctx, frozenRun.RunID, snapshot.SnapshotID)
	require.NoError(t, err)

	targetRunID := util.MustParseUUID("70000000-0000-4000-8000-00000000019b")
	targetProjectID := util.MustParseUUID("70000000-0000-4000-8000-00000000019c")
	createMixedRLRunWithIDs(t, h, targetRunID, targetProjectID)
	targetAgent := bindMixedRLAgentForRun(t, h, targetRunID, 3, "none")

	expectMixedRLConstraintError(t, h, func() error {
		_, updateErr := h.tx.Exec(h.ctx, `
			UPDATE interaction_dag_run_segment
			SET run_id = $1, run_agent_id = $2, snapshot_id = NULL
			WHERE run_id = $3 AND segment_id = 'message:reparent-source'
		`, targetRunID, targetAgent.RunAgentID, frozenRun.RunID)
		return updateErr
	})

	var sourceCount, targetCount int
	require.NoError(t, h.tx.QueryRow(h.ctx, `
		SELECT count(*) FROM interaction_dag_run_segment
		WHERE run_id = $1 AND segment_id = 'message:reparent-source'
	`, frozenRun.RunID).Scan(&sourceCount))
	require.NoError(t, h.tx.QueryRow(h.ctx, `
		SELECT count(*) FROM interaction_dag_run_segment
		WHERE run_id = $1 AND segment_id = 'message:reparent-source'
	`, targetRunID).Scan(&targetCount))
	assert.Equal(t, 1, sourceCount)
	assert.Zero(t, targetCount)
	after, err := h.ledger.GetFrozenDAG(h.ctx, frozenRun.RunID, snapshot.SnapshotID)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestEnvDispatchRunStore_TerminalSnapshotFacingMetadataIsImmutable(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	require.NoError(t, h.runs.RecordCaptureGap(h.ctx, CaptureGapInput{
		EventID: util.MustParseUUID("70000000-0000-4000-8000-00000000019d"),
		RunID:   run.RunID, Reason: "turn_batch_missing", Summary: []byte(`{"call_count":0}`),
	}))
	advanceMixedRLRunToQuietCandidate(t, h, run.RunID)
	_, terminal, err := h.ledger.FreezeAndComplete(h.ctx, FrozenSnapshotInput{
		SnapshotID: "sha256:immutable-run-metadata", RunID: run.RunID, RunStatus: "completed",
		SchemaVersion: "1", NormalizationVersion: "1",
		CanonicalManifest: []byte(`{"calls":[],"segments":[],"edges":[]}`),
		SnapshotHash:      "sha256:immutable-run-metadata",
	})
	require.NoError(t, err)

	alternateProjectID := util.MustParseUUID("70000000-0000-4000-8000-00000000019e")
	alternateWorkspaceID := util.MustParseUUID("70000000-0000-4000-8000-00000000019f")
	_, err = h.tx.Exec(h.ctx, "INSERT INTO project (id) VALUES ($1)", alternateProjectID)
	require.NoError(t, err)
	_, err = h.tx.Exec(h.ctx, "INSERT INTO workspace (id) VALUES ($1)", alternateWorkspaceID)
	require.NoError(t, err)

	for name, statement := range map[string]string{
		"snapshot hash":      `UPDATE env_dispatch_run SET snapshot_hash = 'sha256:tampered' WHERE run_id = $1`,
		"snapshot identity":  `UPDATE env_dispatch_run SET frozen_snapshot_id = 'sha256:tampered' WHERE run_id = $1`,
		"freeze time":        `UPDATE env_dispatch_run SET frozen_at = frozen_at + interval '1 second' WHERE run_id = $1`,
		"capture gap count":  `UPDATE env_dispatch_run SET capture_gap_count = capture_gap_count + 1 WHERE run_id = $1`,
		"project identity":   `UPDATE env_dispatch_run SET project_id = '70000000-0000-4000-8000-00000000019e' WHERE run_id = $1`,
		"workspace identity": `UPDATE env_dispatch_run SET workspace_id = '70000000-0000-4000-8000-00000000019f' WHERE run_id = $1`,
		"timeout origin":     `UPDATE env_dispatch_run SET initial_message_submitted_at = initial_message_submitted_at + interval '1 second' WHERE run_id = $1`,
		"timeout deadline":   `UPDATE env_dispatch_run SET timeout_deadline_at = timeout_deadline_at + interval '1 second' WHERE run_id = $1`,
	} {
		t.Run(name, func(t *testing.T) {
			expectMixedRLConstraintError(t, h, func() error {
				_, updateErr := h.tx.Exec(h.ctx, statement, run.RunID)
				return updateErr
			})
		})
	}

	persisted, err := h.runs.queries.GetMixedRLRun(h.ctx, run.RunID)
	require.NoError(t, err)
	assert.Equal(t, terminal.ProjectID, persisted.ProjectID)
	assert.Equal(t, terminal.WorkspaceID, persisted.WorkspaceID)
	assert.Equal(t, terminal.FrozenSnapshotID, mixedRLTextValue(persisted.FrozenSnapshotID))
	assert.Equal(t, terminal.SnapshotHash, mixedRLTextValue(persisted.SnapshotHash))
	assert.Equal(t, terminal.FrozenAt, timeValue(persisted.FrozenAt))
	assert.Equal(t, terminal.CaptureGapCount, persisted.CaptureGapCount)
}

func TestProviderCallLedger_GetFrozenDAGRejectsRunSnapshotMetadataMismatch(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	advanceMixedRLRunToFreezing(t, h, run.RunID)
	snapshot, err := h.runs.queries.CreateMixedRLFrozenSnapshot(h.ctx, db.CreateMixedRLFrozenSnapshotParams{
		SnapshotID: "sha256:metadata-source", RunID: run.RunID, RunStatus: "completed",
		SchemaVersion: "1", NormalizationVersion: "1",
		SegmentCount: 0, CallCount: 0, EdgeCount: 0,
		CanonicalManifest: []byte(`{"calls":[],"segments":[],"edges":[]}`),
		SnapshotHash:      "sha256:metadata-source",
	})
	require.NoError(t, err)
	_, err = h.tx.Exec(h.ctx, `
		UPDATE env_dispatch_run
		SET status = 'completed', frozen_snapshot_id = $2,
		    snapshot_hash = 'sha256:metadata-tampered', frozen_at = $3
		WHERE run_id = $1
	`, run.RunID, snapshot.SnapshotID, snapshot.CreatedAt)
	require.NoError(t, err, "setup creates a terminal row whose metadata disagrees with its owned snapshot")

	_, err = h.ledger.GetFrozenDAG(h.ctx, run.RunID, snapshot.SnapshotID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "metadata mismatch")
}

func TestEnvDispatchRunStore_RejectsModeSpecificAReALIdentity(t *testing.T) {
	t.Run("none run-agent with AReaL session", func(t *testing.T) {
		h := newMixedRLRepositoryHarness(t)
		run := createMixedRLRun(t, h)
		fixture := testutil.MixedRLRunAgentFixture(3, "none")
		for _, id := range []string{fixture.SourceAgentID, fixture.ExecutionAgentID} {
			_, err := h.tx.Exec(h.ctx, "INSERT INTO agent (id) VALUES ($1)", util.MustParseUUID(id))
			require.NoError(t, err)
		}
		_, err := h.tx.Exec(h.ctx, "INSERT INTO agent_runtime (id) VALUES ($1)", util.MustParseUUID(fixture.RuntimeID))
		require.NoError(t, err)

		_, err = h.runs.BindRunAgent(h.ctx, BindEnvDispatchRunAgentInput{
			RunID: run.RunID, SourceAgentID: util.MustParseUUID(fixture.SourceAgentID),
			ExecutionAgentID: util.MustParseUUID(fixture.ExecutionAgentID),
			RuntimeID:        fixtureUUID(fixture.RuntimeID), PiSessionID: fixture.PiSessionID,
			TrainingMode: "none", AReALSessionID: "areal-session-must-be-absent",
			CaptureBoundary: "boundary-none",
		})
		require.Error(t, err)
	})

	t.Run("online call without AReaL pair", func(t *testing.T) {
		h := newMixedRLRepositoryHarness(t)
		createMixedRLRun(t, h)
		agent := bindMixedRLAgent(t, h, 1, "online_rl")
		turn := createMixedRLTurn(t, h, agent)
		input := mixedRLProviderCallInput(mixedRLRunUUID, agent, turn, "call-online-without-areal", 1)
		input.AReALSessionID = ""
		input.AReALCallID = ""

		_, err := h.ledger.InsertProviderCall(h.ctx, input)
		require.Error(t, err)
		persisted, getErr := h.runs.queries.GetMixedRLRunAgent(h.ctx, db.GetMixedRLRunAgentParams{
			RunID: mixedRLRunUUID, RunAgentID: agent.RunAgentID,
		})
		require.NoError(t, getErr)
		assert.Equal(t, int64(1), persisted.NextCallOrdinal)
	})

	t.Run("none call with AReaL pair", func(t *testing.T) {
		h := newMixedRLRepositoryHarness(t)
		createMixedRLRun(t, h)
		agent := bindMixedRLAgent(t, h, 3, "none")
		turn := createMixedRLTurn(t, h, agent)
		input := mixedRLProviderCallInput(mixedRLRunUUID, agent, turn, "call-none-with-areal", 1)
		input.AReALSessionID = "areal-session-must-be-absent"
		input.AReALCallID = "areal-call-must-be-absent"

		_, err := h.ledger.InsertProviderCall(h.ctx, input)
		require.Error(t, err)
		persisted, getErr := h.runs.queries.GetMixedRLRunAgent(h.ctx, db.GetMixedRLRunAgentParams{
			RunID: mixedRLRunUUID, RunAgentID: agent.RunAgentID,
		})
		require.NoError(t, getErr)
		assert.Equal(t, int64(1), persisted.NextCallOrdinal)
	})
}

func TestProviderCallLedger_GetFrozenDAGOmitCanonicalManifest(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	advanceMixedRLRunToQuietCandidate(t, h, run.RunID)
	manifest := []byte(`{"calls":[],"segments":[],"edges":[],"authorization":"credential-sentinel"}`)
	snapshot, _, err := h.ledger.FreezeAndComplete(h.ctx, FrozenSnapshotInput{
		SnapshotID: "sha256:sanitized-read", RunID: run.RunID, RunStatus: "completed",
		SchemaVersion: "1", NormalizationVersion: "1",
		CanonicalManifest: manifest, SnapshotHash: "sha256:sanitized-read",
	})
	require.NoError(t, err)

	got, err := h.ledger.GetFrozenDAG(h.ctx, run.RunID, snapshot.SnapshotID)
	require.NoError(t, err)
	encoded, err := json.Marshal(got)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "CanonicalManifest")
	assert.NotContains(t, string(encoded), "credential-sentinel")
}
