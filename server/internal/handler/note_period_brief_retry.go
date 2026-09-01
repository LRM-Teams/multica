package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
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
	if !periodBriefRunLocksComposerStatus(run.Status) {
		writeError(w, http.StatusConflict, "period brief run is not running")
		return
	}

	// Authorization is synthesizer_agent_id on the run — not Notes page ACL.
	// Durable agent_credential tokens have no TaskID and cannot pass
	// loadAgentAccessibleNote.

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
SELECT id, workspace_id, parent_id, owner_user_id, title, icon, content, sort_key, created_at, updated_at, deleted_at
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
		packReady := strings.TrimSpace(ref.PackMarkdown) != ""
		d := periodBriefRetryDisposition(projected.Status, failReason, packReady)
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

		draft, err := scanNotePage(h.DB.QueryRow(ctx, `
SELECT id, workspace_id, parent_id, owner_user_id, title, icon, content, sort_key, created_at, updated_at, deleted_at
FROM note_page WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL`,
			run.DraftPageID, workspaceID))
		if err != nil {
			resp.Skipped = append(resp.Skipped, notePeriodBriefRetrySkipped{AgentID: ref.AgentID, Reason: "draft page missing"})
			continue
		}

		// Clear prior pack atomically so a sibling submit-pack cannot restore
		// the old markdown, and harvest cannot treat it as this retry.
		if err := h.mergeNotePeriodBriefCollector(ctx, run.ID, ref.AgentID, map[string]any{
			"pack_markdown": "",
			"pack_job_id":   "",
		}, "collecting"); err != nil {
			resp.Skipped = append(resp.Skipped, notePeriodBriefRetrySkipped{AgentID: ref.AgentID, Reason: "failed to clear prior pack"})
			continue
		}
		ref.PackMarkdown = ""
		ref.PackJobID = ""
		refs[i] = ref

		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/notes/period-briefs/retry", nil)
		job, ok := h.dispatchNotePeriodBriefCollectorOntoDraft(
			rec, req, workspaceID, run.OwnerUserID, uuidToString(run.OwnerUserID),
			agent, draft, ref.WindowLabel, ref.WindowStart, ref.WindowEnd, ref.ChannelID,
			scopeFromCollectPlan(run.CollectPlan, ref.AgentID),
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
		if err := h.mergeNotePeriodBriefCollector(ctx, run.ID, ref.AgentID, map[string]any{
			"job_id":        ref.JobID,
			"channel_id":    ref.ChannelID,
			"retry_count":   ref.RetryCount,
			"pack_markdown": "",
			"pack_job_id":   "",
		}, "collecting"); err != nil {
			resp.Skipped = append(resp.Skipped, notePeriodBriefRetrySkipped{AgentID: ref.AgentID, Reason: "failed to record retry job"})
			continue
		}
		jobs = append(jobs, job)
		resp.Retried = append(resp.Retried, notePeriodBriefRetryItem{
			AgentID:    ref.AgentID,
			PackPageID: uuidToString(run.DraftPageID),
			JobID:      ref.JobID,
			RetryCount: ref.RetryCount,
		})
	}

	if updated {
		_ = h.updateNotePeriodBriefRunStatus(ctx, run.ID, "collecting")
	}
	allJobs := jobsFromCollectorRefs(refs, uuidToString(run.DraftPageID))
	if len(resp.Retried) == 0 {
		return resp, nil, nil
	}
	return resp, allJobs, nil
}

// dispatchNotePeriodBriefCollectorOntoDraft reuses the draft page (retry path).
func (h *Handler) dispatchNotePeriodBriefCollectorOntoDraft(
	w http.ResponseWriter,
	r *http.Request,
	workspaceID, userID pgtype.UUID,
	userIDString string,
	agent db.Agent,
	draft notePageRow,
	windowLabel, windowStart, windowEnd, preferredChannelID string,
	scope notePeriodBriefCollectorScope,
) (NoteWorkerJobResponse, bool) {
	_ = preferredChannelID
	return h.dispatchNotePeriodBriefCollector(w, r, workspaceID, userID, userIDString, draft, agent, windowLabel, windowStart, windowEnd, scope)
}
