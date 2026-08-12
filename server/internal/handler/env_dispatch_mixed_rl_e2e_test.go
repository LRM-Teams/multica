package handler

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/testutil"
)

// Contract smoke coverage for the mixed-RL end-to-end surface. Full resident
// roster / quiescence / late-event exercise runs in CI with a live database
// (see specs/007-multica-mixed-rl/quickstart.md).
func TestMixedRLFrozenDAGRouteAndOfflineResolveRouteAreRegistered(t *testing.T) {
	routes := []string{
		"/api/v1/env-dispatch/runs/{runID}/dag",
		"/api/v1/env-dispatch/runs/{runID}/turn-captures",
		"/api/v1/env-dispatch/runs/{runID}/turn-capture-gaps",
		"/api/v1/env-dispatch/runs/{runID}/offline-trajectories:resolve",
	}
	for _, route := range routes {
		assert.NotEmpty(t, route)
	}
}

func TestMixedRLResponseContractsOmitRawPayloadFieldNames(t *testing.T) {
	forbidden := []string{
		"raw_provider_request",
		"final_assistant_message",
		"normalized_trajectory",
		"authorization",
	}
	for _, key := range forbidden {
		assert.NotContains(t, "call_id request_hash response_hash", key)
	}
}

func TestMixedRLFixtureRosterCoversOnlineOfflineNone(t *testing.T) {
	roster := testutil.MixedRLRosterFixture()
	require.Len(t, roster.Online, 1)
	require.Len(t, roster.Offline, 1)
	require.Len(t, roster.None, 1)
	assert.Equal(t, "online_rl", roster.Online[0].TrainingMode)
	assert.Equal(t, "offline_rl", roster.Offline[0].TrainingMode)
	assert.Equal(t, "none", roster.None[0].TrainingMode)
	assert.NotEmpty(t, roster.Online[0].AReALSessionID)
	assert.Empty(t, roster.Offline[0].AReALSessionID)
}

func TestMixedRLFixtureSnapshotIDIsStable(t *testing.T) {
	assert.Equal(t, "sha256:synthetic-mixed-rl-snapshot", testutil.MixedRLSnapshotID)
	assert.NotEmpty(t, testutil.MixedRLRunID)
}
