package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOfflineNormalizationNeverReturnsCredentials(t *testing.T) {
	rawRequest := []byte(`{
		"messages":[{"role":"user","blocks":[{"type":"text","text":"hi"}]}],
		"authorization":"Bearer secret-token",
		"api_key":"secret"
	}`)
	rawResponse := []byte(`{"role":"assistant","blocks":[{"type":"text","text":"ok"}]}`)
	src := OfflineCallSource{
		CallID:                "sec-1",
		TrainingMode:          "offline_rl",
		Provider:              "provider",
		Model:                 "model",
		APIKind:               "messages",
		RawProviderRequest:    rawRequest,
		FinalAssistantMessage: rawResponse,
		Status:                "completed",
		StopReason:            "stop",
		ResponseComplete:      true,
		TrainingEligible:      true,
		RequestHash:           offlinePayloadHash(rawRequest),
		ResponseHash:          offlinePayloadHash(rawResponse),
	}

	line := NormalizeOfflineCall(src)
	require.Equal(t, offlineStatusTrajectory, line.Status)
	require.NotNil(t, line.Trajectory)
	encoded, err := json.Marshal(line)
	require.NoError(t, err)
	raw := string(encoded)
	assert.NotContains(t, raw, "Bearer ")
	assert.NotContains(t, raw, "secret-token")
	assert.NotContains(t, raw, `"api_key"`)
	assert.NotContains(t, raw, "authorization")
}

func TestFrozenDAGSanitizedResponseOmitsRawProviderPayloadKeys(t *testing.T) {
	forbidden := []string{
		"raw_provider_request",
		"final_assistant_message",
		"normalized_trajectory",
		"authorization",
		"api_key",
	}
	sample, err := json.Marshal(map[string]any{
		"provider_calls": []map[string]any{
			{"call_id": "C1", "request_hash": "sha256:x", "response_hash": "sha256:y"},
		},
	})
	require.NoError(t, err)
	body := string(sample)
	for _, key := range forbidden {
		assert.NotContains(t, body, key)
	}
}

func TestOfflineResolveExcludesWrongModeAndKeepsRetentionBoundary(t *testing.T) {
	online := OfflineCallSource{
		CallID:       "online-1",
		TrainingMode: "online_rl",
	}
	line := NormalizeOfflineCall(online)
	assert.Equal(t, offlineStatusExcluded, line.Status)
	assert.Equal(t, OfflineReasonWrongModeOnlineRL, line.Reason)

	none := OfflineCallSource{CallID: "none-1", TrainingMode: "none"}
	line = NormalizeOfflineCall(none)
	assert.Equal(t, offlineStatusExcluded, line.Status)
	assert.Equal(t, OfflineReasonWrongModeNone, line.Reason)
}
