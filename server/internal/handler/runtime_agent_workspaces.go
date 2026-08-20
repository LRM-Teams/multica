package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/agentworkspace"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const runtimeAgentWorkspacesRPCTimeout = 12 * time.Second

// RuntimeAgentWorkspace is one on-disk agent root under
// `{workspace}/agents/` on a machine (daemon).
type RuntimeAgentWorkspace struct {
	// DirName is the directory basename (normally the agent UUID).
	DirName string `json:"dir_name"`
	// RelPath is relative to the daemon WorkspacesRoot.
	RelPath string `json:"rel_path"`
	// AgentID is set when a matching agent row still exists (including archived).
	AgentID *string `json:"agent_id,omitempty"`
	// AgentName is the display/name when AgentID is set.
	AgentName *string `json:"agent_name,omitempty"`
	// Orphan is true when the agent is missing or archived — UI shows
	// 「agent 已删除」and still allows delete.
	Orphan bool `json:"orphan"`
	// SizeBytes is the directory's own metadata size from the list walk
	// (not a recursive du); 0 when unknown.
	SizeBytes int64 `json:"size_bytes,omitempty"`
}

type RuntimeAgentWorkspacesResponse struct {
	RuntimeID string                  `json:"runtime_id"`
	Status    string                  `json:"status"` // ok | offline | missing | error
	Items     []RuntimeAgentWorkspace `json:"items"`
	Truncated bool                    `json:"truncated,omitempty"`
}

// topLevelAgentDirs keeps only immediate children that are directories.
func topLevelAgentDirs(nodes []protocol.WorkdirFileNode) []protocol.WorkdirFileNode {
	out := make([]protocol.WorkdirFileNode, 0, len(nodes))
	for _, n := range nodes {
		if !n.IsDir {
			continue
		}
		p := strings.Trim(strings.TrimSpace(n.Path), "/")
		if p == "" || strings.Contains(p, "/") {
			continue
		}
		out = append(out, protocol.WorkdirFileNode{Path: p, IsDir: true, Size: n.Size})
	}
	return out
}

func agentWorkspaceOrphan(agent db.Agent, found bool) bool {
	if !found {
		return true
	}
	return agent.ArchivedAt.Valid
}

func agentWorkspaceDisplayName(agent db.Agent) string {
	name := strings.TrimSpace(agent.DisplayName)
	if name != "" {
		return name
	}
	return strings.TrimSpace(agent.Name)
}

