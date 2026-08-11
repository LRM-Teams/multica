package researchrun

import (
	"testing"
)

func TestRegisterArtifactPassportInputDefaults(t *testing.T) {
	in := registerArtifactPassportInput{Kind: ArtifactKindClaim}
	if in.AccessLevel != "" || in.HashOrigin != "" {
		t.Fatal("expected zero defaults before registration helper runs")
	}
	hash := migrationArtifactContentHash(ArtifactKindTask, "ws", "session", "entity")
	if len(hash) != 7+64 {
		t.Fatalf("hash length=%d value=%q", len(hash), hash)
	}
}
