package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// noteWorkerJobCreateRequest is the Worker contract create body (S2-C3).
// It must not accept Editor edit fields (prompt / action / replace_page).
type noteWorkerJobCreateRequest struct {
	AgentID     string `json:"agent_id"`
	Instruction string `json:"instruction"`
	Intent      string `json:"intent"`
	// Editor-only fields — presence is misuse and must 400.
	Prompt string `json:"prompt"`
	Action string `json:"action"`
}

// NoteWorkerJobResponse is the durable Worker job record. After S2-C1 dispatch,
// status is dispatched (or later running/completed) and task_id is set.
type NoteWorkerJobResponse struct {
	ID            string  `json:"id"`
	WorkspaceID   string  `json:"workspace_id"`
	PageID        string  `json:"page_id"`
	AgentID       string  `json:"agent_id"`
	Instruction   string  `json:"instruction"`
	Status        string  `json:"status"`
	Intent        string  `json:"intent"`
	TaskID        *string `json:"task_id,omitempty"`
	FailureReason *string `json:"failure_reason,omitempty"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
}

func (h *Handler) noteWorkerJobResponse(ctx context.Context, workspaceID, userID, jobID pgtype.UUID) (NoteWorkerJobResponse, error) {
	var (
		resp                NoteWorkerJobResponse
		id, wsID, pageID    pgtype.UUID
		agentID, taskID     pgtype.UUID
		instruction, status string
		failure             *string
		createdAt           pgtype.Timestamptz
		updatedAt           pgtype.Timestamptz
	)
	err := h.DB.QueryRow(ctx, `
SELECT j.id, j.workspace_id, j.page_id, j.agent_id, j.instruction, j.status, j.task_id, j.failure_reason, j.created_at, j.updated_at
FROM note_worker_job j
WHERE j.id = $1 AND j.workspace_id = $2 AND j.creator_id = $3`, jobID, workspaceID, userID).Scan(
		&id, &wsID, &pageID, &agentID, &instruction, &status, &taskID, &failure, &createdAt, &updatedAt,
	)
	if err != nil {
		return resp, err
	}
	resp = NoteWorkerJobResponse{
		ID:          uuidToString(id),
		WorkspaceID: uuidToString(wsID),
		PageID:      uuidToString(pageID),
		AgentID:     uuidToString(agentID),
		Instruction: instruction,
		Status:      status,
		Intent:      NoteIntentWorker,
		CreatedAt:   timestampToString(createdAt),
		UpdatedAt:   timestampToString(updatedAt),
	}
	if failure != nil && strings.TrimSpace(*failure) != "" {
		resp.FailureReason = failure
	}
	if taskID.Valid {
		s := uuidToString(taskID)
		resp.TaskID = &s
	}
	return resp, nil
}

// CreateNoteWorkerJob records a Worker job and dispatches a chat task with the
// note mounted as an untrusted brief (S2-C1). It must not create note_ai_job
// rows or apply Editor replace_page actions.
func (h *Handler) CreateNoteWorkerJob(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, userIDString, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	page, _, ok := h.loadAccessibleNote(w, r, chi.URLParam(r, "id"), workspaceID, userID)
	if !ok {
		return
	}
	var req noteWorkerJobCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if reason := workerCreateMisuseReason(strings.TrimSpace(req.Intent), strings.TrimSpace(req.Prompt), strings.TrimSpace(req.Action)); reason != "" {
		writeError(w, http.StatusBadRequest, reason)
		return
	}
	instruction := strings.TrimSpace(req.Instruction)
	if instruction == "" {
		writeError(w, http.StatusBadRequest, errNoteWorkerInstruction)
		return
	}
	agentID, ok := parseUUIDOrBadRequest(w, req.AgentID, "agent_id")
	if !ok {
		return
	}
	agent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{ID: agentID, WorkspaceID: page.WorkspaceID})
	if err != nil || agent.ArchivedAt.Valid {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}
	if h.TaskService == nil {
		writeError(w, http.StatusInternalServerError, "task service unavailable")
		return
	}

	jobID := uuid.New()
	jobUUID := parseUUID(jobID.String())
	if _, err := h.DB.Exec(r.Context(), `
INSERT INTO note_worker_job (id, workspace_id, page_id, creator_id, agent_id, instruction, status)
VALUES ($1, $2, $3, $4, $5, $6, 'pending')`,
		jobUUID, page.WorkspaceID, page.ID, userID, agentID, instruction); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create note Worker job")
		return
	}

	session, err := h.Queries.CreateChatSession(r.Context(), db.CreateChatSessionParams{
		WorkspaceID: page.WorkspaceID,
		AgentID:     agentID,
		CreatorID:   userID,
		Title:       normalizeNoteWorkerJobTitle(page.Title),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create note Worker job")
		return
	}
	prompt := buildNoteWorkerPrompt(instruction, uuidToString(page.ID), page.Title, page.Content)
	msg, err := h.Queries.CreateChatMessage(r.Context(), db.CreateChatMessageParams{
		ChatSessionID: session.ID,
		Role:          "user",
		Content:       prompt,
		Parts:         []byte("[]"),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create note Worker job")
		return
	}
	task, err := h.TaskService.EnqueueChatTask(r.Context(), session, parseUUID(userIDString))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to enqueue note Worker job: "+err.Error())
		return
	}
	if err := h.Queries.LinkChatMessageToTask(r.Context(), db.LinkChatMessageToTaskParams{ID: msg.ID, TaskID: task.ID}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to link note Worker job")
		return
	}

	mergedContext, err := service.WithNoteBrief(task.Context, service.NoteBrief{
		Version: 1,
		PageID:  uuidToString(page.ID),
		Title:   page.Title,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to attach note brief")
		return
	}
	if _, err := h.DB.Exec(r.Context(), `
UPDATE agent_inbox_event SET context = $1::jsonb WHERE id = $2`, mergedContext, task.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to persist note brief")
		return
	}
	if _, err := h.DB.Exec(r.Context(), `
UPDATE note_worker_job
SET task_id = $1, status = 'dispatched', updated_at = now()
WHERE id = $2`, task.ID, jobUUID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to link note Worker task")
		return
	}
	_, _ = h.Queries.UpdateChatSessionStatus(r.Context(), db.UpdateChatSessionStatusParams{ID: session.ID, Status: "archived"})

	resp, err := h.noteWorkerJobResponse(r.Context(), page.WorkspaceID, userID, jobUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load note Worker job")
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func normalizeNoteWorkerJobTitle(noteTitle string) string {
	title := strings.TrimSpace(noteTitle)
	if title == "" {
		title = "Untitled"
	}
	return "Note Worker: " + title
}

// GetNoteWorkerJob returns a Worker job owned by the caller in this workspace.
func (h *Handler) GetNoteWorkerJob(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, _, ok := h.notesWorkspaceAndUser(w, r)
	if !ok {
		return
	}
	jobID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "jobId"), "note Worker job id")
	if !ok {
		return
	}
	resp, err := h.noteWorkerJobResponse(r.Context(), workspaceID, userID, jobID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "note Worker job not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load note Worker job")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
