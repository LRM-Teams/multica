package handler

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// MemberPresenceEntry is one human member's live online state (LRM-462).
type MemberPresenceEntry struct {
	UserID   string `json:"user_id"`
	Status   string `json:"status"` // "online" | "offline"
	Observed string `json:"observed_at,omitempty"`
}

// MemberPresenceResponse is the workspace snapshot consumed by FE avatar dots.
type MemberPresenceResponse struct {
	Members []MemberPresenceEntry `json:"members"`
}

// ListMemberPresence returns currently-online human members for a workspace.
// Offline members are omitted (FE treats missing = offline).
func (h *Handler) ListMemberPresence(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	workspaceID := workspaceIDFromURL(r, "id")
	if _, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id"); !ok {
		return
	}
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}

	store := h.memberPresenceStore()
	ids, err := store.OnlineUserIDs(r.Context(), workspaceID)
	if err != nil {
		slog.Warn("list member presence failed", "error", err, "workspace_id", workspaceID)
		writeError(w, http.StatusInternalServerError, "failed to list member presence")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	out := make([]MemberPresenceEntry, 0, len(ids))
	for _, id := range ids {
		out = append(out, MemberPresenceEntry{
			UserID:   id,
			Status:   "online",
			Observed: now,
		})
	}
	writeJSON(w, http.StatusOK, MemberPresenceResponse{Members: out})
}

func (h *Handler) memberPresenceStore() MemberPresenceStore {
	if h != nil && h.MemberPresenceStore != nil {
		return h.MemberPresenceStore
	}
	return NewMemoryMemberPresenceStore()
}

// WireMemberPresenceHooks attaches Hub connect/disconnect/pong callbacks to the
// presence store and publishes member:presence events (LRM-462).
func (h *Handler) WireMemberPresenceHooks() {
	if h == nil || h.Hub == nil {
		return
	}
	h.Hub.SetMemberPresenceCallbacks(
		func(workspaceID, userID string) {
			h.handleMemberPresenceTransition(workspaceID, userID, true)
		},
		func(workspaceID, userID string) {
			h.handleMemberPresenceTransition(workspaceID, userID, false)
		},
	)
	h.Hub.SetMemberPresenceTouchCallback(func(workspaceID, userID string) {
		store := h.memberPresenceStore()
		if err := store.Touch(context.Background(), workspaceID, userID); err != nil {
			slog.Debug("member presence touch failed", "error", err, "workspace_id", workspaceID, "user_id", userID)
		}
	})
}

func (h *Handler) handleMemberPresenceTransition(workspaceID, userID string, online bool) {
	if workspaceID == "" || userID == "" {
		return
	}
	store := h.memberPresenceStore()
	ctx := context.Background()
	var became bool
	var err error
	if online {
		became, err = store.Connect(ctx, workspaceID, userID)
	} else {
		became, err = store.Disconnect(ctx, workspaceID, userID)
	}
	if err != nil {
		slog.Warn("member presence transition failed",
			"error", err, "workspace_id", workspaceID, "user_id", userID, "online", online)
		return
	}
	if !became {
		return
	}
	status := "offline"
	if online {
		status = "online"
	}
	h.publish(protocol.EventMemberPresence, workspaceID, "member", userID, map[string]any{
		"user_id":      userID,
		"status":       status,
		"observed_at":  time.Now().UTC().Format(time.RFC3339),
		"workspace_id": workspaceID,
	})
}
