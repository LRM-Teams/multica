package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestSourceSearchLineageRejectsInvalidScopeBeforeDatabase(t *testing.T) {
	store := &PostgresStore{}
	if _, err := store.SourceSearchLineage(context.Background(), "bad", "bad", "bad"); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("err=%v want ErrInvalidContract", err)
	}
}

func validSearchLineageBatch() SearchLineageBatch {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	return SearchLineageBatch{
		WorkspaceID: "10000000-0000-4000-8000-000000000001", SessionID: "20000000-0000-4000-8000-000000000002",
		TaskID: "30000000-0000-4000-8000-000000000003", AttemptID: "40000000-0000-4000-8000-000000000004",
		PlanClientKey: "primary-search", PlanObjective: "Find primary evidence",
		ClientRequestID: "search-request-1", Adapter: "web", Query: "primary evidence",
		Status: "succeeded", Cost: json.RawMessage(`{"requests":1}`), Safety: json.RawMessage(`{"scan":"safe"}`), ExecutedAt: now,
		Candidates: []SearchLineageCandidate{
			{
				ClientKey: "primary", CanonicalURL: "https://example.com/report", CanonicalIdentity: "url:https://example.com/report",
				Title: "Report", Snippet: "Evidence", Publisher: "Example", IndependenceFamily: "publisher:example",
				ContentHash: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Position: 1,
				Metadata: json.RawMessage(`{}`), Disposition: "accepted", ReasonCode: "meets_criteria", Reason: "Matches the plan",
				EffectiveIndependenceFamily: "publisher:example", DecidedAt: now,
			},
			{
				ClientKey: "mirror", CanonicalURL: "https://mirror.example/report", CanonicalIdentity: "url:https://mirror.example/report",
				Title: "Mirror", Snippet: "Same evidence", Publisher: "Mirror", IndependenceFamily: "publisher:mirror",
				ContentHash: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Position: 2,
				Metadata: json.RawMessage(`{}`), Disposition: "duplicate", ReasonCode: "content_mirror", Reason: "Exact content duplicate",
				EffectiveIndependenceFamily: "publisher:example", CanonicalCandidateKey: "primary", DecidedAt: now,
			},
		},
	}
}

func TestValidateSearchLineageBatchAcceptsTraceableScreening(t *testing.T) {
	in := validSearchLineageBatch()
	if err := validateSearchLineageBatch(in); err != nil {
		t.Fatal(err)
	}
	first, err := hashSearchLineageBatch(in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := hashSearchLineageBatch(in)
	if err != nil || first != second || !validPrefixedSHA256(first) {
		t.Fatalf("hashes first=%q second=%q err=%v", first, second, err)
	}
	failed := in
	failed.ClientRequestID = "failed-search"
	failed.Status, failed.FailureClass, failed.FailureReason = "failed", "timeout", "provider timed out"
	failed.Candidates = nil
	if err := validateSearchLineageBatch(failed); err != nil {
		t.Fatalf("valid failed Search execution: %v", err)
	}
}

func TestValidateSearchLineageBatchRejectsBrokenLineage(t *testing.T) {
	for name, mutate := range map[string]func(*SearchLineageBatch){
		"failed execution with candidates": func(in *SearchLineageBatch) {
			in.Status, in.FailureClass, in.FailureReason = "failed", "timeout", "provider timed out"
		},
		"unknown failure class": func(in *SearchLineageBatch) {
			in.Status, in.FailureClass, in.FailureReason, in.Candidates = "failed", "mystery", "unknown", nil
		},
		"noncanonical URL":               func(in *SearchLineageBatch) { in.Candidates[0].CanonicalURL = "HTTPS://example.com/report#fragment" },
		"URL credentials":                func(in *SearchLineageBatch) { in.Candidates[0].CanonicalURL = "https://token@example.com/report" },
		"duplicate points outside batch": func(in *SearchLineageBatch) { in.Candidates[1].CanonicalCandidateKey = "missing" },
		"accepted points at canonical":   func(in *SearchLineageBatch) { in.Candidates[0].CanonicalCandidateKey = "mirror" },
		"invalid content hash":           func(in *SearchLineageBatch) { in.Candidates[0].ContentHash = "sha256:nope" },
		"trailing JSON":                  func(in *SearchLineageBatch) { in.Cost = json.RawMessage(`{} {}`) },
	} {
		t.Run(name, func(t *testing.T) {
			in := validSearchLineageBatch()
			mutate(&in)
			if err := validateSearchLineageBatch(in); !errors.Is(err, ErrInvalidContract) {
				t.Fatalf("err=%v want ErrInvalidContract", err)
			}
		})
	}
}
