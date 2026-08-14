package researchrun

import (
	"fmt"
	"testing"
)

func TestArtifactPolicyNormalAccessDominates(t *testing.T) {
	policy := ArtifactPolicy{}
	if !policy.NormalAccessDominates(ArtifactAccessRaw, ArtifactAccessVerifiedOnly) {
		t.Fatal("raw should dominate verified_only")
	}
	if policy.NormalAccessDominates(ArtifactAccessVerifiedOnly, ArtifactAccessRaw) {
		t.Fatal("verified_only must not dominate raw")
	}
}

func TestArtifactPolicyLegacyAdmissionMatrix(t *testing.T) {
	policy := ArtifactPolicy{}
	kinds := append(RegisteredArtifactEntityKinds(), ArtifactEntityKind("future-artifact-kind"))
	lifecycles := []ArtifactLifecycleStatus{
		ArtifactLifecycleRegistered,
		ArtifactLifecycleAccepted,
		ArtifactLifecycleRejected,
		ArtifactLifecycleStale,
		ArtifactLifecycleSuperseded,
		ArtifactLifecycleWithdrawn,
		ArtifactLifecycleStatus("future-lifecycle"),
	}
	provenances := []ArtifactProvenanceCompleteness{
		ArtifactProvenanceComplete,
		ArtifactProvenancePartial,
		ArtifactProvenanceUnknown,
		ArtifactProvenanceCompleteness("future-provenance"),
	}

	for _, kind := range kinds {
		for _, lifecycle := range lifecycles {
			for _, provenance := range provenances {
				name := fmt.Sprintf("%s/%s/%s", kind, lifecycle, provenance)
				t.Run(name, func(t *testing.T) {
					wantOK, wantReason := expectedLegacyAdmission(kind, lifecycle, provenance)
					ok, reason := policy.LegacyAdmissionAllowed(kind, lifecycle, provenance)
					if ok != wantOK || reason != wantReason {
						t.Fatalf("ok=%v reason=%q want ok=%v reason=%q", ok, reason, wantOK, wantReason)
					}
				})
			}
		}
	}
}

func TestArtifactPolicyLegacyDomainAdmissionMatrix(t *testing.T) {
	policy := ArtifactPolicy{}
	base := legacyAdmissionFacts{
		Lifecycle:  ArtifactLifecycleRegistered,
		Provenance: ArtifactProvenancePartial,
	}
	tests := []struct {
		name, status string
		kind         ArtifactEntityKind
		wantOK       bool
	}{
		{name: "task pending", kind: ArtifactKindTask, status: "pending", wantOK: true},
		{name: "task ready", kind: ArtifactKindTask, status: "ready", wantOK: true},
		{name: "task dispatching", kind: ArtifactKindTask, status: "dispatching", wantOK: true},
		{name: "task running", kind: ArtifactKindTask, status: "running", wantOK: true},
		{name: "task succeeded lineage", kind: ArtifactKindTask, status: "succeeded", wantOK: true},
		{name: "task failed lineage", kind: ArtifactKindTask, status: "failed", wantOK: true},
		{name: "task blocked lineage", kind: ArtifactKindTask, status: "blocked", wantOK: true},
		{name: "task obsolete lineage", kind: ArtifactKindTask, status: "obsolete", wantOK: true},
		{name: "task cancelled lineage", kind: ArtifactKindTask, status: "cancelled", wantOK: true},
		{name: "task unknown", kind: ArtifactKindTask, status: "paused"},
		{name: "attempt dispatching", kind: ArtifactKindAttempt, status: "dispatching", wantOK: true},
		{name: "attempt running", kind: ArtifactKindAttempt, status: "running", wantOK: true},
		{name: "attempt cancelling", kind: ArtifactKindAttempt, status: "cancelling", wantOK: true},
		{name: "attempt succeeded lineage", kind: ArtifactKindAttempt, status: "succeeded", wantOK: true},
		{name: "attempt failed lineage", kind: ArtifactKindAttempt, status: "failed", wantOK: true},
		{name: "attempt cancelled lineage", kind: ArtifactKindAttempt, status: "cancelled", wantOK: true},
		{name: "attempt lost lineage", kind: ArtifactKindAttempt, status: "lost", wantOK: true},
		{name: "attempt obsolete invalid", kind: ArtifactKindAttempt, status: "obsolete"},
		{name: "claim proposed", kind: ArtifactKindClaim, status: "proposed", wantOK: true},
		{name: "claim supported", kind: ArtifactKindClaim, status: "supported", wantOK: true},
		{name: "claim disputed", kind: ArtifactKindClaim, status: "disputed", wantOK: true},
		{name: "claim refuted lineage", kind: ArtifactKindClaim, status: "refuted", wantOK: true},
		{name: "claim unresolved", kind: ArtifactKindClaim, status: "unresolved", wantOK: true},
		{name: "claim superseded", kind: ArtifactKindClaim, status: "superseded"},
		{name: "claim unknown", kind: ArtifactKindClaim, status: "accepted"},
		{name: "source pending", kind: ArtifactKindSourceSnapshot, status: "pending", wantOK: true},
		{name: "source verified", kind: ArtifactKindSourceSnapshot, status: "verified", wantOK: true},
		{name: "source rejected", kind: ArtifactKindSourceSnapshot, status: "rejected"},
		{name: "observation unknown", kind: ArtifactKindObservation, status: "unknown"},
		{name: "evidence verified", kind: ArtifactKindEvidenceLink, status: "verified", wantOK: true},
		{name: "context manifest never legacy admitted", kind: ArtifactKindContextManifest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			facts := base
			facts.Kind = tc.kind
			facts.DomainStatus = tc.status
			ok, deny := policy.LegacyAdmissionAllowedFacts(facts)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v deny=%q want ok=%v", ok, deny, tc.wantOK)
			}
			if !tc.wantOK && deny != ArtifactDenyDomainFact && deny != ArtifactDenyLegacyIneligible {
				t.Fatalf("deny=%q want %q or %q", deny, ArtifactDenyDomainFact, ArtifactDenyLegacyIneligible)
			}
		})
	}
}

