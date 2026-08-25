package handler

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// RunnerActivitySummariesResponse is the Workspace-scoped compact Activity
// projection. It deliberately excludes Timeline rows and producer facts.
type RunnerActivitySummariesResponse struct {
	Items []RunnerActivitySummaryResponseItem `json:"items"`
}

type RunnerActivitySummaryResponseItem struct {
	AgentID string                        `json:"agent_id"`
	Summary protocol.AgentActivitySummary `json:"summary"`
}

// ListRunnerActivitySummaries returns one sparse Workspace projection for all
// Agents that have a persisted Runner Activity snapshot.
func (h *Handler) ListRunnerActivitySummaries(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.DB == nil {
		writeError(w, http.StatusInternalServerError, "runner activity store is unavailable")
		return
	}
	workspaceID, ok := h.prepareRunnerActivitySummaryList(w, r)
	if !ok {
		return
	}

	rows, err := h.DB.Query(r.Context(), `
		SELECT s.agent_id, s.daemon_id, s.daemon_instance_id,
		       s.activity_kind, s.detail_kind, s.summary_label
		FROM agent_activity_snapshot s
		WHERE s.workspace_id = $1
		ORDER BY s.agent_id`, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load runner activity summaries")
		return
	}
	defer rows.Close()

	response := RunnerActivitySummariesResponse{Items: []RunnerActivitySummaryResponseItem{}}
	for rows.Next() {
		var agentID pgtype.UUID
		var daemonID string
		var snapshot protocol.AgentActivitySnapshot
		var summary protocol.AgentActivitySummary
		if err := rows.Scan(&agentID, &daemonID, &snapshot.DaemonInstanceID, &summary.ActivityKind, &summary.DetailKind, &summary.Label); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load runner activity summaries")
			return
		}
		agentIDStr := util.UUIDToString(agentID)
		if !h.liveRunnerOwnsActivitySnapshot(daemonID, util.UUIDToString(workspaceID), snapshot) {
			continue
		}
		response.Items = append(response.Items, RunnerActivitySummaryResponseItem{
			AgentID: agentIDStr,
			Summary: summary,
		})
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load runner activity summaries")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) prepareRunnerActivitySummaryList(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
	if _, ok := requireUserID(w, r); !ok {
		return pgtype.UUID{}, false
	}
	workspaceIDText := ctxWorkspaceID(r.Context())
	if workspaceIDText == "" {
		workspaceIDText = h.resolveWorkspaceID(r)
	}
	if workspaceIDText == "" {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return pgtype.UUID{}, false
	}
	actorType, _ := h.resolveActor(r, requestUserID(r), workspaceIDText)
	if actorType == "agent" {
		writeError(w, http.StatusForbidden, "agent principals may not read runner activity")
		return pgtype.UUID{}, false
	}
	if _, ok := h.workspaceMember(w, r, workspaceIDText); !ok {
		return pgtype.UUID{}, false
	}
	workspaceID, err := util.ParseUUID(workspaceIDText)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace_id")
		return pgtype.UUID{}, false
	}
	return workspaceID, true
}
