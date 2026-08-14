package researchrun

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

const ResearchV6PlanContractKind = "plan_result"

type ResearchV6EntityRef struct {
	Kind string `json:"kind"`
	Key  string `json:"key"`
}

type ResearchV6Question struct {
	ClientKey       string   `json:"client_key"`
	ParentClientKey string   `json:"parent_client_key,omitempty"`
	Text            string   `json:"text"`
	Kind            string   `json:"kind"`
	Required        *bool    `json:"required"`
	Priority        *float64 `json:"priority"`
	Impact          *float64 `json:"impact"`
	Uncertainty     *float64 `json:"uncertainty"`
	Novelty         *float64 `json:"novelty"`
}

type ResearchV6Hypothesis struct {
	ClientKey            string         `json:"client_key"`
	QuestionKey          string         `json:"question_key"`
	Statement            string         `json:"statement"`
	Applicability        map[string]any `json:"applicability"`
	ExpectedObservations []string       `json:"expected_observations"`
	WeakeningConditions  []string       `json:"weakening_conditions"`
	ConfidenceLow        *float64       `json:"confidence_low,omitempty"`
	ConfidenceHigh       *float64       `json:"confidence_high,omitempty"`
}

type ResearchV6Branch struct {
	ClientKey       string   `json:"client_key"`
	ParentBranchKey string   `json:"parent_branch_key,omitempty"`
	Objective       string   `json:"objective"`
	EntryConditions []string `json:"entry_conditions"`
	ExitConditions  []string `json:"exit_conditions"`
	BudgetShare     *float64 `json:"budget_share"`
}

type ResearchV6InquiryEdge struct {
	ClientKey string              `json:"client_key"`
	From      ResearchV6EntityRef `json:"from"`
	To        ResearchV6EntityRef `json:"to"`
	Relation  string              `json:"relation"`
	Rationale string              `json:"rationale"`
}

type ResearchV6PlanTask struct {
	ClientKey          string                `json:"client_key"`
	Kind               string                `json:"kind"`
	Objective          string                `json:"objective"`
	RequiredCapability string                `json:"required_capability"`
	ExpectedResult     string                `json:"expected_result"`
	Priority           *float64              `json:"priority"`
	Targets            []ResearchV6EntityRef `json:"targets"`
	DependsOn          []string              `json:"depends_on,omitempty"`
	AcceptanceCriteria map[string]any        `json:"acceptance_criteria,omitempty"`
	MaxAttempts        *int                  `json:"max_attempts,omitempty"`
	TimeoutSeconds     *int                  `json:"timeout_seconds,omitempty"`
}

type ResearchV6SearchPlan struct {
	ClientKey          string                `json:"client_key"`
	Targets            []ResearchV6EntityRef `json:"targets"`
	Adapter            string                `json:"adapter"`
	QueryStrategy      string                `json:"query_strategy"`
	TimeWindow         map[string]any        `json:"time_window,omitempty"`
	Languages          []string              `json:"languages,omitempty"`
	Domains            []string              `json:"domains,omitempty"`
	InclusionCriteria  []string              `json:"inclusion_criteria"`
	ExclusionCriteria  []string              `json:"exclusion_criteria"`
	StoppingConditions []string              `json:"stopping_conditions"`
	StrategyVersion    string                `json:"strategy_version"`
}

type ResearchV6PlanResult struct {
	ContractKind    string                  `json:"contract_kind"`
	SchemaVersion   int                     `json:"schema_version"`
	ClientRequestID string                  `json:"client_request_id"`
	Summary         string                  `json:"summary"`
	Questions       []ResearchV6Question    `json:"questions"`
	Hypotheses      []ResearchV6Hypothesis  `json:"hypotheses"`
	Branches        []ResearchV6Branch      `json:"branches"`
	InquiryEdges    []ResearchV6InquiryEdge `json:"inquiry_edges"`
	Tasks           []ResearchV6PlanTask    `json:"tasks"`
	SearchPlans     []ResearchV6SearchPlan  `json:"search_plans"`
	Method          map[string]any          `json:"method"`
}

