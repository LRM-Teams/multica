package researchrun

import (
	"errors"
	"testing"
	"time"
)

const retrievalRecoveryHash = "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"

func retrievalRecoveryPolicyFixture() RetrievalRecoveryPolicy {
	return RetrievalRecoveryPolicy{Version: RetrievalRecoveryPolicyVersionV1, MaximumExecutionAttempts: 6, MaximumSameFailureOccurrences: 3}
}

func retrievalRecoveryInputFixture() RetrievalRecoveryInput {
	request := RetrievalRecoveryRequest{Adapter: "web-v1", Query: "bounded research query", Cursor: "page-2", Languages: []string{"zh", "en"}, Scopes: []string{"journal.example", "registry.example"}}
	proposal := request
	return RetrievalRecoveryInput{
		WorkspaceID: "workspace-1", SessionID: "session-1", SearchPlanID: "plan-1", QueryExecutionID: "query-1",
		GoalVersion: 2, PlanVersion: 3, ExecutionAttempt: 1, SameFailureOccurrences: 1,
		OriginalRequest: request,
		Failure:         RetrievalFailureFact{Class: "timeout", Retryable: true, RetryAfter: time.Minute, FailureFingerprint: retrievalRecoveryHash},
		Proposal:        RetrievalRecoveryProposal{Action: RetrievalRecoveryRetrySame, Request: &proposal, Reason: "Retry the identical request after the provider backoff."},
	}
}

func TestDecideRetrievalRecoveryLicensesTargetedActions(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*RetrievalRecoveryInput)
		want   RetrievalRecoveryAction
	}{
		"retry same": {func(*RetrievalRecoveryInput) {}, RetrievalRecoveryRetrySame},
		"restart cursor": {func(input *RetrievalRecoveryInput) {
			input.Failure = RetrievalFailureFact{Class: "cursor_expired", Retryable: true, FailureFingerprint: retrievalRecoveryHash}
			request := input.OriginalRequest
			request.Cursor = ""
			input.Proposal = RetrievalRecoveryProposal{Action: RetrievalRecoveryRestartCursor, Request: &request, Reason: "Restart the same query after its provider cursor expired."}
		}, RetrievalRecoveryRestartCursor},
		"rewrite query": {func(input *RetrievalRecoveryInput) {
			input.Failure = RetrievalFailureFact{Class: "not_found", FailureFingerprint: retrievalRecoveryHash}
			request := input.OriginalRequest
			request.Query, request.Cursor = "broader research query", ""
			input.Proposal = RetrievalRecoveryProposal{Action: RetrievalRecoveryRewriteQuery, Request: &request, Reason: "Broaden the query while preserving its frozen research scope."}
		}, RetrievalRecoveryRewriteQuery},
		"switch adapter": {func(input *RetrievalRecoveryInput) {
			input.Failure = RetrievalFailureFact{Class: "provider_unavailable", Retryable: true, FailureFingerprint: retrievalRecoveryHash}
			request := input.OriginalRequest
			request.Adapter, request.Cursor = "archive-v1", ""
			input.Proposal = RetrievalRecoveryProposal{Action: RetrievalRecoverySwitchAdapter, Request: &request, Reason: "Use another licensed provider for the identical query."}
		}, RetrievalRecoverySwitchAdapter},
		"narrow scope": {func(input *RetrievalRecoveryInput) {
			input.Failure = RetrievalFailureFact{Class: "unsafe_target", CandidateID: "candidate-unsafe", FailureFingerprint: retrievalRecoveryHash}
			request := input.OriginalRequest
			request.Cursor, request.Scopes = "", []string{"registry.example"}
			input.Proposal = RetrievalRecoveryProposal{Action: RetrievalRecoveryNarrowScope, Request: &request, Reason: "Restrict retrieval to the remaining explicitly safe scope."}
		}, RetrievalRecoveryNarrowScope},
		"exclude candidate": {func(input *RetrievalRecoveryInput) {
			input.Failure = RetrievalFailureFact{Class: "unsupported_content", CandidateID: "candidate-pdf", FailureFingerprint: retrievalRecoveryHash}
			input.Proposal = RetrievalRecoveryProposal{Action: RetrievalRecoveryExcludeCandidate, CandidateID: "candidate-pdf", Reason: "Exclude only the unsupported candidate and preserve the query."}
		}, RetrievalRecoveryExcludeCandidate},
		"stop": {func(input *RetrievalRecoveryInput) {
			input.Failure = RetrievalFailureFact{Class: "invalid_response", FailureFingerprint: retrievalRecoveryHash}
			input.Proposal = RetrievalRecoveryProposal{Action: RetrievalRecoveryStop, Reason: "Stop this query because no licensed recovery remains."}
		}, RetrievalRecoveryStop},
	} {
		t.Run(name, func(t *testing.T) {
			input := retrievalRecoveryInputFixture()
			tc.mutate(&input)
			decision, err := DecideRetrievalRecovery(retrievalRecoveryPolicyFixture(), input)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Action != tc.want || decision.RecoveryKey == "" {
				t.Fatalf("decision=%+v want action=%s", decision, tc.want)
			}
			if tc.want == RetrievalRecoveryRetrySame && decision.RetryAfter != time.Minute {
				t.Fatalf("retry_after=%s want 1m", decision.RetryAfter)
			}
		})
	}
}

