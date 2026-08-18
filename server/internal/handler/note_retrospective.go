package handler

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// S4-S1 on-demand retrospective: aggregate known Facts into a private note
// under 回顾/. Product note writeback ≠ Agent Daily (S3-W3) — this path never
// reads memory/daily.
//
// S4-S2 attribution: Facts are bucketed into hands_on / delegated / related so
// the note never collapses everything into a single "I did this" list.
//
// S4-S3 layered long windows: week/month compose from day (and for month,
// week) summaries — never dump a month of raw activity lines.

const (
	noteRetrospectiveFolderTitle = "回顾"
	noteRetrospectiveSourceIssue = "issue_activity"
	noteRetrospectiveSourceNotes = "touched_notes"
	noteRetrospectiveSourceRuns  = "agent_runs"

	noteRetrospectiveAttrHandsOn   = "hands_on"
	noteRetrospectiveAttrDelegated = "delegated"
	noteRetrospectiveAttrRelated   = "related"

	noteRetrospectiveCompositionDayRaw  = "day_raw"
	noteRetrospectiveCompositionLayered = "layered_summaries"

	noteRetrospectiveDigestMaxIssues = 5
	noteRetrospectiveDigestMaxNotes  = 5
	noteRetrospectiveExcerptMaxLines = 12
)

var noteRetrospectiveDefaultSources = []string{
	noteRetrospectiveSourceIssue,
	noteRetrospectiveSourceNotes,
}

type noteRetrospectiveWindowKind string

const (
	noteRetrospectiveWindowDay   noteRetrospectiveWindowKind = "day"
	noteRetrospectiveWindowWeek  noteRetrospectiveWindowKind = "week"
	noteRetrospectiveWindowMonth noteRetrospectiveWindowKind = "month"
)

type noteRetrospectiveIssueFact struct {
	IssueID      string
	Identifier   string
	Title        string
	Action       string
	Detail       string
	At           time.Time
	ActorType    string
	ActorID      string
	AgentID      string // for mention when delegated (actor agent or assignee target)
	AgentName    string
	Attribution  string // hands_on | delegated | related
	PullRequests []noteRetrospectivePullRequestFact
}

// Linked GitHub PR identity for Period Work Facts (no patch / diff body).
type noteRetrospectivePullRequestFact struct {
	Number int32
	URL    string
	State  string
	Title  string
}

type noteRetrospectiveNoteFact struct {
	PageID      string
	Title       string
	At          time.Time
	Attribution string
}

// Agent inbox run (agent_inbox_event) projected into the retrospective.
// Short text only — never raw thinking / result payloads.
type noteRetrospectiveRunFact struct {
	RunID           string
	AgentID         string
	AgentName       string
	Summary         string
	Outcome         string
	IssueID         string
	IssueIdentifier string
	IssueTitle      string
	At              time.Time
	Attribution     string // always delegated for MVP
}

type noteRetrospectiveFacts struct {
	Issues []noteRetrospectiveIssueFact
	Notes  []noteRetrospectiveNoteFact
	Runs   []noteRetrospectiveRunFact
}

type noteRetrospectiveWindow struct {
	Kind     noteRetrospectiveWindowKind
	Timezone string
	Start    time.Time // inclusive, UTC instant
	End      time.Time // exclusive, UTC instant
	Label    string    // calendar label in viewing tz
}

// Existing private retrospective leaf under 回顾/, reused as a child summary.
type noteRetrospectiveChildNote struct {
	PageID  string
	Title   string
	Content string
}

