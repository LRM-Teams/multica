package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const maxAgentStartIntentBatch = 50

var agentStartIntentFailureCode = regexp.MustCompile(`^[a-z0-9][a-z0-9_.:/-]{0,127}$`)

// pendingAgentStartIntents returns every unacknowledged first-start intent for
// a Computer. It deliberately does not claim the rows: a dropped heartbeat
// response must offer the same start_dispatch_id again, not lose the start.
func (h *Handler) pendingAgentStartIntents(ctx context.Context, runtimeID pgtype.UUID) ([]protocol.DaemonHeartbeatPendingAgentStartIntent, error) {
	rows, err := h.DB.Query(ctx, `
		WITH due AS (
			SELECT intent.start_dispatch_id
			FROM agent_start_intent intent
			JOIN agent ON agent.id = intent.agent_id
			WHERE intent.runtime_id = $1
			  AND intent.status = 'pending'
			  AND agent.archived_at IS NULL
			  AND agent.runtime_id = intent.runtime_id
			ORDER BY intent.created_at, intent.start_dispatch_id
			LIMIT $2
			FOR UPDATE OF intent SKIP LOCKED
		)
		UPDATE agent_start_intent intent
		SET dispatch_attempts = intent.dispatch_attempts + 1,
		    last_dispatched_at = now(),
		    updated_at = now()
		FROM due
		WHERE intent.start_dispatch_id = due.start_dispatch_id
		RETURNING intent.start_dispatch_id::text,
		          intent.agent_id::text,
		          intent.runtime_id::text,
		          intent.workspace_id::text
	`, runtimeID, maxAgentStartIntentBatch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	intents := make([]protocol.DaemonHeartbeatPendingAgentStartIntent, 0)
	for rows.Next() {
		var intent protocol.DaemonHeartbeatPendingAgentStartIntent
		if err := rows.Scan(&intent.StartDispatchID, &intent.AgentID, &intent.RuntimeID, &intent.WorkspaceID); err != nil {
			return nil, err
		}
		intents = append(intents, intent)
	}
	return intents, rows.Err()
}

type agentStartIntentReportRequest struct {
	Status       string `json:"status"`
	LifecycleSeq int64  `json:"lifecycle_seq"`
	FailureCode  string `json:"failure_code,omitempty"`
}

// ReportAgentStartIntent records the Computer's receipt or later runtime
// observation. The monotonic lifecycle sequence turns duplicate/lost/reordered
// reports into no-ops and allows a later failed observation to follow ready
// without recreating the Agent or reopening its completed Proposal.
func (h *Handler) ReportAgentStartIntent(w http.ResponseWriter, r *http.Request) {
	runtimeID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "runtimeId"), "runtime_id")
	if !ok {
		return
	}
	dispatchID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "startDispatchId"), "start_dispatch_id")
	if !ok {
		return
	}
	runtime, err := h.Queries.GetAgentRuntime(r.Context(), runtimeID)
	if err != nil {
		writeError(w, http.StatusNotFound, "runtime not found")
		return
	}
	if !h.requireDaemonWorkspaceAccess(w, r, uuidToString(runtime.WorkspaceID)) {
		return
	}

	var req agentStartIntentReportRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	status := strings.ToLower(strings.TrimSpace(req.Status))
	if status != "accepted" && status != "queued" && status != "ready" && status != "failed" {
		writeError(w, http.StatusBadRequest, "status must be accepted, queued, ready, or failed")
		return
	}
	if req.LifecycleSeq < 1 {
		writeError(w, http.StatusBadRequest, "lifecycle_seq must be positive")
		return
	}
	failureCode := strings.TrimSpace(req.FailureCode)
	if status == "failed" && !agentStartIntentFailureCode.MatchString(failureCode) {
		writeError(w, http.StatusBadRequest, "failed status requires a sanitized failure_code")
		return
	}
	if status != "failed" && failureCode != "" {
		writeError(w, http.StatusBadRequest, "failure_code is only valid for failed status")
		return
	}

	var finalStatus, agentID, workspaceID string
	var changed bool
	err = h.DB.QueryRow(r.Context(), `
		WITH changed AS (
			UPDATE agent_start_intent
			SET status = $4,
			    lifecycle_seq = $3,
			    reported_at = now(),
			    -- A ready report is only legal after the daemon applied the local
			    -- acceptance work. If its accepted HTTP response was lost, retain
			    -- that fact instead of leaving a semantically impossible ready row
			    -- without accepted_at.
			    accepted_at = CASE WHEN $4 IN ('accepted', 'queued', 'ready') THEN COALESCE(accepted_at, now()) ELSE accepted_at END,
			    ready_at = CASE WHEN $4 = 'ready' THEN now() ELSE ready_at END,
			    failed_at = CASE WHEN $4 = 'failed' THEN now() ELSE failed_at END,
			    failure_code = CASE WHEN $4 = 'failed' THEN $5 ELSE failure_code END,
			    updated_at = now()
			WHERE start_dispatch_id = $1 AND runtime_id = $2 AND $3 > lifecycle_seq
			RETURNING status, agent_id::text, workspace_id::text
		)
		SELECT status, agent_id, workspace_id, true FROM changed
		UNION ALL
		SELECT status, agent_id::text, workspace_id::text, false
		FROM agent_start_intent
		WHERE start_dispatch_id = $1 AND runtime_id = $2
		  AND NOT EXISTS (SELECT 1 FROM changed)
	`, dispatchID, runtimeID, req.LifecycleSeq, status, failureCode).Scan(&finalStatus, &agentID, &workspaceID, &changed)
	if err == pgx.ErrNoRows {
		writeError(w, http.StatusNotFound, "agent start intent not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record agent start intent report")
		return
	}
	if changed {
		if updated, err := h.Queries.GetAgent(r.Context(), parseUUID(agentID)); err == nil {
			resp := agentToResponse(updated)
			h.attachAgentRuntimeName(r.Context(), &resp)
			h.publishAgentVisibilityEvent(protocol.EventAgentStatus, workspaceID, "daemon", "", updated, map[string]any{"agent": broadcastAgentResponse(resp)})
		} else {
			slog.Warn("load agent after start intent report", "agent_id", agentID, "error", err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": finalStatus})
}
