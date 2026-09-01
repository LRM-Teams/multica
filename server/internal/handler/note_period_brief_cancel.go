package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type cancelNotePeriodBriefResponse struct {
	Run notePeriodBriefActiveResponse `json:"run"`
}

// CancelNotePeriodBrief stops an in-flight 写汇报 run from the Notes bubble
// stop button. It marks the run cancelled, suppresses collector / planner /
// synthesizer Worker inbox wakes, and unlocks the composer.
// POST /api/notes/period-briefs/{runId}/cancel
func (h *Handler) CancelNotePeriodBrief(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, userIDString, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	runID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "runId"), "run id")
	if !ok {
		return
	}
	run, err := h.loadNotePeriodBriefRunByID(r.Context(), workspaceID, userID, runID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "period brief run not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load period brief run")
		return
	}
	if run.Status == "cancelled" {
		writeJSON(w, http.StatusOK, cancelNotePeriodBriefResponse{Run: periodBriefActiveFromRun(run)})
		return
	}
	if !periodBriefRunLocksComposerStatus(run.Status) {
		writeError(w, http.StatusConflict, "period brief run is not running")
		return
	}

	h.cancelPeriodBriefWorkerJobs(r, workspaceID, userIDString, run)
	if err := h.markNotePeriodBriefRunCancelled(r.Context(), run.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel period brief run")
		return
	}
	run.Status = "cancelled"
	h.postPeriodBriefBubbleProgress(r.Context(), run, userIDString, "好，已停止这次写汇报。")
	writeJSON(w, http.StatusOK, cancelNotePeriodBriefResponse{Run: periodBriefActiveFromRun(run)})
}

func periodBriefActiveFromRun(run notePeriodBriefRunRow) notePeriodBriefActiveResponse {
	return notePeriodBriefActiveResponse{
		ID:            uuidToString(run.ID),
		Status:        run.Status,
		ChatSessionID: uuidToString(run.ChatSessionID),
		SourcePageID:  uuidToString(run.SourcePageID),
		DraftPageID:   uuidToString(run.DraftPageID),
	}
}

func (h *Handler) markNotePeriodBriefRunCancelled(ctx context.Context, runID pgtype.UUID) error {
	_, err := h.DB.Exec(ctx, `
UPDATE note_period_brief_run
SET status = 'cancelled', updated_at = now()
WHERE id = $1 AND status IN ('planning', 'collecting', 'synthesizing')`, runID)
	return err
}

func (h *Handler) tryAdvancePeriodBriefRunStatus(ctx context.Context, runID pgtype.UUID, status string) (bool, error) {
	if strings.TrimSpace(status) == "" {
		return false, nil
	}
	tag, err := h.DB.Exec(ctx, `
UPDATE note_period_brief_run
SET status = $1, updated_at = now()
WHERE id = $2 AND status IN ('planning', 'collecting', 'synthesizing')`, status, runID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (h *Handler) cancelPeriodBriefWorkerJobs(r *http.Request, workspaceID pgtype.UUID, userIDString string, run notePeriodBriefRunRow) {
	ctx := r.Context()
	rows, err := h.DB.Query(ctx, `
SELECT id, task_id
FROM note_worker_job
WHERE workspace_id = $1 AND page_id = $2
  AND status IN ('pending', 'dispatched', 'running')`, workspaceID, run.DraftPageID)
	if err != nil {
		return
	}
	defer rows.Close()

	type jobRef struct {
		id     pgtype.UUID
		taskID pgtype.UUID
	}
	seen := map[string]bool{}
	jobs := make([]jobRef, 0, 8)
	for rows.Next() {
		var job jobRef
		if err := rows.Scan(&job.id, &job.taskID); err != nil {
			continue
		}
		jobs = append(jobs, job)
		seen[uuidToString(job.id)] = true
	}
	for _, extra := range extraPeriodBriefJobIDs(run) {
		if seen[uuidToString(extra)] {
			continue
		}
		var taskID pgtype.UUID
		var status string
		if err := h.DB.QueryRow(ctx, `
SELECT task_id, status FROM note_worker_job
WHERE id = $1 AND workspace_id = $2`, extra, workspaceID).Scan(&taskID, &status); err != nil {
			continue
		}
		if status != "pending" && status != "dispatched" && status != "running" {
			continue
		}
		jobs = append(jobs, jobRef{id: extra, taskID: taskID})
	}

	workspaceIDString := uuidToString(workspaceID)
	actorType, actorID := h.resolveActor(r, userIDString, workspaceIDString)
	for _, job := range jobs {
		if job.taskID.Valid {
			h.cancelPeriodBriefInboxEvent(ctx, workspaceID, workspaceIDString, actorType, actorID, job.taskID)
		}
		_, _ = h.DB.Exec(ctx, `
UPDATE note_worker_job
SET status = 'cancelled', updated_at = now()
WHERE id = $1 AND status IN ('pending', 'dispatched', 'running')`, job.id)
	}
}

func extraPeriodBriefJobIDs(run notePeriodBriefRunRow) []pgtype.UUID {
	ids := make([]pgtype.UUID, 0, len(run.Collectors)+1)
	if run.PlannerJobID.Valid {
		ids = append(ids, run.PlannerJobID)
	}
	for _, ref := range run.Collectors {
		if id, ok := parseUUIDQuiet(ref.JobID); ok {
			ids = append(ids, id)
		}
		if id, ok := parseUUIDQuiet(ref.PackJobID); ok {
			ids = append(ids, id)
		}
	}
	return ids
}

func (h *Handler) cancelPeriodBriefInboxEvent(
	ctx context.Context,
	workspaceID pgtype.UUID,
	workspaceIDString, actorType, actorID string,
	taskID pgtype.UUID,
) {
	if h.TaskService != nil {
		if _, err := h.TaskService.CancelTaskWithResult(ctx, taskID); err == nil {
			return
		}
	}
	row, err := h.cancelAgentInboxEventCore(ctx, workspaceID, taskID)
	if err != nil {
		return
	}
	payload := h.cancelledInboxEventTaskResponse(row, workspaceIDString)
	h.publishCancelledAgentInboxEvent(workspaceIDString, actorType, actorID, row, payload)
}
