package researchrun

import "testing"

func TestHashManifestEntriesDeterministic(t *testing.T) {
	entries := []artifactVersionCandidate{
		{
			ArtifactID:          "30000000-0000-4000-8000-000000000001",
			Version:             1,
			EligibilityRevision: 1,
			Representation:      "raw",
			RepresentationHash:  "sha256:aaaa",
		},
		{
			ArtifactID:          "30000000-0000-4000-8000-000000000002",
			Version:             1,
			EligibilityRevision: 1,
			Representation:      "raw",
			RepresentationHash:  "sha256:bbbb",
		},
	}
	first := hashManifestEntries(entries)
	second := hashManifestEntries([]artifactVersionCandidate{entries[1], entries[0]})
	if first != second {
		t.Fatalf("hash=%q want=%q", first, second)
	}
}

func TestFilterRunSnapshotByManifest(t *testing.T) {
	allowed := map[string]struct{}{
		"claim-1": {},
	}
	snapshot := RunSnapshot{
		Sources:      []SourceSnapshotView{{ID: "source-1"}, {ID: "source-2"}},
		Observations: []Observation{{ID: "obs-1"}},
		Claims:       []Claim{{ID: "claim-1"}, {ID: "claim-2"}},
	}
	filtered := filterRunSnapshotByManifest(snapshot, allowed)
	if len(filtered.Sources) != 0 || len(filtered.Observations) != 0 || len(filtered.Claims) != 1 {
		t.Fatalf("filtered=%+v", filtered)
	}
	if filtered.Claims[0].ID != "claim-1" {
		t.Fatalf("claim=%q", filtered.Claims[0].ID)
	}
}

func TestCompareShadowManifestError(t *testing.T) {
	live := map[string]struct{}{"a": {}, "b": {}}
	manifest := manifestArtifactSet{
		ArtifactIDs: map[string]struct{}{"a": {}},
		Hash:        "sha256:abc",
	}
	if err := compareShadowManifestError(live, manifest); err == nil {
		t.Fatal("expected shadow mismatch error")
	}
	if err := compareShadowManifestError(live, manifestArtifactSet{ArtifactIDs: live, Hash: "sha256:abc"}); err != nil {
		t.Fatalf("expected match: %v", err)
	}
}

func TestNewArtifactContextModule(t *testing.T) {
	module := NewArtifactContextModule()
	if module.policy.ManifestOmissionReason(ArtifactDenyLifecycle) != "lifecycle" {
		t.Fatal("expected lifecycle omission reason")
	}
}
