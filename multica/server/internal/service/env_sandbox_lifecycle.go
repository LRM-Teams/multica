package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

var (
	ErrSandboxInstanceNotFound = errors.New("sandbox_instance_not_found")
	ErrSandboxNodeUnavailable  = errors.New("sandbox_node_unavailable")
)

// SandboxInstanceRef is the structured env lifecycle handle used by
// env-dispatch and checkpointing when save/resume semantics are required.
type SandboxInstanceRef struct {
	InstanceID      string          `json:"instance_id"`
	WorkspaceID     string          `json:"workspace_id"`
	CreatorUserID   string          `json:"creator_user_id,omitempty"`
	NodeID          string          `json:"node_id"`
	LocalRef        string          `json:"local_ref,omitempty"`
	Template        string          `json:"template,omitempty"`
	Status          string          `json:"status,omitempty"`
	RuntimeMetadata json.RawMessage `json:"runtime,omitempty"`
	EndpointInfo    json.RawMessage `json:"endpoint_info,omitempty"`
	// RuntimeID is the pre-created agent_runtime id (R') for a daemon-enabled
	// sandbox: env-dispatch inserts an offline row keyed by a daemon_id, injects
	// that daemon_id as MULTICA_DAEMON_ID into the sandbox runtime_env, and the
	// in-sandbox daemon adopts this row on register. The task is routed to R'
	// (not the agent's runtime). Empty when the sandbox is not daemon-bound.
	RuntimeID string `json:"runtime_id,omitempty"`
	DaemonID  string `json:"daemon_id,omitempty"`
}

type SandboxLifecycleJobResult struct {
	JobID      string
	InstanceID string
	NodeID     string
	JobType    string
}

type EnvSandboxLifecycleDeps interface {
	GetSandboxInstanceRef(ctx context.Context, workspaceID, instanceID string) (SandboxInstanceRef, error)
	InsertSandboxInstance(ctx context.Context, in CreateSandboxInstanceInput, actorUserID string) (SandboxInstanceRef, error)
	// MintSandboxRuntimeEnv mints the daemon bootstrap env for a sandbox: a
	// server URL + a personal access token (for the actor) + workspace id +
	// MULTICA_DAEMON_ENABLED=1 + a per-instance profile. When displayName is
	// non-empty it is also written as MULTICA_DAEMON_DEVICE_NAME /
	// MULTICA_SANDBOX_NAME so the daemon registers with the control-plane name.
	// Used by Create when a sandbox is flagged DaemonEnabled, so the in-sandbox
	// daemon can reach multica on boot. The minted env (which carries the token)
	// must stay within the create path - it is never returned on the
	// SandboxInstanceRef, so it cannot leak into dispatch responses/ledger.
	MintSandboxRuntimeEnv(ctx context.Context, workspaceID, actorUserID, instanceID, displayName string) (map[string]string, error)
	EnqueueSandboxJob(ctx context.Context, workspaceID, actorUserID, nodeID, instanceID, jobType string, payload json.RawMessage) (SandboxLifecycleJobResult, error)
	NotifySandboxJobAvailable(ctx context.Context, nodeID, jobID string) error
	ForceDeleteSandboxInstance(ctx context.Context, workspaceID, instanceID string) error
	ForceDeleteSandboxRuntime(ctx context.Context, workspaceID, runtimeID string) error
}

// CreateSandboxInstanceInput is the service-layer input for creating a new
// sandbox_instance-backed environment handle. NodeID may be empty when the
// caller wants the deps to auto-select an available node for the workspace.
type CreateSandboxInstanceInput struct {
	WorkspaceID string
	NodeID      string
	Template    string
	// Name is the control-plane display name persisted on metadata.name and
	// injected into MULTICA_DAEMON_DEVICE_NAME when DaemonEnabled.
	Name       string
	Limits     json.RawMessage
	Runtime    json.RawMessage
	RuntimeEnv map[string]string
	// DaemonEnabled requests that Create mint a daemon bootstrap runtime_env
	// (server URL + PAT + workspace + MULTICA_DAEMON_ENABLED=1 + profile) via
	// MintSandboxRuntimeEnv when RuntimeEnv is not supplied, so the in-sandbox
	// daemon can reach multica on boot. False (the zero value) leaves the
	// sandbox without a daemon env - used for base/template sandboxes that only
	// need to hold an image (e.g. the auto-created default self_play base env).
	DaemonEnabled bool
}

// CloneSandboxInstanceInput identifies the destination execution identity and
// the already-resolved create policy to use after Cube snapshots the source.
// RuntimeID is pre-created by the caller so the cloned sandbox can register a
// distinct daemon/runtime pair from its source.
type CloneSandboxInstanceInput struct {
	WorkspaceID   string
	EnvID         string
	AgentID       string
	RuntimeID     string
	DaemonID      string
	Name          string
	CreatePayload json.RawMessage
}

