package handler

import (
	"strings"
	"testing"
)

func TestNotePeriodBriefInstructionRequiresReportingShape(t *testing.T) {
	t.Parallel()
	got := notePeriodBriefInstruction("folder-id", "draft-id", "2026-W34")
	for _, want := range []string{
		"STRICT TIME WINDOW",
		"other people",
		"No evidence layer",
		"Start from collector ## Work groups",
		"Group by initiative identity",
		"never by calendar order",
		"## Summary",
		"Work Summary",
		"Next Steps",
		"Technique",
		"Achievements",
		"Research",
		"omit",
		"initiative",
		"nested sub-points",
		"filesystem path",
		"Mermaid",
		"Do not drop",
		"multica-period-work-brief",
		"--note-write --note-page-id folder-id",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("instruction missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Paths may appear only inside a bullet as evidence") {
		t.Fatal("Brief must not teach path-as-evidence wording")
	}
	if strings.Contains(got, "## 主线") {
		t.Fatal("old 主线 board must not remain in instruction")
	}
	if strings.Contains(got, "at most 3 bullets") {
		t.Fatal("old 3-bullet cap must not flatten reporting threads")
	}
}
