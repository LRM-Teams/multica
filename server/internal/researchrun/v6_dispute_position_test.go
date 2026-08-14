package researchrun

import (
	"errors"
	"testing"
)

func TestDecodeV6DisputePositionSeedRequiresOriginalAgentProvenance(t *testing.T) {
	position := map[string]any{"author_agent_id": "22222222-2222-4222-8222-222222222222", "statement": "The regional condition changes the conclusion.",
		"scope": map[string]any{"region": "EU"}, "claim_refs": []any{map[string]any{"kind": "claim", "key": "claim-1"}}, "evidence_refs": []any{}}
	decoded, err := decodeV6DisputePositionSeed(position)
	if err != nil || decoded.AuthorAgentID == "" || len(decoded.ClaimRefs) != 1 {
		t.Fatalf("position=%+v err=%v", decoded, err)
	}
	delete(position, "author_agent_id")
	if _, err = decodeV6DisputePositionSeed(position); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("err=%v", err)
	}
}
