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
	"github.com/multica-ai/multica/server/internal/agentworkspace"
	"github.com/multica-ai/multica/server/internal/daemonws"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const agentFileRPCTimeout = 12 * time.Second

type agentFileAccessMode string

const (
	agentFileAccessRead  agentFileAccessMode = "read"
	agentFileAccessWrite agentFileAccessMode = "write"
)

type AgentFilesResponse struct {
	AgentID   string                     `json:"agent_id"`
	Status    string                     `json:"status"`
	Nodes     []protocol.WorkdirFileNode `json:"nodes"`
	Truncated bool                       `json:"truncated"`
	RootPath  string                     `json:"root_path,omitempty"`
}

type AgentFileContentResponse struct {
	Content     string `json:"content"`
	Encoding    string `json:"encoding"`
	MimeType    string `json:"mime_type"`
	ContentHash string `json:"content_hash"`
	Truncated   bool   `json:"truncated"`
	TooLarge    bool   `json:"too_large"`
	Binary      bool   `json:"binary"`
}

type UpdateAgentFileContentRequest struct {
	Path                string `json:"path"`
	Content             string `json:"content"`
	ExpectedContentHash string `json:"expected_content_hash"`
}

type UpdateAgentFileContentResponse struct {
	ContentHash string `json:"content_hash"`
	Conflict    bool   `json:"conflict"`
}

func (h *Handler) authorizeAgentFiles(w http.ResponseWriter, r *http.Request, mode agentFileAccessMode) (db.Agent, string, string, bool) {
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return db.Agent{}, "", "", false
	}
	workspaceID := uuidToString(agent.WorkspaceID)
	userID := requestUserID(r)
	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	if actorType == "agent" {
		writeError(w, http.StatusForbidden, "agents may not access agent files")
		return db.Agent{}, "", "", false
	}
	hasAccess := h.canAccessAgentInternals(r.Context(), agent, actorType, actorID, workspaceID)
	if !hasAccess && !(mode == agentFileAccessRead && userID != "" && devAgentProfileAccessEnabled()) {
		writeError(w, http.StatusForbidden, "only the agent creator or a workspace admin can access these files")
		return db.Agent{}, "", "", false
	}
	return agent, actorType, actorID, true
}

func agentRootRelPath(agent db.Agent) string {
	return agentworkspace.RootRelPath(uuidToString(agent.WorkspaceID), uuidToString(agent.ID))
}

func agentFileRuntimeID(agent db.Agent) (string, bool) {
	if !agent.RuntimeID.Valid {
		return "", false
	}
	return uuidToString(agent.RuntimeID), true
}

func includeHiddenAgentFiles(r *http.Request) bool {
	raw := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("include_hidden")))
	return raw == "1" || raw == "true" || raw == "yes" || raw == "on"
}

func (h *Handler) ListAgentFiles(w http.ResponseWriter, r *http.Request) {
	agent, _, _, ok := h.authorizeAgentFiles(w, r, agentFileAccessRead)
	if !ok {
		return
	}
	agentID := uuidToString(agent.ID)
	reply := func(status string, nodes []protocol.WorkdirFileNode, truncated bool, rootPath string) {
		if nodes == nil {
			nodes = []protocol.WorkdirFileNode{}
		}
		writeJSON(w, http.StatusOK, AgentFilesResponse{
			AgentID:   agentID,
			Status:    status,
			Nodes:     nodes,
			Truncated: truncated,
			RootPath:  rootPath,
		})
	}
	runtimeID, ok := agentFileRuntimeID(agent)
	if !ok || h.DaemonHub == nil {
		reply("offline", nil, false, "")
		return
	}
	dirPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if dirPath == "." {
		dirPath = ""
	}
	ctx, cancel := context.WithTimeout(r.Context(), agentFileRPCTimeout)
	defer cancel()
	resp, err := h.DaemonHub.RequestWorkdirFiles(ctx, protocol.ListWorkdirFilesRequestPayload{
		RequestID:    uuid.NewString(),
		RuntimeID:    runtimeID,
		RelPath:      agentRootRelPath(agent),
		DirPath:      dirPath,
		OneLevel:     true,
		HideDotfiles: !includeHiddenAgentFiles(r),
	})
	if err != nil {
		if errors.Is(err, daemonws.ErrRuntimeOffline) {
			reply("offline", nil, false, "")
			return
		}
		reply("error", nil, false, "")
		return
	}
	if resp.Missing {
		reply("missing", nil, false, resp.RootPath)
		return
	}
	if resp.Error != "" {
		reply("error", nil, false, resp.RootPath)
		return
	}
	reply("ok", resp.Nodes, resp.Truncated, resp.RootPath)
}

