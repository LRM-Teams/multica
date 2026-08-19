package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type retryNotePeriodBriefCollectorsRequest struct {
	CollectorAgentIDs []string `json:"collector_agent_ids"`
}

type retryNotePeriodBriefCollectorsResponse struct {
	DraftPageID string                        `json:"draft_page_id"`
	Retried     []notePeriodBriefRetryItem    `json:"retried"`
	Skipped     []notePeriodBriefRetrySkipped `json:"skipped"`
	Message     string                        `json:"message"`
}

type notePeriodBriefRetryItem struct {
	AgentID    string `json:"agent_id"`
	PackPageID string `json:"pack_page_id"`
	JobID      string `json:"job_id"`
	RetryCount int    `json:"retry_count"`
}

type notePeriodBriefRetrySkipped struct {
	AgentID string `json:"agent_id"`
	Reason  string `json:"reason"`
}

// RetryAgentNotePeriodBriefCollectors is the narrow synthesizer tool:
// POST /api/agent/notes/period-briefs/{draftPageId}/retry-collectors
func (h *Handler) RetryAgentNotePeriodBriefCollectors(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	draftID := chi.URLParam(r, "draftPageId")
	draftUUID, ok := parseUUIDOrBadRequest(w, draftID, "draftPageId")
	if !ok {
		return
	}
	workspaceUUID, ok := parseUUIDOrBadRequest(w, principal.WorkspaceID, "workspace id")
	if !ok {
		return
	}
	agentUUID, ok := parseUUIDOrBadRequest(w, principal.AgentID, "agent id")
	if !ok {
		return
	}

	run, err := h.loadNotePeriodBriefRunByDraft(r.Context(), workspaceUUID, draftUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "period brief run not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load period brief run")
		return
	}
	if uuidToString(run.SynthesizerAgentID) != uuidToString(agentUUID) {
		writeError(w, http.StatusForbidden, "only the Period Brief synthesizer for this draft may retry collectors")
		return
	}

	// Authorize via the synthesizer's Worker job / note_brief on this draft.
	if _, _, ok := h.loadAgentAccessibleNote(w, r, principal, draftID); !ok {
		return
	}

	var req retryNotePeriodBriefCollectorsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && r.ContentLength != 0 {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	resp, jobs, err := h.retryNotePeriodBriefCollectors(r.Context(), r, workspaceUUID, run, req.CollectorAgentIDs, principal)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
	if len(jobs) == 0 {
		return
	}

	bg := context.WithoutCancel(r.Context())
	draft, err := scanNotePage(h.DB.QueryRow(bg, `
SELECT id, workspace_id, parent_id, owner_user_id, title, content, sort_key, created_at, updated_at, deleted_at
FROM note_page WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL`, draftUUID, workspaceUUID))
	if err != nil {
		return
	}
	window := noteRetrospectiveWindow{
		Kind:     noteRetrospectiveWindowKind(run.WindowKind),
		Timezone: run.Timezone,
		Start:    run.WindowStart,
		End:      run.WindowEnd,
		Label:    run.WindowLabel,
	}
	channelID := ""
	if run.ChannelID.Valid {
		channelID = uuidToString(run.ChannelID)
	}
	go h.finishNotePeriodBriefAfterCollectors(
		bg, workspaceUUID, run.OwnerUserID, uuidToString(run.OwnerUserID),
		run.SynthesizerAgentID, run.FolderPageID, draft, window, channelID, run.FactsText,
		jobs, run.SourcesUsed, run.SourcesEmpty, run.SourcesSkipped,
	)
}

