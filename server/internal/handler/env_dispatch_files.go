package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/agentworkspace"
	"github.com/multica-ai/multica/server/internal/daemonws"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type envDispatchFileTarget struct {
	AgentID   string
	RuntimeID string
	RelPath   string
}

// DownloadEnvDispatchFile handles GET /api/v1/env-dispatch/{projectID}/files?path=.
// The AReaL eval client expects raw bytes on 200 and treats 404 as a typed
// missing deliverable, not a transport failure.
func (h *Handler) DownloadEnvDispatchFile(w http.ResponseWriter, r *http.Request) {
	h.downloadEnvDispatchFile(w, r, chi.URLParam(r, "projectID"), "")
}

// UploadEnvDispatchFile handles POST /api/v1/env-dispatch/{projectID}/files.
func (h *Handler) UploadEnvDispatchFile(w http.ResponseWriter, r *http.Request) {
	h.uploadEnvDispatchFile(w, r, chi.URLParam(r, "projectID"), "")
}

// DownloadEnvDispatchChannelFile handles GET /api/v1/env-dispatch/channels/{channelID}/files?path=.
func (h *Handler) DownloadEnvDispatchChannelFile(w http.ResponseWriter, r *http.Request) {
	h.downloadEnvDispatchFile(w, r, "", chi.URLParam(r, "channelID"))
}

// UploadEnvDispatchChannelFile handles POST /api/v1/env-dispatch/channels/{channelID}/files.
func (h *Handler) UploadEnvDispatchChannelFile(w http.ResponseWriter, r *http.Request) {
	h.uploadEnvDispatchFile(w, r, "", chi.URLParam(r, "channelID"))
}

func (h *Handler) downloadEnvDispatchFile(w http.ResponseWriter, r *http.Request, projectID, channelID string) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace ID required")
		return
	}
	if projectID != "" {
		if _, ok := parseUUIDOrBadRequest(w, projectID, "projectID"); !ok {
			return
		}
	}
	if channelID != "" {
		if _, ok := parseUUIDOrBadRequest(w, channelID, "channelID"); !ok {
			return
		}
	}
	filePath, err := validateEnvRelativePath(r.URL.Query().Get("path"), "path")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	target, err := h.lookupEnvDispatchFileTarget(r.Context(), workspaceID, projectID, channelID)
	if err != nil {
		writeEnvDispatchFileLookupError(w, err)
		return
	}
	if h.DaemonHub == nil {
		writeError(w, http.StatusServiceUnavailable, "runtime offline")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), agentFileRPCTimeout)
	defer cancel()
	resp, err := h.DaemonHub.RequestReadFile(ctx, protocol.ReadWorkdirFileRequestPayload{
		RequestID: uuid.NewString(),
		RuntimeID: target.RuntimeID,
		RelPath:   target.RelPath,
		FilePath:  filePath,
	})
	if err != nil {
		if errors.Is(err, daemonws.ErrRuntimeOffline) {
			writeError(w, http.StatusServiceUnavailable, "runtime offline")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to read file")
		return
	}
	if resp.Missing {
		writeError(w, http.StatusNotFound, "file not found")
		return
	}
	if resp.Error != "" {
		writeError(w, http.StatusBadRequest, resp.Error)
		return
	}
	body, err := envDispatchFileBytes(resp)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (h *Handler) uploadEnvDispatchFile(w http.ResponseWriter, r *http.Request, projectID, channelID string) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace ID required")
		return
	}
	if projectID != "" {
		if _, ok := parseUUIDOrBadRequest(w, projectID, "projectID"); !ok {
			return
		}
	}
	if channelID != "" {
		if _, ok := parseUUIDOrBadRequest(w, channelID, "channelID"); !ok {
			return
		}
	}
	var req EnvDispatchStagedFile
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	filePath, err := validateEnvRelativePath(req.Path, "path")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	target, err := h.lookupEnvDispatchFileTarget(r.Context(), workspaceID, projectID, channelID)
	if err != nil {
		writeEnvDispatchFileLookupError(w, err)
		return
	}
	if err := h.writeEnvDispatchFile(r.Context(), target, filePath, req.Content); err != nil {
		writeEnvDispatchFileWriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func collectEnvDispatchStagedFiles(req EnvDispatchRequest) ([]EnvDispatchStagedFile, error) {
	out := make([]EnvDispatchStagedFile, 0, len(req.StageFiles))
	for _, file := range req.StageFiles {
		cleaned, err := validateEnvRelativePath(file.Path, "stage_files")
		if err != nil {
			return nil, err
		}
		out = append(out, EnvDispatchStagedFile{Path: cleaned, Content: file.Content})
	}
	if req.Environment == nil {
		return out, nil
	}
	for _, file := range req.Environment.Files {
		if file.Content == "" {
			continue
		}
		cleaned, err := validateEnvRelativePath(file.Path, "environment.files")
		if err != nil {
			return nil, err
		}
		out = append(out, EnvDispatchStagedFile{Path: cleaned, Content: file.Content})
	}
	return out, nil
}

func validateEnvRelativePath(raw, field string) (string, error) {
	normalized := strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	if normalized == "" {
		return "", fmt.Errorf("%s must contain non-empty relative paths", field)
	}
	if strings.HasPrefix(normalized, "/") {
		return "", fmt.Errorf("%s must be workspace-relative, got %q", field, raw)
	}
	for _, part := range strings.Split(normalized, "/") {
		if part == ".." {
			return "", fmt.Errorf("%s must be workspace-relative, got %q", field, raw)
		}
	}
	cleaned := path.Clean(normalized)
	if cleaned == "." || strings.HasPrefix(cleaned, "/") || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("%s must be workspace-relative, got %q", field, raw)
	}
	return cleaned, nil
}

