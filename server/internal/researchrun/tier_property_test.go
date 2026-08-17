package researchrun

import "testing"

func TestV6PromotionAndAssimilationTierProperties(t *testing.T) {
	for _, row := range []struct {
		mode   string
		inputs []V6Tier
		output V6Tier
		valid  bool
	}{
		{"promotion", []V6Tier{V6TierS, V6TierS}, V6TierM, true},
		{"promotion", []V6Tier{V6TierM, V6TierM}, V6TierL, true},
		{"promotion", []V6Tier{V6TierXL, V6TierXL}, V6TierXXL, true},
		{"promotion", []V6Tier{V6TierXXL, V6TierXXL}, V6TierXXL, true},
		{"promotion", []V6Tier{V6TierS, V6TierS}, V6TierL, false},
		{"assimilation", []V6Tier{V6TierL, V6TierS}, V6TierL, true},
		{"assimilation", []V6Tier{V6TierL, V6TierL}, V6TierL, false},
		{"xxl_merge", []V6Tier{V6TierXXL, V6TierXXL}, V6TierXXL, true},
	} {
		refs := make([]V6NodeRef, len(row.inputs))
		for i := range refs {
			refs[i].Tier = row.inputs[i]
		}
		if got := validateV6IntegrationTiers(row.mode, refs, row.output) == nil; got != row.valid {
			t.Fatalf("mode=%s inputs=%v output=%s valid=%v", row.mode, row.inputs, row.output, got)
		}
	}
}
