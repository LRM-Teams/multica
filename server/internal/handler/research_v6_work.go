package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
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
	event, _, active := h.requireAgentCredentialActiveInboxDelivery(w, r)
	if !active {
		return researchrun.V6AttemptAccess{}, false
	}
	var binding researchV6InboxContext
	if json.Unmarshal(event.Context, &binding) != nil || binding.Type != "research_run_work_item" ||
		binding.RunID != access.RunID || binding.WorkItemID != access.WorkItemID || binding.AttemptID != access.AttemptID {
		writeRonaldoV6Error(w, http.StatusForbidden, "research.v6.principal_mismatch", "active inbox delivery does not match this research attempt", false)
		return researchrun.V6AttemptAccess{}, false
	}
	access.InboxTaskID = uuidToString(event.ID)
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
		writeRonaldoV6Error(w, http.StatusBadRequest, "research.v6.invalid_contract", "invalid research V6 contract", false)
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

func writeRonaldoV6Error(w http.ResponseWriter, status int, code, message string, retryable bool) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": message, "retryable": retryable}})
}
