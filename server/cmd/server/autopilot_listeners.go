package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// registerAutopilotListeners hooks into issue and task events to keep
// autopilot runs in sync with their linked issues and tasks.
func registerAutopilotListeners(bus *events.Bus, svc *service.AutopilotService) {
	ctx := context.Background()

	// When an issue with origin_type='autopilot' reaches a terminal status,
	// update the corresponding autopilot run.
	bus.Subscribe(protocol.EventIssueUpdated, func(e events.Event) {
		payload, ok := e.Payload.(map[string]any)
		if !ok {
			return
		}
		statusChanged, _ := payload["status_changed"].(bool)
		if !statusChanged {
			return
		}
		issue, ok := payload["issue"].(handler.IssueResponse)
		if !ok {
			return
		}
		// Only handle statuses that finalize an autopilot run.
		if issue.Status != "done" && issue.Status != "in_review" && issue.Status != "cancelled" && issue.Status != "blocked" {
			return
		}
		// Load the full issue from DB to check origin_type.
		dbIssue, err := svc.Queries.GetIssue(ctx, parseUUID(issue.ID))
		if err != nil {
			slog.Debug("autopilot listener: failed to load issue", "issue_id", issue.ID, "error", err)
			return
		}
		svc.SyncRunFromIssue(ctx, dbIssue)
	})

	// When a task completes or fails, check if it's an autopilot run_only task.
	bus.Subscribe(protocol.EventTaskCompleted, func(e events.Event) {
		syncRunFromTaskEvent(ctx, svc, e)
	})
	bus.Subscribe(protocol.EventTaskFailed, func(e events.Event) {
		syncRunFromTaskEvent(ctx, svc, e)
	})
	bus.Subscribe(protocol.EventTaskCancelled, func(e events.Event) {
		syncRunFromTaskEvent(ctx, svc, e)
	})

	// When an autopilot run finishes, deliver the agent's report to the
	// creator's inbox. run_only dispatches a bare agent task and notifies
	// nobody, so without this the creator never receives what the agent
	// produced.
	bus.Subscribe(protocol.EventAutopilotRunDone, func(e events.Event) {
		notifyCreatorOnAutopilotRunDone(ctx, svc, bus, e)
	})
}

// notifyCreatorOnAutopilotRunDone delivers a completed autopilot run's report
// to the autopilot creator's inbox.
//
// run_only autopilots dispatch a bare agent task (no issue, no comment, no
// notification), so the report the agent produces lands only in the run's
// stored Result and reaches nobody. This closes that gap: on completion we read
// that Result and drop it in the creator's inbox. create_issue runs carry no
// task Result here (they finalize through their issue, which already surfaces),
// so an empty report short-circuits and they're left alone. Failures are not
// notified here — the failure-rate monitor already alerts on auto-pause.
func notifyCreatorOnAutopilotRunDone(ctx context.Context, svc *service.AutopilotService, bus *events.Bus, e events.Event) {
	payload, ok := e.Payload.(map[string]any)
	if !ok {
		return
	}
	if status, _ := payload["status"].(string); status != "completed" {
		return
	}
	runIDStr, _ := payload["run_id"].(string)
	if runIDStr == "" {
		return
	}
	runID, err := util.ParseUUID(runIDStr)
	if err != nil {
		return
	}

	queries := svc.Queries
	run, err := queries.GetAutopilotRun(ctx, runID)
	if err != nil {
		return
	}
	report := autopilotRunReport(run.Result)
	if report == "" {
		return // create_issue / no-output run — surfaced elsewhere
	}

	autopilot, err := queries.GetAutopilot(ctx, run.AutopilotID)
	if err != nil {
		return
	}
	recipients := resolveAutopilotPausedRecipients(ctx, queries, autopilot)
	if len(recipients) == 0 {
		return
	}

	details, _ := json.Marshal(map[string]any{
		"autopilot_id": util.UUIDToString(autopilot.ID),
		"run_id":       util.UUIDToString(run.ID),
		"source":       run.Source,
	})
	title := "Autopilot report: " + autopilot.Title
	body := truncateForInbox(report, 4000)
	workspaceID := util.UUIDToString(autopilot.WorkspaceID)

	for _, r := range recipients {
		item, err := queries.CreateInboxItem(ctx, db.CreateInboxItemParams{
			WorkspaceID:   autopilot.WorkspaceID,
			RecipientType: r.Type,
			RecipientID:   r.ID,
			Type:          "autopilot_run_completed",
			Severity:      "info",
			IssueID:       pgtype.UUID{},
			Title:         title,
			Body:          util.StrToText(body),
			ActorType:     util.StrToText("system"),
			ActorID:       pgtype.UUID{},
			Details:       details,
		})
		if err != nil {
			slog.Warn("autopilot run-done inbox write failed",
				"autopilot_id", util.UUIDToString(autopilot.ID),
				"recipient_id", util.UUIDToString(r.ID),
				"error", err,
			)
			continue
		}
		bus.Publish(events.Event{
			Type:        protocol.EventInboxNew,
			WorkspaceID: workspaceID,
			ActorType:   "system",
			ActorID:     "",
			Payload:     map[string]any{"item": inboxItemToResponse(item)},
		})
	}
}

// autopilotRunReport extracts the agent's final output text from a run's stored
// Result (a JSON-marshaled task-complete payload: {output, pr_url, ...}).
func autopilotRunReport(result []byte) string {
	if len(result) == 0 {
		return ""
	}
	var payload struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Output)
}

// truncateForInbox caps a body at maxRunes runes (not bytes, so multi-byte CJK
// reports aren't cut mid-character), appending an ellipsis when shortened.
func truncateForInbox(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "…"
}

func syncRunFromTaskEvent(ctx context.Context, svc *service.AutopilotService, e events.Event) {
	payload, ok := e.Payload.(map[string]any)
	if !ok {
		return
	}
	taskID, ok := payload["task_id"].(string)
	if !ok || taskID == "" {
		return
	}
	task, err := svc.Queries.GetAgentTask(ctx, parseUUID(taskID))
	if err != nil {
		return
	}
	if task.AutopilotRunID.Valid {
		svc.SyncRunFromTask(ctx, task)
		return
	}
	if e.Type == protocol.EventTaskFailed {
		svc.SyncRunFromLinkedIssueTask(ctx, task)
	}
}
