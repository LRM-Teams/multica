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
	AuthorAgentID string          `json:"author_agent_id"`
	Statement     string          `json:"statement"`
	Scope         map[string]any  `json:"scope"`
	ClaimRefs     []V6EntityRef   `json:"claim_refs"`
	EvidenceRefs  []V6EntityRef   `json:"evidence_refs"`
	ConflictBasis V6ConflictBasis `json:"conflict_basis"`
}

type V6ConflictFact struct {
	EntityKey           string           `json:"entity_key"`
	MetricKey           string           `json:"metric_key"`
	TimeWindowKey       string           `json:"time_window_key"`
	ScopeHash           string           `json:"scope_hash"`
	PropositionHash     string           `json:"proposition_hash"`
	Polarity            ConflictPolarity `json:"polarity"`
	UnitKey             string           `json:"unit_key"`
	VersionKey          string           `json:"version_key"`
	SourceSnapshotID    string           `json:"source_snapshot_id"`
	CitationMeaningHash string           `json:"citation_meaning_hash"`
}

type V6ConflictBasis struct {
	DetectionMode string          `json:"detection_mode"`
	Kind          DisputeKind     `json:"kind"`
	Reason        string          `json:"reason"`
	Fact          *V6ConflictFact `json:"fact"`
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
	if err = requireV6Fields("dispute position", shape, "author_agent_id", "statement", "scope", "claim_refs", "evidence_refs", "conflict_basis"); err != nil {
		return V6DisputePositionSeed{}, err
	}
	var basisShape map[string]json.RawMessage
	if err = json.Unmarshal(shape["conflict_basis"], &basisShape); err != nil {
		return V6DisputePositionSeed{}, fmt.Errorf("%w: conflict_basis must be an object", ErrInvalidResult)
	}
	if err = requireV6Fields("conflict_basis", basisShape, "detection_mode", "kind", "reason"); err != nil {
		return V6DisputePositionSeed{}, err
	}
	if _, ok := basisShape["fact"]; !ok {
		return V6DisputePositionSeed{}, fmt.Errorf("%w: conflict_basis missing required field fact", ErrInvalidResult)
	}
	if string(basisShape["fact"]) != "null" {
		var factShape map[string]json.RawMessage
		if err = json.Unmarshal(basisShape["fact"], &factShape); err != nil {
			return V6DisputePositionSeed{}, fmt.Errorf("%w: conflict fact must be an object", ErrInvalidResult)
		}
		if err = requireV6Fields("conflict_fact", factShape, "entity_key", "metric_key", "time_window_key", "scope_hash", "proposition_hash", "polarity", "unit_key", "version_key", "source_snapshot_id", "citation_meaning_hash"); err != nil {
			return V6DisputePositionSeed{}, err
		}
	}
	var position V6DisputePositionSeed
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
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
	for _, ref := range position.EvidenceRefs {
		if ref.Kind != "claim" && ref.Kind != "source" {
			return V6DisputePositionSeed{}, fmt.Errorf("%w: dispute position evidence_refs must reference Claims or Source Snapshots", ErrInvalidResult)
		}
	}
	if err = validateV6ConflictBasis(position.ConflictBasis, len(position.ClaimRefs)); err != nil {
		return V6DisputePositionSeed{}, err
	}
	return position, nil
}

func validateV6ConflictBasis(basis V6ConflictBasis, claimCount int) error {
	if !validDisputeKind(basis.Kind) || strings.TrimSpace(basis.Reason) == "" || len(basis.Reason) > 32768 {
		return fmt.Errorf("%w: dispute position conflict basis is invalid", ErrInvalidResult)
	}
	switch basis.DetectionMode {
	case "deterministic":
		if basis.Fact == nil || claimCount != 1 || (basis.Kind != DisputeKindLogical && basis.Kind != DisputeKindSourceInterpretation && basis.Kind != DisputeKindVersion && basis.Kind != DisputeKindUnit) {
			return fmt.Errorf("%w: deterministic conflict basis requires one Claim and a deterministic kind", ErrInvalidResult)
		}
		fact := basis.Fact
		if fact.EntityKey == "" || fact.MetricKey == "" || fact.TimeWindowKey == "" || !validPrefixedSHA256(fact.ScopeHash) || !validPrefixedSHA256(fact.PropositionHash) || (fact.Polarity != ConflictPolarityAffirms && fact.Polarity != ConflictPolarityDenies) {
			return fmt.Errorf("%w: deterministic conflict fact is incomplete", ErrInvalidResult)
		}
		if fact.CitationMeaningHash != "" && !validPrefixedSHA256(fact.CitationMeaningHash) {
			return fmt.Errorf("%w: citation meaning hash is invalid", ErrInvalidResult)
		}
		if fact.SourceSnapshotID != "" {
			if _, err := uuid.Parse(fact.SourceSnapshotID); err != nil {
				return fmt.Errorf("%w: conflict Source Snapshot is invalid", ErrInvalidResult)
			}
		}
	case "agent_candidate":
		if basis.Fact != nil || (basis.Kind != DisputeKindScope && basis.Kind != DisputeKindMethod && basis.Kind != DisputeKindSemantic) {
			return fmt.Errorf("%w: Agent conflict candidate must use scope, method, or semantic kind without a deterministic fact", ErrInvalidResult)
		}
	default:
		return fmt.Errorf("%w: unsupported conflict detection mode", ErrInvalidResult)
	}
	return nil
}
