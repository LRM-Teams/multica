package researchrun

import (
	"errors"
	"fmt"
	"sort"
)

type V6Tier string

const (
	V6TierS   V6Tier = "S"
	V6TierM   V6Tier = "M"
	V6TierL   V6Tier = "L"
	V6TierXL  V6Tier = "XL"
	V6TierXXL V6Tier = "XXL"
)

var (
	ErrV6InvalidTierTransition = errors.New("invalid research V6 tier transition")
	ErrV6NodeAlreadyAbsorbed   = errors.New("research V6 node already has a successor")
)

type V6NodeRef struct {
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	VersionID   string `json:"version_id"`
	Tier        V6Tier `json:"tier"`
	ContentHash string `json:"content_hash"`
}

func v6InputSetHash(inputs []V6NodeRef) string {
	copyOf := append([]V6NodeRef(nil), inputs...)
	sort.Slice(copyOf, func(i, j int) bool { return copyOf[i].VersionID < copyOf[j].VersionID })
	items := make([]map[string]any, len(copyOf))
	for i, input := range copyOf {
		items[i] = map[string]any{"version_id": input.VersionID, "tier": input.Tier, "content_hash": input.ContentHash}
	}
	raw, err := marshalV6CanonicalJSON(items)
	if err != nil {
		return ""
	}
	return ArtifactContentHashFromCanonicalJSON(raw)
}

func validateV6IntegrationTiers(mode string, inputs []V6NodeRef, output V6Tier) error {
	if len(inputs) < 2 {
		return fmt.Errorf("%w: at least two inputs are required", ErrV6InvalidTierTransition)
	}
	rank := map[V6Tier]int{V6TierS: 0, V6TierM: 1, V6TierL: 2, V6TierXL: 3, V6TierXXL: 4}
	outputRank, ok := rank[output]
	if !ok || output == V6TierS {
		return ErrV6InvalidTierTransition
	}
	sameOutput, maximum, allSame := 0, -1, true
	first := rank[inputs[0].Tier]
	for _, input := range inputs {
		r, exists := rank[input.Tier]
		if !exists {
			return ErrV6InvalidTierTransition
		}
		maximum = max(maximum, r)
		if r == outputRank {
			sameOutput++
		}
		allSame = allSame && r == first
	}
	switch mode {
	case "promotion":
		if !allSame || len(inputs) < 2 || outputRank != min(first+1, rank[V6TierXXL]) {
			return ErrV6InvalidTierTransition
		}
		if first == rank[V6TierXXL] && output != V6TierXXL {
			return ErrV6InvalidTierTransition
		}
	case "assimilation":
		if maximum != outputRank || sameOutput != 1 {
			return ErrV6InvalidTierTransition
		}
	case "xxl_merge":
		if output != V6TierXXL || maximum != rank[V6TierXXL] || sameOutput < 2 {
			return ErrV6InvalidTierTransition
		}
	default:
		return ErrV6InvalidTierTransition
	}
	return nil
}
