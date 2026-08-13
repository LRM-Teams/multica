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

// workingDetailLabels owns the Raft-aligned activity label for every working
// detail kind the daemon can emit (see daemon toolDetailKind). Tool actions
// carry the in-progress "..." suffix; lifecycle events/states (Starting,
// Message received) do not. Labels only — a detail kind never carries command
// text, paths, or tool input.
var workingDetailLabels = map[string]string{
	"starting":            "Starting…",
	"message_received":    "Message received",
	"compacting_context":  "Compacting context...",
	"compaction_finished": "Compaction finished",
	"compaction_stale":    "Compaction still running",

	"running_command": "Running command...",
	"reading_file":    "Reading file...",
	"writing_file":    "Writing file...",
	"editing_file":    "Editing file...",
	"searching_files": "Searching files...",
	"searching_code":  "Searching code...",
	"fetching_url":    "Fetching web...",
	"searching_web":   "Searching web...",
	"updating_tasks":  "Updating tasks...",

	"sending_message":       "Sending message...",
	"checking_messages":     "Checking messages...",
	"waiting_for_message":   "Waiting for messages...",
	"reading_history":       "Reading history...",
	"searching_messages":    "Searching messages...",
	"listing_server":        "Listing server...",
	"listing_tasks":         "Listing tasks...",
	"creating_tasks":        "Creating tasks...",
	"claiming_task":         "Claiming tasks...",
	"unclaiming_task":       "Unclaiming task...",
	"updating_task_status":  "Updating task status...",
	"adding_channel_member": "Adding channel member...",
	"joining_channel":       "Joining channel...",
	"leaving_channel":       "Leaving channel...",
	"uploading_file":        "Uploading file...",
	"viewing_file":          "Viewing file...",

	"listing_issues":         "Listing issues...",
	"getting_issue":          "Checking issue...",
	"searching_issues":       "Searching issues...",
	"listing_issue_comments": "Checking issue comments...",
	"commenting_issue":       "Commenting on issue...",
	"deleting_issue_comment": "Deleting issue comment...",

	"scheduling_reminder": "Scheduling reminder...",
	"listing_reminders":   "Listing reminders...",
	"canceling_reminder":  "Canceling reminder...",
	"snoozing_reminder":   "Snoozing reminder...",
	"updating_reminder":   "Updating reminder...",
	"logging_reminder":    "Logging reminder...",

	"collaborating": "Collaborating...",
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
		// Lifecycle reasons remain Timeline/diagnostic facts. Compact Agent
		// Activity uses the same Offline word for stop, crash, disconnect, and
		// every future offline detail.
		return Summary{Label: "Offline", Tone: "neutral", Visibility: "visible"}
	case protocol.ActivityKindWorking:
		if label, ok := workingDetailLabels[snapshot.DetailKind]; ok {
			return Summary{Label: label, Tone: "warning", Visibility: "visible"}
		}
		return Summary{Label: "Working...", Tone: "warning", Visibility: "visible"}
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
		case "message_received", "compacting_context", "compaction_finished":
			// Round lifecycle facts share the same timeline grammar: Working is
			// the stable action heading and the event-specific fact is subtext.
			// This keeps compaction aligned with Message received instead of
			// promoting each lifecycle detail into a competing top-level state.
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
