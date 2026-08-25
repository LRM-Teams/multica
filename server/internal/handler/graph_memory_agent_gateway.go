package handler

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/service"
)

// GraphMemoryAgentTool is the Agent-credential-only gateway for start,
// explore, redirect, submit, and checkpoint. The principal cannot choose a
// workspace, graph owner, version, run, or trajectory.
func (h *Handler) GraphMemoryAgentTool(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	if h.GraphMemoryAgentGateway == nil {
		writeError(w, http.StatusServiceUnavailable, "graph memory agent gateway is unavailable")
		return
	}
	channelID := chi.URLParam(r, "channelId")
	operation := chi.URLParam(r, "operation")
	if err := h.GraphMemoryAgentGateway.ServeHTTP(w, r, principal.WorkspaceID, principal.AgentID, channelID, operation); err != nil {
		switch {
		case errors.Is(err, service.ErrGraphMemoryAgentGatewayForbidden):
			writeError(w, http.StatusForbidden, "managed Graph Memory Agent access required")
		case errors.Is(err, service.ErrGraphMemoryAgentGatewayOperation):
			writeError(w, http.StatusNotFound, "unknown graph memory agent operation")
		case errors.Is(err, service.ErrGraphMemoryAgentRunUnavailable):
			writeError(w, http.StatusConflict, "graph memory agent run is unavailable")
		case errors.Is(err, service.ErrGraphMemoryAgentQuotaExceeded):
			writeError(w, http.StatusTooManyRequests, err.Error())
		default:
			writeError(w, http.StatusServiceUnavailable, err.Error())
		}
	}
}

type graphMemoryAgentUsageRequest struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

func (h *Handler) GraphMemoryAgentUsage(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	if h.GraphMemoryAgentGateway == nil {
		writeError(w, http.StatusServiceUnavailable, "graph memory agent gateway is unavailable")
		return
	}
	var request graphMemoryAgentUsageRequest
	if !decodeJSONBody(w, r, &request) {
		return
	}
	err := h.GraphMemoryAgentGateway.AddUsage(r.Context(), principal.WorkspaceID, principal.AgentID, chi.URLParam(r, "channelId"), request.InputTokens, request.OutputTokens)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrGraphMemoryAgentGatewayForbidden):
			writeError(w, http.StatusForbidden, "managed Graph Memory Agent access required")
		case errors.Is(err, service.ErrGraphMemoryAgentQuotaExceeded):
			writeError(w, http.StatusTooManyRequests, err.Error())
		case errors.Is(err, service.ErrGraphMemoryAgentRunUnavailable), errors.Is(err, service.ErrGraphMemoryAgentRunFenced):
			writeError(w, http.StatusConflict, "graph memory agent run is unavailable")
		default:
			writeError(w, http.StatusServiceUnavailable, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"recorded": true})
}