type EnvSandboxLifecycleService struct {
	deps    EnvSandboxLifecycleDeps
	timeout time.Duration
}

func NewEnvSandboxLifecycleService(deps EnvSandboxLifecycleDeps, timeout time.Duration) *EnvSandboxLifecycleService {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &EnvSandboxLifecycleService{deps: deps, timeout: timeout}
}

// Compile-time check: *EnvSandboxLifecycleService satisfies SandboxInstanceCreator
// so env-dispatch can inject it via WithSandboxLifecycle.
var _ SandboxInstanceCreator = (*EnvSandboxLifecycleService)(nil)

func (s *EnvSandboxLifecycleService) Save(ctx context.Context, ref SandboxInstanceRef, actorUserID string) (SandboxLifecycleJobResult, error) {
	return s.enqueue(ctx, ref, actorUserID, "stop", nil)
}

// CreateSandboxInstance satisfies the SandboxInstanceCreator interface used by
// env-dispatch. It delegates to Create so *EnvSandboxLifecycleService can be
// injected via WithSandboxLifecycle.
func (s *EnvSandboxLifecycleService) CreateSandboxInstance(ctx context.Context, in CreateSandboxInstanceInput, actorUserID string) (SandboxInstanceRef, error) {
	return s.Create(ctx, in, actorUserID)
}

// GetSandboxInstanceRef satisfies the SandboxInstanceCreator interface, letting
// env-dispatch resolve a source sandbox_instance's template for branch-from-
// template. Delegates to the deps lookup.
func (s *EnvSandboxLifecycleService) GetSandboxInstanceRef(ctx context.Context, workspaceID, instanceID string) (SandboxInstanceRef, error) {
	return s.deps.GetSandboxInstanceRef(ctx, workspaceID, instanceID)
}

// DeleteSandboxInstance satisfies the SandboxInstanceCreator interface, letting
// env-dispatch reclaim an instance created by the auto-create default-env path
// when env creation fails or when a concurrent writer lost the race.
func (s *EnvSandboxLifecycleService) DeleteSandboxInstance(ctx context.Context, ref SandboxInstanceRef, actorUserID string) error {
	return s.Delete(ctx, ref, actorUserID)
}

// Create inserts a pending sandbox_instance row, enqueues the existing
// sandboxd create job, and notifies the owning node — mirroring the existing
// CreateSandboxInstance handler. It returns the structured ref checkpointing
// and env-dispatch use to track the new environment handle.
func (s *EnvSandboxLifecycleService) Create(ctx context.Context, in CreateSandboxInstanceInput, actorUserID string) (SandboxInstanceRef, error) {
	if in.WorkspaceID == "" {
		return SandboxInstanceRef{}, fmt.Errorf("validation_failed: workspace_id is required for sandbox create")
	}
	if in.Template == "" {
		in.Template = "default"
	}
	// NodeID is optional: when empty, InsertSandboxInstance picks an available
	// node bound to the workspace (mirroring the CreateSandboxInstance handler).
	// A workspace with no online node surfaces a clear error from that pick.
	ref, err := s.deps.InsertSandboxInstance(ctx, in, actorUserID)
	if err != nil {
		return SandboxInstanceRef{}, fmt.Errorf("insert sandbox instance: %w", err)
	}
	compensate := func(cause error) (SandboxInstanceRef, error) {
		if cleanupErr := s.deps.ForceDeleteSandboxInstance(
			context.WithoutCancel(ctx), ref.WorkspaceID, ref.InstanceID,
		); cleanupErr != nil {
			return SandboxInstanceRef{}, errors.Join(cause, fmt.Errorf("compensate sandbox instance: %w", cleanupErr))
		}
		return SandboxInstanceRef{}, cause
	}
	// Daemon-enabled sandbox: mint the bootstrap env (server URL + PAT +
	// workspace + MULTICA_DAEMON_ENABLED=1 + profile + fresh MULTICA_DAEMON_ID)
	// so the in-sandbox daemon can reach multica on boot with a unique runtime
	// identity, then overlay any caller-supplied extras (e.g. Phase 2's
	// pre-assigned MULTICA_DAEMON_ID) on top - caller keys win on conflict,
	// minted keys (MULTICA_TOKEN etc.) are kept when the caller does not supply
	// them. The env stays local to Create (folded into the create job payload
	// below) and is never placed on the ref, so the token cannot leak into
	// dispatch responses or the idempotency ledger.
	if in.DaemonEnabled {
		env, err := s.deps.MintSandboxRuntimeEnv(ctx, in.WorkspaceID, actorUserID, ref.InstanceID, in.Name)
		if err != nil {
			return compensate(fmt.Errorf("mint sandbox runtime env: %w", err))
		}
		for k, v := range in.RuntimeEnv {
			env[k] = v
		}
		in.RuntimeEnv = env
		// Surface the daemon correlation nonce on the ref so env-dispatch
		// first-address provisioning can persist it on the binding and later
		// discover the online runtime by (workspace, daemon_id,
		// sandbox_instance_id). When the caller did not supply
		// MULTICA_DAEMON_ID, MintSandboxRuntimeEnv minted a unique one;
		// caller-supplied values win. Either way this is the daemon ID injected
		// into the sandbox env, so it matches what the in-sandbox daemon
		// registers - the pre-create-free provisioning path (Task 3.1) relies on
		// this instead of a pre-created offline runtime row.
		ref.DaemonID = env["MULTICA_DAEMON_ID"]
	}
	payload, err := sandboxCreatePayload(in, ref.InstanceID, ref.RuntimeMetadata)
	if err != nil {
		return compensate(fmt.Errorf("build sandbox create payload: %w", err))
	}
	job, err := s.deps.EnqueueSandboxJob(ctx, ref.WorkspaceID, actorUserID, ref.NodeID, ref.InstanceID, "create", payload)
	if err != nil {
		return compensate(err)
	}
	if err := s.deps.NotifySandboxJobAvailable(ctx, ref.NodeID, job.JobID); err != nil {
		slog.Warn("sandbox create: failed to notify job available",
			"workspace_id", ref.WorkspaceID,
			"instance_id", ref.InstanceID,
			"node_id", ref.NodeID,
			"job_id", job.JobID,
			"error", err)
	}
	return ref, nil
}

