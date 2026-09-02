// SPDX-License-Identifier: Apache-2.0

package service

// Migration 496 retention behavior against real rows: the diagnostic
// thinking window is a hard 1..30-day ceiling on the policy, report mode
// counts before erasing, and the sweep erases expired diagnostic thinking
// in place (rows and seq order survive; content/output/input clear) and
// is idempotent. Spec §12.2/§12.11.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/skillevolution"
)

func addMessageAt(t *testing.T, f *trajectoryFixture, taskID string, seq int, msgType, visibility, content string, ageDays int) {
	t.Helper()
	_, err := f.pool.Exec(context.Background(), `
		INSERT INTO task_message (task_id, seq, type, tool, content, input, output, visibility, created_at)
		VALUES($1::uuid, $2, $3, NULL, $4, $5::jsonb, $6, $7, now() - make_interval(days => $8))`,
		taskID, seq, msgType, content,
		`{"thought":"private reasoning"}`, `{"note":"also private"}`,
		visibility, int32(ageDays))
	require.NoError(t, err)
}

func messagePayload(t *testing.T, f *trajectoryFixture, taskID string, seq int) (content, output string, inputNil bool) {
	t.Helper()
	var input *string
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`SELECT content, output, input FROM task_message WHERE task_id=$1::uuid AND seq=$2`,
		taskID, seq).Scan(&content, &output, &input))
	return content, output, input == nil
}

