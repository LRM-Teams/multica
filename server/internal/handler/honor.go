package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type honorSnapshotResponse struct {
	Level     int                     `json:"level"`
	NameStyle string                  `json:"name_style"`
	Badge     *service.HonorBadgeView `json:"equipped_badge,omitempty"`
}

func (h *Handler) GetHonorRules(w http.ResponseWriter, r *http.Request) {
	if h.HonorService == nil {
		writeError(w, http.StatusServiceUnavailable, "honor service unavailable")
		return
	}
	doc, err := h.HonorService.GetRules(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load honor rules")
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

func (h *Handler) GetMyHonor(w http.ResponseWriter, r *http.Request) {
	if h.HonorService == nil {
		writeError(w, http.StatusServiceUnavailable, "honor service unavailable")
		return
	}
	userID := requestUserID(r)
	userUUID, ok := parseUUIDOrBadRequest(w, userID, "user id")
	if !ok {
		return
	}
	user, err := h.Queries.GetUser(r.Context(), userUUID)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load honor")
		return
	}
	dashboard, err := h.HonorService.GetDashboard(r.Context(), user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load honor")
		return
	}
	writeJSON(w, http.StatusOK, dashboard)
}

type patchMyHonorRequest struct {
	EquippedBadgeID  *string  `json:"equipped_badge_id"`
	ShowcaseBadgeIDs []string `json:"showcase_badge_ids"`
}

func (h *Handler) PatchMyHonor(w http.ResponseWriter, r *http.Request) {
	if h.HonorService == nil {
		writeError(w, http.StatusServiceUnavailable, "honor service unavailable")
		return
	}
	userID := requestUserID(r)
	userUUID, ok := parseUUIDOrBadRequest(w, userID, "user id")
	if !ok {
		return
	}
	var req patchMyHonorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.EquippedBadgeID == nil && req.ShowcaseBadgeIDs == nil {
		writeError(w, http.StatusBadRequest, "equipped_badge_id or showcase_badge_ids is required")
		return
	}
	if req.EquippedBadgeID != nil {
		if *req.EquippedBadgeID == "" {
			if err := h.HonorService.ClearEquippedBadge(r.Context(), userUUID); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to update honor")
				return
			}
		} else if err := h.HonorService.SetEquippedBadge(r.Context(), userUUID, *req.EquippedBadgeID); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if req.ShowcaseBadgeIDs != nil {
		if err := h.HonorService.SetShowcaseBadges(r.Context(), userUUID, req.ShowcaseBadgeIDs); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	user, err := h.Queries.GetUser(r.Context(), userUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load honor")
		return
	}
	dashboard, err := h.HonorService.GetDashboard(r.Context(), user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load honor")
		return
	}
	writeJSON(w, http.StatusOK, dashboard)
}

func (h *Handler) PostHonorPresence(w http.ResponseWriter, r *http.Request) {
	if h.HonorService == nil {
		writeError(w, http.StatusServiceUnavailable, "honor service unavailable")
		return
	}
	userID := requestUserID(r)
	userUUID, ok := parseUUIDOrBadRequest(w, userID, "user id")
	if !ok {
		return
	}
	_ = h.HonorService.AwardXP(r.Context(), userUUID, "presence.minute", "")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) GetUserHonor(w http.ResponseWriter, r *http.Request) {
	if h.HonorService == nil {
		writeError(w, http.StatusServiceUnavailable, "honor service unavailable")
		return
	}
	userParam := chi.URLParam(r, "userId")
	userUUID, ok := parseUUIDOrBadRequest(w, userParam, "user id")
	if !ok {
		return
	}
	user, err := h.Queries.GetUser(r.Context(), userUUID)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load honor")
		return
	}
	wall, err := h.HonorService.GetPublicWall(r.Context(), user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load honor")
		return
	}
	writeJSON(w, http.StatusOK, wall)
}

func (h *Handler) GetHonorCompare(w http.ResponseWriter, r *http.Request) {
	if h.HonorService == nil {
		writeError(w, http.StatusServiceUnavailable, "honor service unavailable")
		return
	}
	selfID := requestUserID(r)
	selfUUID, ok := parseUUIDOrBadRequest(w, selfID, "user id")
	if !ok {
		return
	}
	otherParam := r.URL.Query().Get("with")
	if otherParam == "" {
		writeError(w, http.StatusBadRequest, "with query param is required")
		return
	}
	otherUUID, ok := parseUUIDOrBadRequest(w, otherParam, "with user id")
	if !ok {
		return
	}
	selfUser, err := h.Queries.GetUser(r.Context(), selfUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	otherUser, err := h.Queries.GetUser(r.Context(), otherUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "compare user not found")
		return
	}
	result, err := h.HonorService.CompareWithUser(r.Context(), selfUser, otherUser)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to compare honor")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) wireHonorUnlockEvents() {
	if h.HonorService == nil {
		return
	}
	h.HonorService.OnBadgeUnlocked = func(ctx context.Context, evt service.HonorBadgeUnlockEvent) {
		h.publishToUsers(
			protocol.EventHonorBadgeUnlocked,
			"",
			"system",
			"",
			[]string{util.UUIDToString(evt.UserID)},
			map[string]any{
				"user_id":    util.UUIDToString(evt.UserID),
				"badge":      evt.Badge,
				"unlock_pct": evt.UnlockPct,
			},
		)
	}
}

func (h *Handler) honorSnapshotsForUsers(r *http.Request, userIDs []pgtype.UUID) map[string]honorSnapshotResponse {
	if h.HonorService == nil || len(userIDs) == 0 {
		return nil
	}
	snaps, err := h.HonorService.BuildSnapshots(r.Context(), userIDs)
	if err != nil {
		return nil
	}
	out := make(map[string]honorSnapshotResponse, len(snaps))
	for id, snap := range snaps {
		out[id] = honorSnapshotResponse{
			Level:     snap.Level,
			NameStyle: snap.NameStyle,
			Badge:     snap.Badge,
		}
	}
	return out
}

func (h *Handler) awardHonorXP(ctx context.Context, userID pgtype.UUID, actionType, refID string) {
	if h.HonorService == nil || !userID.Valid {
		return
	}
	if err := h.HonorService.AwardXP(ctx, userID, actionType, refID); err != nil {
		// Honor must never block primary product actions.
		return
	}
}

func shouldAwardIssueUpdate(actorType string, changed bool) bool {
	return actorType == "member" && changed
}
