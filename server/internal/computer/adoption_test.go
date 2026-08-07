package computer

import "testing"

func TestAdoptionCanonicalUnambiguousAuto(t *testing.T) {
	v, err := EvaluateAdoption(LegacyEvidence{
		OriginHost: "leagent.me", SignedInUser: "u", WorkspaceID: "ws",
		ComputerIDCandidates: []string{"c1"},
	})
	if err != nil || v != AdoptAuto {
		t.Fatalf("canonical+unambiguous = %v, %v; want AdoptAuto", v, err)
	}
}

func TestAdoptionRejectsNonCanonicalAndLocalhost(t *testing.T) {
	for _, host := range []string{"", "localhost", "http://127.0.0.1:8080", "http://192.168.1.5", "custom.example.com"} {
		v, err := EvaluateAdoption(LegacyEvidence{OriginHost: host, SignedInUser: "u", WorkspaceID: "ws", ComputerIDCandidates: []string{"c1"}})
		if v != AdoptRejected {
			t.Fatalf("origin %q = %v (err=%v), want AdoptRejected", host, v, err)
		}
	}
}

func TestAdoptionAmbiguousNeedsExplicitChoice(t *testing.T) {
	// No user/workspace, or conflicting candidate ids → never silent.
	cases := []LegacyEvidence{
		{OriginHost: "leagent.me", ComputerIDCandidates: []string{"c1"}},                       // no user/ws
		{OriginHost: "leagent.me", SignedInUser: "u", WorkspaceID: "ws"},                        // no id
		{OriginHost: "leagent.me", SignedInUser: "u", WorkspaceID: "ws", ComputerIDCandidates: []string{"c1", "c2"}}, // conflicting
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
		OriginHost: "leagent.me", SignedInUser: "u", WorkspaceID: "ws",
		HasHostnameEvidence: true, HasDisplayEvidence: true,
	})
	if v != AdoptNeedsExplicitChoice {
		t.Fatalf("hostname/display alone must never be proof, got %v", v)
	}
}
