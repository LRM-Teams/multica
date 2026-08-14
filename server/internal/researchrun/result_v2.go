package researchrun

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

const (
	minimumReportAnchorCharacters    = 20
	minimumReviewRationaleCharacters = 40
)

var evaluationDimensionNames = []string{
	"factual_grounding",
	"coverage",
	"analytical_depth",
	"source_quality",
	"contradiction_handling",
	"instruction_adherence",
	"readability",
}

type reportStructuredV1 struct {
	SchemaVersion int                        `json:"schema_version"`
	Title         string                     `json:"title"`
	Outline       []reportOutlineItem        `json:"outline"`
	Sections      []reportStructuredSection  `json:"sections"`
	Citations     []reportStructuredCitation `json:"citations"`
	Sources       []reportStructuredSource   `json:"sources"`
	Gaps          []string                   `json:"gaps,omitempty"`
	Conclusion    string                     `json:"conclusion"`
}

type reportOutlineItem struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Level    int      `json:"level"`
	Children []string `json:"children"`
}

type reportStructuredSection struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Level       int      `json:"level"`
	Markdown    string   `json:"markdown"`
	CitationIDs []string `json:"citation_ids"`
}

type reportStructuredCitation struct {
	ID       string `json:"id"`
	Index    int    `json:"index"`
	SourceID string `json:"source_id"`
	Label    string `json:"label"`
	Quote    string `json:"quote,omitempty"`
	Locator  string `json:"locator,omitempty"`
}

type reportStructuredSource struct {
	SourceID          string  `json:"source_id"`
	Title             string  `json:"title"`
	URL               string  `json:"url"`
	CredibilityWeight float64 `json:"credibility_weight"`
	SourceClass       string  `json:"source_class"`
}

type reportDepthPolicy struct {
	MinimumSections             int
	MinimumSectionCharacters    int
	MinimumConclusionCharacters int
}

func (r *ResultEnvelope) validateV2(task Task, cfg RunConfig) error {
	if r.SchemaVersion != 2 {
		return fmt.Errorf("%w: research-run-v2 requires schema_version 2", ErrInvalidResult)
	}
	legacy := *r
	legacy.SchemaVersion = 1
	legacy.AnswerClaimKey = ""
	if r.Report != nil {
		report := *r.Report
		report.Claims = append([]ReportClaimProposal(nil), r.Report.Claims...)
		for i := range report.Claims {
			report.Claims[i].AnchorQuote = ""
		}
		legacy.Report = &report
	}
	if r.Evaluation != nil {
		evaluation := *r.Evaluation
		evaluation.DimensionFindings = nil
		evaluation.ReviewedClaimKeys = nil
		evaluation.ReviewedSectionIDs = nil
		legacy.Evaluation = &evaluation
	}
	if err := legacy.Validate(task, cfg); err != nil {
		return err
	}
	capability, expected, fixed := v2TaskContract(task.Kind)
	if fixed && task.RequiredCapability != capability {
		return fmt.Errorf("%w: assigned %s task requires capability %q", ErrInvalidResult, task.Kind, capability)
	}
	if expected != "" && task.ExpectedResult != expected {
		return fmt.Errorf("%w: assigned %s task requires expected_result %q", ErrInvalidResult, task.Kind, expected)
	}
	if r.Plan != nil && task.Kind != TaskKindPlan && task.Kind != TaskKindReplan {
		return fmt.Errorf("%w: only plan or replan tasks may submit a plan", ErrInvalidResult)
	}
	if r.Report != nil && task.Kind != TaskKindSynthesize {
		return fmt.Errorf("%w: only synthesize tasks may submit a report", ErrInvalidResult)
	}
	if r.Evaluation != nil && task.Kind != TaskKindQualityGate && task.Kind != TaskKindCitationAudit {
		return fmt.Errorf("%w: only quality or citation tasks may submit an evaluation", ErrInvalidResult)
	}
	if err := validateV2TaskContracts(*r); err != nil {
		return err
	}
	if isEvidenceTask(task.Kind) && task.QuestionID != "" {
		if r.CoverageDelta > 0 && r.AnswerClaimKey == "" {
			return fmt.Errorf("%w: evidence coverage requires answer_claim_key", ErrInvalidResult)
		}
		if r.AnswerClaimKey != "" {
			if err := validateKey("answer_claim_key", r.AnswerClaimKey); err != nil {
				return err
			}
			found := false
			for _, claim := range r.Claims {
				found = found || claim.ClientKey == r.AnswerClaimKey
			}
			if !found {
				return fmt.Errorf("%w: answer_claim_key %q is absent from result claims", ErrInvalidResult, r.AnswerClaimKey)
			}
		}
	} else if r.AnswerClaimKey != "" {
		return fmt.Errorf("%w: answer_claim_key is only valid for question-scoped evidence coverage", ErrInvalidResult)
	}
	if r.Report != nil {
		if _, err := validateStructuredReportV2(*r.Report, reportDepthPolicy{MinimumSections: 3, MinimumSectionCharacters: 80, MinimumConclusionCharacters: 80}); err != nil {
			return err
		}
	}
	if r.Evaluation != nil {
		if err := validateEvaluationCoverageV2(*r.Evaluation); err != nil {
			return err
		}
	}
	return nil
}

