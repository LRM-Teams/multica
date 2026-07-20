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
		case "pending", "failed", "failed_retryable":
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
		case "provisioning", "credential_ready", "sandbox_creating", "runtime_waiting", "agent_creating":
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
// envDispatchRuntimeReadinessTimeout bounds how long first-address
// provisioning waits for the in-sandbox daemon to register an online Pi
// runtime. A truthful timeout (vs. indefinite polling) lets env-dispatch fail
// closed and compensate owned resources instead of leaving the DAG
// indefinitely in progress.
const envDispatchRuntimeReadinessTimeout = 2 * time.Minute

// provisionEnvDispatchAgent creates the isolated runtime/session pair that an
// EnvDispatch-bound channel agent must use. It intentionally does not call the
// ordinary channel session helper, which would select the agent's shared
// default runtime.
//
// Scratch first-address path (no source sandbox): the sandbox lifecycle mints a
// daemon correlation nonce (ref.DaemonID), env-dispatch waits for the
// daemon-registered online Pi runtime (WaitForOnlineSandboxRuntime), then
// clones a derived global agent bound to it (CloneEnvDispatchAgentTx). No
// agent_runtime row is pre-created - this is the "Pi offline" fix. The branch
// path (source sandbox filesystem clone) still uses the legacy pre-create flow
// because CloneSandboxInstance requires a destination runtime_id; migrating it
// to the pre-create-free discovery flow is a follow-up.
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

	configJSON := in.SandboxConfig
	if len(configJSON) == 0 || string(configJSON) == "{}" {
		configJSON = binding.SandboxConfig
	}
	config, err := decodeEnvDispatchSandboxConfig(configJSON)
	if err != nil {
		_ = store.markFailed(context.WithoutCancel(ctx), h.DB, in.EnvID, in.AgentID, "decode config failed")
		return ProvisionEnvDispatchAgentResult{}, fmt.Errorf("decode binding sandbox config: %w", err)
	}
	lifecycle := newEnvSandboxLifecycleService(h)
	if lifecycle == nil {
		_ = store.markFailed(context.WithoutCancel(ctx), h.DB, in.EnvID, in.AgentID, "lifecycle unavailable")
		return ProvisionEnvDispatchAgentResult{}, fmt.Errorf("sandbox lifecycle unavailable")
	}
	sourceID := in.SourceSandboxInstanceID
	if sourceID == "" && binding.SourceSandboxInstanceID != nil {
		sourceID = *binding.SourceSandboxInstanceID
	}
	if sourceID != "" {
		return h.provisionEnvDispatchAgentBranch(ctx, in, store, config, lifecycle, sourceID)
	}

	// Scratch first-address: create sandbox (service mints daemon nonce on
	// ref.DaemonID), discover the online runtime, clone the derived agent.
	createInput, err := config.createInput(in.WorkspaceID, "")
	if err != nil {
		_ = store.markFailed(context.WithoutCancel(ctx), h.DB, in.EnvID, in.AgentID, "build create input failed")
		return ProvisionEnvDispatchAgentResult{}, err
	}
	ref, err := lifecycle.Create(ctx, createInput, in.UserID)
	if err != nil {
		_ = store.markFailed(context.WithoutCancel(ctx), h.DB, in.EnvID, in.AgentID, "sandbox create failed")
		return ProvisionEnvDispatchAgentResult{}, fmt.Errorf("create sandbox: %w", err)
	}
	cleanup := func(cause error) (ProvisionEnvDispatchAgentResult, error) {
		_ = lifecycle.Delete(context.WithoutCancel(ctx), ref, in.UserID)
		_ = store.markFailed(context.WithoutCancel(ctx), h.DB, in.EnvID, in.AgentID, "provisioning failed")
		return ProvisionEnvDispatchAgentResult{}, cause
	}
	runtimeRef, err := service.WaitForOnlineSandboxRuntime(ctx, &envSandboxLifecycleDepsAdapter{h: h}, in.WorkspaceID, ref.DaemonID, ref.InstanceID, envDispatchRuntimeReadinessTimeout)
	if err != nil {
		return cleanup(fmt.Errorf("wait for online sandbox runtime: %w", err))
	}
	derivedID, err := CloneEnvDispatchAgentTx(ctx, h, service.CloneEnvDispatchAgentInput{
		WorkspaceID:   in.WorkspaceID,
		SourceAgentID: in.AgentID,
		RuntimeID:     runtimeRef.ID,
		EnvID:         in.EnvID,
		ChannelID:     in.ChannelID,
		BindingID:     binding.ID,
	})
	if err != nil {
		return cleanup(fmt.Errorf("clone derived agent: %w", err))
	}
	var sessionID string
	if err := h.DB.QueryRow(ctx, `
		INSERT INTO chat_session (workspace_id, project_id, agent_id, creator_id, title, runtime_id)
		VALUES ($1, $2, $3, $4, 'env-dispatch', $5) RETURNING id::text`,
		in.WorkspaceID, in.ProjectID, derivedID, in.UserID, runtimeRef.ID).Scan(&sessionID); err != nil {
		return cleanup(fmt.Errorf("create env-dispatch chat session: %w", err))
	}
	if _, err := h.DB.Exec(ctx, `INSERT INTO channel_agent_session (channel_id, agent_id, chat_session_id) VALUES ($1, $2, $3)`, in.ChannelID, derivedID, sessionID); err != nil {
		_, _ = h.DB.Exec(context.WithoutCancel(ctx), `DELETE FROM chat_session WHERE id = $1`, sessionID)
		return cleanup(fmt.Errorf("create env-dispatch channel session: %w", err))
	}
	if err := store.markReady(ctx, h.DB, in.EnvID, in.AgentID, ref.InstanceID, runtimeRef.ID, ref.DaemonID); err != nil {
		_, _ = h.DB.Exec(context.WithoutCancel(ctx), `DELETE FROM channel_agent_session WHERE channel_id = $1 AND agent_id = $2`, in.ChannelID, derivedID)
		_, _ = h.DB.Exec(context.WithoutCancel(ctx), `DELETE FROM chat_session WHERE id = $1`, sessionID)
		return cleanup(fmt.Errorf("mark env-dispatch binding ready: %w", err))
	}
	return ProvisionEnvDispatchAgentResult{SandboxInstanceID: ref.InstanceID, RuntimeID: runtimeRef.ID, DaemonID: ref.DaemonID, ChatSessionID: sessionID}, nil
}

