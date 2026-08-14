package researchrun

import "testing"

func TestLegacySourceArtifactContentCanonicalizesPayloadAndBindsSnapshot(t *testing.T) {
	first, err := ArtifactContentHash(ArtifactKindLegacySource, legacySourceArtifactContent(
		"https://example.com", "title", "primary", 0.9, "", 0.5, "summary", "excerpt",
		[]byte(`{"publisher":"Example","snapshot_id":"snapshot-1"}`), "snapshot-1",
	))
	if err != nil {
		t.Fatal(err)
	}
	second, err := ArtifactContentHash(ArtifactKindLegacySource, legacySourceArtifactContent(
		"https://example.com", "title", "primary", 0.9, "", 0.5, "summary", "excerpt",
		[]byte(`{"snapshot_id":"snapshot-1","publisher":"Example"}`), "snapshot-1",
	))
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("canonical legacy source hashes differ: %q != %q", first, second)
	}
	changed, err := ArtifactContentHash(ArtifactKindLegacySource, legacySourceArtifactContent(
		"https://example.com", "title", "primary", 0.9, "", 0.5, "summary", "excerpt",
		[]byte(`{"snapshot_id":"snapshot-2","publisher":"Example"}`), "snapshot-2",
	))
	if err != nil {
		t.Fatal(err)
	}
	if first == changed {
		t.Fatal("different Source Snapshot relationships must not share a projection hash")
	}
}
