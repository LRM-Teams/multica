package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/memorysignal"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const agentMemoryWriteDedupWindow = 5 * time.Minute

var allowedAgentMemoryScopeTypes = map[string]struct{}{
	"agent_global": {},
	"agent_state":  {},
	"agent_daily":  {},
	"agent_notes":  {},
	"user":         {},
	"channel":      {},
	"project":      {},
}

type agentMemoryWriteReportResponse struct {
	Accepted          int  `json:"accepted"`
	Skipped           int  `json:"skipped"`
	MissedWriteQueued bool `json:"missed_write_queued,omitempty"`
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
	writeEntries := make([]memorysignal.WriteEntry, 0, len(req.Writes))

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
		// Keep every valid write in the current-task evidence, including an
		// event deduplicated from persistence. Otherwise the missed-write guard
		// can enqueue a false miss for a write that really landed on disk.
		writeEntries = append(writeEntries, memorysignal.WriteEntry{
			RelPath: rel, ScopeType: scopeType, FileKey: fileKey,
		})

		recent, err := h.Queries.HasRecentAgentMemoryWrite(r.Context(), db.HasRecentAgentMemoryWriteParams{
			AgentID:     agentID,
			RelPath:     rel,
			ContentHash: write.ContentHash,
			CreatedAt:   pgtype.Timestamptz{Time: dedupSince, Valid: true},
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

	signals := make([]memorysignal.Signal, 0, len(req.Signals))
	for _, s := range req.Signals {
		signals = append(signals, memorysignal.Signal{
			Action:     s.Action,
			Kind:       s.Kind,
			Scope:      s.Scope,
			SubjectID:  s.SubjectID,
			Topic:      s.Topic,
			Summary:    s.Summary,
			Importance: s.Importance,
		})
	}
	friction := memorysignal.FrictionVector{}
	if req.Friction != nil {
		friction = memorysignal.FrictionVector{
			HumanCorrection: req.Friction.HumanCorrection,
			ActionRejected:  req.Friction.ActionRejected,
			RetryLoop:       req.Friction.RetryLoop,
			Rework:          req.Friction.Rework,
			SelfErrorStreak: req.Friction.SelfErrorStreak,
		}
	}
	// A correcting trigger is server-side observable friction: the turn ran
	// under a human correction even when the daemon counted nothing.
	friction = memorysignal.AugmentFrictionFromTrigger(friction, req.TriggerText)
	if queued, err := h.enqueueMemoryGuardCandidates(r.Context(), wsUUID, agentID, taskID, req.TriggerText, req.InitiatorID, signals, writeEntries, friction); err != nil {
		writeError(w, http.StatusInternalServerError, "queue missed memory write failed")
		return
	} else {
		resp.MissedWriteQueued = queued
	}

	if resp.Accepted > 0 {
		h.refreshAgentHonor(r.Context(), wsUUID, agentID, "memory_updated")
	}
	writeJSON(w, http.StatusOK, resp)
}

// enqueueMemoryGuardCandidates runs the missed-write, decision and friction
// guards for one task report. Each guard queues at most one candidate per turn
// and dedupes against pending candidates with the same dedupe_key
// (friction-gated memory spec).
func (h *Handler) enqueueMemoryGuardCandidates(
	ctx context.Context,
	workspaceID, agentID pgtype.UUID,
	taskID pgtype.UUID,
	triggerText, initiatorID string,
	signals []memorysignal.Signal,
	writes []memorysignal.WriteEntry,
	friction memorysignal.FrictionVector,
) (bool, error) {
	if h.DB == nil {
		return false, nil
	}
	queuedAny := false
	if miss, ok := memorysignal.DetectMissedWrite(triggerText, signals, writes, strings.TrimSpace(initiatorID)); ok {
		queued, err := h.insertMemoryGuardCandidate(ctx, workspaceID, agentID, taskID, triggerText, miss, nil)
		if err != nil {
			return queuedAny, err
		}
		queuedAny = queuedAny || queued
	}
	if miss, ok := memorysignal.DetectMissedDecision(triggerText, signals, writes); ok {
		queued, err := h.insertMemoryGuardCandidate(ctx, workspaceID, agentID, taskID, triggerText, miss, nil)
		if err != nil {
			return queuedAny, err
		}
		queuedAny = queuedAny || queued
	}
	if miss, ok := memorysignal.DetectFrictionMiss(friction, signals, writes); ok {
		extra := map[string]any{"friction": friction}
		queued, err := h.insertMemoryGuardCandidate(ctx, workspaceID, agentID, taskID, triggerText, miss, extra)
		if err != nil {
			return queuedAny, err
		}
		queuedAny = queuedAny || queued
	}
	return queuedAny, nil
}

func (h *Handler) insertMemoryGuardCandidate(
	ctx context.Context,
	workspaceID, agentID pgtype.UUID,
	taskID pgtype.UUID,
	triggerText string,
	miss memorysignal.MissedWrite,
	extraMeta map[string]any,
) (bool, error) {
	metaFields := map[string]any{
		"source":          miss.Source,
		"topic":           miss.Topic,
		"topic_key":       miss.Topic,
		"dedupe_key":      miss.DedupeKey,
		"subject_id":      miss.SubjectID,
		"trigger_excerpt": truncateRunes(strings.TrimSpace(triggerText), 280),
		"needs_review":    true,
		"shareable":       false,
		"privacy":         "user_private",
		"task_id":         uuidToString(taskID),
		"awaiting_stage":  "agent_self_review",
	}
	for k, v := range extraMeta {
		metaFields[k] = v
	}
	meta, err := json.Marshal(metaFields)
	if err != nil {
		return false, err
	}
	evidence, err := json.Marshal([]string{})
	if err != nil {
		return false, err
	}

	// Deterministic dedupe: same agent + same dedupe_key while still pending.
	var existing int
	if err := h.DB.QueryRow(ctx, `
		SELECT count(*)
		  FROM agent_memory_curation_candidate
		 WHERE workspace_id = $1
		   AND source_agent_id = $2
		   AND status = 'pending'
		   AND metadata->>'dedupe_key' = $3
	`, workspaceID, agentID, miss.DedupeKey).Scan(&existing); err != nil {
		return false, err
	}
	if existing > 0 {
		return false, nil
	}

	_, err = h.DB.Exec(ctx, `
		INSERT INTO agent_memory_curation_candidate (
		  workspace_id, source_agent_id, candidate_type, scope, title,
		  content, evidence_refs, confidence, status, metadata
		) VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8,'pending',$9::jsonb)
	`, workspaceID, agentID, miss.CandidateType, miss.Scope, miss.Title, miss.Content, evidence, 0.7, meta)
	if err != nil {
		return false, err
	}
	return true, nil
}

func filepathToSlash(path string) string {
	return strings.ReplaceAll(path, "\\", "/")
}
