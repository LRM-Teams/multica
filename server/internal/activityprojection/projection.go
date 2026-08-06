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

// ProjectSummary deliberately never reads command text, paths, tool input,
// prompts, stderr, or Entry bodies. Those facts cannot reach compact surfaces.
func ProjectSummary(snapshot protocol.AgentActivitySnapshot) Summary {
	switch snapshot.ActivityKind {
	case protocol.ActivityKindOnline:
		return Summary{Label: "Online", Tone: "neutral", Visibility: "visible"}
	case protocol.ActivityKindThinking:
		return Summary{Label: "Thinking...", Tone: "info", Visibility: "visible"}
	case protocol.ActivityKindError:
		return Summary{Label: "Runtime error", Tone: "danger", Visibility: "visible"}
	case protocol.ActivityKindOffline:
		if snapshot.DetailKind == "machine_disconnected" {
			return Summary{Label: "Disconnected", Tone: "muted", Visibility: "visible"}
		}
		return Summary{Label: "Offline", Tone: "muted", Visibility: "visible"}
	case protocol.ActivityKindWorking:
		switch snapshot.DetailKind {
		case "message_received":
			return Summary{Label: "Message received", Tone: "info", Visibility: "visible"}
		case "running_command":
			return Summary{Label: "Running command...", Tone: "info", Visibility: "visible"}
		case "checking_messages":
			return Summary{Label: "Checking messages...", Tone: "info", Visibility: "visible"}
		case "compacting_context":
			return Summary{Label: "Compacting context...", Tone: "info", Visibility: "visible"}
		default:
			return Summary{Label: "Working...", Tone: "info", Visibility: "visible"}
		}
	default:
		return Summary{Label: "Working...", Tone: "info", Visibility: "visible"}
	}
}

// ProjectTimelineEntry preserves a bounded, safe user-facing string from a
// known generic body. Unknown or malformed envelopes get a useful generic row
// without exposing raw provider data.
func ProjectTimelineEntry(entry protocol.AgentActivityEntry, summary Summary) TimelineRow {
	row := TimelineRow{Title: summary.Label, Tone: summary.Tone, BodyKind: "generic"}
	if entry.Kind == "narrative" {
		var body struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(entry.Body, &body) == nil {
			row.Body = boundedText(body.Text)
			if row.Body != "" {
				row.BodyKind = "text"
			}
		}
	}
	return row
}

func boundedText(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > maxTimelineBodyBytes {
		return value[:maxTimelineBodyBytes]
	}
	return value
}