// Compact per-day (or reused) summary used as input to week/month composition.
type noteRetrospectivePeriodSummary struct {
	Kind            string // day | week
	Label           string
	Source          string // synthesized | existing_note
	PageID          string
	PageTitle       string
	Excerpt         string
	HandsIssueCount int
	DelegatedCount  int
	RelatedCount    int
	NoteCount       int
	RunCount        int
	HandsIssueLines []string
	DelegatedLines  []string
	RelatedLines    []string
	NoteLines       []string
	RunLines        []string
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
// half-open UTC interval.
// Week = Monday 00:00 → next Monday 00:00; Month = 1st → next month 1st.
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
	case noteRetrospectiveWindowMonth:
		startLocal := time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, loc)
		endLocal := startLocal.AddDate(0, 1, 0)
		return noteRetrospectiveWindow{
			Kind:     kind,
			Timezone: tzName,
			Start:    startLocal.UTC(),
			End:      endLocal.UTC(),
			Label:    startLocal.Format("2006-01"),
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
		return noteRetrospectiveWindow{}, fmt.Errorf("invalid window %q (want day|week|month)", kind)
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
	enabled = make([]string, 0, 3)
	skipped = make([]string, 0, 3)
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

// classifyNoteRetrospectiveIssueAttribution (S4-S2):
//   - hands_on: viewer member acted, except assigning an agent
//   - delegated: viewer assigned an agent, or an agent acted on a related issue
//   - related: another human acted on an issue related to the viewer
func classifyNoteRetrospectiveIssueAttribution(viewerUserID, actorType, actorID, action string, detailsJSON []byte) string {
	viewerUserID = strings.TrimSpace(viewerUserID)
	actorType = strings.TrimSpace(actorType)
	actorID = strings.TrimSpace(actorID)
	if actorType == "member" && actorID != "" && actorID == viewerUserID {
		if action == "assignee_changed" && jsonStringField(detailsJSON, "to_type") == "agent" {
			return noteRetrospectiveAttrDelegated
		}
		return noteRetrospectiveAttrHandsOn
	}
	if actorType == "agent" {
		return noteRetrospectiveAttrDelegated
	}
	return noteRetrospectiveAttrRelated
}

func noteRetrospectiveTitleForLabel(label string) string {
	return "回顾 " + strings.TrimSpace(label)
}

// noteRetrospectiveDayLabels returns each local calendar day (YYYY-MM-DD) in
// the half-open window, in viewing timezone order.
func noteRetrospectiveDayLabels(window noteRetrospectiveWindow) []string {
	loc := resolveNoteRetrospectiveLocation(window.Timezone)
	startLocal := window.Start.In(loc)
	startDay := time.Date(startLocal.Year(), startLocal.Month(), startLocal.Day(), 0, 0, 0, 0, loc)
	endLocal := window.End.In(loc)
	endDay := time.Date(endLocal.Year(), endLocal.Month(), endLocal.Day(), 0, 0, 0, 0, loc)
	out := make([]string, 0)
	for d := startDay; d.Before(endDay); d = d.AddDate(0, 0, 1) {
		out = append(out, d.Format("2006-01-02"))
	}
	return out
}

// noteRetrospectiveWeekLabels returns ISO week labels (YYYY-Www) covering the
// window (used for month composition).
func noteRetrospectiveWeekLabels(window noteRetrospectiveWindow) []string {
	loc := resolveNoteRetrospectiveLocation(window.Timezone)
	startLocal := window.Start.In(loc)
	startDay := time.Date(startLocal.Year(), startLocal.Month(), startLocal.Day(), 0, 0, 0, 0, loc)
	offset := (int(startDay.Weekday()) + 6) % 7
	weekStart := startDay.AddDate(0, 0, -offset)
	endLocal := window.End.In(loc)
	endDay := time.Date(endLocal.Year(), endLocal.Month(), endLocal.Day(), 0, 0, 0, 0, loc)

	seen := map[string]struct{}{}
	out := make([]string, 0)
	for d := weekStart; d.Before(endDay); d = d.AddDate(0, 0, 7) {
		y, w := d.ISOWeek()
		label := fmt.Sprintf("%d-W%02d", y, w)
		if _, ok := seen[label]; ok {
			continue
		}
		seen[label] = struct{}{}
		out = append(out, label)
	}
	return out
}

func partitionNoteRetrospectiveFactsByDay(facts noteRetrospectiveFacts, tz string) map[string]noteRetrospectiveFacts {
	loc := resolveNoteRetrospectiveLocation(tz)
	out := map[string]noteRetrospectiveFacts{}
	for _, fact := range facts.Issues {
		label := fact.At.In(loc).Format("2006-01-02")
		bucket := out[label]
		bucket.Issues = append(bucket.Issues, fact)
		out[label] = bucket
	}
	for _, fact := range facts.Notes {
		label := fact.At.In(loc).Format("2006-01-02")
		bucket := out[label]
		bucket.Notes = append(bucket.Notes, fact)
		out[label] = bucket
	}
	for _, fact := range facts.Runs {
		label := fact.At.In(loc).Format("2006-01-02")
		bucket := out[label]
		bucket.Runs = append(bucket.Runs, fact)
		out[label] = bucket
	}
	return out
}

func synthesizeNoteRetrospectiveDaySummary(label string, facts noteRetrospectiveFacts) noteRetrospectivePeriodSummary {
	hands, delegated, related := splitNoteRetrospectiveIssues(facts.Issues)
	notes := filterNoteRetrospectiveNotes(facts.Notes, noteRetrospectiveAttrHandsOn)
	return noteRetrospectivePeriodSummary{
		Kind:            "day",
		Label:           label,
		Source:          "synthesized",
		HandsIssueCount: len(hands),
		DelegatedCount:  len(delegated),
		RelatedCount:    len(related),
		NoteCount:       len(notes),
		RunCount:        len(facts.Runs),
		HandsIssueLines: uniqueNoteRetrospectiveIssueMentions(hands, noteRetrospectiveDigestMaxIssues),
		DelegatedLines:  uniqueNoteRetrospectiveIssueMentions(delegated, noteRetrospectiveDigestMaxIssues),
		RelatedLines:    uniqueNoteRetrospectiveIssueMentions(related, noteRetrospectiveDigestMaxIssues),
		NoteLines:       uniqueNoteRetrospectiveNoteLines(notes, noteRetrospectiveDigestMaxNotes),
		RunLines:        uniqueNoteRetrospectiveRunLines(facts.Runs, noteRetrospectiveDigestMaxIssues),
	}
}

func summaryFromExistingNoteRetrospective(kind, label string, child noteRetrospectiveChildNote) noteRetrospectivePeriodSummary {
	return noteRetrospectivePeriodSummary{
		Kind:      kind,
		Label:     label,
		Source:    "existing_note",
		PageID:    child.PageID,
		PageTitle: child.Title,
		Excerpt:   excerptNoteRetrospectiveContent(child.Content, noteRetrospectiveExcerptMaxLines),
	}
}

func uniqueNoteRetrospectiveIssueMentions(facts []noteRetrospectiveIssueFact, limit int) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, fact := range facts {
		if fact.IssueID == "" {
			continue
		}
		if _, ok := seen[fact.IssueID]; ok {
			continue
		}
		seen[fact.IssueID] = struct{}{}
		label := fact.Identifier
		if label == "" {
			label = fact.IssueID
		}
		out = append(out, fmt.Sprintf("[%s](mention://issue/%s) %s", label, fact.IssueID, fact.Title))
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func uniqueNoteRetrospectiveNoteLines(facts []noteRetrospectiveNoteFact, limit int) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, fact := range facts {
		if fact.PageID == "" {
			continue
		}
		if _, ok := seen[fact.PageID]; ok {
			continue
		}
		seen[fact.PageID] = struct{}{}
		titleText := strings.TrimSpace(fact.Title)
		if titleText == "" {
			titleText = "Untitled"
		}
		out = append(out, fmt.Sprintf("「%s」 (`%s`)", titleText, fact.PageID))
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func uniqueNoteRetrospectiveRunLines(facts []noteRetrospectiveRunFact, limit int) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, fact := range facts {
		if fact.RunID == "" {
			continue
		}
		if _, ok := seen[fact.RunID]; ok {
			continue
		}
		seen[fact.RunID] = struct{}{}
		out = append(out, formatNoteRetrospectiveRunDigestLine(fact))
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// formatNoteRetrospectiveRunSummary picks a short human line. Never includes
// thinking / result blobs — empty trigger_summary degrades to outcome/status.
func formatNoteRetrospectiveRunSummary(triggerSummary, terminalOutcome, status string) string {
	summary := strings.TrimSpace(triggerSummary)
	if summary != "" {
		summary = strings.ReplaceAll(summary, "\n", " ")
		runes := []rune(summary)
		if len(runes) > 160 {
			summary = string(runes[:160]) + "…"
		}
		return summary
	}
	outcome := strings.TrimSpace(terminalOutcome)
	if outcome == "" {
		outcome = strings.TrimSpace(status)
	}
	if outcome == "" {
		outcome = "unknown"
	}
	return "无摘要 · " + outcome
}

func formatNoteRetrospectiveRunLine(fact noteRetrospectiveRunFact) string {
	agentLabel := strings.TrimSpace(fact.AgentName)
	if agentLabel == "" {
		agentLabel = fact.AgentID
	}
	line := fmt.Sprintf(
		"- %s [run](mention://run/%s) · [%s](mention://agent/%s)",
		fact.At.UTC().Format("15:04"),
		fact.RunID,
		agentLabel,
		fact.AgentID,
	)
	if fact.Summary != "" {
		line += " — " + fact.Summary
	}
	if fact.IssueID != "" {
		label := fact.IssueIdentifier
		if label == "" {
			label = fact.IssueID
		}
		line += fmt.Sprintf(" · [%s](mention://issue/%s)", label, fact.IssueID)
	}
	return line
}

func formatNoteRetrospectiveRunDigestLine(fact noteRetrospectiveRunFact) string {
	agentLabel := strings.TrimSpace(fact.AgentName)
	if agentLabel == "" {
		agentLabel = fact.AgentID
	}
	line := fmt.Sprintf("[run](mention://run/%s) · [%s](mention://agent/%s)", fact.RunID, agentLabel, fact.AgentID)
	if fact.Summary != "" {
		line += " — " + fact.Summary
	}
	return line
}

func excerptNoteRetrospectiveContent(content string, maxLines int) string {
	lines := strings.Split(content, "\n")
	kept := make([]string, 0, maxLines)
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "# ") {
			continue
		}
		kept = append(kept, line)
		if maxLines > 0 && len(kept) >= maxLines {
			break
		}
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

// composeNoteRetrospectivePeriodSummaries builds the child inputs for a
// week/month note. Existing 回顾 leaves win; missing days synthesize digests.
// Month prefers week leaves when present; otherwise falls through to days.
func composeNoteRetrospectivePeriodSummaries(
	window noteRetrospectiveWindow,
	facts noteRetrospectiveFacts,
	childrenByTitle map[string]noteRetrospectiveChildNote,
) (summaries []noteRetrospectivePeriodSummary, layers []string) {
	byDay := partitionNoteRetrospectiveFactsByDay(facts, window.Timezone)

	switch window.Kind {
	case noteRetrospectiveWindowWeek:
		layers = []string{"day"}
		for _, dayLabel := range noteRetrospectiveDayLabels(window) {
			title := noteRetrospectiveTitleForLabel(dayLabel)
			if child, ok := childrenByTitle[title]; ok {
				summaries = append(summaries, summaryFromExistingNoteRetrospective("day", dayLabel, child))
				continue
			}
			dayFacts := byDay[dayLabel]
			if len(dayFacts.Issues) == 0 && len(dayFacts.Notes) == 0 && len(dayFacts.Runs) == 0 {
				continue
			}
			summaries = append(summaries, synthesizeNoteRetrospectiveDaySummary(dayLabel, dayFacts))
		}
	case noteRetrospectiveWindowMonth:
		layers = []string{"week", "day"}
		coveredDays := map[string]struct{}{}
		for _, weekLabel := range noteRetrospectiveWeekLabels(window) {
			title := noteRetrospectiveTitleForLabel(weekLabel)
			if child, ok := childrenByTitle[title]; ok {
				summaries = append(summaries, summaryFromExistingNoteRetrospective("week", weekLabel, child))
				// Mark days of that ISO week as covered so we don't also dump day digests.
				for _, dayLabel := range daysInISOWeekLabel(weekLabel, window.Timezone) {
					coveredDays[dayLabel] = struct{}{}
				}
			}
		}
		for _, dayLabel := range noteRetrospectiveDayLabels(window) {
			if _, covered := coveredDays[dayLabel]; covered {
				continue
			}
			title := noteRetrospectiveTitleForLabel(dayLabel)
			if child, ok := childrenByTitle[title]; ok {
				summaries = append(summaries, summaryFromExistingNoteRetrospective("day", dayLabel, child))
				continue
			}
			dayFacts := byDay[dayLabel]
			if len(dayFacts.Issues) == 0 && len(dayFacts.Notes) == 0 && len(dayFacts.Runs) == 0 {
				continue
			}
			summaries = append(summaries, synthesizeNoteRetrospectiveDaySummary(dayLabel, dayFacts))
		}
	}
	return summaries, layers
}

func daysInISOWeekLabel(weekLabel, tz string) []string {
	// weekLabel = YYYY-Www
	parts := strings.Split(weekLabel, "-W")
	if len(parts) != 2 {
		return nil
	}
	var year, week int
	if _, err := fmt.Sscanf(parts[0], "%d", &year); err != nil {
		return nil
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &week); err != nil {
		return nil
	}
	loc := resolveNoteRetrospectiveLocation(tz)
	// Find Monday of ISO week: start from Jan 4 (always in week 1) then adjust.
	jan4 := time.Date(year, 1, 4, 0, 0, 0, 0, loc)
	offset := (int(jan4.Weekday()) + 6) % 7
	week1Monday := jan4.AddDate(0, 0, -offset)
	monday := week1Monday.AddDate(0, 0, (week-1)*7)
	out := make([]string, 0, 7)
	for i := 0; i < 7; i++ {
		out = append(out, monday.AddDate(0, 0, i).Format("2006-01-02"))
	}
	return out
}

func buildNoteRetrospectiveMarkdown(
	window noteRetrospectiveWindow,
	facts noteRetrospectiveFacts,
	sourcesUsed, sourcesEmpty, sourcesSkipped []string,
) (title string, content string) {
	title = noteRetrospectiveTitleForLabel(window.Label)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# %s\n\n", title))
	b.WriteString(fmt.Sprintf(
		"_窗口：%s · %s → %s（%s）_\n\n",
		window.Kind,
		window.Start.UTC().Format(time.RFC3339),
		window.End.UTC().Format(time.RFC3339),
		window.Timezone,
	))

	writeNoteRetrospectiveSourcesSection(&b, sourcesUsed, sourcesEmpty, sourcesSkipped)

	handsIssues, delegatedIssues, relatedIssues := splitNoteRetrospectiveIssues(facts.Issues)
	handsNotes := filterNoteRetrospectiveNotes(facts.Notes, noteRetrospectiveAttrHandsOn)

	writeNoteRetrospectiveAttributionSection(&b, "亲手", handsIssues, handsNotes, true)
	writeNoteRetrospectiveDelegatedSection(&b, delegatedIssues, facts.Runs)
	writeNoteRetrospectiveAttributionSection(&b, "仅相关", relatedIssues, nil, false)

	return title, b.String()
}

func buildNoteRetrospectiveLayeredMarkdown(
	window noteRetrospectiveWindow,
	summaries []noteRetrospectivePeriodSummary,
	layers []string,
	sourcesUsed, sourcesEmpty, sourcesSkipped []string,
) (title string, content string) {
	title = noteRetrospectiveTitleForLabel(window.Label)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# %s\n\n", title))
	b.WriteString(fmt.Sprintf(
		"_窗口：%s · %s → %s（%s）_\n\n",
		window.Kind,
		window.Start.UTC().Format(time.RFC3339),
		window.End.UTC().Format(time.RFC3339),
		window.Timezone,
	))

	writeNoteRetrospectiveSourcesSection(&b, sourcesUsed, sourcesEmpty, sourcesSkipped)

	b.WriteString("## 分层说明\n\n")
	switch window.Kind {
	case noteRetrospectiveWindowMonth:
		b.WriteString("本月由 **周摘要 / 日摘要** 汇总而成，**不**灌入全月原始事件。已有 `回顾/` 子页优先复用；缺省日在内存生成日摘要。\n\n")
	default:
		b.WriteString("本周由 **日摘要** 汇总而成，**不**灌入全周原始事件。已有日回顾笔记优先复用；缺省日在内存生成日摘要。\n\n")
	}
	if len(layers) > 0 {
		b.WriteString("- 层级：" + strings.Join(layers, " → ") + "\n\n")
	}

	if len(summaries) == 0 {
		b.WriteString("## 摘要\n\n_本窗口无可汇总的日/周摘要。_\n\n")
	} else {
		b.WriteString("## 摘要\n\n")
		for _, summary := range summaries {
			writeNoteRetrospectivePeriodSummary(&b, summary)
		}
	}

	return title, b.String()
}

func writeNoteRetrospectiveSourcesSection(b *strings.Builder, sourcesUsed, sourcesEmpty, sourcesSkipped []string) {
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
}

func writeNoteRetrospectivePeriodSummary(b *strings.Builder, summary noteRetrospectivePeriodSummary) {
	kindLabel := "日"
	if summary.Kind == "week" {
		kindLabel = "周"
	}
	if summary.Source == "existing_note" {
		b.WriteString(fmt.Sprintf("### %s · 复用已有%s回顾\n\n", summary.Label, kindLabel))
		titleText := strings.TrimSpace(summary.PageTitle)
		if titleText == "" {
			titleText = noteRetrospectiveTitleForLabel(summary.Label)
		}
		b.WriteString(fmt.Sprintf("- 笔记：「%s」 (`%s`)\n", titleText, summary.PageID))
		if summary.Excerpt != "" {
			b.WriteString("\n摘录：\n\n")
			for _, line := range strings.Split(summary.Excerpt, "\n") {
				b.WriteString("> " + line + "\n")
			}
			b.WriteString("\n")
		} else {
			b.WriteString("\n")
		}
		return
	}

	b.WriteString(fmt.Sprintf("### %s · 合成%s摘要\n\n", summary.Label, kindLabel))
	if summary.HandsIssueCount == 0 && summary.DelegatedCount == 0 && summary.RelatedCount == 0 && summary.NoteCount == 0 && summary.RunCount == 0 {
		b.WriteString("_无。_\n\n")
		return
	}
	b.WriteString(fmt.Sprintf(
		"- 亲手：%d 条 Issue · %d 篇笔记\n- 委派 Agent：%d 条 Issue · %d 次 run\n- 仅相关：%d 条\n",
		summary.HandsIssueCount, summary.NoteCount, summary.DelegatedCount, summary.RunCount, summary.RelatedCount,
	))
	writeDigestLines(b, "亲手 Issue", summary.HandsIssueLines)
	writeDigestLines(b, "委派 Issue", summary.DelegatedLines)
	writeDigestLines(b, "仅相关 Issue", summary.RelatedLines)
	writeDigestLines(b, "笔记", summary.NoteLines)
	writeDigestLines(b, "Agent runs", summary.RunLines)
	b.WriteString("\n")
}

func writeDigestLines(b *strings.Builder, heading string, lines []string) {
	if len(lines) == 0 {
		return
	}
	b.WriteString("- " + heading + "：\n")
	for _, line := range lines {
		b.WriteString("  - " + line + "\n")
	}
}

func splitNoteRetrospectiveIssues(facts []noteRetrospectiveIssueFact) (hands, delegated, related []noteRetrospectiveIssueFact) {
	for _, fact := range facts {
		switch fact.Attribution {
		case noteRetrospectiveAttrDelegated:
			delegated = append(delegated, fact)
		case noteRetrospectiveAttrRelated:
			related = append(related, fact)
		default:
			hands = append(hands, fact)
		}
	}
	return hands, delegated, related
}

func filterNoteRetrospectiveNotes(facts []noteRetrospectiveNoteFact, want string) []noteRetrospectiveNoteFact {
	out := make([]noteRetrospectiveNoteFact, 0)
	for _, fact := range facts {
		attr := fact.Attribution
		if attr == "" {
			attr = noteRetrospectiveAttrHandsOn
		}
		if attr == want {
			out = append(out, fact)
		}
	}
	return out
}

func writeNoteRetrospectiveDelegatedSection(
	b *strings.Builder,
	issues []noteRetrospectiveIssueFact,
	runs []noteRetrospectiveRunFact,
) {
	b.WriteString("## 委派 Agent\n\n")
	if len(issues) == 0 && len(runs) == 0 {
		b.WriteString("_无。_\n\n")
		return
	}
	b.WriteString("### Issue\n\n")
	if len(issues) == 0 {
		b.WriteString("_无。_\n\n")
	} else {
		for _, fact := range issues {
			b.WriteString(formatNoteRetrospectiveIssueLine(fact) + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("### Agent runs\n\n")
	if len(runs) == 0 {
		b.WriteString("_无。_\n\n")
		return
	}
	for _, fact := range runs {
		b.WriteString(formatNoteRetrospectiveRunLine(fact) + "\n")
	}
	b.WriteString("\n")
}

func writeNoteRetrospectiveAttributionSection(
	b *strings.Builder,
	heading string,
	issues []noteRetrospectiveIssueFact,
	notes []noteRetrospectiveNoteFact,
	includeNotes bool,
) {
	b.WriteString("## " + heading + "\n\n")
	if len(issues) == 0 && (!includeNotes || len(notes) == 0) {
		b.WriteString("_无。_\n\n")
		return
	}
	if len(issues) > 0 {
		b.WriteString("### Issue\n\n")
		for _, fact := range issues {
			b.WriteString(formatNoteRetrospectiveIssueLine(fact) + "\n")
		}
		b.WriteString("\n")
	} else {
		b.WriteString("### Issue\n\n_无。_\n\n")
	}
	if includeNotes {
		b.WriteString("### 笔记\n\n")
		if len(notes) == 0 {
			b.WriteString("_无。_\n\n")
		} else {
			for _, fact := range notes {
				titleText := strings.TrimSpace(fact.Title)
				if titleText == "" {
					titleText = "Untitled"
				}
				b.WriteString(fmt.Sprintf("- %s 「%s」 (`%s`)\n", fact.At.UTC().Format("15:04"), titleText, fact.PageID))
			}
			b.WriteString("\n")
		}
	}
}

func formatNoteRetrospectiveIssueLine(fact noteRetrospectiveIssueFact) string {
	label := fact.Identifier
	if label == "" {
		label = fact.IssueID
	}
	mention := fmt.Sprintf("[%s](mention://issue/%s)", label, fact.IssueID)
	line := fmt.Sprintf("- %s %s — %s", fact.At.UTC().Format("15:04"), mention, fact.Title)
	if fact.Attribution == noteRetrospectiveAttrDelegated && fact.AgentID != "" {
		agentLabel := strings.TrimSpace(fact.AgentName)
		if agentLabel == "" {
			agentLabel = fact.AgentID
		}
		agentMention := fmt.Sprintf("[%s](mention://agent/%s)", agentLabel, fact.AgentID)
		if fact.ActorType == "agent" {
			line += " · 由 " + agentMention + " 执行"
		} else {
			line += " · 委派给 " + agentMention
		}
	}
	if fact.Detail != "" {
		line += " · " + fact.Detail
	} else if fact.Action != "" {
		line += " · `" + fact.Action + "`"
	}
	return line
}

func containsNoteRetrospectiveSource(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func expectedNoteRetrospectiveChildTitles(window noteRetrospectiveWindow) []string {
	titles := make([]string, 0)
	seen := map[string]struct{}{}
	add := func(label string) {
		title := noteRetrospectiveTitleForLabel(label)
		if _, ok := seen[title]; ok {
			return
		}
		seen[title] = struct{}{}
		titles = append(titles, title)
	}
	for _, day := range noteRetrospectiveDayLabels(window) {
		add(day)
	}
	if window.Kind == noteRetrospectiveWindowMonth {
		for _, week := range noteRetrospectiveWeekLabels(window) {
			add(week)
		}
	}
	sort.Strings(titles)
	return titles
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
