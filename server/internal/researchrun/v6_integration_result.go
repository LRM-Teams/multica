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

type V6QuestionProposal struct {
	ClientKey       string  `json:"client_key"`
	ParentClientKey *string `json:"parent_client_key,omitempty"`
	Text            string  `json:"text"`
	Kind            string  `json:"kind"`
	Required        bool    `json:"required"`
	Priority        float64 `json:"priority"`
	Impact          float64 `json:"impact"`
	Uncertainty     float64 `json:"uncertainty"`
	Novelty         float64 `json:"novelty"`
}

type V6IntegrationContribution struct {
	ClientKey          string               `json:"client_key"`
	IntegrationRoundID string               `json:"integration_round_id"`
	ComparedArtifacts  []V6EntityRef        `json:"compared_artifacts"`
	CommonFindings     []string             `json:"common_findings"`
	UniqueFindings     []string             `json:"unique_findings"`
	Conflicts          []string             `json:"conflicts"`
	Scope              map[string]any       `json:"scope"`
	Omissions          []string             `json:"omissions"`
	ProposedInsights   []string             `json:"proposed_insights"`
	FollowUpQuestions  []V6QuestionProposal `json:"follow_up_questions"`
}

type V6InsightProposal struct {
	ClientKey     string         `json:"client_key"`
	Title         string         `json:"title"`
	Summary       string         `json:"summary"`
	Inputs        []V6EntityRef  `json:"inputs"`
	Relation      string         `json:"relation"`
	Scope         map[string]any `json:"scope"`
	SemanticValue string         `json:"semantic_value"`
}

type V6StatusUpdate struct {
	Target       V6EntityRef   `json:"target"`
	Before       string        `json:"before"`
	After        string        `json:"after"`
	Reason       string        `json:"reason"`
	EvidenceRefs []V6EntityRef `json:"evidence_refs"`
}

type V6DisputeProposal struct {
	ClientKey         string           `json:"client_key"`
	Subject           V6EntityRef      `json:"subject"`
	Positions         []map[string]any `json:"positions"`
	Materiality       float64          `json:"materiality"`
	ResolutionRequest string           `json:"resolution_request"`
}

type V6TaskProposal struct {
	ClientKey          string         `json:"client_key"`
	Kind               string         `json:"kind"`
	Objective          string         `json:"objective"`
	RequiredCapability string         `json:"required_capability"`
	ExpectedResult     string         `json:"expected_result"`
	Priority           float64        `json:"priority"`
	Targets            []V6EntityRef  `json:"targets"`
	DependsOn          []string       `json:"depends_on,omitempty"`
	AcceptanceCriteria map[string]any `json:"acceptance_criteria,omitempty"`
	MaxAttempts        *int           `json:"max_attempts,omitempty"`
	TimeoutSeconds     *int           `json:"timeout_seconds,omitempty"`
}

// V6IntegrationResult is the frozen task_result envelope admitted for an
// integration task. V6 remains production-disabled; this decoder establishes
// the strict protocol boundary consumed by later G persistence slices.
type V6IntegrationResult struct {
	ContractKind             string                      `json:"contract_kind"`
	SchemaVersion            int                         `json:"schema_version"`
	ClientRequestID          string                      `json:"client_request_id"`
	Summary                  string                      `json:"summary"`
	QueryExecutions          []json.RawMessage           `json:"query_executions"`
	SourceCandidates         []json.RawMessage           `json:"source_candidates"`
	StatusUpdates            []V6StatusUpdate            `json:"status_updates"`
	IntegrationContributions []V6IntegrationContribution `json:"integration_contributions"`
	Insights                 []V6InsightProposal         `json:"insights"`
	Disputes                 []V6DisputeProposal         `json:"disputes"`
	Divergence               json.RawMessage             `json:"divergence,omitempty"`
	ProposedTasks            []V6TaskProposal            `json:"proposed_tasks"`
	Report                   json.RawMessage             `json:"report,omitempty"`
	Evaluation               json.RawMessage             `json:"evaluation,omitempty"`
	Confidence               float64                     `json:"confidence"`
	IncompleteReason         *string                     `json:"incomplete_reason,omitempty"`
}

