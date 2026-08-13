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

	"github.com/multica-ai/multica/server/internal/messageparts"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// noteWorkerJobCreateRequest is the Worker contract create body (S2-C3).
// It must not accept Editor edit fields (prompt / action / replace_page).
type noteWorkerJobCreateRequest struct {
	AgentID     string `json:"agent_id"`
	Instruction string `json:"instruction"`
	Intent      string `json:"intent"`
	// Optional group channel destination. When empty, the Worker posts into
	// the caller's 1:1 agent DM (main Messages timeline — not chat_session bubble).
	ChannelID string `json:"channel_id"`
	// Editor-only fields — presence is misuse and must 400.
	Prompt string `json:"prompt"`
	Action string `json:"action"`
}

// NoteWorkerJobResponse is the durable Worker job record. After dispatch,
// status is projected from the linked agent_inbox_event when present.
type NoteWorkerJobResponse struct {
	ID               string  `json:"id"`
	WorkspaceID      string  `json:"workspace_id"`
	PageID           string  `json:"page_id"`
	AgentID          string  `json:"agent_id"`
	Instruction      string  `json:"instruction"`
	Status           string  `json:"status"`
	Intent           string  `json:"intent"`
	TaskID           *string `json:"task_id,omitempty"`
	ChannelID        *string `json:"channel_id,omitempty"`
	ChannelMessageID *string `json:"channel_message_id,omitempty"`
	ChatSessionID    *string `json:"chat_session_id,omitempty"`
	FailureReason    *string `json:"failure_reason,omitempty"`
	CreatedAt        string  `json:"created_at"`
	UpdatedAt        string  `json:"updated_at"`
}

// noteWorkerStatusFromTask maps agent_inbox_event lifecycle onto Worker job
// statuses (pending/dispatched/running/completed/failed/cancelled).
func noteWorkerStatusFromTask(taskStatus string, terminalOutcome pgtype.Text, startedAt pgtype.Timestamptz, failureReason *string) string {
	switch taskStatus {
	case "pending", "failed":
		// Inbox "pending" means the Worker task is already enqueued — surface
		// as dispatched so the notes UI does not look stuck before drain.
		return "dispatched"
	case "draining":
		if startedAt.Valid {
			return "running"
		}
		return "dispatched"
	case "suppressed":
		return "cancelled"
	case "acked":
		if terminalOutcome.Valid {
			switch terminalOutcome.String {
			case "failed":
				return "failed"
			case "cancelled":
				return "cancelled"
			case "completed":
				return "completed"
			}
		}
		if failureReason != nil && strings.TrimSpace(*failureReason) != "" {
			return "failed"
		}
		return "completed"
	default:
		return "dispatched"
	}
}

