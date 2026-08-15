package researchrun

import (
	"errors"
	"reflect"
	"testing"
)

func independenceCandidate(id, publisher, owner string, order int) SourceIndependenceCandidate {
	return SourceIndependenceCandidate{
		CandidateID: id, DiscoveryOrder: order,
		Facts: []SourceIndependenceFact{
			{Kind: "publisher", Value: publisher, Verified: true, Locator: "metadata:publisher"},
			{Kind: "owner", Value: owner, Verified: true, Locator: "registry:owner"},
		},
	}
}

func TestClassifySourceIndependenceCountsOneRepresentativePerDependencyFamily(t *testing.T) {
	first := independenceCandidate("candidate-a", "publisher:a", "owner:shared", 2)
	second := independenceCandidate("candidate-b", "publisher:b", "owner:shared", 1)
	third := independenceCandidate("candidate-c", "publisher:c", "owner:c", 3)
	decisions, err := ClassifySourceIndependence(SourceIndependencePolicyVersionV1, []SourceIndependenceCandidate{first, third, second})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]SourceIndependenceDecision{}
	for _, decision := range decisions {
		byID[decision.CandidateID] = decision
	}
	if byID["candidate-a"].FamilyFingerprint != byID["candidate-b"].FamilyFingerprint || byID["candidate-c"].FamilyFingerprint == byID["candidate-a"].FamilyFingerprint {
		t.Fatalf("families=%+v", byID)
	}
	if byID["candidate-a"].IndependentRepresentative || !byID["candidate-b"].IndependentRepresentative || !byID["candidate-c"].IndependentRepresentative {
		t.Fatalf("representatives=%+v", byID)
	}
	if len(byID["candidate-a"].DecisionFingerprint) != 71 {
		t.Fatalf("decision is not auditable: %+v", byID["candidate-a"])
	}
}

func TestClassifySourceIndependenceClosesTransitiveDependencies(t *testing.T) {
	first := independenceCandidate("candidate-a", "publisher:a", "owner:a", 1)
	first.Facts = append(first.Facts, SourceIndependenceFact{Kind: "dataset", Value: "dataset:shared", Locator: "methods:dataset"})
	second := independenceCandidate("candidate-b", "publisher:b", "owner:b", 2)
	second.Facts = append(second.Facts,
		SourceIndependenceFact{Kind: "dataset", Value: "dataset:shared", Locator: "methods:dataset"},
		SourceIndependenceFact{Kind: "syndication", Value: "feed:shared", Locator: "metadata:feed"},
	)
	third := independenceCandidate("candidate-c", "publisher:c", "owner:c", 3)
	third.Facts = append(third.Facts, SourceIndependenceFact{Kind: "syndication", Value: "feed:shared", Locator: "metadata:feed"})
	decisions, err := ClassifySourceIndependence(SourceIndependencePolicyVersionV1, []SourceIndependenceCandidate{third, first, second})
	if err != nil {
		t.Fatal(err)
	}
	for _, decision := range decisions {
		if decision.FamilyFingerprint != decisions[0].FamilyFingerprint || decision.CanonicalCandidateID != "candidate-a" {
			t.Fatalf("transitive closure failed: %+v", decisions)
		}
	}
}

func TestClassifySourceIndependenceDoesNotCountUnverifiedIdentity(t *testing.T) {
	candidate := independenceCandidate("candidate-a", "publisher:a", "owner:a", 1)
	candidate.Facts[1].Verified = false
	decisions, err := ClassifySourceIndependence(SourceIndependencePolicyVersionV1, []SourceIndependenceCandidate{candidate})
	if err != nil {
		t.Fatal(err)
	}
	if decisions[0].IdentityVerified || decisions[0].IndependentRepresentative {
		t.Fatalf("unverified identity counted independently: %+v", decisions[0])
	}
}

func TestClassifySourceIndependenceIsStableAcrossInputAndFactOrder(t *testing.T) {
	first := independenceCandidate("candidate-a", "publisher:a", "owner:shared", 1)
	second := independenceCandidate("candidate-b", "publisher:b", "owner:shared", 2)
	one, err := ClassifySourceIndependence(SourceIndependencePolicyVersionV1, []SourceIndependenceCandidate{first, second})
	if err != nil {
		t.Fatal(err)
	}
	first.Facts[0], first.Facts[1] = first.Facts[1], first.Facts[0]
	two, err := ClassifySourceIndependence(SourceIndependencePolicyVersionV1, []SourceIndependenceCandidate{second, first})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(one, two) {
		t.Fatalf("unstable decisions\none=%+v\ntwo=%+v", one, two)
	}
}

func TestClassifySourceIndependenceRejectsUnauditableFacts(t *testing.T) {
	for name, mutate := range map[string]func(*SourceIndependenceCandidate){
		"unknown policy":     func(*SourceIndependenceCandidate) {},
		"uppercase identity": func(candidate *SourceIndependenceCandidate) { candidate.Facts[0].Value = "Publisher:A" },
		"missing locator":    func(candidate *SourceIndependenceCandidate) { candidate.Facts[0].Locator = "" },
		"unknown kind":       func(candidate *SourceIndependenceCandidate) { candidate.Facts[0].Kind = "editor" },
		"duplicate fact": func(candidate *SourceIndependenceCandidate) {
			candidate.Facts = append(candidate.Facts, candidate.Facts[0])
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := independenceCandidate("candidate-a", "publisher:a", "owner:a", 1)
			mutate(&candidate)
			policy := SourceIndependencePolicyVersionV1
			if name == "unknown policy" {
				policy = "future"
			}
			if _, err := ClassifySourceIndependence(policy, []SourceIndependenceCandidate{candidate}); !errors.Is(err, ErrInvalidContract) {
				t.Fatalf("err=%v want ErrInvalidContract", err)
			}
		})
	}
}