func TestDecideRetrievalRecoveryStopsAtBoundedFailureBudgets(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate     func(*RetrievalRecoveryInput)
		wantReason string
	}{
		"execution attempts": {func(input *RetrievalRecoveryInput) { input.ExecutionAttempt = 6 }, "execution_attempt_budget_exhausted"},
		"same failure": {func(input *RetrievalRecoveryInput) {
			input.ExecutionAttempt, input.SameFailureOccurrences = 3, 3
		}, "same_failure_budget_exhausted"},
	} {
		t.Run(name, func(t *testing.T) {
			input := retrievalRecoveryInputFixture()
			tc.mutate(&input)
			input.Proposal = RetrievalRecoveryProposal{}
			decision, err := DecideRetrievalRecovery(retrievalRecoveryPolicyFixture(), input)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Action != RetrievalRecoveryStop || decision.Reason != tc.wantReason || decision.Request != nil {
				t.Fatalf("decision=%+v", decision)
			}
		})
	}
}

func TestDecideRetrievalRecoveryRejectsUnlicensedChangeMatrix(t *testing.T) {
	for name, mutate := range map[string]func(*RetrievalRecoveryInput){
		"retry changes query": func(input *RetrievalRecoveryInput) { input.Proposal.Request.Query = "changed" },
		"expired cursor retained": func(input *RetrievalRecoveryInput) {
			input.Failure.Class, input.Proposal.Action = "cursor_expired", RetrievalRecoveryRestartCursor
		},
		"not found switches adapter": func(input *RetrievalRecoveryInput) {
			input.Failure = RetrievalFailureFact{Class: "not_found", FailureFingerprint: retrievalRecoveryHash}
			input.Proposal.Action, input.Proposal.Request.Adapter = RetrievalRecoverySwitchAdapter, "other"
			input.Proposal.Request.Cursor = ""
		},
		"unsafe scope expands": func(input *RetrievalRecoveryInput) {
			input.Failure = RetrievalFailureFact{Class: "unsafe_target", CandidateID: "unsafe", FailureFingerprint: retrievalRecoveryHash}
			input.Proposal.Action, input.Proposal.Request.Cursor = RetrievalRecoveryNarrowScope, ""
			input.Proposal.Request.Scopes = []string{"journal.example", "new.example"}
		},
		"exclude wrong candidate": func(input *RetrievalRecoveryInput) {
			input.Failure = RetrievalFailureFact{Class: "unsupported_content", CandidateID: "candidate-a", FailureFingerprint: retrievalRecoveryHash}
			input.Proposal = RetrievalRecoveryProposal{Action: RetrievalRecoveryExcludeCandidate, CandidateID: "candidate-b", Reason: "Exclude a different candidate without supporting failure facts."}
		},
		"permanent marked retryable": func(input *RetrievalRecoveryInput) {
			input.Failure = RetrievalFailureFact{Class: "permission_denied", Retryable: true, FailureFingerprint: retrievalRecoveryHash}
		},
	} {
		t.Run(name, func(t *testing.T) {
			input := retrievalRecoveryInputFixture()
			mutate(&input)
			if _, err := DecideRetrievalRecovery(retrievalRecoveryPolicyFixture(), input); !errors.Is(err, ErrInvalidContract) {
				t.Fatalf("err=%v want ErrInvalidContract", err)
			}
		})
	}
}

func TestDecideRetrievalRecoveryRejectsRecordedRecoveryReplay(t *testing.T) {
	input := retrievalRecoveryInputFixture()
	first, err := DecideRetrievalRecovery(retrievalRecoveryPolicyFixture(), input)
	if err != nil {
		t.Fatal(err)
	}
	input.PriorRecoveryKeys = []string{first.RecoveryKey}
	if _, err = DecideRetrievalRecovery(retrievalRecoveryPolicyFixture(), input); !errors.Is(err, ErrResultConflict) {
		t.Fatalf("err=%v want ErrResultConflict", err)
	}
}

func TestDecideRetrievalRecoveryNormalizesSetAndTimeOrdering(t *testing.T) {
	input := retrievalRecoveryInputFixture()
	from := time.Date(2026, 8, 14, 12, 0, 0, 0, time.FixedZone("local", 8*60*60))
	input.OriginalRequest.WindowFrom = &from
	proposal := input.OriginalRequest
	proposal.Languages = []string{"en", "zh"}
	proposal.Scopes = []string{"registry.example", "journal.example"}
	input.Proposal.Request = &proposal
	decision, err := DecideRetrievalRecovery(retrievalRecoveryPolicyFixture(), input)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Request == nil || decision.Request.Languages[0] != "en" || decision.Request.WindowFrom.Location() != time.UTC {
		t.Fatalf("decision=%+v", decision)
	}
}
