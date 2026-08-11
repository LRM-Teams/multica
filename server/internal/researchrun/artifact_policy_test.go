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

func TestArtifactPolicyLegacyAdmissionAllowsRegisteredPartial(t *testing.T) {
	policy := ArtifactPolicy{}
	ok, reason := policy.LegacyAdmissionAllowed(
		ArtifactKindTask,
		ArtifactLifecycleRegistered,
		ArtifactProvenancePartial,
	)
	if !ok || reason != "" {
		t.Fatalf("admission=%v reason=%q", ok, reason)
	}
}

func TestArtifactPolicyLegacyAdmissionRejectsSuperseded(t *testing.T) {
	policy := ArtifactPolicy{}
	ok, reason := policy.LegacyAdmissionAllowed(
		ArtifactKindTask,
		ArtifactLifecycleSuperseded,
		ArtifactProvenancePartial,
	)
	if ok || reason != ArtifactDenyLifecycle {
		t.Fatalf("admission=%v reason=%q", ok, reason)
	}
}
