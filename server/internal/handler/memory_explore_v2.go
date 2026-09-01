// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
)

// The external Memory Explore v2 API (plan Task 13 Step 3). Route
// registration alone never enables access: every request re-checks the
// workspace's memory_explore_v2 phase gate and the Task 8A source fence, and
// a disabled gate answers 503 — never a v1-shaped fallback. Request refs are
// decoded into the typed memorygraph.MemoryRef and validated before any
// resolver runs; no API field carries a raw unvalidated ref map.

type memoryExploreV2SearchRequest struct {
	Query     string `json:"query"`
	ChannelID string `json:"channel_id"`
}

// MemoryExploreV2Search serves POST /api/workspaces/{id}/memory/explore-v2/search:
// the class-aware SearchAt channel (graph current nodes + active atoms) over
// the channel's scoped graph, structured refs out.
func (h *Handler) MemoryExploreV2Search(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	if h.MemoryExploreV2 == nil {
		writeError(w, http.StatusServiceUnavailable, "memory explore v2 is not configured")
		return
	}
	var request memoryExploreV2SearchRequest
	if !decodeJSONBody(w, r, &request) {
		return
	}
	channelID := chi.URLParam(r, "channelId")
	if channelID == "" {
		channelID = request.ChannelID
	}
	if channelID == "" {
		writeError(w, http.StatusBadRequest, "channel_id is required (the atom ledger is channel/project scoped)")
		return
	}
	workspaceUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}
	hits, err := h.MemoryExploreV2.SearchExternal(r.Context(), workspaceUUID, channelID, request.Query)
	if err != nil {
		writeMemoryExploreV2Error(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspace_id": workspaceID, "hits": hits})
}

type memoryExploreV2EvidenceRequest struct {
	TrajectoryID string                `json:"trajectory_id"`
	Ref          memorygraph.MemoryRef `json:"ref"`
}

// MemoryExploreV2Evidence serves POST /api/workspaces/{id}/memory/explore-v2/evidence:
// the summary-first evidence of one strictly validated ref, authorized from
// the trajectory's persisted plan.
func (h *Handler) MemoryExploreV2Evidence(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	if h.MemoryExploreV2 == nil {
		writeError(w, http.StatusServiceUnavailable, "memory explore v2 is not configured")
		return
	}
	var request memoryExploreV2EvidenceRequest
	if !decodeJSONBody(w, r, &request) {
		return
	}
	if err := memorygraph.ValidateMemoryRef(request.Ref); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	workspaceUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}
	evidence, err := h.MemoryExploreV2.Evidence(r.Context(), workspaceUUID, request.TrajectoryID, request.Ref)
	if err != nil {
		writeMemoryExploreV2Error(w, err)
		return
	}
	writeJSON(w, http.StatusOK, evidence)
}

// MemoryExploreV2History serves GET /api/workspaces/{id}/memory/explore-v2/history?trajectory_id=…:
// the bounded authorized walk of one trajectory.
func (h *Handler) MemoryExploreV2History(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	if h.MemoryExploreV2 == nil {
		writeError(w, http.StatusServiceUnavailable, "memory explore v2 is not configured")
		return
	}
	trajectoryID := r.URL.Query().Get("trajectory_id")
	if trajectoryID == "" {
		writeError(w, http.StatusBadRequest, "trajectory_id is required")
		return
	}
	workspaceUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}
	history, err := h.MemoryExploreV2.History(r.Context(), workspaceUUID, trajectoryID)
	if err != nil {
		writeMemoryExploreV2Error(w, err)
		return
	}
	writeJSON(w, http.StatusOK, history)
}

func writeMemoryExploreV2Error(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrMemoryRouteDisabled):
		writeError(w, http.StatusServiceUnavailable, "memory explore v2 is disabled for this workspace")
	case errors.Is(err, service.ErrMemoryRefUnauthorized),
		errors.Is(err, service.ErrMemorySourceRetracted):
		writeError(w, http.StatusForbidden, "memory ref is not authorized for this trajectory")
	case errors.Is(err, service.ErrMemoryExploreTrajectoryNotFound):
		writeError(w, http.StatusNotFound, "memory explore trajectory not found")
	default:
		writeError(w, http.StatusServiceUnavailable, err.Error())
	}
}
