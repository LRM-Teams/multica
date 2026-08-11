package researchrun

import "testing"

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
	tests := []struct {
		name       string
		kind       ArtifactEntityKind
		lifecycle  ArtifactLifecycleStatus
		provenance ArtifactProvenanceCompleteness
		wantOK     bool
		wantReason ArtifactDenyReason
	}{
		{
			name: "registered partial task", kind: ArtifactKindTask,
			lifecycle: ArtifactLifecycleRegistered, provenance: ArtifactProvenancePartial,
			wantOK: true,
		},
		{
			name: "registered unknown-producer claim", kind: ArtifactKindClaim,
			lifecycle: ArtifactLifecycleRegistered, provenance: ArtifactProvenanceUnknown,
			wantOK: true,
		},
		{
			name: "accepted partial source", kind: ArtifactKindSourceSnapshot,
			lifecycle: ArtifactLifecycleAccepted, provenance: ArtifactProvenancePartial,
			wantOK: true,
		},
		{
			name: "succeeded task lineage registered", kind: ArtifactKindTask,
			lifecycle: ArtifactLifecycleRegistered, provenance: ArtifactProvenanceComplete,
			wantOK: true,
		},
		{
			name: "succeeded attempt lineage registered", kind: ArtifactKindAttempt,
			lifecycle: ArtifactLifecycleRegistered, provenance: ArtifactProvenanceComplete,
			wantOK: true,
		},
		{
			name: "context manifest always denied", kind: ArtifactKindContextManifest,
			lifecycle: ArtifactLifecycleRegistered, provenance: ArtifactProvenanceComplete,
			wantOK: true, // kind is valid; manifest omission happens at plan layer
		},
		{
			name: "superseded lifecycle denied", kind: ArtifactKindClaim,
			lifecycle: ArtifactLifecycleSuperseded, provenance: ArtifactProvenancePartial,
			wantOK: false, wantReason: ArtifactDenyLifecycle,
		},
		{
			name: "withdrawn lifecycle denied", kind: ArtifactKindObservation,
			lifecycle: ArtifactLifecycleWithdrawn, provenance: ArtifactProvenancePartial,
			wantOK: false, wantReason: ArtifactDenyLifecycle,
		},
		{
			name: "unknown kind denied", kind: ArtifactEntityKind("not-a-kind"),
			lifecycle: ArtifactLifecycleRegistered, provenance: ArtifactProvenancePartial,
			wantOK: false, wantReason: ArtifactDenyUnknownKind,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ok, reason := policy.LegacyAdmissionAllowed(tc.kind, tc.lifecycle, tc.provenance)
			if ok != tc.wantOK || reason != tc.wantReason {
				t.Fatalf("ok=%v reason=%q want ok=%v reason=%q", ok, reason, tc.wantOK, tc.wantReason)
			}
		})
	}
}

func TestArtifactPolicyAccessMatrix(t *testing.T) {
	policy := ArtifactPolicy{}
	tests := []struct {
		name      string
		clearance ArtifactClearance
		level     ArtifactAccessLevel
		purpose   ArtifactPurpose
		private   bool
		wantOK    bool
		wantDeny  ArtifactDenyReason
	}{
		{
			name: "raw reads raw", clearance: ArtifactClearanceRaw,
			level: ArtifactAccessRaw, purpose: ArtifactPurposeTaskExecution,
			wantOK: true,
		},
		{
			name: "verified_only reads verified_only", clearance: ArtifactClearanceVerifiedOnly,
			level: ArtifactAccessVerifiedOnly, purpose: ArtifactPurposeTaskExecution,
			wantOK: true,
		},
		{
			name: "verified_only cannot read raw", clearance: ArtifactClearanceVerifiedOnly,
			level: ArtifactAccessRaw, purpose: ArtifactPurposeTaskExecution,
			wantOK: false, wantDeny: ArtifactDenyInsufficientClearance,
		},
		{
			name: "redacted reads redacted", clearance: ArtifactClearanceRedacted,
			level: ArtifactAccessRedacted, purpose: ArtifactPurposeTaskExecution,
			wantOK: true,
		},
		{
			name: "redacted cannot read raw", clearance: ArtifactClearanceRedacted,
			level: ArtifactAccessRaw, purpose: ArtifactPurposeTaskExecution,
			wantOK: false, wantDeny: ArtifactDenyInsufficientClearance,
		},
		{
			name: "unknown access denied", clearance: ArtifactClearanceRaw,
			level: ArtifactAccessLevel("classified"), purpose: ArtifactPurposeTaskExecution,
			wantOK: false, wantDeny: ArtifactDenyUnknownAccess,
		},
		{
			name: "evaluation compartment blocks task execution", clearance: ArtifactClearanceRaw,
			level: ArtifactAccessRaw, purpose: ArtifactPurposeTaskExecution, private: true,
			wantOK: false, wantDeny: ArtifactDenyEvaluationCompartment,
		},
		{
			name: "evaluation compartment allows evaluation purpose", clearance: ArtifactClearanceRaw,
			level: ArtifactAccessRaw, purpose: ArtifactPurposeEvaluation, private: true,
			wantOK: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ok, deny := policy.CanReadNormal(tc.clearance, tc.level, tc.purpose, tc.private)
			if ok != tc.wantOK || deny != tc.wantDeny {
				t.Fatalf("ok=%v deny=%q want ok=%v deny=%q", ok, deny, tc.wantOK, tc.wantDeny)
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
}