// CloneSandboxInstance creates a destination instance and queues a clone job
// that snapshots the source filesystem before starting the destination. The
// source is only read; completion updates the destination instance.
func (s *EnvSandboxLifecycleService) CloneSandboxInstance(ctx context.Context, source SandboxInstanceRef, in CloneSandboxInstanceInput, actorUserID string) (SandboxInstanceRef, error) {
	if in.WorkspaceID == "" || in.RuntimeID == "" || in.DaemonID == "" {
		return SandboxInstanceRef{}, fmt.Errorf("validation_failed: workspace_id, runtime_id, and daemon_id are required for sandbox clone")
	}
	fresh, err := s.deps.GetSandboxInstanceRef(ctx, source.WorkspaceID, source.InstanceID)
	if err != nil {
		return SandboxInstanceRef{}, err
	}
	if fresh.WorkspaceID != in.WorkspaceID {
		return SandboxInstanceRef{}, fmt.Errorf("validation_failed: source and destination workspace differ")
	}
	if fresh.LocalRef == "" {
		return SandboxInstanceRef{}, fmt.Errorf("validation_failed: source sandbox has no external id")
	}
	var create CreateSandboxInstanceInput
	if err := json.Unmarshal(in.CreatePayload, &create); err != nil {
		return SandboxInstanceRef{}, fmt.Errorf("decode clone create payload: %w", err)
	}
	create.WorkspaceID = in.WorkspaceID
	destination, err := s.deps.InsertSandboxInstance(ctx, create, actorUserID)
	if err != nil {
		return SandboxInstanceRef{}, fmt.Errorf("insert cloned sandbox instance: %w", err)
	}
	compensate := func(cause error) (SandboxInstanceRef, error) {
		cleanupCtx := context.WithoutCancel(ctx)
		if runtimeErr := s.deps.ForceDeleteSandboxRuntime(cleanupCtx, in.WorkspaceID, in.RuntimeID); runtimeErr != nil {
			cause = errors.Join(cause, fmt.Errorf("compensate clone runtime: %w", runtimeErr))
		}
		if instanceErr := s.deps.ForceDeleteSandboxInstance(cleanupCtx, in.WorkspaceID, destination.InstanceID); instanceErr != nil {
			cause = errors.Join(cause, fmt.Errorf("compensate cloned sandbox instance: %w", instanceErr))
		}
		return SandboxInstanceRef{}, cause
	}
	payload, err := json.Marshal(map[string]any{
		"source_sandbox_instance_id": fresh.InstanceID,
		"source_external_id":         fresh.LocalRef,
		"create_payload":             json.RawMessage(in.CreatePayload),
	})
	if err != nil {
		return compensate(fmt.Errorf("build sandbox clone payload: %w", err))
	}
	job, err := s.deps.EnqueueSandboxJob(ctx, in.WorkspaceID, actorUserID, destination.NodeID, destination.InstanceID, "clone", payload)
	if err != nil {
		return compensate(fmt.Errorf("enqueue sandbox clone job: %w", err))
	}
	if err := s.deps.NotifySandboxJobAvailable(ctx, destination.NodeID, job.JobID); err != nil {
		slog.Warn("sandbox clone: failed to notify job available", "instance_id", destination.InstanceID, "job_id", job.JobID, "error", err)
	}
	return destination, nil
}

