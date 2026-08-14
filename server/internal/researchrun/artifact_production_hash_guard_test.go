package researchrun

import (
	"context"
	"errors"
	"testing"
)

func TestRegisterArtifactPassportRejectsProductionHashFallback(t *testing.T) {
	err := registerArtifactPassportTx(context.Background(), nil, registerArtifactPassportInput{
		Kind:       ArtifactKindContextManifest,
		HashOrigin: ArtifactHashOriginProduction,
	})
	if !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("error=%v want ErrInvalidContract", err)
	}
}
