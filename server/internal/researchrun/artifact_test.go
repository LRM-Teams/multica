package researchrun

import (
	"slices"
	"testing"
)

func TestRegisteredArtifactEntityKindsMatchSpecInventory(t *testing.T) {
	want := []ArtifactEntityKind{
		ArtifactKindRunSession,
		ArtifactKindContractRevision,
		ArtifactKindMethodDecision,
		ArtifactKindQuestion,
		ArtifactKindTask,
		ArtifactKindAttempt,
		ArtifactKindResultArtifact,
		ArtifactKindLegacySource,
		ArtifactKindSourceSnapshot,
		ArtifactKindObservation,
		ArtifactKindClaim,
		ArtifactKindEvidenceLink,
		ArtifactKindReportRevision,
		ArtifactKindEvaluationDecision,
		ArtifactKindStageEvaluation,
		ArtifactKindResearchMessage,
		ArtifactKindProductRoundDecision,
		ArtifactKindContextManifest,
		ArtifactKindRunEvent,
		ArtifactKindGraphNode,
		ArtifactKindGraphEdge,
	}
	got := RegisteredArtifactEntityKinds()
	slices.SortFunc(got, func(a, b ArtifactEntityKind) int {
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
		return 0
	})
	slices.SortFunc(want, func(a, b ArtifactEntityKind) int {
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
		return 0
	})
	if !slices.Equal(got, want) {
		t.Fatalf("registered kinds=%v want=%v", got, want)
	}
}

func TestParseArtifactEntityKindRejectsUnknown(t *testing.T) {
	if _, err := ParseArtifactEntityKind("hypothesis"); err == nil {
		t.Fatal("expected unknown kind error")
	}
}

func TestParseArtifactEntityKindAcceptsRegistered(t *testing.T) {
	kind, err := ParseArtifactEntityKind(string(ArtifactKindTask))
	if err != nil || kind != ArtifactKindTask {
		t.Fatalf("kind=%q err=%v", kind, err)
	}
}
