package researchrun

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestResearchMessageArtifactContentCanonicalizesMetaAndBindsTarget(t *testing.T) {
	first, err := ArtifactContentHash(ArtifactKindResearchMessage, researchMessageArtifactContent(
		"agent", "agent-1", "agent-2", "", "body", "chat", []byte(`{"b":2,"a":1}`),
	))
	if err != nil {
		t.Fatal(err)
	}
	second, err := ArtifactContentHash(ArtifactKindResearchMessage, researchMessageArtifactContent(
		"agent", "agent-1", "agent-2", "", "body", "chat", []byte(`{"a":1,"b":2}`),
	))
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("canonical message hashes differ: %q != %q", first, second)
	}
	changed, err := ArtifactContentHash(ArtifactKindResearchMessage, researchMessageArtifactContent(
		"agent", "agent-1", "agent-3", "", "body", "chat", []byte(`{"a":1,"b":2}`),
	))
	if err != nil {
		t.Fatal(err)
	}
	if first == changed {
		t.Fatal("different target Agents must not share a message hash")
	}
	lineage, err := ArtifactContentHash(ArtifactKindResearchMessage, researchMessageArtifactContent(
		"agent", "agent-1", "agent-2", "event-1", "body", "chat", []byte(`{"a":1,"b":2}`),
	))
	if err != nil {
		t.Fatal(err)
	}
	if first == lineage {
		t.Fatal("different Run Event lineage must not share a message hash")
	}
}

func TestParseResearchMessageMatchDecisionRefsRejectsWrongOwnerAndDuplicateDecision(t *testing.T) {
	messageID := uuid.NewString()
	nodeID := uuid.NewString()
	wrongOwner, _ := json.Marshal(map[string]any{
		"utterance_id": uuid.NewString(), "matched_node_ids": []string{nodeID},
	})
	if _, err := parseResearchMessageMatchDecisionRefs(messageID, wrongOwner); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("wrong owner error=%v", err)
	}
	duplicate, _ := json.Marshal(map[string]any{
		"utterance_id": messageID,
		"decisions":    []map[string]any{{"node_id": nodeID}, {"node_id": nodeID}},
	})
	if _, err := parseResearchMessageMatchDecisionRefs(messageID, duplicate); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("duplicate decision error=%v", err)
	}
}
