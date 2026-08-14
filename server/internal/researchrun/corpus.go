package researchrun

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

type corpusModule struct{}

type corpusTargetRef struct {
	Kind string
	ID   string
}

type searchPlanCommand struct {
	ClientKey          string
	Targets            []corpusTargetRef
	Adapter            string
	QueryStrategies    []string
	Languages          []string
	Scopes             []string
	InclusionCriteria  []string
	ExclusionCriteria  []string
	StoppingConditions []string
	WindowStart        *time.Time
	WindowEnd          *time.Time
	MaximumCost        float64
	StrategyVersion    string
}

type queryExecutionCommand struct {
	ClientKey          string
	SearchPlanKey      string
	ParentExecutionKey string
	Adapter            string
	Query              string
	CursorIn           string
	CursorOut          string
	StartedAt          time.Time
	FinishedAt         time.Time
	Outcome            string
	Cost               json.RawMessage
	Safety             json.RawMessage
	FailureClass       string
}

type sourceCandidateCommand struct {
	ClientKey          string
	QueryExecutionKey  string
	URL                string
	CanonicalIdentity  string
	ContentHash        string
	Title              string
	Summary            string
	Publisher          string
	IndependenceFamily string
	DuplicateCluster   string
	RiskFlags          []string
	ResultPosition     int
}

type screeningDecisionCommand struct {
	ClientKey           string
	CandidateKey        string
	Outcome             string
	Rule                string
	Reason              string
	OperatorKind        string
	OperatorID          string
	GoalVersion         int
	PlanVersion         int
	ReviewedAgainstPlan bool
}

type corpusBatchCommand struct {
	GoalVersion int
	PlanVersion int
	Plans       []searchPlanCommand
	Executions  []queryExecutionCommand
	Candidates  []sourceCandidateCommand
	Decisions   []screeningDecisionCommand
}

type corpusBatchValidation struct {
	IncludedCandidateKeys []string
}