// DecodeAndValidateResearchV6PlanResult is deliberately separate from the
// V1-V5 decoder. Adding V6 to the production orchestrator remains a later,
// explicit activation gate.
func DecodeAndValidateResearchV6PlanResult(raw []byte) (ResearchV6PlanResult, string, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return ResearchV6PlanResult{}, "", fmt.Errorf("%w: decode research-run-v6 plan_result: %v", ErrInvalidResult, err)
	}
	for _, field := range []string{"contract_kind", "schema_version", "client_request_id", "summary", "questions", "hypotheses", "branches", "inquiry_edges", "tasks", "search_plans", "method"} {
		value, present := envelope[field]
		if !present {
			return ResearchV6PlanResult{}, "", fmt.Errorf("%w: plan_result requires %s", ErrInvalidResult, field)
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return ResearchV6PlanResult{}, "", fmt.Errorf("%w: plan_result %s cannot be null", ErrInvalidResult, field)
		}
	}
	if err := rejectResearchV6PlanNulls(envelope); err != nil {
		return ResearchV6PlanResult{}, "", err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var result ResearchV6PlanResult
	if err := decoder.Decode(&result); err != nil {
		return ResearchV6PlanResult{}, "", fmt.Errorf("%w: decode research-run-v6 plan_result: %v", ErrInvalidResult, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return ResearchV6PlanResult{}, "", fmt.Errorf("%w: plan_result contains trailing JSON", ErrInvalidResult)
	}
	if err := validateResearchV6PlanResult(result); err != nil {
		return ResearchV6PlanResult{}, "", err
	}
	canonical, err := MarshalArtifactCanonicalJSON(json.RawMessage(raw))
	if err != nil {
		return ResearchV6PlanResult{}, "", fmt.Errorf("%w: canonicalize plan_result: %v", ErrInvalidResult, err)
	}
	return result, ArtifactContentHashFromCanonicalJSON(canonical), nil
}

func rejectResearchV6PlanNulls(envelope map[string]json.RawMessage) error {
	groups := map[string][]string{
		"questions":    {"parent_client_key"},
		"hypotheses":   {"confidence_low", "confidence_high"},
		"branches":     {"parent_branch_key"},
		"tasks":        {"depends_on", "acceptance_criteria", "max_attempts", "timeout_seconds"},
		"search_plans": {"time_window", "languages", "domains"},
	}
	for group, optionalFields := range groups {
		var objects []map[string]json.RawMessage
		if err := json.Unmarshal(envelope[group], &objects); err != nil {
			return fmt.Errorf("%w: plan_result %s must be an object array", ErrInvalidResult, group)
		}
		for index, object := range objects {
			for _, field := range optionalFields {
				if value, present := object[field]; present && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
					return fmt.Errorf("%w: plan_result %s[%d].%s cannot be null", ErrInvalidResult, group, index, field)
				}
			}
		}
	}
	return nil
}

