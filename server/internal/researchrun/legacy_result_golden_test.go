package researchrun

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

const legacyResultGoldenSchemaVersion = "research-legacy-result-contracts-v1"

type legacyResultGoldenManifest struct {
	SchemaVersion string                      `json:"schema_version"`
	Versions      []legacyResultVersionGolden `json:"versions"`
}

type legacyResultVersionGolden struct {
	Version string            `json:"version"`
	Cases   map[string]string `json:"cases"`
}

type legacyResultCase struct {
	Task   Task
	Result ResultEnvelope
}

func TestLegacyResultContractsMatchGoldenFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/golden/legacy_result_contracts.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest legacyResultGoldenManifest
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != legacyResultGoldenSchemaVersion {
		t.Fatalf("golden schema=%q want=%q", manifest.SchemaVersion, legacyResultGoldenSchemaVersion)
	}

	wantVersions := []string{
		OrchestratorVersionV1,
		OrchestratorVersionV2,
		OrchestratorVersionV3,
		OrchestratorVersionV4,
		OrchestratorVersionV5,
	}
	if len(manifest.Versions) != len(wantVersions) {
		t.Fatalf("golden versions=%d want=%d", len(manifest.Versions), len(wantVersions))
	}
	seenVersions := make(map[string]bool, len(manifest.Versions))
	for _, versionGolden := range manifest.Versions {
		t.Run(versionGolden.Version, func(t *testing.T) {
			if seenVersions[versionGolden.Version] {
				t.Fatalf("duplicate golden version %q", versionGolden.Version)
			}
			seenVersions[versionGolden.Version] = true
			cases := goldenLegacyResultCases(t, versionGolden.Version)
			gotNames := sortedLegacyResultCaseNames(cases)
			wantNames := sortedLegacyResultHashNames(versionGolden.Cases)
			if !reflect.DeepEqual(gotNames, wantNames) {
				t.Fatalf("golden cases=%v want=%v", wantNames, gotNames)
			}
			for _, name := range gotNames {
				t.Run(name, func(t *testing.T) {
					fixture := cases[name]
					rawResult, err := json.Marshal(fixture.Result)
					if err != nil {
						t.Fatal(err)
					}
					_, canonicalHash, err := DecodeAndValidateResultForVersion(
						versionGolden.Version,
						rawResult,
						fixture.Task,
						DefaultRunConfig("shallow"),
					)
					if err != nil {
						t.Fatalf("accepted legacy result rejected: %v", err)
					}
					if canonicalHash != versionGolden.Cases[name] {
						t.Errorf("canonical hash=%s want=%s", canonicalHash, versionGolden.Cases[name])
					}

					var future map[string]any
					if err = json.Unmarshal(rawResult, &future); err != nil {
						t.Fatal(err)
					}
					future["hypotheses"] = []any{map[string]any{"client_key": "future-hypothesis"}}
					futureRaw, err := json.Marshal(future)
					if err != nil {
						t.Fatal(err)
					}
					if _, _, err = DecodeAndValidateResultForVersion(
						versionGolden.Version,
						futureRaw,
						fixture.Task,
						DefaultRunConfig("shallow"),
					); err == nil || !strings.Contains(err.Error(), `unknown field "hypotheses"`) {
						t.Fatalf("future result field error=%v", err)
					}
				})
			}
		})
	}
	for _, version := range wantVersions {
		if !seenVersions[version] {
			t.Errorf("missing golden version %q", version)
		}
	}
}

