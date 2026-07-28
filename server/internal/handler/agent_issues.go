package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Agent issue data-plane (#801).
//
// Dedicated /api/agent/issues/* routes require AgentPrincipal. Shared issue
// loaders called from these paths use principal.WorkspaceID (never owner
// channel/workspace membership for surface ACL). Human URLs remain fail-closed
// via rejectAgentOnHumanRoute.

func (h *Handler) GetAgentIssue(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.GetIssue(w, r)
}

func (h *Handler) CreateAgentIssue(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	// CreateIssue uses resolveActor → agent creator under mat_* auth.
	h.CreateIssue(w, r)
}

func (h *Handler) UpdateAgentIssue(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.UpdateIssue(w, r)
}

func (h *Handler) ListAgentIssueComments(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.ListComments(w, r)
}

func (h *Handler) CreateAgentIssueComment(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.CreateComment(w, r)
}

func (h *Handler) ListAgentIssues(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.ListIssues(w, r)
}

func (h *Handler) ListAgentIssueMetadata(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.ListIssueMetadata(w, r)
}

func (h *Handler) SetAgentIssueMetadataKey(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.SetIssueMetadataKey(w, r)
}

func (h *Handler) DeleteAgentIssueMetadataKey(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.DeleteIssueMetadataKey(w, r)
}

// --- necessary batch: labels / subscribers / runs / channel ---

func (h *Handler) ListAgentIssueLabels(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	// ListLabelsForIssue uses loadIssueForUser → principal workspace under /api/agent/*.
	h.ListLabelsForIssue(w, r)
}

func (h *Handler) AttachAgentIssueLabel(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	h.mutateAgentIssueLabel(w, r, p, true)
}

func (h *Handler) DetachAgentIssueLabel(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	h.mutateAgentIssueLabel(w, r, p, false)
}

// mutateAgentIssueLabel attaches (attach=true) or detaches a label without
// requireUserID/owner actor. Audit/publish actor is the agent principal.
func (h *Handler) mutateAgentIssueLabel(w http.ResponseWriter, r *http.Request, p middleware.AgentPrincipal, attach bool) {
	issueID := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForAgent(w, r, p, issueID)
	if !ok {
		return
	}
	agentID, aok := p.AgentUUID()
	if !aok {
		writeError(w, http.StatusForbidden, "access denied")
		return
	}

	var labelUUID pgtype.UUID
	if attach {
		var req AttachLabelRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if req.LabelID == "" {
			writeError(w, http.StatusBadRequest, "label_id is required")
			return
		}
		var okParse bool
		labelUUID, okParse = parseUUIDOrBadRequest(w, req.LabelID, "label_id")
		if !okParse {
			return
		}
	} else {
		var okParse bool
		labelUUID, okParse = parseUUIDOrBadRequest(w, chi.URLParam(r, "labelId"), "label id")
		if !okParse {
			return
		}
	}

	if _, err := h.Queries.GetLabel(r.Context(), db.GetLabelParams{
		ID: labelUUID, WorkspaceID: issue.WorkspaceID,
	}); err != nil {
		writeError(w, http.StatusNotFound, "label not found")
		return
	}

	if attach {
		if err := h.Queries.AttachLabelToIssue(r.Context(), db.AttachLabelToIssueParams{
			IssueID:     issue.ID,
			LabelID:     labelUUID,
			WorkspaceID: issue.WorkspaceID,
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to attach label")
			return
		}
	} else {
		if err := h.Queries.DetachLabelFromIssue(r.Context(), db.DetachLabelFromIssueParams{
			IssueID:     issue.ID,
			LabelID:     labelUUID,
			WorkspaceID: issue.WorkspaceID,
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to detach label")
			return
		}
	}

	labels, ok2 := h.listLabelsForIssueSafe(r, issue.ID, issue.WorkspaceID)
	if !ok2 {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	resp := labelsToResponse(labels)
	h.publish(protocol.EventIssueLabelsChanged, uuidToString(issue.WorkspaceID), "agent", uuidToString(agentID), map[string]any{
		"issue_id": uuidToString(issue.ID),
		"labels":   resp,
	})
	writeJSON(w, http.StatusOK, map[string]any{"labels": resp})
}

func (h *Handler) ListAgentIssueSubscribers(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.ListIssueSubscribers(w, r)
}

func (h *Handler) SubscribeAgentToIssue(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.SubscribeToIssue(w, r)
}

func (h *Handler) UnsubscribeAgentFromIssue(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.UnsubscribeFromIssue(w, r)
}

func (h *Handler) ListAgentIssueTaskRuns(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.ListTasksByIssue(w, r)
}

func (h *Handler) RerunAgentIssue(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.RerunIssue(w, r)
}

func (h *Handler) SetAgentIssueSourceChannel(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.SetIssueSourceChannel(w, r)
}

// AgentDirectoryItem is the narrow directory DTO for agent @-resolve (#801).
// Never expose Instructions/RuntimeConfig/skills/mcp secrets on this surface.
type AgentDirectoryItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
}

// ListAgentDirectoryAgents — GET /api/agent/agents
// Principal-native narrow directory. Never calls workspaceMember(owner).
func (h *Handler) ListAgentDirectoryAgents(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	ws, wok := p.WorkspaceUUID()
	selfID, aok := p.AgentUUID()
	if !wok || !aok {
		writeError(w, http.StatusForbidden, "access denied")
		return
	}

	agents, err := h.Queries.ListAgents(r.Context(), ws)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list agents")
		return
	}
	groupManagers, gmErr := h.groupManagerAgentIDs(r.Context(), ws)
	if gmErr != nil {
		groupManagers = map[string]bool{}
	}
	listChannelID := strings.TrimSpace(r.URL.Query().Get("channel_id"))
	if listChannelID != "" {
		if _, ok := parseUUIDOrBadRequest(w, listChannelID, "channel_id"); !ok {
			return
		}
	}
	agentIDs := make([]pgtype.UUID, 0, len(agents))
	for _, a := range agents {
		agentIDs = append(agentIDs, a.ID)
	}
	homeByAgent := h.loadAgentHomeChannelIDs(r.Context(), agentIDs)
	visible := make([]AgentDirectoryItem, 0, len(agents))
	for _, a := range agents {
		if groupManagers[uuidToString(a.ID)] {
			continue
		}
		homeID := homeByAgent[uuidToString(a.ID)]
		if !agentVisibleInChannelContext(a.Visibility, homeID, listChannelID) {
			continue
		}
		// Public agents + self only — never other private agents via owner role.
		if (a.Visibility == "private" || privateAgentOwnerOnly(a)) && uuidToString(a.ID) != uuidToString(selfID) {
			continue
		}
		item := AgentDirectoryItem{
			ID:   uuidToString(a.ID),
			Name: a.Name,
		}
		if dn := strings.TrimSpace(a.DisplayName); dn != "" {
			item.DisplayName = dn
		}
		visible = append(visible, item)
	}
	writeJSON(w, http.StatusOK, visible)
}
