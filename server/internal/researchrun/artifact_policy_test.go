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

func expectedLegacyAdmission(
	kind ArtifactEntityKind,
	lifecycle ArtifactLifecycleStatus,
	provenance ArtifactProvenanceCompleteness,
) (bool, ArtifactDenyReason) {
	if _, ok := registeredArtifactEntityKinds[kind]; !ok {
		return false, ArtifactDenyUnknownKind
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