// ListRuntimeAgentWorkspaces handles GET /api/runtimes/{runtimeId}/agent-workspaces.
// On-demand scan of `{workspace}/agents/*` on the machine behind the
// runtime (including orphan dirs whose agent was deleted/archived).
func (h *Handler) ListRuntimeAgentWorkspaces(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	runtimeUUID, ok := parseUUIDOrBadRequest(w, runtimeID, "runtime_id")
	if !ok {
		return
	}
	rt, err := h.Queries.GetAgentRuntime(r.Context(), runtimeUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "runtime not found")
		return
	}
	if _, ok := h.requireWorkspaceMember(w, r, uuidToString(rt.WorkspaceID), "runtime not found"); !ok {
		return
	}

	reply := func(status string, items []RuntimeAgentWorkspace, truncated bool) {
		if items == nil {
			items = []RuntimeAgentWorkspace{}
		}
		writeJSON(w, http.StatusOK, RuntimeAgentWorkspacesResponse{
			RuntimeID: runtimeID,
			Status:    status,
			Items:     items,
			Truncated: truncated,
		})
	}

	if h.DaemonHub == nil {
		reply("offline", nil, false)
		return
	}

	workspaceID := uuidToString(rt.WorkspaceID)
	ctx, cancel := context.WithTimeout(r.Context(), runtimeAgentWorkspacesRPCTimeout)
	defer cancel()
	resp, err := h.DaemonHub.RequestWorkdirFiles(ctx, protocol.ListWorkdirFilesRequestPayload{
		RequestID:  uuid.NewString(),
		RuntimeID:  runtimeID,
		RelPath:    agentworkspace.AgentsRelPath(workspaceID),
		MaxDepth:   1,
		MaxEntries: 500,
	})
	if err != nil {
		if errors.Is(err, daemonws.ErrRuntimeOffline) {
			reply("offline", nil, false)
			return
		}
		reply("error", nil, false)
		return
	}
	if resp.Missing {
		reply("missing", nil, false)
		return
	}
	if resp.Error != "" {
		reply("error", nil, false)
		return
	}

	dirs := topLevelAgentDirs(resp.Nodes)
	items := make([]RuntimeAgentWorkspace, 0, len(dirs))
	for _, d := range dirs {
		item := RuntimeAgentWorkspace{
			DirName:   d.Path,
			RelPath:   agentworkspace.RootRelPath(workspaceID, d.Path),
			Orphan:    true,
			SizeBytes: d.Size,
		}
		agentUUID, parseErr := util.ParseUUID(d.Path)
		if parseErr == nil {
			agent, getErr := h.Queries.GetAgent(r.Context(), agentUUID)
			if getErr == nil {
				if uuidToString(agent.WorkspaceID) == workspaceID {
					id := uuidToString(agent.ID)
					name := agentWorkspaceDisplayName(agent)
					item.AgentID = &id
					item.AgentName = &name
					item.Orphan = agentWorkspaceOrphan(agent, true)
				}
			}
		}
		items = append(items, item)
	}
	reply("ok", items, resp.Truncated)
}

// DeleteRuntimeAgentWorkspace handles
// DELETE /api/runtimes/{runtimeId}/agent-workspaces/{dirName}.
// Runtime owner (or workspace owner/admin) only — mirrors canEditRuntime.
func (h *Handler) DeleteRuntimeAgentWorkspace(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	dirName := strings.TrimSpace(chi.URLParam(r, "dirName"))
	runtimeUUID, ok := parseUUIDOrBadRequest(w, runtimeID, "runtime_id")
	if !ok {
		return
	}
	if dirName == "" || strings.ContainsAny(dirName, `/\`) || dirName == "." || dirName == ".." {
		writeError(w, http.StatusBadRequest, "invalid dir_name")
		return
	}
	rt, err := h.Queries.GetAgentRuntime(r.Context(), runtimeUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "runtime not found")
		return
	}
	member, ok := h.requireWorkspaceMember(w, r, uuidToString(rt.WorkspaceID), "runtime not found")
	if !ok {
		return
	}
	rtOwnerID, _ := h.resolveRuntimeOwnerQuery(r.Context(), rt)
	if !canEditRuntime(member, rt, rtOwnerID) {
		writeError(w, http.StatusForbidden, "you can only delete workspaces on your own runtimes")
		return
	}
	if h.DaemonHub == nil {
		writeError(w, http.StatusServiceUnavailable, "runtime offline")
		return
	}

	workspaceID := uuidToString(rt.WorkspaceID)
	ctx, cancel := context.WithTimeout(r.Context(), runtimeAgentWorkspacesRPCTimeout)
	defer cancel()
	resp, err := h.DaemonHub.RequestDeleteDir(ctx, protocol.DeleteWorkdirDirRequestPayload{
		RequestID: uuid.NewString(),
		RuntimeID: runtimeID,
		RelPath:   agentworkspace.RootRelPath(workspaceID, dirName),
	})
	if err != nil {
		if errors.Is(err, daemonws.ErrRuntimeOffline) {
			writeError(w, http.StatusServiceUnavailable, "runtime offline")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete workspace")
		return
	}
	if resp.Error != "" {
		writeError(w, http.StatusBadRequest, resp.Error)
		return
	}
	// Missing is idempotent success — directory already gone.
	w.WriteHeader(http.StatusNoContent)
}