func sortedLegacyResultCaseNames(values map[string]legacyResultCase) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedLegacyResultHashNames(values map[string]string) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func goldenLegacyResultCases(t *testing.T, version string) map[string]legacyResultCase {
	t.Helper()
	plan, planTask := goldenPlanResult(t, version)
	return map[string]legacyResultCase{
		"plan": {
			Task:   planTask,
			Result: plan,
		},
		"evidence": {
			Task: Task{
				Kind: TaskKindDiscover, QuestionID: "golden-question", RequiredCapability: "scout",
				ExpectedResult: goldenLegacyExpectedResult(version, "research_evidence"),
			},
			Result: goldenLegacyEvidenceResult(version),
		},
		"report": {
			Task: Task{
				Kind: TaskKindSynthesize, RequiredCapability: "reporter",
				ExpectedResult: goldenLegacyExpectedResult(version, "research_report"),
			},
			Result: goldenLegacyReportResult(t, version),
		},
		"quality_evaluation": {
			Task: Task{
				Kind: TaskKindQualityGate, RequiredCapability: "validator",
				ExpectedResult: goldenLegacyExpectedResult(version, "research_quality_evaluation"),
			},
			Result: goldenLegacyEvaluationResult(version, "quality"),
		},
		"citation_audit": {
			Task: Task{
				Kind: TaskKindCitationAudit, RequiredCapability: "validator",
				ExpectedResult: goldenLegacyExpectedResult(version, "research_citation_audit"),
			},
			Result: goldenLegacyEvaluationResult(version, "citation"),
		},
	}
}

func goldenLegacySchemaVersion(version string) int {
	switch version {
	case OrchestratorVersionV1:
		return 1
	case OrchestratorVersionV2:
		return 2
	case OrchestratorVersionV3:
		return 3
	case OrchestratorVersionV4:
		return 4
	case OrchestratorVersionV5:
		return 5
	default:
		return 0
	}
}

func goldenLegacyExpectedResult(version, prefix string) string {
	return fmt.Sprintf("%s_v%d", prefix, goldenLegacySchemaVersion(version))
}

func goldenLegacyEvidenceResult(version string) ResultEnvelope {
	schemaVersion := goldenLegacySchemaVersion(version)
	result := ResultEnvelope{
		SchemaVersion: schemaVersion, ClientRequestID: fmt.Sprintf("golden-evidence-v%d", schemaVersion),
		Summary: "The controlling record states the measured value inside the declared boundary.",
		Sources: []SourceProposal{{
			ClientKey: "source-primary", URL: "https://example.test/record", Title: "Controlling record",
			Publisher: "Example Registry", SourceClass: "official", IndependenceKey: "example-registry",
			RetrievedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), SnapshotText: "Inside the declared boundary, the measured value is 42.",
		}},
		Observations: []ObservationProposal{{
			ClientKey: "observation-value", SourceKey: "source-primary",
			Quote: "Inside the declared boundary, the measured value is 42.", Locator: "record 1",
			Interpretation: "The measurement is bounded by the declared operating scope.",
		}},
		Claims: []ClaimProposal{{
			ClientKey: "claim-value", Text: "The measured value is 42 inside the declared boundary.",
			Significance: "high", Confidence: 0.9, Status: ClaimStatusSupported,
			Evidence: []EvidenceProposal{{
				ObservationKey: "observation-value", Relation: "supports", Strength: 0.95,
			}},
		}},
		CoverageDelta: 0.7, Confidence: 0.9,
	}
	if schemaVersion >= 2 {
		result.AnswerClaimKey = "claim-value"
	}
	if schemaVersion >= 4 {
		result.Sources[0].EvidenceTraits = []string{"official_record"}
		result.Claims[0].EvidenceStandardKey = "controlling-record"
		result.Claims[0].Evidence[0].Directness = 1
		result.Claims[0].Evidence[0].MethodFit = 0.95
		result.Claims[0].Evidence[0].Rationale = "The controlling record directly reports the scoped measurement."
	}
	return result
}

