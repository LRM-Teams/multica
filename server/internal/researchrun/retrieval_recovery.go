package researchrun

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const RetrievalRecoveryPolicyVersionV1 = "research-retrieval-recovery-v1"

type RetrievalRecoveryPolicy struct {
	Version                       string
	MaximumExecutionAttempts      int
	MaximumSameFailureOccurrences int
}

type RetrievalRecoveryRequest struct {
	Adapter    string
	Query      string
	Cursor     string
	Languages  []string
	Scopes     []string
	WindowFrom *time.Time
	WindowTo   *time.Time
}

type RetrievalFailureFact struct {
	Class              string
	Retryable          bool
	RetryAfter         time.Duration
	FailureFingerprint string
	CandidateID        string
}

type RetrievalRecoveryAction string

const (
	RetrievalRecoveryRetrySame        RetrievalRecoveryAction = "retry_same"
	RetrievalRecoveryRestartCursor    RetrievalRecoveryAction = "restart_cursor"
	RetrievalRecoveryRewriteQuery     RetrievalRecoveryAction = "rewrite_query"
	RetrievalRecoverySwitchAdapter    RetrievalRecoveryAction = "switch_adapter"
	RetrievalRecoveryNarrowScope      RetrievalRecoveryAction = "narrow_scope"
	RetrievalRecoveryExcludeCandidate RetrievalRecoveryAction = "exclude_candidate"
	RetrievalRecoveryStop             RetrievalRecoveryAction = "stop"
)

type RetrievalRecoveryProposal struct {
	Action      RetrievalRecoveryAction
	Request     *RetrievalRecoveryRequest
	CandidateID string
	Reason      string
}

type RetrievalRecoveryInput struct {
	WorkspaceID            string
	SessionID              string
	SearchPlanID           string
	QueryExecutionID       string
	GoalVersion            int64
	PlanVersion            int64
	ExecutionAttempt       int
	SameFailureOccurrences int
	OriginalRequest        RetrievalRecoveryRequest
	Failure                RetrievalFailureFact
	Proposal               RetrievalRecoveryProposal
	PriorRecoveryKeys      []string
}

type RetrievalRecoveryDecision struct {
	Action      RetrievalRecoveryAction
	Request     *RetrievalRecoveryRequest
	CandidateID string
	Reason      string
	RetryAfter  time.Duration
	RecoveryKey string
}

// DecideRetrievalRecovery validates one targeted response to a failed Query
// Execution. It never invents semantic query text: a planner proposes the
// replacement, while this boundary proves that the exact change is licensed by
// the structured failure and cannot repeat an already-recorded recovery fact.
func DecideRetrievalRecovery(policy RetrievalRecoveryPolicy, input RetrievalRecoveryInput) (RetrievalRecoveryDecision, error) {
	if err := validateRetrievalRecoveryPolicy(policy); err != nil {
		return RetrievalRecoveryDecision{}, err
	}
	normalized, err := normalizeRetrievalRecoveryInput(input)
	if err != nil {
		return RetrievalRecoveryDecision{}, err
	}
	decision := RetrievalRecoveryDecision{}
	if normalized.ExecutionAttempt >= policy.MaximumExecutionAttempts {
		decision.Action, decision.Reason = RetrievalRecoveryStop, "execution_attempt_budget_exhausted"
	} else if normalized.SameFailureOccurrences >= policy.MaximumSameFailureOccurrences {
		decision.Action, decision.Reason = RetrievalRecoveryStop, "same_failure_budget_exhausted"
	} else {
		decision.Action = normalized.Proposal.Action
		decision.Reason = normalized.Proposal.Reason
		decision.CandidateID = normalized.Proposal.CandidateID
		if normalized.Proposal.Request != nil {
			request := *normalized.Proposal.Request
			decision.Request = &request
		}
		if err := validateRetrievalRecoveryAction(normalized.OriginalRequest, normalized.Failure, decision); err != nil {
			return RetrievalRecoveryDecision{}, err
		}
		if decision.Action == RetrievalRecoveryRetrySame {
			decision.RetryAfter = normalized.Failure.RetryAfter
		}
	}
	keyInput := struct {
		PolicyVersion    string
		WorkspaceID      string
		SessionID        string
		SearchPlanID     string
		QueryExecutionID string
		GoalVersion      int64
		PlanVersion      int64
		Failure          RetrievalFailureFact
		Decision         RetrievalRecoveryDecision
	}{
		PolicyVersion: policy.Version, WorkspaceID: normalized.WorkspaceID, SessionID: normalized.SessionID,
		SearchPlanID: normalized.SearchPlanID, QueryExecutionID: normalized.QueryExecutionID,
		GoalVersion: normalized.GoalVersion, PlanVersion: normalized.PlanVersion,
		Failure: normalized.Failure, Decision: decision,
	}
	encoded, err := json.Marshal(keyInput)
	if err != nil {
		return RetrievalRecoveryDecision{}, err
	}
	digest := sha256.Sum256(encoded)
	decision.RecoveryKey = fmt.Sprintf("retrieval-recovery:sha256:%x", digest)
	for _, prior := range normalized.PriorRecoveryKeys {
		if prior == decision.RecoveryKey {
			return RetrievalRecoveryDecision{}, fmt.Errorf("%w: Retrieval Recovery already recorded", ErrResultConflict)
		}
	}
	return decision, nil
}