// provisionEnvDispatchAgentBranch handles the branch-trigger path (cloning a
// source sandbox filesystem). It retains the legacy pre-create-runtime flow
// because CloneSandboxInstance requires a destination runtime_id; migrating it
// to the pre-create-free discovery flow is a follow-up.
func (h *Handler) provisionEnvDispatchAgentBranch(ctx context.Context, in ProvisionEnvDispatchAgentInput, store envDispatchChannelStore, config envDispatchSandboxConfig, lifecycle *service.EnvSandboxLifecycleService, sourceID string) (ProvisionEnvDispatchAgentResult, error) {
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
	createInput, err := config.createInput(in.WorkspaceID, daemonID)
	if err != nil {
		return cleanup(err)
	}
	createJSON, mErr := json.Marshal(createInput)
	if mErr != nil {
		return cleanup(fmt.Errorf("encode clone create payload: %w", mErr))
	}
	ref, err := lifecycle.CloneSandboxInstance(ctx,
		service.SandboxInstanceRef{WorkspaceID: in.WorkspaceID, InstanceID: sourceID},
		service.CloneSandboxInstanceInput{
			WorkspaceID:   in.WorkspaceID,
			EnvID:         in.EnvID,
			AgentID:       in.AgentID,
			RuntimeID:     runtimeID,
			DaemonID:      daemonID,
			CreatePayload: createJSON,
		}, in.UserID)
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