func expectedLegacyAdmission(
	kind ArtifactEntityKind,
	lifecycle ArtifactLifecycleStatus,
	provenance ArtifactProvenanceCompleteness,
) (bool, ArtifactDenyReason) {
	if _, ok := registeredArtifactEntityKinds[kind]; !ok {
		return false, ArtifactDenyUnknownKind
	}
	switch kind {
	case ArtifactKindContextManifest, ArtifactKindHypothesis, ArtifactKindBranch, ArtifactKindInsight, ArtifactKindIntegrationContribution, ArtifactKindIntegrationRound, ArtifactKindDispute, ArtifactKindDisputePosition, ArtifactKindDeliberation, ArtifactKindDeliberationTurn, ArtifactKindResearchDirectorIdentity, ArtifactKindAdjudicationDecision, ArtifactKindInquiryEdge:
		return false, ArtifactDenyLegacyIneligible
	}
	if lifecycle != ArtifactLifecycleRegistered && lifecycle != ArtifactLifecycleAccepted {
		return false, ArtifactDenyLifecycle
	}
	if provenance != ArtifactProvenanceComplete &&
		provenance != ArtifactProvenancePartial &&
		provenance != ArtifactProvenanceUnknown {
		return false, ArtifactDenyMissingPassport
	}
	return true, ""
}

func TestArtifactPolicyLegacyAdmissionDeniesDAndFutureOnlyKinds(t *testing.T) {
	policy := ArtifactPolicy{}
	for _, kind := range []ArtifactEntityKind{
		ArtifactKindContextManifest,
		ArtifactKindHypothesis,
		ArtifactKindBranch,
		ArtifactKindInsight,
		ArtifactKindIntegrationContribution,
		ArtifactKindIntegrationRound,
		ArtifactKindDispute,
		ArtifactKindDisputePosition,
		ArtifactKindDeliberation,
		ArtifactKindDeliberationTurn,
		ArtifactKindAdjudicationDecision,
		ArtifactKindResearchDirectorIdentity,
		ArtifactKindInquiryEdge,
	} {
		ok, reason := policy.LegacyAdmissionAllowed(kind, ArtifactLifecycleAccepted, ArtifactProvenanceComplete)
		if ok || reason != ArtifactDenyLegacyIneligible {
			t.Fatalf("kind=%s ok=%v reason=%q want legacy-ineligible denial", kind, ok, reason)
		}
	}
}

func TestArtifactPolicyAccessMatrix(t *testing.T) {
	policy := ArtifactPolicy{}
	clearances := []ArtifactClearance{
		ArtifactClearanceVerifiedOnly, ArtifactClearanceRedacted, ArtifactClearanceRaw,
	}
	levels := []ArtifactAccessLevel{
		ArtifactAccessVerifiedOnly, ArtifactAccessRedacted, ArtifactAccessRaw,
	}
	purposes := []ArtifactPurpose{ArtifactPurposeTaskExecution, ArtifactPurposeEvaluation}
	for clearanceRank, clearance := range clearances {
		for levelRank, level := range levels {
			for _, purpose := range purposes {
				for _, private := range []bool{false, true} {
					name := string(clearance) + "/" + string(level) + "/" + string(purpose)
					if private {
						name += "/private"
					}
					t.Run(name, func(t *testing.T) {
						wantOK := clearanceRank >= levelRank && (!private || purpose == ArtifactPurposeEvaluation)
						wantDeny := ArtifactDenyReason("")
						if private && purpose != ArtifactPurposeEvaluation {
							wantDeny = ArtifactDenyEvaluationCompartment
						} else if clearanceRank < levelRank {
							wantDeny = ArtifactDenyInsufficientClearance
						}
						ok, deny := policy.CanReadNormal(clearance, level, purpose, private)
						if ok != wantOK || deny != wantDeny {
							t.Fatalf("ok=%v deny=%q want ok=%v deny=%q", ok, deny, wantOK, wantDeny)
						}
					})
				}
			}
		}
	}

	invalid := []struct {
		name      string
		clearance ArtifactClearance
		level     ArtifactAccessLevel
		purpose   ArtifactPurpose
		wantDeny  ArtifactDenyReason
	}{
		{name: "unknown clearance", clearance: "secret", level: ArtifactAccessVerifiedOnly, purpose: ArtifactPurposeTaskExecution, wantDeny: ArtifactDenyInsufficientClearance},
		{name: "unknown access", clearance: ArtifactClearanceRaw, level: "classified", purpose: ArtifactPurposeTaskExecution, wantDeny: ArtifactDenyUnknownAccess},
		{name: "unknown purpose", clearance: ArtifactClearanceRaw, level: ArtifactAccessVerifiedOnly, purpose: "synthesis", wantDeny: ArtifactDenyUnknownPurpose},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			ok, deny := policy.CanReadNormal(tc.clearance, tc.level, tc.purpose, false)
			if ok || deny != tc.wantDeny {
				t.Fatalf("ok=%v deny=%q want denied with %q", ok, deny, tc.wantDeny)
			}
		})
	}
}