func (h *Handler) noteWorkerJobResponse(ctx context.Context, workspaceID, userID, jobID pgtype.UUID) (NoteWorkerJobResponse, error) {
	var (
		resp                NoteWorkerJobResponse
		id, wsID, pageID    pgtype.UUID
		agentID, taskID     pgtype.UUID
		channelID           pgtype.UUID
		channelMessageID    pgtype.UUID
		chatSessionID       pgtype.UUID
		instruction, status string
		failure             *string
		createdAt           pgtype.Timestamptz
		updatedAt           pgtype.Timestamptz
		taskStatus          pgtype.Text
		terminalOutcome     pgtype.Text
		startedAt           pgtype.Timestamptz
		taskFailure         *string
	)
	err := h.DB.QueryRow(ctx, `
SELECT
  j.id, j.workspace_id, j.page_id, j.agent_id, j.instruction, j.status, j.task_id,
  j.channel_id, j.channel_message_id, j.failure_reason, j.created_at, j.updated_at,
  e.status, e.terminal_outcome, e.started_at, e.failure_reason, e.chat_session_id
FROM note_worker_job j
LEFT JOIN agent_inbox_event e ON e.id = j.task_id
WHERE j.id = $1 AND j.workspace_id = $2 AND j.creator_id = $3`, jobID, workspaceID, userID).Scan(
		&id, &wsID, &pageID, &agentID, &instruction, &status, &taskID,
		&channelID, &channelMessageID, &failure, &createdAt, &updatedAt,
		&taskStatus, &terminalOutcome, &startedAt, &taskFailure, &chatSessionID,
	)
	if err != nil {
		return resp, err
	}

	projected := status
	if taskID.Valid && taskStatus.Valid {
		projected = noteWorkerStatusFromTask(taskStatus.String, terminalOutcome, startedAt, taskFailure)
		// Best-effort persist so list/debug and later polls stay coherent.
		if projected != status {
			_, _ = h.DB.Exec(ctx, `
UPDATE note_worker_job
SET status = $1, failure_reason = COALESCE($2, failure_reason), updated_at = now()
WHERE id = $3`, projected, taskFailure, id)
			status = projected
			if taskFailure != nil && strings.TrimSpace(*taskFailure) != "" {
				failure = taskFailure
			}
		}
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
	if channelID.Valid {
		s := uuidToString(channelID)
		resp.ChannelID = &s
	}
	if channelMessageID.Valid {
		s := uuidToString(channelMessageID)
		resp.ChannelMessageID = &s
	}
	if chatSessionID.Valid {
		s := uuidToString(chatSessionID)
		resp.ChatSessionID = &s
	}
	return resp, nil
}

// CreateNoteWorkerJob records a Worker job and posts into a Messages channel
// (agent DM or group) so progress and replies appear in the main conversation
// timeline — not the retired floating chat_session bubble.
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
	if h.TxStarter == nil {
		writeError(w, http.StatusInternalServerError, "transaction starter unavailable")
		return
	}

	ch, ok := h.resolveNoteWorkerChannel(w, r, page.WorkspaceID, userIDString, agent, strings.TrimSpace(req.ChannelID))
	if !ok {
		return
	}

	jobID := uuid.New()
	jobUUID := parseUUID(jobID.String())
	if _, err := h.DB.Exec(r.Context(), `
INSERT INTO note_worker_job (id, workspace_id, page_id, creator_id, agent_id, instruction, status, channel_id)
VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7)`,
		jobUUID, page.WorkspaceID, page.ID, userID, agentID, instruction, parseUUID(ch.ID)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create note Worker job")
		return
	}

	visibleContent, parts, err := h.buildNoteWorkerChannelMessage(r.Context(), ch, agent, page, instruction)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	authorName := h.channelAuthorName(r.Context(), userIDString)
	threadID := uuid.NewString()
	result, err := h.createUserChannelMessageWithIdempotency(r.Context(), channelMessageInsertInput{
		ChannelID:   parseUUID(ch.ID),
		WorkspaceID: page.WorkspaceID,
		AuthorID:    userID,
		AuthorName:  authorName,
		Content:     visibleContent,
		Parts:       parts,
		ThreadID:    &threadID,
	}, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to post note Worker message")
		return
	}
	msg := result.Message
	_, _ = h.DB.Exec(r.Context(), `UPDATE channel SET updated_at = now() WHERE id = $1`, parseUUID(ch.ID))
	if ch.Kind == "dm" {
		h.clearDMHiddenForChannelMembers(r.Context(), uuidToString(page.WorkspaceID), parseUUID(ch.ID))
	}
	// Publish to humans only — skip scheduleCanonicalMessageDelivery so the
	// directed inbox wake below is the sole agent wake (no Message double-fire).
	recipientIDs := recipientUserIDsFromSet(h.channelHumanMemberIDs(r.Context(), uuidToString(page.WorkspaceID), ch.ID))
	h.publishToUsers(protocol.EventChannelMessage, uuidToString(page.WorkspaceID), "member", userIDString, recipientIDs, msg)

	workerPrompt := wrapNoteWorkerChannelWakePrompt(
		buildNoteWorkerPrompt(instruction, uuidToString(page.ID), page.Title, page.Content),
		h.agentMessageTargetForPrompt(r.Context(), ch, msg),
	)
	task, err := h.enqueueChannelAgentPrompt(
		r.Context(), ch, agent, msg, userID, workerPrompt,
		"note worker", true, protocol.AgentInboxReasonNoteWorker, channelDirectedWakePriority,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to enqueue note Worker job: "+err.Error())
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
SET task_id = $1, channel_message_id = $2, status = 'dispatched', updated_at = now()
WHERE id = $3`, task.ID, parseUUID(msg.ID), jobUUID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to link note Worker task")
		return
	}

	resp, err := h.noteWorkerJobResponse(r.Context(), page.WorkspaceID, userID, jobUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load note Worker job")
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *Handler) resolveNoteWorkerChannel(
	w http.ResponseWriter,
	r *http.Request,
	workspaceID pgtype.UUID,
	userID string,
	agent db.Agent,
	channelID string,
) (ChannelResponse, bool) {
	ws := uuidToString(workspaceID)
	if channelID == "" {
		return h.ensureNoteWorkerAgentDM(w, r.Context(), ws, userID, agent)
	}
	chID, ok := parseUUIDOrBadRequest(w, channelID, "channel_id")
	if !ok {
		return ChannelResponse{}, false
	}
	ch, found := h.getChannel(r.Context(), ws, chID)
	if !found || ch.Kind != "group" {
		writeError(w, http.StatusNotFound, "channel not found")
		return ChannelResponse{}, false
	}
	if !h.requireChannelUserMember(w, r.Context(), ws, chID, parseUUID(userID)) {
		return ChannelResponse{}, false
	}
	if !h.channelHasAgentMember(r.Context(), workspaceID, chID, agent.ID) {
		writeError(w, http.StatusBadRequest, "agent is not a member of this channel")
		return ChannelResponse{}, false
	}
	return ch, true
}

func (h *Handler) ensureNoteWorkerAgentDM(
	w http.ResponseWriter,
	ctx context.Context,
	workspaceID, userID string,
	agent db.Agent,
) (ChannelResponse, bool) {
	canonical := dmCanonicalName("user", userID, "agent", uuidToString(agent.ID))
	if ch, found := h.findDMChannel(ctx, workspaceID, canonical); found {
		h.clearDMPeerHidden(ctx, workspaceID, userID, dmPeerRef{Type: "agent", ID: agent.ID})
		return ch, true
	}
	ch, created := h.createDMChannel(ctx, w, workspaceID, userID, canonical, []dmMember{
		{memberType: "user", memberID: parseUUID(userID)},
		{memberType: "agent", memberID: agent.ID},
	})
	if !created {
		return ChannelResponse{}, false
	}
	h.clearDMPeerHidden(ctx, workspaceID, userID, dmPeerRef{Type: "agent", ID: agent.ID})
	return ch, true
}

func (h *Handler) buildNoteWorkerChannelMessage(
	ctx context.Context,
	ch ChannelResponse,
	agent db.Agent,
	page notePageRow,
	instruction string,
) (string, []protocol.MessagePart, error) {
	title := strings.TrimSpace(page.Title)
	if title == "" {
		title = "Untitled"
	}
	body := strings.TrimSpace(instruction)
	if ch.Kind == "group" {
		handle := strings.TrimSpace(agent.Name)
		if handle == "" {
			handle = uuidToString(agent.ID)
		}
		body = "@" + handle + " " + body
	}
	brief := protocol.MessagePart{
		Type:  protocol.MessagePartTypeNoteBrief,
		RefID: uuidToString(page.ID),
		Label: title,
		Text:  page.Content,
	}
	content, parts, err := messageparts.Normalize(body, []protocol.MessagePart{brief})
	if err != nil {
		return "", nil, err
	}
	content, parts, err = h.enrichChannelMessageMentions(ctx, ch, content, parts)
	if err != nil {
		return "", nil, err
	}
	return content, parts, nil
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
