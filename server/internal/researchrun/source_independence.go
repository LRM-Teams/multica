package researchrun

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const SourceIndependencePolicyVersionV1 = "research-source-independence-v1"

type SourceIndependenceFact struct {
	Kind     string
	Value    string
	Verified bool
	Locator  string
}

type SourceIndependenceCandidate struct {
	CandidateID    string
	DiscoveryOrder int
	Facts          []SourceIndependenceFact
}

type SourceIndependenceDecision struct {
	CandidateID               string
	FamilyFingerprint         string
	CanonicalCandidateID      string
	IdentityVerified          bool
	IndependentRepresentative bool
	DependencyKeys            []string
	Reason                    string
	DecisionFingerprint       string
}

// ClassifySourceIndependence conservatively closes dependency relationships
// across publisher, owner, dataset, and syndication facts. A family contributes
// at most one independent representative, and only when that representative
// has located, verified publisher and owner identity facts.
func ClassifySourceIndependence(policyVersion string, candidates []SourceIndependenceCandidate) ([]SourceIndependenceDecision, error) {
	if policyVersion != SourceIndependencePolicyVersionV1 || len(candidates) == 0 || len(candidates) > maxResultItems {
		return nil, fmt.Errorf("%w: Source Independence policy or batch is invalid", ErrInvalidContract)
	}
	normalized := make([]SourceIndependenceCandidate, 0, len(candidates))
	byID := make(map[string]struct{}, len(candidates))
	parent := make(map[string]string, len(candidates))
	byDependency := map[string][]string{}
	for _, candidate := range candidates {
		item, err := normalizeSourceIndependenceCandidate(candidate)
		if err != nil {
			return nil, err
		}
		if _, duplicate := byID[item.CandidateID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate Source Independence candidate", ErrInvalidContract)
		}
		byID[item.CandidateID] = struct{}{}
		parent[item.CandidateID] = item.CandidateID
		normalized = append(normalized, item)
		for _, fact := range item.Facts {
			key := sourceIndependenceDependencyKey(fact)
			byDependency[key] = append(byDependency[key], item.CandidateID)
		}
	}
	var find func(string) string
	find = func(id string) string {
		if parent[id] != id {
			parent[id] = find(parent[id])
		}
		return parent[id]
	}
	union := func(ids []string) {
		if len(ids) < 2 {
			return
		}
		root := find(ids[0])
		for _, id := range ids[1:] {
			other := find(id)
			if root != other {
				if other < root {
					root, other = other, root
				}
				parent[other] = root
			}
		}
	}
	for _, ids := range byDependency {
		union(ids)
	}
	groups := map[string][]SourceIndependenceCandidate{}
	for _, candidate := range normalized {
		root := find(candidate.CandidateID)
		groups[root] = append(groups[root], candidate)
	}
	decisions := make([]SourceIndependenceDecision, 0, len(candidates))
	for _, group := range groups {
		sort.Slice(group, func(i, j int) bool {
			leftVerified, rightVerified := sourceIndependenceIdentityVerified(group[i]), sourceIndependenceIdentityVerified(group[j])
			if leftVerified != rightVerified {
				return leftVerified
			}
			if group[i].DiscoveryOrder != group[j].DiscoveryOrder {
				return group[i].DiscoveryOrder < group[j].DiscoveryOrder
			}
			return group[i].CandidateID < group[j].CandidateID
		})
		canonical := group[0]
		dependencySet := map[string]struct{}{}
		for _, candidate := range group {
			for _, fact := range candidate.Facts {
				dependencySet[sourceIndependenceDependencyKey(fact)] = struct{}{}
			}
		}
		dependencyKeys := make([]string, 0, len(dependencySet))
		for key := range dependencySet {
			dependencyKeys = append(dependencyKeys, key)
		}
		sort.Strings(dependencyKeys)
		fingerprintInput := strings.Join(dependencyKeys, "\x00")
		fingerprint := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(SourceIndependencePolicyVersionV1+"\x00"+fingerprintInput)))
		for _, candidate := range group {
			verified := sourceIndependenceIdentityVerified(candidate)
			decision := SourceIndependenceDecision{
				CandidateID: candidate.CandidateID, FamilyFingerprint: fingerprint,
				CanonicalCandidateID: canonical.CandidateID, IdentityVerified: verified,
				IndependentRepresentative: candidate.CandidateID == canonical.CandidateID && verified,
				DependencyKeys:            append([]string(nil), dependencyKeys...),
			}
			switch {
			case !verified:
				decision.Reason = "publisher and owner identity are not both verified"
			case candidate.CandidateID != canonical.CandidateID:
				decision.Reason = "source shares a publisher, owner, dataset, or syndication dependency with the family representative"
			default:
				decision.Reason = "verified source is the sole independent representative of its dependency family"
			}
			encoded, err := json.Marshal(struct {
				PolicyVersion string
				Candidate     SourceIndependenceCandidate
				Decision      SourceIndependenceDecision
			}{PolicyVersion: policyVersion, Candidate: candidate, Decision: decision})
			if err != nil {
				return nil, err
			}
			decision.DecisionFingerprint = fmt.Sprintf("sha256:%x", sha256.Sum256(encoded))
			decisions = append(decisions, decision)
		}
	}
	sort.Slice(decisions, func(i, j int) bool { return decisions[i].CandidateID < decisions[j].CandidateID })
	return decisions, nil
}