func TestV6ContextPolicyAdmitsCompleteV6ArtifactsOnly(t *testing.T) {
	policy := ArtifactPolicy{}
	facts := legacyAdmissionFacts{Kind: ArtifactKindDisputePosition, Lifecycle: ArtifactLifecycleAccepted, Provenance: ArtifactProvenanceComplete}
	if allowed, deny := policy.AdmissionAllowedFacts(ResearchV6ContextPolicy, facts); !allowed || deny != "" {
		t.Fatalf("complete V6 position allowed=%t deny=%s", allowed, deny)
	}
	facts.Provenance = ArtifactProvenancePartial
	if allowed, deny := policy.AdmissionAllowedFacts(ResearchV6ContextPolicy, facts); allowed || deny != ArtifactDenyMissingPassport {
		t.Fatalf("partial V6 position allowed=%t deny=%s", allowed, deny)
	}
	if allowed, _ := policy.AdmissionAllowedFacts(LegacyV1V5CompatPolicy, legacyAdmissionFacts{Kind: ArtifactKindDisputePosition, Lifecycle: ArtifactLifecycleAccepted, Provenance: ArtifactProvenanceComplete}); allowed {
		t.Fatal("legacy policy admitted V6-only position")
	}
}

func TestArtifactPolicyManifestOmissionReasons(t *testing.T) {
	policy := ArtifactPolicy{}
	if policy.ManifestOmissionReason(ArtifactDenyLifecycle) != "lifecycle" {
		t.Fatal("expected lifecycle omission reason")
	}
	if policy.ManifestOmissionReason(ArtifactDenyInsufficientClearance) != "insufficient_clearance" {
		t.Fatal("expected insufficient_clearance omission reason")
	}
	if policy.ManifestOmissionReason(ArtifactDenyEvaluationCompartment) != "evaluation_compartment" {
		t.Fatal("expected evaluation_compartment omission reason")
	}
}

func TestArtifactPolicyEvaluationPrivateKinds(t *testing.T) {
	policy := ArtifactPolicy{}
	if !policy.EvaluationPrivateKind(ArtifactKindStageEvaluation) {
		t.Fatal("stage_evaluation must be evaluation-private")
	}
	if policy.EvaluationPrivateKind(ArtifactKindClaim) {
		t.Fatal("ordinary claim must not be evaluation-private")
	}
}
func TestManifestPurposeForTaskKind(t *testing.T) {
	if got := manifestPurposeForTaskKind(TaskKindQualityGate); got != ArtifactPurposeEvaluation {
		t.Fatalf("quality gate purpose=%q", got)
	}
	if got := manifestPurposeForTaskKind(TaskKindCitationAudit); got != ArtifactPurposeEvaluation {
		t.Fatalf("citation audit purpose=%q", got)
	}
	if got := manifestPurposeForTaskKind(TaskKindDiscover); got != ArtifactPurposeTaskExecution {
		t.Fatalf("discover purpose=%q", got)
	}
}

func TestAcceptanceManifestGrantShapeAllowed(t *testing.T) {
	tests := []struct {
		name       string
		purpose    string
		normal     string
		evaluation string
		want       bool
	}{
		{name: "task execution normal only", purpose: "task_execution", normal: "normal", want: true},
		{name: "task execution missing normal", purpose: "task_execution", want: false},
		{name: "task execution cannot carry evaluation", purpose: "task_execution", normal: "normal", evaluation: "evaluation", want: false},
		{name: "evaluation requires both", purpose: "evaluation", normal: "normal", evaluation: "evaluation", want: true},
		{name: "evaluation missing private grant", purpose: "evaluation", normal: "normal", want: false},
		{name: "unknown purpose fails closed", purpose: "synthesis", normal: "normal", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := acceptanceManifestGrantShapeAllowed(tt.purpose, tt.normal, tt.evaluation); got != tt.want {
				t.Fatalf("acceptanceManifestGrantShapeAllowed(%q, %q, %q)=%v, want %v",
					tt.purpose, tt.normal, tt.evaluation, got, tt.want)
			}
		})
	}
}
