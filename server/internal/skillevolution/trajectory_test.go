// SPDX-License-Identifier: Apache-2.0

package skillevolution

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var trajectoryTestTime = time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

func eligibleSnapshot() TrajectoryEligibility {
	return TrajectoryEligibility{
		RunID: "run-1", WorkspaceID: "workspace-1", RunKind: "agent_task",
		EvolutionEligible: true,
		AllowedPurposes:   []TrajectoryPurpose{TrajectoryPurposeSkillEvolution},
		TaskType:          "spreadsheet", LineageID: "lineage-1",
		FixedAt: trajectoryTestTime, FixedByActor: "system:run-start",
	}
}

func passingOutcome() OutcomeRecord {
	return OutcomeRecord{
		Outcome: OutcomePass, Reason: "deliverable accepted",
		SourceRef: "agent_task_queue:task-1", RecordedAt: trajectoryTestTime,
	}
}

func segmentFixture() SourceSegment {
	return SourceSegment{
		SegmentID: "seg-1", SanitizerVersion: "sixfield-redact-v1", PolicyVersion: "redact-default-v1",
		Messages: []SourceMessage{
			{Sequence: 1, Type: "user", Visibility: "user_facing", Content: "export the Q3 sheet"},
			{Sequence: 2, Type: "thinking", Visibility: "diagnostic_only", Content: "chain of thought"},
			{Sequence: 3, Type: "tool_use", Tool: "xlsx.read", Visibility: "user_facing"},
			{Sequence: 4, Type: "tool_output", Tool: "xlsx.read", Visibility: "user_facing", Output: "[ARTIFACT EXTERNALIZED artifact:sha256:" + strings.Repeat("ab", 32) + "]"},
			{Sequence: 5, Type: "assistant", Visibility: "user_facing", Content: "exported"},
		},
		ArtifactRefs: []string{"artifact:sha256:" + strings.Repeat("ab", 32)},
	}
}

// Outcome classification never smears infrastructure, adapter, evaluator,
// unsupported-feature, or policy failures onto the agent.
func TestClassifyOutcomeNeverSmearsInfraOntoAgent(t *testing.T) {
	for _, class := range []string{"infrastructure", "adapter", "evaluator"} {
		kind, err := ClassifyOutcome(OutcomeSignal{Status: "failed", ErrorClass: class})
		require.NoError(t, err, class)
		assert.Equal(t, OutcomeInfrastructureInvalid, kind, class)
	}
	for _, class := range []string{"policy", "unsupported"} {
		kind, err := ClassifyOutcome(OutcomeSignal{Status: "failed", ErrorClass: class})
		require.NoError(t, err, class)
		assert.Equal(t, OutcomePolicyDenied, kind, class)
	}
	kind, err := ClassifyOutcome(OutcomeSignal{Status: "failed", ErrorClass: "agent"})
	require.NoError(t, err)
	assert.Equal(t, OutcomeAgentFailure, kind)

	kind, err = ClassifyOutcome(OutcomeSignal{Status: "completed"})
	require.NoError(t, err)
	assert.Equal(t, OutcomePass, kind)
	kind, err = ClassifyOutcome(OutcomeSignal{Status: "completed", Partial: true})
	require.NoError(t, err)
	assert.Equal(t, OutcomePartial, kind)

	_, err = ClassifyOutcome(OutcomeSignal{Status: "failed"})
	assert.Error(t, err, "an unclassified failure must not default to agent failure")
	_, err = ClassifyOutcome(OutcomeSignal{Status: "completed", ErrorClass: "vibes"})
	assert.Error(t, err)
}

// Eligibility is pinned at run start and can only be revoked.
func TestTrajectoryEligibilityIsPinnedAtRunStart(t *testing.T) {
	snapshot := eligibleSnapshot()
	require.NoError(t, snapshot.Validate())

	revoked, err := snapshot.RevokeEligibility("member:admin", "dataset retracted", trajectoryTestTime.Add(time.Hour))
	require.NoError(t, err)
	assert.True(t, revoked.Revoked())
	assert.False(t, revoked.EvolutionEligible)
	assert.Equal(t, trajectoryTestTime, revoked.FixedAt, "the run-start pin survives revocation")

	_, err = revoked.RevokeEligibility("member:admin", "again", trajectoryTestTime.Add(2*time.Hour))
	assert.Error(t, err, "double revocation is rejected")

	_, err = ProjectObservableTrajectory(revoked, passingOutcome(), []SourceSegment{segmentFixture()})
	assert.ErrorIs(t, err, ErrTrajectoryNotEligible, "a revoked run never projects")

	_, err = ProjectObservableTrajectory(TrajectoryEligibility{RunID: "run-2"}, passingOutcome(), nil)
	assert.Error(t, err, "an unpinned snapshot never validates")

	unclassified := eligibleSnapshot()
	unclassified.AllowedPurposes = append(unclassified.AllowedPurposes, TrajectoryPurposeSkillEvolution)
	assert.Error(t, unclassified.Validate(), "duplicate purposes are rejected")
	closed := eligibleSnapshot()
	closed.AllowedPurposes = []TrajectoryPurpose{"task_recall"}
	assert.Error(t, closed.Validate(), "task recall is not a trajectory purpose")
}

