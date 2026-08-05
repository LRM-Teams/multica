package researchrun

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestEvaluationFeedbackMetadataIsBoundedAndOrdered(t *testing.T) {
	evaluation := EvaluationProposal{
		Passed: false, FactualGrounding: 0.2, Coverage: 0.3, AnalyticalDepth: 0.9,
		SourceQuality: 0.9, ContradictionHandling: 0.9, InstructionAdherence: 0.9, Readability: 0.9,
		DimensionFindings: map[string]string{
			"factual_grounding": strings.Repeat("grounding ", 300),
			"coverage":          strings.Repeat("coverage ", 300),
		},
	}
	for i := 0; i < 80; i++ {
		evaluation.Findings = append(evaluation.Findings, strings.Repeat(fmt.Sprintf("finding-%d ", i), 200))
		evaluation.ReviewedClaimKeys = append(evaluation.ReviewedClaimKeys, fmt.Sprintf("claim-%d", i))
		evaluation.ReviewedSectionIDs = append(evaluation.ReviewedSectionIDs, fmt.Sprintf("section-%d", i))
	}
	raw, err := json.Marshal(evaluation)
	if err != nil {
		t.Fatal(err)
	}
	metadata := evaluationFeedbackMetadata("decision", "report", "reviewer", raw, 0.75)
	failed := metadata["failed_dimensions"].([]map[string]any)
	findings := metadata["findings"].([]string)
	claims := metadata["reviewed_claim_keys"].([]string)
	sections := metadata["reviewed_section_ids"].([]string)
	if len(failed) != 2 || failed[0]["dimension"] != "factual_grounding" || failed[1]["dimension"] != "coverage" {
		t.Fatalf("failed dimensions=%+v", failed)
	}
	if len(findings) != 8 || len(findings[0]) > 1024 || len(claims) != 64 || len(sections) != 64 {
		t.Fatalf("findings=%d first_bytes=%d claims=%d sections=%d", len(findings), len(findings[0]), len(claims), len(sections))
	}
}
