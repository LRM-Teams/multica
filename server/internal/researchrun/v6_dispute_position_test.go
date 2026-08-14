package researchrun

import (
	"errors"
	"testing"
)

func TestDecodeV6DisputePositionSeedRequiresOriginalAgentProvenance(t *testing.T) {
	position := map[string]any{"author_agent_id": "22222222-2222-4222-8222-222222222222", "statement": "The regional condition changes the conclusion.",
		"scope": map[string]any{"region": "EU"}, "claim_refs": []any{map[string]any{"kind": "claim", "key": "claim-1"}}, "evidence_refs": []any{},
		"conflict_basis": map[string]any{"detection_mode": "agent_candidate", "kind": "scope", "reason": "The geographic populations differ.", "fact": nil}}
	decoded, err := decodeV6DisputePositionSeed(position)
	if err != nil || decoded.AuthorAgentID == "" || len(decoded.ClaimRefs) != 1 {
		t.Fatalf("position=%+v err=%v", decoded, err)
	}
	delete(position, "author_agent_id")
	if _, err = decodeV6DisputePositionSeed(position); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("err=%v", err)
	}
}

func TestDecodeV6DisputePositionSeedRequiresCompleteDeterministicBasis(t *testing.T) {
	position := map[string]any{"author_agent_id": "22222222-2222-4222-8222-222222222222", "statement": "The claims use incompatible units.",
		"scope": map[string]any{"region": "EU"}, "claim_refs": []any{map[string]any{"kind": "claim", "key": "claim-1"}}, "evidence_refs": []any{},
		"conflict_basis": map[string]any{"detection_mode": "deterministic", "kind": "unit", "reason": "USD and EUR were compared without conversion.", "fact": map[string]any{
			"entity_key": "company", "metric_key": "revenue", "time_window_key": "2025", "scope_hash": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"proposition_hash": "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "polarity": "affirms", "unit_key": "USD", "version_key": "v1", "source_snapshot_id": "", "citation_meaning_hash": ""}}}
	if _, err := decodeV6DisputePositionSeed(position); err != nil {
		t.Fatal(err)
	}
	delete(position["conflict_basis"].(map[string]any)["fact"].(map[string]any), "metric_key")
	if _, err := decodeV6DisputePositionSeed(position); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("err=%v", err)
	}
}