func (h *Handler) GetAgentFileContent(w http.ResponseWriter, r *http.Request) {
	agent, _, _, ok := h.authorizeAgentFiles(w, r, agentFileAccessRead)
	if !ok {
		return
	}
	filePath := strings.TrimSpace(r.URL.Query().Get("path"))
	if filePath == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	if reason := agentworkspace.PreviewDeniedReason(filePath); reason != "" {
		writeError(w, http.StatusBadRequest, reason)
		return
	}
	runtimeID, ok := agentFileRuntimeID(agent)
	if !ok || h.DaemonHub == nil {
		writeError(w, http.StatusServiceUnavailable, "runtime offline")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), agentFileRPCTimeout)
	defer cancel()
	resp, err := h.DaemonHub.RequestReadFile(ctx, protocol.ReadWorkdirFileRequestPayload{
		RequestID: uuid.NewString(),
		RuntimeID: runtimeID,
		RelPath:   agentRootRelPath(agent),
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
	writeJSON(w, http.StatusOK, AgentFileContentResponse{
		Content:     resp.Content,
		Encoding:    resp.Encoding,
		MimeType:    resp.MimeType,
		ContentHash: resp.ContentHash,
		Truncated:   resp.Truncated,
		TooLarge:    resp.TooLarge,
		Binary:      resp.Binary,
	})
}

func (h *Handler) UpdateAgentFileContent(w http.ResponseWriter, r *http.Request) {
	agent, _, _, ok := h.authorizeAgentFiles(w, r, agentFileAccessWrite)
	if !ok {
		return
	}
	var req UpdateAgentFileContentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Path = strings.TrimSpace(req.Path)
	if req.Path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	runtimeID, ok := agentFileRuntimeID(agent)
	if !ok || h.DaemonHub == nil {
		writeError(w, http.StatusServiceUnavailable, "runtime offline")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), agentFileRPCTimeout)
	defer cancel()
	resp, err := h.DaemonHub.RequestWriteFile(ctx, protocol.WriteWorkdirFileRequestPayload{
		RequestID:           uuid.NewString(),
		RuntimeID:           runtimeID,
		RelPath:             agentRootRelPath(agent),
		FilePath:            req.Path,
		Content:             req.Content,
		ExpectedContentHash: req.ExpectedContentHash,
	})
	if err != nil {
		if errors.Is(err, daemonws.ErrRuntimeOffline) {
			writeError(w, http.StatusServiceUnavailable, "runtime offline")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to write file")
		return
	}
	if resp.Missing {
		writeError(w, http.StatusNotFound, "file not found")
		return
	}
	if resp.Conflict {
		writeJSON(w, http.StatusConflict, UpdateAgentFileContentResponse{
			ContentHash: resp.ContentHash,
			Conflict:    true,
		})
		return
	}
	if resp.TooLarge {
		writeError(w, http.StatusRequestEntityTooLarge, "file too large")
		return
	}
	if resp.Binary {
		writeError(w, http.StatusBadRequest, "file must be utf-8 text")
		return
	}
	if resp.Error != "" {
		writeError(w, http.StatusBadRequest, resp.Error)
		return
	}
	writeJSON(w, http.StatusOK, UpdateAgentFileContentResponse{
		ContentHash: resp.ContentHash,
		Conflict:    false,
	})
}
