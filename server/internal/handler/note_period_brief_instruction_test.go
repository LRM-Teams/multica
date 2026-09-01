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
		"Notes pages are not source material",
		"This is the write wake",
		"Do not call retry-collectors",
		"Inbox will not auto-retry",
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
	if strings.Contains(got, "MUST call the retry CLI once now") {
		t.Fatal("write wake must not also order a retry")
	}
}

func TestNotePeriodBriefRetryInstructionForbidsWrite(t *testing.T) {
	t.Parallel()
	got := notePeriodBriefRetryInstruction("draft-id")
	for _, want := range []string{
		"retry-collectors --draft-page-id draft-id",
		"Do not write the Brief",
		"Do not --note-write",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("retry instruction missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "--note-write --note-page-id") {
		t.Fatal("retry wake must not deliver the Brief")
	}
}

func TestNotePeriodBriefCollectorInstructionNamesLinuxVisibleRoots(t *testing.T) {
	t.Parallel()
	got := notePeriodBriefCollectorInstruction("draft-id", "本周", "2026-08-10T00:00:00Z", "2026-08-17T00:00:00Z")
	for _, want := range []string{
		"SCAN_ROOTS",
		"HOME symlink children",
		"~/go",
		"--all",
		"porcelain-dirty",
		"mtime is outside the window",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("collector instruction missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "journalctl") {
		t.Fatal("instruction must not teach journal harvest")
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
