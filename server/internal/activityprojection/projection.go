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
// surfaces must never receive provider detail merely because no summary
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
	"starting":               "Starting…",
	"message_received":       "Message received",
	"model_response_started": "Working...",
	"compacting_context":     "Compacting context...",
	"compaction_finished":    "Compaction finished",
	"compaction_stale":       "Compaction still running",

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

var toolStartDetailKinds = map[string]string{
	"send_message": "sending_message", "check_messages": "checking_messages", "receive_message": "checking_messages",
	"wait_for_message": "waiting_for_message", "read_history": "reading_history", "search_messages": "searching_messages",
	"list_server": "listing_server", "list_tasks": "listing_tasks", "create_tasks": "creating_tasks", "claim_tasks": "claiming_task",
	"unclaim_task": "unclaiming_task", "update_task_status": "updating_task_status", "add_channel_member": "adding_channel_member",
	"join_channel": "joining_channel", "leave_channel": "leaving_channel", "upload_file": "uploading_file", "view_file": "viewing_file",
	"read_file": "reading_file", "write_file": "writing_file", "edit_file": "editing_file", "bash": "running_command",
	"glob": "searching_files", "grep": "searching_code", "web_fetch": "fetching_url", "web_search": "searching_web",
	"todo_write": "updating_tasks", "schedule_reminder": "scheduling_reminder", "list_reminders": "listing_reminders",
	"cancel_reminder": "canceling_reminder", "snooze_reminder": "snoozing_reminder", "update_reminder": "updating_reminder",
	"log_reminder": "logging_reminder", "collab_tool_call": "collaborating",
	"list_issues": "listing_issues", "get_issue": "getting_issue", "search_issues": "searching_issues",
	"list_issue_comments": "listing_issue_comments", "comment_issue": "commenting_issue", "delete_issue_comment": "deleting_issue_comment",
}

// ActivityKindFromDetailKind is the one server-owned reduction from Raft's
// execution fact vocabulary to Multica's compact lifecycle vocabulary. The
// daemon may track an ActivityKind locally for heartbeat scheduling, but that
// presentation state never crosses the WorkspaceDaemon wire.
func ActivityKindFromDetailKind(detailKind string) string {
	switch detailKind {
	case "idle", "ready":
		return protocol.ActivityKindOnline
	case "thinking_started":
		return protocol.ActivityKindThinking
	case "runtime_error", "runtime_crashed", "runtime_stalled", "computer_operation_failed":
		return protocol.ActivityKindError
	case "runtime_unavailable", "runtime_interrupted", "machine_disconnected", "computer_restarted", "computer_upgraded", "stopped":
		return protocol.ActivityKindOffline
	default:
		return protocol.ActivityKindWorking
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
		// Lifecycle reasons remain Timeline/diagnostic facts. Compact Agent
		// Activity uses the same Offline word for stop, crash, disconnect, and
		// every future offline detail.
		return Summary{Label: "Offline", Tone: "neutral", Visibility: "visible"}
	case protocol.ActivityKindWorking:
		if label, ok := workingDetailLabels[snapshot.DetailKind]; ok {
			tone := "warning"
			if snapshot.DetailKind == "running_command" {
				tone = "running"
			}
			if snapshot.DetailKind == "model_response_started" {
				tone = "info"
			}
			return Summary{Label: label, Tone: tone, Visibility: "visible"}
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
	if entry.Kind == "tool_start" {
		var body protocol.AgentActivityToolStartBody
		if json.Unmarshal(entry.Body, &body) == nil {
			detailKind := toolStartDetailKinds[body.ToolName]
			if detailKind == "" {
				detailKind = "tool_started"
			}
			toolSummary := Summary{Label: "Working...", Tone: "warning", Visibility: "visible"}
			if label, ok := workingDetailLabels[detailKind]; ok {
				toolSummary.Label = label
			}
			if detailKind == "running_command" {
				toolSummary.Tone = "running"
			}
			bodyKind := "none"
			if body.ToolName == "bash" {
				bodyKind = "command"
			}
			row = TimelineRow{Title: strings.TrimSuffix(toolSummary.Label, "..."), Tone: toolSummary.Tone, BodyKind: bodyKind}
			if body.ToolInput != "" {
				row.Subtext = boundedText(body.ToolInput)
			}
			return row
		}
	}
	if entry.Kind == "status" {
		var body protocol.AgentActivityStatusBody
		if json.Unmarshal(entry.Body, &body) == nil {
			return projectStatusTimelineRow(body, summary)
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

func projectStatusTimelineRow(body protocol.AgentActivityStatusBody, fallback Summary) TimelineRow {
	text := boundedText(body.Detail)
	if strings.TrimSpace(body.DetailKind) == "" {
		return TimelineRow{Title: fallback.Label, Subtext: text, Tone: fallback.Tone, BodyKind: "none"}
	}

	activityKind := ActivityKindFromDetailKind(body.DetailKind)
	summary := ProjectSummary(protocol.AgentActivitySnapshot{
		ActivityKind: activityKind,
		DetailKind:   body.DetailKind,
	})
	row := TimelineRow{Title: strings.TrimSuffix(summary.Label, "..."), Tone: summary.Tone, BodyKind: "none"}
	switch activityKind {
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
		case "running_command":
			row.BodyKind = "command"
			if text != "" && text != row.Title {
				row.Subtext = text
			}
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
		if body.DetailKind == "stopped" {
			row.Title = "Stopped"
			row.Subtext = "Agent stopped by user"
		} else if text != "" && text != row.Title {
			row.Subtext = text
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
