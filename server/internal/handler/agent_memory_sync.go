package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/memorysync"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type agentMemorySyncRow struct {
	ID          string
	IdentityKey string
	Scope       string
	SubjectID   string
	Kind        string
	Topic       string
	RelPath     string
	Content     string
	ContentHash string
	Status      string
	ConflictOf  pgtype.UUID
}

// SyncAgentMemoryCenter upserts durable local memory atoms into the center store
// using conflict strategy A (keep first active; opposed -> conflict row).
func (h *Handler) SyncAgentMemoryCenter(w http.ResponseWriter, r *http.Request) {
	workspaceID := middleware.DaemonWorkspaceIDFromContext(r.Context())
	if workspaceID == "" {
		writeError(w, http.StatusUnauthorized, "daemon workspace required")
		return
	}
	if !h.requireDaemonWorkspaceAccess(w, r, workspaceID) {
		return
	}
	var req protocol.AgentMemoryCenterSyncReport
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	agentID, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(req.AgentID), "agent_id")
	if !ok {
		return
	}
	wsUUID := parseUUID(workspaceID)
	if _, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{
		ID:          agentID,
		WorkspaceID: wsUUID,
	}); err != nil {
		writeError(w, http.StatusBadRequest, "agent not found in workspace")
		return
	}
	var runtimeID pgtype.UUID
	if strings.TrimSpace(req.RuntimeID) != "" {
		runtimeID = parseUUID(req.RuntimeID)
	}

	resp := protocol.AgentMemoryCenterSyncResponse{}
	if h.DB == nil {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	for _, atom := range req.Entries {
		content := strings.TrimSpace(atom.Content)
		if content == "" {
			resp.Skipped++
			continue
		}
		scope := strings.TrimSpace(atom.Scope)
		subjectID := strings.TrimSpace(atom.SubjectID)
		kind := strings.TrimSpace(atom.Kind)
		relPath := filepathToSlash(strings.TrimSpace(atom.RelPath))
		if scope == "" || kind == "" {
			derivedScope, derivedSubject, derivedKind := memorysync.ScopeFromRelPath(relPath)
			if scope == "" {
				scope = derivedScope
			}
			if subjectID == "" {
				subjectID = derivedSubject
			}
			if kind == "" {
				kind = derivedKind
			}
		}
		if scope == "" {
			resp.Skipped++
			continue
		}
		if relPath != "" && !memorysync.IsDurableRelPath(relPath) {
			resp.Skipped++
			continue
		}
		topic := strings.TrimSpace(atom.Topic)
		if topic == "" {
			topic = memorysync.InferTopic(content)
		}
		identity := memorysync.IdentityKey(scope, subjectID, kind, topic, content)
		hash := memorysync.ContentHash(content)

		existing, err := h.getActiveMemorySyncEntry(r.Context(), agentID, identity)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "load active memory sync entry failed")
			return
		}
		if existing == nil {
			if err := h.insertMemorySyncEntry(r.Context(), wsUUID, agentID, runtimeID, identity, scope, subjectID, kind, topic, relPath, content, hash, memorysync.StatusActive, pgtype.UUID{}); err != nil {
				writeError(w, http.StatusInternalServerError, "insert memory sync entry failed")
				return
			}
			resp.Accepted++
			continue
		}

		decision := memorysync.Compare(existing.Content, content)
		switch decision.Decision {
		case memorysync.DecisionSame:
			_, _ = h.DB.Exec(r.Context(), `
				UPDATE agent_memory_sync_entry
				   SET seen_at = now(), updated_at = now(),
				       source_runtime_id = COALESCE($2, source_runtime_id)
				 WHERE id = $1
			`, parseUUID(existing.ID), runtimeID)
			resp.Skipped++
		case memorysync.DecisionMoreSpecific:
			_, err := h.DB.Exec(r.Context(), `
				UPDATE agent_memory_sync_entry
				   SET content = $2,
				       content_hash = $3,
				       topic = $4,
				       rel_path = CASE WHEN $5 <> '' THEN $5 ELSE rel_path END,
				       source_runtime_id = COALESCE($6, source_runtime_id),
				       metadata = metadata || jsonb_build_object('last_decision', $7::text, 'previous_content', content),
				       seen_at = now(),
				       updated_at = now()
				 WHERE id = $1 AND status = 'active'
			`, parseUUID(existing.ID), content, hash, memorysync.NormalizeTopic(topic), relPath, runtimeID, decision.Decision)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "update memory sync entry failed")
				return
			}
			resp.Updated++
		case memorysync.DecisionOpposed:
			// Strategy A: keep existing active; record opposing content as conflict.
			var conflictCount int
			_ = h.DB.QueryRow(r.Context(), `
				SELECT count(*) FROM agent_memory_sync_entry
				 WHERE agent_id = $1 AND identity_key = $2 AND status = 'conflict' AND content_hash = $3
			`, agentID, identity, hash).Scan(&conflictCount)
			if conflictCount == 0 {
				if err := h.insertMemorySyncEntry(r.Context(), wsUUID, agentID, runtimeID, identity, scope, subjectID, kind, topic, relPath, content, hash, memorysync.StatusConflict, parseUUID(existing.ID)); err != nil {
					writeError(w, http.StatusInternalServerError, "insert conflict memory sync entry failed")
					return
				}
				resp.Conflicts++
			} else {
				resp.Skipped++
			}
			_, _ = h.DB.Exec(r.Context(), `
				UPDATE agent_memory_sync_entry
				   SET seen_at = now(), updated_at = now()
				 WHERE id = $1
			`, parseUUID(existing.ID))
		default:
			resp.Skipped++
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// HydrateAgentMemoryCenter returns active + conflict entries for local materialization.
func (h *Handler) HydrateAgentMemoryCenter(w http.ResponseWriter, r *http.Request) {
	workspaceID := middleware.DaemonWorkspaceIDFromContext(r.Context())
	if workspaceID == "" {
		writeError(w, http.StatusUnauthorized, "daemon workspace required")
		return
	}
	if !h.requireDaemonWorkspaceAccess(w, r, workspaceID) {
		return
	}
	var req protocol.AgentMemoryHydrateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	agentID, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(req.AgentID), "agent_id")
	if !ok {
		return
	}
	wsUUID := parseUUID(workspaceID)
	if _, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{
		ID:          agentID,
		WorkspaceID: wsUUID,
	}); err != nil {
		writeError(w, http.StatusBadRequest, "agent not found in workspace")
		return
	}
	resp := protocol.AgentMemoryHydrateResponse{
		Active:    []protocol.AgentMemoryHydrateEntry{},
		Conflicts: []protocol.AgentMemoryHydrateEntry{},
	}
	if h.DB == nil {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	rows, err := h.DB.Query(r.Context(), `
		SELECT id::text, identity_key, scope, subject_id, kind, topic, rel_path, content, status,
		       COALESCE(conflict_of::text, '')
		  FROM agent_memory_sync_entry
		 WHERE workspace_id = $1 AND agent_id = $2 AND status IN ('active', 'conflict')
		 ORDER BY status ASC, updated_at ASC, id ASC
	`, wsUUID, agentID)
	if err != nil {
		// Table may not exist yet on partially migrated envs.
		if strings.Contains(err.Error(), "agent_memory_sync_entry") {
			writeJSON(w, http.StatusOK, resp)
			return
		}
		writeError(w, http.StatusInternalServerError, "list memory sync entries failed")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var item protocol.AgentMemoryHydrateEntry
		if err := rows.Scan(&item.ID, &item.IdentityKey, &item.Scope, &item.SubjectID, &item.Kind, &item.Topic, &item.RelPath, &item.Content, &item.Status, &item.ConflictOf); err != nil {
			writeError(w, http.StatusInternalServerError, "scan memory sync entry failed")
			return
		}
		switch item.Status {
		case memorysync.StatusConflict:
			resp.Conflicts = append(resp.Conflicts, item)
		default:
			resp.Active = append(resp.Active, item)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) getActiveMemorySyncEntry(ctx context.Context, agentID pgtype.UUID, identityKey string) (*agentMemorySyncRow, error) {
	row := h.DB.QueryRow(ctx, `
		SELECT id::text, identity_key, scope, subject_id, kind, topic, rel_path, content, content_hash, status, conflict_of
		  FROM agent_memory_sync_entry
		 WHERE agent_id = $1 AND identity_key = $2 AND status = 'active'
		 LIMIT 1
	`, agentID, identityKey)
	var out agentMemorySyncRow
	if err := row.Scan(&out.ID, &out.IdentityKey, &out.Scope, &out.SubjectID, &out.Kind, &out.Topic, &out.RelPath, &out.Content, &out.ContentHash, &out.Status, &out.ConflictOf); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		// Undefined table during rollout.
		if strings.Contains(err.Error(), "agent_memory_sync_entry") {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

func (h *Handler) insertMemorySyncEntry(
	ctx context.Context,
	workspaceID, agentID, runtimeID pgtype.UUID,
	identity, scope, subjectID, kind, topic, relPath, content, hash, status string,
	conflictOf pgtype.UUID,
) error {
	meta, _ := json.Marshal(map[string]any{
		"source":    "center_sync",
		"policy":    "strategy_a",
		"synced_at": time.Now().UTC().Format(time.RFC3339),
	})
	_, err := h.DB.Exec(ctx, `
		INSERT INTO agent_memory_sync_entry (
		  workspace_id, agent_id, identity_key, scope, subject_id, kind, topic,
		  rel_path, content, content_hash, status, conflict_of, source_runtime_id, metadata
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14::jsonb)
	`, workspaceID, agentID, identity, scope, subjectID, kind, memorysync.NormalizeTopic(topic), relPath, content, hash, status, conflictOf, runtimeID, meta)
	return err
}
