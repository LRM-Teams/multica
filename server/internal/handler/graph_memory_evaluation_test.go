// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/service"
)

// Evaluation arm enforcement on the write seams (Handoff 7 §10: policy
// tests before any benchmark execution): a live persistence_off episode
// suppresses graph capture anchors — the channel's only durable write entry
// for graph AND legacy memory after #2295 — and the suppression lifts the
// moment the episode settles.

// insertLiveEvaluationEpisode provisions a run plus one live episode row
// through the real service (env-gated), so the handler seams read exactly
// what production would read.
func insertLiveEvaluationEpisode(t *testing.T, workspaceID, channelID, agentID, arm, runID, episodeID string) {
	t.Helper()
	evaluation := service.NewGraphMemoryEvaluationService(testPool)
	require.NoError(t, evaluation.CreateRun(context.Background(), service.GraphMemoryEvaluationRunInput{
		WorkspaceID: workspaceID, RunID: runID, CreatedByActor: "harness",
	}))
	require.NoError(t, evaluation.CreateEpisode(context.Background(), service.GraphMemoryEvaluationEpisodeInput{
		WorkspaceID: workspaceID, RunID: runID, EpisodeID: episodeID,
		ChannelID: channelID, PrimaryAgentID: agentID,
		Arm: arm, SessionGeneration: "sgen-" + episodeID,
	}))
}

func TestGraphCapturePersistenceOffSuppressesDurableWrites(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	workspaceID, channelID, agentID := seedGraphCaptureFixture(t, true)
	t.Setenv("MULTICA_GRAPH_MEMORY_EVALUATION_ENABLED", "1")
	t.Setenv("MULTICA_GRAPH_MEMORY_EVALUATION_WORKSPACES", uuidToString(workspaceID))

	// The seam helper agrees with durable state.
	require.False(t, graphMemoryEvaluationPersistenceOff(ctx, testPool, workspaceID, channelID)) // no episode yet

	runID := "capture-run-" + uuid.NewString()[:8]
	insertLiveEvaluationEpisode(t, uuidToString(workspaceID), uuidToString(channelID), agentID,
		service.GraphMemoryEvaluationArmPersistenceOff, runID, "ep-off")
	require.True(t, graphMemoryEvaluationPersistenceOff(ctx, testPool, workspaceID, channelID))

	suppressed := sendDirectedGraphCaptureMessage(t, workspaceID, channelID, agentID,
		"Suppressed turn: the fusion reactor uses deuterium, token "+uuid.NewString()[:8]+".")
	require.Equal(t, 0, graphCaptureAnchorCount(t, suppressed.ID), "persistence_off episode must mint no capture anchor")
	var segments int
	require.NoError(t, testPool.QueryRow(ctx, `
		SELECT count(*) FROM interaction_dag_segment WHERE workspace_id=$1`, workspaceID).Scan(&segments))
	require.Equal(t, 0, segments, "persistence_off episode must leave no DAG segment")

	// Settling the episode lifts the suppression: the next turn mints the
	// anchor exactly as before.
	evaluation := service.NewGraphMemoryEvaluationService(testPool)
	require.NoError(t, evaluation.StartEpisode(ctx, uuidToString(workspaceID), runID, "ep-off", suppressed.ID))
	complete := map[string]service.GraphMemoryClosureState{}
	for _, condition := range []string{
		service.GraphMemoryClosureSessionGenerationReset,
		service.GraphMemoryClosurePrimaryReplyCommitted,
		service.GraphMemoryClosureDaemonProjection,
		service.GraphMemoryClosureNoActiveClaim,
		service.GraphMemoryClosureCheckpointSettled,
		service.GraphMemoryClosureJobsSettled,
		service.GraphMemoryClosureStateTiedToGeneration,
	} {
		complete[condition] = service.GraphMemoryClosureState{State: "complete"}
	}
	require.NoError(t, evaluation.SettleEpisode(ctx, uuidToString(workspaceID), runID, "ep-off", uuid.NewString(), complete))
	require.False(t, graphMemoryEvaluationPersistenceOff(ctx, testPool, workspaceID, channelID))

	restored := sendDirectedGraphCaptureMessage(t, workspaceID, channelID, agentID,
		"Restored turn: the fusion reactor uses deuterium, token "+uuid.NewString()[:8]+".")
	require.Equal(t, 1, graphCaptureAnchorCount(t, restored.ID), "settled episode must restore capture")
}
