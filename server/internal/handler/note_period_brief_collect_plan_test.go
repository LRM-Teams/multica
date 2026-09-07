package handler

import (
	"strings"
	"testing"
)

func TestNormalizePeriodBriefUserFocus(t *testing.T) {
	t.Parallel()
	if got := normalizePeriodBriefUserFocus("   \n\t  "); got != "" {
		t.Fatalf("blank focus = %q, want empty", got)
	}
	if got := normalizePeriodBriefUserFocus("  only ~/multica  "); got != "only ~/multica" {
		t.Fatalf("trimmed = %q", got)
	}
	long := strings.Repeat("主题", notePeriodBriefUserFocusMaxRunes+8)
	got := normalizePeriodBriefUserFocus(long)
	if got == "" {
		t.Fatal("long focus should not become empty")
	}
	if n := len([]rune(got)); n != notePeriodBriefUserFocusMaxRunes {
		t.Fatalf("truncated runes = %d, want %d", n, notePeriodBriefUserFocusMaxRunes)
	}
}

func TestApplyNotePeriodBriefCollectPlanFallsBackWhenEmpty(t *testing.T) {
	t.Parallel()
	selected := []string{"a", "b"}
	got := applyNotePeriodBriefCollectPlan(selected, nil)
	if !got.Fallback || len(got.DispatchIDs) != 2 {
		t.Fatalf("nil plan: %+v", got)
	}
	got = applyNotePeriodBriefCollectPlan(selected, &notePeriodBriefCollectPlan{})
	if !got.Fallback || strings.Join(got.DispatchIDs, ",") != "a,b" {
		t.Fatalf("empty assignments: %+v", got)
	}
}

func TestApplyNotePeriodBriefCollectPlanHonorsSkipAndIgnoresUnknown(t *testing.T) {
	t.Parallel()
	selected := []string{"laptop", "cloud"}
	got := applyNotePeriodBriefCollectPlan(selected, &notePeriodBriefCollectPlan{
		Summary: "only laptop notes-agent work",
		Assignments: []notePeriodBriefCollectAssignment{
			{
				CollectorAgentID: "laptop",
				Paths:            []string{"/home/jian40/multica", " /home/jian40/multica "},
				Topics:           []string{"notes assistant"},
				Aspects:          []string{"commits"},
				Brief:            "feat/notes-agent only",
			},
			{CollectorAgentID: "cloud", Skip: true},
			{CollectorAgentID: "foreign", Brief: "must not be added"},
		},
	})
	if got.Fallback {
		t.Fatalf("should not fallback: %+v", got)
	}
	if len(got.DispatchIDs) != 1 || got.DispatchIDs[0] != "laptop" {
		t.Fatalf("dispatch = %#v", got.DispatchIDs)
	}
	scope := got.Scopes["laptop"]
	if len(scope.Paths) != 1 || scope.Paths[0] != "/home/jian40/multica" {
		t.Fatalf("paths = %#v", scope.Paths)
	}
	if scope.Brief != "feat/notes-agent only" {
		t.Fatalf("brief = %q", scope.Brief)
	}
	if _, ok := got.Scopes["cloud"]; ok {
		t.Fatal("skipped collector must not have a scope")
	}
}

func TestApplyNotePeriodBriefCollectPlanAllSkipFallsBack(t *testing.T) {
	t.Parallel()
	got := applyNotePeriodBriefCollectPlan([]string{"a"}, &notePeriodBriefCollectPlan{
		Assignments: []notePeriodBriefCollectAssignment{
			{CollectorAgentID: "a", Skip: true},
		},
	})
	if !got.Fallback || len(got.DispatchIDs) != 1 || got.DispatchIDs[0] != "a" {
		t.Fatalf("all-skip fallback: %+v", got)
	}
	if s := got.Scopes["a"]; !s.Empty() {
		t.Fatalf("fallback scope should be empty: %+v", s)
	}
}

func TestFormatNotePeriodBriefFocusPartitionEmptyWhenUnscoped(t *testing.T) {
	t.Parallel()
	if got := formatNotePeriodBriefFocusPartition("", "", notePeriodBriefCollectorScope{}); got != "" {
		t.Fatalf("empty focus partition = %q", got)
	}
	got := formatNotePeriodBriefFocusPartition("只整理 ~/multica", "notes-agent on laptop", notePeriodBriefCollectorScope{
		Paths: []string{"/home/jian40/multica"},
	})
	for _, want := range []string{
		"human_request:",
		"只整理 ~/multica",
		"planner_summary:",
		"paths:",
		"/home/jian40/multica",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("focus partition missing %q:\n%s", want, got)
		}
	}
}
