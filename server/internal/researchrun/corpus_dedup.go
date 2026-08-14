package researchrun

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
)

type CorpusDedupCandidate struct {
	ID                 string
	CanonicalURL       string
	ContentHash        string
	IndependenceFamily string
	DiscoveryOrder     int
}

type CorpusDedupDecision struct {
	CandidateID                 string
	Disposition                 string
	Rule                        string
	Reason                      string
	DuplicateCluster            string
	CanonicalCandidateID        string
	EffectiveIndependenceFamily string
}

// ComputeCorpusDedupDecisions deterministically collapses exact URL and content
// duplicates without treating different documents from one publisher family as
// independent support. A canonical URL returning different content is held for
// review instead of silently choosing one version.
func ComputeCorpusDedupDecisions(candidates []CorpusDedupCandidate) ([]CorpusDedupDecision, error) {
	if len(candidates) == 0 || len(candidates) > maxResultItems {
		return nil, fmt.Errorf("%w: Corpus dedup batch is empty or exceeds its limit", ErrInvalidContract)
	}
	byID := make(map[string]CorpusDedupCandidate, len(candidates))
	byURL := map[string][]string{}
	byHash := map[string][]string{}
	parent := map[string]string{}
	for _, candidate := range candidates {
		if !validCorpusDedupToken(candidate.ID, 512) || !validCorpusDedupToken(candidate.IndependenceFamily, 512) || !validCorpusDedupHash(candidate.ContentHash) || candidate.DiscoveryOrder < 0 {
			return nil, fmt.Errorf("%w: Corpus dedup candidate metadata is invalid", ErrInvalidContract)
		}
		canonical, err := CanonicalURL(candidate.CanonicalURL)
		if err != nil || canonical != candidate.CanonicalURL {
			return nil, fmt.Errorf("%w: Corpus dedup URL is not canonical", ErrInvalidContract)
		}
		if _, duplicate := byID[candidate.ID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate Corpus candidate ID %q", ErrInvalidContract, candidate.ID)
		}
		byID[candidate.ID] = candidate
		byURL[candidate.CanonicalURL] = append(byURL[candidate.CanonicalURL], candidate.ID)
		byHash[candidate.ContentHash] = append(byHash[candidate.ContentHash], candidate.ID)
		parent[candidate.ID] = candidate.ID
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
		first := find(ids[0])
		for _, id := range ids[1:] {
			other := find(id)
			if first != other {
				parent[other] = first
			}
		}
	}
	for _, ids := range byURL {
		union(ids)
	}
	for _, ids := range byHash {
		union(ids)
	}

	groups := map[string][]CorpusDedupCandidate{}
	for _, candidate := range candidates {
		groups[find(candidate.ID)] = append(groups[find(candidate.ID)], candidate)
	}
	decisions := make([]CorpusDedupDecision, 0, len(candidates))
	for _, group := range groups {
		sort.Slice(group, func(i, j int) bool {
			if group[i].DiscoveryOrder == group[j].DiscoveryOrder {
				return group[i].ID < group[j].ID
			}
			return group[i].DiscoveryOrder < group[j].DiscoveryOrder
		})
		primary := group[0]
		ids := make([]string, len(group))
		urlHashes := map[string]map[string]bool{}
		families := map[string]bool{}
		for i, candidate := range group {
			ids[i] = candidate.ID
			if urlHashes[candidate.CanonicalURL] == nil {
				urlHashes[candidate.CanonicalURL] = map[string]bool{}
			}
			urlHashes[candidate.CanonicalURL][candidate.ContentHash] = true
			families[candidate.IndependenceFamily] = true
		}
		sort.Strings(ids)
		cluster := corpusDedupStableID("duplicate", ids)
		effectiveFamily := primary.IndependenceFamily
		if len(group) > 1 && len(families) > 1 {
			effectiveFamily = corpusDedupStableID("mirror-family", ids)
		}
		conflict := false
		for _, hashes := range urlHashes {
			if len(hashes) > 1 {
				conflict = true
			}
		}
		for _, candidate := range group {
			decision := CorpusDedupDecision{CandidateID: candidate.ID, DuplicateCluster: cluster, CanonicalCandidateID: primary.ID, EffectiveIndependenceFamily: effectiveFamily}
			switch {
			case conflict:
				decision.Disposition, decision.Rule, decision.Reason = "review", "canonical_url_content_conflict", "one canonical URL produced multiple content hashes"
			case candidate.ID == primary.ID:
				decision.Disposition, decision.Rule, decision.Reason = "include", "canonical_candidate", "first stable candidate retained for the duplicate cluster"
			case candidate.ContentHash == primary.ContentHash && candidate.CanonicalURL != primary.CanonicalURL:
				decision.Disposition, decision.Rule, decision.Reason = "duplicate", "content_mirror", "identical content was discovered at another canonical URL"
			default:
				decision.Disposition, decision.Rule, decision.Reason = "duplicate", "canonical_url_duplicate", "the canonical URL is already represented in this batch"
			}
			decisions = append(decisions, decision)
		}
	}
	sort.Slice(decisions, func(i, j int) bool { return decisions[i].CandidateID < decisions[j].CandidateID })
	return decisions, nil
}

func corpusDedupStableID(namespace string, ids []string) string {
	hash := sha256.Sum256([]byte(namespace + "\x00" + strings.Join(ids, "\x00")))
	return fmt.Sprintf("sha256:%x", hash)
}

func validCorpusDedupToken(value string, limit int) bool {
	return strings.TrimSpace(value) == value && value != "" && len(value) <= limit
}

func validCorpusDedupHash(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, char := range value[len("sha256:"):] {
		if char < '0' || char > '9' && char < 'a' || char > 'f' {
			return false
		}
	}
	return true
}
