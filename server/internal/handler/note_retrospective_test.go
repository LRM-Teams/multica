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

func TestResolveNoteRetrospectiveWindowMonth(t *testing.T) {
	w, err := resolveNoteRetrospectiveWindow(noteRetrospectiveWindowMonth, "2026-08-13", "Asia/Shanghai", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if w.Label != "2026-08" {
		t.Fatalf("label = %q", w.Label)
	}
	if got := w.Start.UTC().Format(time.RFC3339); got != "2026-07-31T16:00:00Z" {
		t.Fatalf("start = %s", got)
	}
	if got := w.End.UTC().Format(time.RFC3339); got != "2026-08-31T16:00:00Z" {
		t.Fatalf("end = %s", got)
	}
	days := noteRetrospectiveDayLabels(w)
	if len(days) != 31 || days[0] != "2026-08-01" || days[len(days)-1] != "2026-08-31" {
		t.Fatalf("day labels = %v", days)
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
	agentID := "33333333-3333-3333-3333-333333333333"
	relatedIssueID := "44444444-4444-4444-4444-444444444444"
	window := noteRetrospectiveWindow{
		Kind:     noteRetrospectiveWindowDay,
		Timezone: "UTC",
		Start:    time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC),
		End:      time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC),
		Label:    "2026-08-13",
	}
	title, content := buildNoteRetrospectiveMarkdown(window, noteRetrospectiveFacts{
		Issues: []noteRetrospectiveIssueFact{
			{
				IssueID: issueID, Identifier: "MUL-9", Title: "Ship", Action: "status_changed",
				Detail: "todo → done", At: time.Date(2026, 8, 13, 8, 30, 0, 0, time.UTC),
				Attribution: noteRetrospectiveAttrHandsOn,
			},
			{
				IssueID: issueID, Identifier: "MUL-9", Title: "Ship", Action: "status_changed",
				Detail: "in_progress → done", At: time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC),
				ActorType: "agent", ActorID: agentID, AgentID: agentID, AgentName: "Bot",
				Attribution: noteRetrospectiveAttrDelegated,
			},
			{
				IssueID: relatedIssueID, Identifier: "MUL-10", Title: "Review", Action: "status_changed",
				Detail: "todo → in_progress", At: time.Date(2026, 8, 13, 11, 0, 0, 0, time.UTC),
				ActorType: "member", Attribution: noteRetrospectiveAttrRelated,
			},
		},
		Notes: []noteRetrospectiveNoteFact{{
			PageID: pageID, Title: "Brief", At: time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC),
			Attribution: noteRetrospectiveAttrHandsOn,
		}},
	}, []string{"issue_activity", "touched_notes"}, nil, []string{"agent_runs"})

	if title != "回顾 2026-08-13" {
		t.Fatalf("title = %q", title)
	}
	for _, heading := range []string{"## 亲手", "## 委派 Agent", "## 仅相关"} {
		if !strings.Contains(content, heading) {
			t.Fatalf("missing attribution heading %q in: %s", heading, content)
		}
	}
	if !strings.Contains(content, "[MUL-9](mention://issue/"+issueID+")") {
		t.Fatalf("missing issue mention: %s", content)
	}
	if !strings.Contains(content, "由 [Bot](mention://agent/"+agentID+") 执行") {
		t.Fatalf("missing delegated agent mention: %s", content)
	}
	if !strings.Contains(content, "[MUL-10](mention://issue/"+relatedIssueID+")") {
		t.Fatalf("missing related issue: %s", content)
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
	// Must not collapse into a single "我做了" Issue section.
	if strings.Contains(content, "## Issue 活动") {
		t.Fatalf("legacy single Issue section still present: %s", content)
	}
}