func normalizeSourceIndependenceCandidate(candidate SourceIndependenceCandidate) (SourceIndependenceCandidate, error) {
	if !validSourceIndependenceToken(candidate.CandidateID, 512) || candidate.DiscoveryOrder < 0 || len(candidate.Facts) == 0 || len(candidate.Facts) > 64 {
		return SourceIndependenceCandidate{}, fmt.Errorf("%w: Source Independence candidate is invalid", ErrInvalidContract)
	}
	normalized := candidate
	normalized.Facts = append([]SourceIndependenceFact(nil), candidate.Facts...)
	sort.Slice(normalized.Facts, func(i, j int) bool {
		if normalized.Facts[i].Kind != normalized.Facts[j].Kind {
			return normalized.Facts[i].Kind < normalized.Facts[j].Kind
		}
		return normalized.Facts[i].Value < normalized.Facts[j].Value
	})
	seen := map[string]struct{}{}
	for _, fact := range normalized.Facts {
		if !validSourceIndependenceFact(fact) {
			return SourceIndependenceCandidate{}, fmt.Errorf("%w: Source Independence fact is invalid", ErrInvalidContract)
		}
		key := sourceIndependenceDependencyKey(fact)
		if _, duplicate := seen[key]; duplicate {
			return SourceIndependenceCandidate{}, fmt.Errorf("%w: duplicate Source Independence fact", ErrInvalidContract)
		}
		seen[key] = struct{}{}
	}
	return normalized, nil
}

func validSourceIndependenceFact(fact SourceIndependenceFact) bool {
	if !validSourceIndependenceToken(fact.Value, 512) || fact.Value != strings.ToLower(fact.Value) || !validSourceIndependenceToken(fact.Locator, 2048) {
		return false
	}
	switch fact.Kind {
	case "publisher", "owner", "dataset", "syndication":
		return true
	default:
		return false
	}
}

func sourceIndependenceDependencyKey(fact SourceIndependenceFact) string {
	return fact.Kind + ":" + fact.Value
}

func sourceIndependenceIdentityVerified(candidate SourceIndependenceCandidate) bool {
	publisher, owner := false, false
	for _, fact := range candidate.Facts {
		if fact.Verified && fact.Kind == "publisher" {
			publisher = true
		}
		if fact.Verified && fact.Kind == "owner" {
			owner = true
		}
	}
	return publisher && owner
}

func validSourceIndependenceToken(value string, limit int) bool {
	return value != "" && len(value) <= limit && strings.TrimSpace(value) == value
}
