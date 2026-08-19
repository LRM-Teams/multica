package handler

import (
	"strings"
	"testing"
)

func TestNotePeriodBriefInstructionRequiresReportingShape(t *testing.T) {
	t.Parallel()
	got := notePeriodBriefInstruction("folder-id", "draft-id", "2026-W34")
	for _, want := range []string{
		"initiative/outcome",
		"nested sub-points",
		"filesystem path",
		"Mermaid diagram",
		"Do not drop diagrams",
		"multica-period-work-brief",
		"--note-write --note-page-id folder-id",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("instruction missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "at most 3 bullets") {
		t.Fatal("old 3-bullet cap must not flatten reporting threads")
	}
}
