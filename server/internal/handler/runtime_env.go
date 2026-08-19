package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/logger"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	runtimeEnvActivityRevealed = "runtime_env_revealed"
	runtimeEnvActivityUpdated  = "runtime_env_updated"
)

// RuntimeEnvResponse is the wire shape for GET /api/runtimes/{id}/env.
type RuntimeEnvResponse struct {
	RuntimeID string            `json:"runtime_id"`
	CustomEnv map[string]string `json:"custom_env"`
}

// UpdateRuntimeEnvRequest is the wire shape for PUT /api/runtimes/{id}/env.
// Only custom_env is accepted; values equal to "****" preserve the existing
// value for that key (same **** sentinel contract as agent env).
type UpdateRuntimeEnvRequest struct {
	CustomEnv map[string]string `json:"custom_env"`
}

// authorizeRuntimeEnv loads the runtime and gates access to owner / workspace
// admin / runtime owner. Agent actors are denied. Returns the runtime and the
// authorizing member.
func (h *Handler) authorizeRuntimeEnv(w http.ResponseWriter, r *http.Request) (db.AgentRuntime, db.Member, bool) {
	runtimeID := chi.URLParam(r, "runtimeId")
	runtimeUUID, ok := parseUUIDOrBadRequest(w, runtimeID, "runtime_id")
	if !ok {
		return db.AgentRuntime{}, db.Member{}, false
	}
	rt, err := h.Queries.GetAgentRuntime(r.Context(), runtimeUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "runtime not found")
		return db.AgentRuntime{}, db.Member{}, false
	}

	workspaceID := uuidToString(rt.WorkspaceID)
	userID := requestUserID(r)

	actorType, _ := h.resolveActor(r, userID, workspaceID)
	if actorType == "agent" {
		writeError(w, http.StatusForbidden, "agents may not access env management endpoints")
		return db.AgentRuntime{}, db.Member{}, false
	}

	member, ok := h.requireWorkspaceMember(w, r, workspaceID, "runtime not found")
	if !ok {
		return db.AgentRuntime{}, db.Member{}, false
	}
	if !canEditRuntime(member, rt) {
		writeError(w, http.StatusForbidden, "you can only manage env on your own runtimes")
		return db.AgentRuntime{}, db.Member{}, false
	}

	return rt, member, true
}

// GetRuntimeEnv returns the plaintext custom_env map for a single runtime.
// Owner/admin/runtime-owner only; every reveal writes an audit row.
func (h *Handler) GetRuntimeEnv(w http.ResponseWriter, r *http.Request) {
	rt, member, ok := h.authorizeRuntimeEnv(w, r)
	if !ok {
		return
	}

	customEnv := unmarshalRuntimeCustomEnv(rt)

	revealedKeys := sortedKeys(customEnv)
	details, _ := json.Marshal(map[string]any{
		"runtime_id":    uuidToString(rt.ID),
		"runtime_name":  rt.Name,
		"revealed_keys": revealedKeys,
		"key_count":     len(revealedKeys),
	})
	if _, err := h.Queries.CreateActivity(r.Context(), db.CreateActivityParams{
		WorkspaceID: rt.WorkspaceID,
		IssueID:     pgtype.UUID{},
		ActorType:   pgtype.Text{String: "member", Valid: true},
		ActorID:     parseUUID(uuidToString(member.UserID)),
		Action:      runtimeEnvActivityRevealed,
		Details:     details,
	}); err != nil {
		slog.Error("runtime_env_revealed audit write failed; refusing to serve plaintext",
			append(logger.RequestAttrs(r), "error", err, "runtime_id", uuidToString(rt.ID))...)
		writeError(w, http.StatusInternalServerError, "audit log write failed; refusing to serve env without a recorded reveal")
		return
	}

	writeJSON(w, http.StatusOK, RuntimeEnvResponse{
		RuntimeID: uuidToString(rt.ID),
		CustomEnv: customEnv,
	})
}

