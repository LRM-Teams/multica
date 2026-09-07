package handler

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/messageparts"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	periodBriefCarriedPackJobID     = "carried"
	periodBriefPackCardLabelPrefix  = "采集包 · "
	periodBriefSessionMaterialsOpen = "<period_brief_session_materials>"
)

type periodBriefSessionMaterial struct {
	AgentID     string
	Label       string
	Hostname    string
	OS          string
	WindowLabel string
	Markdown    string
	SourceRunID string
	InLatestRun bool
}

func periodBriefPackIsCarried(ref notePeriodBriefCollectorRef) bool {
	return strings.TrimSpace(ref.PackJobID) == periodBriefCarriedPackJobID ||
		strings.TrimSpace(ref.JobID) == periodBriefCarriedPackJobID
}

func periodBriefEmptyScanSignals() []string {
	return []string{
		"no in-window",
		"no scoped local work",
		"no scoped in-window",
		"no highlights beyond the empty",
		"gopath scan root with no in-window",
		"没有可用采集包",
		"未发现符合条件",
		"未发现 git",
	}
}

func periodBriefLineLooksEmptyScan(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "" {
		return false
	}
	for _, signal := range periodBriefEmptyScanSignals() {
		if strings.Contains(lower, signal) {
			return true
		}
	}
	return false
}

func periodBriefHighlightBullets(markdown string) []string {
	lines := strings.Split(markdown, "\n")
	in := false
	out := make([]string, 0)
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "## ") {
			heading := strings.TrimSpace(strings.TrimPrefix(trim, "## "))
			if in {
				break
			}
			in = strings.EqualFold(heading, "Highlights")
			continue
		}
		if !in {
			continue
		}
		if strings.HasPrefix(trim, "- ") || strings.HasPrefix(trim, "* ") {
			out = append(out, strings.TrimSpace(trim[2:]))
		}
	}
	return out
}

func periodBriefPackHasHarvest(markdown string) bool {
	md := strings.TrimSpace(markdown)
	if md == "" {
		return false
	}
	bullets := periodBriefHighlightBullets(md)
	if len(bullets) > 0 {
		for _, bullet := range bullets {
			if !periodBriefLineLooksEmptyScan(bullet) {
				return true
			}
		}
		return false
	}
	return !periodBriefLineLooksEmptyScan(md)
}

func periodBriefNormalizePackOS(env, hostname string) string {
	blob := strings.ToLower(strings.TrimSpace(env) + " " + strings.TrimSpace(hostname))
	switch {
	case strings.Contains(blob, "mingw"), strings.Contains(blob, "windows"), strings.Contains(blob, "win32"):
		return "windows"
	case strings.Contains(blob, "darwin"), strings.Contains(blob, "macos"), strings.Contains(blob, "mac os"):
		return "macos"
	case strings.Contains(blob, "linux"),
		strings.Contains(blob, "ubuntu"),
		strings.Contains(blob, "debian"),
		strings.Contains(blob, "fedora"),
		strings.Contains(blob, "centos"):
		return "linux"
	default:
		return ""
	}
}

// periodBriefPackMachineIdentity reads each collector pack's Runtime identity
// (hostname + OS family). There is no fixed machine roster.
func periodBriefPackMachineIdentity(markdown string) (hostname, osFamily string) {
	for _, line := range strings.Split(markdown, "\n") {
		trim := strings.TrimSpace(line)
		trim = strings.TrimPrefix(trim, "- ")
		lower := strings.ToLower(trim)
		if !strings.HasPrefix(lower, "hostname") {
			continue
		}
		_, rest, ok := strings.Cut(trim, ":")
		if !ok {
			continue
		}
		rest = strings.TrimSpace(rest)
		host, env, found := strings.Cut(rest, "/")
		hostname = strings.TrimSpace(host)
		if found {
			osFamily = periodBriefNormalizePackOS(strings.TrimSpace(env), hostname)
		} else {
			osFamily = periodBriefNormalizePackOS("", hostname)
		}
		if hostname != "" || osFamily != "" {
			return hostname, osFamily
		}
	}
	return "", ""
}

func formatPeriodBriefSessionMaterialsBoard(materials []periodBriefSessionMaterial) string {
	if len(materials) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(periodBriefSessionMaterialsOpen)
	b.WriteString("\n")
	b.WriteString("Ready computer harvests already in this bubble session. Read-only context — do not dump pack bodies here.\n")
	b.WriteString("computers:\n")
	for _, material := range materials {
		name := firstNonEmpty(material.Label, material.AgentID)
		inLatest := "no"
		if material.InLatestRun {
			inLatest = "yes"
		}
		b.WriteString("- name: ")
		b.WriteString(name)
		if id := strings.TrimSpace(material.AgentID); id != "" {
			b.WriteString(" id: ")
			b.WriteString(id)
		}
		if osFamily := strings.TrimSpace(material.OS); osFamily != "" {
			b.WriteString(" os: ")
			b.WriteString(osFamily)
		}
		if host := strings.TrimSpace(material.Hostname); host != "" {
			b.WriteString(" hostname: ")
			b.WriteString(host)
		}
		if label := strings.TrimSpace(material.WindowLabel); label != "" {
			b.WriteString(" window: ")
			b.WriteString(label)
		}
		b.WriteString(" in_latest_run: ")
		b.WriteString(inLatest)
		b.WriteString("\n")
	}
	b.WriteString("Match machine words to these rows by os, hostname, or collector name when answering questions. Do not change the collect window, computers, or focus. Do not call start.\n")
	b.WriteString("If the human asks 写汇报 / 重新采集, the platform opens the plan card. They choose the range and whether to continue. 开始采集 walks every selected computer, then writes the brief. Do not emit chat XML. Do not rewrite <period_brief> as chat markdown.\n")
	b.WriteString("</period_brief_session_materials>\n\n")
	return b.String()
}