func (h *Handler) stageEnvDispatchFiles(ctx context.Context, workspaceID, projectID, channelID string, files []EnvDispatchStagedFile) error {
	target, err := h.lookupEnvDispatchFileTarget(ctx, workspaceID, projectID, channelID)
	if err != nil {
		return err
	}
	for _, file := range files {
		if err := h.writeEnvDispatchFile(ctx, target, file.Path, file.Content); err != nil {
			return fmt.Errorf("%s: %w", file.Path, err)
		}
	}
	return nil
}

func (h *Handler) lookupEnvDispatchFileTarget(ctx context.Context, workspaceID, projectID, channelID string) (envDispatchFileTarget, error) {
	if h.Queries == nil || h.DB == nil {
		return envDispatchFileTarget{}, fmt.Errorf("runtime offline")
	}
	envID := ""
	if channelID != "" {
		_, resolvedEnvID, err := h.resolveChannelProject(ctx, workspaceID, channelID)
		if err != nil {
			return envDispatchFileTarget{}, err
		}
		envID = resolvedEnvID
	} else {
		project, err := h.Queries.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{
			ID:          parseUUID(projectID),
			WorkspaceID: parseUUID(workspaceID),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return envDispatchFileTarget{}, fmt.Errorf("not found")
			}
			return envDispatchFileTarget{}, fmt.Errorf("lookup project: %w", err)
		}
		if !project.EnvID.Valid {
			return envDispatchFileTarget{}, fmt.Errorf("not found")
		}
		envID = uuidToString(project.EnvID)
	}
	var agentID, runtimeID string
	err := h.DB.QueryRow(ctx, `
SELECT COALESCE(NULLIF(derived_agent_id::text, ''), agent_id::text),
       runtime_id::text
FROM environment_agent_sandbox
WHERE env_id = $1
  AND runtime_id IS NOT NULL
ORDER BY created_at ASC
LIMIT 1`, envID).Scan(&agentID, &runtimeID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return envDispatchFileTarget{}, fmt.Errorf("runtime offline")
		}
		return envDispatchFileTarget{}, fmt.Errorf("lookup sandbox binding: %w", err)
	}
	if agentID == "" || runtimeID == "" {
		return envDispatchFileTarget{}, fmt.Errorf("runtime offline")
	}
	return envDispatchFileTarget{
		AgentID:   agentID,
		RuntimeID: runtimeID,
		RelPath:   agentworkspace.RootRelPath(workspaceID, agentID),
	}, nil
}

func (h *Handler) writeEnvDispatchFile(ctx context.Context, target envDispatchFileTarget, filePath, content string) error {
	if h.DaemonHub == nil {
		return fmt.Errorf("runtime offline")
	}
	rpcCtx, cancel := context.WithTimeout(ctx, agentFileRPCTimeout)
	defer cancel()
	resp, err := h.DaemonHub.RequestWriteFile(rpcCtx, protocol.WriteWorkdirFileRequestPayload{
		RequestID: uuid.NewString(),
		RuntimeID: target.RuntimeID,
		RelPath:   target.RelPath,
		FilePath:  filePath,
		Content:   content,
		Create:    true,
	})
	if err != nil {
		if errors.Is(err, daemonws.ErrRuntimeOffline) {
			return fmt.Errorf("runtime offline")
		}
		return fmt.Errorf("failed to write file: %w", err)
	}
	switch {
	case resp.Missing:
		return fmt.Errorf("failed to create file")
	case resp.TooLarge:
		return fmt.Errorf("file too large")
	case resp.Binary:
		return fmt.Errorf("file must be UTF-8 text")
	case resp.Conflict:
		return fmt.Errorf("file write conflict")
	case resp.Error != "":
		return fmt.Errorf("write file: %s", resp.Error)
	}
	return nil
}

func envDispatchFileBytes(resp *protocol.ReadWorkdirFileResponsePayload) ([]byte, error) {
	if resp.Binary {
		return nil, fmt.Errorf("file is binary")
	}
	if strings.EqualFold(resp.Encoding, "base64") {
		decoded, err := base64.StdEncoding.DecodeString(resp.Content)
		if err != nil {
			return nil, fmt.Errorf("invalid base64 file encoding")
		}
		return decoded, nil
	}
	return []byte(resp.Content), nil
}

func writeEnvDispatchFileLookupError(w http.ResponseWriter, err error) {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "not found"):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
	case strings.Contains(msg, "runtime offline"):
		writeError(w, http.StatusServiceUnavailable, "runtime offline")
	default:
		writeError(w, http.StatusServiceUnavailable, msg)
	}
}

func writeEnvDispatchFileWriteError(w http.ResponseWriter, err error) {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "runtime offline"):
		writeError(w, http.StatusServiceUnavailable, "runtime offline")
	case strings.Contains(msg, "too large"), strings.Contains(msg, "UTF-8"), strings.Contains(msg, "conflict"):
		writeError(w, http.StatusBadRequest, msg)
	default:
		writeError(w, http.StatusInternalServerError, msg)
	}
}
