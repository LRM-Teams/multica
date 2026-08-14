package researchrun

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

type InsightInputKind string
type InsightDerivationRelation string
type InsightSemanticValue string

const (
	InsightInputClaim   InsightInputKind = "claim"
	InsightInputInsight InsightInputKind = "insight"

	InsightRelationIntegrates    InsightDerivationRelation = "integrates"
	InsightRelationExplains      InsightDerivationRelation = "explains"
	InsightRelationConditions    InsightDerivationRelation = "conditions"
	InsightRelationResolves      InsightDerivationRelation = "resolves"
	InsightRelationDistinguishes InsightDerivationRelation = "distinguishes"

	InsightValueNewExplanation      InsightSemanticValue = "new_explanation"
	InsightValueDeduplication       InsightSemanticValue = "deduplication"
	InsightValueConflictResolution  InsightSemanticValue = "conflict_resolution"
	InsightValueHypothesisChange    InsightSemanticValue = "hypothesis_change"
	InsightValueFrontierChange      InsightSemanticValue = "frontier_change"
	InsightValueReportChange        InsightSemanticValue = "report_change"
	InsightValueLosslessCompression InsightSemanticValue = "lossless_compression"
)

const InsightRejectionNoSemanticGain = "no_semantic_gain"

// InsightDerivationInput is an authoritative resolved Artifact Version fact.
// Producer and freshness values come from server ledgers, never Agent prose.
type InsightDerivationInput struct {
	Kind           InsightInputKind
	ArtifactID     string
	VersionID      string
	ContentHash    string
	ProducerTaskID string
	BranchID       string
	InsightLevel   int
	Accepted       bool
	Fresh          bool
}

type InsightDerivationCandidate struct {
	Relation       InsightDerivationRelation
	ScopeHash      string
	Inputs         []InsightDerivationInput
	ObservedValues []InsightSemanticValue
}

type InsightDerivationAdmission struct {
	Accepted        bool
	RejectionReason string
	Level           int
	Fingerprint     string
	InputVersionIDs []string
}

type InsightDerivationEdge struct {
	InputArtifactID string
	InsightID       string
}

type insightDerivationModule struct{}

func (insightDerivationModule) Admit(candidate InsightDerivationCandidate) (InsightDerivationAdmission, error) {
	if !validInsightRelation(candidate.Relation) || !validSHA256(candidate.ScopeHash) {
		return InsightDerivationAdmission{}, fmt.Errorf("%w: invalid Insight relation or scope hash", ErrInvalidContract)
	}
	if len(candidate.Inputs) < 2 || len(candidate.Inputs) > 128 {
		return InsightDerivationAdmission{}, fmt.Errorf("%w: Insight requires 2-128 inputs", ErrInvalidContract)
	}
	if len(candidate.ObservedValues) == 0 {
		return InsightDerivationAdmission{RejectionReason: InsightRejectionNoSemanticGain}, nil
	}
	values := make(map[InsightSemanticValue]struct{}, len(candidate.ObservedValues))
	for _, value := range candidate.ObservedValues {
		if !validInsightSemanticValue(value) {
			return InsightDerivationAdmission{}, fmt.Errorf("%w: unknown Insight semantic value %q", ErrInvalidContract, value)
		}
		values[value] = struct{}{}
	}

	seenArtifacts := make(map[string]struct{}, len(candidate.Inputs))
	taskOrigins := make(map[string]struct{}, len(candidate.Inputs))
	branchOrigins := make(map[string]struct{}, len(candidate.Inputs))
	parts := make([]string, 0, len(candidate.Inputs)+len(values)+2)
	versionIDs := make([]string, 0, len(candidate.Inputs))
	maxLevel := 0
	for _, input := range candidate.Inputs {
		if input.ArtifactID == "" || input.VersionID == "" || !validSHA256(input.ContentHash) {
			return InsightDerivationAdmission{}, fmt.Errorf("%w: Insight input identity is incomplete", ErrInvalidContract)
		}
		if _, duplicate := seenArtifacts[input.ArtifactID]; duplicate {
			return InsightDerivationAdmission{}, fmt.Errorf("%w: duplicate Insight input %s", ErrInvalidContract, input.ArtifactID)
		}
		seenArtifacts[input.ArtifactID] = struct{}{}
		if !input.Accepted || !input.Fresh {
			return InsightDerivationAdmission{}, fmt.Errorf("%w: Insight input %s is not accepted and fresh", ErrInvalidTransition, input.ArtifactID)
		}
		switch input.Kind {
		case InsightInputClaim:
			if input.InsightLevel != 0 {
				return InsightDerivationAdmission{}, fmt.Errorf("%w: Claim input has non-zero Insight level", ErrInvalidContract)
			}
		case InsightInputInsight:
			if input.InsightLevel < 1 {
				return InsightDerivationAdmission{}, fmt.Errorf("%w: Insight input has invalid level", ErrInvalidContract)
			}
			if input.InsightLevel > maxLevel {
				maxLevel = input.InsightLevel
			}
		default:
			return InsightDerivationAdmission{}, fmt.Errorf("%w: unknown Insight input kind %q", ErrInvalidContract, input.Kind)
		}
		hasOrigin := false
		if input.ProducerTaskID != "" {
			taskOrigins[input.ProducerTaskID] = struct{}{}
			hasOrigin = true
		}
		if input.BranchID != "" {
			branchOrigins[input.BranchID] = struct{}{}
			hasOrigin = true
		}
		if !hasOrigin {
			return InsightDerivationAdmission{}, fmt.Errorf("%w: Insight input %s has no Task or Branch origin", ErrInvalidContract, input.ArtifactID)
		}
		parts = append(parts, fmt.Sprintf("input=%s:%s:%s", input.Kind, input.VersionID, input.ContentHash))
		versionIDs = append(versionIDs, input.VersionID)
	}
	if len(taskOrigins) < 2 && len(branchOrigins) < 2 {
		return InsightDerivationAdmission{RejectionReason: InsightRejectionNoSemanticGain}, nil
	}
	sort.Strings(parts)
	sort.Strings(versionIDs)
	parts = append(parts, "relation="+string(candidate.Relation), "scope="+candidate.ScopeHash)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return InsightDerivationAdmission{
		Accepted:        true,
		Level:           maxLevel + 1,
		Fingerprint:     "sha256:" + hex.EncodeToString(sum[:]),
		InputVersionIDs: versionIDs,
	}, nil
}