func validateResearchV6PlanResult(result ResearchV6PlanResult) error {
	invalid := func(format string, args ...any) error {
		return fmt.Errorf("%w: "+format, append([]any{ErrInvalidResult}, args...)...)
	}
	if result.ContractKind != ResearchV6PlanContractKind || result.SchemaVersion != 6 || uuid.Validate(result.ClientRequestID) != nil || !validV6Text(result.Summary) || result.Method == nil {
		return invalid("invalid plan_result envelope")
	}
	if len(result.Questions) < 1 || len(result.Questions) > 256 || len(result.Hypotheses) > 256 || len(result.Branches) < 1 || len(result.Branches) > 128 || len(result.InquiryEdges) > 1024 || len(result.Tasks) < 1 || len(result.Tasks) > 256 || len(result.SearchPlans) < 1 || len(result.SearchPlans) > 128 {
		return invalid("plan_result collection bounds")
	}
	entities := map[string]map[string]bool{"question": {}, "hypothesis": {}, "branch": {}, "task": {}}
	questionParents := map[string]string{}
	for _, item := range result.Questions {
		if !claimV6Key(entities["question"], item.ClientKey) || !optionalV6Key(item.ParentClientKey) || !validV6Text(item.Text) || !oneOf(item.Kind, "dimension", "hypothesis", "contradiction", "gap", "follow_up") || item.Required == nil || !validRequiredScore(item.Priority) || !validRequiredScore(item.Impact) || !validRequiredScore(item.Uncertainty) || !validRequiredScore(item.Novelty) {
			return invalid("invalid question %q", item.ClientKey)
		}
		questionParents[item.ClientKey] = item.ParentClientKey
	}
	if err := validateV6ParentGraph("question", questionParents, entities["question"]); err != nil {
		return err
	}
	for _, item := range result.Hypotheses {
		if !claimV6Key(entities["hypothesis"], item.ClientKey) || !entities["question"][item.QuestionKey] || !validV6Text(item.Statement) || item.Applicability == nil || !validV6TextList(item.ExpectedObservations, 1, 64) || !validV6TextList(item.WeakeningConditions, 1, 64) || !validOptionalScore(item.ConfidenceLow) || !validOptionalScore(item.ConfidenceHigh) || (item.ConfidenceLow != nil && item.ConfidenceHigh != nil && *item.ConfidenceLow > *item.ConfidenceHigh) {
			return invalid("invalid hypothesis %q", item.ClientKey)
		}
	}
	branchParents := map[string]string{}
	budget := 0.0
	for _, item := range result.Branches {
		if !claimV6Key(entities["branch"], item.ClientKey) || !optionalV6Key(item.ParentBranchKey) || !validV6Text(item.Objective) || !validV6TextList(item.EntryConditions, 1, 64) || !validV6TextList(item.ExitConditions, 1, 64) || !validRequiredScore(item.BudgetShare) {
			return invalid("invalid branch %q", item.ClientKey)
		}
		budget += *item.BudgetShare
		branchParents[item.ClientKey] = item.ParentBranchKey
	}
	if budget > 1.000000001 {
		return invalid("branch budget_share sum exceeds 1")
	}
	if err := validateV6ParentGraph("branch", branchParents, entities["branch"]); err != nil {
		return err
	}
	for _, task := range result.Tasks {
		if !claimV6Key(entities["task"], task.ClientKey) || !oneOf(task.Kind, "discover", "deep_read", "verify", "counter_search", "integrate", "deliberate", "diverge", "synthesize", "quality_gate", "citation_audit") || !validV6Text(task.Objective) || !validV6Key(task.RequiredCapability) || !oneOf(task.ExpectedResult, "research_evidence_v6", "research_integration_v6", "research_deliberation_v6", "research_divergence_v6", "research_report_v6", "research_quality_evaluation_v6", "research_citation_audit_v6") || !validRequiredScore(task.Priority) || len(task.Targets) < 1 || len(task.Targets) > 32 || (task.MaxAttempts != nil && (*task.MaxAttempts < 1 || *task.MaxAttempts > 16)) || (task.TimeoutSeconds != nil && (*task.TimeoutSeconds < 1 || *task.TimeoutSeconds > 86400)) {
			return invalid("invalid task %q", task.ClientKey)
		}
	}
	for _, task := range result.Tasks {
		hasInquiryTarget := false
		for _, target := range task.Targets {
			if !resolveV6InitialEntity(target, entities) {
				return invalid("task %q has unresolved target %s:%s", task.ClientKey, target.Kind, target.Key)
			}
			if target.Kind == "question" || target.Kind == "hypothesis" || target.Kind == "branch" {
				hasInquiryTarget = true
			}
		}
		if !hasInquiryTarget {
			return invalid("task %q has no Inquiry target", task.ClientKey)
		}
		seen := map[string]bool{}
		for _, dependency := range task.DependsOn {
			if !entities["task"][dependency] || dependency == task.ClientKey || seen[dependency] {
				return invalid("task %q has invalid dependency %q", task.ClientKey, dependency)
			}
			seen[dependency] = true
		}
	}
	if err := validateV6TaskDAG(result.Tasks); err != nil {
		return err
	}
	edgeKeys := map[string]bool{}
	for _, edge := range result.InquiryEdges {
		if !claimV6Key(edgeKeys, edge.ClientKey) || !resolveV6InitialEntity(edge.From, entities) || !resolveV6InitialEntity(edge.To, entities) || edge.From == edge.To || !oneOf(edge.Relation, "decomposes", "tests", "explains", "depends_on", "competes_with", "refines", "invalidates", "motivates") || !validV6Text(edge.Rationale) {
			return invalid("invalid inquiry edge %q", edge.ClientKey)
		}
	}
	if err := validateV6InquiryDAG(result.InquiryEdges); err != nil {
		return err
	}
	searchKeys := map[string]bool{}
	for _, plan := range result.SearchPlans {
		if !claimV6Key(searchKeys, plan.ClientKey) || len(plan.Targets) < 1 || !validV6Key(plan.Adapter) || !validV6Text(plan.QueryStrategy) || !validV6TextList(plan.InclusionCriteria, 1, 0) || !validV6TextList(plan.ExclusionCriteria, 1, 0) || !validV6TextList(plan.StoppingConditions, 1, 0) || !validV6Key(plan.StrategyVersion) {
			return invalid("invalid search plan %q", plan.ClientKey)
		}
		for _, target := range plan.Targets {
			if !resolveV6InitialEntity(target, entities) {
				return invalid("search plan %q has unresolved target", plan.ClientKey)
			}
		}
		for _, language := range plan.Languages {
			if !validV6Key(language) {
				return invalid("search plan %q has invalid language", plan.ClientKey)
			}
		}
		for _, domain := range plan.Domains {
			if utf8.RuneCountInString(domain) > 253 {
				return invalid("search plan %q has oversized domain", plan.ClientKey)
			}
		}
	}
	return nil
}

