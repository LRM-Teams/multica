package handler

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/researchrun"
)

const maxResearchV6SubmissionBytes int64 = 2 << 20

type researchV6InboxContext struct {
	Type         string `json:"type"`
	RunID        string `json:"run_id"`
	WorkItemID   string `json:"work_item_id"`
	AttemptID    string `json:"attempt_id"`
	ManifestID   string `json:"manifest_id"`
	ManifestHash string `json:"manifest_hash"`
}

func (h *Handler) researchV6Submission(w http.ResponseWriter) (researchrun.ResearchRunSubmission, bool) {
	submission, ok := h.ResearchRun.(researchrun.ResearchRunSubmission)
	if !ok || submission == nil {
		writeRonaldoV6Error(w, http.StatusServiceUnavailable, "research.v6.capability_unavailable", "research V6 work service is unavailable", true)
		return nil, false
	}
	return submission, true
}

func (h *Handler) authorizeResearchV6Attempt(w http.ResponseWriter, r *http.Request) (researchrun.V6AttemptAccess, bool) {
	principal, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return researchrun.V6AttemptAccess{}, false
	}
	workspaceID := h.resolveWorkspaceID(r)
	if principal.WorkspaceID != workspaceID {
		writeRonaldoV6Error(w, http.StatusForbidden, "research.v6.principal_mismatch", "access denied", false)
		return researchrun.V6AttemptAccess{}, false
	}
	access := researchrun.V6AttemptAccess{
		WorkspaceID: workspaceID, RunID: chi.URLParam(r, "id"), WorkItemID: chi.URLParam(r, "workItemId"),
		AttemptID: chi.URLParam(r, "attemptId"), AgentID: principal.AgentID,
	}
	for field, value := range map[string]string{"workspace_id": access.WorkspaceID, "id": access.RunID, "work_item_id": access.WorkItemID, "attempt_id": access.AttemptID} {
		if _, valid := parseUUIDOrBadRequest(w, value, field); !valid {
			return researchrun.V6AttemptAccess{}, false
		}
	}
	if principal.ActorSource != "agent_credential" {
		access.InboxTaskID = strings.TrimSpace(principal.InboxEventID)
		if access.InboxTaskID == "" {
			access.InboxTaskID = strings.TrimSpace(principal.TaskID)
		}
		if access.InboxTaskID == "" {
			writeRonaldoV6Error(w, http.StatusForbidden, "research.v6.principal_mismatch", "task-bound agent credential required", false)
			return researchrun.V6AttemptAccess{}, false
		}
		return access, true
	}
	// The daemon's durable Credential Proxy deliberately strips the retired
	// task and lease headers. Bind the request back to the exact active V6
	// attempt server-side instead of asking the Agent process to carry those
	// credentials. The attempt UUIDs, assignment, Inbox binding, and draining
	// state together form the task fence. The Agent may start immediately after
	// Inbox insertion, before outbox completion attaches inbox_task_id, so the
	// exact immutable Inbox context also closes that dispatch race.
	err := h.DB.QueryRow(r.Context(), `
		SELECT COALESCE(a.inbox_task_id::text,'')
		FROM research_work_item_attempt a
		JOIN agent_inbox_event e
		  ON e.workspace_id=a.workspace_id AND e.agent_id=a.assigned_agent_id
		JOIN research_team_membership m
		  ON (m.workspace_id,m.session_id,m.id)=(a.workspace_id,a.session_id,a.membership_id)
		WHERE a.workspace_id=$1::uuid AND a.session_id=$2::uuid
		  AND a.work_item_id=$3::uuid AND a.id=$4::uuid
		  AND a.assigned_agent_id=$5::uuid AND m.agent_id=$5::uuid
		  AND e.workspace_id=$1::uuid AND e.agent_id=$5::uuid
		  AND m.state NOT IN ('archived','failed')
		  AND a.status IN ('dispatching','running') AND e.status='draining'
		  AND (a.inbox_task_id IS NULL OR e.id=a.inbox_task_id)
		  AND e.context->>'type'='research_run_work_item'
		  AND e.context->>'run_id'=a.session_id::text
		  AND e.context->>'work_item_id'=a.work_item_id::text
		  AND e.context->>'attempt_id'=a.id::text
	`, access.WorkspaceID, access.RunID, access.WorkItemID, access.AttemptID, access.AgentID).Scan(&access.InboxTaskID)
	if err != nil {
		writeRonaldoV6Error(w, http.StatusConflict, "research.v6.principal_mismatch", "research attempt is not the active Agent task", false)
		return researchrun.V6AttemptAccess{}, false
	}
	return access, true
}

