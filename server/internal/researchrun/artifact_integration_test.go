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

func TestNewArtifactContextModule(t *testing.T) {
	module := NewArtifactContextModule()
	if module.policy.ManifestOmissionReason(ArtifactDenyLifecycle) != "lifecycle" {
		t.Fatal("expected lifecycle omission reason")
	}
}
