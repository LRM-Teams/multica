package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/arealrl"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
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
			executionAgentID := envDispatchBindingExecutionAgentID(binding)
			var sessionID pgtype.UUID
			if err := exec.QueryRow(ctx, `SELECT chat_session_id FROM channel_agent_session WHERE channel_id = $1 AND agent_id = $2`, channelID, executionAgentID).Scan(&sessionID); err != nil {
				return db.ChatSession{}, binding, true, fmt.Errorf("load env-dispatch channel session: %w", err)
			}
			session, err := qtx.GetChatSession(ctx, sessionID)
			if err != nil {
				return db.ChatSession{}, binding, true, fmt.Errorf("load env-dispatch chat session: %w", err)
			}
			if binding.RuntimeID == nil {
				return db.ChatSession{}, binding, true, fmt.Errorf("env-dispatch binding has no runtime")
			}
			if sessionRuntimeID := uuidToString(session.RuntimeID); sessionRuntimeID != *binding.RuntimeID {
				// The legacy branch flow pre-creates a placeholder runtime for the
				// sandbox clone, but the daemon registers its real online runtime
				// on boot and inbox completion re-points the chat session to it
				// (agent_inbox.go UpdateChatSessionSession). The session's runtime
				// is the live one — the scratch flow records exactly that on the
				// binding — so heal the binding instead of rejecting every later
				// channel message with "session runtime does not match binding".
				if _, err := exec.Exec(ctx, `
					UPDATE environment_agent_sandbox
					SET runtime_id = $1, updated_at = now()
					WHERE env_id = $2 AND agent_id = $3`,
					session.RuntimeID, binding.EnvID, agentID); err != nil {
					return db.ChatSession{}, binding, true, fmt.Errorf("heal env-dispatch binding runtime: %w", err)
				}
				binding.RuntimeID = &sessionRuntimeID
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

func envDispatchBindingExecutionAgentID(binding envAgentSandboxBinding) string {
	if binding.DerivedAgentID != nil && *binding.DerivedAgentID != "" {
		return *binding.DerivedAgentID
	}
	return binding.SourceAgentID
}

type ProvisionEnvDispatchAgentResult struct {
	AgentID, SandboxInstanceID, RuntimeID, DaemonID, ChatSessionID string
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

// envDispatchDerivedAgentEnabled gates the derived-agent provisioning path
// (ARE-5 feature flag, openspec env-dispatch-agent-runtime-config Task 8.3).
// Set ENV_DISPATCH_DERIVED_AGENT=false to disable new derived-agent
// provisioning as a rollout kill-switch. Default true: the scratch
// first-address path is already fully migrated to the derived flow (no legacy
// pre-create scratch path remains), so defaulting false would reject all
// scratch provisioning and break the suite. Prod rollout should set the env
// explicitly; see verify report for the open default decision.
func envDispatchDerivedAgentEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("ENV_DISPATCH_DERIVED_AGENT")))
	if v == "" {
		return true
	}
	return v != "false" && v != "0" && v != "no"
}

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
	if !envDispatchDerivedAgentEnabled() {
		return ProvisionEnvDispatchAgentResult{}, fmt.Errorf("env-dispatch derived-agent provisioning is disabled")
	}
	store := envDispatchChannelStore{}
	won, binding, err := store.claimProvisioning(ctx, h.DB, in.EnvID, in.AgentID)
	if err != nil {
		return ProvisionEnvDispatchAgentResult{}, fmt.Errorf("claim env-dispatch binding: %w", err)
	}
	if !won {
		if binding.Status == "ready" && binding.SandboxInstanceID != nil && binding.RuntimeID != nil && binding.DaemonID != nil {
			executionAgentID := envDispatchBindingExecutionAgentID(binding)
			var sessionID string
			err := h.DB.QueryRow(ctx, `SELECT chat_session_id::text FROM channel_agent_session WHERE channel_id = $1 AND agent_id = $2`, in.ChannelID, executionAgentID).Scan(&sessionID)
			if err == nil {
				return ProvisionEnvDispatchAgentResult{AgentID: executionAgentID, SandboxInstanceID: *binding.SandboxInstanceID, RuntimeID: *binding.RuntimeID, DaemonID: *binding.DaemonID, ChatSessionID: sessionID}, nil
			}
		}
		return ProvisionEnvDispatchAgentResult{}, fmt.Errorf("env-dispatch binding is %s", binding.Status)
	}

	// Credential owner invariant (spec AC-4): a binding's model credential owner
	// must equal its source agent. A mismatch means a credential was resolved for
	// a different source agent and must never reach the sandbox; fail closed and
	// leave the binding retryable so the caller can re-resolve correctly.
	if err := validateEnvDispatchCredentialOwner(binding, in.AgentID); err != nil {
		_ = store.markFailed(context.WithoutCancel(ctx), h.DB, in.EnvID, in.AgentID, "model credential owner mismatch")
		return ProvisionEnvDispatchAgentResult{}, err
	}

	// AC-4: training source agent -> training provisioning branch. The training
	// session is opened with session_ref=binding.ID BEFORE sandbox creation,
	// persisted for retry reuse, and the sandbox uses a server-owned
	// areal-default + bridge URL + session-key runtime. The real task is
	// enqueued by the service layer after provisioning and linked to the
	// session for DAG assembly.
	if h.isEnvDispatchTrainingTarget(ctx, in.ProjectID, in.AgentID) {
		return h.provisionEnvDispatchAgentTraining(ctx, in, store, binding)
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
	// AC-4: a training source opens its RL session BEFORE sandbox creation with
	// the persistent binding ID as session_ref, persists it on the binding (so
	// retries reuse it), and overrides the sandbox config with areal-default +
	// the configured bridge URL + the returned session key. Non-training sources
	// keep the caller-supplied config. spec [需澄清]#3 locked defaults.
	if h.TaskService != nil && h.TaskService.IsTrainingTarget(ctx, in.ProjectID, in.AgentID) {
		sessionID, sessionKey := "", ""
		if binding.TrainingSessionID != nil && *binding.TrainingSessionID != "" {
			sessionID = *binding.TrainingSessionID // retry reuse (AC-4)
			if binding.TrainingSessionKey != nil {
				sessionKey = *binding.TrainingSessionKey
			}
		} else {
			creds, sErr := h.TaskService.OpenEnvDispatchTrainingSession(ctx, in.EnvID, binding.ID)
			if sErr != nil {
				_ = store.markFailed(context.WithoutCancel(ctx), h.DB, in.EnvID, in.AgentID, "training session open failed")
				return ProvisionEnvDispatchAgentResult{}, fmt.Errorf("training: open env-dispatch session: %w", sErr)
			}
			sessionID = creds.SessionID
			sessionKey = creds.ProxyKey
			if pErr := store.setTrainingSession(ctx, h.DB, in.EnvID, in.AgentID, sessionID, binding.ID, sessionKey); pErr != nil {
				_ = store.markFailed(context.WithoutCancel(ctx), h.DB, in.EnvID, in.AgentID, "training session persist failed")
				return ProvisionEnvDispatchAgentResult{}, fmt.Errorf("training: persist env-dispatch session: %w", pErr)
			}
		}
		trainingRuntime, rErr := service.NormalizeExternalModelRuntime(&service.ExternalModelRuntime{
			BaseURL: h.TaskService.TrainingProxyURL(),
			APIKey:  sessionKey,
			Model:   "areal-default", // arealProxyModel (service/training.go); spec-locked
		})
		if rErr != nil {
			_ = store.markFailed(context.WithoutCancel(ctx), h.DB, in.EnvID, in.AgentID, "training runtime config invalid")
			return ProvisionEnvDispatchAgentResult{}, fmt.Errorf("training: invalid env-dispatch runtime config: %w", rErr)
		}
		config = envDispatchSandboxConfig{Template: config.Template, Runtime: trainingRuntime}
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
	derivedID := ""
	cleanup := func(cause error) (ProvisionEnvDispatchAgentResult, error) {
		if cleanupErr := h.cleanupFailedEnvDispatchDerivedAgent(context.WithoutCancel(ctx), binding.ID, in.AgentID, derivedID); cleanupErr != nil {
			cause = fmt.Errorf("%w (cleanup derived agent: %v)", cause, cleanupErr)
		}
		_ = lifecycle.Delete(context.WithoutCancel(ctx), ref, in.UserID)
		_ = store.markFailed(context.WithoutCancel(ctx), h.DB, in.EnvID, in.AgentID, "provisioning failed")
		return ProvisionEnvDispatchAgentResult{}, cause
	}
	runtimeRef, err := service.WaitForOnlineSandboxRuntime(ctx, &envSandboxLifecycleDepsAdapter{h: h}, in.WorkspaceID, ref.DaemonID, ref.InstanceID, envDispatchRuntimeReadinessTimeout)
	if err != nil {
		return cleanup(fmt.Errorf("wait for online sandbox runtime: %w", err))
	}
	derivedID, err = CloneEnvDispatchAgentTx(ctx, h, service.CloneEnvDispatchAgentInput{
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
	sessionID, sessionCreated, err := h.ensureEnvDispatchChannelSession(ctx, envDispatchChannelSessionInput{
		WorkspaceID: in.WorkspaceID,
		ProjectID:   in.ProjectID,
		ChannelID:   in.ChannelID,
		AgentID:     derivedID,
		CreatorID:   in.UserID,
		RuntimeID:   runtimeRef.ID,
	})
	if err != nil {
		return cleanup(fmt.Errorf("ensure env-dispatch channel session: %w", err))
	}
	if err := store.markReady(ctx, h.DB, in.EnvID, in.AgentID, ref.InstanceID, runtimeRef.ID, ref.DaemonID); err != nil {
		if sessionCreated {
			_, _ = h.DB.Exec(context.WithoutCancel(ctx), `DELETE FROM channel_agent_session WHERE channel_id = $1 AND agent_id = $2`, in.ChannelID, derivedID)
			_, _ = h.DB.Exec(context.WithoutCancel(ctx), `DELETE FROM chat_session WHERE id = $1`, sessionID)
		}
		return cleanup(fmt.Errorf("mark env-dispatch binding ready: %w", err))
	}
	return ProvisionEnvDispatchAgentResult{AgentID: derivedID, SandboxInstanceID: ref.InstanceID, RuntimeID: runtimeRef.ID, DaemonID: ref.DaemonID, ChatSessionID: sessionID}, nil
}

// cleanupFailedEnvDispatchDerivedAgent removes the runtime-owning derived
// agent before sandbox deletion so agent.runtime_id's ON DELETE RESTRICT does
// not strand the failed attempt's runtime. The CTE makes binding detach and
// agent deletion atomic and source-lineage guarded. An empty derived ID means
// provisioning failed before the clone committed and is a no-op.
func (h *Handler) cleanupFailedEnvDispatchDerivedAgent(ctx context.Context, bindingID, sourceAgentID, derivedAgentID string) error {
	if derivedAgentID == "" {
		return nil
	}
	_, err := h.DB.Exec(ctx, `
WITH cleared AS (
    UPDATE environment_agent_sandbox
    SET derived_agent_id = NULL, updated_at = now()
    WHERE id::text = $1 AND derived_agent_id::text = $2
    RETURNING 1
)
DELETE FROM agent
WHERE id::text = $2
  AND source_agent_id::text = $3
  AND EXISTS (SELECT 1 FROM cleared)`, bindingID, derivedAgentID, sourceAgentID)
	return err
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
	return ProvisionEnvDispatchAgentResult{AgentID: in.AgentID, SandboxInstanceID: ref.InstanceID, RuntimeID: runtimeID, DaemonID: daemonID, ChatSessionID: sessionID}, nil
}

// isEnvDispatchTrainingTarget reports whether the addressed agent is the
// training target for its project's env-dispatch (training_dispatch row with
// train_agent_id == agentID). Used to route env-dispatch provisioning to the
// AC-4 training branch.
func (h *Handler) isEnvDispatchTrainingTarget(ctx context.Context, projectID, agentID string) bool {
	if projectID == "" || agentID == "" {
		return false
	}
	projectUUID, err := util.ParseUUID(projectID)
	if err != nil {
		return false
	}
	dispatch, err := h.Queries.GetTrainingDispatchByProject(ctx, projectUUID)
	if err != nil {
		return false // no training dispatch / not a training project
	}
	return util.UUIDToString(dispatch.TrainAgentID) == agentID
}

// provisionEnvDispatchAgentTraining handles the AC-4 training source-agent
// first-address path. It opens the AReaL training session with
// session_ref=binding.ID BEFORE sandbox creation (reusing a persisted session on
// retry), persists session id/ref/key on the binding, creates the sandbox with a
// server-owned areal-default + bridge URL + session-key runtime, discovers the
// online runtime, clones the derived agent, and opens the channel session. The
// real task is enqueued by the service layer after provisioning and linked to
// the session for DAG assembly. No agent_runtime row is pre-created.
func (h *Handler) provisionEnvDispatchAgentTraining(ctx context.Context, in ProvisionEnvDispatchAgentInput, store envDispatchChannelStore, binding envAgentSandboxBinding) (ProvisionEnvDispatchAgentResult, error) {
	cfg := service.LoadTrainingConfig()
	if cfg.BridgeStubURL == "" || cfg.AdminAPIKey == "" {
		_ = store.markFailed(context.WithoutCancel(ctx), h.DB, in.EnvID, in.AgentID, "training bridge not configured")
		return ProvisionEnvDispatchAgentResult{}, fmt.Errorf("env-dispatch training: bridge not configured")
	}
	client := arealrl.New(cfg.BridgeStubURL, cfg.AdminAPIKey)

	existingID, existingKey := "", ""
	if binding.TrainingSessionID != nil {
		existingID = *binding.TrainingSessionID
	}
	if binding.TrainingSessionKey != nil {
		existingKey = *binding.TrainingSessionKey
	}
	res, err := service.ResolveEnvDispatchTrainingSession(ctx, client, service.EnvDispatchTrainingBinding{
		BindingID:          binding.ID,
		TrainingSessionID:  existingID,
		TrainingSessionKey: existingKey,
	}, in.EnvID)
	if err != nil {
		_ = store.markFailed(context.WithoutCancel(ctx), h.DB, in.EnvID, in.AgentID, "training session resolve failed")
		return ProvisionEnvDispatchAgentResult{}, err
	}
	if res.Opened {
		if err := store.setTrainingSession(ctx, h.DB, in.EnvID, in.AgentID, res.SessionID, res.SessionRef, res.ProxyKey); err != nil {
			_ = store.markFailed(context.WithoutCancel(ctx), h.DB, in.EnvID, in.AgentID, "persist training session failed")
			return ProvisionEnvDispatchAgentResult{}, fmt.Errorf("persist training session: %w", err)
		}
	}

	lifecycle := newEnvSandboxLifecycleService(h)
	if lifecycle == nil {
		_ = store.markFailed(context.WithoutCancel(ctx), h.DB, in.EnvID, in.AgentID, "lifecycle unavailable")
		return ProvisionEnvDispatchAgentResult{}, fmt.Errorf("sandbox lifecycle unavailable")
	}
	// The sandbox pi routes its LLM calls to the address reachable *from inside
	// the sandbox VM* (AREAL_PROXY_URL / cfg.ProxyURL). This is deliberately
	// distinct from cfg.BridgeStubURL (AREAL_BRIDGE_STUB_URL), which is the
	// backend->stub address used by the arealrl control-plane client above:
	// BridgeStubURL may be a Docker-compose DNS name (e.g. db-bridge-stub-multica)
	// that resolves on the backend's compose network but NOT inside the sandbox
	// VM, so using it as the pi base_url makes every LLM call hang on DNS/connect
	// and the training DAG times out. Fall back to BridgeStubURL only when
	// ProxyURL is unset (single-host deploys where the two addresses coincide).
	sandboxProxyURL := cfg.ProxyURL
	if sandboxProxyURL == "" {
		sandboxProxyURL = cfg.BridgeStubURL
	}
	rtJSON, err := json.Marshal(service.EnvDispatchTrainingRuntimePolicy(sandboxProxyURL, res.ProxyKey))
	if err != nil {
		_ = store.markFailed(context.WithoutCancel(ctx), h.DB, in.EnvID, in.AgentID, "encode training runtime failed")
		return ProvisionEnvDispatchAgentResult{}, fmt.Errorf("encode training runtime: %w", err)
	}
	ref, err := lifecycle.Create(ctx, service.CreateSandboxInstanceInput{
		WorkspaceID:   in.WorkspaceID,
		Template:      "default",
		DaemonEnabled: true,
		Runtime:       rtJSON,
		RuntimeEnv:    map[string]string{},
	}, in.UserID)
	if err != nil {
		_ = store.markFailed(context.WithoutCancel(ctx), h.DB, in.EnvID, in.AgentID, "sandbox create failed")
		return ProvisionEnvDispatchAgentResult{}, fmt.Errorf("create training sandbox: %w", err)
	}
	derivedID := ""
	cleanup := func(cause error) (ProvisionEnvDispatchAgentResult, error) {
		if cleanupErr := h.cleanupFailedEnvDispatchDerivedAgent(context.WithoutCancel(ctx), binding.ID, in.AgentID, derivedID); cleanupErr != nil {
			cause = fmt.Errorf("%w (cleanup derived agent: %v)", cause, cleanupErr)
		}
		_ = lifecycle.Delete(context.WithoutCancel(ctx), ref, in.UserID)
		_ = store.markFailed(context.WithoutCancel(ctx), h.DB, in.EnvID, in.AgentID, "training provisioning failed")
		return ProvisionEnvDispatchAgentResult{}, cause
	}
	runtimeRef, err := service.WaitForOnlineSandboxRuntime(ctx, &envSandboxLifecycleDepsAdapter{h: h}, in.WorkspaceID, ref.DaemonID, ref.InstanceID, envDispatchRuntimeReadinessTimeout)
	if err != nil {
		return cleanup(fmt.Errorf("wait for online sandbox runtime: %w", err))
	}
	derivedID, err = CloneEnvDispatchAgentTx(ctx, h, service.CloneEnvDispatchAgentInput{
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
	sessionID, sessionCreated, err := h.ensureEnvDispatchChannelSession(ctx, envDispatchChannelSessionInput{
		WorkspaceID: in.WorkspaceID,
		ProjectID:   in.ProjectID,
		ChannelID:   in.ChannelID,
		AgentID:     derivedID,
		CreatorID:   in.UserID,
		RuntimeID:   runtimeRef.ID,
	})
	if err != nil {
		return cleanup(fmt.Errorf("ensure env-dispatch channel session: %w", err))
	}
	if err := store.markReady(ctx, h.DB, in.EnvID, in.AgentID, ref.InstanceID, runtimeRef.ID, ref.DaemonID); err != nil {
		if sessionCreated {
			_, _ = h.DB.Exec(context.WithoutCancel(ctx), `DELETE FROM channel_agent_session WHERE channel_id = $1 AND agent_id = $2`, in.ChannelID, derivedID)
			_, _ = h.DB.Exec(context.WithoutCancel(ctx), `DELETE FROM chat_session WHERE id = $1`, sessionID)
		}
		return cleanup(fmt.Errorf("mark env-dispatch binding ready: %w", err))
	}
	return ProvisionEnvDispatchAgentResult{AgentID: derivedID, SandboxInstanceID: ref.InstanceID, RuntimeID: runtimeRef.ID, DaemonID: ref.DaemonID, ChatSessionID: sessionID}, nil
}