func validateV2TaskContracts(result ResultEnvelope) error {
	tasks := append([]TaskProposal(nil), result.ProposedTasks...)
	if result.Plan != nil {
		tasks = append(tasks, result.Plan.Tasks...)
	}
	for _, task := range tasks {
		capability, expected, fixed := v2TaskContract(task.Kind)
		if fixed && task.RequiredCapability != capability {
			return fmt.Errorf("%w: %s task %q requires capability %q", ErrInvalidResult, task.Kind, task.ClientKey, capability)
		}
		if expected != "" && task.ExpectedResult != expected {
			return fmt.Errorf("%w: %s task %q requires expected_result %q", ErrInvalidResult, task.Kind, task.ClientKey, expected)
		}
	}
	if result.Plan != nil {
		counts := map[TaskKind]int{}
		synthesisKeys := map[string]struct{}{}
		for _, task := range result.Plan.Tasks {
			counts[task.Kind]++
			if task.Kind == TaskKindSynthesize {
				synthesisKeys[task.ClientKey] = struct{}{}
			}
		}
		for _, kind := range []TaskKind{TaskKindSynthesize, TaskKindQualityGate, TaskKindCitationAudit} {
			if counts[kind] == 0 {
				return fmt.Errorf("%w: v2 plan requires a %s delivery task", ErrInvalidResult, kind)
			}
		}
		for _, task := range result.Plan.Tasks {
			if task.Kind != TaskKindQualityGate && task.Kind != TaskKindCitationAudit {
				continue
			}
			dependsOnSynthesis := false
			for _, dependency := range task.DependsOn {
				_, dependsOnSynthesis = synthesisKeys[dependency]
				if dependsOnSynthesis {
					break
				}
			}
			if !dependsOnSynthesis {
				return fmt.Errorf("%w: %s task %q must depend on a synthesize task", ErrInvalidResult, task.Kind, task.ClientKey)
			}
		}
	}
	return nil
}

func v2TaskContract(kind TaskKind) (string, string, bool) {
	switch kind {
	case TaskKindPlan, TaskKindReplan:
		return "lead", "research_plan_v2", true
	case TaskKindSynthesize:
		return "reporter", "research_report_v2", true
	case TaskKindQualityGate:
		return "validator", "research_quality_evaluation_v2", true
	case TaskKindCitationAudit:
		return "validator", "research_citation_audit_v2", true
	case TaskKindDiscover, TaskKindDeepRead, TaskKindVerify, TaskKindCounterSearch:
		return "", "research_evidence_v2", false
	default:
		return "", "", false
	}
}

