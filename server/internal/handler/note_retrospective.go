package handler

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// S4-S1 on-demand retrospective: aggregate known Facts into a private note
// under 回顾/. Product note writeback ≠ Agent Daily (S3-W3) — this path never
// reads memory/daily.

const (
	noteRetrospectiveFolderTitle = "回顾"
	noteRetrospectiveSourceIssue = "issue_activity"
	noteRetrospectiveSourceNotes = "touched_notes"
	noteRetrospectiveSourceRuns  = "agent_runs"
)

var noteRetrospectiveDefaultSources = []string{
	noteRetrospectiveSourceIssue,
	noteRetrospectiveSourceNotes,
}

type noteRetrospectiveWindowKind string

const (
	noteRetrospectiveWindowDay  noteRetrospectiveWindowKind = "day"
	noteRetrospectiveWindowWeek noteRetrospectiveWindowKind = "week"
)

type noteRetrospectiveIssueFact struct {
	IssueID    string
	Identifier string
	Title      string
	Action     string
	Detail     string
	At         time.Time
}

type noteRetrospectiveNoteFact struct {
	PageID string
	Title  string
	At     time.Time
}

type noteRetrospectiveFacts struct {
	Issues []noteRetrospectiveIssueFact
	Notes  []noteRetrospectiveNoteFact
}

type noteRetrospectiveWindow struct {
	Kind     noteRetrospectiveWindowKind
	Timezone string
	Start    time.Time // inclusive, UTC instant
	End      time.Time // exclusive, UTC instant
	Label    string    // calendar label in viewing tz
}

func resolveNoteRetrospectiveLocation(tz string) *time.Location {
	tz = strings.TrimSpace(tz)
	if tz == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(tz)
	if err != nil || loc == nil {
		return time.UTC
	}
	return loc
}

// resolveNoteRetrospectiveWindow maps a calendar date (YYYY-MM-DD) in tz to a
// half-open UTC interval. Week = Monday 00:00 → next Monday 00:00 in that tz.
func resolveNoteRetrospectiveWindow(kind noteRetrospectiveWindowKind, dateYYYYMMDD, tz string, now time.Time) (noteRetrospectiveWindow, error) {
	loc := resolveNoteRetrospectiveLocation(tz)
	tzName := loc.String()
	if tzName == "Local" {
		tzName = "UTC"
		loc = time.UTC
	}

	var day time.Time
	if strings.TrimSpace(dateYYYYMMDD) == "" {
		nowLocal := now.In(loc)
		day = time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 0, 0, 0, 0, loc)
	} else {
		parsed, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(dateYYYYMMDD), loc)
		if err != nil {
			return noteRetrospectiveWindow{}, fmt.Errorf("invalid date %q (want YYYY-MM-DD)", dateYYYYMMDD)
		}
		day = parsed
	}

	switch kind {
	case noteRetrospectiveWindowWeek:
		// Go Weekday: Sunday=0 … Saturday=6. Shift so Monday is start.
		offset := (int(day.Weekday()) + 6) % 7
		startLocal := day.AddDate(0, 0, -offset)
		endLocal := startLocal.AddDate(0, 0, 7)
		isoYear, isoWeek := startLocal.ISOWeek()
		return noteRetrospectiveWindow{
			Kind:     kind,
			Timezone: tzName,
			Start:    startLocal.UTC(),
			End:      endLocal.UTC(),
			Label:    fmt.Sprintf("%d-W%02d", isoYear, isoWeek),
		}, nil
	case noteRetrospectiveWindowDay, "":
		startLocal := day
		endLocal := day.AddDate(0, 0, 1)
		return noteRetrospectiveWindow{
			Kind:     noteRetrospectiveWindowDay,
			Timezone: tzName,
			Start:    startLocal.UTC(),
			End:      endLocal.UTC(),
			Label:    startLocal.Format("2006-01-02"),
		}, nil
	default:
		return noteRetrospectiveWindow{}, fmt.Errorf("invalid window %q (want day|week)", kind)
	}
}

