package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// graphConsolidationRunResponse is the JSON view of one
// graph_memory_consolidation_run row (spec §10).
type graphConsolidationRunResponse struct {
	ID          string          `json:"id"`
	WorkspaceID string          `json:"workspace_id"`
	Status      string          `json:"status"`
	TriggerKind string          `json:"trigger_kind"`
	Error       string          `json:"error,omitempty"`
	Details     json.RawMessage `json:"details,omitempty"`
	CreatedAt   string          `json:"created_at"`
	StartedAt   string          `json:"started_at,omitempty"`
	FinishedAt  string          `json:"finished_at,omitempty"`
}

func graphConsolidationRunResponseFromRow(row db.GraphMemoryConsolidationRun) graphConsolidationRunResponse {
	resp := graphConsolidationRunResponse{
		ID:          uuidToString(row.ID),
		WorkspaceID: uuidToString(row.WorkspaceID),
		Status:      row.Status,
		TriggerKind: row.TriggerKind,
		Error:       row.Error,
		CreatedAt:   row.CreatedAt.Time.UTC().Format(time.RFC3339),
	}
	if len(row.Details) > 0 {
		resp.Details = json.RawMessage(row.Details)
	}
	if row.StartedAt.Valid {
		resp.StartedAt = row.StartedAt.Time.UTC().Format(time.RFC3339)
	}
	if row.FinishedAt.Valid {
		resp.FinishedAt = row.FinishedAt.Time.UTC().Format(time.RFC3339)
	}
	return resp
}

func graphConsolidationRunResponses(rows []db.GraphMemoryConsolidationRun) []graphConsolidationRunResponse {
	out := make([]graphConsolidationRunResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, graphConsolidationRunResponseFromRow(row))
	}
	return out
}

// StartGraphMemoryConsolidation serves POST .../graph-memory/consolidations
// (owner/admin). Retry = POST again (spec §10).
func (h *Handler) StartGraphMemoryConsolidation(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}
	if !roleAllowed(member.Role, "owner", "admin") {
		writeError(w, http.StatusForbidden, "only workspace owner/admin can run graph consolidation")
		return
	}
	runID, err := h.GraphMemoryConsolidation.Run(r.Context(), workspaceID, "manual")
	if errors.Is(err, service.ErrGraphNotReady) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"graph_not_ready"}`))
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start graph consolidation")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"id": runID, "status": "queued"})
}

// ListGraphMemoryConsolidations serves GET .../graph-memory/consolidations.
func (h *Handler) ListGraphMemoryConsolidations(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	rows, err := h.Queries.ListGraphMemoryConsolidationRuns(r.Context(), parseUUID(workspaceID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list graph consolidations")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": graphConsolidationRunResponses(rows)})
}

// GetGraphMemoryConsolidation serves GET .../graph-memory/consolidations/{runId}.
func (h *Handler) GetGraphMemoryConsolidation(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	runID, perr := parseUUIDLoose(chi.URLParam(r, "runId"))
	if perr != nil || !runID.Valid {
		writeError(w, http.StatusBadRequest, "invalid run id")
		return
	}
	row, err := h.Queries.GetGraphMemoryConsolidationRun(r.Context(), db.GetGraphMemoryConsolidationRunParams{
		ID: runID, WorkspaceID: parseUUID(workspaceID),
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "graph consolidation run not found")
		return
	}
	writeJSON(w, http.StatusOK, graphConsolidationRunResponseFromRow(row))
}