func (h *Handler) GetAgentResearchV6WorkManifest(w http.ResponseWriter, r *http.Request) {
	service, ok := h.researchV6Submission(w)
	if !ok {
		return
	}
	access, ok := h.authorizeResearchV6Attempt(w, r)
	if !ok {
		return
	}
	manifest, err := service.WorkManifest(r.Context(), access)
	if err != nil {
		writeResearchV6DomainError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", `"`+manifest.ETag+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(manifest.Bytes)
}

func (h *Handler) GetAgentResearchV6WorkCatalog(w http.ResponseWriter, r *http.Request) {
	service, ok := h.researchV6Submission(w)
	if !ok {
		return
	}
	access, ok := h.authorizeResearchV6Attempt(w, r)
	if !ok {
		return
	}
	page, err := service.WorkCatalog(r.Context(), researchrun.V6CatalogRequest{
		V6AttemptAccess: access, View: researchrun.V6CatalogView(r.URL.Query().Get("view")), Cursor: r.URL.Query().Get("cursor"),
	})
	if err != nil {
		writeResearchV6DomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"page_key": page.PageKey, "page_hash": page.PageHash, "next_cursor": page.NextCursor, "has_more": page.HasMore, "items": json.RawMessage(page.Bytes)})
}

type acknowledgeResearchV6CatalogRequest struct {
	ClientRequestID string `json:"client_request_id"`
	PageKey         string `json:"page_key"`
	PageHash        string `json:"page_hash"`
}

func (h *Handler) AcknowledgeAgentResearchV6WorkCatalog(w http.ResponseWriter, r *http.Request) {
	service, ok := h.researchV6Submission(w)
	if !ok {
		return
	}
	access, ok := h.authorizeResearchV6Attempt(w, r)
	if !ok {
		return
	}
	var request acknowledgeResearchV6CatalogRequest
	if !decodeResearchJSON(w, r, &request) {
		return
	}
	if _, valid := parseUUIDOrBadRequest(w, request.ClientRequestID, "client_request_id"); !valid {
		return
	}
	err := service.AcknowledgeWorkCatalog(r.Context(), researchrun.AcknowledgeV6CatalogInput{
		V6AttemptAccess: access, ClientRequestID: request.ClientRequestID, PageKey: request.PageKey, PageHash: request.PageHash,
	})
	if err != nil {
		writeResearchV6DomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"acknowledged": true})
}

func (h *Handler) SubmitAgentResearchV6Work(w http.ResponseWriter, r *http.Request) {
	service, ok := h.researchV6Submission(w)
	if !ok {
		return
	}
	access, ok := h.authorizeResearchV6Attempt(w, r)
	if !ok {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxResearchV6SubmissionBytes+1))
	if err != nil {
		writeRonaldoV6Error(w, http.StatusBadRequest, "research.v6.invalid_contract", "failed to read submission", false)
		return
	}
	if int64(len(raw)) > maxResearchV6SubmissionBytes {
		writeRonaldoV6Error(w, http.StatusRequestEntityTooLarge, "research.v6.payload_too_large", "submission exceeds 2 MiB", false)
		return
	}
	outcome, err := service.SubmitV6Work(r.Context(), researchrun.V6SubmissionInput{V6AttemptAccess: access, Raw: raw})
	if err != nil {
		slog.Warn("research V6 work submission rejected", append(logger.RequestAttrs(r),
			"error", err,
			"run_id", access.RunID,
			"work_item_id", access.WorkItemID,
			"attempt_id", access.AttemptID,
			"agent_id", access.AgentID,
		)...)
		writeResearchV6DomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"client_request_id":      outcome.ClientRequestID,
		"outcome":                "accepted",
		"replayed":               outcome.Replayed,
		"state_version":          outcome.StateVersion,
		"through_event_sequence": outcome.ThroughEventSequence,
		"refs":                   []map[string]string{{"kind": "submission", "id": outcome.SubmissionID}},
		"submission_kind":        outcome.Kind,
		"submission_status":      outcome.Status,
		"content_hash":           outcome.ContentHash,
	})
}

func (h *Handler) GetAgentResearchV6DirectorBrief(w http.ResponseWriter, r *http.Request) {
	service, ok := h.researchV6Submission(w)
	if !ok {
		return
	}
	access, ok := h.authorizeResearchV6Attempt(w, r)
	if !ok {
		return
	}
	page, err := service.DirectorBriefPage(r.Context(), access, r.URL.Query().Get("cursor"))
	if err != nil {
		writeResearchV6DomainError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", `"`+page.PageHash+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(page.Bytes)
}

type acknowledgeResearchV6DirectorBriefRequest struct {
	ClientRequestID string `json:"client_request_id"`
	BriefID         string `json:"brief_id"`
	BriefHash       string `json:"brief_hash"`
	PageKey         string `json:"page_key"`
	PageHash        string `json:"page_hash"`
}

func (h *Handler) AcknowledgeAgentResearchV6DirectorBrief(w http.ResponseWriter, r *http.Request) {
	service, ok := h.researchV6Submission(w)
	if !ok {
		return
	}
	access, ok := h.authorizeResearchV6Attempt(w, r)
	if !ok {
		return
	}
	var request acknowledgeResearchV6DirectorBriefRequest
	if !decodeResearchJSON(w, r, &request) {
		return
	}
	for field, value := range map[string]string{"client_request_id": request.ClientRequestID, "brief_id": request.BriefID} {
		if _, valid := parseUUIDOrBadRequest(w, value, field); !valid {
			return
		}
	}
	err := service.AcknowledgeDirectorBrief(r.Context(), researchrun.AcknowledgeV6DirectorBriefInput{
		V6AttemptAccess: access, ClientRequestID: request.ClientRequestID,
		BriefID: request.BriefID, BriefHash: request.BriefHash,
		PageKey: request.PageKey, PageHash: request.PageHash,
	})
	if err != nil {
		writeResearchV6DomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"acknowledged": true})
}