func validateStructuredReportV2(report ReportProposal, policy reportDepthPolicy) (reportStructuredV1, error) {
	var structured reportStructuredV1
	decoder := json.NewDecoder(bytes.NewReader(report.Structured))
	if err := decoder.Decode(&structured); err != nil {
		return structured, fmt.Errorf("%w: report.structured must match report schema v1: %v", ErrInvalidResult, err)
	}
	if structured.SchemaVersion != 1 || strings.TrimSpace(structured.Title) == "" {
		return structured, fmt.Errorf("%w: report requires structured schema_version 1 and title", ErrInvalidResult)
	}
	if len(structured.Sections) < policy.MinimumSections {
		return structured, fmt.Errorf("%w: report requires at least %d substantive sections", ErrInvalidResult, policy.MinimumSections)
	}
	if substantiveRuneCount(structured.Conclusion) < policy.MinimumConclusionCharacters || !strings.Contains(report.ContentMD, structured.Conclusion) {
		return structured, fmt.Errorf("%w: report conclusion is incomplete or absent from content_md", ErrInvalidResult)
	}

	sections := make(map[string]reportStructuredSection, len(structured.Sections))
	for _, section := range structured.Sections {
		if err := validateKey("report section id", section.ID); err != nil || strings.TrimSpace(section.Title) == "" || section.Level < 1 || section.Level > 6 {
			return structured, fmt.Errorf("%w: report section %q is invalid", ErrInvalidResult, section.ID)
		}
		if _, exists := sections[section.ID]; exists {
			return structured, fmt.Errorf("%w: duplicate report section %q", ErrInvalidResult, section.ID)
		}
		if substantiveRuneCount(section.Markdown) < policy.MinimumSectionCharacters || !strings.Contains(report.ContentMD, section.Markdown) {
			return structured, fmt.Errorf("%w: report section %q is placeholder content or absent from content_md", ErrInvalidResult, section.ID)
		}
		sections[section.ID] = section
	}

	outlineIDs := map[string]struct{}{}
	outlineChildren := make(map[string][]string, len(structured.Outline))
	outlineParents := make(map[string]string, len(structured.Outline))
	for _, item := range structured.Outline {
		if _, exists := sections[item.ID]; !exists {
			return structured, fmt.Errorf("%w: report outline references unknown section %q", ErrInvalidResult, item.ID)
		}
		if _, duplicate := outlineIDs[item.ID]; duplicate {
			return structured, fmt.Errorf("%w: duplicate report outline item %q", ErrInvalidResult, item.ID)
		}
		outlineIDs[item.ID] = struct{}{}
		childIDs := make(map[string]struct{}, len(item.Children))
		for _, child := range item.Children {
			if _, exists := sections[child]; !exists {
				return structured, fmt.Errorf("%w: report outline child references unknown section %q", ErrInvalidResult, child)
			}
			if _, duplicate := childIDs[child]; duplicate {
				return structured, fmt.Errorf("%w: report outline item %q repeats child %q", ErrInvalidResult, item.ID, child)
			}
			childIDs[child] = struct{}{}
			if parent, duplicate := outlineParents[child]; duplicate {
				return structured, fmt.Errorf("%w: report outline section %q has multiple parents %q and %q", ErrInvalidResult, child, parent, item.ID)
			}
			outlineParents[child] = item.ID
		}
		outlineChildren[item.ID] = append([]string(nil), item.Children...)
	}
	if len(outlineIDs) != len(sections) {
		return structured, fmt.Errorf("%w: report outline must cover every section", ErrInvalidResult)
	}
	visiting := make(map[string]bool, len(outlineChildren))
	visited := make(map[string]bool, len(outlineChildren))
	var visitOutline func(string) bool
	visitOutline = func(sectionID string) bool {
		if visiting[sectionID] {
			return true
		}
		if visited[sectionID] {
			return false
		}
		visiting[sectionID] = true
		for _, childID := range outlineChildren[sectionID] {
			if visitOutline(childID) {
				return true
			}
		}
		visiting[sectionID] = false
		visited[sectionID] = true
		return false
	}
	for sectionID := range outlineChildren {
		if visitOutline(sectionID) {
			return structured, fmt.Errorf("%w: report outline contains a cycle at section %q", ErrInvalidResult, sectionID)
		}
	}

	sourceIDs := map[string]struct{}{}
	for _, source := range structured.Sources {
		if strings.TrimSpace(source.SourceID) == "" {
			return structured, fmt.Errorf("%w: report structured source requires source_id", ErrInvalidResult)
		}
		if _, duplicate := sourceIDs[source.SourceID]; duplicate {
			return structured, fmt.Errorf("%w: duplicate report structured source %q", ErrInvalidResult, source.SourceID)
		}
		sourceIDs[source.SourceID] = struct{}{}
	}
	citations := map[string]reportStructuredCitation{}
	for _, citation := range structured.Citations {
		if err := validateKey("report citation id", citation.ID); err != nil || citation.Index < 1 || strings.TrimSpace(citation.Label) == "" {
			return structured, fmt.Errorf("%w: report citation %q is invalid", ErrInvalidResult, citation.ID)
		}
		if _, exists := sourceIDs[citation.SourceID]; !exists {
			return structured, fmt.Errorf("%w: report citation %q references unknown source %q", ErrInvalidResult, citation.ID, citation.SourceID)
		}
		if _, duplicate := citations[citation.ID]; duplicate {
			return structured, fmt.Errorf("%w: duplicate report citation %q", ErrInvalidResult, citation.ID)
		}
		citations[citation.ID] = citation
	}
	for _, section := range structured.Sections {
		for _, citationID := range section.CitationIDs {
			if _, exists := citations[citationID]; !exists {
				return structured, fmt.Errorf("%w: report section %q references unknown citation %q", ErrInvalidResult, section.ID, citationID)
			}
		}
	}

	linked := map[string]struct{}{}
	for _, link := range report.Claims {
		if err := validateKey("report.claim_key", link.ClaimKey); err != nil {
			return structured, err
		}
		section, exists := sections[link.SectionID]
		if !exists {
			return structured, fmt.Errorf("%w: report claim %q references unknown section %q", ErrInvalidResult, link.ClaimKey, link.SectionID)
		}
		if len(section.CitationIDs) == 0 {
			return structured, fmt.Errorf("%w: report claim %q section has no citations", ErrInvalidResult, link.ClaimKey)
		}
		if substantiveRuneCount(link.AnchorQuote) < minimumReportAnchorCharacters || !strings.Contains(section.Markdown, link.AnchorQuote) {
			return structured, fmt.Errorf("%w: report claim %q requires an exact substantive prose anchor", ErrInvalidResult, link.ClaimKey)
		}
		key := link.ClaimKey + "\x00" + link.SectionID
		if _, duplicate := linked[key]; duplicate {
			return structured, fmt.Errorf("%w: duplicate report claim section link for %q", ErrInvalidResult, link.ClaimKey)
		}
		linked[key] = struct{}{}
	}
	return structured, nil
}

