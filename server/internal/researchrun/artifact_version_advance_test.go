package researchrun

import "testing"

func TestAdvanceArtifactVersionRequiresHashAndAccess(t *testing.T) {
	// The validation happens before the transaction is touched, so a nil tx is
	// intentional and locks the fail-closed input boundary down.
	if _, err := advanceArtifactVersionTx(t.Context(), nil, advanceArtifactVersionInput{
		ContentHash: "sha256:value",
	}); err == nil {
		t.Fatal("missing access level was accepted")
	}
	if _, err := advanceArtifactVersionTx(t.Context(), nil, advanceArtifactVersionInput{
		AccessLevel: ArtifactAccessRaw,
	}); err == nil {
		t.Fatal("missing content hash was accepted")
	}
}
