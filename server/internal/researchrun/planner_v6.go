package researchrun

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"
)

const v6TextMaxBytes = 32768

var v6KeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)

// V6PlannerResult mirrors the frozen plan_result definition in
// docs/contracts/research-run-v6.schema.json. V6 remains unavailable to
// production Runs until the complete protocol is accepted.
type V6PlannerResult struct {
	ContractKind    string              `json:"contract_kind"`
	SchemaVersion   int                 `json:"schema_version"`
	ClientRequestID string              `json:"client_request_id"`
	Summary         string              `json:"summary"`
	Questions       []QuestionProposal  `json:"questions"`
	Hypotheses      []V6HypothesisSeed  `json:"hypotheses"`
	Branches        []V6BranchSeed      `json:"branches"`
	Edges           []V6InquiryEdgeSeed `json:"inquiry_edges"`
	Tasks           []V6PlannerTask     `json:"tasks"`
	SearchPlans     []V6SearchPlanSeed  `json:"search_plans"`
	Method          json.RawMessage     `json:"method"`
}

type V6PlannerValidationContext struct {
	GoalVersion            int
	ContractRevisionHash   string
	AuthorizedBranchBudget float64
}

type V6HypothesisSeed struct {
	ClientKey            string          `json:"client_key"`
	QuestionKey          string          `json:"question_key"`
	Statement            string          `json:"statement"`
	Applicability        json.RawMessage `json:"applicability"`
	ExpectedObservations []string        `json:"expected_observations"`
	WeakeningConditions  []string        `json:"weakening_conditions"`
	ConfidenceLow        *float64        `json:"confidence_low,omitempty"`
	ConfidenceHigh       *float64        `json:"confidence_high,omitempty"`
}

type V6BranchSeed struct {
	ClientKey       string   `json:"client_key"`
	ParentBranchKey string   `json:"parent_branch_key,omitempty"`
	Objective       string   `json:"objective"`
	EntryConditions []string `json:"entry_conditions"`
	ExitConditions  []string `json:"exit_conditions"`
	BudgetShare     float64  `json:"budget_share"`
}

type V6EntityRef struct {
	Kind string `json:"kind"`
	Key  string `json:"key"`
}

type V6InquiryEdgeSeed struct {
	ClientKey string      `json:"client_key"`
	From      V6EntityRef `json:"from"`
	To        V6EntityRef `json:"to"`
	Relation  string      `json:"relation"`
	Rationale string      `json:"rationale"`
}

type V6PlannerTask struct {
	ClientKey          string          `json:"client_key"`
	Kind               string          `json:"kind"`
	Objective          string          `json:"objective"`
	RequiredCapability string          `json:"required_capability"`
	ExpectedResult     string          `json:"expected_result"`
	Priority           float64         `json:"priority"`
	Targets            []V6EntityRef   `json:"targets"`
	DependsOn          []string        `json:"depends_on,omitempty"`
	AcceptanceCriteria json.RawMessage `json:"acceptance_criteria,omitempty"`
	MaxAttempts        int             `json:"max_attempts,omitempty"`
	TimeoutSeconds     int             `json:"timeout_seconds,omitempty"`
}

type V6SearchPlanSeed struct {
	ClientKey          string          `json:"client_key"`
	Targets            []V6EntityRef   `json:"targets"`
	Adapter            string          `json:"adapter"`
	QueryStrategy      string          `json:"query_strategy"`
	TimeWindow         json.RawMessage `json:"time_window,omitempty"`
	Languages          []string        `json:"languages,omitempty"`
	Domains            []string        `json:"domains,omitempty"`
	InclusionCriteria  []string        `json:"inclusion_criteria"`
	ExclusionCriteria  []string        `json:"exclusion_criteria"`
	StoppingConditions []string        `json:"stopping_conditions"`
	StrategyVersion    string          `json:"strategy_version"`
}