// UpdateRuntimeEnv replaces a runtime's custom_env wholesale, honouring the
// **** sentinel per-key, inside a single transaction with its audit row.
func (h *Handler) UpdateRuntimeEnv(w http.ResponseWriter, r *http.Request) {
	rt, member, ok := h.authorizeRuntimeEnv(w, r)
	if !ok {
		return
	}

	var req UpdateRuntimeEnvRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.CustomEnv == nil {
		req.CustomEnv = map[string]string{}
	}

	existing := unmarshalRuntimeCustomEnv(rt)
	merged, audit := mergeAgentEnv(existing, req.CustomEnv)

	envBytes, err := json.Marshal(merged)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode env")
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		slog.Error("runtime_env update: begin tx failed",
			append(logger.RequestAttrs(r), "error", err, "runtime_id", uuidToString(rt.ID))...)
		writeError(w, http.StatusInternalServerError, "failed to update env")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	updated, err := qtx.UpdateAgentRuntimeCustomEnv(r.Context(), db.UpdateAgentRuntimeCustomEnvParams{
		ID:        rt.ID,
		CustomEnv: envBytes,
	})
	if err != nil {
		slog.Warn("update runtime custom_env failed",
			append(logger.RequestAttrs(r), "error", err, "runtime_id", uuidToString(rt.ID))...)
		writeError(w, http.StatusInternalServerError, "failed to update env")
		return
	}

	auditDetails := map[string]any{
		"runtime_id":     uuidToString(rt.ID),
		"runtime_name":   rt.Name,
		"added_keys":     audit.added,
		"removed_keys":   audit.removed,
		"changed_keys":   audit.changed,
		"preserved_keys": audit.preserved,
	}
	details, _ := json.Marshal(auditDetails)
	if _, err := qtx.CreateActivity(r.Context(), db.CreateActivityParams{
		WorkspaceID: rt.WorkspaceID,
		IssueID:     pgtype.UUID{},
		ActorType:   pgtype.Text{String: "member", Valid: true},
		ActorID:     parseUUID(uuidToString(member.UserID)),
		Action:      runtimeEnvActivityUpdated,
		Details:     details,
	}); err != nil {
		slog.Error("runtime_env_updated audit write failed; rolling back update",
			append(logger.RequestAttrs(r), "error", err, "runtime_id", uuidToString(rt.ID))...)
		writeError(w, http.StatusInternalServerError, "audit log write failed; env update rolled back")
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		slog.Error("runtime_env update: tx commit failed",
			append(logger.RequestAttrs(r), "error", err, "runtime_id", uuidToString(rt.ID))...)
		writeError(w, http.StatusInternalServerError, "failed to update env")
		return
	}

	// Notify connected clients that runtime metadata changed so the list/detail
	// pages refresh their "N variables configured" indicator — matches
	// UpdateAgentRuntime. No env values are sent.
	h.publish(protocol.EventDaemonRegister, uuidToString(rt.WorkspaceID), "member", uuidToString(member.UserID), map[string]any{
		"action": "update",
	})

	writeJSON(w, http.StatusOK, RuntimeEnvResponse{
		RuntimeID: uuidToString(updated.ID),
		CustomEnv: merged,
	})
}

// unmarshalRuntimeCustomEnv decodes a runtime's stored custom_env bytea into
// a map, returning an empty (never nil) map.
func unmarshalRuntimeCustomEnv(rt db.AgentRuntime) map[string]string {
	out := map[string]string{}
	if len(rt.CustomEnv) == 0 {
		return out
	}
	if err := json.Unmarshal(rt.CustomEnv, &out); err != nil {
		slog.Warn("failed to unmarshal runtime custom_env", "runtime_id", uuidToString(rt.ID), "error", err)
		return map[string]string{}
	}
	if out == nil {
		return map[string]string{}
	}
	return out
}