func TestClassifyNoteRetrospectiveIssueAttribution(t *testing.T) {
	viewer := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	agent := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	other := "cccccccc-cccc-cccc-cccc-cccccccccccc"

	cases := []struct {
		name       string
		actorType  string
		actorID    string
		action     string
		details    string
		want       string
	}{
		{"my status", "member", viewer, "status_changed", `{"from":"todo","to":"done"}`, noteRetrospectiveAttrHandsOn},
		{"my assign agent", "member", viewer, "assignee_changed", `{"to_type":"agent","to_id":"` + agent + `"}`, noteRetrospectiveAttrDelegated},
		{"agent acted", "agent", agent, "status_changed", `{"from":"todo","to":"done"}`, noteRetrospectiveAttrDelegated},
		{"other member", "member", other, "status_changed", `{"from":"todo","to":"done"}`, noteRetrospectiveAttrRelated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyNoteRetrospectiveIssueAttribution(viewer, tc.actorType, tc.actorID, tc.action, []byte(tc.details))
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestFormatIssueActivityDetail(t *testing.T) {
	got := formatIssueActivityDetail("status_changed", []byte(`{"from":"todo","to":"done"}`))
	if got != "todo → done" {
		t.Fatalf("got %q", got)
	}
}

func TestFormatNoteRetrospectiveRunSummary(t *testing.T) {
	if got := formatNoteRetrospectiveRunSummary("Fixed the bug", "completed", "acked"); got != "Fixed the bug" {
		t.Fatalf("got %q", got)
	}
	if got := formatNoteRetrospectiveRunSummary("", "failed", "acked"); got != "无摘要 · failed" {
		t.Fatalf("got %q", got)
	}
	if got := formatNoteRetrospectiveRunSummary("  ", "", "completed"); got != "无摘要 · completed" {
		t.Fatalf("got %q", got)
	}
}

func TestBuildNoteRetrospectiveMarkdownIncludesAgentRuns(t *testing.T) {
	runID := "55555555-5555-5555-5555-555555555555"
	agentID := "66666666-6666-6666-6666-666666666666"
	window := noteRetrospectiveWindow{
		Kind:     noteRetrospectiveWindowDay,
		Timezone: "UTC",
		Start:    time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC),
		End:      time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC),
		Label:    "2026-08-13",
	}
	_, content := buildNoteRetrospectiveMarkdown(window, noteRetrospectiveFacts{
		Runs: []noteRetrospectiveRunFact{{
			RunID: runID, AgentID: agentID, AgentName: "Bot",
			Summary: "Ship the patch", At: time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC),
			Attribution: noteRetrospectiveAttrDelegated,
		}},
	}, []string{"agent_runs"}, nil, []string{"issue_activity", "touched_notes"})

	if !strings.Contains(content, "## 委派 Agent") || !strings.Contains(content, "### Agent runs") {
		t.Fatalf("missing delegated runs section: %s", content)
	}
	if !strings.Contains(content, "mention://run/"+runID) || !strings.Contains(content, "mention://agent/"+agentID) {
		t.Fatalf("missing run/agent mentions: %s", content)
	}
	if !strings.Contains(content, "Ship the patch") {
		t.Fatalf("missing summary: %s", content)
	}
	if strings.Contains(content, "未接入") {
		t.Fatalf("stale unwired copy still present: %s", content)
	}
}

func TestBuildNoteRetrospectiveLayeredMarkdownFromDayDigests(t *testing.T) {
	issueID := "11111111-1111-1111-1111-111111111111"
	window := noteRetrospectiveWindow{
		Kind:     noteRetrospectiveWindowWeek,
		Timezone: "UTC",
		Start:    time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
		End:      time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC),
		Label:    "2026-W33",
	}
	facts := noteRetrospectiveFacts{
		Issues: []noteRetrospectiveIssueFact{
			{
				IssueID: issueID, Identifier: "MUL-9", Title: "Ship",
				At: time.Date(2026, 8, 11, 8, 0, 0, 0, time.UTC),
				Attribution: noteRetrospectiveAttrHandsOn,
			},
			{
				IssueID: issueID, Identifier: "MUL-9", Title: "Ship",
				At: time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC),
				Attribution: noteRetrospectiveAttrHandsOn, Detail: "todo → done",
			},
			{
				IssueID: issueID, Identifier: "MUL-9", Title: "Ship",
				At: time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC),
				Attribution: noteRetrospectiveAttrHandsOn, Detail: "done → todo",
			},
		},
	}
	summaries, layers := composeNoteRetrospectivePeriodSummaries(window, facts, nil)
	if len(layers) != 1 || layers[0] != "day" {
		t.Fatalf("layers = %v", layers)
	}
	if len(summaries) != 2 {
		t.Fatalf("summaries = %d want 2: %#v", len(summaries), summaries)
	}
	_, content := buildNoteRetrospectiveLayeredMarkdown(window, summaries, layers, []string{"issue_activity"}, nil, []string{"agent_runs"})
	if !strings.Contains(content, "## 分层说明") {
		t.Fatalf("missing layered header: %s", content)
	}
	if !strings.Contains(content, "合成日摘要") {
		t.Fatalf("missing synthesized day digest: %s", content)
	}
	if !strings.Contains(content, "mention://issue/"+issueID) {
		t.Fatalf("missing issue mention in digest: %s", content)
	}
	// Must not dump every raw timestamped activity line.
	if strings.Contains(content, "08:00") || strings.Contains(content, "09:00") || strings.Contains(content, "## 亲手") {
		t.Fatalf("raw day dump leaked into week note: %s", content)
	}
}

