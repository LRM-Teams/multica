package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestOfflineNormalizationNeverReturnsCredentials(t *testing.T) {
	call := db.PiProviderCall{
		CallID:       "sec-1",
		Provider:     "provider",
		Model:        "model",
		ApiKind:      "messages",
		RequestHash:  "sha256:req",
		ResponseHash: text("sha256:resp"),
		RawProviderRequest: []byte(`{
			"messages":[{"role":"user","blocks":[{"type":"text","text":"hi"}]}],
			"authorization":"Bearer secret-token",
			"api_key":"secret"
		}`),
		FinalAssistantMessage: []byte(`{"role":"assistant","blocks":[{"type":"text","text":"ok"}]}`),
	}
	encoded, reason, err := normalizeOfflineTrajectory(call)
	require.NoError(t, err)
	require.Empty(t, reason)
	raw := string(encoded)
	assert.NotContains(t, raw, "Bearer ")
	assert.NotContains(t, raw, "secret-token")
	assert.NotContains(t, raw, `"api_key"`)
}

func TestFrozenDAGSanitizedResponseOmitsRawProviderPayloadKeys(t *testing.T) {
	forbidden := []string{"raw_provider_request", "final_assistant_message", "normalized_trajectory"}
	sample, err := json.Marshal(map[string]any{
		"provider_calls": []map[string]any{{"call_id": "C1", "request_hash": "sha256:x"}},
	})
	require.NoError(t, err)
	body := string(sample)
	for _, key := range forbidden {
		assert.NotContains(t, body, key)
	}
}
