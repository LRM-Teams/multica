package handler

import (
	"strings"
	"testing"
)

func TestWindyInstructionsAreHROnly(t *testing.T) {
	// Wendy is HR-only now: she must NOT claim to supervise/monitor groups.
	lower := strings.ToLower(windyInstructions)
	for _, banned := range []string{"workspace supervisor", "work graph", "ambient review", "10 minutes"} {
		if strings.Contains(lower, banned) {
			t.Fatalf("Wendy instructions must not contain supervision phrase %q (that is Beckham's job now)", banned)
		}
	}
	for _, phrase := range []string{"HR", "team", "recruiting"} {
		if !strings.Contains(windyInstructions, phrase) {
			t.Fatalf("Wendy instructions must describe HR role, missing %q", phrase)
		}
	}
}

func TestRefreshWindyInstructionsIfStale(t *testing.T) {
	if !strings.Contains(windyInstructions, windyInstructionsCapabilityMarker) {
		t.Fatal("capability marker missing from current Wendy instructions")
	}
	if !strings.Contains(windyInstructions, windyInstructionsAvatarDraftMarker) {
		t.Fatal("avatar-draft marker missing from current Wendy instructions")
	}
	stale := "You are Wendy, the user's personal HR"
	if strings.Contains(stale, windyInstructionsCapabilityMarker) {
		t.Fatal("stale fixture unexpectedly contains capability marker")
	}
}
