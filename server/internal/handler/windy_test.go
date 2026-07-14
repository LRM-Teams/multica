package handler

import (
	"strings"
	"testing"
)

func TestWindyInstructionsDescribeWorkspaceSupervisorWorkGraph(t *testing.T) {
	for _, phrase := range []string{"workspace supervisor", "work graph", "automatically"} {
		if !strings.Contains(strings.ToLower(windyInstructions), phrase) {
			t.Fatalf("Wendy instructions must contain %q", phrase)
		}
	}
}

func TestWindyInstructionsDescribeAmbientGroupMonitoring(t *testing.T) {
	for _, phrase := range []string{
		windyInstructionsCapabilityMarker,
		"10 minutes",
		"do not need to configure",
		"autopilot",
		"add you to that group",
		"Your own posts do not re-arm",
	} {
		if !strings.Contains(windyInstructions, phrase) {
			t.Fatalf("Wendy instructions must contain %q", phrase)
		}
	}
}

func TestRefreshWindyInstructionsIfStale(t *testing.T) {
	if !strings.Contains(windyInstructions, windyInstructionsCapabilityMarker) {
		t.Fatal("capability marker missing from current Wendy instructions")
	}
	stale := "You are Wendy, the user's personal HR"
	if strings.Contains(stale, windyInstructionsCapabilityMarker) {
		t.Fatal("stale fixture unexpectedly contains capability marker")
	}
}