func (corpusModule) ValidateBatch(command corpusBatchCommand) (corpusBatchValidation, error) {
	if command.GoalVersion < 1 || command.PlanVersion < 1 {
		return corpusBatchValidation{}, fmt.Errorf("%w: Corpus batch requires positive Contract versions", ErrInvalidContract)
	}
	if len(command.Plans) == 0 || len(command.Plans) > maxResultItems || len(command.Executions) == 0 || len(command.Executions) > maxResultItems || len(command.Candidates) > maxResultItems || len(command.Decisions) > maxResultItems {
		return corpusBatchValidation{}, fmt.Errorf("%w: Corpus batch collection is empty or exceeds its limit", ErrInvalidContract)
	}
	plans := make(map[string]searchPlanCommand, len(command.Plans))
	for _, plan := range command.Plans {
		if err := validateKey("search_plan.client_key", plan.ClientKey); err != nil {
			return corpusBatchValidation{}, err
		}
		if _, duplicate := plans[plan.ClientKey]; duplicate {
			return corpusBatchValidation{}, fmt.Errorf("%w: duplicate Search Plan key %q", ErrInvalidContract, plan.ClientKey)
		}
		if err := validateSearchPlan(plan); err != nil {
			return corpusBatchValidation{}, err
		}
		plans[plan.ClientKey] = plan
	}

	executions := make(map[string]queryExecutionCommand, len(command.Executions))
	for _, execution := range command.Executions {
		if err := validateKey("query_execution.client_key", execution.ClientKey); err != nil {
			return corpusBatchValidation{}, err
		}
		if _, duplicate := executions[execution.ClientKey]; duplicate {
			return corpusBatchValidation{}, fmt.Errorf("%w: duplicate Query Execution key %q", ErrInvalidContract, execution.ClientKey)
		}
		plan, ok := plans[execution.SearchPlanKey]
		if !ok {
			return corpusBatchValidation{}, fmt.Errorf("%w: Query Execution %q references unknown Search Plan", ErrInvalidContract, execution.ClientKey)
		}
		if err := validateQueryExecution(execution, plan); err != nil {
			return corpusBatchValidation{}, err
		}
		executions[execution.ClientKey] = execution
	}
	for _, execution := range command.Executions {
		if execution.ParentExecutionKey == "" {
			continue
		}
		parent, ok := executions[execution.ParentExecutionKey]
		if !ok || parent.SearchPlanKey != execution.SearchPlanKey || parent.ClientKey == execution.ClientKey {
			return corpusBatchValidation{}, fmt.Errorf("%w: Query Execution %q has invalid rewrite parent", ErrInvalidContract, execution.ClientKey)
		}
	}
	if hasQueryRewriteCycle(executions) {
		return corpusBatchValidation{}, fmt.Errorf("%w: Query Execution rewrite graph contains a cycle", ErrInvalidContract)
	}

	candidates := make(map[string]sourceCandidateCommand, len(command.Candidates))
	canonicalIdentities := make(map[string]sourceCandidateCommand, len(command.Candidates))
	for _, candidate := range command.Candidates {
		if err := validateKey("source_candidate.client_key", candidate.ClientKey); err != nil {
			return corpusBatchValidation{}, err
		}
		if _, duplicate := candidates[candidate.ClientKey]; duplicate {
			return corpusBatchValidation{}, fmt.Errorf("%w: duplicate Source Candidate key %q", ErrInvalidContract, candidate.ClientKey)
		}
		execution, ok := executions[candidate.QueryExecutionKey]
		if !ok || execution.Outcome != "succeeded" && execution.Outcome != "partial" {
			return corpusBatchValidation{}, fmt.Errorf("%w: Source Candidate %q requires a result-bearing Query Execution", ErrInvalidContract, candidate.ClientKey)
		}
		if err := validateSourceCandidate(candidate); err != nil {
			return corpusBatchValidation{}, err
		}
		if previous, duplicate := canonicalIdentities[candidate.CanonicalIdentity]; duplicate {
			if candidate.DuplicateCluster == "" || candidate.DuplicateCluster != previous.DuplicateCluster || candidate.IndependenceFamily != previous.IndependenceFamily {
				return corpusBatchValidation{}, fmt.Errorf("%w: duplicate Source Candidates require one cluster and independence family", ErrInvalidContract)
			}
		}
		canonicalIdentities[candidate.CanonicalIdentity] = candidate
		candidates[candidate.ClientKey] = candidate
	}

	decided := make(map[string]struct{}, len(command.Decisions))
	decisionKeys := make(map[string]struct{}, len(command.Decisions))
	validation := corpusBatchValidation{}
	for _, decision := range command.Decisions {
		if err := validateKey("screening_decision.client_key", decision.ClientKey); err != nil {
			return corpusBatchValidation{}, err
		}
		if _, duplicate := decisionKeys[decision.ClientKey]; duplicate {
			return corpusBatchValidation{}, fmt.Errorf("%w: duplicate Screening Decision key %q", ErrInvalidContract, decision.ClientKey)
		}
		decisionKeys[decision.ClientKey] = struct{}{}
		if _, ok := candidates[decision.CandidateKey]; !ok {
			return corpusBatchValidation{}, fmt.Errorf("%w: Screening Decision references unknown Source Candidate", ErrInvalidContract)
		}
		if _, duplicate := decided[decision.CandidateKey]; duplicate {
			return corpusBatchValidation{}, fmt.Errorf("%w: Source Candidate has multiple Screening Decisions in one batch", ErrInvalidContract)
		}
		decided[decision.CandidateKey] = struct{}{}
		if err := validateScreeningDecision(decision, command.GoalVersion, command.PlanVersion); err != nil {
			return corpusBatchValidation{}, err
		}
		if decision.Outcome == "include" {
			validation.IncludedCandidateKeys = append(validation.IncludedCandidateKeys, decision.CandidateKey)
		}
	}
	sort.Strings(validation.IncludedCandidateKeys)
	return validation, nil
}