func writeResearchV6DomainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, researchrun.ErrInvalidContract):
		writeRonaldoV6Error(w, http.StatusBadRequest, "research.v6.invalid_contract", researchV6InvalidContractMessage(err), false)
	case errors.Is(err, researchrun.ErrAttemptNotAssigned):
		writeRonaldoV6Error(w, http.StatusForbidden, "research.v6.principal_mismatch", "research attempt is not assigned to this principal", false)
	case errors.Is(err, researchrun.ErrV6IdempotencyConflict), errors.Is(err, researchrun.ErrResultConflict):
		writeRonaldoV6Error(w, http.StatusConflict, "research.v6.idempotency_conflict", "request ID was reused with different content", false)
	case errors.Is(err, researchrun.ErrWorkItemChanged), errors.Is(err, researchrun.ErrWorkItemLeaseLost):
		writeRonaldoV6Error(w, http.StatusConflict, "research.v6.state_version_conflict", "research work item changed", true)
	case errors.Is(err, researchrun.ErrV6NodeAlreadyAbsorbed):
		writeRonaldoV6Error(w, http.StatusConflict, "research.v6.successor_conflict", "research input already has a canonical successor", false)
	case errors.Is(err, researchrun.ErrV6InvalidTierTransition):
		writeRonaldoV6Error(w, http.StatusUnprocessableEntity, "research.v6.invalid_tier_transition", "research tier transition is not allowed", false)
	case errors.Is(err, researchrun.ErrV6DirectorUnavailable):
		writeRonaldoV6Error(w, http.StatusConflict, "research.v6.director_unavailable", "research Director is unavailable", false)
	case errors.Is(err, researchrun.ErrRunNotFound):
		writeRonaldoV6Error(w, http.StatusNotFound, "research.v6.not_found", "research V6 object not found", false)
	default:
		writeRonaldoV6Error(w, http.StatusInternalServerError, "research.v6.internal", "research V6 request failed", true)
	}
}

func researchV6InvalidContractMessage(err error) string {
	const fallback = "invalid research V6 contract"
	detail := strings.TrimSpace(err.Error())
	for strings.HasPrefix(detail, researchrun.ErrInvalidContract.Error()) {
		detail = strings.TrimSpace(strings.TrimPrefix(detail, researchrun.ErrInvalidContract.Error()))
		detail = strings.TrimSpace(strings.TrimPrefix(detail, ":"))
	}
	if detail == "" {
		return fallback
	}
	const maxDetailBytes = 1024
	if len(detail) > maxDetailBytes {
		detail = detail[:maxDetailBytes]
	}
	return fallback + ": " + detail
}

func writeRonaldoV6Error(w http.ResponseWriter, status int, code, message string, retryable bool) {
	writeJSON(w, status, map[string]any{"code": code, "error": message, "retryable": retryable})
}