func DecodeAndValidateV6PlannerResult(raw json.RawMessage, cfg RunConfig, context V6PlannerValidationContext) (V6PlannerResult, string, error) {
	if len(raw) == 0 || len(raw) > cfg.MaxResultBytes {
		return V6PlannerResult{}, "", fmt.Errorf("%w: result size must be between 1 and %d bytes", ErrInvalidResult, cfg.MaxResultBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var result V6PlannerResult
	if err := decoder.Decode(&result); err != nil {
		return V6PlannerResult{}, "", fmt.Errorf("%w: decode v6 planner result: %v", ErrInvalidResult, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return V6PlannerResult{}, "", fmt.Errorf("%w: v6 planner result has trailing JSON", ErrInvalidResult)
	}
	if err := result.Validate(cfg, context); err != nil {
		return V6PlannerResult{}, "", err
	}
	canonical, err := json.Marshal(struct {
		GoalVersion          int             `json:"goal_version"`
		ContractRevisionHash string          `json:"contract_revision_hash"`
		Result               V6PlannerResult `json:"result"`
	}{GoalVersion: context.GoalVersion, ContractRevisionHash: strings.ToLower(strings.TrimSpace(context.ContractRevisionHash)), Result: result})
	if err != nil {
		return V6PlannerResult{}, "", fmt.Errorf("%w: canonicalize v6 planner result: %v", ErrInvalidResult, err)
	}
	hash := sha256.Sum256(canonical)
	return result, hex.EncodeToString(hash[:]), nil
}

func (r V6PlannerResult) Validate(cfg RunConfig, context V6PlannerValidationContext) error {
	if r.ContractKind != "plan_result" || r.SchemaVersion != 6 {
		return fmt.Errorf("%w: v6 planner requires plan_result schema_version 6", ErrInvalidResult)
	}
	if _, err := uuid.Parse(r.ClientRequestID); err != nil {
		return fmt.Errorf("%w: v6 planner client_request_id must be a UUID", ErrInvalidResult)
	}
	if strings.TrimSpace(r.Summary) == "" || len(r.Summary) > v6TextMaxBytes {
		return fmt.Errorf("%w: v6 planner summary is invalid", ErrInvalidResult)
	}
	contractHash := strings.ToLower(strings.TrimSpace(context.ContractRevisionHash))
	if context.GoalVersion < 1 || !isSHA256Hex(contractHash) || !unitInterval(context.AuthorizedBranchBudget) {
		return fmt.Errorf("%w: assigned V6 Contract context is invalid", ErrInvalidResult)
	}
	if !isJSONObject(r.Method) {
		return fmt.Errorf("%w: v6 planner method must be an object", ErrInvalidResult)
	}
	if len(r.Questions) == 0 || r.Hypotheses == nil || len(r.Branches) == 0 || r.Edges == nil || len(r.Tasks) == 0 || len(r.SearchPlans) == 0 {
		return fmt.Errorf("%w: v6 planner requires questions, branches, tasks, and Search Plans", ErrInvalidResult)
	}
	if len(r.Questions) > 256 || len(r.Hypotheses) > 256 || len(r.Branches) > 128 || len(r.Edges) > 1024 || len(r.Tasks) > 256 || len(r.Tasks) > cfg.MaxTasks || len(r.SearchPlans) > 128 {
		return fmt.Errorf("%w: v6 planner collection exceeds configured limit", ErrInvalidResult)
	}

	entities := make(map[V6EntityRef]struct{}, len(r.Questions)+len(r.Hypotheses)+len(r.Branches))
	questionKeys := make(map[string]struct{}, len(r.Questions))
	for _, question := range r.Questions {
		if err := validateV6Question(question, questionKeys); err != nil {
			return err
		}
		entities[V6EntityRef{Kind: "question", Key: question.ClientKey}] = struct{}{}
	}
	for _, question := range r.Questions {
		if question.ParentClientKey != "" {
			if _, ok := questionKeys[question.ParentClientKey]; !ok || question.ParentClientKey == question.ClientKey {
				return fmt.Errorf("%w: question %q references invalid parent", ErrInvalidResult, question.ClientKey)
			}
		}
	}
	if hasV6QuestionCycle(r.Questions) {
		return fmt.Errorf("%w: v6 question hierarchy contains a cycle", ErrInvalidResult)
	}

	hypothesisKeys := make(map[string]struct{}, len(r.Hypotheses))
	for _, hypothesis := range r.Hypotheses {
		if err := validateV6Key("hypothesis.client_key", hypothesis.ClientKey); err != nil {
			return err
		}
		if _, duplicate := hypothesisKeys[hypothesis.ClientKey]; duplicate {
			return fmt.Errorf("%w: duplicate hypothesis key %q", ErrInvalidResult, hypothesis.ClientKey)
		}
		if _, ok := questionKeys[hypothesis.QuestionKey]; !ok || strings.TrimSpace(hypothesis.Statement) == "" || len(hypothesis.Statement) > v6TextMaxBytes || !isJSONObject(hypothesis.Applicability) {
			return fmt.Errorf("%w: hypothesis %q is invalid", ErrInvalidResult, hypothesis.ClientKey)
		}
		for name, values := range map[string][]string{"expected_observations": hypothesis.ExpectedObservations, "weakening_conditions": hypothesis.WeakeningConditions} {
			if len(values) == 0 || len(values) > 64 {
				return fmt.Errorf("%w: hypothesis %q requires bounded %s", ErrInvalidResult, hypothesis.ClientKey, name)
			}
			if err := validateV6TextList("hypothesis."+name, values, 64); err != nil {
				return err
			}
		}
		if hypothesis.ConfidenceLow != nil && !unitInterval(*hypothesis.ConfidenceLow) || hypothesis.ConfidenceHigh != nil && !unitInterval(*hypothesis.ConfidenceHigh) || hypothesis.ConfidenceLow != nil && hypothesis.ConfidenceHigh != nil && *hypothesis.ConfidenceLow > *hypothesis.ConfidenceHigh {
			return fmt.Errorf("%w: hypothesis %q confidence interval is invalid", ErrInvalidResult, hypothesis.ClientKey)
		}
		hypothesisKeys[hypothesis.ClientKey] = struct{}{}
		entities[V6EntityRef{Kind: "hypothesis", Key: hypothesis.ClientKey}] = struct{}{}
	}

	branchKeys := make(map[string]V6BranchSeed, len(r.Branches))
	budget := 0.0
	for _, branch := range r.Branches {
		if err := validateV6Key("branch.client_key", branch.ClientKey); err != nil {
			return err
		}
		if _, duplicate := branchKeys[branch.ClientKey]; duplicate {
			return fmt.Errorf("%w: duplicate branch key %q", ErrInvalidResult, branch.ClientKey)
		}
		if strings.TrimSpace(branch.Objective) == "" || len(branch.Objective) > v6TextMaxBytes || !unitInterval(branch.BudgetShare) || len(branch.EntryConditions) == 0 || len(branch.EntryConditions) > 64 || len(branch.ExitConditions) == 0 || len(branch.ExitConditions) > 64 {
			return fmt.Errorf("%w: branch %q is invalid", ErrInvalidResult, branch.ClientKey)
		}
		if err := validateV6TextList("branch.entry_conditions", branch.EntryConditions, 64); err != nil {
			return err
		}
		if err := validateV6TextList("branch.exit_conditions", branch.ExitConditions, 64); err != nil {
			return err
		}
		branchKeys[branch.ClientKey] = branch
		entities[V6EntityRef{Kind: "branch", Key: branch.ClientKey}] = struct{}{}
		budget += branch.BudgetShare
	}
	if budget > context.AuthorizedBranchBudget+1e-9 || hasV6BranchCycle(branchKeys) {
		return fmt.Errorf("%w: v6 branch forest exceeds authorization or contains a cycle", ErrInvalidResult)
	}
	for _, branch := range r.Branches {
		if branch.ParentBranchKey != "" {
			if _, ok := branchKeys[branch.ParentBranchKey]; !ok || branch.ParentBranchKey == branch.ClientKey {
				return fmt.Errorf("%w: branch %q references invalid parent", ErrInvalidResult, branch.ClientKey)
			}
		}
	}

	seenEdgeKeys := map[string]struct{}{}
	seenEdges := map[string]struct{}{}
	acyclic := make(map[V6EntityRef][]V6EntityRef)
	graph := make(map[V6EntityRef][]V6EntityRef)
	for _, edge := range r.Edges {
		if err := validateV6Key("inquiry_edge.client_key", edge.ClientKey); err != nil {
			return err
		}
		if _, duplicate := seenEdgeKeys[edge.ClientKey]; duplicate {
			return fmt.Errorf("%w: duplicate inquiry edge key", ErrInvalidResult)
		}
		seenEdgeKeys[edge.ClientKey] = struct{}{}
		if _, ok := entities[edge.From]; !ok {
			return fmt.Errorf("%w: inquiry edge references unknown from endpoint", ErrInvalidResult)
		}
		if _, ok := entities[edge.To]; !ok || edge.From == edge.To {
			return fmt.Errorf("%w: inquiry edge references invalid to endpoint", ErrInvalidResult)
		}
		switch edge.Relation {
		case "decomposes", "tests", "explains", "depends_on", "competes_with", "refines", "invalidates", "motivates":
		default:
			return fmt.Errorf("%w: unsupported inquiry relation %q", ErrInvalidResult, edge.Relation)
		}
		if strings.TrimSpace(edge.Rationale) == "" || len(edge.Rationale) > v6TextMaxBytes {
			return fmt.Errorf("%w: inquiry edge requires bounded rationale", ErrInvalidResult)
		}
		identity := edge.From.Kind + "\x00" + edge.From.Key + "\x00" + edge.To.Kind + "\x00" + edge.To.Key + "\x00" + edge.Relation
		if _, duplicate := seenEdges[identity]; duplicate {
			return fmt.Errorf("%w: duplicate inquiry edge", ErrInvalidResult)
		}
		seenEdges[identity] = struct{}{}
		graph[edge.From] = append(graph[edge.From], edge.To)
		if edge.Relation == "decomposes" || edge.Relation == "depends_on" || edge.Relation == "refines" {
			acyclic[edge.From] = append(acyclic[edge.From], edge.To)
		}
	}
	if hasV6InquiryCycle(entities, acyclic) || !allV6InquiryEntitiesReachableFromQuestions(entities, graph) {
		return fmt.Errorf("%w: v6 inquiry graph contains a cycle or orphan entity", ErrInvalidResult)
	}

	taskKeys := make(map[string]V6PlannerTask, len(r.Tasks))
	for _, task := range r.Tasks {
		if err := validateV6PlannerTask(task, entities, cfg); err != nil {
			return err
		}
		if _, duplicate := taskKeys[task.ClientKey]; duplicate {
			return fmt.Errorf("%w: duplicate v6 task key %q", ErrInvalidResult, task.ClientKey)
		}
		taskKeys[task.ClientKey] = task
	}
	for _, task := range r.Tasks {
		seenDependencies := map[string]struct{}{}
		for _, dependency := range task.DependsOn {
			if _, ok := taskKeys[dependency]; !ok || dependency == task.ClientKey {
				return fmt.Errorf("%w: task %q has invalid dependency %q", ErrInvalidResult, task.ClientKey, dependency)
			}
			if _, duplicate := seenDependencies[dependency]; duplicate {
				return fmt.Errorf("%w: task %q repeats dependency %q", ErrInvalidResult, task.ClientKey, dependency)
			}
			seenDependencies[dependency] = struct{}{}
		}
	}
	if hasV6TaskCycle(taskKeys) {
		return fmt.Errorf("%w: v6 task dependency graph contains a cycle", ErrInvalidResult)
	}

	searchPlanKeys := map[string]struct{}{}
	for _, plan := range r.SearchPlans {
		if err := validateV6SearchPlan(plan, entities); err != nil {
			return err
		}
		if _, duplicate := searchPlanKeys[plan.ClientKey]; duplicate {
			return fmt.Errorf("%w: duplicate Search Plan key %q", ErrInvalidResult, plan.ClientKey)
		}
		searchPlanKeys[plan.ClientKey] = struct{}{}
	}
	return nil
}

func validateV6PlannerTask(task V6PlannerTask, entities map[V6EntityRef]struct{}, cfg RunConfig) error {
	if err := validateV6Key("task.client_key", task.ClientKey); err != nil {
		return err
	}
	allowedKinds := map[string]bool{"discover": true, "deep_read": true, "verify": true, "counter_search": true, "integrate": true, "deliberate": true, "diverge": true, "synthesize": true, "quality_gate": true, "citation_audit": true}
	allowedResults := map[string]bool{"research_evidence_v6": true, "research_integration_v6": true, "research_deliberation_v6": true, "research_divergence_v6": true, "research_report_v6": true, "research_quality_evaluation_v6": true, "research_citation_audit_v6": true}
	if !allowedKinds[task.Kind] || !allowedResults[task.ExpectedResult] || strings.TrimSpace(task.Objective) == "" || len(task.Objective) > v6TextMaxBytes || validateV6Key("task.required_capability", task.RequiredCapability) != nil || !unitInterval(task.Priority) {
		return fmt.Errorf("%w: v6 task %q is invalid", ErrInvalidResult, task.ClientKey)
	}
	if len(task.Targets) == 0 || len(task.Targets) > 32 {
		return fmt.Errorf("%w: v6 task %q requires bounded targets", ErrInvalidResult, task.ClientKey)
	}
	seenTargets := map[V6EntityRef]struct{}{}
	for _, target := range task.Targets {
		if _, ok := entities[target]; !ok {
			return fmt.Errorf("%w: v6 task %q targets unknown Inquiry entity", ErrInvalidResult, task.ClientKey)
		}
		if _, duplicate := seenTargets[target]; duplicate {
			return fmt.Errorf("%w: v6 task %q repeats target", ErrInvalidResult, task.ClientKey)
		}
		seenTargets[target] = struct{}{}
	}
	if len(task.DependsOn) > 128 || task.MaxAttempts < 0 || task.MaxAttempts > 16 || task.MaxAttempts > cfg.MaxAttemptsPerTask || task.TimeoutSeconds < 0 || task.TimeoutSeconds > 86400 || len(task.AcceptanceCriteria) > 0 && !isJSONObject(task.AcceptanceCriteria) {
		return fmt.Errorf("%w: v6 task %q limits or acceptance criteria are invalid", ErrInvalidResult, task.ClientKey)
	}
	return nil
}

func validateV6SearchPlan(plan V6SearchPlanSeed, entities map[V6EntityRef]struct{}) error {
	if err := validateV6Key("search_plan.client_key", plan.ClientKey); err != nil {
		return err
	}
	if len(plan.Targets) == 0 || !validV6Key(plan.Adapter) || !validV6Key(plan.StrategyVersion) || strings.TrimSpace(plan.QueryStrategy) == "" || len(plan.QueryStrategy) > v6TextMaxBytes {
		return fmt.Errorf("%w: Search Plan %q is invalid", ErrInvalidResult, plan.ClientKey)
	}
	for _, target := range plan.Targets {
		if _, ok := entities[target]; !ok {
			return fmt.Errorf("%w: Search Plan %q targets unknown Inquiry entity", ErrInvalidResult, plan.ClientKey)
		}
	}
	for name, values := range map[string][]string{"inclusion_criteria": plan.InclusionCriteria, "exclusion_criteria": plan.ExclusionCriteria, "stopping_conditions": plan.StoppingConditions} {
		if len(values) == 0 {
			return fmt.Errorf("%w: Search Plan %q requires %s", ErrInvalidResult, plan.ClientKey, name)
		}
		if err := validateV6TextList("search_plan."+name, values, 0); err != nil {
			return err
		}
	}
	if len(plan.TimeWindow) > 0 && !isJSONObject(plan.TimeWindow) {
		return fmt.Errorf("%w: Search Plan time_window must be an object", ErrInvalidResult)
	}
	for _, language := range plan.Languages {
		if !validV6Key(language) {
			return fmt.Errorf("%w: Search Plan language is invalid", ErrInvalidResult)
		}
	}
	for _, domain := range plan.Domains {
		if strings.TrimSpace(domain) == "" || len(domain) > 253 {
			return fmt.Errorf("%w: Search Plan domain is invalid", ErrInvalidResult)
		}
	}
	return nil
}

func validV6Key(value string) bool {
	return validateV6Key("v6.key", value) == nil
}

func validateV6Key(name, value string) error {
	if len(value) == 0 || len(value) > maxClientKeyBytes || !v6KeyPattern.MatchString(value) {
		return fmt.Errorf("%w: %s does not match the frozen V6 key contract", ErrInvalidResult, name)
	}
	return nil
}

func validateV6TextList(name string, values []string, limit int) error {
	if limit > 0 && len(values) > limit {
		return fmt.Errorf("%w: %s exceeds %d items", ErrInvalidResult, name, limit)
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" || len(value) > v6TextMaxBytes {
			return fmt.Errorf("%w: %s contains invalid text", ErrInvalidResult, name)
		}
	}
	return nil
}

func validateV6Question(question QuestionProposal, seen map[string]struct{}) error {
	if err := validateV6Key("question.client_key", question.ClientKey); err != nil {
		return err
	}
	if _, duplicate := seen[question.ClientKey]; duplicate {
		return fmt.Errorf("%w: duplicate question key %q", ErrInvalidResult, question.ClientKey)
	}
	seen[question.ClientKey] = struct{}{}
	if question.ParentClientKey != "" {
		if err := validateV6Key("question.parent_client_key", question.ParentClientKey); err != nil {
			return err
		}
	}
	switch question.Kind {
	case QuestionKindDimension, QuestionKindHypothesis, QuestionKindContradiction, QuestionKindGap, QuestionKindFollowUp:
	default:
		return fmt.Errorf("%w: invalid v6 question kind %q", ErrInvalidResult, question.Kind)
	}
	if strings.TrimSpace(question.Text) == "" || len(question.Text) > v6TextMaxBytes || !unitInterval(question.Priority) || !unitInterval(question.Impact) || !unitInterval(question.Uncertainty) || !unitInterval(question.Novelty) {
		return fmt.Errorf("%w: v6 question %q is invalid", ErrInvalidResult, question.ClientKey)
	}
	return nil
}

func isJSONObject(raw json.RawMessage) bool {
	if len(raw) == 0 || !json.Valid(raw) {
		return false
	}
	var value map[string]json.RawMessage
	return json.Unmarshal(raw, &value) == nil && value != nil
}

func isSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func hasV6QuestionCycle(questions []QuestionProposal) bool {
	parents := make(map[string]string, len(questions))
	for _, question := range questions {
		parents[question.ClientKey] = question.ParentClientKey
	}
	state := map[string]uint8{}
	var visit func(string) bool
	visit = func(key string) bool {
		if state[key] == 1 {
			return true
		}
		if state[key] == 2 {
			return false
		}
		state[key] = 1
		if parent := parents[key]; parent != "" && visit(parent) {
			return true
		}
		state[key] = 2
		return false
	}
	for key := range parents {
		if visit(key) {
			return true
		}
	}
	return false
}

func hasV6BranchCycle(branches map[string]V6BranchSeed) bool {
	state := map[string]uint8{}
	var visit func(string) bool
	visit = func(key string) bool {
		if state[key] == 1 {
			return true
		}
		if state[key] == 2 {
			return false
		}
		state[key] = 1
		if parent := branches[key].ParentBranchKey; parent != "" {
			if _, ok := branches[parent]; ok && visit(parent) {
				return true
			}
		}
		state[key] = 2
		return false
	}
	keys := make([]string, 0, len(branches))
	for key := range branches {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if visit(key) {
			return true
		}
	}
	return false
}

func hasV6InquiryCycle(entities map[V6EntityRef]struct{}, edges map[V6EntityRef][]V6EntityRef) bool {
	state := map[V6EntityRef]uint8{}
	var visit func(V6EntityRef) bool
	visit = func(ref V6EntityRef) bool {
		if state[ref] == 1 {
			return true
		}
		if state[ref] == 2 {
			return false
		}
		state[ref] = 1
		for _, next := range edges[ref] {
			if visit(next) {
				return true
			}
		}
		state[ref] = 2
		return false
	}
	for ref := range entities {
		if visit(ref) {
			return true
		}
	}
	return false
}

func allV6InquiryEntitiesReachableFromQuestions(entities map[V6EntityRef]struct{}, graph map[V6EntityRef][]V6EntityRef) bool {
	reached := make(map[V6EntityRef]struct{}, len(entities))
	queue := make([]V6EntityRef, 0, len(entities))
	for entity := range entities {
		if entity.Kind == "question" {
			reached[entity] = struct{}{}
			queue = append(queue, entity)
		}
	}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range graph[current] {
			if _, ok := reached[next]; !ok {
				reached[next] = struct{}{}
				queue = append(queue, next)
			}
		}
	}
	return len(reached) == len(entities)
}

func hasV6TaskCycle(tasks map[string]V6PlannerTask) bool {
	state := map[string]uint8{}
	var visit func(string) bool
	visit = func(key string) bool {
		if state[key] == 1 {
			return true
		}
		if state[key] == 2 {
			return false
		}
		state[key] = 1
		for _, dependency := range tasks[key].DependsOn {
			if visit(dependency) {
				return true
			}
		}
		state[key] = 2
		return false
	}
	for key := range tasks {
		if visit(key) {
			return true
		}
	}
	return false
}
