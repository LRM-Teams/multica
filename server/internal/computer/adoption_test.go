package computer

import (
	"testing"

	"github.com/google/uuid"
)

func TestAdoptionCanonicalUnambiguousAuto(t *testing.T) {
	v, err := EvaluateAdoption(LegacyEvidence{
		OriginHost: "api.leagent.me", SignedInUser: uuid.NewString(), WorkspaceID: uuid.NewString(),
		ComputerIDCandidates: []string{uuid.NewString()}, UserVerified: true,
		WorkspaceVerified: true, ComputerVerified: true,
	})
	if err != nil || v != AdoptAuto {
		t.Fatalf("canonical+unambiguous = %v, %v; want AdoptAuto", v, err)
	}
}

func TestAdoptionAcceptsLegacyCloudHostsDuringMigration(t *testing.T) {
	for _, host := range []string{"leagent.me", "www.leagent.me"} {
		v, err := EvaluateAdoption(LegacyEvidence{
			OriginHost: host, SignedInUser: uuid.NewString(), WorkspaceID: uuid.NewString(),
			ComputerIDCandidates: []string{uuid.NewString()}, UserVerified: true,
			WorkspaceVerified: true, ComputerVerified: true,
		})
		if err != nil || v != AdoptAuto {
			t.Fatalf("legacy cloud host %q = %v, %v; want AdoptAuto", host, v, err)
		}
	}
}

func TestAdoptionRejectsNonCanonicalAndLocalhost(t *testing.T) {
	for _, host := range []string{"", "localhost", "http://127.0.0.1:8080", "http://192.168.1.5", "custom.example.com"} {
		v, err := EvaluateAdoption(LegacyEvidence{
			OriginHost: host, SignedInUser: uuid.NewString(), WorkspaceID: uuid.NewString(),
			ComputerIDCandidates: []string{uuid.NewString()}, UserVerified: true,
			WorkspaceVerified: true, ComputerVerified: true,
		})
		if v != AdoptRejected {
			t.Fatalf("origin %q = %v (err=%v), want AdoptRejected", host, v, err)
		}
	}
}

func TestAdoptionAmbiguousNeedsExplicitChoice(t *testing.T) {
	// No user/workspace, or conflicting candidate ids → never silent.
	cases := []LegacyEvidence{
		{OriginHost: "leagent.me", ComputerIDCandidates: []string{uuid.NewString()}},
		{OriginHost: "leagent.me", SignedInUser: uuid.NewString(), WorkspaceID: uuid.NewString(), UserVerified: true, WorkspaceVerified: true, ComputerVerified: true},
		{OriginHost: "leagent.me", SignedInUser: uuid.NewString(), WorkspaceID: uuid.NewString(), ComputerIDCandidates: []string{uuid.NewString(), uuid.NewString()}, UserVerified: true, WorkspaceVerified: true, ComputerVerified: true},
	}
	for _, e := range cases {
		v, err := EvaluateAdoption(e)
		if err != nil || v != AdoptNeedsExplicitChoice {
			t.Fatalf("evidence %+v = %v, %v; want AdoptNeedsExplicitChoice", e, v, err)
		}
	}
}

func TestAdoptionHostnameDisplayNeverIdentityProof(t *testing.T) {
	// Rich hostname/display with no computer id must NOT auto-adopt.
	v, _ := EvaluateAdoption(LegacyEvidence{
		OriginHost: "leagent.me", SignedInUser: uuid.NewString(), WorkspaceID: uuid.NewString(),
		UserVerified: true, WorkspaceVerified: true, ComputerVerified: true,
		HasHostnameEvidence: true, HasDisplayEvidence: true,
	})
	if v != AdoptNeedsExplicitChoice {
		t.Fatalf("hostname/display alone must never be proof, got %v", v)
	}
}
