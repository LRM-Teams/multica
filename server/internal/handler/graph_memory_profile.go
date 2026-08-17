package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Graph memory reviewer profile (design §1 memory_type, adjustment A4):
// per-workspace reviewer configuration that overrides the process-level
// MULTICA_MEMORY_TYPE env default. One row per workspace; an absent row
// means "no workspace override" and the env default applies.

const defaultGraphMemoryType = "legacy"

type graphMemoryProfileResponse struct {
	WorkspaceID      string `json:"workspace_id"`
	MemoryType       string `json:"memory_type"`
	ExploreAgents    int32  `json:"explore_agents"`
	ExploreMaxRounds int32  `json:"explore_max_rounds"`
	UpdatedAt        string `json:"updated_at,omitempty"`
}

type updateGraphMemoryProfileRequest struct {
	MemoryType       string `json:"memory_type"`
	ExploreAgents    int32  `json:"explore_agents"`
	ExploreMaxRounds int32  `json:"explore_max_rounds"`
}

func validGraphMemoryType(t string) bool {
	return t == "legacy" || t == "graph"
}

func graphMemoryProfileFromRow(p db.GraphMemoryProfile) graphMemoryProfileResponse {
	resp := graphMemoryProfileResponse{
		WorkspaceID:      uuidToString(p.WorkspaceID),
		MemoryType:       p.MemoryType,
		ExploreAgents:    p.ExploreAgents,
		ExploreMaxRounds: p.ExploreMaxRounds,
	}
	if p.UpdatedAt.Valid {
		resp.UpdatedAt = p.UpdatedAt.Time.UTC().Format(time.RFC3339)
	}
	return resp
}

func defaultGraphMemoryProfile(workspaceID string) graphMemoryProfileResponse {
	return graphMemoryProfileResponse{
		WorkspaceID:      workspaceID,
		MemoryType:       defaultGraphMemoryType,
		ExploreAgents:    4,
		ExploreMaxRounds: 3,
	}
}

func (h *Handler) GetGraphMemoryProfile(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	profile, err := h.Queries.GetGraphMemoryProfile(r.Context(), parseUUID(workspaceID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusOK, defaultGraphMemoryProfile(workspaceID))
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load graph memory profile")
		return
	}
	writeJSON(w, http.StatusOK, graphMemoryProfileFromRow(profile))
}

func (h *Handler) UpdateGraphMemoryProfile(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}
	if !roleAllowed(member.Role, "owner", "admin") {
		writeError(w, http.StatusForbidden, "only workspace owner/admin can configure the graph memory profile")
		return
	}

	var req updateGraphMemoryProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.MemoryType = strings.ToLower(strings.TrimSpace(req.MemoryType))
	if req.MemoryType == "" {
		req.MemoryType = defaultGraphMemoryType
	}
	if !validGraphMemoryType(req.MemoryType) {
		writeError(w, http.StatusBadRequest, "memory_type must be 'legacy' or 'graph'")
		return
	}
	if req.ExploreAgents < 1 || req.ExploreAgents > 16 {
		writeError(w, http.StatusBadRequest, "explore_agents must be between 1 and 16")
		return
	}
	if req.ExploreMaxRounds < 1 || req.ExploreMaxRounds > 20 {
		writeError(w, http.StatusBadRequest, "explore_max_rounds must be between 1 and 20")
		return
	}

	profile, err := h.Queries.UpsertGraphMemoryProfile(r.Context(), db.UpsertGraphMemoryProfileParams{
		WorkspaceID:      parseUUID(workspaceID),
		MemoryType:       req.MemoryType,
		ExploreAgents:    req.ExploreAgents,
		ExploreMaxRounds: req.ExploreMaxRounds,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save graph memory profile")
		return
	}
	writeJSON(w, http.StatusOK, graphMemoryProfileFromRow(profile))
}

// graphMemoryProfileValues is the effective per-workspace graph memory
// profile delivered to runtimes. A zero value means "no workspace profile":
// every field then falls back to the process env default (spec §10).
type graphMemoryProfileValues struct {
	memoryType       string
	exploreAgents    int32
	exploreMaxRounds int32
}

// graphMemoryProfileForWorkspace loads the workspace's profile row. Lookup
// errors fail open to the zero value so a transient DB hiccup never flips a
// workspace's memory pipeline.
func (h *Handler) graphMemoryProfileForWorkspace(ctx context.Context, workspaceID pgtype.UUID) graphMemoryProfileValues {
	profile, err := h.Queries.GetGraphMemoryProfile(ctx, workspaceID)
	if err != nil || !validGraphMemoryType(profile.MemoryType) {
		return graphMemoryProfileValues{}
	}
	return graphMemoryProfileValues{
		memoryType:       profile.MemoryType,
		exploreAgents:    profile.ExploreAgents,
		exploreMaxRounds: profile.ExploreMaxRounds,
	}
}
