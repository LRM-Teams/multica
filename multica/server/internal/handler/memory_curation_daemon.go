package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/memorycuration"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	// memoryCurationReclaimTimeout is how long a claimed running row may sit
	// without claimed_at refresh before the server treats it as undelivered /
	// abandoned and either reclaims or fails it.
	//
	// Must stay above typical team-curator wall time: cursor team curation
	// regularly exceeds 2 minutes, and a too-short window produces false
	// "exceeded max daemon claim attempts" failures (~3×timeout) even when
	// the daemon is still working. Keep it under memoryCurationMaxRunAge so
	// true undelivered claims still recycle before the hard age cap.
	memoryCurationReclaimTimeout = 12 * time.Minute
	memoryCurationMaxRunAge      = 60 * time.Minute
	memoryCurationMaxAttempts    = 3
)

func (h *Handler) claimPendingMemoryCurationRun(ctx context.Context, rt db.AgentRuntime, activeRunID string) (*protocol.DaemonHeartbeatPendingMemoryCuration, error) {
	// Refresh the active claim before sweeping expirations so a briefly delayed
	// heartbeat cannot fail a still-running curator (fail-then-refresh would
	// race healthy long team-curation runs into claim-attempt death).
	activeID := strings.TrimSpace(activeRunID)
	if activeID != "" {
		if err := h.refreshActiveMemoryCurationClaim(ctx, rt.ID, activeID); err != nil {
			return nil, err
		}
	}
	if err := h.failExpiredMemoryCurationRuns(ctx, rt.ID, rt.WorkspaceID); err != nil {
		return nil, err
	}
	if activeID != "" {
		return nil, nil
	}
	if pending, err := h.claimPendingMemoryCurationAgentRun(ctx, rt); err != nil || pending != nil {
		return pending, err
	}
	return h.claimPendingMemoryCurationParentRun(ctx, rt)
}

// refreshActiveMemoryCurationClaim bumps claimed_at for the daemon's in-flight
// curation row. When the heartbeat carries an agent-run id, also refresh the
// parent so stage=all parents are not swept while a child self-review is live.
func (h *Handler) refreshActiveMemoryCurationClaim(ctx context.Context, runtimeID pgtype.UUID, activeRunID string) error {
	if _, err := h.DB.Exec(ctx, `
		UPDATE memory_curation_run
		   SET claimed_at = now()
		 WHERE id::text = $1 AND runtime_id = $2 AND status = 'running'
	`, activeRunID, runtimeID); err != nil {
		return err
	}
	if _, err := h.DB.Exec(ctx, `
		UPDATE memory_curation_agent_run
		   SET claimed_at = now(), updated_at = now()
		 WHERE id::text = $1 AND runtime_id = $2 AND status = 'running'
	`, activeRunID, runtimeID); err != nil {
		return err
	}
	_, err := h.DB.Exec(ctx, `
		UPDATE memory_curation_run r
		   SET claimed_at = now()
		  FROM memory_curation_agent_run cr
		 WHERE cr.id::text = $1
		   AND cr.runtime_id = $2
		   AND cr.status = 'running'
		   AND r.id = cr.parent_run_id
		   AND r.runtime_id = $2
		   AND r.status = 'running'
	`, activeRunID, runtimeID)
	return err
}

