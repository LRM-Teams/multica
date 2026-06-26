package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Channel ↔ project binding lives in its own file so it doesn't collide with
// the actively-developed channel.go. The binding is read at task claim time via
// COALESCE(channel.project_id, chat_session.project_id) (see ClaimTaskByRuntime),
// so setting it here makes every agent in the channel share the project's
// directory without touching individual chat sessions.

// GetChannelProject returns the channel's bound project id (empty when unbound).
func (h *Handler) GetChannelProject(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	var pid pgtype.UUID
	_ = h.DB.QueryRow(r.Context(), `SELECT project_id FROM channel WHERE id = $1`, channelID).Scan(&pid)
	out := ""
	if pid.Valid {
		out = uuidToString(pid)
	}
	writeJSON(w, http.StatusOK, map[string]string{"project_id": out})
}

// resolveProjectWorkdirRuntime finds the online runtime that hosts a project's
// managed working directory.
//
// The managed workdir lives on whichever daemon provisioned it. On a shared /
// cloud runtime that daemon is NOT owned by the viewer, so resolving "the
// viewer's own runtime" (the old behavior) wrongly reports offline. We instead
// resolve the daemon recorded on the project's managed local_directory resource
// and look up its online runtime, falling back to the viewer's own daemon for
// the single-user local model. Returns ok=false when no reachable online
// runtime exists.
func (h *Handler) resolveProjectWorkdirRuntime(ctx context.Context, workspaceID, userID, projectID string) (pgtype.UUID, bool) {
	var runtimeID pgtype.UUID
	if h.DaemonHub == nil {
		return runtimeID, false
	}
	wsID := parseUUID(workspaceID)

	// Primary: the daemon that registered the project's managed workdir. This
	// is where the files actually are — including a shared/public runtime.
	var daemonID string
	_ = h.DB.QueryRow(ctx, `
		SELECT resource_ref->>'daemon_id'
		FROM project_resource
		WHERE project_id = $1 AND resource_type = 'local_directory'
		  AND COALESCE((resource_ref->>'managed')::boolean, false) = true
		  AND resource_ref->>'daemon_id' IS NOT NULL
		ORDER BY created_at DESC
		LIMIT 1`, parseUUID(projectID)).Scan(&daemonID)
	if daemonID != "" {
		if err := h.DB.QueryRow(ctx, `
			SELECT id FROM agent_runtime
			WHERE workspace_id = $1 AND daemon_id = $2 AND status = 'online'
			ORDER BY last_seen_at DESC NULLS LAST
			LIMIT 1`, wsID, daemonID).Scan(&runtimeID); err == nil && runtimeID.Valid {
			return runtimeID, true
		}
	}

	// Fallback: the viewer's own online runtime (local single-daemon model,
	// where each user keeps their own copy of the managed workdir).
	if err := h.DB.QueryRow(ctx, `
		SELECT id FROM agent_runtime
		WHERE workspace_id = $1 AND owner_id = $2 AND status = 'online'
		ORDER BY last_seen_at DESC NULLS LAST
		LIMIT 1`, wsID, parseUUID(userID)).Scan(&runtimeID); err == nil && runtimeID.Valid {
		return runtimeID, true
	}
	return runtimeID, false
}

// ChannelProjectFilesResponse is the file-tree listing for a channel's bound
// project workdir. Status tells the frontend which empty/error state to show:
//   ok | no_project | offline (no online daemon for the viewer) | missing
//   (the project workdir doesn't exist on that daemon yet) | error
type ChannelProjectFilesResponse struct {
	ProjectID string                     `json:"project_id"`
	Status    string                     `json:"status"`
	Nodes     []protocol.WorkdirFileNode `json:"nodes"`
	Truncated bool                       `json:"truncated"`
}

// ListChannelProjectFiles returns the file tree of the channel's bound project
// working directory. The workdir lives on a daemon machine, so this fans out a
// list-files RPC to the requesting user's own online runtime (each daemon keeps
// its own copy of a managed workdir) and returns whatever that daemon reports.
func (h *Handler) ListChannelProjectFiles(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}

	reply := func(status string, nodes []protocol.WorkdirFileNode, truncated bool, projectID string) {
		if nodes == nil {
			nodes = []protocol.WorkdirFileNode{}
		}
		writeJSON(w, http.StatusOK, ChannelProjectFilesResponse{ProjectID: projectID, Status: status, Nodes: nodes, Truncated: truncated})
	}

	var pid pgtype.UUID
	_ = h.DB.QueryRow(r.Context(), `SELECT project_id FROM channel WHERE id = $1 AND workspace_id = $2`,
		channelID, parseUUID(workspaceID)).Scan(&pid)
	if !pid.Valid {
		reply("no_project", nil, false, "")
		return
	}
	projectID := uuidToString(pid)

	// GitHub-backed projects: read the file tree read-only from the bound repo
	// via the workspace's GitHub App token. Decoupled from any runtime and from
	// where agents execute, and automatic for every github_repo project.
	if owner, repo, okGH := h.projectGitHubRepo(r.Context(), projectID); okGH {
		ghCtx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		nodes, truncated, status := h.githubProjectFiles(ghCtx, parseUUID(workspaceID), owner, repo)
		reply(status, nodes, truncated, projectID)
		return
	}

	// Otherwise: resolve the runtime that hosts the managed workdir (the shared/
	// cloud daemon on the server, the viewer's own daemon locally).
	runtimeID, ok := h.resolveProjectWorkdirRuntime(r.Context(), workspaceID, userID, projectID)
	if !ok {
		reply("offline", nil, false, projectID)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	resp, err := h.DaemonHub.RequestWorkdirFiles(ctx, protocol.ListWorkdirFilesRequestPayload{
		RequestID: uuid.NewString(),
		RuntimeID: uuidToString(runtimeID),
		RelPath:   managedWorkdirRelPath(projectID),
	})
	if err != nil {
		if errors.Is(err, daemonws.ErrRuntimeOffline) {
			reply("offline", nil, false, projectID)
			return
		}
		reply("error", nil, false, projectID)
		return
	}
	if resp.Missing {
		reply("missing", nil, false, projectID)
		return
	}
	if resp.Error != "" {
		reply("error", nil, false, projectID)
		return
	}
	reply("ok", resp.Nodes, resp.Truncated, projectID)
}