func validateV6InquiryDAG(edges []ResearchV6InquiryEdge) error {
	graph := map[string][]string{}
	for _, edge := range edges {
		if !oneOf(edge.Relation, "decomposes", "depends_on", "refines") {
			continue
		}
		from := edge.From.Kind + ":" + edge.From.Key
		to := edge.To.Kind + ":" + edge.To.Key
		graph[from] = append(graph[from], to)
		if _, exists := graph[to]; !exists {
			graph[to] = nil
		}
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) bool
	visit = func(key string) bool {
		if visiting[key] {
			return false
		}
		if visited[key] {
			return true
		}
		visiting[key] = true
		for _, next := range graph[key] {
			if !visit(next) {
				return false
			}
		}
		delete(visiting, key)
		visited[key] = true
		return true
	}
	for key := range graph {
		if !visit(key) {
			return fmt.Errorf("%w: inquiry dependency cycle", ErrInvalidResult)
		}
	}
	return nil
}

func optionalV6Key(value string) bool { return value == "" || validV6Key(value) }
func validV6Text(value string) bool {
	return utf8.RuneCountInString(value) >= 1 && utf8.RuneCountInString(value) <= 32768 && strings.TrimSpace(value) != ""
}
func claimV6Key(set map[string]bool, key string) bool {
	if !validV6Key(key) || set[key] {
		return false
	}
	set[key] = true
	return true
}
func validRequiredScore(value *float64) bool { return value != nil && *value >= 0 && *value <= 1 }
func validOptionalScore(value *float64) bool { return value == nil || (*value >= 0 && *value <= 1) }
func validV6TextList(values []string, min, max int) bool {
	if len(values) < min || (max > 0 && len(values) > max) {
		return false
	}
	for _, value := range values {
		if !validV6Text(value) {
			return false
		}
	}
	return true
}
func oneOf(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}

func resolveV6InitialEntity(ref ResearchV6EntityRef, entities map[string]map[string]bool) bool {
	set, ok := entities[ref.Kind]
	return ok && validV6Key(ref.Key) && set[ref.Key]
}

func validateV6ParentGraph(kind string, parents map[string]string, known map[string]bool) error {
	for key, parent := range parents {
		if parent == "" {
			continue
		}
		if !known[parent] || parent == key {
			return fmt.Errorf("%w: %s %q has invalid parent %q", ErrInvalidResult, kind, key, parent)
		}
		seen := map[string]bool{key: true}
		for current := parent; current != ""; current = parents[current] {
			if seen[current] {
				return fmt.Errorf("%w: %s parent cycle", ErrInvalidResult, kind)
			}
			seen[current] = true
		}
	}
	return nil
}

func validateV6TaskDAG(tasks []ResearchV6PlanTask) error {
	dependencies := make(map[string][]string, len(tasks))
	for _, task := range tasks {
		dependencies[task.ClientKey] = task.DependsOn
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) bool
	visit = func(key string) bool {
		if visiting[key] {
			return false
		}
		if visited[key] {
			return true
		}
		visiting[key] = true
		for _, dependency := range dependencies[key] {
			if !visit(dependency) {
				return false
			}
		}
		delete(visiting, key)
		visited[key] = true
		return true
	}
	for key := range dependencies {
		if !visit(key) {
			return fmt.Errorf("%w: task dependency cycle", ErrInvalidResult)
		}
	}
	return nil
}