func (h *Handler) claimPendingMemoryCurationAgentRun(ctx context.Context, rt db.AgentRuntime) (*protocol.DaemonHeartbeatPendingMemoryCuration, error) {
	var pending protocol.DaemonHeartbeatPendingMemoryCuration
	var targetAgentIDs []string
	var instructions string
	var customArgsRaw, mcpConfigRaw []byte
	err := h.DB.QueryRow(ctx, `
		WITH guard AS MATERIALIZED (
		  SELECT pg_advisory_xact_lock(hashtextextended($1::text || ':agent-memory-curation-child', 0))
		), candidate AS (
		  SELECT cr.id
		    FROM memory_curation_agent_run cr
		    JOIN memory_curation_run r ON r.id = cr.parent_run_id
		    CROSS JOIN guard
		   WHERE cr.runtime_id = $1::uuid
		     AND cr.workspace_id = $2
		     AND cr.stage = 'agent_self_review'
		     AND r.execution_owner = 'daemon'
		     AND r.stage IN ('agent_self_review', 'all')
		     AND r.status IN ('queued', 'waiting_runtime', 'running')
		     AND (
		       (cr.status IN ('queued', 'waiting_runtime') AND NOT EXISTS (
		         SELECT 1 FROM memory_curation_agent_run active
		          WHERE active.runtime_id = cr.runtime_id AND active.status = 'running'
		            AND active.claimed_at >= now() - make_interval(secs => $3::double precision)
		       ))
		       OR (cr.status = 'running' AND cr.claimed_at < now() - make_interval(secs => $3::double precision)
		           AND cr.attempt < $4
		           AND (cr.started_at IS NULL OR cr.started_at >= now() - make_interval(secs => $5::double precision)))
		     )
		   ORDER BY (cr.status = 'running') DESC, cr.created_at
		   FOR UPDATE SKIP LOCKED
		   LIMIT 1
		), claimed AS (
		  UPDATE memory_curation_agent_run cr
		     SET status = 'running', claimed_at = now(), claim_token = gen_random_uuid(),
		         started_at = COALESCE(started_at, now()), attempt = attempt + 1, error = '', updated_at = now()
		    FROM candidate c
		   WHERE cr.id = c.id
		  RETURNING cr.*
		)
		SELECT c.id::text, c.parent_run_id::text, c.workspace_id::text, c.stage,
		       COALESCE(r.date_from::text, ''), COALESCE(r.date_to::text, ''),
		       ARRAY[c.agent_id::text], c.agent_id::text,
		       COALESCE(NULLIF(r.curator_model, ''), a.model, ''), COALESCE(a.thinking_level, ''),
		       COALESCE(a.custom_args, '[]'::jsonb), COALESCE(a.mcp_config, 'null'::jsonb),
		       COALESCE(a.instructions, ''),
		       COALESCE(p.timezone, 'Asia/Shanghai'),
		       r.trigger_kind = 'backfill', r.dry_run, r.force,
		       c.claim_token::text, r.curator_mode, r.confidence_threshold
		  FROM claimed c
		  JOIN memory_curation_run r ON r.id = c.parent_run_id
		  LEFT JOIN memory_curator_profile p ON p.id = r.profile_id
		  LEFT JOIN agent a ON a.id = c.agent_id
	`, rt.ID, rt.WorkspaceID, memoryCurationReclaimTimeout.Seconds(), memoryCurationMaxAttempts, memoryCurationMaxRunAge.Seconds()).Scan(
		&pending.ID, &pending.ParentRunID, &pending.WorkspaceID, &pending.Stage, &pending.DateFrom, &pending.DateTo,
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
	pending.AgentRunID = pending.ID
	pending.AgentIDs = targetAgentIDs
	pending.CuratorInstructions = strings.TrimSpace(instructions)
	_ = json.Unmarshal(customArgsRaw, &pending.CuratorCustomArgs)
	if string(mcpConfigRaw) != "null" {
		pending.CuratorMcpConfig = append([]byte(nil), mcpConfigRaw...)
	}
	pending.DBEvidence = h.memoryCurationEvidenceBundles(ctx, pending.WorkspaceID, pending.AgentIDs, pending.DateFrom, pending.DateTo, false)
	return &pending, nil
}

func (h *Handler) claimPendingMemoryCurationParentRun(ctx context.Context, rt db.AgentRuntime) (*protocol.DaemonHeartbeatPendingMemoryCuration, error) {
	var pending protocol.DaemonHeartbeatPendingMemoryCuration
	var targetAgentIDs []string
	var instructions string
	var customArgsRaw, mcpConfigRaw []byte
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
		       r.stage = 'team_curation'
		       OR (r.stage = 'agent_self_review' AND NOT EXISTS (SELECT 1 FROM memory_curation_agent_run cr WHERE cr.parent_run_id = r.id))
		       OR (r.stage = 'all' AND (
		         NOT EXISTS (SELECT 1 FROM memory_curation_agent_run cr WHERE cr.parent_run_id = r.id)
		         OR (
		           NOT EXISTS (SELECT 1 FROM memory_curation_agent_run cr WHERE cr.parent_run_id = r.id AND cr.status IN ('queued','waiting_runtime','running'))
		           AND EXISTS (SELECT 1 FROM memory_curation_agent_run cr WHERE cr.parent_run_id = r.id AND cr.status = 'succeeded')
		         )
		       ))
		     )
		     AND (
		       (r.status IN ('queued', 'waiting_runtime') AND NOT EXISTS (
		         SELECT 1 FROM memory_curation_run active
		          WHERE active.runtime_id = r.runtime_id AND active.status = 'running'
		            AND active.claimed_at >= now() - make_interval(secs => $3::double precision)
		       ))
		       OR (r.status = 'running' AND r.claimed_at < now() - make_interval(secs => $3::double precision)
		           AND r.attempt < $4
		           AND (r.started_at IS NULL OR r.started_at >= now() - make_interval(secs => $5::double precision)))
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
		SELECT c.id::text, c.workspace_id::text,
		       CASE WHEN c.stage = 'all' THEN 'team_curation' ELSE c.stage END,
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
	`, rt.ID, rt.WorkspaceID, memoryCurationReclaimTimeout.Seconds(), memoryCurationMaxAttempts, memoryCurationMaxRunAge.Seconds()).Scan(
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
	pending.DBEvidence = h.memoryCurationEvidenceBundles(ctx, pending.WorkspaceID, pending.AgentIDs, pending.DateFrom, pending.DateTo, pending.Stage == "team_curation")
	return &pending, nil
}

func (h *Handler) failExpiredMemoryCurationRuns(ctx context.Context, runtimeID, workspaceID pgtype.UUID) error {
	if _, err := h.DB.Exec(ctx, `
		UPDATE memory_curation_run
		   SET status = 'failed', finished_at = now(),
		       error = CASE
		         WHEN started_at IS NOT NULL AND started_at < now() - make_interval(secs => $3::double precision)
		           THEN 'memory curation exceeded server max runtime'
		         ELSE 'memory curation exceeded max daemon claim attempts'
		       END
		 WHERE runtime_id = $1
		   AND workspace_id = $2
		   AND execution_owner = 'daemon'
		   AND status = 'running'
		   AND (
		     (started_at IS NOT NULL AND started_at < now() - make_interval(secs => $3::double precision))
		     OR (claimed_at < now() - make_interval(secs => $4::double precision) AND attempt >= $5)
		   )
	`, runtimeID, workspaceID, memoryCurationMaxRunAge.Seconds(), memoryCurationReclaimTimeout.Seconds(), memoryCurationMaxAttempts); err != nil {
		return err
	}
	if _, err := h.DB.Exec(ctx, `
		UPDATE memory_curation_agent_run
		   SET status = 'failed', finished_at = now(), updated_at = now(),
		       error = CASE
		         WHEN started_at IS NOT NULL AND started_at < now() - make_interval(secs => $3::double precision)
		           THEN 'memory curation agent self-review exceeded server max runtime'
		         ELSE 'memory curation agent self-review exceeded max daemon claim attempts'
		       END
		 WHERE runtime_id = $1
		   AND workspace_id = $2
		   AND status = 'running'
		   AND (
		     (started_at IS NOT NULL AND started_at < now() - make_interval(secs => $3::double precision))
		     OR (claimed_at < now() - make_interval(secs => $4::double precision) AND attempt >= $5)
		   )
	`, runtimeID, workspaceID, memoryCurationMaxRunAge.Seconds(), memoryCurationReclaimTimeout.Seconds(), memoryCurationMaxAttempts); err != nil {
		return err
	}
	return h.reconcileOfflineMemoryCurationAgentRuns(ctx, uuidToString(workspaceID), "")
}

// failExpiredMemoryCurationRunsForWorkspace sweeps zombie running rows for an
// entire workspace. Used by status polling so the evolution UI does not stay
// spinning when daemon heartbeats skip the claim/fail path.
func (h *Handler) failExpiredMemoryCurationRunsForWorkspace(ctx context.Context, workspaceID string) error {
	if _, err := h.DB.Exec(ctx, `
		UPDATE memory_curation_run
		   SET status = 'failed', finished_at = now(),
		       error = CASE
		         WHEN started_at IS NOT NULL AND started_at < now() - make_interval(secs => $2::double precision)
		           THEN 'memory curation exceeded server max runtime'
		         ELSE 'memory curation exceeded max daemon claim attempts'
		       END
		 WHERE workspace_id = $1::uuid
		   AND execution_owner = 'daemon'
		   AND status = 'running'
		   AND (
		     (started_at IS NOT NULL AND started_at < now() - make_interval(secs => $2::double precision))
		     OR (claimed_at < now() - make_interval(secs => $3::double precision) AND attempt >= $4)
		   )
	`, workspaceID, memoryCurationMaxRunAge.Seconds(), memoryCurationReclaimTimeout.Seconds(), memoryCurationMaxAttempts); err != nil {
		return err
	}
	if _, err := h.DB.Exec(ctx, `
		UPDATE memory_curation_agent_run
		   SET status = 'failed', finished_at = now(), updated_at = now(),
		       error = CASE
		         WHEN started_at IS NOT NULL AND started_at < now() - make_interval(secs => $2::double precision)
		           THEN 'memory curation agent self-review exceeded server max runtime'
		         ELSE 'memory curation agent self-review exceeded max daemon claim attempts'
		       END
		 WHERE workspace_id = $1::uuid
		   AND status = 'running'
		   AND (
		     (started_at IS NOT NULL AND started_at < now() - make_interval(secs => $2::double precision))
		     OR (claimed_at < now() - make_interval(secs => $3::double precision) AND attempt >= $4)
		   )
	`, workspaceID, memoryCurationMaxRunAge.Seconds(), memoryCurationReclaimTimeout.Seconds(), memoryCurationMaxAttempts); err != nil {
		return err
	}
	return h.reconcileOfflineMemoryCurationAgentRuns(ctx, workspaceID, "")
}

const memoryCurationOfflineSkipError = "runtime offline; skipped"

// reconcileOfflineMemoryCurationAgentRuns marks waiting_runtime children whose
// runtime is missing/offline as skipped, then finalizes any parent whose
// remaining children are all terminal. Offline agents must not block the
// rest of a self-review wave.
func (h *Handler) reconcileOfflineMemoryCurationAgentRuns(ctx context.Context, workspaceID, parentRunID string) error {
	workspaceID = strings.TrimSpace(workspaceID)
	parentRunID = strings.TrimSpace(parentRunID)
	if workspaceID == "" && parentRunID == "" {
		return nil
	}
	if _, err := h.DB.Exec(ctx, `
		UPDATE memory_curation_agent_run cr
		   SET status = 'skipped',
		       error = $3,
		       finished_at = COALESCE(finished_at, now()),
		       updated_at = now()
		 WHERE cr.status = 'waiting_runtime'
		   AND ($1 = '' OR cr.workspace_id = $1::uuid)
		   AND ($2 = '' OR cr.parent_run_id = $2::uuid)
		   AND (
		     cr.runtime_id IS NULL
		     OR NOT EXISTS (
		       SELECT 1 FROM agent_runtime rt
		        WHERE rt.id = cr.runtime_id
		          AND rt.last_seen_at >= now() - make_interval(secs => $4::double precision)
		     )
		   )
	`, workspaceID, parentRunID, memoryCurationOfflineSkipError, memoryCurationRuntimeStaleSecs); err != nil {
		return err
	}
	rows, err := h.DB.Query(ctx, `
		SELECT r.id::text
		  FROM memory_curation_run r
		 WHERE r.status IN ('queued', 'waiting_runtime', 'running')
		   AND r.stage IN ('agent_self_review', 'all')
		   AND ($1 = '' OR r.workspace_id = $1::uuid)
		   AND ($2 = '' OR r.id = $2::uuid)
		   AND EXISTS (
		     SELECT 1 FROM memory_curation_agent_run cr WHERE cr.parent_run_id = r.id
		   )
		   AND NOT EXISTS (
		     SELECT 1 FROM memory_curation_agent_run cr
		      WHERE cr.parent_run_id = r.id
		        AND cr.status IN ('queued', 'waiting_runtime', 'running')
		   )
	`, workspaceID, parentRunID)
	if err != nil {
		return err
	}
	defer rows.Close()
	parentIDs := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		parentIDs = append(parentIDs, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range parentIDs {
		if err := finalizeMemoryCurationParentFromAgentRuns(ctx, h.DB, id); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) memoryCurationEvidenceBundles(ctx context.Context, workspaceID string, agentIDs []string, dateFrom, dateTo string, teamCurationOnly bool) []protocol.DaemonMemoryCurationEvidenceBundle {
	bundles := make([]protocol.DaemonMemoryCurationEvidenceBundle, 0, len(agentIDs))
	bundleIndexes := make(map[string]int, len(agentIDs))

	// Team curation consumes self-review outputs + pending candidates. Do not
	// re-send raw same-day chat/session evidence (that already fed agent_self_review).
	if !teamCurationOnly {
		since, err := time.Parse("2006-01-02", dateFrom)
		if err != nil {
			return nil
		}
		until, err := time.Parse("2006-01-02", dateTo)
		if err != nil {
			return nil
		}
		for _, agentID := range agentIDs {
			items, err := memorycuration.CollectDBEvidence(ctx, h.DB, workspaceID, agentID, since, until)
			if err != nil || len(items) == 0 {
				continue
			}
			bundle := protocol.DaemonMemoryCurationEvidenceBundle{AgentID: agentID, Items: make([]protocol.DaemonMemoryCurationEvidenceItem, 0, len(items))}
			for _, item := range items {
				bundle.Items = append(bundle.Items, protocol.DaemonMemoryCurationEvidenceItem{Kind: item.Kind, ID: item.ID, Title: item.Title, Snippet: item.Snippet, CreatedAt: item.CreatedAt.UTC().Format(time.RFC3339)})
			}
			bundleIndexes[agentID] = len(bundles)
			bundles = append(bundles, bundle)
		}
		return bundles
	}

	rows, err := h.DB.Query(ctx, `
		SELECT id::text, COALESCE(source_agent_id::text,''), title,
		       left(content, 2000), created_at
		  FROM agent_memory_curation_candidate
		 WHERE workspace_id = $1 AND status = 'pending'
		   AND (source_agent_id = ANY($2::uuid[]) OR source_agent_id IS NULL)
		 ORDER BY created_at, id
		 LIMIT 200
	`, workspaceID, agentIDs)
	if err != nil {
		return bundles
	}
	defer rows.Close()
	for rows.Next() {
		var candidateID, agentID, title, snippet string
		var createdAt time.Time
		if rows.Scan(&candidateID, &agentID, &title, &snippet, &createdAt) != nil {
			continue
		}
		idx, ok := bundleIndexes[agentID]
		if !ok {
			idx = len(bundles)
			bundleIndexes[agentID] = idx
			bundles = append(bundles, protocol.DaemonMemoryCurationEvidenceBundle{AgentID: agentID})
		}
		bundles[idx].Items = append(bundles[idx].Items, protocol.DaemonMemoryCurationEvidenceItem{
			Kind: "curation_candidate", ID: candidateID, Title: title, Snippet: snippet,
			CreatedAt: createdAt.UTC().Format(time.RFC3339),
		})
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
	var reported struct {
		DryRun bool `json:"dry_run"`
	}
	_ = json.Unmarshal(result, &reported)
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin curation result transaction")
		return
	}
	defer tx.Rollback(r.Context())
	var childDryRun bool
	var childWorkspaceID, childParentStage, childParentRunID string
	err = tx.QueryRow(r.Context(), `
		UPDATE memory_curation_agent_run cr
		   SET status = $4, stats = $5::jsonb, output = $5::jsonb, error = $6,
		       finished_at = now(), updated_at = now()
		  FROM memory_curation_run r
		 WHERE cr.id = $1 AND cr.runtime_id = $2 AND cr.claim_token = $3 AND cr.status = 'running'
		   AND r.id = cr.parent_run_id
		 RETURNING r.dry_run, r.workspace_id::text, r.stage, r.id::text
	`, runUUID, runtimeUUID, claimToken, status, result, strings.TrimSpace(req.Error)).Scan(&childDryRun, &childWorkspaceID, &childParentStage, &childParentRunID)
	if err == nil {
		if err := h.persistMemoryCurationAgentRunOutputsFromRaw(r.Context(), tx, childParentRunID, childWorkspaceID, childParentStage, result); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to persist agent curation run output")
			return
		}
		if status == "succeeded" && !childDryRun && !reported.DryRun {
			if err := h.persistAgenticCurationOutputs(r.Context(), tx, childParentRunID, childWorkspaceID, "agent_self_review", result); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to persist self-review outputs")
				return
			}
		}
		// Skip offline siblings inside the same transaction path via a post-commit
		// reconcile is racy; mark them here before parent finalization so one
		// finished online child can close the wave without waiting forever.
		if _, err := tx.Exec(r.Context(), `
			UPDATE memory_curation_agent_run cr
			   SET status = 'skipped',
			       error = $2,
			       finished_at = COALESCE(finished_at, now()),
			       updated_at = now()
			 WHERE cr.parent_run_id = $1::uuid
			   AND cr.status = 'waiting_runtime'
			   AND (
			     cr.runtime_id IS NULL
			     OR NOT EXISTS (
			       SELECT 1 FROM agent_runtime rt
			        WHERE rt.id = cr.runtime_id
			          AND rt.last_seen_at >= now() - make_interval(secs => $3::double precision)
			     )
			   )
		`, childParentRunID, memoryCurationOfflineSkipError, memoryCurationRuntimeStaleSecs); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to skip offline self-review children")
			return
		}
		if err := finalizeMemoryCurationParentFromAgentRuns(r.Context(), tx, childParentRunID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to aggregate parent curation run")
			return
		}
		if err := tx.Commit(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to commit curation result")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": runID, "parent_run_id": childParentRunID, "status": status})
		return
	}
	if err != pgx.ErrNoRows {
		writeError(w, http.StatusInternalServerError, "failed to report agent curation result")
		return
	}

	var dryRun bool
	var workspaceID, stage string
	err = tx.QueryRow(r.Context(), `
		UPDATE memory_curation_run
		   SET status = $4, stats = $5::jsonb, error = $6, finished_at = now()
		 WHERE id = $1 AND runtime_id = $2 AND claim_token = $3 AND status = 'running'
		 RETURNING dry_run, workspace_id::text, stage
	`, runUUID, runtimeUUID, claimToken, status, result, strings.TrimSpace(req.Error)).Scan(&dryRun, &workspaceID, &stage)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusConflict, "curation run is not claimable by this runtime")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to report curation result")
		return
	}
	if err := h.persistMemoryCurationAgentRunOutputsFromRaw(r.Context(), tx, runID, workspaceID, stage, result); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to persist agent curation runs")
		return
	}
	if status == "succeeded" && !dryRun && !reported.DryRun {
		if err := h.persistAgenticCurationOutputs(r.Context(), tx, runID, workspaceID, stage, result); err != nil {
			slog.Error("failed to persist curation outputs", "run_id", runID, "runtime_id", runtimeID, "stage", stage, "error", err)
			writeError(w, http.StatusInternalServerError, "failed to persist curation outputs")
			return
		}
	}
	if stage == "all" {
		if err := mergeMemoryCurationParentStatsWithAgentRuns(r.Context(), tx, runID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to merge agent curation stats")
			return
		}
	}
	if err := finishUnreportedMemoryCurationAgentRuns(r.Context(), tx, runID, status, req.Error); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to finalize agent curation runs")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit curation result")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": runID, "status": status})
}