func validateRetrievalRecoveryPolicy(policy RetrievalRecoveryPolicy) error {
	if policy.Version != RetrievalRecoveryPolicyVersionV1 || policy.MaximumExecutionAttempts < 1 || policy.MaximumExecutionAttempts > 100 ||
		policy.MaximumSameFailureOccurrences < 1 || policy.MaximumSameFailureOccurrences > policy.MaximumExecutionAttempts {
		return fmt.Errorf("%w: Retrieval Recovery Policy is invalid", ErrInvalidContract)
	}
	return nil
}

func normalizeRetrievalRecoveryInput(input RetrievalRecoveryInput) (RetrievalRecoveryInput, error) {
	if !validRetrievalRecoveryToken(input.WorkspaceID, 512) || !validRetrievalRecoveryToken(input.SessionID, 512) ||
		!validRetrievalRecoveryToken(input.SearchPlanID, 512) || !validRetrievalRecoveryToken(input.QueryExecutionID, 512) ||
		input.GoalVersion < 1 || input.PlanVersion < 1 || input.ExecutionAttempt < 1 || input.SameFailureOccurrences < 1 ||
		input.SameFailureOccurrences > input.ExecutionAttempt || len(input.PriorRecoveryKeys) > 100 {
		return RetrievalRecoveryInput{}, fmt.Errorf("%w: Retrieval Recovery identity or counters are invalid", ErrInvalidContract)
	}
	normalized := input
	request, err := normalizeRetrievalRecoveryRequest(input.OriginalRequest)
	if err != nil {
		return RetrievalRecoveryInput{}, err
	}
	normalized.OriginalRequest = request
	if err = validateRetrievalRecoveryFailure(input.Failure); err != nil {
		return RetrievalRecoveryInput{}, err
	}
	if input.Proposal.Request != nil {
		proposal, normalizeErr := normalizeRetrievalRecoveryRequest(*input.Proposal.Request)
		if normalizeErr != nil {
			return RetrievalRecoveryInput{}, normalizeErr
		}
		normalized.Proposal.Request = &proposal
	}
	normalized.PriorRecoveryKeys = append([]string(nil), input.PriorRecoveryKeys...)
	sort.Strings(normalized.PriorRecoveryKeys)
	for index, key := range normalized.PriorRecoveryKeys {
		if !validRetrievalRecoveryKey(key) || index > 0 && normalized.PriorRecoveryKeys[index-1] == key {
			return RetrievalRecoveryInput{}, fmt.Errorf("%w: prior Retrieval Recovery keys are invalid", ErrInvalidContract)
		}
	}
	return normalized, nil
}

