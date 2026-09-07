package handler

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const notePeriodBriefUserFocusMaxRunes = 4000

// notePeriodBriefCollectorScope is the scoped harvest brief for one collector.
// Empty means full SCAN_ROOTS (today's default).
type notePeriodBriefCollectorScope struct {
	Paths   []string
	Topics  []string
	Aspects []string
	Brief   string
}

func (s notePeriodBriefCollectorScope) Empty() bool {
	return len(s.Paths) == 0 && len(s.Topics) == 0 && len(s.Aspects) == 0 && strings.TrimSpace(s.Brief) == ""
}

// notePeriodBriefCollectAssignment is one collector row in a Notes Assistant plan.
type notePeriodBriefCollectAssignment struct {
	CollectorAgentID string   `json:"collector_agent_id"`
	Skip             bool     `json:"skip"`
	Paths            []string `json:"paths,omitempty"`
	Topics           []string `json:"topics,omitempty"`
	Aspects          []string `json:"aspects,omitempty"`
	Brief            string   `json:"brief,omitempty"`
}

// notePeriodBriefCollectPlan is the Notes Assistant collect-plan artifact.
type notePeriodBriefCollectPlan struct {
	Summary     string                             `json:"summary"`
	Assignments []notePeriodBriefCollectAssignment `json:"assignments"`
}

type notePeriodBriefAppliedPlan struct {
	DispatchIDs []string
	Scopes      map[string]notePeriodBriefCollectorScope
	Summary     string
	Fallback    bool
}

func normalizePeriodBriefUserFocus(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if utf8.RuneCountInString(s) <= notePeriodBriefUserFocusMaxRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:notePeriodBriefUserFocusMaxRunes])
}

func defaultPeriodBriefAppliedPlan(selected []string) notePeriodBriefAppliedPlan {
	scopes := make(map[string]notePeriodBriefCollectorScope, len(selected))
	ids := append([]string(nil), selected...)
	return notePeriodBriefAppliedPlan{
		DispatchIDs: ids,
		Scopes:      scopes,
		Summary:     "full-scope default",
		Fallback:    true,
	}
}

// applyNotePeriodBriefCollectPlan keeps only human-selected collectors.
// Unknown ids are ignored. Unlisted selected collectors are treated as skip.
// If nothing remains to dispatch, fall back to full-scope on every selected collector.
func applyNotePeriodBriefCollectPlan(selected []string, plan *notePeriodBriefCollectPlan) notePeriodBriefAppliedPlan {
	fallback := defaultPeriodBriefAppliedPlan(selected)
	if plan == nil || len(plan.Assignments) == 0 {
		return fallback
	}
	allowed := make(map[string]struct{}, len(selected))
	for _, id := range selected {
		id = strings.TrimSpace(id)
		if id != "" {
			allowed[id] = struct{}{}
		}
	}
	dispatch := make([]string, 0, len(selected))
	scopes := make(map[string]notePeriodBriefCollectorScope, len(selected))
	seen := make(map[string]struct{}, len(selected))
	for _, raw := range plan.Assignments {
		id := strings.TrimSpace(raw.CollectorAgentID)
		if id == "" {
			continue
		}
		if _, ok := allowed[id]; !ok {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		if raw.Skip {
			continue
		}
		dispatch = append(dispatch, id)
		scopes[id] = notePeriodBriefCollectorScope{
			Paths:   cleanPeriodBriefScopeList(raw.Paths),
			Topics:  cleanPeriodBriefScopeList(raw.Topics),
			Aspects: cleanPeriodBriefScopeList(raw.Aspects),
			Brief:   strings.TrimSpace(raw.Brief),
		}
	}
	if len(dispatch) == 0 {
		return fallback
	}
	summary := strings.TrimSpace(plan.Summary)
	if summary == "" {
		summary = "scoped collect from notes assistant plan"
	}
	return notePeriodBriefAppliedPlan{
		DispatchIDs: dispatch,
		Scopes:      scopes,
		Summary:     summary,
		Fallback:    false,
	}
}

func cleanPeriodBriefScopeList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func scopeFromCollectPlan(plan *notePeriodBriefCollectPlan, agentID string) notePeriodBriefCollectorScope {
	if plan == nil {
		return notePeriodBriefCollectorScope{}
	}
	id := strings.TrimSpace(agentID)
	for _, a := range plan.Assignments {
		if strings.TrimSpace(a.CollectorAgentID) == id && !a.Skip {
			return notePeriodBriefCollectorScope{
				Paths:   cleanPeriodBriefScopeList(a.Paths),
				Topics:  cleanPeriodBriefScopeList(a.Topics),
				Aspects: cleanPeriodBriefScopeList(a.Aspects),
				Brief:   strings.TrimSpace(a.Brief),
			}
		}
	}
	return notePeriodBriefCollectorScope{}
}

func formatNotePeriodBriefFocusPartition(userFocus, planSummary string, scope notePeriodBriefCollectorScope) string {
	userFocus = strings.TrimSpace(userFocus)
	planSummary = strings.TrimSpace(planSummary)
	if userFocus == "" && planSummary == "" && scope.Empty() {
		return ""
	}
	var b strings.Builder
	if userFocus != "" {
		b.WriteString("human_request:\n")
		b.WriteString(userFocus)
		b.WriteByte('\n')
	}
	if planSummary != "" {
		b.WriteString("planner_summary:\n")
		b.WriteString(planSummary)
		b.WriteByte('\n')
	}
	if !scope.Empty() {
		if len(scope.Paths) > 0 {
			b.WriteString("paths:\n")
			for _, p := range scope.Paths {
				fmt.Fprintf(&b, "- %s\n", p)
			}
		}
		if len(scope.Topics) > 0 {
			b.WriteString("topics:\n")
			for _, t := range scope.Topics {
				fmt.Fprintf(&b, "- %s\n", t)
			}
		}
		if len(scope.Aspects) > 0 {
			b.WriteString("aspects:\n")
			for _, a := range scope.Aspects {
				fmt.Fprintf(&b, "- %s\n", a)
			}
		}
		if strings.TrimSpace(scope.Brief) != "" {
			b.WriteString("collector_brief:\n")
			b.WriteString(strings.TrimSpace(scope.Brief))
			b.WriteByte('\n')
		}
	}
	return strings.TrimSpace(b.String())
}

func formatNotePeriodBriefRoster(agents []notePeriodBriefRosterRow) string {
	if len(agents) == 0 {
		return "(no collectors)"
	}
	var b strings.Builder
	b.WriteString("Human-selected collectors. Assign only these ids; you cannot add others.\n")
	for _, row := range agents {
		label := strings.TrimSpace(row.DisplayName)
		if label == "" {
			label = row.Name
		}
		mode := strings.TrimSpace(row.RuntimeMode)
		if mode == "" {
			mode = "local"
		}
		fmt.Fprintf(&b, "- id: %s\n  name: %s\n  display: %s\n  runtime_mode: %s\n",
			row.ID, row.Name, label, mode)
	}
	return strings.TrimSpace(b.String())
}

type notePeriodBriefRosterRow struct {
	ID          string
	Name        string
	DisplayName string
	RuntimeMode string
}
