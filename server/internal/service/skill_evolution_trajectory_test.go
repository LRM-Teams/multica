// SPDX-License-Identifier: Apache-2.0

package service

// Migration-free Slice 2.2 wiring against real durable rows: the
// trajectory projector reads task_message through the shared sanitizer,
// excludes diagnostic-only content, fails closed on cross-tenant reads and
// dangling artifacts, and classifies outcomes without blaming the agent
// for infrastructure or policy breaks.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/skillevolution"
)

type trajectoryFixture struct {
	projector                                      *SkillEvolutionTrajectoryProjector
	pool                                           *pgxpool.Pool
	workspaceID, agentID, ownerID, issueID, taskID string
}

func newTrajectoryFixture(t *testing.T) *trajectoryFixture {
	t.Helper()
	pool := bootstrapUniversalDAGProjectionSchema(t)
	ctx := context.Background()
	var workspaceID string
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug) VALUES ('trajectory test', 'traj-'||$1)
		RETURNING id::text`, uuid.NewString()[:8]).Scan(&workspaceID))
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM workspace WHERE id=$1::uuid`, workspaceID) })

	var ownerID string
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ('traj-owner-'||$1, 'traj-owner-'||$1||'@multica.ai')
		RETURNING id::text`, uuid.NewString()[:8]).Scan(&ownerID))

	var runtimeID, agentID string
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO agent_runtime(workspace_id,daemon_id,name,runtime_mode,provider,status,device_info,metadata,visibility,last_seen_at)
		VALUES($1::uuid,$2,'traj-runtime','local','pi','online','','{}','private',now()) RETURNING id::text`,
		workspaceID, "traj-daemon-"+uuid.NewString()[:8]).Scan(&runtimeID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO agent(workspace_id,name,display_name,runtime_mode,runtime_config,runtime_id,owner_id,managed_role,instructions)
		VALUES($1::uuid,$2,'Trajectory agent','local','{}',$3::uuid,$4::uuid,'graph_memory_channel','managed memory') RETURNING id::text`,
		workspaceID, "traj-agent-"+uuid.NewString()[:8], runtimeID, ownerID).Scan(&agentID))

	var issueID string
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, number)
		VALUES($1::uuid, 'trajectory source', 'done', 'none', 'member', $2::uuid, 1)
		RETURNING id::text`, workspaceID, ownerID).Scan(&issueID))

	return &trajectoryFixture{
		projector: NewSkillEvolutionTrajectoryProjector(pool, nil),
		pool:      pool, workspaceID: workspaceID, agentID: agentID,
		ownerID: ownerID, issueID: issueID,
	}
}

func (f *trajectoryFixture) createTask(t *testing.T, terminalOutcome, runError string) string {
	t.Helper()
	var taskID string
	require.NoError(t, f.pool.QueryRow(context.Background(), `
		INSERT INTO agent_inbox_event (workspace_id, agent_id, reason, status, terminal_outcome, error)
		VALUES($1::uuid, $2::uuid, 'issue', 'acked', NULLIF($3, ''), NULLIF($4, ''))
		RETURNING id::text`, f.workspaceID, f.agentID, terminalOutcome, runError).Scan(&taskID))
	return taskID
}

func (f *trajectoryFixture) addMessage(t *testing.T, taskID string, seq int, msgType, visibility, content string) {
	t.Helper()
	_, err := f.pool.Exec(context.Background(), `
		INSERT INTO task_message (task_id, seq, type, tool, content, input, output, visibility)
		VALUES($1::uuid, $2, $3, NULL, $4, NULL, NULL, $5)`,
		taskID, seq, msgType, content, visibility)
	require.NoError(t, err)
}

func (f *trajectoryFixture) eligibility(taskID string) skillevolution.TrajectoryEligibility {
	return skillevolution.TrajectoryEligibility{
		RunID: taskID, WorkspaceID: f.workspaceID, RunKind: "agent_task",
		EvolutionEligible: true,
		AllowedPurposes:   []skillevolution.TrajectoryPurpose{skillevolution.TrajectoryPurposeSkillEvolution},
		TaskType:          "spreadsheet", LineageID: "lineage-1",
		FixedAt: time.Now().UTC(), FixedByActor: "system:run-start",
	}
}

func (f *trajectoryFixture) outcome(kind skillevolution.OutcomeKind, taskID string) skillevolution.OutcomeRecord {
	return skillevolution.OutcomeRecord{
		Outcome: kind, Reason: "fixture",
		SourceRef: "agent_inbox_event:" + taskID, RecordedAt: time.Now().UTC(),
	}
}