func normalizeRetrievalRecoveryRequest(request RetrievalRecoveryRequest) (RetrievalRecoveryRequest, error) {
	if !validRetrievalRecoveryToken(request.Adapter, 160) || strings.TrimSpace(request.Query) == "" || strings.TrimSpace(request.Query) != request.Query ||
		len(request.Query) > maxTaskObjectiveBytes || len(request.Cursor) > 4096 || len(request.Languages) > 32 || len(request.Scopes) > 64 ||
		request.WindowFrom != nil && request.WindowTo != nil && request.WindowFrom.After(*request.WindowTo) {
		return RetrievalRecoveryRequest{}, fmt.Errorf("%w: Retrieval Recovery request is invalid", ErrInvalidContract)
	}
	normalized := request
	normalized.Languages = append([]string(nil), request.Languages...)
	normalized.Scopes = append([]string(nil), request.Scopes...)
	if err := normalizeRetrievalRecoverySet(normalized.Languages); err != nil {
		return RetrievalRecoveryRequest{}, err
	}
	if err := normalizeRetrievalRecoverySet(normalized.Scopes); err != nil {
		return RetrievalRecoveryRequest{}, err
	}
	if request.WindowFrom != nil {
		value := request.WindowFrom.UTC()
		normalized.WindowFrom = &value
	}
	if request.WindowTo != nil {
		value := request.WindowTo.UTC()
		normalized.WindowTo = &value
	}
	return normalized, nil
}

func normalizeRetrievalRecoverySet(values []string) error {
	sort.Strings(values)
	for index, value := range values {
		if !validRetrievalRecoveryToken(value, 512) || index > 0 && values[index-1] == value {
			return fmt.Errorf("%w: Retrieval Recovery request set is invalid", ErrInvalidContract)
		}
	}
	return nil
}

func validateRetrievalRecoveryFailure(failure RetrievalFailureFact) error {
	valid := map[string]bool{"rate_limited": true, "timeout": true, "provider_unavailable": true, "cursor_expired": true, "not_found": true, "permission_denied": true, "unsafe_target": true, "unsupported_content": true, "content_too_large": true, "invalid_response": true}
	if !valid[failure.Class] || !validRetrievalRecoveryHash(failure.FailureFingerprint) || failure.RetryAfter < 0 || len(failure.CandidateID) > 512 || strings.TrimSpace(failure.CandidateID) != failure.CandidateID {
		return fmt.Errorf("%w: Retrieval Recovery failure fact is invalid", ErrInvalidContract)
	}
	permanent := failure.Class == "not_found" || failure.Class == "permission_denied" || failure.Class == "unsafe_target" || failure.Class == "unsupported_content" || failure.Class == "content_too_large" || failure.Class == "invalid_response"
	if permanent && (failure.Retryable || failure.RetryAfter > 0) || !failure.Retryable && failure.RetryAfter > 0 {
		return fmt.Errorf("%w: Retrieval Recovery failure retry facts are inconsistent", ErrInvalidContract)
	}
	return nil
}

