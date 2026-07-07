package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type fakeEnvSandboxLifecycleDeps struct {
	refs map[string]SandboxInstanceRef

	jobs         []sandboxJobCall
	wakeups      []sandboxWakeupCall
	forceDeletes []string
	inserts      []createSandboxInstanceCall

	insertedRef SandboxInstanceRef
	enqueueErr  error
	notifyErr   error
	deleteErr   error
}

type createSandboxInstanceCall struct {
	WorkspaceID string
	ActorUserID string
	Template    string
	NodeID      string
}

type sandboxJobCall struct {
	WorkspaceID string
	ActorUserID string
	NodeID      string
	InstanceID  string
	JobType     string
	Payload     json.RawMessage
}

type sandboxWakeupCall struct {
	NodeID string
	JobID  string
}

func (f *fakeEnvSandboxLifecycleDeps) GetSandboxInstanceRef(_ context.Context, workspaceID, instanceID string) (SandboxInstanceRef, error) {
	ref, ok := f.refs[workspaceID+":"+instanceID]
	if !ok {
		return SandboxInstanceRef{}, ErrSandboxInstanceNotFound
	}
	return ref, nil
}

func (f *fakeEnvSandboxLifecycleDeps) EnqueueSandboxJob(_ context.Context, workspaceID, actorUserID, nodeID, instanceID, jobType string, payload json.RawMessage) (SandboxLifecycleJobResult, error) {
	if f.enqueueErr != nil {
		return SandboxLifecycleJobResult{}, f.enqueueErr
	}
	f.jobs = append(f.jobs, sandboxJobCall{
		WorkspaceID: workspaceID,
		ActorUserID: actorUserID,
		NodeID:      nodeID,
		InstanceID:  instanceID,
		JobType:     jobType,
		Payload:     append(json.RawMessage(nil), payload...),
	})
	return SandboxLifecycleJobResult{JobID: "job-1", InstanceID: instanceID, NodeID: nodeID, JobType: jobType}, nil
}

func (f *fakeEnvSandboxLifecycleDeps) NotifySandboxJobAvailable(_ context.Context, nodeID, jobID string) error {
	if f.notifyErr != nil {
		return f.notifyErr
	}
	f.wakeups = append(f.wakeups, sandboxWakeupCall{NodeID: nodeID, JobID: jobID})
	return nil
}

func (f *fakeEnvSandboxLifecycleDeps) ForceDeleteSandboxInstance(_ context.Context, workspaceID, instanceID string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.forceDeletes = append(f.forceDeletes, workspaceID+":"+instanceID)
	return nil
}

func (f *fakeEnvSandboxLifecycleDeps) InsertSandboxInstance(_ context.Context, in CreateSandboxInstanceInput, actorUserID string) (SandboxInstanceRef, error) {
	f.inserts = append(f.inserts, createSandboxInstanceCall{
		WorkspaceID: in.WorkspaceID,
		ActorUserID: actorUserID,
		Template:    in.Template,
		NodeID:      in.NodeID,
	})
	if f.insertedRef.InstanceID == "" {
		f.insertedRef = SandboxInstanceRef{
			InstanceID:      "inst-created",
			WorkspaceID:     in.WorkspaceID,
			NodeID:          in.NodeID,
			Template:        in.Template,
			Status:          "pending",
			RuntimeMetadata: in.Runtime,
			EndpointInfo:    in.Limits,
		}
	}
	return f.insertedRef, nil
}

func lifecycleRef() SandboxInstanceRef {
	return SandboxInstanceRef{
		InstanceID:      "inst-1",
		WorkspaceID:     "ws-1",
		NodeID:          "node-1",
		LocalRef:        "cube-local-1",
		Template:        "default",
		Status:          "running",
		RuntimeMetadata: json.RawMessage(`{"model":"gpt-test"}`),
		EndpointInfo:    json.RawMessage(`{"url":"http://sandbox"}`),
	}
}

func TestEnvSandboxLifecycleSaveEnqueuesStopJobAndWakeup(t *testing.T) {
	ctx := context.Background()
	ref := lifecycleRef()
	deps := &fakeEnvSandboxLifecycleDeps{refs: map[string]SandboxInstanceRef{"ws-1:inst-1": ref}}
	svc := NewEnvSandboxLifecycleService(deps, 5*time.Second)

	result, err := svc.Save(ctx, ref, "user-1")

	if err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	if result.JobType != "stop" {
		t.Fatalf("want stop job, got %q", result.JobType)
	}
	if len(deps.jobs) != 1 {
		t.Fatalf("want one sandbox job, got %d", len(deps.jobs))
	}
	job := deps.jobs[0]
	if job.WorkspaceID != "ws-1" || job.ActorUserID != "user-1" || job.NodeID != "node-1" || job.InstanceID != "inst-1" {
		t.Fatalf("unexpected job identity: %+v", job)
	}
	var payload map[string]any
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		t.Fatalf("payload is invalid JSON: %v", err)
	}
	if payload["local_ref"] != "cube-local-1" {
		t.Fatalf("payload local_ref = %v", payload["local_ref"])
	}
	if len(deps.wakeups) != 1 || deps.wakeups[0].NodeID != "node-1" || deps.wakeups[0].JobID != "job-1" {
		t.Fatalf("missing node wakeup: %+v", deps.wakeups)
	}
}