// ChannelProjectFileContentResponse is a single file's preview content. For
// text files Content is UTF-8 (encoding empty); for media (image/audio/video/
// pdf) Content is base64 with Encoding "base64" and MimeType set so the client
// renders it directly.
type ChannelProjectFileContentResponse struct {
	Content   string `json:"content"`
	Encoding  string `json:"encoding"`
	MimeType  string `json:"mime_type"`
	Truncated bool   `json:"truncated"`
	TooLarge  bool   `json:"too_large"`
	Binary    bool   `json:"binary"`
}

// GetChannelProjectFile streams one file's text content from the channel's
// bound project workdir (on the viewer's online daemon) for preview. The
// daemon confines `path` to the workdir, caps the size, and flags binary files.
func (h *Handler) GetChannelProjectFile(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	filePath := strings.TrimSpace(r.URL.Query().Get("path"))
	if filePath == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}

	var pid pgtype.UUID
	_ = h.DB.QueryRow(r.Context(), `SELECT project_id FROM channel WHERE id = $1 AND workspace_id = $2`,
		channelID, parseUUID(workspaceID)).Scan(&pid)
	if !pid.Valid {
		writeError(w, http.StatusNotFound, "channel has no project")
		return
	}
	projectID := uuidToString(pid)

	// GitHub-backed projects: read the file content from the bound repo.
	if owner, repo, okGH := h.projectGitHubRepo(r.Context(), projectID); okGH {
		ghCtx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		out, found := h.githubProjectFileContent(ghCtx, parseUUID(workspaceID), owner, repo, filePath)
		if !found {
			writeError(w, http.StatusNotFound, "file not found")
			return
		}
		writeJSON(w, http.StatusOK, out)
		return
	}

	runtimeID, ok := h.resolveProjectWorkdirRuntime(r.Context(), workspaceID, userID, projectID)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "runtime offline")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	resp, err := h.DaemonHub.RequestReadFile(ctx, protocol.ReadWorkdirFileRequestPayload{
		RequestID: uuid.NewString(),
		RuntimeID: uuidToString(runtimeID),
		RelPath:   managedWorkdirRelPath(projectID),
		FilePath:  filePath,
	})
	if err != nil {
		if errors.Is(err, daemonws.ErrRuntimeOffline) {
			writeError(w, http.StatusServiceUnavailable, "runtime offline")
			return
		}
		writeError(w, http.StatusGatewayTimeout, "failed to read file")
		return
	}
	if resp.Missing {
		writeError(w, http.StatusNotFound, "file not found")
		return
	}
	if resp.Error != "" {
		writeError(w, http.StatusBadGateway, "failed to read file")
		return
	}
	writeJSON(w, http.StatusOK, ChannelProjectFileContentResponse{
		Content:   resp.Content,
		Encoding:  resp.Encoding,
		MimeType:  resp.MimeType,
		Truncated: resp.Truncated,
		TooLarge:  resp.TooLarge,
		Binary:    resp.Binary,
	})
}

type setChannelProjectRequest struct {
	// ProjectID: a uuid binds/switches; null or "" clears.
	ProjectID json.RawMessage `json:"project_id"`
}

// SetChannelProject binds, switches, or clears the channel's project.
func (h *Handler) SetChannelProject(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	var req setChannelProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var projectID pgtype.UUID // zero/invalid → clears the binding (NULL)
	if len(req.ProjectID) > 0 && string(req.ProjectID) != "null" {
		var raw string
		if err := json.Unmarshal(req.ProjectID, &raw); err != nil {
			writeError(w, http.StatusBadRequest, "invalid project_id")
			return
		}
		if raw = strings.TrimSpace(raw); raw != "" {
			pid, perr := util.ParseUUID(raw)
			if perr != nil {
				writeError(w, http.StatusBadRequest, "invalid project_id")
				return
			}
			proj, gerr := h.Queries.GetProject(r.Context(), pid)
			if gerr != nil || uuidToString(proj.WorkspaceID) != workspaceID {
				writeError(w, http.StatusBadRequest, "project not found")
				return
			}
			projectID = pid
		}
	}
	if _, err := h.DB.Exec(r.Context(),
		`UPDATE channel SET project_id = $2 WHERE id = $1`,
		channelID, projectID,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update channel project")
		return
	}
	out := ""
	if projectID.Valid {
		out = uuidToString(projectID)
	}
	writeJSON(w, http.StatusOK, map[string]string{"project_id": out})
}