func TestComposeNoteRetrospectivePrefersExistingDayNote(t *testing.T) {
	window := noteRetrospectiveWindow{
		Kind:     noteRetrospectiveWindowWeek,
		Timezone: "UTC",
		Start:    time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
		End:      time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC),
		Label:    "2026-W33",
	}
	issueID := "11111111-1111-1111-1111-111111111111"
	facts := noteRetrospectiveFacts{
		Issues: []noteRetrospectiveIssueFact{{
			IssueID: issueID, Identifier: "MUL-9", Title: "Ship",
			At: time.Date(2026, 8, 11, 8, 0, 0, 0, time.UTC),
			Attribution: noteRetrospectiveAttrHandsOn,
		}},
	}
	children := map[string]noteRetrospectiveChildNote{
		"回顾 2026-08-11": {
			PageID:  "page-day",
			Title:   "回顾 2026-08-11",
			Content: "# 回顾 2026-08-11\n\n## 亲手\n\n- already summarized\n",
		},
	}
	summaries, _ := composeNoteRetrospectivePeriodSummaries(window, facts, children)
	if len(summaries) != 1 || summaries[0].Source != "existing_note" || summaries[0].PageID != "page-day" {
		t.Fatalf("summaries = %#v", summaries)
	}
	_, content := buildNoteRetrospectiveLayeredMarkdown(window, summaries, []string{"day"}, nil, nil, nil)
	if !strings.Contains(content, "复用已有日回顾") || !strings.Contains(content, "page-day") {
		t.Fatalf("missing reuse: %s", content)
	}
	if !strings.Contains(content, "already summarized") {
		t.Fatalf("missing excerpt: %s", content)
	}
}

func TestComposeMonthPrefersWeekNotesOverDayRaw(t *testing.T) {
	window := noteRetrospectiveWindow{
		Kind:     noteRetrospectiveWindowMonth,
		Timezone: "UTC",
		Start:    time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		End:      time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		Label:    "2026-08",
	}
	issueID := "11111111-1111-1111-1111-111111111111"
	// Activity inside 2026-W32 (Mon Aug 3) and a day outside covered weeks.
	facts := noteRetrospectiveFacts{
		Issues: []noteRetrospectiveIssueFact{
			{
				IssueID: issueID, Identifier: "MUL-1", Title: "A",
				At: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC),
				Attribution: noteRetrospectiveAttrHandsOn,
			},
			{
				IssueID: issueID, Identifier: "MUL-1", Title: "A",
				At: time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC),
				Attribution: noteRetrospectiveAttrHandsOn,
			},
		},
	}
	children := map[string]noteRetrospectiveChildNote{
		"回顾 2026-W32": {PageID: "week-page", Title: "回顾 2026-W32", Content: "# week\n\n## 摘要\n\nweek body\n"},
	}
	summaries, layers := composeNoteRetrospectivePeriodSummaries(window, facts, children)
	if len(layers) != 2 {
		t.Fatalf("layers = %v", layers)
	}
	var sawWeek, sawDaySynth bool
	for _, s := range summaries {
		if s.Kind == "week" && s.Source == "existing_note" {
			sawWeek = true
		}
		if s.Label == "2026-08-20" && s.Source == "synthesized" {
			sawDaySynth = true
		}
		if s.Label == "2026-08-04" {
			t.Fatalf("day inside reused week should be covered: %#v", s)
		}
	}
	if !sawWeek || !sawDaySynth {
		t.Fatalf("summaries = %#v", summaries)
	}
}