func periodBriefUniqueCollectorIDs(selected []string) []string {
	out := make([]string, 0, len(selected))
	seen := make(map[string]struct{}, len(selected))
	for _, id := range selected {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func (h *Handler) loadPeriodBriefSessionMaterials(
	ctx context.Context,
	sessionID pgtype.UUID,
) []periodBriefSessionMaterial {
	if !sessionID.Valid {
		return nil
	}
	rows, err := h.DB.Query(ctx, `
SELECT r.id::text, r.workspace_id, r.window_label, r.collectors
FROM note_period_brief_run r
WHERE r.chat_session_id = $1
ORDER BY r.created_at ASC`, sessionID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	type sessionRun struct {
		ID          string
		WorkspaceID pgtype.UUID
		WindowLabel string
		Collectors  []notePeriodBriefCollectorRef
	}
	runs := make([]sessionRun, 0)
	for rows.Next() {
		var run sessionRun
		var raw []byte
		if scanErr := rows.Scan(&run.ID, &run.WorkspaceID, &run.WindowLabel, &raw); scanErr != nil {
			return nil
		}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &run.Collectors)
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil || len(runs) == 0 {
		return nil
	}

	byAgent := make(map[string]periodBriefSessionMaterial)
	agentIDs := make([]string, 0)
	seenAgent := make(map[string]struct{})
	latestCollectors := make(map[string]struct{})
	latest := runs[len(runs)-1]
	for _, ref := range latest.Collectors {
		if id := strings.TrimSpace(ref.AgentID); id != "" {
			latestCollectors[id] = struct{}{}
		}
	}
	for _, run := range runs {
		for _, ref := range run.Collectors {
			id := strings.TrimSpace(ref.AgentID)
			if id == "" {
				continue
			}
			if _, seen := seenAgent[id]; !seen {
				seenAgent[id] = struct{}{}
				agentIDs = append(agentIDs, id)
			}
			md := strings.TrimSpace(ref.PackMarkdown)
			if md == "" {
				continue
			}
			if periodBriefPackHasHarvest(md) {
				host, osFamily := periodBriefPackMachineIdentity(md)
				byAgent[id] = periodBriefSessionMaterial{
					AgentID:     id,
					Hostname:    host,
					OS:          osFamily,
					WindowLabel: firstNonEmpty(strings.TrimSpace(ref.WindowLabel), run.WindowLabel),
					Markdown:    md,
					SourceRunID: run.ID,
				}
				continue
			}
			delete(byAgent, id)
		}
	}

	workspaceID := latest.WorkspaceID
	names := h.periodBriefCollectorSpokenNames(ctx, workspaceID, agentIDs)
	nameByID := make(map[string]string, len(agentIDs))
	idByName := make(map[string]string, len(agentIDs))
	for i, id := range agentIDs {
		label := id
		if i < len(names) && strings.TrimSpace(names[i]) != "" {
			label = names[i]
		}
		nameByID[id] = label
		idByName[label] = id
	}

	for id, markdown := range h.loadPeriodBriefSessionPacksFromChat(ctx, sessionID) {
		agentID := idByName[id]
		if agentID == "" {
			continue
		}
		if existing, ok := byAgent[agentID]; ok && periodBriefPackHasHarvest(existing.Markdown) {
			continue
		}
		if !periodBriefPackHasHarvest(markdown) {
			continue
		}
		host, osFamily := periodBriefPackMachineIdentity(markdown)
		byAgent[agentID] = periodBriefSessionMaterial{
			AgentID:     agentID,
			Hostname:    host,
			OS:          osFamily,
			WindowLabel: latest.WindowLabel,
			Markdown:    markdown,
			SourceRunID: latest.ID,
		}
	}

	out := make([]periodBriefSessionMaterial, 0, len(byAgent))
	for _, id := range agentIDs {
		material, ok := byAgent[id]
		if !ok {
			continue
		}
		material.Label = nameByID[id]
		_, material.InLatestRun = latestCollectors[id]
		out = append(out, material)
	}
	return out
}

func (h *Handler) loadPeriodBriefSessionPacksFromChat(
	ctx context.Context,
	sessionID pgtype.UUID,
) map[string]string {
	out := make(map[string]string)
	rows, err := h.DB.Query(ctx, `
SELECT parts
FROM chat_message
WHERE chat_session_id = $1 AND role = 'assistant'
ORDER BY created_at ASC`, sessionID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if scanErr := rows.Scan(&raw); scanErr != nil {
			return out
		}
		for _, part := range messageparts.Decode(raw) {
			if part.Type != protocol.MessagePartTypeNoteBrief {
				continue
			}
			label := strings.TrimSpace(part.Label)
			if !strings.HasPrefix(label, periodBriefPackCardLabelPrefix) {
				continue
			}
			name := strings.TrimSpace(strings.TrimPrefix(label, periodBriefPackCardLabelPrefix))
			if name == "" || !periodBriefPackHasHarvest(part.Text) {
				continue
			}
			out[name] = strings.TrimSpace(part.Text)
		}
	}
	return out
}
