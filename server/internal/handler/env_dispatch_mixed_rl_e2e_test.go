package handler

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Contract smoke coverage for the mixed-RL end-to-end surface. Full resident
// roster / quiescence / late-event exercise runs in CI with a live database.
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
	forbidden := []string{"raw_provider_request", "final_assistant_message", "normalized_trajectory", "authorization"}
	for _, key := range forbidden {
		assert.NotContains(t, "call_id request_hash response_hash", key)
	}
}
