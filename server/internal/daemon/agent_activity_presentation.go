package daemon

import (
	"encoding/json"
	"strings"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

const maxTimelineBodyBytes = 4000

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

// activityKindFromDetailKind reduces the daemon execution vocabulary to the
// compact Activity vocabulary sent to clients.
func activityKindFromDetailKind(detailKind string) string {
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

// projectActivitySummary deliberately never reads command text, paths, tool input,
// prompts, stderr, or Entry bodies. Those facts cannot reach compact surfaces.
func projectActivitySummary(snapshot protocol.AgentActivitySnapshot) protocol.AgentActivitySummary {
	summary := protocol.AgentActivitySummary{ActivityKind: snapshot.ActivityKind, DetailKind: snapshot.DetailKind}
	switch snapshot.ActivityKind {
	case protocol.ActivityKindOnline:
		summary.Label = "Online"
	case protocol.ActivityKindThinking:
		summary.Label = "Thinking..."
	case protocol.ActivityKindError:
		summary.Label = "Error"
	case protocol.ActivityKindOffline:
		// Lifecycle reasons remain Timeline/diagnostic facts. Compact Agent
		// Activity uses the same Offline word for stop, crash, disconnect, and
		// every future offline detail.
		summary.Label = "Offline"
	case protocol.ActivityKindWorking:
		if label, ok := workingDetailLabels[snapshot.DetailKind]; ok {
			summary.Label = label
			return summary
		}
		summary.Label = "Working..."
	default:
		summary.Label = "Working..."
	}
	return summary
}

// projectActivityTimelineEntry preserves a bounded, safe user-facing string from a
// known generic body. Unknown or malformed envelopes get a useful generic row
// without exposing raw provider data.
func projectActivityTimelineEntry(entry protocol.AgentActivityEntry, summary protocol.AgentActivitySummary) protocol.AgentActivityTimelineRow {
	row := protocol.AgentActivityTimelineRow{ActivityKind: summary.ActivityKind, DetailKind: summary.DetailKind, Title: summary.Label, BodyKind: "generic"}
	if entry.Kind == "tool_start" {
		var body protocol.AgentActivityToolStartBody
		if json.Unmarshal(entry.Body, &body) == nil {
			detailKind := toolStartDetailKinds[body.ToolName]
			if detailKind == "" {
				detailKind = "tool_started"
			}
			toolSummary := protocol.AgentActivitySummary{ActivityKind: protocol.ActivityKindWorking, DetailKind: detailKind, Label: "Working..."}
			if label, ok := workingDetailLabels[detailKind]; ok {
				toolSummary.Label = label
			}
			bodyKind := "none"
			if body.ToolName == "bash" {
				bodyKind = "command"
			}
			row = protocol.AgentActivityTimelineRow{ActivityKind: toolSummary.ActivityKind, DetailKind: detailKind, Title: strings.TrimSuffix(toolSummary.Label, "..."), BodyKind: bodyKind}
			if body.ToolInput != "" {
				row.Subtext = boundedText(body.ToolInput)
			}
			return row
		}
	}
	if entry.Kind == "status" {
		var body protocol.AgentActivityStatusBody
		if json.Unmarshal(entry.Body, &body) == nil {
			return projectActivityStatusTimelineRow(body, summary)
		}
	}
	if entry.Kind == "system" {
		var body protocol.AgentActivitySystemBody
		if json.Unmarshal(entry.Body, &body) == nil {
			title := boundedText(body.Title)
			if title == "" {
				title = "Runtime warning"
			}
			text := boundedText(body.Text)
			if source := boundedText(body.Source); source != "" {
				text = "Provider: " + source + "\n" + text
			}
			if reference := boundedText(body.Reference); reference != "" {
				text += "\nRun: " + reference
			}
			return protocol.AgentActivityTimelineRow{ActivityKind: protocol.ActivityKindError, DetailKind: "runtime_diagnostic", Title: title, Subtext: boundedText(text), BodyKind: "none"}
		}
	}
	return row
}

func projectActivityStatusTimelineRow(body protocol.AgentActivityStatusBody, fallback protocol.AgentActivitySummary) protocol.AgentActivityTimelineRow {
	text := boundedText(body.Detail)
	if strings.TrimSpace(body.DetailKind) == "" {
		return protocol.AgentActivityTimelineRow{ActivityKind: fallback.ActivityKind, DetailKind: fallback.DetailKind, Title: fallback.Label, Subtext: text, BodyKind: "none"}
	}

	activityKind := activityKindFromDetailKind(body.DetailKind)
	summary := projectActivitySummary(protocol.AgentActivitySnapshot{
		ActivityKind: activityKind,
		DetailKind:   body.DetailKind,
	})
	row := protocol.AgentActivityTimelineRow{ActivityKind: activityKind, DetailKind: body.DetailKind, Title: strings.TrimSuffix(summary.Label, "..."), BodyKind: "none"}
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