func validateRetrievalRecoveryAction(original RetrievalRecoveryRequest, failure RetrievalFailureFact, decision RetrievalRecoveryDecision) error {
	if strings.TrimSpace(decision.Reason) != decision.Reason || substantiveRuneCount(decision.Reason) < 8 || len(decision.Reason) > 4096 {
		return fmt.Errorf("%w: Retrieval Recovery decision reason is invalid", ErrInvalidContract)
	}
	request := decision.Request
	switch decision.Action {
	case RetrievalRecoveryRetrySame:
		if !failure.Retryable || !retrievalRecoveryRequestEqual(request, &original) {
			return invalidRetrievalRecoveryAction()
		}
	case RetrievalRecoveryRestartCursor:
		if failure.Class != "cursor_expired" || original.Cursor == "" || request == nil || request.Cursor != "" || !retrievalRecoveryEqualExcept(original, *request, "cursor") {
			return invalidRetrievalRecoveryAction()
		}
	case RetrievalRecoveryRewriteQuery:
		if failure.Class != "not_found" || request == nil || request.Query == original.Query || request.Cursor != "" || !retrievalRecoveryEqualExcept(original, *request, "query") {
			return invalidRetrievalRecoveryAction()
		}
	case RetrievalRecoverySwitchAdapter:
		allowed := map[string]bool{"rate_limited": true, "timeout": true, "provider_unavailable": true, "permission_denied": true, "unsupported_content": true, "content_too_large": true, "invalid_response": true}
		if !allowed[failure.Class] || request == nil || request.Adapter == original.Adapter || request.Cursor != "" || !retrievalRecoveryEqualExcept(original, *request, "adapter") {
			return invalidRetrievalRecoveryAction()
		}
	case RetrievalRecoveryNarrowScope:
		if failure.Class != "unsafe_target" || request == nil || request.Cursor != "" || !retrievalRecoveryEqualExcept(original, *request, "scopes") || !strictRetrievalScopeSubset(request.Scopes, original.Scopes) {
			return invalidRetrievalRecoveryAction()
		}
	case RetrievalRecoveryExcludeCandidate:
		allowed := map[string]bool{"not_found": true, "unsafe_target": true, "unsupported_content": true, "content_too_large": true}
		if !allowed[failure.Class] || request != nil || !validRetrievalRecoveryToken(decision.CandidateID, 512) || decision.CandidateID != failure.CandidateID {
			return invalidRetrievalRecoveryAction()
		}
	case RetrievalRecoveryStop:
		if request != nil || decision.CandidateID != "" {
			return invalidRetrievalRecoveryAction()
		}
	default:
		return invalidRetrievalRecoveryAction()
	}
	return nil
}

func retrievalRecoveryRequestEqual(left, right *RetrievalRecoveryRequest) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	encodedLeft, _ := json.Marshal(left)
	encodedRight, _ := json.Marshal(right)
	return string(encodedLeft) == string(encodedRight)
}

func retrievalRecoveryEqualExcept(original, proposed RetrievalRecoveryRequest, field string) bool {
	switch field {
	case "cursor":
		original.Cursor, proposed.Cursor = "", ""
	case "query":
		original.Query, proposed.Query, original.Cursor, proposed.Cursor = "", "", "", ""
	case "adapter":
		original.Adapter, proposed.Adapter, original.Cursor, proposed.Cursor = "", "", "", ""
	case "scopes":
		original.Scopes, proposed.Scopes, original.Cursor, proposed.Cursor = nil, nil, "", ""
	}
	return retrievalRecoveryRequestEqual(&original, &proposed)
}

func strictRetrievalScopeSubset(proposed, original []string) bool {
	if len(proposed) == 0 || len(proposed) >= len(original) {
		return false
	}
	available := make(map[string]struct{}, len(original))
	for _, scope := range original {
		available[scope] = struct{}{}
	}
	for _, scope := range proposed {
		if _, ok := available[scope]; !ok {
			return false
		}
	}
	return true
}

func invalidRetrievalRecoveryAction() error {
	return fmt.Errorf("%w: Retrieval Recovery action is not licensed by the failure", ErrInvalidContract)
}

func validRetrievalRecoveryToken(value string, limit int) bool {
	return value != "" && len(value) <= limit && strings.TrimSpace(value) == value
}

func validRetrievalRecoveryHash(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return false
	}
	for _, r := range strings.TrimPrefix(value, "sha256:") {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func validRetrievalRecoveryKey(value string) bool {
	if !strings.HasPrefix(value, "retrieval-recovery:sha256:") || len(value) != len("retrieval-recovery:sha256:")+sha256.Size*2 {
		return false
	}
	return validRetrievalRecoveryHash(strings.TrimPrefix(value, "retrieval-recovery:"))
}
