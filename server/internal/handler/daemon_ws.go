package handler

import (
	"net/http"
	"strings"

	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/pkg/agent"
)

// MinWorkspaceDaemonProtocolCLIVersion is the first release line for the
// coordinated WorkspaceDaemon Attachment hard cut. Ready validation also
// requires the Attachment capability, which fences prerelease builds from the
// same release line that predate the cut.
const MinWorkspaceDaemonProtocolCLIVersion = "0.4.24"

func (h *Handler) DaemonWebSocket(w http.ResponseWriter, r *http.Request) {
	if h.DaemonHub == nil {
		writeError(w, http.StatusServiceUnavailable, "daemon websocket unavailable")
		return
	}

	runtimeIDs := parseRuntimeIDs(r)
	if len(runtimeIDs) == 0 {
		// TODO(workspace-daemon-connect): Remove this WorkspaceDaemon branch
		// only after the minimum supported Computer version connects through
		// protocol.WorkspaceDaemonConnectPath and fleet upgrade coverage proves
		// no released Computer still needs this path to receive computer:upgrade.
		// Upgrade bridge: released Computers only know
		// /api/daemon/connect?workspace_id=... and must remain reachable long
		// enough to receive computer:upgrade. New Computers use the dedicated
		// WorkspaceDaemonConnectPath instead.
		workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
		if workspaceID == "" || workspaceID != middleware.DaemonWorkspaceIDFromContext(r.Context()) {
			writeError(w, http.StatusNotFound, "workspace not found")
			return
		}
		h.serveWorkspaceDaemonWebSocket(w, r, workspaceID, "workspace_runner_protocol_unsupported")
		return
	}

	for _, runtimeID := range runtimeIDs {
		rt, ok := h.requireDaemonRuntimeAccess(w, r, runtimeID)
		if !ok {
			return
		}
		if daemonID := middleware.DaemonIDFromContext(r.Context()); daemonID != "" && rt.DaemonID.Valid && rt.DaemonID.String != daemonID {
			writeError(w, http.StatusNotFound, "runtime not found")
			return
		}
	}

	h.DaemonHub.HandleWebSocket(w, r, daemonws.ClientIdentity{
		DaemonID:      middleware.DaemonIDFromContext(r.Context()),
		UserID:        requestUserID(r),
		WorkspaceID:   middleware.DaemonWorkspaceIDFromContext(r.Context()),
		RuntimeIDs:    runtimeIDs,
		ClientVersion: r.Header.Get("X-Client-Version"),
	})
}

// WorkspaceDaemonWebSocket is the single server connection owned by one
// WorkspaceDaemonCore. It is deliberately separate from the legacy
// runtime/task transport even though both authenticate with a workspace-scoped
// daemon token.
func (h *Handler) WorkspaceDaemonWebSocket(w http.ResponseWriter, r *http.Request) {
	if h.DaemonHub == nil {
		writeError(w, http.StatusServiceUnavailable, "workspace daemon websocket unavailable")
		return
	}
	workspaceID := strings.TrimSpace(middleware.DaemonWorkspaceIDFromContext(r.Context()))
	if workspaceID == "" {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	h.serveWorkspaceDaemonWebSocket(w, r, workspaceID, "workspace_daemon_protocol_unsupported")
}

func (h *Handler) serveWorkspaceDaemonWebSocket(w http.ResponseWriter, r *http.Request, workspaceID, unsupportedCode string) {
	if err := agent.CheckCLIVersionAtLeast(r.Header.Get("X-Client-Version"), MinWorkspaceDaemonProtocolCLIVersion); err != nil {
		writeJSON(w, http.StatusUpgradeRequired, map[string]any{"code": unsupportedCode, "min_version": MinWorkspaceDaemonProtocolCLIVersion})
		return
	}
	h.DaemonHub.HandleWebSocket(w, r, daemonws.ClientIdentity{
		DaemonID:      middleware.DaemonIDFromContext(r.Context()),
		UserID:        requestUserID(r),
		WorkspaceID:   workspaceID,
		ClientVersion: r.Header.Get("X-Client-Version"),
	})
}

func parseRuntimeIDs(r *http.Request) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(raw string) {
		for _, part := range strings.Split(raw, ",") {
			id := strings.TrimSpace(part)
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	for _, raw := range r.URL.Query()["runtime_id"] {
		add(raw)
	}
	for _, raw := range r.URL.Query()["runtime_ids"] {
		add(raw)
	}
	return out
}