// The projector reuses the shared sanitizer and excludes diagnostic-only
// content from real persisted rows.
func TestTrajectoryProjectorServesSanitizedObservableEvents(t *testing.T) {
	f := newTrajectoryFixture(t)
	ctx := context.Background()
	taskID := f.createTask(t, "completed", "")

	f.addMessage(t, taskID, 1, "user", "user_facing", "export the Q3 summary sheet")
	f.addMessage(t, taskID, 2, "thinking", "diagnostic_only", "secret chain of thought")
	f.addMessage(t, taskID, 3, "tool_use", "user_facing", "")
	f.addMessage(t, taskID, 4, "tool_output", "user_facing", "sk-12345 redacted downstream")
	f.addMessage(t, taskID, 5, "assistant", "user_facing", "exported with totals")

	trajectory, err := f.projector.ProjectRunTrajectory(ctx, f.workspaceID, taskID,
		f.eligibility(taskID), f.outcome(skillevolution.OutcomePass, taskID))
	require.NoError(t, err)
	require.Len(t, trajectory.Events, 4)
	assert.Equal(t, skillevolution.KindMessage, trajectory.Events[0].Kind)
	assert.Equal(t, skillevolution.KindToolCall, trajectory.Events[1].Kind)
	assert.Equal(t, skillevolution.KindToolResult, trajectory.Events[2].Kind)
	assert.Equal(t, 1, trajectory.DiagnosticExclusions, "the thinking row is excluded and counted")
	assert.Equal(t, DefaultSanitizerPolicy().SanitizerVersion, trajectory.SanitizerVersion)
	for _, event := range trajectory.Events {
		assert.NotContains(t, event.Content, "chain of thought")
	}

	_, err = f.projector.ProjectRunTrajectory(ctx, uuid.NewString(), taskID,
		f.eligibility(taskID), f.outcome(skillevolution.OutcomePass, taskID))
	require.ErrorIs(t, err, skillevolution.ErrTrajectoryNotEligible,
		"a task never projects outside its workspace")

	mismatched := f.eligibility(uuid.NewString())
	_, err = f.projector.ProjectRunTrajectory(ctx, f.workspaceID, taskID,
		mismatched, f.outcome(skillevolution.OutcomePass, taskID))
	require.ErrorIs(t, err, skillevolution.ErrTrajectoryNotEligible,
		"the eligibility snapshot must be pinned to this very run")
}

// A trajectory that externalized oversized fields needs a checker, and the
// checker must confirm the backing blob — otherwise the projection fails
// closed instead of admitting a dangling placeholder.
func TestTrajectoryProjectorFailsClosedOnDanglingArtifacts(t *testing.T) {
	f := newTrajectoryFixture(t)
	ctx := context.Background()
	taskID := f.createTask(t, "completed", "")
	f.addMessage(t, taskID, 1, "user", "user_facing", "small message")
	f.addMessage(t, taskID, 2, "tool_output", "user_facing", strings.Repeat("x", 128*1024))

	eligibility := f.eligibility(taskID)
	outcome := f.outcome(skillevolution.OutcomePass, taskID)

	_, err := f.projector.ProjectRunTrajectory(ctx, f.workspaceID, taskID, eligibility, outcome)
	require.ErrorIs(t, err, skillevolution.ErrTrajectoryArtifactRef,
		"no checker wired means the oversized field cannot be trusted")

	checking := NewSkillEvolutionTrajectoryProjector(f.pool, stubArtifactChecker{exists: false})
	_, err = checking.ProjectRunTrajectory(ctx, f.workspaceID, taskID, eligibility, outcome)
	require.ErrorIs(t, err, skillevolution.ErrTrajectoryArtifactRef,
		"a missing backing blob never enters the corpus")

	verified := NewSkillEvolutionTrajectoryProjector(f.pool, stubArtifactChecker{exists: true})
	trajectory, err := verified.ProjectRunTrajectory(ctx, f.workspaceID, taskID, eligibility, outcome)
	require.NoError(t, err)
	require.Len(t, trajectory.ArtifactRefs, 1)
	assert.Len(t, trajectory.Events, 2)
}

type stubArtifactChecker struct{ exists bool }

func (s stubArtifactChecker) ArtifactExists(context.Context, string, string) (bool, error) {
	return s.exists, nil
}

// Outcome signals classify without smearing infrastructure or policy
// breaks onto the agent; unrecognized failures stay unclassified.
func TestTrajectoryOutcomeSignalClassification(t *testing.T) {
	f := newTrajectoryFixture(t)
	ctx := context.Background()

	cases := []struct {
		status, runError string
		expected         skillevolution.OutcomeKind
	}{
		{"completed", "", skillevolution.OutcomePass},
		{"replied", "", skillevolution.OutcomePass},
		{"completed", "partial delivery: 2 of 3 sheets exported", skillevolution.OutcomePartial},
		{"failed", "provider error: connection reset by peer", skillevolution.OutcomeInfrastructureInvalid},
		{"failed", "permission denied on storage bucket", skillevolution.OutcomePolicyDenied},
		{"failed", "unsupported feature: macro evaluation", skillevolution.OutcomePolicyDenied},
	}
	for _, testCase := range cases {
		taskID := f.createTask(t, testCase.status, testCase.runError)
		signal, err := f.projector.TaskOutcomeSignal(ctx, f.workspaceID, taskID)
		require.NoError(t, err)
		kind, err := skillevolution.ClassifyOutcome(signal)
		require.NoError(t, err, testCase.runError)
		assert.Equal(t, testCase.expected, kind, testCase.runError)
	}

	unclear := f.createTask(t, "failed", "the agent produced the wrong formula")
	signal, err := f.projector.TaskOutcomeSignal(ctx, f.workspaceID, unclear)
	require.NoError(t, err)
	_, err = skillevolution.ClassifyOutcome(signal)
	require.Error(t, err,
		"an unclassified failure must be reviewed, never defaulted to agent failure")

	_, err = f.projector.TaskOutcomeSignal(ctx, uuid.NewString(), unclear)
	require.Error(t, err, "outcome signals never resolve across tenants")

	cancelled := f.createTask(t, "cancelled", "")
	signal, err = f.projector.TaskOutcomeSignal(ctx, f.workspaceID, cancelled)
	require.NoError(t, err)
	_, err = skillevolution.ClassifyOutcome(signal)
	require.Error(t, err, "a cancelled run needs explicit review, not a default outcome")
}
