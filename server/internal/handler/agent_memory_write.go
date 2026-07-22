package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/pkg/protocol"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const agentMemoryWriteDedupWindow = 5 * time.Minute

var allowedAgentMemoryScopeTypes = map[string]struct{}{
	"agent_global": {},
	"agent_state":  {},
	"user":         {},
	"channel":      {},
	"project":      {},
}

type agentMemoryWriteReportResponse struct {
	Accepted int `json:"accepted"`
	Skipped  int `json:"skipped"`
}

func (h *Handler) ReportAgentMemoryWrites(w http.ResponseWriter, r *http.Request) {
	workspaceID := middleware.DaemonWorkspaceIDFromContext(r.Context())
	if workspaceID == "" {
		writeError(w, http.StatusUnauthorized, "daemon workspace required")
		return
	}
	if !h.requireDaemonWorkspaceAccess(w, r, workspaceID) {
		return
	}

	var req protocol.AgentMemoryWriteReport
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	agentID, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(req.AgentID), "agent_id")
	if !ok {
		return
	}
	wsUUID := parseUUID(workspaceID)
	agent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{
		ID:          agentID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "agent not found in workspace")
		return
	}
	_ = agent

	var runtimeID pgtype.UUID
	if strings.TrimSpace(req.RuntimeID) != "" {
		runtimeID = parseUUID(req.RuntimeID)
	}
	var taskID pgtype.UUID
	if strings.TrimSpace(req.TaskID) != "" {
		taskID = parseUUID(req.TaskID)
	}

	resp := agentMemoryWriteReportResponse{}
	dedupSince := time.Now().UTC().Add(-agentMemoryWriteDedupWindow)
	today := pgtype.Date{Time: time.Now().UTC(), Valid: true}

	for _, write := range req.Writes {
		rel := filepathToSlash(strings.TrimSpace(write.RelPath))
		if rel == "" || strings.HasSuffix(rel, "REVIEW.md") {
			resp.Skipped++
			continue
		}
		if write.ContentHash == "" || write.DeltaChars <= 0 {
			resp.Skipped++
			continue
		}
		scopeType := strings.TrimSpace(write.ScopeType)
		if _, allowed := allowedAgentMemoryScopeTypes[scopeType]; !allowed {
			resp.Skipped++
			continue
		}
		fileKey := strings.TrimSpace(write.FileKey)
		if fileKey == "" {
			resp.Skipped++
			continue
		}

		recent, err := h.Queries.HasRecentAgentMemoryWrite(r.Context(), db.HasRecentAgentMemoryWriteParams{
			AgentID:   agentID,
			RelPath:   rel,
			CreatedAt: dedupSince,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "dedup check failed")
			return
		}
		if recent {
			resp.Skipped++
			continue
		}

		if _, err := h.Queries.InsertAgentMemoryWriteEvent(r.Context(), db.InsertAgentMemoryWriteEventParams{
			WorkspaceID: wsUUID,
			AgentID:     agentID,
			RuntimeID:   runtimeID,
			TaskID:      taskID,
			RelPath:     rel,
			ScopeType:   scopeType,
			FileKey:     fileKey,
			ContentHash: write.ContentHash,
			DeltaChars:  int32(write.DeltaChars),
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "insert memory write event failed")
			return
		}
		if _, err := h.Queries.UpsertAgentMemoryWriteDaily(r.Context(), db.UpsertAgentMemoryWriteDailyParams{
			WorkspaceID: wsUUID,
			AgentID:     agentID,
			WriteDate:   today,
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "update daily memory write count failed")
			return
		}

		h.publish(protocol.EventAgentMemoryUpdated, workspaceID, "agent", uuidToString(agentID), protocol.AgentMemoryUpdatedPayload{
			AgentID:   uuidToString(agentID),
			ScopeType: scopeType,
			FileKey:   fileKey,
			Count:     1,
		})
		resp.Accepted++
	}

	writeJSON(w, http.StatusOK, resp)
}

func filepathToSlash(path string) string {
	return strings.ReplaceAll(path, "\\", "/")
}