func validateEvaluationCoverageV2(evaluation EvaluationProposal) error {
	if len(evaluation.DimensionFindings) != len(evaluationDimensionNames) {
		return fmt.Errorf("%w: evaluation requires a rationale for every quality dimension", ErrInvalidResult)
	}
	for _, dimension := range evaluationDimensionNames {
		if substantiveRuneCount(evaluation.DimensionFindings[dimension]) < minimumReviewRationaleCharacters {
			return fmt.Errorf("%w: evaluation dimension %q lacks substantive rationale", ErrInvalidResult, dimension)
		}
	}
	if len(evaluation.ReviewedClaimKeys) == 0 || len(evaluation.ReviewedSectionIDs) == 0 {
		return fmt.Errorf("%w: evaluation requires reviewed claim and section coverage", ErrInvalidResult)
	}
	for _, key := range append(append([]string(nil), evaluation.ReviewedClaimKeys...), evaluation.ReviewedSectionIDs...) {
		if err := validateKey("evaluation coverage key", key); err != nil {
			return err
		}
	}
	if !evaluation.Passed && len(evaluation.Findings) == 0 {
		return fmt.Errorf("%w: failed evaluation requires findings", ErrInvalidResult)
	}
	return nil
}

func reportPolicyForDepth(depthTier string) reportDepthPolicy {
	switch depthTier {
	case "shallow":
		return reportDepthPolicy{MinimumSections: 3, MinimumSectionCharacters: 80, MinimumConclusionCharacters: 80}
	case "deep":
		return reportDepthPolicy{MinimumSections: 7, MinimumSectionCharacters: 160, MinimumConclusionCharacters: 160}
	default:
		return reportDepthPolicy{MinimumSections: 5, MinimumSectionCharacters: 120, MinimumConclusionCharacters: 120}
	}
}

func substantiveRuneCount(value string) int {
	count := 0
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			count++
		}
	}
	return count
}

func sortedUnique(values []string) ([]string, bool) {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, duplicate := seen[value]; duplicate {
			return nil, false
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result, true
}

func sameUniqueStringSet(left, right []string) bool {
	leftSorted, leftUnique := sortedUnique(left)
	rightSorted, rightUnique := sortedUnique(right)
	if !leftUnique || !rightUnique || len(leftSorted) != len(rightSorted) {
		return false
	}
	for i := range leftSorted {
		if leftSorted[i] != rightSorted[i] {
			return false
		}
	}
	return true
}
