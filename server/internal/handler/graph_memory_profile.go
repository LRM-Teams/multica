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
	"github.com/multica-ai/multica/server/pkg/protocol"
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
	MemoryType        string `json:"memory_type"`
	ExploreAgents     int32  `json:"explore_agents"`
	ExploreMaxRounds  int32  `json:"explore_max_rounds"`
	ConfirmEmptyStart bool   `json:"confirm_empty_start"`
}

func validGraphMemoryType(t string) bool {
	return t == "legacy" || t == "graph"
}

func graphMemoryProfileFromRow(workspaceID pgtype.UUID, memoryType string, exploreAgents, exploreMaxRounds int32, updatedAt pgtype.Timestamptz) graphMemoryProfileResponse {
	resp := graphMemoryProfileResponse{
		WorkspaceID:      uuidToString(workspaceID),
		MemoryType:       memoryType,
		ExploreAgents:    exploreAgents,
		ExploreMaxRounds: exploreMaxRounds,
	}
	if updatedAt.Valid {
		resp.UpdatedAt = updatedAt.Time.UTC().Format(time.RFC3339)
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
	writeJSON(w, http.StatusOK, graphMemoryProfileFromRow(profile.WorkspaceID, profile.MemoryType, profile.ExploreAgents, profile.ExploreMaxRounds, profile.UpdatedAt))
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

	// Switching TO graph requires explicit admin confirmation of the
	// empty-start and no-fallback contract (spec §11). Knob updates and
	// graph->legacy switches do not.
	if req.MemoryType == "graph" && !req.ConfirmEmptyStart {
		current, err := h.Queries.GetGraphMemoryProfile(r.Context(), parseUUID(workspaceID))
		if err != nil || current.MemoryType != "graph" {
			writeError(w, http.StatusBadRequest, "confirm_empty_start_required: switching to graph memory starts with empty graphs and never falls back to legacy project/channel/daily memory; resend with confirm_empty_start=true")
			return
		}
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
	writeJSON(w, http.StatusOK, graphMemoryProfileFromRow(profile.WorkspaceID, profile.MemoryType, profile.ExploreAgents, profile.ExploreMaxRounds, profile.UpdatedAt))
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
// applyGraphMemoryProfileToDelivery stamps the workspace's effective graph
// memory profile onto an outgoing Agent delivery (spec §10): the daemon
// caches it per workspace for the resident-message memory path. A workspace
// without a profile row leaves the payload untouched (daemon env defaults
// apply).
func (h *Handler) applyGraphMemoryProfileToDelivery(ctx context.Context, workspaceID string, delivery *protocol.AgentDeliverPayload) {
	if delivery == nil {
		return
	}
	if profile := h.graphMemoryProfileForWorkspace(ctx, parseUUID(workspaceID)); profile.memoryType != "" {
		delivery.MemoryType = profile.memoryType
		delivery.ExploreAgents = int(profile.exploreAgents)
		delivery.ExploreMaxRounds = int(profile.exploreMaxRounds)
	}
}

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