func (h *Handler) retryNotePeriodBriefCollectors(
	ctx context.Context,
	r *http.Request,
	workspaceID pgtype.UUID,
	run notePeriodBriefRunRow,
	wantAgentIDs []string,
	_ middleware.AgentPrincipal,
) (retryNotePeriodBriefCollectorsResponse, []NoteWorkerJobResponse, error) {
	want := map[string]struct{}{}
	for _, id := range wantAgentIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			want[id] = struct{}{}
		}
	}
	filter := len(want) > 0

	resp := retryNotePeriodBriefCollectorsResponse{
		DraftPageID: uuidToString(run.DraftPageID),
		Message:     "Retried collectors dispatched. Stop and wait — platform re-wakes the synthesizer when packs settle.",
	}
	refs := append([]notePeriodBriefCollectorRef(nil), run.Collectors...)
	jobs := make([]NoteWorkerJobResponse, 0, len(refs))
	updated := false

	for i, ref := range refs {
		if filter {
			if _, ok := want[ref.AgentID]; !ok {
				continue
			}
		}
		// Snapshot current outcome for this collector's latest job.
		projected, _ := h.noteWorkerJobResponse(ctx, workspaceID, run.OwnerUserID, parseUUID(ref.JobID))
		failReason := ""
		if projected.FailureReason != nil {
			failReason = *projected.FailureReason
		}
		page, err := scanNotePage(h.DB.QueryRow(ctx, `
SELECT id, workspace_id, parent_id, owner_user_id, title, content, sort_key, created_at, updated_at, deleted_at
FROM note_page WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL`,
			parseUUID(ref.PackPageID), workspaceID))
		if err != nil {
			resp.Skipped = append(resp.Skipped, notePeriodBriefRetrySkipped{AgentID: ref.AgentID, Reason: "pack page missing"})
			continue
		}
		stub := strings.Contains(page.Content, notePeriodBriefCollectorStubMarker)
		packReady := !stub && strings.TrimSpace(page.Content) != ""
		if !packReady {
			chID := ref.ChannelID
			if projected.ChannelID != nil {
				chID = *projected.ChannelID
			}
			if proposal := h.loadCollectorPackNoteWriteProposal(ctx, chID, ref.PackPageID); proposal != "" {
				packReady = true
			}
		}
		d := classifyPeriodBriefCollectorOutcome(projected.Status, failReason, failReason, packReady, false)
		if packReady || d.Status == "ready" {
			resp.Skipped = append(resp.Skipped, notePeriodBriefRetrySkipped{AgentID: ref.AgentID, Reason: "already ready"})
			continue
		}
		if !d.Retryable {
			reason := d.AbandonWhy
			if reason == "" {
				reason = "not retryable"
			}
			resp.Skipped = append(resp.Skipped, notePeriodBriefRetrySkipped{AgentID: ref.AgentID, Reason: reason})
			continue
		}
		if ref.RetryCount >= notePeriodBriefCollectorMaxRetries {
			resp.Skipped = append(resp.Skipped, notePeriodBriefRetrySkipped{
				AgentID: ref.AgentID,
				Reason:  "max retries reached",
			})
			continue
		}

		agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
			ID:          parseUUID(ref.AgentID),
			WorkspaceID: workspaceID,
		})
		if err != nil || agent.ArchivedAt.Valid {
			resp.Skipped = append(resp.Skipped, notePeriodBriefRetrySkipped{AgentID: ref.AgentID, Reason: "collector agent missing"})
			continue
		}

		// Reset stub so harvest does not treat stale content as ready.
		agentLabel := strings.TrimSpace(agent.DisplayName)
		if agentLabel == "" {
			agentLabel = strings.TrimSpace(agent.Name)
		}
		stubBody := notePeriodBriefCollectorPackStub(ref.WindowLabel, agentLabel)
		_, _ = h.DB.Exec(ctx, `
UPDATE note_page SET content = $1, updated_at = now(), updated_by = $2 WHERE id = $3 AND workspace_id = $4`,
			stubBody, run.OwnerUserID, page.ID, workspaceID)
		page.Content = stubBody

		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/notes/period-briefs/retry", nil)
		job, ok := h.dispatchNotePeriodBriefCollectorOntoPack(
			rec, req, workspaceID, run.OwnerUserID, uuidToString(run.OwnerUserID),
			agent, page, ref.WindowLabel, ref.WindowStart, ref.WindowEnd, ref.ChannelID,
		)
		if !ok {
			resp.Skipped = append(resp.Skipped, notePeriodBriefRetrySkipped{
				AgentID: ref.AgentID,
				Reason:  "redispatch failed: " + strings.TrimSpace(rec.Body.String()),
			})
			continue
		}

		ref.JobID = job.ID
		if job.ChannelID != nil {
			ref.ChannelID = *job.ChannelID
		}
		ref.RetryCount++
		refs[i] = ref
		updated = true
		jobs = append(jobs, job)
		resp.Retried = append(resp.Retried, notePeriodBriefRetryItem{
			AgentID:    ref.AgentID,
			PackPageID: ref.PackPageID,
			JobID:      ref.JobID,
			RetryCount: ref.RetryCount,
		})
	}

	if updated {
		_ = h.updateNotePeriodBriefRunCollectors(ctx, run.ID, refs, "collecting")
	}
	// Await path needs the full job list (retried + still-ready others) so
	// synthesis sees the complete board. Rebuild from updated refs.
	allJobs := jobsFromCollectorRefs(refs)
	if len(resp.Retried) == 0 {
		return resp, nil, nil
	}
	return resp, allJobs, nil
}

