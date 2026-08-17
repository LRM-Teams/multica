package handler

import (
	"encoding/json"
	"testing"

	"github.com/multica-ai/multica/server/internal/researchrun"
)

func TestResearchV6DispatchContextBindsEntireAttemptIdentity(t *testing.T) {
	request := researchrun.DispatchRequest{
		Run:          researchrun.Run{SessionID: "00000000-0000-4000-8000-000000000003"},
		WorkItemID:   "00000000-0000-4000-8000-000000000202",
		AttemptID:    "00000000-0000-4000-8000-000000000204",
		ManifestID:   "00000000-0000-4000-8000-000000000201",
		ManifestHash: "sha256:3333333333333333333333333333333333333333333333333333333333333333",
	}
	raw, err := encodeResearchDispatchInboxContext(request, "ignored-for-v6")
	if err != nil {
		t.Fatal(err)
	}
	var got researchV6InboxContext
	if err = json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != "research_run_work_item" || got.RunID != request.Run.SessionID || got.WorkItemID != request.WorkItemID ||
		got.AttemptID != request.AttemptID || got.ManifestID != request.ManifestID || got.ManifestHash != request.ManifestHash {
		t.Fatalf("context=%+v", got)
	}
}
