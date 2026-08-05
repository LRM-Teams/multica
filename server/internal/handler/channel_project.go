package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Channel ↔ project binding lives in its own file so it doesn't collide with
// the actively-developed channel.go. The binding is read at task claim time via
// COALESCE(channel.project_id, chat_session.project_id) (see inbox drain),
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

// ChannelProjectFilesResponse is the file-tree listing for a channel's bound
// project workdir. Status tells the frontend which empty/error state to show:
//
//	ok | no_project | offline (no online daemon for the viewer) | missing
//	(the project workdir doesn't exist on that daemon yet) | error
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

	// Project resources are metadata only. Multica does not provision a
	// shared workdir or inspect an Agent's private workspace for this view.
	reply("offline", nil, false, projectID)
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

	writeError(w, http.StatusServiceUnavailable, "project files are not managed by Multica")
}

type setChannelProjectRequest struct {
	// ProjectID: a uuid binds/switches; null or "" clears.
	ProjectID json.RawMessage `json:"project_id"`
}

// parseChannelProjectBinding validates the nullable channel.project_id payload
// shared by channel creation and the group-settings endpoint.
func (h *Handler) parseChannelProjectBinding(w http.ResponseWriter, r *http.Request, workspaceID string, raw json.RawMessage) (pgtype.UUID, bool) {
	var projectID pgtype.UUID // invalid means no project binding
	if len(raw) == 0 || string(raw) == "null" {
		return projectID, true
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		writeError(w, http.StatusBadRequest, "invalid project_id")
		return projectID, false
	}
	if value = strings.TrimSpace(value); value == "" {
		return projectID, true
	}
	parsed, err := util.ParseUUID(value)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project_id")
		return projectID, false
	}
	project, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{
		ID:          parsed,
		WorkspaceID: parseUUID(workspaceID),
	})
	if err != nil || !project.ID.Valid {
		writeError(w, http.StatusBadRequest, "project not found")
		return projectID, false
	}
	return parsed, true
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
	if !h.requireChannelManager(w, r, workspaceID, channelID, parseUUID(userID)) {
		return
	}
	if !h.requireChannelWritable(w, r.Context(), workspaceID, channelID) {
		return
	}
	if !h.requireChannelNotSystem(w, r.Context(), workspaceID, channelID) {
		return
	}
	var req setChannelProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.ProjectID) == 0 {
		writeError(w, http.StatusBadRequest, "project_id is required; use null to clear")
		return
	}
	projectID, ok := h.parseChannelProjectBinding(w, r, workspaceID, req.ProjectID)
	if !ok {
		return
	}
	var previousProjectID pgtype.UUID
	if err := h.DB.QueryRow(r.Context(), `
		SELECT project_id
		FROM channel
		WHERE id = $1 AND workspace_id = $2`, channelID, parseUUID(workspaceID)).Scan(&previousProjectID); err != nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	if _, err := h.DB.Exec(r.Context(),
		`UPDATE channel SET project_id = $2, updated_at = now() WHERE id = $1 AND workspace_id = $3`,
		channelID, projectID, parseUUID(workspaceID),
	); err != nil {
		if isSystemGeneralGuardError(err) {
			writeSystemChannelProtected(w)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update channel project")
		return
	}
	out := ""
	if projectID.Valid {
		out = uuidToString(projectID)
	}
	h.emitChannelProjectSystemEvent(r.Context(), workspaceID, channelID, parseUUID(userID), previousProjectID, projectID)
	writeJSON(w, http.StatusOK, map[string]string{"project_id": out})
}

// ProjectChannelResponse is the lightweight reverse projection used by a
// project's Groups panel. A project owns neither the group nor its members;
// the relation is only optional discussion context.
type ProjectChannelResponse struct {
	ID          string  `json:"id"`
	WorkspaceID string  `json:"workspace_id"`
	ProjectID   string  `json:"project_id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Kind        string  `json:"kind"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

// ListProjectChannels returns active group channels attached to a project.
// It deliberately does not imply a new ownership model: it is the inverse of
// channel.project_id and excludes archived groups from the normal surface.
func (h *Handler) ListProjectChannels(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	projectID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "project id")
	if !ok {
		return
	}
	if _, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{
		ID:          projectID,
		WorkspaceID: workspaceUUID,
	}); err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}

	rows, err := h.DB.Query(r.Context(), `
		SELECT ch.id, ch.workspace_id, ch.project_id, ch.name, ch.description, ch.kind, ch.created_at, ch.updated_at
		FROM channel ch
		JOIN channel_member cm ON cm.channel_id = ch.id
		  AND cm.workspace_id = ch.workspace_id
		  AND cm.member_type = 'user'
		  AND cm.member_id = $3
		WHERE ch.workspace_id = $1 AND ch.project_id = $2 AND ch.kind = 'group' AND ch.archived_at IS NULL
		ORDER BY ch.updated_at DESC, ch.created_at DESC`, workspaceUUID, projectID, parseUUID(userID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list project channels")
		return
	}
	defer rows.Close()

	out := []ProjectChannelResponse{}
	for rows.Next() {
		var id, wsID, pid pgtype.UUID
		var name, kind string
		var description pgtype.Text
		var createdAt, updatedAt pgtype.Timestamptz
		if err := rows.Scan(&id, &wsID, &pid, &name, &description, &kind, &createdAt, &updatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read project channels")
			return
		}
		out = append(out, ProjectChannelResponse{
			ID:          uuidToString(id),
			WorkspaceID: uuidToString(wsID),
			ProjectID:   uuidToString(pid),
			Name:        name,
			Description: textToPtr(description),
			Kind:        kind,
			CreatedAt:   timestampToString(createdAt),
			UpdatedAt:   timestampToString(updatedAt),
		})
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list project channels")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": out, "total": len(out)})
}