func (s *EnvSandboxLifecycleService) Resume(ctx context.Context, ref SandboxInstanceRef, actorUserID string) (SandboxLifecycleJobResult, error) {
	return s.enqueue(ctx, ref, actorUserID, "resume", ref.RuntimeMetadata)
}

func (s *EnvSandboxLifecycleService) Reconfigure(ctx context.Context, ref SandboxInstanceRef, actorUserID string, runtime json.RawMessage) (SandboxLifecycleJobResult, error) {
	return s.enqueue(ctx, ref, actorUserID, "reconfigure", runtime)
}

func (s *EnvSandboxLifecycleService) Delete(ctx context.Context, ref SandboxInstanceRef, actorUserID string) error {
	_, err := s.enqueue(ctx, ref, actorUserID, "delete", nil)
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrSandboxNodeUnavailable) {
		return s.deps.ForceDeleteSandboxInstance(ctx, ref.WorkspaceID, ref.InstanceID)
	}
	return err
}

func (s *EnvSandboxLifecycleService) enqueue(ctx context.Context, ref SandboxInstanceRef, actorUserID, jobType string, runtime json.RawMessage) (SandboxLifecycleJobResult, error) {
	fresh, err := s.deps.GetSandboxInstanceRef(ctx, ref.WorkspaceID, ref.InstanceID)
	if err != nil {
		return SandboxLifecycleJobResult{}, err
	}
	if fresh.NodeID == "" {
		return SandboxLifecycleJobResult{}, ErrSandboxNodeUnavailable
	}
	payload, err := sandboxLifecyclePayload(fresh, runtime)
	if err != nil {
		return SandboxLifecycleJobResult{}, fmt.Errorf("build sandbox %s payload: %w", jobType, err)
	}
	job, err := s.deps.EnqueueSandboxJob(ctx, fresh.WorkspaceID, actorUserID, fresh.NodeID, fresh.InstanceID, jobType, payload)
	if err != nil {
		return SandboxLifecycleJobResult{}, err
	}
	if err := s.deps.NotifySandboxJobAvailable(ctx, fresh.NodeID, job.JobID); err != nil {
		return SandboxLifecycleJobResult{}, err
	}
	return job, nil
}

func sandboxLifecyclePayload(ref SandboxInstanceRef, runtime json.RawMessage) (json.RawMessage, error) {
	payload := map[string]any{
		"instance_id": ref.InstanceID,
		"local_ref":   ref.LocalRef,
		"template":    ref.Template,
	}
	if len(ref.EndpointInfo) > 0 && string(ref.EndpointInfo) != "null" {
		var endpoint any
		if err := json.Unmarshal(ref.EndpointInfo, &endpoint); err != nil {
			return nil, err
		}
		payload["endpoint_info"] = endpoint
	}
	if len(runtime) > 0 && string(runtime) != "null" {
		var rt any
		if err := json.Unmarshal(runtime, &rt); err != nil {
			return nil, err
		}
		payload["runtime"] = rt
	}
	return json.Marshal(payload)
}

// sandboxCreatePayload builds the canonical sandboxd create job payload shared
// by the frontend CreateSandboxInstance handler and env-dispatch provisioning:
// template, limits, runtime, runtime_env (when present), metadata, and
// instance_id. instance_id lets the in-sandbox daemon register its runtime with
// sandbox_instance_id so env-dispatch can discover the online runtime by
// (workspace, daemon_id, sandbox_instance_id) instead of binding to a
// pre-created row. metadata mirrors the instance's persisted metadata so
// frontend and env-dispatch create jobs carry the same canonical shape.
func sandboxCreatePayload(in CreateSandboxInstanceInput, instanceID string, metadata json.RawMessage) (json.RawMessage, error) {
	payload := map[string]any{
		"template":    in.Template,
		"limits":      json.RawMessage(jsonBytesOrDefault(in.Limits, "{}")),
		"runtime":     json.RawMessage(jsonBytesOrDefault(in.Runtime, "{}")),
		"metadata":    json.RawMessage(jsonBytesOrDefault(metadata, "{}")),
		"instance_id": instanceID,
	}
	if len(in.RuntimeEnv) > 0 {
		payload["runtime_env"] = in.RuntimeEnv
	}
	return json.Marshal(payload)
}

// jsonBytesOrDefault returns v when non-empty (and not the literal "null"),
// otherwise the fallback JSON. Used so sandbox payloads always carry valid
// JSON objects even when a caller omits an optional field.
func jsonBytesOrDefault(v json.RawMessage, fallback string) []byte {
	if len(v) == 0 || string(v) == "null" {
		return []byte(fallback)
	}
	return v
}
