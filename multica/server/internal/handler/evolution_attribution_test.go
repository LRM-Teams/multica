package handler

import (
	"encoding/json"
	"testing"
)

func TestMergeEvolutionAttributionPayloadUsesExplicitStableIDs(t *testing.T) {
	payload := mergeEvolutionAttributionPayload(json.RawMessage(`{"title":"candidate"}`), EvolutionSubmissionRequest{
		SourceUserID: " user-frank ", SubjectType: "MEMBER", SubjectID: " user-frank ",
	})
	for key, want := range map[string]string{
		"source_user_id": "user-frank",
		"subject_type":   "member",
		"subject_id":     "user-frank",
	} {
		if got := evolutionPayloadString(payload, key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}