// The thinking sweep clears only expired diagnostic-only thinking rows —
// in place, keeping every row and its seq position so sanitized
// trajectories stay contiguous.
func TestDiagnosticThinkingSweepErasesOnlyExpiredDiagnosticRows(t *testing.T) {
	f := newTrajectoryFixture(t)
	retention := NewMemoryRetentionService(f.pool, nil)
	ctx := context.Background()
	taskID := f.createTask(t, "completed", "")

	// 40 days old: past the 30-day bootstrap window.
	addMessageAt(t, f, taskID, 1, "user", "user_facing", "old visible user message", 40)
	addMessageAt(t, f, taskID, 2, "thinking", "diagnostic_only", "old chain of thought", 40)
	addMessageAt(t, f, taskID, 3, "thinking", "diagnostic_only", "fresh chain of thought", 1)
	addMessageAt(t, f, taskID, 4, "thinking", "user_facing", "old but observable thinking", 40)
	addMessageAt(t, f, taskID, 5, "log", "diagnostic_only", "old diagnostic log line", 40)

	policy, err := retention.CurrentPolicy(ctx, parseUUID(t, f.workspaceID))
	require.NoError(t, err)
	assert.Equal(t, 30, policy.DiagnosticThinkingDays, "bootstrap binds the platform ceiling immediately")
	assert.Equal(t, int64(1), policy.Version)

	// Report mode counts without erasing.
	due, err := retention.ReportDueDiagnosticThinking(ctx, parseUUID(t, f.workspaceID))
	require.NoError(t, err)
	assert.Equal(t, 1, due, "only the expired diagnostic-only thinking row is due")
	_, output, _ := messagePayload(t, f, taskID, 2)
	assert.NotEmpty(t, output, "report mode erases nothing")

	_, err = retention.SweepDue(ctx, 64)
	require.NoError(t, err)

	// The expired thinking row survives as a row with cleared payload.
	content, output, inputNil := messagePayload(t, f, taskID, 2)
	assert.Equal(t, "", content)
	assert.Equal(t, "", output)
	assert.True(t, inputNil)
	var rows int
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM task_message WHERE task_id=$1::uuid`, taskID).Scan(&rows))
	assert.Equal(t, 5, rows, "no rows are deleted: seq continuity is preserved")

	// Everything else is untouched.
	assert.Equal(t, "old visible user message", mustContent(t, f, taskID, 1))
	assert.Equal(t, "fresh chain of thought", mustContent(t, f, taskID, 3))
	assert.Equal(t, "old but observable thinking", mustContent(t, f, taskID, 4))
	assert.Equal(t, "old diagnostic log line", mustContent(t, f, taskID, 5))

	// Idempotent: nothing is due anymore.
	due, err = retention.ReportDueDiagnosticThinking(ctx, parseUUID(t, f.workspaceID))
	require.NoError(t, err)
	assert.Zero(t, due)
	_, err = retention.SweepDue(ctx, 64)
	require.NoError(t, err)
	content, output, _ = messagePayload(t, f, taskID, 3)
	assert.Equal(t, "fresh chain of thought", content)
	assert.NotEmpty(t, output)
}

func mustContent(t *testing.T, f *trajectoryFixture, taskID string, seq int) string {
	t.Helper()
	content, _, _ := messagePayload(t, f, taskID, seq)
	return content
}

// The thinking window may shorten within 1..30 but never exceeds the
// platform ceiling — in validation and in the DB CHECK.
func TestDiagnosticThinkingPolicyHasHardCeiling(t *testing.T) {
	f := newTrajectoryFixture(t)
	retention := NewMemoryRetentionService(f.pool, nil)
	ctx := context.Background()

	base := MemoryRetentionUpdate{
		TrajectoryHotDays: 90, ArchiveDays: 365, TraceHotDays: 30,
		DiagnosticThinkingDays: 30, ExpectedVersion: 1,
	}

	tooLong := base
	tooLong.DiagnosticThinkingDays = 31
	assert.ErrorIs(t, tooLong.Validate(), ErrMemoryRetentionCap)

	zero := base
	zero.DiagnosticThinkingDays = 0
	assert.ErrorIs(t, zero.Validate(), ErrMemoryRetentionCap)

	shortened := base
	shortened.DiagnosticThinkingDays = 7
	updated, err := retention.UpdatePolicy(ctx, parseUUID(t, f.workspaceID), shortened, "admin:ops")
	require.NoError(t, err)
	assert.Equal(t, int64(2), updated.Version)
	assert.Equal(t, 7, updated.DiagnosticThinkingDays)

	// Lengthening back inside the ceiling is allowed; leaving it is not.
	restored := shortened
	restored.DiagnosticThinkingDays = 30
	restored.ExpectedVersion = 2
	_, err = retention.UpdatePolicy(ctx, parseUUID(t, f.workspaceID), restored, "admin:ops")
	require.NoError(t, err)

	// The DB CHECK is the second wall against raw writes.
	_, err = f.pool.Exec(ctx, `
		INSERT INTO memory_retention_policy (workspace_id, version, trajectory_hot_days, archive_days, trace_hot_days, diagnostic_thinking_days, updated_by)
		VALUES($1::uuid, 99, 90, 365, 30, 31, 'raw-sql')`, parseUUID(t, f.workspaceID))
	require.Error(t, err, "the ceiling holds at the storage layer too")
}

// An erased thinking row keeps projecting as an excluded diagnostic row:
// the projector stays usable on swept histories (spec §12.2 contiguity).
func TestProjectorStillHandlesSweptThinkingRows(t *testing.T) {
	f := newTrajectoryFixture(t)
	retention := NewMemoryRetentionService(f.pool, nil)
	ctx := context.Background()
	taskID := f.createTask(t, "completed", "")

	addMessageAt(t, f, taskID, 1, "user", "user_facing", "export the sheet", 1)
	addMessageAt(t, f, taskID, 2, "thinking", "diagnostic_only", "old chain of thought", 40)
	addMessageAt(t, f, taskID, 3, "assistant", "user_facing", "exported", 1)

	_, err := retention.SweepDue(ctx, 64)
	require.NoError(t, err)

	trajectory, err := f.projector.ProjectRunTrajectory(ctx, f.workspaceID, taskID,
		f.eligibility(taskID), f.outcome(skillevolution.OutcomePass, taskID))
	require.NoError(t, err)
	assert.Len(t, trajectory.Events, 2, "the swept thinking row is still excluded by type")
	assert.Equal(t, 1, trajectory.DiagnosticExclusions)
}
