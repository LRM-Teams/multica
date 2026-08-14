package researchrun

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
)

type V6DisputePositionSeed struct {
	AuthorAgentID string         `json:"author_agent_id"`
	Statement     string         `json:"statement"`
	Scope         map[string]any `json:"scope"`
	ClaimRefs     []V6EntityRef  `json:"claim_refs"`
	EvidenceRefs  []V6EntityRef  `json:"evidence_refs"`
}

func decodeV6DisputePositionSeed(value map[string]any) (V6DisputePositionSeed, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return V6DisputePositionSeed{}, err
	}
	var shape map[string]json.RawMessage
	if err = json.Unmarshal(raw, &shape); err != nil {
		return V6DisputePositionSeed{}, err
	}
	if err = requireV6Fields("dispute position", shape, "author_agent_id", "statement", "scope", "claim_refs", "evidence_refs"); err != nil {
		return V6DisputePositionSeed{}, err
	}
	var position V6DisputePositionSeed
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err = decoder.Decode(&position); err != nil {
		return V6DisputePositionSeed{}, fmt.Errorf("%w: decode dispute position: %v", ErrInvalidResult, err)
	}
	if err = decoder.Decode(&struct{}{}); err != io.EOF {
		return V6DisputePositionSeed{}, fmt.Errorf("%w: dispute position has trailing JSON", ErrInvalidResult)
	}
	if _, err = uuid.Parse(position.AuthorAgentID); err != nil || strings.TrimSpace(position.Statement) == "" || len(position.Statement) > 32768 || position.Scope == nil || position.ClaimRefs == nil || position.EvidenceRefs == nil {
		return V6DisputePositionSeed{}, fmt.Errorf("%w: dispute position identity or content is invalid", ErrInvalidResult)
	}
	for _, ref := range append(append([]V6EntityRef{}, position.ClaimRefs...), position.EvidenceRefs...) {
		if err = validateV6Ref("dispute position reference", ref); err != nil {
			return V6DisputePositionSeed{}, err
		}
	}
	for _, ref := range position.ClaimRefs {
		if ref.Kind != "claim" {
			return V6DisputePositionSeed{}, fmt.Errorf("%w: dispute position claim_refs must reference Claims", ErrInvalidResult)
		}
	}
	return position, nil
}