func DecodeAndValidateV6IntegrationResult(raw []byte) (V6IntegrationResult, error) {
	if err := validateV6IntegrationRequiredShape(raw); err != nil {
		return V6IntegrationResult{}, err
	}
	var result V6IntegrationResult
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return V6IntegrationResult{}, fmt.Errorf("%w: decode V6 integration result: %v", ErrInvalidResult, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return V6IntegrationResult{}, fmt.Errorf("%w: V6 integration result has trailing JSON", ErrInvalidResult)
	}
	if err := result.validate(); err != nil {
		return V6IntegrationResult{}, err
	}
	return result, nil
}

func validateV6IntegrationRequiredShape(raw []byte) error {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("%w: decode V6 integration result: %v", ErrInvalidResult, err)
	}
	if err := requireV6Fields("task_result", envelope,
		"contract_kind", "schema_version", "client_request_id", "summary",
		"query_executions", "source_candidates", "status_updates",
		"integration_contributions", "insights", "disputes", "proposed_tasks", "confidence",
	); err != nil {
		return err
	}
	checks := []struct {
		field    string
		required []string
	}{
		{"status_updates", []string{"target", "before", "after", "reason", "evidence_refs"}},
		{"integration_contributions", []string{"client_key", "integration_round_id", "compared_artifacts", "common_findings", "unique_findings", "conflicts", "scope", "omissions", "proposed_insights", "follow_up_questions"}},
		{"insights", []string{"client_key", "title", "summary", "inputs", "relation", "scope", "semantic_value"}},
		{"disputes", []string{"client_key", "subject", "positions", "materiality", "resolution_request"}},
		{"proposed_tasks", []string{"client_key", "kind", "objective", "required_capability", "expected_result", "priority", "targets"}},
	}
	for _, check := range checks {
		items, err := decodeV6ObjectArray(envelope[check.field], check.field)
		if err != nil {
			return err
		}
		for index, item := range items {
			if err = requireV6Fields(fmt.Sprintf("%s[%d]", check.field, index), item, check.required...); err != nil {
				return err
			}
			if check.field == "proposed_tasks" {
				if err = rejectV6NullFields(fmt.Sprintf("%s[%d]", check.field, index), item,
					"depends_on", "acceptance_criteria", "max_attempts", "timeout_seconds",
				); err != nil {
					return err
				}
			}
			if check.field == "integration_contributions" {
				questions, questionErr := decodeV6ObjectArray(item["follow_up_questions"], "follow_up_questions")
				if questionErr != nil {
					return questionErr
				}
				for questionIndex, question := range questions {
					if err = requireV6Fields(fmt.Sprintf("follow_up_questions[%d]", questionIndex), question,
						"client_key", "text", "kind", "required", "priority", "impact", "uncertainty", "novelty",
					); err != nil {
						return err
					}
					if err = rejectV6NullFields(fmt.Sprintf("follow_up_questions[%d]", questionIndex), question, "parent_client_key"); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func requireV6Fields(name string, object map[string]json.RawMessage, fields ...string) error {
	for _, field := range fields {
		raw, ok := object[field]
		if !ok {
			return fmt.Errorf("%w: %s misses required field %s", ErrInvalidResult, name, field)
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("%w: %s field %s cannot be null", ErrInvalidResult, name, field)
		}
	}
	return nil
}

func rejectV6NullFields(name string, object map[string]json.RawMessage, fields ...string) error {
	for _, field := range fields {
		if raw, ok := object[field]; ok && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("%w: %s optional field %s cannot be null", ErrInvalidResult, name, field)
		}
	}
	return nil
}

func decodeV6ObjectArray(raw json.RawMessage, name string) ([]map[string]json.RawMessage, error) {
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("%w: %s must be an object array", ErrInvalidResult, name)
	}
	if items == nil {
		return nil, fmt.Errorf("%w: %s cannot be null", ErrInvalidResult, name)
	}
	return items, nil
}

func (r V6IntegrationResult) validate() error {
	if r.ContractKind != "task_result" || r.SchemaVersion != 6 {
		return fmt.Errorf("%w: V6 integration result requires task_result schema 6", ErrInvalidResult)
	}
	if _, err := uuid.Parse(r.ClientRequestID); err != nil {
		return fmt.Errorf("%w: invalid client_request_id", ErrInvalidResult)
	}
	if err := validateV6Text("summary", r.Summary, 32768); err != nil {
		return err
	}
	if r.QueryExecutions == nil || r.SourceCandidates == nil || r.StatusUpdates == nil ||
		r.IntegrationContributions == nil || r.Insights == nil || r.Disputes == nil || r.ProposedTasks == nil {
		return fmt.Errorf("%w: V6 integration result requires every task_result array", ErrInvalidResult)
	}
	if len(r.QueryExecutions) != 0 || len(r.SourceCandidates) != 0 || presentJSON(r.Divergence) || presentJSON(r.Report) || presentJSON(r.Evaluation) {
		return fmt.Errorf("%w: integration result contains fields owned by another task kind", ErrInvalidResult)
	}
	if len(r.IntegrationContributions) == 0 || len(r.IntegrationContributions) > 64 || len(r.StatusUpdates) > 256 || len(r.Insights) > 64 || len(r.Disputes) > 32 || len(r.ProposedTasks) > 128 {
		return fmt.Errorf("%w: V6 integration result collection bounds violated", ErrInvalidResult)
	}
	if !unitInterval(r.Confidence) {
		return fmt.Errorf("%w: confidence must be in [0,1]", ErrInvalidResult)
	}
	if r.IncompleteReason != nil && utf8.RuneCountInString(*r.IncompleteReason) > 32768 {
		return fmt.Errorf("%w: incomplete_reason exceeds 32768 bytes", ErrInvalidResult)
	}
	contributionKeys := map[string]struct{}{}
	questionKeys := map[string]struct{}{}
	for i, contribution := range r.IntegrationContributions {
		if err := contribution.validate(i, contributionKeys, questionKeys); err != nil {
			return err
		}
	}
	insightKeys := map[string]struct{}{}
	for i, insight := range r.Insights {
		if err := insight.validate(i, insightKeys); err != nil {
			return err
		}
	}
	for i, update := range r.StatusUpdates {
		if err := update.validate(i); err != nil {
			return err
		}
	}
	disputeKeys := map[string]struct{}{}
	for i, dispute := range r.Disputes {
		if err := dispute.validate(i, disputeKeys); err != nil {
			return err
		}
		for _, position := range dispute.Positions {
			if _, err := decodeV6DisputePositionSeed(position); err != nil {
				return err
			}
		}
	}
	taskKeys := map[string]struct{}{}
	for i, task := range r.ProposedTasks {
		if err := task.validate(i, taskKeys); err != nil {
			return err
		}
	}
	return nil
}

func (c V6IntegrationContribution) validate(index int, seen, questionKeys map[string]struct{}) error {
	if err := validateV6UniqueKey(fmt.Sprintf("integration_contributions[%d].client_key", index), c.ClientKey, seen); err != nil {
		return err
	}
	if _, err := uuid.Parse(c.IntegrationRoundID); err != nil {
		return fmt.Errorf("%w: integration contribution %q has invalid round id", ErrInvalidResult, c.ClientKey)
	}
	if len(c.ComparedArtifacts) == 0 || c.Scope == nil || c.CommonFindings == nil || c.UniqueFindings == nil || c.Conflicts == nil || c.Omissions == nil || c.ProposedInsights == nil || c.FollowUpQuestions == nil {
		return fmt.Errorf("%w: integration contribution %q misses required collections or scope", ErrInvalidResult, c.ClientKey)
	}
	if err := validateV6Refs("compared_artifacts", c.ComparedArtifacts, 1, len(c.ComparedArtifacts)); err != nil {
		return err
	}
	for name, values := range map[string][]string{"common_findings": c.CommonFindings, "unique_findings": c.UniqueFindings, "conflicts": c.Conflicts, "omissions": c.Omissions, "proposed_insights": c.ProposedInsights} {
		if err := validateV6Texts(name, values, 32768); err != nil {
			return err
		}
	}
	for i, q := range c.FollowUpQuestions {
		if err := q.validate(i, questionKeys); err != nil {
			return err
		}
	}
	return nil
}

func (i V6InsightProposal) validate(index int, seen map[string]struct{}) error {
	if err := validateV6UniqueKey(fmt.Sprintf("insights[%d].client_key", index), i.ClientKey, seen); err != nil {
		return err
	}
	if strings.TrimSpace(i.Title) == "" || utf8.RuneCountInString(i.Title) > 4096 {
		return fmt.Errorf("%w: insight %q has invalid title", ErrInvalidResult, i.ClientKey)
	}
	if err := validateV6Text("insight summary", i.Summary, 32768); err != nil {
		return err
	}
	if i.Scope == nil {
		return fmt.Errorf("%w: insight %q requires scope", ErrInvalidResult, i.ClientKey)
	}
	if err := validateV6Refs("insight inputs", i.Inputs, 2, 128); err != nil {
		return err
	}
	if !integrationOneOf(i.Relation, "integrates", "explains", "conditions", "resolves", "distinguishes") || !integrationOneOf(i.SemanticValue, "new_explanation", "deduplication", "conflict_resolution", "hypothesis_change", "frontier_change", "report_change", "lossless_compression") {
		return fmt.Errorf("%w: insight %q has invalid relation or semantic value", ErrInvalidResult, i.ClientKey)
	}
	return nil
}

func (u V6StatusUpdate) validate(index int) error {
	if err := validateV6Ref(fmt.Sprintf("status_updates[%d].target", index), u.Target); err != nil {
		return err
	}
	if err := validateV6Key("status before", u.Before); err != nil {
		return err
	}
	if err := validateV6Key("status after", u.After); err != nil {
		return err
	}
	if u.Before == u.After {
		return fmt.Errorf("%w: status update before and after are identical", ErrInvalidResult)
	}
	if err := validateV6Text("status reason", u.Reason, 32768); err != nil {
		return err
	}
	return validateV6Refs("status evidence_refs", u.EvidenceRefs, 1, 128)
}

func (d V6DisputeProposal) validate(index int, seen map[string]struct{}) error {
	if err := validateV6UniqueKey(fmt.Sprintf("disputes[%d].client_key", index), d.ClientKey, seen); err != nil {
		return err
	}
	if err := validateV6Ref("dispute subject", d.Subject); err != nil {
		return err
	}
	if len(d.Positions) < 2 || len(d.Positions) > 16 {
		return fmt.Errorf("%w: dispute %q requires 2-16 positions", ErrInvalidResult, d.ClientKey)
	}
	for _, position := range d.Positions {
		if position == nil {
			return fmt.Errorf("%w: dispute %q position must be an object", ErrInvalidResult, d.ClientKey)
		}
	}
	if !unitInterval(d.Materiality) {
		return fmt.Errorf("%w: dispute %q materiality must be in [0,1]", ErrInvalidResult, d.ClientKey)
	}
	return validateV6Text("resolution_request", d.ResolutionRequest, 32768)
}

func (t V6TaskProposal) validate(index int, seen map[string]struct{}) error {
	if err := validateV6UniqueKey(fmt.Sprintf("proposed_tasks[%d].client_key", index), t.ClientKey, seen); err != nil {
		return err
	}
	if !integrationOneOf(t.Kind, "discover", "deep_read", "verify", "counter_search", "integrate", "deliberate", "diverge", "synthesize", "quality_gate", "citation_audit") {
		return fmt.Errorf("%w: task %q has invalid kind", ErrInvalidResult, t.ClientKey)
	}
	if err := validateV6Text("task objective", t.Objective, 32768); err != nil {
		return err
	}
	if err := validateV6Key("required_capability", t.RequiredCapability); err != nil {
		return err
	}
	if !integrationOneOf(t.ExpectedResult, "research_evidence_v6", "research_integration_v6", "research_deliberation_v6", "research_divergence_v6", "research_report_v6", "research_quality_evaluation_v6", "research_citation_audit_v6") || !unitInterval(t.Priority) {
		return fmt.Errorf("%w: task %q has invalid expected result or priority", ErrInvalidResult, t.ClientKey)
	}
	if err := validateV6Refs("task targets", t.Targets, 1, 32); err != nil {
		return err
	}
	if len(t.DependsOn) > 128 {
		return fmt.Errorf("%w: task %q has too many dependencies", ErrInvalidResult, t.ClientKey)
	}
	depSeen := map[string]struct{}{}
	for _, dependency := range t.DependsOn {
		if err := validateV6UniqueKey("task dependency", dependency, depSeen); err != nil {
			return err
		}
	}
	if t.MaxAttempts != nil && (*t.MaxAttempts < 1 || *t.MaxAttempts > 16) {
		return fmt.Errorf("%w: task %q max_attempts out of range", ErrInvalidResult, t.ClientKey)
	}
	if t.TimeoutSeconds != nil && (*t.TimeoutSeconds < 1 || *t.TimeoutSeconds > 86400) {
		return fmt.Errorf("%w: task %q timeout_seconds out of range", ErrInvalidResult, t.ClientKey)
	}
	return nil
}

func (q V6QuestionProposal) validate(index int, seen map[string]struct{}) error {
	if err := validateV6UniqueKey(fmt.Sprintf("follow_up_questions[%d].client_key", index), q.ClientKey, seen); err != nil {
		return err
	}
	if q.ParentClientKey != nil {
		if err := validateV6Key("parent_client_key", *q.ParentClientKey); err != nil {
			return err
		}
	}
	if err := validateV6Text("question text", q.Text, 32768); err != nil {
		return err
	}
	if !integrationOneOf(q.Kind, "dimension", "hypothesis", "contradiction", "gap", "follow_up") || !unitInterval(q.Priority) || !unitInterval(q.Impact) || !unitInterval(q.Uncertainty) || !unitInterval(q.Novelty) {
		return fmt.Errorf("%w: question %q has invalid kind or score", ErrInvalidResult, q.ClientKey)
	}
	return nil
}

func validateV6Refs(name string, refs []V6EntityRef, min, max int) error {
	if len(refs) < min || len(refs) > max {
		return fmt.Errorf("%w: %s count out of range", ErrInvalidResult, name)
	}
	for i, ref := range refs {
		if err := validateV6Ref(fmt.Sprintf("%s[%d]", name, i), ref); err != nil {
			return err
		}
	}
	return nil
}

func validateV6Ref(name string, ref V6EntityRef) error {
	if !integrationOneOf(ref.Kind, "question", "hypothesis", "branch", "claim", "insight", "dispute", "task", "source") {
		return fmt.Errorf("%w: %s has invalid kind", ErrInvalidResult, name)
	}
	return validateV6Key(name+".key", ref.Key)
}

func validateV6UniqueKey(name, value string, seen map[string]struct{}) error {
	if err := validateV6Key(name, value); err != nil {
		return err
	}
	if _, ok := seen[value]; ok {
		return fmt.Errorf("%w: duplicate V6 key %q", ErrInvalidResult, value)
	}
	seen[value] = struct{}{}
	return nil
}

func validateV6Text(name, value string, max int) error {
	if strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > max {
		return fmt.Errorf("%w: %s is invalid", ErrInvalidResult, name)
	}
	return nil
}

func validateV6Texts(name string, values []string, max int) error {
	for _, value := range values {
		if err := validateV6Text(name, value, max); err != nil {
			return err
		}
	}
	return nil
}

func integrationOneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func presentJSON(raw json.RawMessage) bool {
	return len(bytes.TrimSpace(raw)) > 0
}
