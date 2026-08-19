package researchrun

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

type V6QueryExecution struct {
	ClientKey     string         `json:"client_key"`
	SearchPlanKey string         `json:"search_plan_key"`
	Adapter       string         `json:"adapter"`
	Query         string         `json:"query"`
	CursorIn      string         `json:"cursor_in,omitempty"`
	CursorOut     string         `json:"cursor_out,omitempty"`
	StartedAt     time.Time      `json:"started_at"`
	FinishedAt    time.Time      `json:"finished_at"`
	Outcome       string         `json:"outcome"`
	FailureClass  string         `json:"failure_class,omitempty"`
	Cost          map[string]any `json:"cost"`
	Safety        map[string]any `json:"safety,omitempty"`
}

type V6Screening struct {
	Decision            string   `json:"decision"`
	Reasons             []string `json:"reasons"`
	ReviewedAgainstPlan bool     `json:"reviewed_against_plan"`
}

type V6SourceCandidate struct {
	ClientKey          string      `json:"client_key"`
	QueryExecutionKey  string      `json:"query_execution_key"`
	URL                string      `json:"url"`
	Title              string      `json:"title"`
	ContentHash        string      `json:"content_hash"`
	IndependenceFamily string      `json:"independence_family"`
	Screening          V6Screening `json:"screening"`
}

