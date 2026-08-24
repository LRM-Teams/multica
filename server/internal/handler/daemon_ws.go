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
		// A WorkspaceDaemon is deliberately not a runtime registration: it
		// remains connected while a workspace has no provider runtime at all.
		// Its workspace must nevertheless be the exact authenticated daemon
		// token scope; accepting an arbitrary query value here would turn the
		// Runner into a cross-workspace command channel.
		workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
		if workspaceID == "" || workspaceID != middleware.DaemonWorkspaceIDFromContext(r.Context()) {
			writeError(w, http.StatusNotFound, "workspace not found")
			return
		}
		if err := agent.CheckCLIVersionAtLeast(r.Header.Get("X-Client-Version"), MinWorkspaceDaemonProtocolCLIVersion); err != nil {
			writeJSON(w, http.StatusUpgradeRequired, map[string]any{"code": "workspace_daemon_protocol_unsupported", "min_version": MinWorkspaceDaemonProtocolCLIVersion})
			return
		}
		h.DaemonHub.HandleWebSocket(w, r, daemonws.ClientIdentity{
			DaemonID:      middleware.DaemonIDFromContext(r.Context()),
			UserID:        requestUserID(r),
			WorkspaceID:   workspaceID,
			ClientVersion: r.Header.Get("X-Client-Version"),
		})
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
