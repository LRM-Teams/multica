package researchrun

import (
	"errors"
	"reflect"
	"testing"
)

const (
	corpusHashA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	corpusHashB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestComputeCorpusDedupDecisionsSeparatesDuplicatesFromIndependence(t *testing.T) {
	input := []CorpusDedupCandidate{
		{ID: "primary", CanonicalURL: "https://one.example/report", ContentHash: corpusHashA, IndependenceFamily: "publisher:one", DiscoveryOrder: 1},
		{ID: "same-url", CanonicalURL: "https://one.example/report", ContentHash: corpusHashA, IndependenceFamily: "publisher:one", DiscoveryOrder: 2},
		{ID: "mirror", CanonicalURL: "https://mirror.example/report", ContentHash: corpusHashA, IndependenceFamily: "publisher:mirror", DiscoveryOrder: 3},
		{ID: "same-family-new-content", CanonicalURL: "https://one.example/analysis", ContentHash: corpusHashB, IndependenceFamily: "publisher:one", DiscoveryOrder: 4},
	}
	got, err := ComputeCorpusDedupDecisions(input)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]CorpusDedupDecision{}
	for _, decision := range got {
		byID[decision.CandidateID] = decision
	}
	if byID["primary"].Disposition != "include" || byID["same-url"].Rule != "canonical_url_duplicate" || byID["mirror"].Rule != "content_mirror" {
		t.Fatalf("decisions=%+v", got)
	}
	if byID["primary"].EffectiveIndependenceFamily != byID["mirror"].EffectiveIndependenceFamily {
		t.Fatal("mirror was allowed to count as independent support")
	}
	if byID["same-family-new-content"].Disposition != "include" || byID["same-family-new-content"].EffectiveIndependenceFamily != "publisher:one" {
		t.Fatalf("same-family independent document=%+v", byID["same-family-new-content"])
	}
	shuffled := []CorpusDedupCandidate{input[3], input[2], input[1], input[0]}
	replayed, err := ComputeCorpusDedupDecisions(shuffled)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, replayed) {
		t.Fatalf("non-deterministic replay\nfirst=%+v\nsecond=%+v", got, replayed)
	}
}

func TestComputeCorpusDedupDecisionsHoldsURLContentConflictsForReview(t *testing.T) {
	got, err := ComputeCorpusDedupDecisions([]CorpusDedupCandidate{
		{ID: "old", CanonicalURL: "https://example.com/live", ContentHash: corpusHashA, IndependenceFamily: "publisher:example", DiscoveryOrder: 1},
		{ID: "new", CanonicalURL: "https://example.com/live", ContentHash: corpusHashB, IndependenceFamily: "publisher:example", DiscoveryOrder: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, decision := range got {
		if decision.Disposition != "review" || decision.Rule != "canonical_url_content_conflict" {
			t.Fatalf("decision=%+v", decision)
		}
	}
}

func TestComputeCorpusDedupDecisionsRejectsMalformedFacts(t *testing.T) {
	base := CorpusDedupCandidate{ID: "source", CanonicalURL: "https://example.com/source", ContentHash: corpusHashA, IndependenceFamily: "publisher:example"}
	for name, mutate := range map[string]func(*CorpusDedupCandidate){
		"noncanonical URL": func(candidate *CorpusDedupCandidate) { candidate.CanonicalURL = "HTTPS://example.com/source#fragment" },
		"invalid hash":     func(candidate *CorpusDedupCandidate) { candidate.ContentHash = "sha256:nope" },
		"missing family":   func(candidate *CorpusDedupCandidate) { candidate.IndependenceFamily = "" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := base
			mutate(&candidate)
			if _, err := ComputeCorpusDedupDecisions([]CorpusDedupCandidate{candidate}); !errors.Is(err, ErrInvalidContract) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}