type V6EvidenceResult struct {
	ContractKind             string                      `json:"contract_kind"`
	SchemaVersion            int                         `json:"schema_version"`
	ClientRequestID          string                      `json:"client_request_id"`
	Summary                  string                      `json:"summary"`
	QueryExecutions          []V6QueryExecution          `json:"query_executions"`
	SourceCandidates         []V6SourceCandidate         `json:"source_candidates"`
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

func DecodeAndValidateV6EvidenceResult(raw []byte) (V6EvidenceResult, string, error) {
	if err := validateV6EvidenceRequiredShape(raw); err != nil {
		return V6EvidenceResult{}, "", err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var result V6EvidenceResult
	if err := decoder.Decode(&result); err != nil {
		return V6EvidenceResult{}, "", fmt.Errorf("%w: decode V6 evidence result: %v", ErrInvalidResult, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return V6EvidenceResult{}, "", fmt.Errorf("%w: V6 evidence result has trailing JSON", ErrInvalidResult)
	}
	if err := result.validate(); err != nil {
		return V6EvidenceResult{}, "", err
	}
	canonical, err := MarshalArtifactCanonicalJSON(json.RawMessage(raw))
	if err != nil {
		return V6EvidenceResult{}, "", err
	}
	return result, ArtifactContentHashFromCanonicalJSON(canonical), nil
}

func validateV6EvidenceRequiredShape(raw []byte) error {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("%w: decode V6 evidence result: %v", ErrInvalidResult, err)
	}
	if err := requireV6Fields("task_result", envelope, "contract_kind", "schema_version", "client_request_id", "summary", "query_executions", "source_candidates", "status_updates", "integration_contributions", "insights", "disputes", "proposed_tasks", "confidence"); err != nil {
		return err
	}
	for field, required := range map[string][]string{
		"query_executions":  {"client_key", "search_plan_key", "adapter", "query", "started_at", "finished_at", "outcome", "cost"},
		"source_candidates": {"client_key", "query_execution_key", "url", "title", "content_hash", "independence_family", "screening"},
		"status_updates":    {"target", "before", "after", "reason", "evidence_refs"},
	} {
		items, err := decodeV6ObjectArray(envelope[field], field)
		if err != nil {
			return err
		}
		for index, item := range items {
			if err = requireV6Fields(fmt.Sprintf("%s[%d]", field, index), item, required...); err != nil {
				return err
			}
			if field == "query_executions" {
				if err = rejectV6NullFields(fmt.Sprintf("%s[%d]", field, index), item, "cursor_in", "cursor_out", "failure_class", "safety"); err != nil {
					return err
				}
			}
			if field == "source_candidates" {
				var screening map[string]json.RawMessage
				if err = json.Unmarshal(item["screening"], &screening); err != nil {
					return fmt.Errorf("%w: source screening must be an object", ErrInvalidResult)
				}
				if err = requireV6Fields("screening", screening, "decision", "reasons", "reviewed_against_plan"); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (r V6EvidenceResult) validate() error {
	if r.ContractKind != "task_result" || r.SchemaVersion != 6 || !validV6Text(r.Summary) || !unitInterval(r.Confidence) {
		return fmt.Errorf("%w: invalid V6 evidence envelope", ErrInvalidResult)
	}
	if _, err := uuid.Parse(r.ClientRequestID); err != nil {
		return fmt.Errorf("%w: invalid client_request_id", ErrInvalidResult)
	}
	if r.QueryExecutions == nil || r.SourceCandidates == nil || r.StatusUpdates == nil || r.IntegrationContributions == nil || r.Insights == nil || r.Disputes == nil || r.ProposedTasks == nil {
		return fmt.Errorf("%w: V6 evidence result requires every task_result array", ErrInvalidResult)
	}
	if len(r.QueryExecutions) < 1 || len(r.QueryExecutions) > 256 || len(r.SourceCandidates) > 256 || len(r.StatusUpdates) > 256 || len(r.IntegrationContributions) != 0 || len(r.Insights) != 0 || len(r.Disputes) != 0 || len(r.ProposedTasks) != 0 || presentJSON(r.Divergence) || presentJSON(r.Report) || presentJSON(r.Evaluation) {
		return fmt.Errorf("%w: evidence result contains invalid collection bounds or fields owned by another task kind", ErrInvalidResult)
	}
	queryKeys := map[string]bool{}
	queryOutcomes := map[string]string{}
	for _, execution := range r.QueryExecutions {
		if !validV6Key(execution.ClientKey) || queryKeys[execution.ClientKey] || !validV6Key(execution.SearchPlanKey) || !validV6Key(execution.Adapter) || !validV6Text(execution.Query) || len(execution.CursorIn) > 4096 || len(execution.CursorOut) > 4096 || execution.StartedAt.IsZero() || execution.FinishedAt.Before(execution.StartedAt) || execution.Cost == nil || !oneOf(execution.Outcome, "succeeded", "partial", "failed", "blocked") {
			return fmt.Errorf("%w: invalid query execution %q", ErrInvalidResult, execution.ClientKey)
		}
		if oneOf(execution.Outcome, "failed", "blocked") != (strings.TrimSpace(execution.FailureClass) != "") || len(execution.FailureClass) > 160 {
			return fmt.Errorf("%w: inconsistent query failure %q", ErrInvalidResult, execution.ClientKey)
		}
		if execution.FailureClass != "" {
			if _, allowed := searchLineageFailureClasses[execution.FailureClass]; !allowed {
				return fmt.Errorf("%w: unknown query failure class %q", ErrInvalidResult, execution.FailureClass)
			}
		}
		queryKeys[execution.ClientKey] = true
		queryOutcomes[execution.ClientKey] = execution.Outcome
	}
	candidateKeys := map[string]bool{}
	queryURLs := map[string]map[string]bool{}
	for _, candidate := range r.SourceCandidates {
		parsed, err := url.Parse(candidate.URL)
		canonical, canonicalErr := CanonicalURL(candidate.URL)
		if !validV6Key(candidate.ClientKey) || candidateKeys[candidate.ClientKey] || !queryKeys[candidate.QueryExecutionKey] || oneOf(queryOutcomes[candidate.QueryExecutionKey], "failed", "blocked") || err != nil || canonicalErr != nil || parsed.User != nil || canonical != candidate.URL || len(candidate.Title) > 4096 || !validPrefixedSHA256(candidate.ContentHash) || !validV6Key(candidate.IndependenceFamily) || !oneOf(candidate.Screening.Decision, "include", "exclude", "duplicate", "unsafe") || !candidate.Screening.ReviewedAgainstPlan || !validV6TextList(candidate.Screening.Reasons, 1, 64) {
			return fmt.Errorf("%w: invalid source candidate %q", ErrInvalidResult, candidate.ClientKey)
		}
		if queryURLs[candidate.QueryExecutionKey] == nil {
			queryURLs[candidate.QueryExecutionKey] = map[string]bool{}
		}
		if queryURLs[candidate.QueryExecutionKey][candidate.URL] {
			return fmt.Errorf("%w: query %q repeats canonical URL", ErrInvalidResult, candidate.QueryExecutionKey)
		}
		queryURLs[candidate.QueryExecutionKey][candidate.URL] = true
		candidateKeys[candidate.ClientKey] = true
	}
	for index, update := range r.StatusUpdates {
		if err := update.validate(index); err != nil {
			return err
		}
	}
	if r.IncompleteReason != nil && len(*r.IncompleteReason) > 32768 {
		return fmt.Errorf("%w: incomplete_reason is too large", ErrInvalidResult)
	}
	return nil
}
