package researchrun

import (
	"errors"
	"fmt"
	"testing"
)

func TestDispatchManifestCandidateKindsPartitionEntireRegistry(t *testing.T) {
	excluded := map[ArtifactEntityKind]bool{
		ArtifactKindAttempt:         true,
		ArtifactKindContextManifest: true,
		ArtifactKindResultArtifact:  true,
	}
	for _, kind := range RegisteredArtifactEntityKinds() {
		if got, want := isDispatchManifestCandidateKind(kind), !excluded[kind]; got != want {
			t.Fatalf("kind=%s candidate=%t want=%t", kind, got, want)
		}
	}
	if len(registeredArtifactEntityKinds) != 35 {
		t.Fatalf("registered kinds=%d; update the full-domain disposition matrix", len(registeredArtifactEntityKinds))
	}
}

func TestManifestCandidateDispositionAuditCoversEveryCandidateFamily(t *testing.T) {
	policy := ArtifactPolicy{}
	candidates := make([]artifactVersionCandidate, 0, len(registeredArtifactEntityKinds)+3)
	for _, kind := range RegisteredArtifactEntityKinds() {
		if !isDispatchManifestCandidateKind(kind) {
			continue
		}
		candidates = append(candidates, manifestDispositionCandidate(kind, "admitted", ArtifactLifecycleAccepted, ArtifactProvenanceComplete, ArtifactAccessVerifiedOnly))
	}
	lifecycleCandidate := manifestDispositionCandidate(ArtifactKindClaim, "lifecycle", ArtifactLifecycleStale, ArtifactProvenanceComplete, ArtifactAccessVerifiedOnly)
	lifecycleCandidate.VersionRowID = "lifecycle"
	provenanceCandidate := manifestDispositionCandidate(ArtifactKindObservation, "provenance", ArtifactLifecycleAccepted, ArtifactProvenanceCompleteness("invalid"), ArtifactAccessVerifiedOnly)
	provenanceCandidate.VersionRowID = "provenance"
	clearanceCandidate := manifestDispositionCandidate(ArtifactKindQuestion, "clearance", ArtifactLifecycleAccepted, ArtifactProvenanceComplete, ArtifactAccessRaw)
	clearanceCandidate.VersionRowID = "clearance"
	candidates = append(candidates, lifecycleCandidate, provenanceCandidate, clearanceCandidate)

	entries := make([]artifactVersionCandidate, 0, len(candidates))
	omissions := make([]artifactVersionCandidate, 0, 4)
	for _, candidate := range candidates {
		switch candidate.VersionRowID {
		case "lifecycle":
			candidate.OmissionReason = "lifecycle"
			omissions = append(omissions, candidate)
		case "provenance":
			candidate.OmissionReason = "policy_denied"
			omissions = append(omissions, candidate)
		case "clearance":
			candidate.OmissionReason = "insufficient_clearance"
			omissions = append(omissions, candidate)
		default:
			if policy.EvaluationPrivateKind(candidate.Kind) {
				candidate.OmissionReason = "evaluation_compartment"
				omissions = append(omissions, candidate)
			} else if admitted, deny := policy.LegacyAdmissionAllowed(candidate.Kind, candidate.Lifecycle, candidate.Provenance); !admitted {
				candidate.OmissionReason = policy.ManifestOmissionReason(deny)
				omissions = append(omissions, candidate)
			} else {
				entries = append(entries, candidate)
			}
		}
	}
	if err := auditManifestCandidateDispositions(
		candidates, entries, omissions, ArtifactClearanceVerifiedOnly, ArtifactPurposeTaskExecution, policy,
	); err != nil {
		t.Fatalf("complete dispositions rejected: %v", err)
	}

	tests := []struct {
		name      string
		entries   []artifactVersionCandidate
		omissions []artifactVersionCandidate
	}{
		{name: "missing", entries: entries[1:], omissions: omissions},
		{name: "duplicate", entries: append(append([]artifactVersionCandidate{}, entries...), entries[0]), omissions: omissions},
		{name: "both", entries: entries, omissions: append(append([]artifactVersionCandidate{}, omissions...), entries[0])},
		{name: "wrong bucket", entries: append(append([]artifactVersionCandidate{}, entries...), omissions[0]), omissions: omissions[1:]},
		{name: "wrong reason", entries: entries, omissions: mutateDispositionReason(omissions, len(omissions)-1, "policy_denied")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := auditManifestCandidateDispositions(
				candidates, test.entries, test.omissions,
				ArtifactClearanceVerifiedOnly, ArtifactPurposeTaskExecution, policy,
			)
			if !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("err=%v want ErrInvalidTransition", err)
			}
		})
	}
}

func manifestDispositionCandidate(
	kind ArtifactEntityKind,
	suffix string,
	lifecycle ArtifactLifecycleStatus,
	provenance ArtifactProvenanceCompleteness,
	access ArtifactAccessLevel,
) artifactVersionCandidate {
	id := fmt.Sprintf("%s:%s", kind, suffix)
	return artifactVersionCandidate{
		VersionRowID: id,
		ArtifactID:   "artifact:" + id,
		Kind:         kind,
		Lifecycle:    lifecycle,
		Provenance:   provenance,
		AccessLevel:  access,
	}
}

func mutateDispositionReason(
	omissions []artifactVersionCandidate,
	index int,
	reason string,
) []artifactVersionCandidate {
	copyOf := append([]artifactVersionCandidate{}, omissions...)
	copyOf[index].OmissionReason = reason
	return copyOf
}
