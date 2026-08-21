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
		"<focus>",
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

func TestNotePeriodBriefCollectorInstructionScopedAddsHonorFocus(t *testing.T) {
	t.Parallel()
	full := notePeriodBriefCollectorInstruction("draft-id", "本周", "2026-08-10T00:00:00Z", "2026-08-17T00:00:00Z")
	if strings.Contains(full, "SCOPED COLLECT") {
		t.Fatal("unscoped collector instruction must stay full SCAN_ROOTS")
	}
	scoped := notePeriodBriefCollectorInstructionScoped("draft-id", "本周", "2026-08-10T00:00:00Z", "2026-08-17T00:00:00Z", notePeriodBriefCollectorScope{
		Paths: []string{"/home/jian40/multica"},
		Brief: "notes-agent only",
	})
	for _, want := range []string{"SCOPED COLLECT", "<focus>", "do not scan unrelated"} {
		if !strings.Contains(strings.ToLower(scoped), strings.ToLower(want)) && !strings.Contains(scoped, want) {
			t.Fatalf("scoped instruction missing %q:\n%s", want, scoped)
		}
	}
}

func TestNotePeriodBriefPlannerInstructionForbidsBrief(t *testing.T) {
	t.Parallel()
	got := notePeriodBriefPlannerInstruction("draft-id")
	for _, want := range []string{
		"submit-collect-plan --draft-page-id draft-id",
		"multica-period-work-plan",
		"Do not --note-write",
		"Do not submit-pack",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("planner instruction missing %q:\n%s", want, got)
		}
	}
}