// dispatchNotePeriodBriefCollectorOntoPack reuses an existing pack page
// (retry path). Same wake contract as first dispatch.
func (h *Handler) dispatchNotePeriodBriefCollectorOntoPack(
	w http.ResponseWriter,
	r *http.Request,
	workspaceID, userID pgtype.UUID,
	userIDString string,
	agent db.Agent,
	packPage notePageRow,
	windowLabel, windowStart, windowEnd, preferredChannelID string,
) (NoteWorkerJobResponse, bool) {
	ch, ok := h.resolveNoteWorkerChannel(w, r, workspaceID, userIDString, agent, preferredChannelID)
	if !ok {
		// Fall back to DM when preferred channel is stale.
		if preferredChannelID != "" {
			ch, ok = h.resolveNoteWorkerChannel(w, r, workspaceID, userIDString, agent, "")
		}
		if !ok {
			return NoteWorkerJobResponse{}, false
		}
	}

	packPageID := uuidToString(packPage.ID)
	instruction := notePeriodBriefCollectorInstruction(packPageID, windowLabel, windowStart, windowEnd)

	jobID := uuid.New()
	jobUUID := parseUUID(jobID.String())
	if _, err := h.DB.Exec(r.Context(), `
INSERT INTO note_worker_job (id, workspace_id, page_id, creator_id, agent_id, instruction, status, channel_id)
VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7)`,
		jobUUID, workspaceID, packPage.ID, userID, agent.ID, instruction, parseUUID(ch.ID)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create collector Worker job")
		return NoteWorkerJobResponse{}, false
	}

	visibleContent, parts, err := h.buildNoteWorkerChannelMessage(r.Context(), ch, agent, packPage, instruction)
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
		writeError(w, http.StatusInternalServerError, "failed to post collector Worker message")
		return NoteWorkerJobResponse{}, false
	}
	msg := result.Message
	_, _ = h.DB.Exec(r.Context(), `UPDATE channel SET updated_at = now() WHERE id = $1`, parseUUID(ch.ID))

	workerPrompt := wrapNoteWorkerChannelWakePrompt(
		buildNotePeriodBriefCollectorPrompt(
			instruction, packPageID, windowLabel, windowStart, windowEnd, packPage.Title, packPage.Content,
		),
		h.agentMessageTargetForPrompt(r.Context(), ch, msg),
	)
	task, err := h.enqueueChannelAgentPrompt(
		r.Context(), ch, agent, msg, userID, workerPrompt,
		"note worker", true, protocol.AgentInboxReasonNoteWorker, channelDirectedWakePriority,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to enqueue collector Worker job: "+err.Error())
		return NoteWorkerJobResponse{}, false
	}
	mergedContext, err := service.WithNoteBrief(task.Context, service.NoteBrief{
		Version: 1,
		PageID:  packPageID,
		Title:   packPage.Title,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to attach collector note brief")
		return NoteWorkerJobResponse{}, false
	}
	if err := h.persistPeriodBriefNoteBriefContext(r.Context(), task.ID, mergedContext); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to persist collector note brief")
		return NoteWorkerJobResponse{}, false
	}
	if _, err := h.DB.Exec(r.Context(), `
UPDATE note_worker_job
SET task_id = $1, channel_message_id = $2, status = 'dispatched', updated_at = now()
WHERE id = $3`, task.ID, parseUUID(msg.ID), jobUUID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to link collector Worker task")
		return NoteWorkerJobResponse{}, false
	}
	resp, err := h.noteWorkerJobResponse(r.Context(), workspaceID, userID, jobUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load collector Worker job")
		return NoteWorkerJobResponse{}, false
	}
	return resp, true
}
