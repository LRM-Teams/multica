package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const agentCredentialActivityCreated = "agent_credential_created"
const agentCredentialActivityDaemonEnsured = "agent_credential_daemon_ensured"
const daemonAgentCredentialTTL = 24 * time.Hour

type CreateAgentCredentialRequest struct {
	ExpiresInDays *int `json:"expires_in_days"`
}

type AgentCredentialResponse struct {
	ID         string  `json:"id"`
	AgentID    string  `json:"agent_id"`
	Prefix     string  `json:"token_prefix"`
	ExpiresAt  *string `json:"expires_at"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  string  `json:"created_at"`
}

type CreateAgentCredentialResponse struct {
	AgentCredentialResponse
	Token string `json:"token"`
}

func agentCredentialToResponse(credential db.AgentCredential) AgentCredentialResponse {
	return AgentCredentialResponse{
		ID:         uuidToString(credential.ID),
		AgentID:    uuidToString(credential.AgentID),
		Prefix:     credential.TokenPrefix,
		ExpiresAt:  timestampToPtr(credential.ExpiresAt),
		LastUsedAt: timestampToPtr(credential.LastUsedAt),
		CreatedAt:  timestampToString(credential.CreatedAt),
	}
}

func tokenPrefix(token string) string {
	if len(token) <= 12 {
		return token
	}
	return token[:12]
}

func (h *Handler) authorizeAgentCredentialIssuance(w http.ResponseWriter, r *http.Request) (db.Agent, db.AgentRuntime, db.Member, bool) {
	agentID := chi.URLParam(r, "id")
	agent, ok := h.loadAgentForUser(w, r, agentID)
	if !ok {
		return db.Agent{}, db.AgentRuntime{}, db.Member{}, false
	}

	workspaceID := uuidToString(agent.WorkspaceID)
	userID := requestUserID(r)
	actorType, _ := h.resolveActor(r, userID, workspaceID)
	if actorType == "agent" {
		writeError(w, http.StatusForbidden, "agents may not issue agent credentials")
		return db.Agent{}, db.AgentRuntime{}, db.Member{}, false
	}

	runtime, err := h.Queries.GetAgentRuntimeForWorkspace(r.Context(), db.GetAgentRuntimeForWorkspaceParams{
		ID:          agent.RuntimeID,
		WorkspaceID: agent.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "agent runtime not found")
		return db.Agent{}, db.AgentRuntime{}, db.Member{}, false
	}
	member, ok := h.requireWorkspaceRole(w, r, workspaceID, "agent not found", "owner", "admin", "member")
	if !ok {
		return db.Agent{}, db.AgentRuntime{}, db.Member{}, false
	}
	if !runtime.OwnerID.Valid {
		writeError(w, http.StatusConflict, "agent runtime has no owning user")
		return db.Agent{}, db.AgentRuntime{}, db.Member{}, false
	}
	if !roleAllowed(member.Role, "owner", "admin") && uuidToString(runtime.OwnerID) != userID {
		writeError(w, http.StatusForbidden, "only workspace owner/admin or the runtime owner can issue agent credentials")
		return db.Agent{}, db.AgentRuntime{}, db.Member{}, false
	}
	return agent, runtime, member, true
}

func (h *Handler) CreateAgentCredential(w http.ResponseWriter, r *http.Request) {
	agent, runtime, member, ok := h.authorizeAgentCredentialIssuance(w, r)
	if !ok {
		return
	}

	var req CreateAgentCredentialRequest
	rawFields, err := decodeJSONBodyWithRawFields(r.Body, &req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	for _, field := range []string{"agent_id", "workspace_id", "user_id"} {
		if _, ok := rawFields[field]; ok {
			writeError(w, http.StatusBadRequest, field+" is derived from the agent and request context")
			return
		}
	}
	if req.ExpiresInDays != nil && *req.ExpiresInDays < 0 {
		writeError(w, http.StatusBadRequest, "expires_in_days must be non-negative")
		return
	}

	rawToken, err := auth.GenerateAgentCredentialToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate agent credential")
		return
	}

	var expiresAt pgtype.Timestamptz
	if req.ExpiresInDays != nil && *req.ExpiresInDays > 0 {
		expiresAt = pgtype.Timestamptz{
			Time:  time.Now().Add(time.Duration(*req.ExpiresInDays) * 24 * time.Hour),
			Valid: true,
		}
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		slog.Error("agent credential create: begin tx failed",
			append(logger.RequestAttrs(r), "error", err, "agent_id", uuidToString(agent.ID))...)
		writeError(w, http.StatusInternalServerError, "failed to create agent credential")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	credential, err := qtx.CreateAgentCredential(r.Context(), db.CreateAgentCredentialParams{
		TokenHash:   auth.HashToken(rawToken),
		TokenPrefix: tokenPrefix(rawToken),
		AgentID:     agent.ID,
		WorkspaceID: agent.WorkspaceID,
		UserID:      runtime.OwnerID,
		ExpiresAt:   expiresAt,
	})
	if err != nil {
		slog.Error("agent credential create: insert failed",
			append(logger.RequestAttrs(r), "error", err, "agent_id", uuidToString(agent.ID))...)
		writeError(w, http.StatusInternalServerError, "failed to create agent credential")
		return
	}

	details, _ := json.Marshal(map[string]any{
		"agent_id":                uuidToString(agent.ID),
		"agent_name":              agentDisplayName(agent),
		"agent_credential_id":     uuidToString(credential.ID),
		"agent_credential_prefix": credential.TokenPrefix,
		"expires_at":              timestampToPtr(credential.ExpiresAt),
	})
	if _, err := qtx.CreateActivity(r.Context(), db.CreateActivityParams{
		WorkspaceID: agent.WorkspaceID,
		IssueID:     pgtype.UUID{},
		ActorType:   pgtype.Text{String: "member", Valid: true},
		ActorID:     member.UserID,
		Action:      agentCredentialActivityCreated,
		Details:     details,
	}); err != nil {
		slog.Error("agent credential create: audit write failed; rolling back",
			append(logger.RequestAttrs(r), "error", err, "agent_id", uuidToString(agent.ID))...)
		writeError(w, http.StatusInternalServerError, "audit log write failed; credential create rolled back")
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		slog.Error("agent credential create: tx commit failed",
			append(logger.RequestAttrs(r), "error", err, "agent_id", uuidToString(agent.ID))...)
		writeError(w, http.StatusInternalServerError, "failed to create agent credential")
		return
	}

	writeJSON(w, http.StatusCreated, CreateAgentCredentialResponse{
		AgentCredentialResponse: agentCredentialToResponse(credential),
		Token:                   rawToken,
	})
}

func (h *Handler) EnsureDaemonAgentCredential(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	runtime, ok := h.requireDaemonRuntimeAccess(w, r, runtimeID)
	if !ok {
		return
	}
	if middleware.DaemonAuthPathFromContext(r.Context()) != middleware.DaemonAuthPathDaemonToken {
		writeError(w, http.StatusForbidden, "daemon credential provisioning requires daemon token")
		return
	}
	daemonID := middleware.DaemonIDFromContext(r.Context())
	if daemonID == "" {
		writeError(w, http.StatusForbidden, "daemon credential provisioning requires daemon identity")
		return
	}
	if !runtime.DaemonID.Valid || runtime.DaemonID.String != daemonID {
		writeError(w, http.StatusForbidden, "daemon token is not bound to this runtime")
		return
	}
	if !runtime.OwnerID.Valid {
		writeError(w, http.StatusConflict, "agent runtime has no owning user")
		return
	}

	agentIDParam := chi.URLParam(r, "agentId")
	agentUUID, ok := parseUUIDOrBadRequest(w, agentIDParam, "agent_id")
	if !ok {
		return
	}
	agent, err := h.Queries.GetAgent(r.Context(), agentUUID)
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, "agent not found")
			return
		}
		slog.Warn("daemon agent credential ensure: load agent failed",
			append(logger.RequestAttrs(r), "error", err, "agent_id", agentIDParam, "runtime_id", runtimeID)...)
		writeError(w, http.StatusInternalServerError, "failed to load agent")
		return
	}
	if agent.ArchivedAt.Valid {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}
	if uuidToString(agent.WorkspaceID) != uuidToString(runtime.WorkspaceID) || uuidToString(agent.RuntimeID) != uuidToString(runtime.ID) {
		writeError(w, http.StatusForbidden, "agent is not bound to this runtime")
		return
	}

	rawToken, err := auth.GenerateAgentCredentialToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate agent credential")
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		slog.Error("daemon agent credential ensure: begin tx failed",
			append(logger.RequestAttrs(r), "error", err, "agent_id", uuidToString(agent.ID), "runtime_id", runtimeID)...)
		writeError(w, http.StatusInternalServerError, "failed to create agent credential")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	credential, err := qtx.CreateAgentCredential(r.Context(), db.CreateAgentCredentialParams{
		TokenHash:   auth.HashToken(rawToken),
		TokenPrefix: tokenPrefix(rawToken),
		AgentID:     agent.ID,
		WorkspaceID: agent.WorkspaceID,
		UserID:      runtime.OwnerID,
		ExpiresAt:   pgtype.Timestamptz{Time: time.Now().Add(daemonAgentCredentialTTL), Valid: true},
	})
	if err != nil {
		slog.Error("daemon agent credential ensure: insert failed",
			append(logger.RequestAttrs(r), "error", err, "agent_id", uuidToString(agent.ID), "runtime_id", runtimeID)...)
		writeError(w, http.StatusInternalServerError, "failed to create agent credential")
		return
	}

	details, _ := json.Marshal(map[string]any{
		"source":                  "daemon_runtime_ensure",
		"daemon_id":               daemonID,
		"runtime_id":              uuidToString(runtime.ID),
		"agent_id":                uuidToString(agent.ID),
		"agent_name":              agentDisplayName(agent),
		"agent_credential_id":     uuidToString(credential.ID),
		"agent_credential_prefix": credential.TokenPrefix,
		"expires_at":              timestampToPtr(credential.ExpiresAt),
	})
	if _, err := qtx.CreateActivity(r.Context(), db.CreateActivityParams{
		WorkspaceID: agent.WorkspaceID,
		IssueID:     pgtype.UUID{},
		ActorType:   pgtype.Text{String: "system", Valid: true},
		ActorID:     pgtype.UUID{},
		Action:      agentCredentialActivityDaemonEnsured,
		Details:     details,
	}); err != nil {
		slog.Error("daemon agent credential ensure: audit write failed; rolling back",
			append(logger.RequestAttrs(r), "error", err, "agent_id", uuidToString(agent.ID), "runtime_id", runtimeID)...)
		writeError(w, http.StatusInternalServerError, "audit log write failed; credential create rolled back")
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		slog.Error("daemon agent credential ensure: tx commit failed",
			append(logger.RequestAttrs(r), "error", err, "agent_id", uuidToString(agent.ID), "runtime_id", runtimeID)...)
		writeError(w, http.StatusInternalServerError, "failed to create agent credential")
		return
	}

	writeJSON(w, http.StatusCreated, CreateAgentCredentialResponse{
		AgentCredentialResponse: agentCredentialToResponse(credential),
		Token:                   rawToken,
	})
}
