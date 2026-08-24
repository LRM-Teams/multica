package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

var notePeriodBriefPlannerMaxWait = 15 * time.Minute

func (h *Handler) dispatchNotePeriodBriefPlanner(
	w http.ResponseWriter,
	r *http.Request,
	workspaceID, userID pgtype.UUID,
	userIDString string,
	draft notePageRow,
	synthAgentID pgtype.UUID,
	windowLabel, windowStart, windowEnd, channelID, focus string,
	collectorIDs []string,
) (NoteWorkerJobResponse, bool) {
	agent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{
		ID:          synthAgentID,
		WorkspaceID: workspaceID,
	})
	if err != nil || agent.ArchivedAt.Valid {
		writeError(w, http.StatusNotFound, "agent not found")
		return NoteWorkerJobResponse{}, false
	}
	ch, ok := h.resolveNoteWorkerChannel(w, r, workspaceID, userIDString, agent, channelID)
	if !ok {
		return NoteWorkerJobResponse{}, false
	}

	draftPageID := uuidToString(draft.ID)
	instruction := notePeriodBriefPlannerInstruction(draftPageID)
	roster := formatNotePeriodBriefRoster(h.periodBriefRosterRows(r.Context(), workspaceID, collectorIDs))
	focusText := formatNotePeriodBriefFocusPartition(focus, "", notePeriodBriefCollectorScope{})

	jobID := uuid.New()
	jobUUID := parseUUID(jobID.String())
	if _, err := h.DB.Exec(r.Context(), `
INSERT INTO note_worker_job (id, workspace_id, page_id, creator_id, agent_id, instruction, status, channel_id)
VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7)`,
		jobUUID, workspaceID, draft.ID, userID, agent.ID, instruction, parseUUID(ch.ID)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create collect-plan Worker job")
		return NoteWorkerJobResponse{}, false
	}

	visibleContent, parts, err := h.buildNoteWorkerChannelMessage(r.Context(), ch, agent, draft, instruction)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return NoteWorkerJobResponse{}, false
	}
	authorName := h.channelAuthorName(r.Context(), userIDString)
	threadID := uuid.NewString()
	result, err := h.createUserChannelMessageWithIdempotency(r.Context(), channelMessageInsertInput{
		ChannelID:   parseUUID(ch.ID),
		WorkspaceID: workspaceID,
		AuthorID:    userID,
		AuthorName:  authorName,
		Content:     visibleContent,
		Parts:       parts,
		ThreadID:    &threadID,
	}, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to post collect-plan Worker message")
		return NoteWorkerJobResponse{}, false
	}
	msg := result.Message
	_, _ = h.DB.Exec(r.Context(), `UPDATE channel SET updated_at = now() WHERE id = $1`, parseUUID(ch.ID))
	if ch.Kind == "dm" {
		h.clearDMHiddenForChannelMembers(r.Context(), uuidToString(workspaceID), parseUUID(ch.ID))
	}
	recipientIDs := recipientUserIDsFromSet(h.channelHumanMemberIDs(r.Context(), uuidToString(workspaceID), ch.ID))
	h.publishToUsers(protocol.EventChannelMessage, uuidToString(workspaceID), "member", userIDString, recipientIDs, msg)

	workerPrompt := wrapNoteWorkerChannelWakePrompt(
		buildNotePeriodBriefPlannerPrompt(
			instruction, draftPageID, windowLabel, windowStart, windowEnd, draft.Title, "", roster, focusText,
		),
		h.agentMessageThreadTargetForPrompt(r.Context(), ch, msg),
	)
	task, err := h.enqueueChannelAgentPrompt(
		r.Context(), ch, agent, msg, userID, workerPrompt,
		"note worker", true, protocol.AgentInboxReasonNoteWorker, channelDirectedWakePriority,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to enqueue collect-plan Worker job: "+err.Error())
		return NoteWorkerJobResponse{}, false
	}
	mergedContext, err := service.WithNoteBrief(task.Context, service.NoteBrief{
		Version: 1,
		PageID:  draftPageID,
		Title:   draft.Title,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to attach collect-plan note brief")
		return NoteWorkerJobResponse{}, false
	}
	if err := h.persistPeriodBriefNoteBriefContext(r.Context(), task.ID, mergedContext); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to persist collect-plan note brief")
		return NoteWorkerJobResponse{}, false
	}
	if _, err := h.DB.Exec(r.Context(), `
UPDATE note_worker_job
SET task_id = $1, channel_message_id = $2, status = 'dispatched', updated_at = now()
WHERE id = $3`, task.ID, parseUUID(msg.ID), jobUUID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to link collect-plan Worker task")
		return NoteWorkerJobResponse{}, false
	}
	resp, err := h.noteWorkerJobResponse(r.Context(), workspaceID, userID, jobUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load collect-plan Worker job")
		return NoteWorkerJobResponse{}, false
	}
	return resp, true
}

func (h *Handler) periodBriefRosterRows(ctx context.Context, workspaceID pgtype.UUID, collectorIDs []string) []notePeriodBriefRosterRow {
	out := make([]notePeriodBriefRosterRow, 0, len(collectorIDs))
	for _, id := range collectorIDs {
		agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
			ID:          parseUUID(id),
			WorkspaceID: workspaceID,
		})
		if err != nil {
			out = append(out, notePeriodBriefRosterRow{ID: id, Name: id})
			continue
		}
		out = append(out, notePeriodBriefRosterRow{
			ID:          id,
			Name:        agent.Name,
			DisplayName: agent.DisplayName,
			RuntimeMode: agent.RuntimeMode,
		})
	}
	return out
}

func (h *Handler) awaitPeriodBriefCollectPlan(
	ctx context.Context,
	workspaceID, draftPageID pgtype.UUID,
) *notePeriodBriefCollectPlan {
	deadline := time.Now().Add(notePeriodBriefPlannerMaxWait)
	for {
		run, err := h.loadNotePeriodBriefRunByDraft(ctx, workspaceID, draftPageID)
		if err == nil && run.CollectPlan != nil && len(run.CollectPlan.Assignments) > 0 {
			return run.CollectPlan
		}
		if err == nil && run.PlannerJobID.Valid {
			projected, _ := h.noteWorkerJobResponse(ctx, workspaceID, run.OwnerUserID, run.PlannerJobID)
			switch projected.Status {
			case "failed", "cancelled", "completed":
				if run.CollectPlan == nil || len(run.CollectPlan.Assignments) == 0 {
					return nil
				}
				return run.CollectPlan
			}
		}
		if !time.Now().Before(deadline) {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(notePeriodBriefCollectorPollEvery):
		}
	}
}