// The projector keeps only the observable allowlist: thinking and other
// diagnostic shapes are excluded and counted, sanitized tool activity
// survives, and unknown shapes fail closed.
func TestProjectorExcludesThinkingAndKeepsSanitizedToolActivity(t *testing.T) {
	trajectory, err := ProjectObservableTrajectory(eligibleSnapshot(), passingOutcome(), []SourceSegment{segmentFixture()})
	require.NoError(t, err)

	require.Len(t, trajectory.Events, 4)
	assert.Equal(t, KindMessage, trajectory.Events[0].Kind)
	assert.Equal(t, KindToolCall, trajectory.Events[1].Kind)
	assert.Equal(t, "xlsx.read", trajectory.Events[1].Tool)
	assert.Equal(t, KindToolResult, trajectory.Events[2].Kind)
	assert.Equal(t, KindMessage, trajectory.Events[3].Kind)
	assert.Equal(t, 1, trajectory.DiagnosticExclusions, "the thinking message is excluded and counted")
	require.Len(t, trajectory.ArtifactRefs, 1)
	assert.Equal(t, "sixfield-redact-v1", trajectory.SanitizerVersion)
	for _, event := range trajectory.Events {
		assert.NotContains(t, event.Content, "chain of thought")
	}
}

func TestProjectorFailsClosedOnUnknownShapes(t *testing.T) {
	forbidden := segmentFixture()
	forbidden.Messages = append(forbidden.Messages, SourceMessage{Sequence: 6, Type: "vibe_check", Visibility: "user_facing"})
	_, err := ProjectObservableTrajectory(eligibleSnapshot(), passingOutcome(), []SourceSegment{forbidden})
	assert.ErrorIs(t, err, ErrTrajectoryForbiddenType)

	unknownVisibility := segmentFixture()
	unknownVisibility.Messages = append(unknownVisibility.Messages, SourceMessage{Sequence: 6, Type: "text", Visibility: "maybe"})
	_, err = ProjectObservableTrajectory(eligibleSnapshot(), passingOutcome(), []SourceSegment{unknownVisibility})
	assert.ErrorIs(t, err, ErrTrajectoryVisibility)

	mixed := []SourceSegment{segmentFixture(), {
		SegmentID: "seg-2", SanitizerVersion: "other-v9", PolicyVersion: "redact-default-v1",
		Messages: []SourceMessage{{Sequence: 10, Type: "text", Visibility: "user_facing"}},
	}}
	_, err = ProjectObservableTrajectory(eligibleSnapshot(), passingOutcome(), mixed)
	assert.ErrorIs(t, err, ErrTrajectoryInconsistentSanitizer)

	unprovenanced := SourceSegment{SegmentID: "seg-3", Messages: []SourceMessage{{Sequence: 1, Type: "text", Visibility: "user_facing"}}}
	_, err = ProjectObservableTrajectory(eligibleSnapshot(), passingOutcome(), []SourceSegment{unprovenanced})
	assert.ErrorIs(t, err, ErrTrajectoryInconsistentSanitizer)

	disordered := segmentFixture()
	disordered.Messages = append(disordered.Messages, SourceMessage{Sequence: 5, Type: "text", Visibility: "user_facing"})
	_, err = ProjectObservableTrajectory(eligibleSnapshot(), passingOutcome(), []SourceSegment{disordered})
	assert.ErrorIs(t, err, ErrTrajectorySequence)

	dangling := segmentFixture()
	dangling.ArtifactRefs = []string{"artifact:literal-filename.xlsx"}
	_, err = ProjectObservableTrajectory(eligibleSnapshot(), passingOutcome(), []SourceSegment{dangling})
	assert.ErrorIs(t, err, ErrTrajectoryArtifactRef)

	_, err = ProjectObservableTrajectory(eligibleSnapshot(), OutcomeRecord{Outcome: "vibes", SourceRef: "x", RecordedAt: trajectoryTestTime}, []SourceSegment{segmentFixture()})
	assert.Error(t, err)
}
