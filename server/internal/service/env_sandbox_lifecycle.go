package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	NodeID          string          `json:"node_id"`
	LocalRef        string          `json:"local_ref,omitempty"`
	Template        string          `json:"template,omitempty"`
	Status          string          `json:"status,omitempty"`
	RuntimeMetadata json.RawMessage `json:"runtime,omitempty"`
	EndpointInfo    json.RawMessage `json:"endpoint_info,omitempty"`
}

type SandboxLifecycleJobResult struct {
	JobID      string
	InstanceID string
	NodeID     string
	JobType    string
}

type EnvSandboxLifecycleDeps interface {
	GetSandboxInstanceRef(ctx context.Context, workspaceID, instanceID string) (SandboxInstanceRef, error)
	EnqueueSandboxJob(ctx context.Context, workspaceID, actorUserID, nodeID, instanceID, jobType string, payload json.RawMessage) (SandboxLifecycleJobResult, error)
	NotifySandboxJobAvailable(ctx context.Context, nodeID, jobID string) error
	ForceDeleteSandboxInstance(ctx context.Context, workspaceID, instanceID string) error
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

func (s *EnvSandboxLifecycleService) Save(ctx context.Context, ref SandboxInstanceRef, actorUserID string) (SandboxLifecycleJobResult, error) {
	return s.enqueue(ctx, ref, actorUserID, "stop", nil)
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
