package handler

import (
	"encoding/json"
	"testing"

	"github.com/multica-ai/multica/server/internal/researchrun"
)

func TestResearchDispatchInboxContextCarriesFrozenManifestIdentity(t *testing.T) {
	request := researchrun.DispatchRequest{
		Run: researchrun.Run{SessionID: "session-1"}, Task: researchrun.Task{ID: "task-1", TimeoutSeconds: 30, AcceptanceCriteria: json.RawMessage(`[{"criterion":"structured"}]`)},
		AttemptID: "attempt-1", Key: "dispatch-1", ManifestID: "manifest-1",
		ManifestHash: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	encoded, err := encodeResearchDispatchInboxContext(request, "request-hash")
	if err != nil {
		t.Fatal(err)
	}
	var contextPayload map[string]any
	if err = json.Unmarshal(encoded, &contextPayload); err != nil {
		t.Fatal(err)
	}
	if contextPayload["research_manifest_id"] != request.ManifestID || contextPayload["research_manifest_hash"] != request.ManifestHash {
		t.Fatalf("context=%v", contextPayload)
	}
	if _, ok := contextPayload["research_task_timeout_seconds"].(float64); !ok {
		t.Fatalf("timeout lost numeric JSON type: %T", contextPayload["research_task_timeout_seconds"])
	}
	if _, ok := contextPayload["research_task_acceptance_criteria"].([]any); !ok {
		t.Fatalf("criteria lost array JSON type: %T", contextPayload["research_task_acceptance_criteria"])
	}
}

func TestResearchDispatchInboxContextOmitsManifestForHistoricalRequest(t *testing.T) {
	encoded, err := encodeResearchDispatchInboxContext(researchrun.DispatchRequest{Run: researchrun.Run{SessionID: "session-1"}, Task: researchrun.Task{ID: "task-1"}}, "request-hash")
	if err != nil {
		t.Fatal(err)
	}
	var contextPayload map[string]any
	if err = json.Unmarshal(encoded, &contextPayload); err != nil {
		t.Fatal(err)
	}
	if _, exists := contextPayload["research_manifest_id"]; exists {
		t.Fatalf("historical context exposed empty manifest: %v", contextPayload)
	}
}

func TestResearchDispatchInboxContextRejectsPartialManifestIdentity(t *testing.T) {
	_, err := encodeResearchDispatchInboxContext(researchrun.DispatchRequest{ManifestID: "manifest-1"}, "request-hash")
	if err == nil {
		t.Fatal("partial manifest identity was accepted")
	}
}