func validateSearchPlan(plan searchPlanCommand) error {
	if len(plan.Targets) == 0 || len(plan.Targets) > 64 {
		return fmt.Errorf("%w: Search Plan requires bounded Inquiry targets", ErrInvalidContract)
	}
	seenTargets := map[corpusTargetRef]struct{}{}
	for _, target := range plan.Targets {
		if target.Kind != "question" && target.Kind != "branch" {
			return fmt.Errorf("%w: Search Plan target kind %q is invalid", ErrInvalidContract, target.Kind)
		}
		if _, err := uuid.Parse(target.ID); err != nil {
			return fmt.Errorf("%w: Search Plan target is not resolved", ErrInvalidContract)
		}
		if _, duplicate := seenTargets[target]; duplicate {
			return fmt.Errorf("%w: duplicate Search Plan target", ErrInvalidContract)
		}
		seenTargets[target] = struct{}{}
	}
	if !validCorpusToken(plan.Adapter, 160) || !validCorpusToken(plan.StrategyVersion, 160) || plan.MaximumCost < 0 {
		return fmt.Errorf("%w: Search Plan adapter, strategy version, or cost is invalid", ErrInvalidContract)
	}
	for name, values := range map[string][]string{
		"query_strategies": plan.QueryStrategies, "languages": plan.Languages,
		"inclusion_criteria": plan.InclusionCriteria, "exclusion_criteria": plan.ExclusionCriteria,
		"stopping_conditions": plan.StoppingConditions,
	} {
		if len(values) == 0 || len(values) > 64 {
			return fmt.Errorf("%w: Search Plan requires bounded %s", ErrInvalidContract, name)
		}
		if err := validateStringList("search_plan."+name, values); err != nil {
			return err
		}
	}
	if len(plan.Scopes) > 64 {
		return fmt.Errorf("%w: Search Plan scopes exceed limit", ErrInvalidContract)
	}
	if len(plan.Scopes) > 0 {
		if err := validateStringList("search_plan.scopes", plan.Scopes); err != nil {
			return err
		}
	}
	if plan.WindowStart != nil && plan.WindowEnd != nil && plan.WindowStart.After(*plan.WindowEnd) {
		return fmt.Errorf("%w: Search Plan time window is inverted", ErrInvalidContract)
	}
	return nil
}

func validateQueryExecution(execution queryExecutionCommand, plan searchPlanCommand) error {
	if execution.Adapter != plan.Adapter || strings.TrimSpace(execution.Query) == "" || len(execution.Query) > maxTaskObjectiveBytes || execution.StartedAt.IsZero() || execution.FinishedAt.IsZero() || execution.FinishedAt.Before(execution.StartedAt) {
		return fmt.Errorf("%w: Query Execution %q does not match its Search Plan", ErrInvalidContract, execution.ClientKey)
	}
	if !isCorpusJSONObject(execution.Cost) || len(execution.Cost) > maxMetadataBytes || len(execution.Safety) > 0 && (!isCorpusJSONObject(execution.Safety) || len(execution.Safety) > maxMetadataBytes) {
		return fmt.Errorf("%w: Query Execution %q cost or safety metadata is invalid", ErrInvalidContract, execution.ClientKey)
	}
	switch execution.Outcome {
	case "succeeded":
		if execution.FailureClass != "" {
			return fmt.Errorf("%w: succeeded Query Execution cannot carry failure_class", ErrInvalidContract)
		}
	case "partial":
	case "failed", "blocked":
		if !validCorpusToken(execution.FailureClass, 160) {
			return fmt.Errorf("%w: failed or blocked Query Execution requires failure_class", ErrInvalidContract)
		}
	default:
		return fmt.Errorf("%w: Query Execution outcome %q is invalid", ErrInvalidContract, execution.Outcome)
	}
	if len(execution.CursorIn) > 4096 || len(execution.CursorOut) > 4096 {
		return fmt.Errorf("%w: Query Execution cursor exceeds limit", ErrInvalidContract)
	}
	return nil
}

func validateSourceCandidate(candidate sourceCandidateCommand) error {
	canonicalURL, err := CanonicalURL(candidate.URL)
	if err != nil || canonicalURL != candidate.URL {
		return fmt.Errorf("%w: Source Candidate %q URL is not canonical", ErrInvalidContract, candidate.ClientKey)
	}
	if !validCorpusToken(candidate.CanonicalIdentity, 512) || !validCorpusToken(candidate.IndependenceFamily, 160) || !validSHA256Prefixed(candidate.ContentHash) || candidate.ResultPosition < 1 {
		return fmt.Errorf("%w: Source Candidate %q identity, family, or position is invalid", ErrInvalidContract, candidate.ClientKey)
	}
	if strings.TrimSpace(candidate.Title) == "" || len(candidate.Title) > 4096 || strings.TrimSpace(candidate.Summary) == "" || len(candidate.Summary) > maxTaskObjectiveBytes || len(candidate.Publisher) > 4096 {
		return fmt.Errorf("%w: Source Candidate %q presentation is invalid", ErrInvalidContract, candidate.ClientKey)
	}
	if len(candidate.DuplicateCluster) > 512 || len(candidate.RiskFlags) > 64 {
		return fmt.Errorf("%w: Source Candidate %q duplicate or risk metadata exceeds limit", ErrInvalidContract, candidate.ClientKey)
	}
	if len(candidate.RiskFlags) > 0 {
		if err := validateStringList("source_candidate.risk_flags", candidate.RiskFlags); err != nil {
			return err
		}
	}
	return nil
}

