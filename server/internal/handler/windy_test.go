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
