package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type ProvisionEnvDispatchAgentInput struct {
	WorkspaceID, UserID, EnvID, ProjectID, ChannelID, AgentID string
	SourceSandboxInstanceID                                   string
	SandboxConfig                                             json.RawMessage
}

// routeEnvDispatchChannelAgent returns handled=false only for ordinary
// channels. A bound EnvDispatch agent always receives its binding session and
// never reaches ensureChannelAgentSessionWithDB, which could select the shared
// default runtime.
func (h *Handler) routeEnvDispatchChannelAgent(ctx context.Context, qtx *db.Queries, exec db.DBTX, channelID, workspaceID string, agentID pgtype.UUID, userID pgtype.UUID) (db.ChatSession, envAgentSandboxBinding, bool, error) {
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	store := envDispatchChannelStore{}
	binding, err := scanEnvAgentSandboxBinding(exec.QueryRow(ctx, bindingSelect+` WHERE channel_id = $1 AND agent_id = $2`, channelID, agentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return db.ChatSession{}, envAgentSandboxBinding{}, false, nil
	}
	if err != nil {
		return db.ChatSession{}, envAgentSandboxBinding{}, true, fmt.Errorf("load env-dispatch binding: %w", err)
	}

	for {
		switch binding.Status {
		case "ready":
			var sessionID pgtype.UUID
			if err := exec.QueryRow(ctx, `SELECT chat_session_id FROM channel_agent_session WHERE channel_id = $1 AND agent_id = $2`, channelID, agentID).Scan(&sessionID); err != nil {
				return db.ChatSession{}, binding, true, fmt.Errorf("load env-dispatch channel session: %w", err)
			}
			session, err := qtx.GetChatSession(ctx, sessionID)
			if err != nil {
				return db.ChatSession{}, binding, true, fmt.Errorf("load env-dispatch chat session: %w", err)
			}
			if binding.RuntimeID == nil || uuidToString(session.RuntimeID) != *binding.RuntimeID {
				return db.ChatSession{}, binding, true, fmt.Errorf("env-dispatch channel session runtime does not match binding")
			}
			return session, binding, true, nil
		case "pending", "failed":
			var projectID string
			if err := h.DB.QueryRow(ctx, `SELECT project_id::text FROM channel WHERE id = $1 AND workspace_id = $2`, channelID, workspaceID).Scan(&projectID); err != nil {
				return db.ChatSession{}, binding, true, fmt.Errorf("load env-dispatch channel project: %w", err)
			}
			_, err := h.provisionEnvDispatchAgent(waitCtx, ProvisionEnvDispatchAgentInput{
				WorkspaceID:   workspaceID,
				UserID:        uuidToString(userID),
				EnvID:         binding.EnvID,
				ProjectID:     projectID,
				ChannelID:     channelID,
				AgentID:       uuidToString(agentID),
				SandboxConfig: binding.SandboxConfig,
			})
			if err != nil {
				return db.ChatSession{}, binding, true, err
			}
			binding, err = store.binding(ctx, h.DB, binding.EnvID, uuidToString(agentID))
			if err != nil {
				return db.ChatSession{}, envAgentSandboxBinding{}, true, err
			}
		case "provisioning":
			select {
			case <-waitCtx.Done():
				return db.ChatSession{}, binding, true, waitCtx.Err()
			case <-time.After(50 * time.Millisecond):
			}
			binding, err = store.binding(ctx, h.DB, binding.EnvID, uuidToString(agentID))
			if err != nil {
				return db.ChatSession{}, envAgentSandboxBinding{}, true, err
			}
		case "deleting":
			return db.ChatSession{}, binding, true, fmt.Errorf("env-dispatch binding is deleting")
		default:
			return db.ChatSession{}, binding, true, fmt.Errorf("unexpected env-dispatch binding status %q", binding.Status)
		}
	}
}

type ProvisionEnvDispatchAgentResult struct {
	SandboxInstanceID, RuntimeID, DaemonID, ChatSessionID string
}

// provisionEnvDispatchAgent creates the isolated runtime/session pair that an
// EnvDispatch-bound channel agent must use. It intentionally does not call the
// ordinary channel session helper, which would select the agent's shared
// default runtime.
func (h *Handler) provisionEnvDispatchAgent(ctx context.Context, in ProvisionEnvDispatchAgentInput) (ProvisionEnvDispatchAgentResult, error) {
	store := envDispatchChannelStore{}
	won, binding, err := store.claimProvisioning(ctx, h.DB, in.EnvID, in.AgentID)
	if err != nil {
		return ProvisionEnvDispatchAgentResult{}, fmt.Errorf("claim env-dispatch binding: %w", err)
	}
	if !won {
		if binding.Status == "ready" && binding.SandboxInstanceID != nil && binding.RuntimeID != nil && binding.DaemonID != nil {
			var sessionID string
			err := h.DB.QueryRow(ctx, `SELECT chat_session_id::text FROM channel_agent_session WHERE channel_id = $1 AND agent_id = $2`, in.ChannelID, in.AgentID).Scan(&sessionID)
			if err == nil {
				return ProvisionEnvDispatchAgentResult{SandboxInstanceID: *binding.SandboxInstanceID, RuntimeID: *binding.RuntimeID, DaemonID: *binding.DaemonID, ChatSessionID: sessionID}, nil
			}
		}
		return ProvisionEnvDispatchAgentResult{}, fmt.Errorf("env-dispatch binding is %s", binding.Status)
	}

	runtimeID, daemonID, err := (&envDispatchDepsAdapter{h: h}).PrecreateAgentRuntime(ctx, in.WorkspaceID, in.UserID, in.AgentID)
	if err != nil {
		_ = store.markFailed(context.WithoutCancel(ctx), h.DB, in.EnvID, in.AgentID, "precreate runtime failed")
		return ProvisionEnvDispatchAgentResult{}, err
	}
	cleanup := func(cause error) (ProvisionEnvDispatchAgentResult, error) {
		_ = (&envDispatchDepsAdapter{h: h}).DeleteAgentRuntime(context.WithoutCancel(ctx), in.WorkspaceID, runtimeID)
		_ = store.markFailed(context.WithoutCancel(ctx), h.DB, in.EnvID, in.AgentID, "provisioning failed")
		return ProvisionEnvDispatchAgentResult{}, cause
	}

	configJSON := in.SandboxConfig
	if len(configJSON) == 0 || string(configJSON) == "{}" {
		configJSON = binding.SandboxConfig
	}
	var config struct {
		Template string `json:"template"`
	}
	_ = json.Unmarshal(configJSON, &config)
	if config.Template == "" {
		config.Template = "default"
	}
	lifecycle := newEnvSandboxLifecycleService(h)
	if lifecycle == nil {
		return cleanup(fmt.Errorf("sandbox lifecycle unavailable"))
	}
	ref, err := lifecycle.Create(ctx, service.CreateSandboxInstanceInput{WorkspaceID: in.WorkspaceID, Template: config.Template, DaemonEnabled: true, RuntimeEnv: map[string]string{"MULTICA_DAEMON_ID": daemonID}}, in.UserID)
	if err != nil {
		return cleanup(err)
	}
	var sessionID string
	if err := h.DB.QueryRow(ctx, `
		INSERT INTO chat_session (workspace_id, project_id, agent_id, creator_id, title, runtime_id)
		VALUES ($1, $2, $3, $4, 'env-dispatch', $5) RETURNING id::text`,
		in.WorkspaceID, in.ProjectID, in.AgentID, in.UserID, runtimeID).Scan(&sessionID); err != nil {
		_ = lifecycle.Delete(ctx, ref, in.UserID)
		return cleanup(err)
	}
	if _, err := h.DB.Exec(ctx, `INSERT INTO channel_agent_session (channel_id, agent_id, chat_session_id) VALUES ($1, $2, $3)`, in.ChannelID, in.AgentID, sessionID); err != nil {
		_, _ = h.DB.Exec(context.WithoutCancel(ctx), `DELETE FROM chat_session WHERE id = $1`, sessionID)
		_ = lifecycle.Delete(ctx, ref, in.UserID)
		return cleanup(err)
	}
	if err := store.markReady(ctx, h.DB, in.EnvID, in.AgentID, ref.InstanceID, runtimeID, daemonID); err != nil {
		_, _ = h.DB.Exec(context.WithoutCancel(ctx), `DELETE FROM channel_agent_session WHERE channel_id = $1 AND agent_id = $2`, in.ChannelID, in.AgentID)
		_, _ = h.DB.Exec(context.WithoutCancel(ctx), `DELETE FROM chat_session WHERE id = $1`, sessionID)
		_ = lifecycle.Delete(context.WithoutCancel(ctx), ref, in.UserID)
		return cleanup(err)
	}
	return ProvisionEnvDispatchAgentResult{SandboxInstanceID: ref.InstanceID, RuntimeID: runtimeID, DaemonID: daemonID, ChatSessionID: sessionID}, nil
}
