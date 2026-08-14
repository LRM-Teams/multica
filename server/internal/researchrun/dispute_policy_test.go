package researchrun

import (
	"errors"
	"testing"
)

func TestValidateDisputeProposalRequiresDistinctCanonicalPositions(t *testing.T) {
	proposal := DisputeProposal{
		Kind: DisputeKindMethod, Severity: DisputeSeverityBlocking, SubjectArtifactID: "claim-subject",
		Materiality: 0.8, ResolutionRequest: "Determine which measurement is comparable",
		Positions: []DisputePosition{
			{PositionID: "p1", AuthorAgentID: "a1", ClaimIDs: []string{"c1"}, EvidenceIDs: []string{"e1"}, ScopeHash: "scope:one", Statement: "The samples are comparable"},
			{PositionID: "p2", AuthorAgentID: "a2", ClaimIDs: []string{"c2"}, EvidenceIDs: []string{"e2"}, ScopeHash: "scope:two", Statement: "The samples are not comparable"},
		},
	}
	if err := ValidateDisputeProposal(proposal); err != nil {
		t.Fatal(err)
	}
	proposal.Positions[1].PositionID = "p1"
	if err := ValidateDisputeProposal(proposal); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("duplicate position err=%v", err)
	}
}

func TestValidateDisputeTransitionEnforcesIndependentEvidenceBasedAdjudication(t *testing.T) {
	resolution := DisputeResolution{Status: DisputeStatusResolved, AdjudicatorAgentID: "judge", VerifiedEvidenceIDs: []string{"e1"}, Explanation: "Evidence distinguishes the methods"}
	if err := ValidateDisputeTransition(DisputeStatusInvestigating, resolution, []string{"a1", "a2"}); err != nil {
		t.Fatal(err)
	}
	resolution.AdjudicatorAgentID = "a1"
	if err := ValidateDisputeTransition(DisputeStatusInvestigating, resolution, []string{"a1", "a2"}); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("non-independent adjudicator err=%v", err)
	}
	resolution.AdjudicatorAgentID = "judge"
	resolution.VerifiedEvidenceIDs = nil
	if err := ValidateDisputeTransition(DisputeStatusInvestigating, resolution, nil); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("evidence-free resolution err=%v", err)
	}
}

func TestValidateDisputeTransitionPreservesResidualUncertainty(t *testing.T) {
	resolution := DisputeResolution{Status: DisputeStatusConditionallyResolved, AdjudicatorAgentID: "judge", Explanation: "Both positions hold in different regions"}
	if err := ValidateDisputeTransition(DisputeStatusInvestigating, resolution, []string{"a1", "a2"}); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("missing uncertainty err=%v", err)
	}
	resolution.ResidualUncertainty = "The boundary between regions is not established"
	if err := ValidateDisputeTransition(DisputeStatusInvestigating, resolution, []string{"a1", "a2"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDisputeTransition(DisputeStatusResolved, resolution, nil); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("terminal rewrite err=%v", err)
	}
}

func TestDisputeDeliveryPolicy(t *testing.T) {
	open, err := DisputeDeliveryPolicy(DisputeSeverityBlocking, DisputeStatusOpen, false)
	if err != nil || !open.BlocksDelivery || open.MustAppearInReport {
		t.Fatalf("open policy=%+v err=%v", open, err)
	}
	irreducible, err := DisputeDeliveryPolicy(DisputeSeverityBlocking, DisputeStatusIrreducible, true)
	if err != nil || irreducible.BlocksDelivery || !irreducible.MustAppearInReport || !irreducible.RequiresHumanGate {
		t.Fatalf("irreducible policy=%+v err=%v", irreducible, err)
	}
	advisory, err := DisputeDeliveryPolicy(DisputeSeverityAdvisory, DisputeStatusInvestigating, false)
	if err != nil || advisory.BlocksDelivery {
		t.Fatalf("advisory policy=%+v err=%v", advisory, err)
	}
}