func TestEnvSandboxLifecycleResumeEnqueuesResumeWithRuntimeMetadata(t *testing.T) {
	ctx := context.Background()
	ref := lifecycleRef()
	deps := &fakeEnvSandboxLifecycleDeps{refs: map[string]SandboxInstanceRef{"ws-1:inst-1": ref}}
	svc := NewEnvSandboxLifecycleService(deps, 5*time.Second)

	_, err := svc.Resume(ctx, ref, "user-1")

	if err != nil {
		t.Fatalf("Resume returned error: %v", err)
	}
	if len(deps.jobs) != 1 || deps.jobs[0].JobType != "resume" {
		t.Fatalf("want one resume job, got %+v", deps.jobs)
	}
	var payload map[string]any
	if err := json.Unmarshal(deps.jobs[0].Payload, &payload); err != nil {
		t.Fatalf("payload is invalid JSON: %v", err)
	}
	if payload["local_ref"] != "cube-local-1" {
		t.Fatalf("payload local_ref = %v", payload["local_ref"])
	}
	if _, ok := payload["runtime"]; !ok {
		t.Fatalf("resume payload missing runtime metadata: %s", string(deps.jobs[0].Payload))
	}
}

func TestEnvSandboxLifecycleDeletePreservesOfflineForceDelete(t *testing.T) {
	ctx := context.Background()
	ref := lifecycleRef()
	deps := &fakeEnvSandboxLifecycleDeps{
		refs:       map[string]SandboxInstanceRef{"ws-1:inst-1": ref},
		enqueueErr: ErrSandboxNodeUnavailable,
	}
	svc := NewEnvSandboxLifecycleService(deps, 5*time.Second)

	err := svc.Delete(ctx, ref, "user-1")

	if err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if len(deps.forceDeletes) != 1 || deps.forceDeletes[0] != "ws-1:inst-1" {
		t.Fatalf("force-delete fallback not used: %+v", deps.forceDeletes)
	}
}

func TestEnvSandboxLifecycleMissingSandboxReturnsTypedError(t *testing.T) {
	ctx := context.Background()
	deps := &fakeEnvSandboxLifecycleDeps{refs: map[string]SandboxInstanceRef{}}
	svc := NewEnvSandboxLifecycleService(deps, 5*time.Second)

	_, err := svc.Save(ctx, SandboxInstanceRef{WorkspaceID: "ws-1", InstanceID: "missing"}, "user-1")

	if !errors.Is(err, ErrSandboxInstanceNotFound) {
		t.Fatalf("want ErrSandboxInstanceNotFound, got %v", err)
	}
}

func TestEnvSandboxLifecycleCreateEnqueuesCreateJobAndWakesNode(t *testing.T) {
	ctx := context.Background()
	deps := &fakeEnvSandboxLifecycleDeps{}
	svc := NewEnvSandboxLifecycleService(deps, 5*time.Second)

	in := CreateSandboxInstanceInput{
		WorkspaceID: "ws-1",
		NodeID:      "node-1",
		Template:    "python",
		Limits:      json.RawMessage(`{"cpu":2}`),
		Runtime:     json.RawMessage(`{"model":"gpt-test"}`),
		RuntimeEnv:  map[string]string{"MULTICA_TOKEN": "tok"},
	}
	ref, err := svc.Create(ctx, in, "user-1")

	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if ref.InstanceID == "" || ref.Status != "pending" {
		t.Fatalf("unexpected created ref: %+v", ref)
	}
	if len(deps.inserts) != 1 || deps.inserts[0].Template != "python" || deps.inserts[0].NodeID != "node-1" {
		t.Fatalf("missing insert: %+v", deps.inserts)
	}
	if len(deps.jobs) != 1 || deps.jobs[0].JobType != "create" {
		t.Fatalf("want one create job, got %+v", deps.jobs)
	}
	var payload map[string]any
	if err := json.Unmarshal(deps.jobs[0].Payload, &payload); err != nil {
		t.Fatalf("payload invalid JSON: %v", err)
	}
	if payload["template"] != "python" {
		t.Fatalf("payload template = %v", payload["template"])
	}
	if _, ok := payload["runtime_env"]; !ok {
		t.Fatalf("payload missing runtime_env: %s", string(deps.jobs[0].Payload))
	}
	if len(deps.wakeups) != 1 || deps.wakeups[0].NodeID != "node-1" {
		t.Fatalf("missing node wakeup: %+v", deps.wakeups)
	}
}
