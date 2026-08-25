package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type submitNotePeriodBriefPackRequest struct {
	Markdown string `json:"markdown"`
}

type submitNotePeriodBriefPackResponse struct {
	DraftPageID string `json:"draft_page_id"`
	AgentID     string `json:"agent_id"`
	Bytes       int    `json:"bytes"`
	Message     string `json:"message"`
}

// SubmitAgentNotePeriodBriefPack stores a collector pack into the run's
// collectors JSONB (implicit artifact). Packs are not Notes pages.
// POST /api/agent/notes/period-briefs/{draftPageId}/submit-pack
func (h *Handler) SubmitAgentNotePeriodBriefPack(w http.ResponseWriter, r *http.Request) {
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

	ref, _, found := findCollectorRef(run.Collectors, uuidToString(agentUUID))
	if !found {
		writeError(w, http.StatusForbidden, "only a collector on this Period Brief run may submit a pack")
		return
	}

	// Authorization is membership on the run — not Notes page ACL.
	// Collectors often authenticate with a durable agent_credential (no
	// TaskID), which cannot pass loadAgentAccessibleNote / notes get.

	markdown, ok := readPeriodBriefPackMarkdown(w, r)
	if !ok {
		return
	}
	if markdown == "" {
		writeError(w, http.StatusBadRequest, "pack markdown is required")
		return
	}

	callerJobID := h.lookupNoteWorkerJobIDByTask(r.Context(), workspaceUUID, firstNonEmpty(principal.InboxEventID, principal.TaskID))
	if callerJobID != "" && strings.TrimSpace(ref.JobID) != "" && callerJobID != ref.JobID {
		writeError(w, http.StatusConflict, "stale collector job; this run already retried")
		return
	}
	packJobID := callerJobID
	if packJobID == "" {
		packJobID = strings.TrimSpace(ref.JobID)
	}

	status := run.Status
	if status == "" {
		status = "collecting"
	}
	if err := h.mergeNotePeriodBriefCollector(r.Context(), run.ID, uuidToString(agentUUID), map[string]any{
		"pack_markdown": markdown,
		"pack_job_id":   packJobID,
	}, status); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store collector pack")
		return
	}
	if names := h.periodBriefCollectorSpokenNames(r.Context(), run.WorkspaceID, []string{uuidToString(agentUUID)}); len(names) > 0 {
		h.postPeriodBriefPackReceived(r.Context(), run, names[0], markdown)
	} else {
		h.postPeriodBriefPackReceived(r.Context(), run, "", markdown)
	}

	writeJSON(w, http.StatusOK, submitNotePeriodBriefPackResponse{
		DraftPageID: draftID,
		AgentID:     uuidToString(agentUUID),
		Bytes:       len(markdown),
		Message:     "Collector pack stored on the Period Brief run. Do not --note-write this pack into Notes.",
	})
}

func (h *Handler) lookupNoteWorkerJobIDByTask(ctx context.Context, workspaceID pgtype.UUID, taskID string) string {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return ""
	}
	id, ok := parseUUIDQuiet(taskID)
	if !ok {
		return ""
	}
	var jobID string
	if err := h.DB.QueryRow(ctx, `
SELECT id::text FROM note_worker_job
WHERE workspace_id = $1 AND task_id = $2
LIMIT 1`, workspaceID, id).Scan(&jobID); err != nil {
		return ""
	}
	return strings.TrimSpace(jobID)
}

func readPeriodBriefPackMarkdown(w http.ResponseWriter, r *http.Request) (string, bool) {
	ct := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20)) // 2 MiB
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read body")
		return "", false
	}
	text := strings.TrimSpace(string(body))
	if text == "" {
		return "", true
	}
	if strings.Contains(ct, "application/json") {
		var req submitNotePeriodBriefPackRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return "", false
		}
		return strings.TrimSpace(req.Markdown), true
	}
	return text, true
}
