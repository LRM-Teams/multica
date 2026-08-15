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

// Graph memory reviewer profile (design §1 reviewer.type, adjustment A4):
// per-workspace reviewer configuration that overrides the process-level
// MULTICA_REVIEWER_TYPE env default. One row per workspace; an absent row
// means "no workspace override" and the env default applies.

const defaultGraphReviewerType = "legacy"

type graphMemoryProfileResponse struct {
	WorkspaceID      string `json:"workspace_id"`
	ReviewerType     string `json:"reviewer_type"`
	ExploreAgents    int32  `json:"explore_agents"`
	ExploreMaxRounds int32  `json:"explore_max_rounds"`
	UpdatedAt        string `json:"updated_at,omitempty"`
}

type updateGraphMemoryProfileRequest struct {
	ReviewerType     string `json:"reviewer_type"`
	ExploreAgents    int32  `json:"explore_agents"`
	ExploreMaxRounds int32  `json:"explore_max_rounds"`
}

func validGraphReviewerType(t string) bool {
	return t == "legacy" || t == "graph"
}

func graphMemoryProfileFromRow(p db.GraphMemoryProfile) graphMemoryProfileResponse {
	resp := graphMemoryProfileResponse{
		WorkspaceID:      uuidToString(p.WorkspaceID),
		ReviewerType:     p.ReviewerType,
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
		ReviewerType:     defaultGraphReviewerType,
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
		writeError(w, http.StatusForbidden, "only workspace owner/admin can configure the graph memory reviewer")
		return
	}

	var req updateGraphMemoryProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.ReviewerType = strings.ToLower(strings.TrimSpace(req.ReviewerType))
	if req.ReviewerType == "" {
		req.ReviewerType = defaultGraphReviewerType
	}
	if !validGraphReviewerType(req.ReviewerType) {
		writeError(w, http.StatusBadRequest, "reviewer_type must be 'legacy' or 'graph'")
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
		ReviewerType:     req.ReviewerType,
		ExploreAgents:    req.ExploreAgents,
		ExploreMaxRounds: req.ExploreMaxRounds,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save graph memory profile")
		return
	}
	writeJSON(w, http.StatusOK, graphMemoryProfileFromRow(profile))
}

// graphMemoryReviewerTypeForWorkspace resolves the effective reviewer type
// for a workspace: the per-workspace profile when one exists, else empty
// (callers fall back to the process env default). A lookup error fails open
// to "" so a transient DB hiccup never flips a workspace's memory pipeline.
func (h *Handler) graphMemoryReviewerTypeForWorkspace(ctx context.Context, workspaceID pgtype.UUID) string {
	profile, err := h.Queries.GetGraphMemoryProfile(ctx, workspaceID)
	if err != nil {
		return ""
	}
	if !validGraphReviewerType(profile.ReviewerType) {
		return ""
	}
	return profile.ReviewerType
}
