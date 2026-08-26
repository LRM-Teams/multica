package handler

import (
	"strings"
	"testing"
)

// The stale-Issue notice is how a mid-flight Goal revision reaches the
// manager: without it, Issues created under older requirements keep running
// on stale scope with nothing prompting re-validation.
func TestGoalControllerPromptSurfacesStaleIssues(t *testing.T) {
	kinds := map[string]int{"goal_updated": 1}
	sources := []string{"issue:abc"}

	fresh := buildGoalControllerPrompt("goal-1", "Ship it", 5, 0, kinds, sources)
	if strings.Contains(fresh, "older version") {
		t.Fatalf("prompt with zero stale Issues must not carry the re-validation notice: %q", fresh)
	}

	stale := buildGoalControllerPrompt("goal-1", "Ship it", 5, 2, kinds, sources)
	if !strings.Contains(stale, "version 5") || !strings.Contains(stale, "2 open Issue(s) were created under an older version") {
		t.Fatalf("prompt missing stale re-validation notice: %q", stale)
	}
	if !strings.Contains(stale, "re-validate") || !strings.Contains(stale, "cancel any made obsolete") {
		t.Fatalf("stale notice missing the re-validate/cancel instruction: %q", stale)
	}
}