func goldenLegacyReportResult(t *testing.T, version string) ResultEnvelope {
	t.Helper()
	schemaVersion := goldenLegacySchemaVersion(version)
	sectionText := func(subject string) string {
		return strings.Repeat(subject+" records the evidence boundary, comparison method, observed result, uncertainty, and decision consequence in traceable prose. ", 2)
	}
	anchor := "The controlling record supports the measured value 42 inside the declared boundary."
	conclusion := strings.Repeat("The measured result supports the bounded decision while preserving the source scope and remaining uncertainty. ", 2)
	sections := []reportStructuredSection{
		{ID: "method", Title: "Method", Level: 1, Markdown: sectionText("The method section")},
		{ID: "finding", Title: "Finding", Level: 1, Markdown: anchor + " " + sectionText("The finding section"), CitationIDs: []string{"citation-1"}},
		{ID: "conclusion", Title: "Conclusion", Level: 1, Markdown: conclusion},
	}
	structured := reportStructuredV1{
		SchemaVersion: 1, Title: "Golden research report", Conclusion: conclusion,
		Sources: []reportStructuredSource{{
			SourceID: "source-primary-id", Title: "Controlling record", URL: "https://example.test/record",
			CredibilityWeight: 0.95, SourceClass: "official",
		}},
		Citations: []reportStructuredCitation{{
			ID: "citation-1", Index: 1, SourceID: "source-primary-id", Label: "[1]",
			Quote: "Inside the declared boundary, the measured value is 42.", Locator: "record 1",
		}},
	}
	contentParts := []string{"# Golden research report"}
	for _, section := range sections {
		structured.Sections = append(structured.Sections, section)
		structured.Outline = append(structured.Outline, reportOutlineItem{
			ID: section.ID, Title: section.Title, Level: section.Level, Children: []string{},
		})
		contentParts = append(contentParts, "## "+section.Title+"\n\n"+section.Markdown)
	}
	structuredJSON, err := json.Marshal(structured)
	if err != nil {
		t.Fatal(err)
	}
	reportClaim := ReportClaimProposal{ClaimKey: "claim-value", SectionID: "finding"}
	if schemaVersion >= 2 {
		reportClaim.AnchorQuote = anchor
	}
	return ResultEnvelope{
		SchemaVersion: schemaVersion, ClientRequestID: fmt.Sprintf("golden-report-v%d", schemaVersion),
		Summary: "The report integrates the bounded finding and its decision consequence.",
		Report: &ReportProposal{
			ContentMD: strings.Join(contentParts, "\n\n"), Structured: structuredJSON,
			Claims: []ReportClaimProposal{reportClaim},
		},
		CoverageDelta: 0, Confidence: 0.9,
	}
}

func TestStructuredReportRejectsOutlineCycle(t *testing.T) {
	result := goldenLegacyReportResult(t, OrchestratorVersionV2)
	var structured reportStructuredV1
	if err := json.Unmarshal(result.Report.Structured, &structured); err != nil {
		t.Fatal(err)
	}
	structured.Outline[0].Children = []string{structured.Outline[1].ID}
	structured.Outline[1].Children = []string{structured.Outline[0].ID}
	raw, err := json.Marshal(structured)
	if err != nil {
		t.Fatal(err)
	}
	result.Report.Structured = raw
	if _, err = validateStructuredReportV2(*result.Report, reportPolicyForDepth("shallow")); err == nil || !strings.Contains(err.Error(), "outline contains a cycle") {
		t.Fatalf("cycle validation error=%v", err)
	}
}

func goldenLegacyEvaluationResult(version, reviewKind string) ResultEnvelope {
	schemaVersion := goldenLegacySchemaVersion(version)
	evaluation := EvaluationProposal{
		Passed: true, FactualGrounding: 0.9, Coverage: 0.9, AnalyticalDepth: 0.85,
		SourceQuality: 0.9, ContradictionHandling: 0.85, InstructionAdherence: 0.95, Readability: 0.9,
		Findings: []string{},
	}
	if schemaVersion >= 2 {
		evaluation.DimensionFindings = map[string]string{}
		for _, dimension := range evaluationDimensionNames {
			evaluation.DimensionFindings[dimension] = "The reviewer checked every report claim and section against the accepted evidence and found no unresolved defect."
		}
		evaluation.ReviewedClaimKeys = []string{"claim-value"}
		evaluation.ReviewedSectionIDs = []string{"finding"}
	}
	return ResultEnvelope{
		SchemaVersion: schemaVersion, ClientRequestID: fmt.Sprintf("golden-%s-v%d", reviewKind, schemaVersion),
		Summary: "The reviewer completed the declared coverage and recorded the result.", Evaluation: &evaluation,
		CoverageDelta: 0, Confidence: 0.9,
	}
}
