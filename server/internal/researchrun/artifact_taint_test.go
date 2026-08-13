package researchrun

import (
	"errors"
	"testing"
)

func TestDeriveManifestOutputAccess(t *testing.T) {
	tests := []struct {
		name    string
		levels  []ArtifactAccessLevel
		want    ArtifactAccessLevel
		wantErr bool
	}{
		{name: "empty model output is raw", want: ArtifactAccessRaw},
		{name: "verified inputs", levels: []ArtifactAccessLevel{ArtifactAccessVerifiedOnly}, want: ArtifactAccessVerifiedOnly},
		{name: "redacted taints verified", levels: []ArtifactAccessLevel{ArtifactAccessVerifiedOnly, ArtifactAccessRedacted}, want: ArtifactAccessRedacted},
		{name: "raw taints all", levels: []ArtifactAccessLevel{ArtifactAccessRedacted, ArtifactAccessRaw, ArtifactAccessVerifiedOnly}, want: ArtifactAccessRaw},
		{name: "unknown fails closed", levels: []ArtifactAccessLevel{"unknown"}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := deriveManifestOutputAccess(tc.levels)
			if tc.wantErr {
				if !errors.Is(err, ErrInvalidTransition) {
					t.Fatalf("err=%v", err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got=%q err=%v want=%q", got, err, tc.want)
			}
		})
	}
}
