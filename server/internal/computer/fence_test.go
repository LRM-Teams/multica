package computer

import (
	"errors"
	"testing"
)

func okSet() map[string]bool { return map[string]bool{"ws-1": true, "ws-2": true} }
func okBind() []WorkspaceBinding {
	return []WorkspaceBinding{{WorkspaceID: "ws-1", Active: true}, {WorkspaceID: "ws-2", Active: true}}
}
func okAtt() SuccessorAttestation {
	return SuccessorAttestation{Version: "v2", ComputerID: "c", Generation: 5, BindingSet: okBind(), ExpectedGen: 5, ExpectedVer: "v2", ExpectedID: "c", ExpectedSet: okSet()}
}

func TestReleaseIncumbentOnCompleteAttestation(t *testing.T) {
	if err := ReleaseIncumbent(okAtt()); err != nil {
		t.Fatalf("valid attestation should release incumbent, got %v", err)
	}
}

func TestReleaseDeniedOnWrongVersionOrIdentity(t *testing.T) {
	for _, mut := range []func(*SuccessorAttestation){
		func(a *SuccessorAttestation) { a.Version = "v1" },
		func(a *SuccessorAttestation) { a.ComputerID = "other" },
		func(a *SuccessorAttestation) { a.ComputerID = "" },
	} {
		a := okAtt()
		mut(&a)
		if err := ReleaseIncumbent(a); !errors.Is(err, ErrFenceDenied) {
			t.Fatalf("mutation should be fence-denied, got %v", err)
		}
	}
}

func TestReleaseDeniedOnStaleGeneration(t *testing.T) {
	a := okAtt()
	a.Generation = 4 // expected 5
	if err := ReleaseIncumbent(a); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("stale generation should be rejected, got %v", err)
	}
}

func TestReleaseDeniedOnMissingBinding(t *testing.T) {
	a := okAtt()
	a.BindingSet = []WorkspaceBinding{{WorkspaceID: "ws-1", Active: true}} // missing ws-2
	if err := ReleaseIncumbent(a); !errors.Is(err, ErrFenceDenied) {
		t.Fatalf("missing binding should block release, got %v", err)
	}
}