func (insightDerivationModule) ValidateDAG(edges []InsightDerivationEdge) error {
	adjacency := make(map[string][]string)
	seenEdges := make(map[string]struct{}, len(edges))
	for _, edge := range edges {
		if edge.InputArtifactID == "" || edge.InsightID == "" || edge.InputArtifactID == edge.InsightID {
			return fmt.Errorf("%w: invalid Insight Derivation edge", ErrInvalidContract)
		}
		key := edge.InputArtifactID + "\x00" + edge.InsightID
		if _, duplicate := seenEdges[key]; duplicate {
			return fmt.Errorf("%w: duplicate Insight Derivation edge", ErrInvalidContract)
		}
		seenEdges[key] = struct{}{}
		adjacency[edge.InputArtifactID] = append(adjacency[edge.InputArtifactID], edge.InsightID)
	}
	state := make(map[string]uint8)
	var visit func(string) error
	visit = func(id string) error {
		if state[id] == 1 {
			return fmt.Errorf("%w: Insight Derivation cycle at %s", ErrInvalidContract, id)
		}
		if state[id] == 2 {
			return nil
		}
		state[id] = 1
		for _, next := range adjacency[id] {
			if err := visit(next); err != nil {
				return err
			}
		}
		state[id] = 2
		return nil
	}
	for id := range adjacency {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

func (m insightDerivationModule) PropagateStale(edges []InsightDerivationEdge, invalidatedArtifactIDs []string) ([]string, error) {
	if err := m.ValidateDAG(edges); err != nil {
		return nil, err
	}
	adjacency := make(map[string][]string)
	insights := make(map[string]struct{})
	for _, edge := range edges {
		adjacency[edge.InputArtifactID] = append(adjacency[edge.InputArtifactID], edge.InsightID)
		insights[edge.InsightID] = struct{}{}
	}
	queue := append([]string(nil), invalidatedArtifactIDs...)
	seen := make(map[string]struct{}, len(queue))
	stale := make(map[string]struct{})
	for _, id := range queue {
		if id == "" {
			return nil, fmt.Errorf("%w: invalidated Artifact ID is empty", ErrInvalidContract)
		}
		seen[id] = struct{}{}
		if _, isInsight := insights[id]; isInsight {
			stale[id] = struct{}{}
		}
	}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, dependent := range adjacency[current] {
			stale[dependent] = struct{}{}
			if _, visited := seen[dependent]; visited {
				continue
			}
			seen[dependent] = struct{}{}
			queue = append(queue, dependent)
		}
	}
	out := make([]string, 0, len(stale))
	for id := range stale {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

func validInsightRelation(value InsightDerivationRelation) bool {
	switch value {
	case InsightRelationIntegrates, InsightRelationExplains, InsightRelationConditions, InsightRelationResolves, InsightRelationDistinguishes:
		return true
	default:
		return false
	}
}

func validInsightSemanticValue(value InsightSemanticValue) bool {
	switch value {
	case InsightValueNewExplanation, InsightValueDeduplication, InsightValueConflictResolution,
		InsightValueHypothesisChange, InsightValueFrontierChange, InsightValueReportChange,
		InsightValueLosslessCompression:
		return true
	default:
		return false
	}
}

func validSHA256(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	digest := strings.TrimPrefix(value, "sha256:")
	if digest != strings.ToLower(digest) {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}
