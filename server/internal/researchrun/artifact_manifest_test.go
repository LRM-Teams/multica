package researchrun

import (
	"errors"
	"testing"
)

func TestCompareShadowManifestDetectsMissingArtifact(t *testing.T) {
	live := map[string]struct{}{"source-a": {}, "claim-a": {}}
	manifest := manifestArtifactSet{
		ArtifactIDs: map[string]struct{}{"source-a": {}},
		Hash:        "sha256:deadbeef",
	}
	err := compareShadowManifestError(live, manifest)
	if err == nil || !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("err=%v want shadow mismatch", err)
	}
}

func TestCompareShadowManifestAcceptsExactSet(t *testing.T) {
	ids := map[string]struct{}{"task-a": {}, "attempt-a": {}}
	if err := compareShadowManifestError(ids, manifestArtifactSet{ArtifactIDs: ids, Hash: "sha256:abc"}); err != nil {
		t.Fatalf("unexpected mismatch: %v", err)
	}
}
