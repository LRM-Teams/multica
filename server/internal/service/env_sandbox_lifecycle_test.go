package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeEnvSandboxLifecycleDeps struct {
	refs map[string]SandboxInstanceRef

	jobs           []sandboxJobCall
	wakeups        []sandboxWakeupCall
	forceDeletes   []string
	runtimeDeletes []string
	inserts        []createSandboxInstanceCall

	insertedRef SandboxInstanceRef
	enqueueErr  error
	notifyErr   error
	deleteErr   error

	// MintSandboxRuntimeEnv recording. mintEnv is the canned env returned when
	// non-nil; otherwise the fake synthesizes one tagged with the instanceID.
	mintCalls []mintRuntimeEnvCall
	mintEnv   map[string]string
	mintErr   error
}

type mintRuntimeEnvCall struct {
	WorkspaceID string
	ActorUserID string
	InstanceID  string
	DisplayName string
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

func (f *fakeEnvSandboxLifecycleDeps) ForceDeleteSandboxRuntime(_ context.Context, workspaceID, runtimeID string) error {
	f.runtimeDeletes = append(f.runtimeDeletes, workspaceID+":"+runtimeID)
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

func (f *fakeEnvSandboxLifecycleDeps) MintSandboxRuntimeEnv(_ context.Context, workspaceID, actorUserID, instanceID, displayName string) (map[string]string, error) {
	f.mintCalls = append(f.mintCalls, mintRuntimeEnvCall{
		WorkspaceID: workspaceID,
		ActorUserID: actorUserID,
		InstanceID:  instanceID,
		DisplayName: displayName,
	})
	if f.mintErr != nil {
		return nil, f.mintErr
	}
	if f.mintEnv != nil {
		return f.mintEnv, nil
	}
	env := map[string]string{
		"MULTICA_SERVER_URL":     "http://multica.test",
		"MULTICA_TOKEN":          "tok-" + instanceID,
		"MULTICA_WORKSPACE_ID":   workspaceID,
		"MULTICA_DAEMON_ENABLED": "1",
		"MULTICA_PROFILE":        "sandbox-" + instanceID,
		"MULTICA_DAEMON_ID":      "daemon-" + instanceID,
	}
	if name := strings.TrimSpace(displayName); name != "" {
		env["MULTICA_DAEMON_DEVICE_NAME"] = name
		env["MULTICA_SANDBOX_NAME"] = name
	}
	return env, nil
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

// TestEnvSandboxLifecycleCreateEmitsCanonicalPayloadWithInstanceIDAndMetadata
// verifies that Create's sandboxd job payload carries the canonical key set the
// frontend CreateSandboxInstance handler emits - including instance_id (so the
// in-sandbox daemon can register its runtime with sandbox_instance_id for
// env-dispatch discovery) and metadata - so frontend and env-dispatch create
// jobs are interchangeable. See openspec change env-dispatch-agent-runtime-config
// Task 2 (shared canonical payload).
func TestEnvSandboxLifecycleCreateEmitsCanonicalPayloadWithInstanceIDAndMetadata(t *testing.T) {
	ctx := context.Background()
	deps := &fakeEnvSandboxLifecycleDeps{}
	svc := NewEnvSandboxLifecycleService(deps, 5*time.Second)

	in := CreateSandboxInstanceInput{
		WorkspaceID:   "ws-1",
		NodeID:        "node-1",
		Template:      "python",
		Limits:        json.RawMessage(`{"cpu":2}`),
		Runtime:       json.RawMessage(`{"model":"gpt-test"}`),
		DaemonEnabled: true,
	}
	ref, err := svc.Create(ctx, in, "user-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(deps.jobs) != 1 {
		t.Fatalf("want 1 create job, got %d", len(deps.jobs))
	}
	var payload map[string]any
	if err := json.Unmarshal(deps.jobs[0].Payload, &payload); err != nil {
		t.Fatalf("payload invalid JSON: %v", err)
	}
	for _, key := range []string{"template", "limits", "runtime", "runtime_env", "metadata", "instance_id"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("canonical create payload missing %q: %s", key, string(deps.jobs[0].Payload))
		}
	}
	if payload["instance_id"] != ref.InstanceID {
		t.Fatalf("instance_id = %v, want %s", payload["instance_id"], ref.InstanceID)
	}
}

// TestEnvSandboxLifecycleCreateSurfacesMintedDaemonIDOnRef verifies that Create
// surfaces the minted daemon correlation nonce on ref.DaemonID (== the
// MULTICA_DAEMON_ID injected into the sandbox env) so the pre-create-free
// provisioning path can discover the online runtime by daemon_id. The fake
// mints "daemon-<instanceID>".
func TestEnvSandboxLifecycleCreateSurfacesMintedDaemonIDOnRef(t *testing.T) {
	ctx := context.Background()
	deps := &fakeEnvSandboxLifecycleDeps{}
	svc := NewEnvSandboxLifecycleService(deps, 5*time.Second)
	in := CreateSandboxInstanceInput{
		WorkspaceID: "ws-1", NodeID: "node-1", Template: "python",
		DaemonEnabled: true,
	}
	ref, err := svc.Create(ctx, in, "user-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	wantDaemonID := "daemon-" + ref.InstanceID
	if ref.DaemonID != wantDaemonID {
		t.Fatalf("ref.DaemonID = %q, want %q", ref.DaemonID, wantDaemonID)
	}
}

// TestEnvSandboxLifecycleCreateSurfacesCallerSuppliedDaemonIDOnRef verifies
// that when the caller pre-assigns MULTICA_DAEMON_ID (the legacy pre-create
// path), ref.DaemonID echoes it - so surfacing is non-breaking for the existing
// env-dispatch flow while establishing the contract for the pre-create-free path.
func TestEnvSandboxLifecycleCreateSurfacesCallerSuppliedDaemonIDOnRef(t *testing.T) {
	ctx := context.Background()
	deps := &fakeEnvSandboxLifecycleDeps{}
	svc := NewEnvSandboxLifecycleService(deps, 5*time.Second)
	in := CreateSandboxInstanceInput{
		WorkspaceID: "ws-1", NodeID: "node-1", Template: "python",
		DaemonEnabled: true,
		RuntimeEnv:    map[string]string{"MULTICA_DAEMON_ID": "preassigned-daemon"},
	}
	ref, err := svc.Create(ctx, in, "user-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ref.DaemonID != "preassigned-daemon" {
		t.Fatalf("ref.DaemonID = %q, want preassigned-daemon", ref.DaemonID)
	}
}

// TestEnvSandboxLifecycleCreateLeavesDaemonIDEmptyWhenDaemonDisabled verifies a
// non-daemon sandbox (base/template) gets no daemon correlation nonce.
func TestEnvSandboxLifecycleCreateLeavesDaemonIDEmptyWhenDaemonDisabled(t *testing.T) {
	ctx := context.Background()
	deps := &fakeEnvSandboxLifecycleDeps{}
	svc := NewEnvSandboxLifecycleService(deps, 5*time.Second)
	ref, err := svc.Create(ctx, CreateSandboxInstanceInput{
		WorkspaceID: "ws-1", NodeID: "node-1", Template: "python",
	}, "user-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ref.DaemonID != "" {
		t.Fatalf("ref.DaemonID = %q, want empty for non-daemon sandbox", ref.DaemonID)
	}
}

// TestEnvSandboxLifecycleCreateMintsDaemonEnvWhenEnabled verifies that Create
// mints a daemon bootstrap runtime_env (via MintSandboxRuntimeEnv) when the
// sandbox is flagged DaemonEnabled and no RuntimeEnv was supplied, folds that
// env into the create job payload, and - critically - does NOT place the
// token-bearing env on the returned ref (so it cannot leak into dispatch
// responses or the idempotency ledger).
func TestEnvSandboxLifecycleCreateMintsDaemonEnvWhenEnabled(t *testing.T) {
	ctx := context.Background()
	deps := &fakeEnvSandboxLifecycleDeps{}
	svc := NewEnvSandboxLifecycleService(deps, 5*time.Second)

	in := CreateSandboxInstanceInput{
		WorkspaceID:   "ws-1",
		NodeID:        "node-1",
		Template:      "python",
		Name:          "friendly-sandbox",
		DaemonEnabled: true,
	}
	ref, err := svc.Create(ctx, in, "user-1")

	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	// Mint was called exactly once, keyed to the inserted instance.
	if len(deps.mintCalls) != 1 {
		t.Fatalf("want 1 mint call, got %d", len(deps.mintCalls))
	}
	mc := deps.mintCalls[0]
	if mc.WorkspaceID != "ws-1" || mc.ActorUserID != "user-1" || mc.InstanceID != ref.InstanceID {
		t.Fatalf("unexpected mint call: %+v", mc)
	}
	if mc.DisplayName != "friendly-sandbox" {
		t.Fatalf("DisplayName = %q, want friendly-sandbox", mc.DisplayName)
	}
	// The minted env is folded into the create job payload.
	if len(deps.jobs) != 1 || deps.jobs[0].JobType != "create" {
		t.Fatalf("want one create job, got %+v", deps.jobs)
	}
	var payload map[string]any
	if err := json.Unmarshal(deps.jobs[0].Payload, &payload); err != nil {
		t.Fatalf("payload invalid JSON: %v", err)
	}
	env, ok := payload["runtime_env"].(map[string]any)
	if !ok {
		t.Fatalf("payload missing runtime_env: %s", string(deps.jobs[0].Payload))
	}
	if env["MULTICA_DAEMON_ENABLED"] != "1" {
		t.Fatalf("MULTICA_DAEMON_ENABLED = %v", env["MULTICA_DAEMON_ENABLED"])
	}
	if env["MULTICA_PROFILE"] != "sandbox-"+ref.InstanceID {
		t.Fatalf("MULTICA_PROFILE = %v", env["MULTICA_PROFILE"])
	}
	wantTok := "tok-" + ref.InstanceID
	if env["MULTICA_TOKEN"] != wantTok {
		t.Fatalf("MULTICA_TOKEN = %v, want %v", env["MULTICA_TOKEN"], wantTok)
	}
	wantDaemonID := "daemon-" + ref.InstanceID
	if env["MULTICA_DAEMON_ID"] != wantDaemonID {
		t.Fatalf("MULTICA_DAEMON_ID = %v, want %v", env["MULTICA_DAEMON_ID"], wantDaemonID)
	}
	if env["MULTICA_DAEMON_DEVICE_NAME"] != "friendly-sandbox" {
		t.Fatalf("MULTICA_DAEMON_DEVICE_NAME = %v", env["MULTICA_DAEMON_DEVICE_NAME"])
	}
	if env["MULTICA_SANDBOX_NAME"] != "friendly-sandbox" {
		t.Fatalf("MULTICA_SANDBOX_NAME = %v", env["MULTICA_SANDBOX_NAME"])
	}
	// The token must NOT leak onto the returned ref (it would otherwise be
	// serialized into the rollout response + idempotency ledger).
	refJSON, _ := json.Marshal(ref)
	if strings.Contains(string(refJSON), wantTok) {
		t.Fatalf("minted token leaked onto ref: %s", string(refJSON))
	}
}

// TestEnvSandboxLifecycleCreateSkipsMintWhenDaemonDisabled verifies that a
// non-daemon sandbox (the zero value - e.g. an auto-created base/template env)
// gets no minted runtime_env and no mint call.
func TestEnvSandboxLifecycleCreateSkipsMintWhenDaemonDisabled(t *testing.T) {
	ctx := context.Background()
	deps := &fakeEnvSandboxLifecycleDeps{}
	svc := NewEnvSandboxLifecycleService(deps, 5*time.Second)

	in := CreateSandboxInstanceInput{
		WorkspaceID: "ws-1",
		NodeID:      "node-1",
		Template:    "python",
		// DaemonEnabled left false (base/template sandbox).
	}
	if _, err := svc.Create(ctx, in, "user-1"); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if len(deps.mintCalls) != 0 {
		t.Fatalf("non-daemon sandbox must not mint, got %d calls", len(deps.mintCalls))
	}
	if len(deps.jobs) != 1 {
		t.Fatalf("want one create job, got %d", len(deps.jobs))
	}
	var payload map[string]any
	if err := json.Unmarshal(deps.jobs[0].Payload, &payload); err != nil {
		t.Fatalf("payload invalid JSON: %v", err)
	}
	if _, ok := payload["runtime_env"]; ok {
		t.Fatalf("non-daemon payload must not carry runtime_env: %s", string(deps.jobs[0].Payload))
	}
}

// TestEnvSandboxLifecycleCreateMergesCallerSuppliedEnv verifies that a
// DaemonEnabled sandbox mints the bootstrap env AND overlays caller-supplied
// extras (Phase 2 injects MULTICA_DAEMON_ID this way): minted bootstrap keys +
// token are kept, and the caller's extra is merged in.
func TestEnvSandboxLifecycleCreateMergesCallerSuppliedEnv(t *testing.T) {
	ctx := context.Background()
	deps := &fakeEnvSandboxLifecycleDeps{}
	svc := NewEnvSandboxLifecycleService(deps, 5*time.Second)

	in := CreateSandboxInstanceInput{
		WorkspaceID:   "ws-1",
		NodeID:        "node-1",
		Template:      "python",
		DaemonEnabled: true,
		RuntimeEnv:    map[string]string{"MULTICA_DAEMON_ID": "preassigned-daemon"},
	}
	ref, err := svc.Create(ctx, in, "user-1")
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if len(deps.mintCalls) != 1 {
		t.Fatalf("want 1 mint call (mint + merge, not skip), got %d", len(deps.mintCalls))
	}
	var payload map[string]any
	if err := json.Unmarshal(deps.jobs[0].Payload, &payload); err != nil {
		t.Fatalf("payload invalid JSON: %v", err)
	}
	env, ok := payload["runtime_env"].(map[string]any)
	if !ok {
		t.Fatalf("payload missing runtime_env: %s", string(deps.jobs[0].Payload))
	}
	// Minted bootstrap keys are present.
	if env["MULTICA_DAEMON_ENABLED"] != "1" {
		t.Fatalf("MULTICA_DAEMON_ENABLED = %v", env["MULTICA_DAEMON_ENABLED"])
	}
	if env["MULTICA_PROFILE"] != "sandbox-"+ref.InstanceID {
		t.Fatalf("MULTICA_PROFILE = %v", env["MULTICA_PROFILE"])
	}
	// The minted token is kept (caller did not override it).
	if env["MULTICA_TOKEN"] != "tok-"+ref.InstanceID {
		t.Fatalf("MULTICA_TOKEN = %v", env["MULTICA_TOKEN"])
	}
	// The caller-supplied extra is merged in.
	if env["MULTICA_DAEMON_ID"] != "preassigned-daemon" {
		t.Fatalf("MULTICA_DAEMON_ID = %v", env["MULTICA_DAEMON_ID"])
	}
}

func TestEnvSandboxLifecycleCreateCompensatesPostInsertFailures(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*fakeEnvSandboxLifecycleDeps)
	}{
		{"mint failure", func(f *fakeEnvSandboxLifecycleDeps) { f.mintErr = errors.New("mint") }},
		{"enqueue failure", func(f *fakeEnvSandboxLifecycleDeps) { f.enqueueErr = errors.New("enqueue") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeEnvSandboxLifecycleDeps{}
			tt.configure(f)
			svc := NewEnvSandboxLifecycleService(f, time.Second)
			_, err := svc.Create(context.Background(), CreateSandboxInstanceInput{
				WorkspaceID: "ws", Template: "default", DaemonEnabled: true,
			}, "user")
			if err == nil {
				t.Fatal("expected create failure")
			}
			if len(f.forceDeletes) != 1 || f.forceDeletes[0] != "ws:inst-created" {
				t.Fatalf("force deletes = %v, want [ws:inst-created]", f.forceDeletes)
			}
		})
	}
}

func TestEnvSandboxLifecycleCreateNotificationFailureKeepsDurableJob(t *testing.T) {
	f := &fakeEnvSandboxLifecycleDeps{}
	f.notifyErr = errors.New("websocket unavailable")
	svc := NewEnvSandboxLifecycleService(f, time.Second)
	ref, err := svc.Create(context.Background(), CreateSandboxInstanceInput{
		WorkspaceID: "ws", Template: "default",
	}, "user")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ref.InstanceID != "inst-created" {
		t.Fatalf("instance = %q", ref.InstanceID)
	}
	if len(f.forceDeletes) != 0 {
		t.Fatalf("unexpected compensation: %v", f.forceDeletes)
	}
}
