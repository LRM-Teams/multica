package researchrun

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestV5EvaluationNormalizesStructuredDefects(t *testing.T) {
	result := validV5EvaluationResult(false)
	result.Findings = nil
	raw, err := json.Marshal(ResultEnvelope{
		SchemaVersion: 5, ClientRequestID: "v5-evaluation", Summary: "reviewed", Confidence: 0.9,
		Evaluation: &result,
	})
	if err != nil {
		t.Fatal(err)
	}
	decoded, _, err := DecodeAndValidateResultForVersion(OrchestratorVersionV5, raw, Task{
		Kind: TaskKindQualityGate, RequiredCapability: "validator", ExpectedResult: "research_quality_evaluation_v5",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Evaluation.Findings) != 1 || decoded.Evaluation.Findings[0] != result.Defects[0].Problem {
		t.Fatalf("findings=%#v", decoded.Evaluation.Findings)
	}
}

func TestV5EvaluationRejectsUnaddressedOrConflictingDefects(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*EvaluationProposal)
		want   string
	}{
		{name: "failed without defect", mutate: func(e *EvaluationProposal) { e.Defects = nil }, want: "requires a blocking defect"},
		{name: "unaddressed", mutate: func(e *EvaluationProposal) { e.Defects[0].ClaimKeys = nil; e.Defects[0].SectionIDs = nil }, want: "must target"},
		{name: "conflicting summaries", mutate: func(e *EvaluationProposal) { e.Findings = []string{"different reviewer story"} }, want: "exactly match"},
		{name: "passed with blocking", mutate: func(e *EvaluationProposal) { e.Passed = true }, want: "cannot contain defects"},
		{name: "passed with advisory", mutate: func(e *EvaluationProposal) { e.Passed = true; e.Defects[0].Severity = evaluationDefectAdvisory }, want: "cannot contain defects"},
		{name: "too many defects", mutate: func(e *EvaluationProposal) { e.Defects = make([]EvaluationDefect, maxEvaluationDefects+1) }, want: "defects exceed"},
		{name: "defect text too large", mutate: func(e *EvaluationProposal) {
			e.Defects[0].Problem = strings.Repeat("x", maxEvaluationDefectTextBytes+1)
		}, want: "exceeds 1024 bytes"},
		{name: "too many targets", mutate: func(e *EvaluationProposal) { e.Defects[0].ClaimKeys = make([]string, maxEvaluationDefectTargets+1) }, want: "exceeds 64 items"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evaluation := validV5EvaluationResult(false)
			test.mutate(&evaluation)
			raw, err := json.Marshal(ResultEnvelope{
				SchemaVersion: 5, ClientRequestID: "v5-invalid-" + strings.ReplaceAll(test.name, " ", "-"), Summary: "reviewed", Confidence: 0.9,
				Evaluation: &evaluation,
			})
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = DecodeAndValidateResultForVersion(OrchestratorVersionV5, raw, Task{
				Kind: TaskKindQualityGate, RequiredCapability: "validator", ExpectedResult: "research_quality_evaluation_v5",
			}, DefaultRunConfig("standard"))
			if !errors.Is(err, ErrInvalidResult) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err=%v want=%q", err, test.want)
			}
		})
	}
}

func TestV4RejectsV5EvaluationDefects(t *testing.T) {
	evaluation := validV5EvaluationResult(false)
	raw, err := json.Marshal(ResultEnvelope{
		SchemaVersion: 4, ClientRequestID: "v4-defect", Summary: "reviewed", Confidence: 0.9,
		Evaluation: &evaluation,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = DecodeAndValidateResultForVersion(OrchestratorVersionV4, raw, Task{
		Kind: TaskKindQualityGate, RequiredCapability: "validator", ExpectedResult: "research_quality_evaluation_v4",
	}, DefaultRunConfig("standard"))
	if !errors.Is(err, ErrInvalidResult) || !strings.Contains(err.Error(), "schema_version 5") {
		t.Fatalf("err=%v", err)
	}
}

func TestV5EvaluationDefectsMatchReportAndScoreFloor(t *testing.T) {
	evaluation := validV5EvaluationResult(false)
	if err := validateEvaluationDefectsAgainstReport(evaluation, []string{"claim-alpha"}, []string{"section-alpha"}, 0.75); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*EvaluationProposal)
		want   string
	}{
		{name: "unknown claim", mutate: func(e *EvaluationProposal) { e.Defects[0].ClaimKeys = []string{"claim-missing"} }, want: "unknown report Claim"},
		{name: "unmapped low score", mutate: func(e *EvaluationProposal) { e.Defects[0].Dimension = "coverage" }, want: "factual_grounding"},
		{name: "failed above floor", mutate: func(e *EvaluationProposal) { e.FactualGrounding = 0.9; e.Defects = nil }, want: "requires a dimension below"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := validV5EvaluationResult(false)
			test.mutate(&candidate)
			err := validateEvaluationDefectsAgainstReport(candidate, []string{"claim-alpha"}, []string{"section-alpha"}, 0.75)
			if !errors.Is(err, ErrInvalidResult) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err=%v want=%q", err, test.want)
			}
		})
	}
}

func validV5EvaluationResult(passed bool) EvaluationProposal {
	dimensions := map[string]string{}
	for _, dimension := range evaluationDimensionNames {
		dimensions[dimension] = "The reviewer inspected this dimension against every report claim and section and recorded the concrete evidence trail."
	}
	return EvaluationProposal{
		Passed: passed, FactualGrounding: 0.5, Coverage: 0.9, AnalyticalDepth: 0.9,
		SourceQuality: 0.9, ContradictionHandling: 0.9, InstructionAdherence: 0.9, Readability: 0.9,
		DimensionFindings: dimensions,
		ReviewedClaimKeys: []string{"claim-alpha"}, ReviewedSectionIDs: []string{"section-alpha"},
		Defects: []EvaluationDefect{{
			ClientKey: "defect-grounding-alpha", Dimension: "factual_grounding", Severity: evaluationDefectBlocking,
			Problem:        "The conclusion states claim-alpha as unconditional although its cited observation applies only inside the measured operating boundary.",
			RequiredChange: "Revise claim-alpha in section-alpha to retain the measured boundary and cite the exact supporting observation without broadening it.",
			ClaimKeys:      []string{"claim-alpha"}, SectionIDs: []string{"section-alpha"},
		}},
	}
}