func normalizeNoteRetrospectiveSources(requested []string) (enabled []string, skipped []string) {
	want := map[string]bool{}
	if len(requested) == 0 {
		for _, s := range noteRetrospectiveDefaultSources {
			want[s] = true
		}
	} else {
		for _, raw := range requested {
			s := strings.TrimSpace(raw)
			switch s {
			case noteRetrospectiveSourceIssue, noteRetrospectiveSourceNotes, noteRetrospectiveSourceRuns:
				want[s] = true
			}
		}
	}
	order := []string{noteRetrospectiveSourceIssue, noteRetrospectiveSourceNotes, noteRetrospectiveSourceRuns}
	for _, s := range order {
		if want[s] {
			enabled = append(enabled, s)
		} else {
			skipped = append(skipped, s)
		}
	}
	return enabled, skipped
}

func buildNoteRetrospectiveMarkdown(
	window noteRetrospectiveWindow,
	facts noteRetrospectiveFacts,
	sourcesUsed, sourcesEmpty, sourcesSkipped []string,
) (title string, content string) {
	title = fmt.Sprintf("回顾 %s", window.Label)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# %s\n\n", title))
	b.WriteString(fmt.Sprintf(
		"_窗口：%s · %s → %s（%s）_\n\n",
		window.Kind,
		window.Start.UTC().Format(time.RFC3339),
		window.End.UTC().Format(time.RFC3339),
		window.Timezone,
	))

	b.WriteString("## 数据源\n\n")
	if len(sourcesUsed) > 0 {
		b.WriteString("- 已用：" + strings.Join(sourcesUsed, ", ") + "\n")
	}
	if len(sourcesEmpty) > 0 {
		b.WriteString("- 已请求但无事件：" + strings.Join(sourcesEmpty, ", ") + "\n")
	}
	if len(sourcesSkipped) > 0 {
		b.WriteString("- 已关闭：" + strings.Join(sourcesSkipped, ", ") + "\n")
	}
	b.WriteString("\n")

	b.WriteString("## Issue 活动\n\n")
	if len(facts.Issues) == 0 {
		b.WriteString("_本窗口无 Issue 活动（或未启用该源）。_\n\n")
	} else {
		for _, fact := range facts.Issues {
			label := fact.Identifier
			if label == "" {
				label = fact.IssueID
			}
			mention := fmt.Sprintf("[%s](mention://issue/%s)", label, fact.IssueID)
			line := fmt.Sprintf("- %s %s — %s", fact.At.UTC().Format("15:04"), mention, fact.Title)
			if fact.Detail != "" {
				line += " · " + fact.Detail
			} else if fact.Action != "" {
				line += " · `" + fact.Action + "`"
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("## 更新过的笔记\n\n")
	if len(facts.Notes) == 0 {
		b.WriteString("_本窗口无笔记更新（或未启用该源）。_\n\n")
	} else {
		for _, fact := range facts.Notes {
			titleText := strings.TrimSpace(fact.Title)
			if titleText == "" {
				titleText = "Untitled"
			}
			b.WriteString(fmt.Sprintf("- %s 「%s」 (`%s`)\n", fact.At.UTC().Format("15:04"), titleText, fact.PageID))
		}
		b.WriteString("\n")
	}

	if containsNoteRetrospectiveSource(sourcesSkipped, noteRetrospectiveSourceRuns) || containsNoteRetrospectiveSource(sourcesEmpty, noteRetrospectiveSourceRuns) {
		b.WriteString("## Agent runs\n\n_本版本未接入 run 摘要；关闭或留空属预期降级。_\n\n")
	}

	return title, b.String()
}

func containsNoteRetrospectiveSource(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func formatIssueActivityDetail(action string, detailsJSON []byte) string {
	switch action {
	case "status_changed":
		from, to := jsonStringField(detailsJSON, "from"), jsonStringField(detailsJSON, "to")
		if from != "" || to != "" {
			return fmt.Sprintf("%s → %s", emptyDash(from), emptyDash(to))
		}
	case "assignee_changed":
		toType, toID := jsonStringField(detailsJSON, "to_type"), jsonStringField(detailsJSON, "to_id")
		if toType != "" || toID != "" {
			return fmt.Sprintf("assignee %s:%s", emptyDash(toType), emptyDash(toID))
		}
	case "created":
		return "created"
	}
	if action != "" {
		return action
	}
	return ""
}

func emptyDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func jsonStringField(raw []byte, key string) string {
	if len(raw) == 0 {
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	v, ok := obj[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}