func validateScreeningDecision(decision screeningDecisionCommand, goalVersion, planVersion int) error {
	if decision.GoalVersion != goalVersion || decision.PlanVersion != planVersion {
		return fmt.Errorf("%w: Screening Decision method version is stale", ErrControlTargetChanged)
	}
	switch decision.Outcome {
	case "include", "exclude", "duplicate", "unsafe":
	default:
		return fmt.Errorf("%w: Screening Decision outcome %q is invalid", ErrInvalidContract, decision.Outcome)
	}
	if !validCorpusToken(decision.Rule, 160) || strings.TrimSpace(decision.Reason) == "" || len(decision.Reason) > maxTaskObjectiveBytes {
		return fmt.Errorf("%w: Screening Decision requires rule and reason", ErrInvalidContract)
	}
	if decision.OperatorKind != "agent" && decision.OperatorKind != "user" && decision.OperatorKind != "system" {
		return fmt.Errorf("%w: Screening Decision operator kind is invalid", ErrInvalidContract)
	}
	if _, err := uuid.Parse(decision.OperatorID); err != nil {
		return fmt.Errorf("%w: Screening Decision operator is not resolved", ErrInvalidContract)
	}
	if !decision.ReviewedAgainstPlan {
		return fmt.Errorf("%w: Screening Decision was not reviewed against its Search Plan", ErrInvalidContract)
	}
	return nil
}

func isCorpusJSONObject(raw json.RawMessage) bool {
	if len(raw) == 0 || !json.Valid(raw) {
		return false
	}
	var value map[string]json.RawMessage
	return json.Unmarshal(raw, &value) == nil && value != nil
}

func validSHA256Prefixed(value string) bool {
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

func validCorpusToken(value string, limit int) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= limit
}

func hasQueryRewriteCycle(executions map[string]queryExecutionCommand) bool {
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
		if parent := executions[key].ParentExecutionKey; parent != "" && visit(parent) {
			return true
		}
		state[key] = 2
		return false
	}
	for key := range executions {
		if visit(key) {
			return true
		}
	}
	return false
}

type sourceAdmissionCommand struct {
	CandidateID         string
	ScreeningDecisionID string
	ScreeningOutcome    string
	DirectIngestionKind string
}

func (corpusModule) ValidateSourceAdmission(command sourceAdmissionCommand) error {
	retrieved := command.CandidateID != "" || command.ScreeningDecisionID != ""
	direct := command.DirectIngestionKind != ""
	if retrieved == direct {
		return fmt.Errorf("%w: source admission must be retrieved or direct", ErrInvalidContract)
	}
	if retrieved {
		if _, err := uuid.Parse(command.CandidateID); err != nil {
			return fmt.Errorf("%w: source admission Candidate is unresolved", ErrInvalidContract)
		}
		if _, err := uuid.Parse(command.ScreeningDecisionID); err != nil {
			return fmt.Errorf("%w: source admission Screening Decision is unresolved", ErrInvalidContract)
		}
		if command.ScreeningOutcome != "include" {
			return fmt.Errorf("%w: retrieved source requires an include Screening Decision", ErrInvalidContract)
		}
		return nil
	}
	if command.ScreeningOutcome != "" {
		return fmt.Errorf("%w: direct source cannot carry a Screening Decision outcome", ErrInvalidContract)
	}
	switch command.DirectIngestionKind {
	case "user_document", "workspace_artifact", "agent_observation", "tool_output":
		return nil
	default:
		return fmt.Errorf("%w: direct source ingestion kind is invalid", ErrInvalidContract)
	}
}
