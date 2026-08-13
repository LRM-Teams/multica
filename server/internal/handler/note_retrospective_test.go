package handler

import (
	"strings"
	"testing"
	"time"
)

func TestResolveNoteRetrospectiveWindowDay(t *testing.T) {
	w, err := resolveNoteRetrospectiveWindow(noteRetrospectiveWindowDay, "2026-08-13", "Asia/Shanghai", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if w.Label != "2026-08-13" || w.Timezone != "Asia/Shanghai" {
		t.Fatalf("window = %+v", w)
	}
	// 2026-08-13 00:00 CST = 2026-08-12 16:00 UTC
	if got := w.Start.UTC().Format(time.RFC3339); got != "2026-08-12T16:00:00Z" {
		t.Fatalf("start = %s", got)
	}
	if got := w.End.UTC().Format(time.RFC3339); got != "2026-08-13T16:00:00Z" {
		t.Fatalf("end = %s", got)
	}
}

func TestResolveNoteRetrospectiveWindowWeek(t *testing.T) {
	// 2026-08-13 is Thursday; week starts Monday 2026-08-10 CST.
	w, err := resolveNoteRetrospectiveWindow(noteRetrospectiveWindowWeek, "2026-08-13", "Asia/Shanghai", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if w.Label != "2026-W33" {
		t.Fatalf("label = %q", w.Label)
	}
	if got := w.Start.UTC().Format(time.RFC3339); got != "2026-08-09T16:00:00Z" {
		t.Fatalf("start = %s", got)
	}
	if got := w.End.UTC().Format(time.RFC3339); got != "2026-08-16T16:00:00Z" {
		t.Fatalf("end = %s", got)
	}
}

func TestNormalizeNoteRetrospectiveSources(t *testing.T) {
	enabled, skipped := normalizeNoteRetrospectiveSources(nil)
	if strings.Join(enabled, ",") != "issue_activity,touched_notes" {
		t.Fatalf("default enabled = %v", enabled)
	}
	if !containsNoteRetrospectiveSource(skipped, noteRetrospectiveSourceRuns) {
		t.Fatalf("runs should be skipped by default: %v", skipped)
	}

	enabled, skipped = normalizeNoteRetrospectiveSources([]string{"touched_notes", "agent_runs", "bogus"})
	if strings.Join(enabled, ",") != "touched_notes,agent_runs" {
		t.Fatalf("enabled = %v", enabled)
	}
	if !containsNoteRetrospectiveSource(skipped, noteRetrospectiveSourceIssue) {
		t.Fatalf("issue should be skipped: %v", skipped)
	}
}

func TestBuildNoteRetrospectiveMarkdownIncludesMentionsAndSources(t *testing.T) {
	issueID := "11111111-1111-1111-1111-111111111111"
	pageID := "22222222-2222-2222-2222-222222222222"
	window := noteRetrospectiveWindow{
		Kind:     noteRetrospectiveWindowDay,
		Timezone: "UTC",
		Start:    time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC),
		End:      time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC),
		Label:    "2026-08-13",
	}
	title, content := buildNoteRetrospectiveMarkdown(window, noteRetrospectiveFacts{
		Issues: []noteRetrospectiveIssueFact{{
			IssueID: issueID, Identifier: "MUL-9", Title: "Ship", Action: "status_changed",
			Detail: "todo → done", At: time.Date(2026, 8, 13, 8, 30, 0, 0, time.UTC),
		}},
		Notes: []noteRetrospectiveNoteFact{{
			PageID: pageID, Title: "Brief", At: time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC),
		}},
	}, []string{"issue_activity", "touched_notes"}, nil, []string{"agent_runs"})

	if title != "回顾 2026-08-13" {
		t.Fatalf("title = %q", title)
	}
	if !strings.Contains(content, "[MUL-9](mention://issue/"+issueID+")") {
		t.Fatalf("missing issue mention: %s", content)
	}
	if !strings.Contains(content, "「Brief」") || !strings.Contains(content, pageID) {
		t.Fatalf("missing note fact: %s", content)
	}
	if !strings.Contains(content, "已用：issue_activity, touched_notes") {
		t.Fatalf("missing sources_used: %s", content)
	}
	if !strings.Contains(content, "已关闭：agent_runs") {
		t.Fatalf("missing sources_skipped: %s", content)
	}
}

func TestFormatIssueActivityDetail(t *testing.T) {
	got := formatIssueActivityDetail("status_changed", []byte(`{"from":"todo","to":"done"}`))
	if got != "todo → done" {
		t.Fatalf("got %q", got)
	}
}
