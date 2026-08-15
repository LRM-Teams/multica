package researchrun

import (
	"errors"
	"fmt"
	"testing"
)

func TestDeriveManifestOutputAccessClosedMatrix(t *testing.T) {
	t.Parallel()
	levels := []ArtifactAccessLevel{
		ArtifactAccessVerifiedOnly, ArtifactAccessRedacted, ArtifactAccessRaw,
	}
	for length := 1; length <= 3; length++ {
		var visit func([]ArtifactAccessLevel)
		visit = func(prefix []ArtifactAccessLevel) {
			if len(prefix) == length {
				t.Run(fmt.Sprint(prefix), func(t *testing.T) {
					want := ArtifactAccessVerifiedOnly
					for _, level := range prefix {
						if level == ArtifactAccessRaw {
							want = ArtifactAccessRaw
							break
						}
						if level == ArtifactAccessRedacted {
							want = ArtifactAccessRedacted
						}
					}
					got, err := deriveManifestOutputAccess(prefix)
					if err != nil || got != want {
						t.Fatalf("deriveManifestOutputAccess(%v)=(%q,%v), want (%q,nil)", prefix, got, err, want)
					}
				})
				return
			}
			for _, level := range levels {
				next := append(append([]ArtifactAccessLevel(nil), prefix...), level)
				visit(next)
			}
		}
		visit(nil)
	}
}

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
