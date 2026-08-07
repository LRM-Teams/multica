// Package activityprojection contains the server-owned semantic projection
// from Runner Activity facts to UI-ready summaries and timeline rows.
package activityprojection

import (
	"encoding/json"
	"strings"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

const maxTimelineBodyBytes = 4000

type Summary struct {
	Label      string `json:"label"`
	Tone       string `json:"tone"`
	Visibility string `json:"visibility"`
}

type TimelineRow struct {
	Title    string `json:"title"`
	Subtext  string `json:"subtext,omitempty"`
	Tone     string `json:"tone"`
	BodyKind string `json:"body_kind"`
	Body     string `json:"body,omitempty"`
}

// TimelineRowFromSnapshot is the server-owned presentation for the current
// replaceable observation. It deliberately has no body: compact and header
// surfaces must never receive provider detail merely because no narrative
// Entry accompanied the observation.
func TimelineRowFromSnapshot(snapshot protocol.AgentActivitySnapshot) TimelineRow {
	summary := ProjectSummary(snapshot)
	return TimelineRow{
		Title:    summary.Label,
		Tone:     summary.Tone,
		BodyKind: "none",
	}
}

// ProjectSummary deliberately never reads command text, paths, tool input,
// prompts, stderr, or Entry bodies. Those facts cannot reach compact surfaces.
func ProjectSummary(snapshot protocol.AgentActivitySnapshot) Summary {
	switch snapshot.ActivityKind {
	case protocol.ActivityKindOnline:
		return Summary{Label: "Online", Tone: "success", Visibility: "visible"}
	case protocol.ActivityKindThinking:
		return Summary{Label: "Thinking...", Tone: "info", Visibility: "visible"}
	case protocol.ActivityKindError:
		return Summary{Label: "Error", Tone: "error", Visibility: "visible"}
	case protocol.ActivityKindOffline:
		if snapshot.DetailKind == "stopped" {
			return Summary{Label: "Stopped", Tone: "neutral", Visibility: "visible"}
		}
		// A disconnected Computer makes its Agents offline. "Disconnected" is
		// Computer connectivity vocabulary and must not leak into Agent state.
		return Summary{Label: "Offline", Tone: "neutral", Visibility: "visible"}
	case protocol.ActivityKindWorking:
		switch snapshot.DetailKind {
		case "starting":
			return Summary{Label: "Starting", Tone: "warning", Visibility: "visible"}
		case "message_received":
			return Summary{Label: "Message received", Tone: "warning", Visibility: "visible"}
		case "running_command":
			return Summary{Label: "Running command...", Tone: "warning", Visibility: "visible"}
		case "checking_messages":
			return Summary{Label: "Checking messages...", Tone: "warning", Visibility: "visible"}
		case "compacting_context":
			return Summary{Label: "Compacting context...", Tone: "warning", Visibility: "visible"}
		default:
			return Summary{Label: "Working...", Tone: "warning", Visibility: "visible"}
		}
	default:
		return Summary{Label: "Working...", Tone: "warning", Visibility: "visible"}
	}
}

// ProjectTimelineEntry preserves a bounded, safe user-facing string from a
// known generic body. Unknown or malformed envelopes get a useful generic row
// without exposing raw provider data.
func ProjectTimelineEntry(entry protocol.AgentActivityEntry, summary Summary) TimelineRow {
	row := TimelineRow{Title: summary.Label, Tone: summary.Tone, BodyKind: "generic"}
	if entry.Kind == "narrative" {
		var body protocol.AgentActivityNarrativeBody
		if json.Unmarshal(entry.Body, &body) == nil {
			return projectNarrativeTimelineRow(body, summary)
		}
	}
	if entry.Kind == "system" {
		var body protocol.AgentActivitySystemBody
		if json.Unmarshal(entry.Body, &body) == nil {
			title := boundedText(body.Title)
			if title == "" {
				title = "Runtime warning"
			}
			return TimelineRow{Title: title, Subtext: boundedText(body.Text), Tone: "warning", BodyKind: "none"}
		}
	}
	return row
}

func projectNarrativeTimelineRow(body protocol.AgentActivityNarrativeBody, fallback Summary) TimelineRow {
	text := boundedText(body.Text)
	if !knownActivityKind(body.ActivityKind) {
		return TimelineRow{Title: fallback.Label, Subtext: text, Tone: fallback.Tone, BodyKind: "none"}
	}

	summary := ProjectSummary(protocol.AgentActivitySnapshot{
		ActivityKind: body.ActivityKind,
		DetailKind:   body.DetailKind,
	})
	row := TimelineRow{Title: strings.TrimSuffix(summary.Label, "..."), Tone: summary.Tone, BodyKind: "none"}
	switch body.ActivityKind {
	case protocol.ActivityKindError:
		row.Title = "Error"
		row.Subtext = text
	case protocol.ActivityKindThinking:
		row.Title = "Thinking"
		if text != "" && text != "Thinking" {
			row.Subtext = text
		}
	case protocol.ActivityKindOnline:
		if body.DetailKind == "idle" {
			row.Title = "Idle"
		}
		row.Subtext = text
	case protocol.ActivityKindWorking:
		switch body.DetailKind {
		case "message_received":
			row.Title = "Working"
			row.Subtext = text
		default:
			if text != "" && text != row.Title {
				row.Subtext = text
			}
		}
	case protocol.ActivityKindOffline:
		if text != "" && text != row.Title {
			row.Subtext = text
		}
	}
	return row
}

func knownActivityKind(kind string) bool {
	switch kind {
	case protocol.ActivityKindOnline, protocol.ActivityKindThinking, protocol.ActivityKindWorking, protocol.ActivityKindError, protocol.ActivityKindOffline:
		return true
	default:
		return false
	}
}

func boundedText(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > maxTimelineBodyBytes {
		return value[:maxTimelineBodyBytes]
	}
	return value
}
