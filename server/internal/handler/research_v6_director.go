package handler

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/researchrun"
)

type replaceResearchV6DirectorRequest struct {
	DirectorAgentID      string `json:"director_agent_id"`
	ExpectedStateVersion int64  `json:"expected_state_version"`
	Reason               string `json:"reason"`
	ClientRequestID      string `json:"client_request_id"`
}

type markResearchV6DirectorUnavailableRequest struct {
	AssignmentID         string `json:"assignment_id"`
	FailureClass         string `json:"failure_class"`
	Diagnostics          string `json:"diagnostics"`
	ExpectedStateVersion int64  `json:"expected_state_version"`
	ClientRequestID      string `json:"client_request_id"`
}

func (h *Handler) PutResearchV6Director(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	service, ok := h.ResearchRun.(researchrun.ResearchRunDirectorControl)
	if !ok || service == nil {
		writeRonaldoV6Error(w, http.StatusServiceUnavailable, "research.v6.capability_unavailable", "research V6 Director service is unavailable", true)
		return
	}
	var request replaceResearchV6DirectorRequest
	if !decodeResearchJSON(w, r, &request) {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	runID := chi.URLParam(r, "id")
	for field, value := range map[string]string{"workspace_id": workspaceID, "id": runID, "director_agent_id": request.DirectorAgentID, "client_request_id": request.ClientRequestID} {
		if _, valid := parseUUIDOrBadRequest(w, value, field); !valid {
			return
		}
	}
	assignment, err := service.AssignV6Director(r.Context(), researchrun.AssignV6DirectorInput{
		WorkspaceID: workspaceID, RunID: runID, AgentID: request.DirectorAgentID,
		UserID: userID, Reason: request.Reason, ClientRequestID: request.ClientRequestID,
		ExpectedStateVersion: request.ExpectedStateVersion,
	})
	if err != nil {
		writeResearchV6DomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, assignment)
}

// PostResearchV6DirectorUnavailable records a durable Director failure and
// moves the Run into awaiting_director until the user assigns a replacement.
func (h *Handler) PostResearchV6DirectorUnavailable(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	service, ok := h.ResearchRun.(researchrun.ResearchRunDirectorControl)
	if !ok || service == nil {
		writeRonaldoV6Error(w, http.StatusServiceUnavailable, "research.v6.capability_unavailable", "research V6 Director service is unavailable", true)
		return
	}
	var request markResearchV6DirectorUnavailableRequest
	if !decodeResearchJSON(w, r, &request) {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	runID := chi.URLParam(r, "id")
	for field, value := range map[string]string{
		"workspace_id":      workspaceID,
		"id":                runID,
		"assignment_id":     request.AssignmentID,
		"client_request_id": request.ClientRequestID,
	} {
		if _, valid := parseUUIDOrBadRequest(w, value, field); !valid {
			return
		}
	}
	if strings.TrimSpace(request.FailureClass) == "" {
		writeError(w, http.StatusBadRequest, "failure_class is required")
		return
	}
	assignment, err := service.MarkV6DirectorUnavailable(r.Context(), researchrun.MarkV6DirectorUnavailableInput{
		WorkspaceID: workspaceID, RunID: runID, AssignmentID: request.AssignmentID,
		FailureClass: request.FailureClass, Diagnostics: request.Diagnostics,
		ExpectedStateVersion: request.ExpectedStateVersion, ClientRequestID: request.ClientRequestID,
	})
	if err != nil {
		writeResearchV6DomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, assignment)
}
