package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/multica-ai/multica/server/internal/memorycuration"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const memoryCurationClaimTimeout = 10 * time.Minute

func (h *Handler) claimPendingMemoryCurationRun(ctx context.Context, rt db.AgentRuntime, activeRunID string) (*protocol.DaemonHeartbeatPendingMemoryCuration, error) {
	var pending protocol.DaemonHeartbeatPendingMemoryCuration
	var targetAgentIDs []string
	var instructions string
	var customArgsRaw, mcpConfigRaw []byte
	if strings.TrimSpace(activeRunID) != "" {
		if _, err := h.DB.Exec(ctx, `
			UPDATE memory_curation_run
			   SET claimed_at = now()
			 WHERE id::text = $1 AND runtime_id = $2 AND status = 'running'
		`, strings.TrimSpace(activeRunID), rt.ID); err != nil {
			return nil, err
		}
		return nil, nil
	}
	err := h.DB.QueryRow(ctx, `
		WITH guard AS MATERIALIZED (
		  SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))
		), candidate AS (
		  SELECT r.id
		    FROM memory_curation_run r
		    CROSS JOIN guard
		   WHERE r.runtime_id = $1::uuid
		     AND r.workspace_id = $2
		     AND r.execution_owner = 'daemon'
		     AND (
		       (r.status IN ('queued', 'waiting_runtime') AND NOT EXISTS (
		         SELECT 1 FROM memory_curation_run active
		          WHERE active.runtime_id = r.runtime_id AND active.status = 'running'
		            AND active.claimed_at >= now() - make_interval(secs => $3::double precision)
		       ))
		       OR (r.status = 'running' AND r.claimed_at < now() - make_interval(secs => $3::double precision))
		     )
		   ORDER BY (r.status = 'running') DESC, r.created_at
		   FOR UPDATE SKIP LOCKED
		   LIMIT 1
		), claimed AS (
		  UPDATE memory_curation_run r
		     SET status = 'running', claimed_at = now(), claim_token = gen_random_uuid(),
		         started_at = COALESCE(started_at, now()), attempt = attempt + 1, error = ''
		    FROM candidate c
		   WHERE r.id = c.id
		  RETURNING r.*
		)
		SELECT c.id::text, c.workspace_id::text, c.stage,
		       COALESCE(c.date_from::text, ''), COALESCE(c.date_to::text, ''),
		       c.target_agent_ids::text[], COALESCE(c.curator_agent_id::text, ''),
		       COALESCE(NULLIF(c.curator_model, ''), a.model, ''), COALESCE(a.thinking_level, ''),
		       COALESCE(a.custom_args, '[]'::jsonb), COALESCE(a.mcp_config, 'null'::jsonb),
		       COALESCE(a.instructions, ''),
		       COALESCE(p.timezone, 'Asia/Shanghai'),
		       c.trigger_kind = 'backfill', c.dry_run, c.force,
		       c.claim_token::text, c.curator_mode, c.confidence_threshold
		  FROM claimed c
		  LEFT JOIN memory_curator_profile p ON p.id = c.profile_id
		  LEFT JOIN agent a ON a.id = c.curator_agent_id
	`, rt.ID, rt.WorkspaceID, memoryCurationClaimTimeout.Seconds()).Scan(
		&pending.ID, &pending.WorkspaceID, &pending.Stage, &pending.DateFrom, &pending.DateTo,
		&targetAgentIDs, &pending.CuratorAgentID, &pending.CuratorModel, &pending.CuratorThinkingLevel,
		&customArgsRaw, &mcpConfigRaw, &instructions,
		&pending.Timezone, &pending.IncludeHistory, &pending.DryRun, &pending.Force,
		&pending.ClaimToken, &pending.Mode, &pending.ConfidenceThreshold,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	pending.AgentIDs = targetAgentIDs
	pending.CuratorInstructions = strings.TrimSpace(instructions)
	_ = json.Unmarshal(customArgsRaw, &pending.CuratorCustomArgs)
	if string(mcpConfigRaw) != "null" {
		pending.CuratorMcpConfig = append([]byte(nil), mcpConfigRaw...)
	}
	pending.DBEvidence = h.memoryCurationEvidenceBundles(ctx, pending.WorkspaceID, pending.AgentIDs, pending.DateFrom, pending.DateTo)
	return &pending, nil
}

func (h *Handler) memoryCurationEvidenceBundles(ctx context.Context, workspaceID string, agentIDs []string, dateFrom, dateTo string) []protocol.DaemonMemoryCurationEvidenceBundle {
	since, err := time.Parse("2006-01-02", dateFrom)
	if err != nil {
		return nil
	}
	until, err := time.Parse("2006-01-02", dateTo)
	if err != nil {
		return nil
	}
	bundles := make([]protocol.DaemonMemoryCurationEvidenceBundle, 0, len(agentIDs))
	for _, agentID := range agentIDs {
		items, err := memorycuration.CollectDBEvidence(ctx, h.DB, workspaceID, agentID, since, until)
		if err != nil || len(items) == 0 {
			continue
		}
		bundle := protocol.DaemonMemoryCurationEvidenceBundle{AgentID: agentID, Items: make([]protocol.DaemonMemoryCurationEvidenceItem, 0, len(items))}
		for _, item := range items {
			bundle.Items = append(bundle.Items, protocol.DaemonMemoryCurationEvidenceItem{Kind: item.Kind, ID: item.ID, Title: item.Title, Snippet: item.Snippet, CreatedAt: item.CreatedAt.UTC().Format(time.RFC3339)})
		}
		bundles = append(bundles, bundle)
	}
	return bundles
}

type memoryCurationDaemonResultRequest struct {
	Status     string          `json:"status"`
	ClaimToken string          `json:"claim_token"`
	Result     json.RawMessage `json:"result"`
	Error      string          `json:"error"`
}

func (h *Handler) ReportMemoryCurationRunResult(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	runID := chi.URLParam(r, "runId")
	runtimeUUID, ok := parseUUIDOrBadRequest(w, runtimeID, "runtime_id")
	if !ok {
		return
	}
	runUUID, ok := parseUUIDOrBadRequest(w, runID, "run_id")
	if !ok {
		return
	}
	rt, err := h.Queries.GetAgentRuntime(r.Context(), runtimeUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "runtime not found")
		return
	}
	if !h.requireDaemonWorkspaceAccess(w, r, uuidToString(rt.WorkspaceID)) {
		return
	}
	var req memoryCurationDaemonResultRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	status := strings.ToLower(strings.TrimSpace(req.Status))
	claimToken, ok := parseUUIDOrBadRequest(w, req.ClaimToken, "claim_token")
	if !ok {
		return
	}
	if status != "succeeded" && status != "failed" {
		writeError(w, http.StatusBadRequest, "status must be succeeded or failed")
		return
	}
	result := req.Result
	if len(result) == 0 {
		result = json.RawMessage(`{}`)
	}
	tag, err := h.DB.Exec(r.Context(), `
		UPDATE memory_curation_run
		   SET status = $4, stats = $5::jsonb, error = $6, finished_at = now()
		 WHERE id = $1 AND runtime_id = $2 AND claim_token = $3 AND status = 'running'
	`, runUUID, runtimeUUID, claimToken, status, result, strings.TrimSpace(req.Error))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to report curation result")
		return
	}
	if tag.RowsAffected() != 1 {
		writeError(w, http.StatusConflict, "curation run is not claimable by this runtime")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": runID, "status": status})
}
