package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- fakes ---

type fakeCheckpointRepo struct {
	mu          sync.Mutex
	checkpoints map[string]EnvCheckpoint
	nextID      int
	createCalls []createCheckpointCall
	updateCalls []updateCheckpointCall
	listCalls   []listCall
}

type createCheckpointCall struct {
	in      EnvCheckpointCreateInput
	status  EnvCheckpointStatus
	saveErr string
}

type updateCheckpointCall struct {
	checkpointID string
	workspaceID  string
	status       EnvCheckpointStatus
	saveErr      string
}

type listCall struct {
	workspaceID string
	projectID   string
}

func newFakeCheckpointRepo() *fakeCheckpointRepo {
	return &fakeCheckpointRepo{checkpoints: map[string]EnvCheckpoint{}}
}

func (r *fakeCheckpointRepo) CreateCheckpoint(_ context.Context, in EnvCheckpointCreateInput, status EnvCheckpointStatus, saveErr string) (EnvCheckpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	id := fmt.Sprintf("cp-%d", r.nextID)
	cp := EnvCheckpoint{
		ID:            id,
		WorkspaceID:   in.WorkspaceID,
		ProjectID:     in.ProjectID,
		EventRef:      in.EventRef,
		Kind:          in.Kind,
		SaveMode:      in.SaveMode,
		EnvIDMap:      in.EnvIDMap,
		SandboxRefs:   in.SandboxRefs,
		DBSnapshot:    in.DBSnapshot,
		ResumeTrigger: in.ResumeTrigger,
		EntropyScore:  in.EntropyScore,
		SaveTimeoutMs: int(in.SaveTimeout / time.Millisecond),
		SaveStatus:    status,
		SaveError:     saveErr,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	r.checkpoints[id] = cp
	r.createCalls = append(r.createCalls, createCheckpointCall{in, status, saveErr})
	return cp, nil
}

func (r *fakeCheckpointRepo) UpdateCheckpointSaveStatus(_ context.Context, checkpointID, workspaceID string, status EnvCheckpointStatus, saveErr string) (EnvCheckpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp, ok := r.checkpoints[checkpointID]
	if !ok || cp.WorkspaceID != workspaceID {
		return EnvCheckpoint{}, fmt.Errorf("not found")
	}
	cp.SaveStatus = status
	cp.SaveError = saveErr
	cp.UpdatedAt = time.Now()
	r.checkpoints[checkpointID] = cp
	r.updateCalls = append(r.updateCalls, updateCheckpointCall{checkpointID, workspaceID, status, saveErr})
	return cp, nil
}

func (r *fakeCheckpointRepo) GetCheckpoint(_ context.Context, checkpointID, workspaceID string) (EnvCheckpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp, ok := r.checkpoints[checkpointID]
	if !ok || cp.WorkspaceID != workspaceID {
		return EnvCheckpoint{}, fmt.Errorf("not found")
	}
	return cp, nil
}

func (r *fakeCheckpointRepo) DeleteCheckpoint(_ context.Context, checkpointID, workspaceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp, ok := r.checkpoints[checkpointID]
	if !ok || cp.WorkspaceID != workspaceID {
		return fmt.Errorf("not found: checkpoint %q", checkpointID)
	}
	delete(r.checkpoints, checkpointID)
	return nil
}

func (r *fakeCheckpointRepo) ListCheckpoints(_ context.Context, workspaceID, projectID string) ([]EnvCheckpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []EnvCheckpoint
	for _, cp := range r.checkpoints {
		if cp.WorkspaceID == workspaceID && cp.ProjectID == projectID {
			out = append(out, cp)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	r.listCalls = append(r.listCalls, listCall{workspaceID, projectID})
	return out, nil
}

type fakeCheckpointSaver struct {
	calls            []SandboxInstanceRef
	err              error
	blockUntilCancel bool
}

func (f *fakeCheckpointSaver) Save(ctx context.Context, ref SandboxInstanceRef, _ string) error {
	f.calls = append(f.calls, ref)
	if f.err != nil {
		return f.err
	}
	if f.blockUntilCancel {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

type fakeCheckpointResumer struct {
	calls []SandboxInstanceRef
	err   error
}

func (f *fakeCheckpointResumer) Resume(_ context.Context, ref SandboxInstanceRef, _ string) error {
	f.calls = append(f.calls, ref)
	return f.err
}

type fakeProjectSnapshotReader struct {
	snapshot json.RawMessage
	err      error
}

func (f *fakeProjectSnapshotReader) CaptureProjectSnapshot(_ context.Context, _, _ string) (json.RawMessage, error) {
	return f.snapshot, f.err
}

type fakeInFlightResolver struct {
	triggers []ResumeTrigger
	err      error
}

func (f *fakeInFlightResolver) ListInFlightTasksForProject(_ context.Context, _, _ string) ([]ResumeTrigger, error) {
	return f.triggers, f.err
}

type fakeContinuationStrategy struct {
	mode    EnvCheckpointSaveMode
	calls   []ContinuationRequest
	outcome ContinuationOutcome
	err     error
	// errOnCall fails only the given call indexes, so a per-lane failure can be
	// distinguished from a whole-resume failure.
	errOnCall map[int]error
}

func (f *fakeContinuationStrategy) Mode() EnvCheckpointSaveMode { return f.mode }

func (f *fakeContinuationStrategy) ResumeAgentRun(_ context.Context, req ContinuationRequest) (ContinuationOutcome, error) {
	f.calls = append(f.calls, req)
	if err, ok := f.errOnCall[len(f.calls)-1]; ok {
		return ContinuationOutcome{Status: TriggerFailed}, err
	}
	if f.err != nil {
		return ContinuationOutcome{Status: TriggerFailed}, f.err
	}
	if f.outcome.Status == "" {
		return ContinuationOutcome{Status: TriggerExecuted, TaskID: req.Trigger.TaskID}, nil
	}
	return f.outcome, nil
}

type fakeSavepointCreator struct {
	calls    []SandboxInstanceRef
	checkpts []string
	status   string // defaults to "ready"
	err      error
	// produced records what create handed back, so a round-trip test can feed
	// resume the actual savepoints instead of restating them as literals — a
	// literal would keep passing if create and resume disagreed on the ids.
	produced []Savepoint
}

func (f *fakeSavepointCreator) CreateSavepoint(_ context.Context, ref SandboxInstanceRef, checkpointID, _ string) (Savepoint, error) {
	f.calls = append(f.calls, ref)
	f.checkpts = append(f.checkpts, checkpointID)
	if f.err != nil {
		return Savepoint{}, f.err
	}
	status := f.status
	if status == "" {
		status = "ready"
	}
	sp := Savepoint{
		SnapshotID:     fmt.Sprintf("snap-%d", len(f.calls)),
		CubeSnapshotID: fmt.Sprintf("cube-%d", len(f.calls)),
		InstanceID:     ref.InstanceID,
		Status:         status,
	}
	f.produced = append(f.produced, sp)
	return sp, nil
}

// --- tests ---

func TestEnvCheckpointCreateWaitsForSynchronousSaveComplete(t *testing.T) {
	repo := newFakeCheckpointRepo()
	saver := &fakeCheckpointSaver{}
	snapshot := &fakeProjectSnapshotReader{snapshot: json.RawMessage(`{"issues":[]}`)}
	svc := NewEnvCheckpointService(repo, saver, &fakeCheckpointResumer{}, snapshot, &fakeInFlightResolver{}, ContinuationRegistry{})

	cp, err := svc.Create(context.Background(), EnvCheckpointCreateInput{
		WorkspaceID: "ws",
		ProjectID:   "proj",
		EventRef:    "evt-1",
		Kind:        "structural",
		SandboxRefs: []SandboxInstanceRef{
			{InstanceID: "inst-1", WorkspaceID: "ws"},
		},
		ActorUserID: "u",
		SaveTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if cp.SaveStatus != EnvCheckpointSaveComplete {
		t.Fatalf("status = %s, want complete", cp.SaveStatus)
	}
	if len(saver.calls) != 1 {
		t.Fatalf("want 1 save call, got %d", len(saver.calls))
	}
	if len(repo.updateCalls) != 1 {
		t.Fatalf("want 1 status update, got %d", len(repo.updateCalls))
	}
	if repo.updateCalls[0].status != EnvCheckpointSaveComplete {
		t.Fatalf("update status = %s, want complete", repo.updateCalls[0].status)
	}
}

func TestEnvCheckpointCreateRecordsTimeoutStatus(t *testing.T) {
	repo := newFakeCheckpointRepo()
	saver := &fakeCheckpointSaver{blockUntilCancel: true}
	snapshot := &fakeProjectSnapshotReader{snapshot: json.RawMessage(`{}`)}
	svc := NewEnvCheckpointService(repo, saver, &fakeCheckpointResumer{}, snapshot, &fakeInFlightResolver{}, ContinuationRegistry{})

	cp, err := svc.Create(context.Background(), EnvCheckpointCreateInput{
		WorkspaceID: "ws",
		ProjectID:   "proj",
		SandboxRefs: []SandboxInstanceRef{{InstanceID: "inst-1", WorkspaceID: "ws"}},
		ActorUserID: "u",
		SaveTimeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if cp.SaveStatus != EnvCheckpointSaveTimedOut {
		t.Fatalf("status = %s, want timed_out", cp.SaveStatus)
	}
}

func TestEnvCheckpointCreateRecordsSaveFailureStatus(t *testing.T) {
	repo := newFakeCheckpointRepo()
	saver := &fakeCheckpointSaver{err: fmt.Errorf("sandbox crash")}
	snapshot := &fakeProjectSnapshotReader{snapshot: json.RawMessage(`{}`)}
	svc := NewEnvCheckpointService(repo, saver, &fakeCheckpointResumer{}, snapshot, &fakeInFlightResolver{}, ContinuationRegistry{})

	cp, err := svc.Create(context.Background(), EnvCheckpointCreateInput{
		WorkspaceID: "ws",
		ProjectID:   "proj",
		SandboxRefs: []SandboxInstanceRef{{InstanceID: "inst-1", WorkspaceID: "ws"}},
		ActorUserID: "u",
		SaveTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if cp.SaveStatus != EnvCheckpointSaveFailed {
		t.Fatalf("status = %s, want failed", cp.SaveStatus)
	}
	if !strings.Contains(cp.SaveError, "sandbox crash") {
		t.Fatalf("save error should contain 'sandbox crash', got %q", cp.SaveError)
	}
}

func TestEnvCheckpointListNewestFirstAndWorkspaceScoped(t *testing.T) {
	repo := newFakeCheckpointRepo()
	base := time.Now()
	repo.checkpoints["cp-1"] = EnvCheckpoint{ID: "cp-1", WorkspaceID: "ws", ProjectID: "proj", CreatedAt: base.Add(-2 * time.Minute)}
	repo.checkpoints["cp-2"] = EnvCheckpoint{ID: "cp-2", WorkspaceID: "ws", ProjectID: "proj", CreatedAt: base}
	repo.checkpoints["cp-3"] = EnvCheckpoint{ID: "cp-3", WorkspaceID: "other-ws", ProjectID: "proj", CreatedAt: base}

	svc := NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, &fakeCheckpointResumer{}, &fakeProjectSnapshotReader{}, &fakeInFlightResolver{}, ContinuationRegistry{})

	list, err := svc.List(context.Background(), "ws", "proj")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 checkpoints for ws/proj, got %d", len(list))
	}
	if list[0].ID != "cp-2" || list[1].ID != "cp-1" {
		t.Fatalf("expected newest-first [cp-2, cp-1], got [%s, %s]", list[0].ID, list[1].ID)
	}
}

func TestEnvCheckpointCreateRejectsFleetOnlyEnv(t *testing.T) {
	repo := newFakeCheckpointRepo()
	svc := NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, &fakeCheckpointResumer{}, &fakeProjectSnapshotReader{}, &fakeInFlightResolver{}, ContinuationRegistry{})

	_, err := svc.Create(context.Background(), EnvCheckpointCreateInput{
		WorkspaceID: "ws",
		ProjectID:   "proj",
		SandboxRefs: nil, // Fleet-only env: no sandbox_instance refs
		ActorUserID: "u",
		SaveTimeout: 5 * time.Second,
	})
	if err == nil {
		t.Fatalf("expected validation error for Fleet-only env, got nil")
	}
	if !strings.Contains(err.Error(), "validation_failed") {
		t.Fatalf("expected validation_failed error, got %v", err)
	}
	if !strings.Contains(err.Error(), "Fleet-only") {
		t.Fatalf("expected error to mention Fleet-only, got %v", err)
	}
	if len(repo.createCalls) != 0 {
		t.Fatalf("Fleet-only checkpoint must not persist a row, got %d", len(repo.createCalls))
	}
}

func TestEnvCheckpointStoresInlineDBSnapshot(t *testing.T) {
	repo := newFakeCheckpointRepo()
	snapshotJSON := json.RawMessage(`{"issues":[{"id":"i1"}],"sessions":[]}`)
	snapshot := &fakeProjectSnapshotReader{snapshot: snapshotJSON}
	svc := NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, &fakeCheckpointResumer{}, snapshot, &fakeInFlightResolver{}, ContinuationRegistry{})

	cp, err := svc.Create(context.Background(), EnvCheckpointCreateInput{
		WorkspaceID: "ws",
		ProjectID:   "proj",
		SandboxRefs: []SandboxInstanceRef{{InstanceID: "inst-1", WorkspaceID: "ws"}},
		ActorUserID: "u",
		SaveTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if string(cp.DBSnapshot) != string(snapshotJSON) {
		t.Fatalf("snapshot mismatch: got %s, want %s", cp.DBSnapshot, snapshotJSON)
	}
	if len(repo.createCalls) != 1 {
		t.Fatalf("want 1 create call, got %d", len(repo.createCalls))
	}
	if string(repo.createCalls[0].in.DBSnapshot) != string(snapshotJSON) {
		t.Fatalf("create call snapshot mismatch: got %s", repo.createCalls[0].in.DBSnapshot)
	}
}

// --- resume tests ---

func TestResumeFromCheckpointResumesCompletedSandboxRefs(t *testing.T) {
	repo := newFakeCheckpointRepo()
	repo.checkpoints["cp-1"] = EnvCheckpoint{
		ID: "cp-1", WorkspaceID: "ws", ProjectID: "proj",
		SaveStatus: EnvCheckpointSaveComplete,
		SandboxRefs: []SandboxInstanceRef{
			{InstanceID: "inst-1", WorkspaceID: "ws"},
			{InstanceID: "inst-2", WorkspaceID: "ws"},
		},
	}
	resumer := &fakeCheckpointResumer{}
	svc := NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, resumer, &fakeProjectSnapshotReader{}, &fakeInFlightResolver{}, ContinuationRegistry{})

	res, err := resumeOneLane(svc, "cp-1")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if len(resumer.calls) != 2 {
		t.Fatalf("want 2 resume calls, got %d", len(resumer.calls))
	}
	if res.CheckpointID != "cp-1" || res.ProjectID != "proj" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if res.RolloutHandle == "" {
		t.Fatalf("rollout handle must be non-empty")
	}
}

func TestResumeFromCheckpointRejectsTimedOutCheckpoint(t *testing.T) {
	repo := newFakeCheckpointRepo()
	repo.checkpoints["cp-1"] = EnvCheckpoint{
		ID: "cp-1", WorkspaceID: "ws", ProjectID: "proj",
		SaveStatus: EnvCheckpointSaveTimedOut,
	}
	resumer := &fakeCheckpointResumer{}
	svc := NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, resumer, &fakeProjectSnapshotReader{}, &fakeInFlightResolver{}, ContinuationRegistry{})

	_, err := resumeOneLane(svc, "cp-1")
	if err == nil {
		t.Fatalf("expected error for timed_out checkpoint, got nil")
	}
	if !strings.Contains(err.Error(), "validation_failed") {
		t.Fatalf("expected validation_failed error, got %v", err)
	}
	if len(resumer.calls) != 0 {
		t.Fatalf("timed_out checkpoint must not resume any sandboxes, got %d", len(resumer.calls))
	}
}

func TestResumeFromCheckpointRejectsFailedCheckpoint(t *testing.T) {
	repo := newFakeCheckpointRepo()
	repo.checkpoints["cp-1"] = EnvCheckpoint{
		ID: "cp-1", WorkspaceID: "ws", ProjectID: "proj",
		SaveStatus: EnvCheckpointSaveFailed,
	}
	resumer := &fakeCheckpointResumer{}
	svc := NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, resumer, &fakeProjectSnapshotReader{}, &fakeInFlightResolver{}, ContinuationRegistry{})

	_, err := resumeOneLane(svc, "cp-1")
	if err == nil {
		t.Fatalf("expected error for failed checkpoint, got nil")
	}
	if !strings.Contains(err.Error(), "validation_failed") {
		t.Fatalf("expected validation_failed error, got %v", err)
	}
	if len(resumer.calls) != 0 {
		t.Fatalf("failed checkpoint must not resume any sandboxes, got %d", len(resumer.calls))
	}
}

func TestResumeFromCheckpointNotFound(t *testing.T) {
	repo := newFakeCheckpointRepo()
	resumer := &fakeCheckpointResumer{}
	svc := NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, resumer, &fakeProjectSnapshotReader{}, &fakeInFlightResolver{}, ContinuationRegistry{})

	_, err := resumeOneLane(svc, "missing")
	if err == nil {
		t.Fatalf("expected error for missing checkpoint, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found error, got %v", err)
	}
	if len(resumer.calls) != 0 {
		t.Fatalf("missing checkpoint must not resume any sandboxes, got %d", len(resumer.calls))
	}
}

func TestResumeFromCheckpointPreservesPerAgentSandboxRefs(t *testing.T) {
	repo := newFakeCheckpointRepo()
	refs := []SandboxInstanceRef{
		{InstanceID: "inst-a1", WorkspaceID: "ws", Template: "python"},
		{InstanceID: "inst-a2", WorkspaceID: "ws", Template: "node"},
	}
	repo.checkpoints["cp-1"] = EnvCheckpoint{
		ID: "cp-1", WorkspaceID: "ws", ProjectID: "proj",
		SaveStatus:  EnvCheckpointSaveComplete,
		SandboxRefs: refs,
		EnvIDMap:    map[string]string{"a1": "env-1", "a2": "env-2"},
	}
	resumer := &fakeCheckpointResumer{}
	svc := NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, resumer, &fakeProjectSnapshotReader{}, &fakeInFlightResolver{}, ContinuationRegistry{})

	res, err := resumeOneLane(svc, "cp-1")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if len(res.SandboxRefs) != 2 {
		t.Fatalf("want 2 sandbox refs in result, got %d", len(res.SandboxRefs))
	}
	if res.SandboxRefs[0].InstanceID != "inst-a1" || res.SandboxRefs[1].InstanceID != "inst-a2" {
		t.Fatalf("sandbox refs not preserved: %+v", res.SandboxRefs)
	}
	if res.EnvIDMap["a1"] != "env-1" || res.EnvIDMap["a2"] != "env-2" {
		t.Fatalf("env id map not preserved: %+v", res.EnvIDMap)
	}
}

func TestEnvCheckpointCreateResolvesResumeTriggerFromInFlightTask(t *testing.T) {
	repo := newFakeCheckpointRepo()
	saver := &fakeCheckpointSaver{}
	resumer := &fakeCheckpointResumer{}
	snapshot := &fakeProjectSnapshotReader{snapshot: json.RawMessage(`{}`)}
	trigger := ResumeTrigger{TaskID: "t-1", RuntimeID: "r-1", AgentID: "a-1", IssueID: "i-1", ProjectID: "p-1", Kind: "issue"}
	svc := NewEnvCheckpointService(repo, saver, resumer, snapshot, &fakeInFlightResolver{triggers: []ResumeTrigger{trigger}}, ContinuationRegistry{})

	cp, err := svc.Create(context.Background(), EnvCheckpointCreateInput{
		WorkspaceID: "ws",
		ProjectID:   "p-1",
		EventRef:    "e",
		Kind:        "always",
		SandboxRefs: []SandboxInstanceRef{{InstanceID: "s-1", WorkspaceID: "ws"}},
		ActorUserID: "u",
		SaveTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(cp.ResumeTrigger) == 0 {
		t.Fatalf("resume_trigger not resolved: %s", cp.ResumeTrigger)
	}
	var rt ResumeTrigger
	if err := json.Unmarshal(cp.ResumeTrigger, &rt); err != nil {
		t.Fatalf("unmarshal resume_trigger: %v", err)
	}
	if rt.TaskID != "t-1" {
		t.Fatalf("resume_trigger task_id = %q, want t-1", rt.TaskID)
	}
	if len(repo.createCalls) != 1 || len(repo.createCalls[0].in.ResumeTrigger) == 0 {
		t.Fatal("repo did not receive resume_trigger")
	}
}

func TestEnvCheckpointCreateEmptyResumeTriggerWhenNoInFlightTask(t *testing.T) {
	repo := newFakeCheckpointRepo()
	svc := NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, &fakeCheckpointResumer{}, &fakeProjectSnapshotReader{snapshot: json.RawMessage(`{}`)}, &fakeInFlightResolver{triggers: nil}, ContinuationRegistry{})
	cp, err := svc.Create(context.Background(), EnvCheckpointCreateInput{
		WorkspaceID: "ws",
		ProjectID:   "p-1",
		EventRef:    "e",
		Kind:        "always",
		SandboxRefs: []SandboxInstanceRef{{InstanceID: "s-1", WorkspaceID: "ws"}},
		ActorUserID: "u",
		SaveTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(cp.ResumeTrigger) != 0 {
		t.Fatalf("expected empty resume_trigger, got %s", cp.ResumeTrigger)
	}
}

func TestResumeFromCheckpointExecutesTriggerAfterSandboxResume(t *testing.T) {
	repo := newFakeCheckpointRepo()
	repo.checkpoints["cp-1"] = EnvCheckpoint{
		ID: "cp-1", WorkspaceID: "ws", SaveStatus: EnvCheckpointSaveComplete,
		SandboxRefs:   []SandboxInstanceRef{{InstanceID: "s-1", WorkspaceID: "ws"}},
		ResumeTrigger: json.RawMessage(`{"task_id":"t-1","runtime_id":"r-1","agent_id":"a-1","project_id":"p-1","kind":"issue"}`),
	}
	runner := &fakeContinuationStrategy{mode: SaveModePauseInPlace}
	svc := NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, &fakeCheckpointResumer{}, &fakeProjectSnapshotReader{}, &fakeInFlightResolver{}, ContinuationRegistry{SameRuntime: runner})

	res, err := resumeOneLane(svc, "cp-1")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if len(runner.calls) == 0 || runner.calls[0].Trigger.TaskID != "t-1" {
		t.Fatal("trigger not executed")
	}
	if res.TriggerStatus != TriggerExecuted {
		t.Fatalf("status=%v want executed", res.TriggerStatus)
	}
}

func TestResumeFromCheckpointSkipsLegacyEmptyTrigger(t *testing.T) {
	repo := newFakeCheckpointRepo()
	repo.checkpoints["cp-1"] = EnvCheckpoint{
		ID: "cp-1", WorkspaceID: "ws", SaveStatus: EnvCheckpointSaveComplete,
		SandboxRefs:   []SandboxInstanceRef{{InstanceID: "s-1", WorkspaceID: "ws"}},
		ResumeTrigger: nil,
	}
	runner := &fakeContinuationStrategy{mode: SaveModePauseInPlace}
	svc := NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, &fakeCheckpointResumer{}, &fakeProjectSnapshotReader{}, &fakeInFlightResolver{}, ContinuationRegistry{SameRuntime: runner})

	res, err := resumeOneLane(svc, "cp-1")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatal("legacy trigger should not execute")
	}
	if res.TriggerStatus != TriggerSkippedLegacy {
		t.Fatalf("status=%v want skipped_legacy", res.TriggerStatus)
	}
}

func TestResumeFromCheckpointTriggerFailureIsPartialResume(t *testing.T) {
	repo := newFakeCheckpointRepo()
	repo.checkpoints["cp-1"] = EnvCheckpoint{
		ID: "cp-1", WorkspaceID: "ws", SaveStatus: EnvCheckpointSaveComplete,
		SandboxRefs:   []SandboxInstanceRef{{InstanceID: "s-1", WorkspaceID: "ws"}},
		ResumeTrigger: json.RawMessage(`{"task_id":"t-1","runtime_id":"r-1","kind":"issue"}`),
	}
	runner := &fakeContinuationStrategy{mode: SaveModePauseInPlace, err: ErrTriggerTaskNotResumable}
	svc := NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, &fakeCheckpointResumer{}, &fakeProjectSnapshotReader{}, &fakeInFlightResolver{}, ContinuationRegistry{SameRuntime: runner})

	res, err := resumeOneLane(svc, "cp-1")
	if err == nil {
		t.Fatal("expected partial-resume error")
	}
	if res.TriggerStatus != TriggerFailed {
		t.Fatalf("status=%v want failed", res.TriggerStatus)
	}
}

func TestResumeFromCheckpointRejectsTriggerWithoutRunner(t *testing.T) {
	repo := newFakeCheckpointRepo()
	repo.checkpoints["cp-1"] = EnvCheckpoint{
		ID: "cp-1", WorkspaceID: "ws", SaveStatus: EnvCheckpointSaveComplete,
		SandboxRefs:   []SandboxInstanceRef{{InstanceID: "s-1", WorkspaceID: "ws"}},
		ResumeTrigger: json.RawMessage(`{"task_id":"t-1","kind":"issue"}`),
	}
	svc := NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, &fakeCheckpointResumer{}, &fakeProjectSnapshotReader{}, &fakeInFlightResolver{}, ContinuationRegistry{})

	if _, err := resumeOneLane(svc, "cp-1"); err == nil {
		t.Fatal("expected error for non-empty trigger with nil runner")
	}
}

func TestResumeSelectsSameRuntimeStrategyForPauseInPlace(t *testing.T) {
	repo := newFakeCheckpointRepo()
	repo.checkpoints["cp-1"] = EnvCheckpoint{
		ID: "cp-1", WorkspaceID: "ws", SaveStatus: EnvCheckpointSaveComplete,
		SaveMode:      SaveModePauseInPlace,
		SandboxRefs:   []SandboxInstanceRef{{InstanceID: "s-1", WorkspaceID: "ws"}},
		ResumeTrigger: json.RawMessage(`{"task_id":"t-1","runtime_id":"r-1","kind":"issue"}`),
	}
	same := &fakeContinuationStrategy{mode: SaveModePauseInPlace}
	forked := &fakeContinuationStrategy{mode: SaveModeSnapshot}
	svc := NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, &fakeCheckpointResumer{},
		&fakeProjectSnapshotReader{}, &fakeInFlightResolver{},
		ContinuationRegistry{SameRuntime: same, Forked: forked})

	res, err := resumeOneLane(svc, "cp-1")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if len(same.calls) != 1 {
		t.Fatalf("same-runtime strategy calls = %d, want 1", len(same.calls))
	}
	if len(forked.calls) != 0 {
		t.Fatalf("forked strategy must not be invoked for pause_in_place, got %d", len(forked.calls))
	}
	if res.TriggerStatus != TriggerExecuted {
		t.Fatalf("trigger status = %s, want executed", res.TriggerStatus)
	}
}

// A snapshot-mode checkpoint left its source instances running, so resuming
// those instances would be meaningless at best and would disturb live work at
// worst. Its resume must leave the sources alone and never reach the
// same-runtime strategy, no matter how the lane path is implemented. Task 3's
// lane path adds the positive assertion that each lane reaches the forked
// strategy.
func TestSnapshotResumeNeverTakesThePauseInPlacePath(t *testing.T) {
	repo := newFakeCheckpointRepo()
	repo.checkpoints["cp-1"] = EnvCheckpoint{
		ID: "cp-1", WorkspaceID: "ws", SaveStatus: EnvCheckpointSaveComplete,
		SaveMode:      SaveModeSnapshot,
		SandboxRefs:   []SandboxInstanceRef{{InstanceID: "s-1", WorkspaceID: "ws"}},
		ResumeTrigger: json.RawMessage(`{"task_id":"t-1","runtime_id":"r-1","kind":"issue"}`),
	}
	same := &fakeContinuationStrategy{mode: SaveModePauseInPlace}
	forked := &fakeContinuationStrategy{mode: SaveModeSnapshot}
	resumer := &fakeCheckpointResumer{}
	svc := NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, resumer,
		&fakeProjectSnapshotReader{}, &fakeInFlightResolver{},
		ContinuationRegistry{SameRuntime: same, Forked: forked})

	_, _ = resumeOneLane(svc, "cp-1")

	if len(resumer.calls) != 0 {
		t.Fatalf("snapshot resume must not resume its still-running sources, got %d", len(resumer.calls))
	}
	if len(same.calls) != 0 {
		t.Fatalf("same-runtime strategy must not be invoked for snapshot, got %d", len(same.calls))
	}
}

func TestResumeDefaultsToSameRuntimeStrategyForLegacyRowsWithoutSaveMode(t *testing.T) {
	repo := newFakeCheckpointRepo()
	repo.checkpoints["cp-1"] = EnvCheckpoint{
		ID: "cp-1", WorkspaceID: "ws", SaveStatus: EnvCheckpointSaveComplete,
		SaveMode:      "", // pre-change row
		SandboxRefs:   []SandboxInstanceRef{{InstanceID: "s-1", WorkspaceID: "ws"}},
		ResumeTrigger: json.RawMessage(`{"task_id":"t-1","runtime_id":"r-1","kind":"issue"}`),
	}
	same := &fakeContinuationStrategy{mode: SaveModePauseInPlace}
	svc := NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, &fakeCheckpointResumer{},
		&fakeProjectSnapshotReader{}, &fakeInFlightResolver{},
		ContinuationRegistry{SameRuntime: same})

	if _, err := resumeOneLane(svc, "cp-1"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if len(same.calls) != 1 {
		t.Fatalf("legacy row must use same-runtime strategy, calls = %d", len(same.calls))
	}
}

func TestResumeReportsSkippedWhenNoContinuationDescriptor(t *testing.T) {
	repo := newFakeCheckpointRepo()
	repo.checkpoints["cp-1"] = EnvCheckpoint{
		ID: "cp-1", WorkspaceID: "ws", SaveStatus: EnvCheckpointSaveComplete,
		SaveMode:    SaveModePauseInPlace,
		SandboxRefs: []SandboxInstanceRef{{InstanceID: "s-1", WorkspaceID: "ws"}},
	}
	same := &fakeContinuationStrategy{mode: SaveModePauseInPlace}
	svc := NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, &fakeCheckpointResumer{},
		&fakeProjectSnapshotReader{}, &fakeInFlightResolver{},
		ContinuationRegistry{SameRuntime: same})

	res, err := resumeOneLane(svc, "cp-1")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if res.TriggerStatus != TriggerSkippedLegacy {
		t.Fatalf("status = %s, want skipped_legacy", res.TriggerStatus)
	}
	if len(same.calls) != 0 {
		t.Fatalf("no strategy may be invoked without a descriptor, got %d", len(same.calls))
	}
}

// The reported trigger status must come from the strategy rather than being
// hardcoded to "executed" once the strategy returns without error.
func TestResumeReportsStrategyOutcomeStatus(t *testing.T) {
	repo := newFakeCheckpointRepo()
	repo.checkpoints["cp-1"] = EnvCheckpoint{
		ID: "cp-1", WorkspaceID: "ws", SaveStatus: EnvCheckpointSaveComplete,
		SaveMode:      SaveModePauseInPlace,
		SandboxRefs:   []SandboxInstanceRef{{InstanceID: "s-1", WorkspaceID: "ws"}},
		ResumeTrigger: json.RawMessage(`{"task_id":"t-1","runtime_id":"r-1","kind":"issue"}`),
	}
	same := &fakeContinuationStrategy{
		mode:    SaveModePauseInPlace,
		outcome: ContinuationOutcome{Status: TriggerSkippedLegacy, LaneKey: "lane-1"},
	}
	svc := NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, &fakeCheckpointResumer{},
		&fakeProjectSnapshotReader{}, &fakeInFlightResolver{},
		ContinuationRegistry{SameRuntime: same})

	res, err := resumeOneLane(svc, "cp-1")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if res.TriggerStatus != TriggerSkippedLegacy {
		t.Fatalf("status = %s, want the strategy's reported outcome", res.TriggerStatus)
	}
}

// resumeOneLane is the single-lane resume every pre-existing test performs, kept
// in one place so the request shape lives in one spot rather than fourteen.
func resumeOneLane(svc *EnvCheckpointService, checkpointID string) (ResumeFromCheckpointResult, error) {
	return svc.ResumeFromCheckpoint(context.Background(), ResumeFromCheckpointInput{
		WorkspaceID:   "ws",
		CheckpointID:  checkpointID,
		ActorUserID:   "u",
		LaneCount:     1,
		LaneKeyAnchor: checkpointID,
	})
}

func newSnapshotModeService(repo *fakeCheckpointRepo, saver *fakeCheckpointSaver, creator *fakeSavepointCreator) *EnvCheckpointService {
	return NewEnvCheckpointService(repo, saver, &fakeCheckpointResumer{},
		&fakeProjectSnapshotReader{snapshot: json.RawMessage(`{}`)},
		&fakeInFlightResolver{}, ContinuationRegistry{}).WithSavepointCreator(creator)
}

func TestSnapshotModeCreateOwnsReadySavepointAndLeavesSourceRunning(t *testing.T) {
	repo := newFakeCheckpointRepo()
	saver := &fakeCheckpointSaver{}
	creator := &fakeSavepointCreator{}
	svc := newSnapshotModeService(repo, saver, creator)

	cp, err := svc.Create(context.Background(), EnvCheckpointCreateInput{
		WorkspaceID: "ws", ProjectID: "proj", SaveMode: SaveModeSnapshot,
		SandboxRefs: []SandboxInstanceRef{
			{InstanceID: "inst-1", WorkspaceID: "ws"},
			{InstanceID: "inst-2", WorkspaceID: "ws"},
		},
		ActorUserID: "u", SaveTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if cp.SaveStatus != EnvCheckpointSaveComplete {
		t.Fatalf("status = %s, want complete", cp.SaveStatus)
	}
	if len(creator.calls) != 2 {
		t.Fatalf("want one savepoint per source instance (2), got %d", len(creator.calls))
	}
	if len(saver.calls) != 0 {
		t.Fatalf("snapshot mode must not stop any source instance, got %d stops", len(saver.calls))
	}
	for _, id := range creator.checkpts {
		if id != cp.ID {
			t.Fatalf("savepoint owner = %q, want %q", id, cp.ID)
		}
	}
}

func TestSnapshotModeCreateFailsWhenSavepointReachesFailed(t *testing.T) {
	repo := newFakeCheckpointRepo()
	svc := newSnapshotModeService(repo, &fakeCheckpointSaver{}, &fakeSavepointCreator{status: "failed"})

	cp, err := svc.Create(context.Background(), EnvCheckpointCreateInput{
		WorkspaceID: "ws", ProjectID: "proj", SaveMode: SaveModeSnapshot,
		SandboxRefs: []SandboxInstanceRef{{InstanceID: "inst-1", WorkspaceID: "ws"}},
		ActorUserID: "u", SaveTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("create should record the failure, not return it: %v", err)
	}
	if cp.SaveStatus != EnvCheckpointSaveFailed {
		t.Fatalf("status = %s, want failed", cp.SaveStatus)
	}
	if !strings.Contains(cp.SaveError, "savepoint_failed") {
		t.Fatalf("save error = %q, want it to name the savepoint failure", cp.SaveError)
	}
}

func TestSnapshotModeCreateRecordsTimeoutStatus(t *testing.T) {
	repo := newFakeCheckpointRepo()
	svc := newSnapshotModeService(repo, &fakeCheckpointSaver{}, &fakeSavepointCreator{err: context.DeadlineExceeded})

	cp, err := svc.Create(context.Background(), EnvCheckpointCreateInput{
		WorkspaceID: "ws", ProjectID: "proj", SaveMode: SaveModeSnapshot,
		SandboxRefs: []SandboxInstanceRef{{InstanceID: "inst-1", WorkspaceID: "ws"}},
		ActorUserID: "u", SaveTimeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if cp.SaveStatus != EnvCheckpointSaveTimedOut {
		t.Fatalf("status = %s, want timed_out", cp.SaveStatus)
	}
}

func TestSnapshotModeCreateRejectedWithoutSavepointCreator(t *testing.T) {
	repo := newFakeCheckpointRepo()
	svc := NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, &fakeCheckpointResumer{},
		&fakeProjectSnapshotReader{snapshot: json.RawMessage(`{}`)},
		&fakeInFlightResolver{}, ContinuationRegistry{})

	_, err := svc.Create(context.Background(), EnvCheckpointCreateInput{
		WorkspaceID: "ws", ProjectID: "proj", SaveMode: SaveModeSnapshot,
		SandboxRefs: []SandboxInstanceRef{{InstanceID: "inst-1", WorkspaceID: "ws"}},
		ActorUserID: "u", SaveTimeout: 5 * time.Second,
	})
	if err == nil {
		t.Fatal("snapshot mode without a savepoint creator must be refused, not silently downgraded")
	}
	if !strings.Contains(err.Error(), "validation_failed") {
		t.Fatalf("error = %v, want a validation_failed rejection", err)
	}
	if len(repo.createCalls) != 0 {
		t.Fatalf("refusal must happen before persisting a checkpoint, got %d creates", len(repo.createCalls))
	}
}

func TestCreateRejectsUnknownSaveMode(t *testing.T) {
	repo := newFakeCheckpointRepo()
	svc := newSnapshotModeService(repo, &fakeCheckpointSaver{}, &fakeSavepointCreator{})

	_, err := svc.Create(context.Background(), EnvCheckpointCreateInput{
		WorkspaceID: "ws", ProjectID: "proj", SaveMode: EnvCheckpointSaveMode("bogus"),
		SandboxRefs: []SandboxInstanceRef{{InstanceID: "inst-1", WorkspaceID: "ws"}},
		ActorUserID: "u", SaveTimeout: 5 * time.Second,
	})
	if err == nil {
		t.Fatal("an unknown save mode must be refused before it can reach the CHECK constraint")
	}
	if !strings.Contains(err.Error(), "validation_failed") {
		t.Fatalf("error = %v, want a validation_failed rejection", err)
	}
}

func TestPauseInPlaceCreateStaysOnTheStopPath(t *testing.T) {
	repo := newFakeCheckpointRepo()
	saver := &fakeCheckpointSaver{}
	creator := &fakeSavepointCreator{}
	// SaveMode omitted, so it must normalize to pause_in_place.
	svc := newSnapshotModeService(repo, saver, creator)

	cp, err := svc.Create(context.Background(), EnvCheckpointCreateInput{
		WorkspaceID: "ws", ProjectID: "proj",
		SandboxRefs: []SandboxInstanceRef{{InstanceID: "inst-1", WorkspaceID: "ws"}},
		ActorUserID: "u", SaveTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if cp.SaveMode != SaveModePauseInPlace {
		t.Fatalf("save mode = %q, want pause_in_place", cp.SaveMode)
	}
	if len(saver.calls) != 1 {
		t.Fatalf("pause_in_place must stop the source, stops = %d", len(saver.calls))
	}
	if len(creator.calls) != 0 {
		t.Fatalf("pause_in_place must take no savepoint, got %d", len(creator.calls))
	}
}

func newResumeService(repo *fakeCheckpointRepo, resumer *fakeCheckpointResumer) *EnvCheckpointService {
	return NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, resumer,
		&fakeProjectSnapshotReader{}, &fakeInFlightResolver{}, ContinuationRegistry{})
}

func putResumableCheckpoint(repo *fakeCheckpointRepo, mode EnvCheckpointSaveMode, status EnvCheckpointStatus, refs ...SandboxInstanceRef) {
	if len(refs) == 0 {
		refs = []SandboxInstanceRef{{InstanceID: "inst-1", WorkspaceID: "ws"}}
	}
	repo.checkpoints["cp-1"] = EnvCheckpoint{
		ID: "cp-1", WorkspaceID: "ws", ProjectID: "proj",
		SaveMode: mode, SaveStatus: status, SandboxRefs: refs,
	}
}

func TestResumeRejectsZeroLaneCount(t *testing.T) {
	repo := newFakeCheckpointRepo()
	putResumableCheckpoint(repo, SaveModeSnapshot, EnvCheckpointSaveComplete)
	resumer := &fakeCheckpointResumer{}
	svc := newResumeService(repo, resumer)

	_, err := svc.ResumeFromCheckpoint(context.Background(), ResumeFromCheckpointInput{
		WorkspaceID: "ws", CheckpointID: "cp-1", ActorUserID: "u",
		LaneCount: 0, LaneKeyAnchor: "anchor",
	})
	if !errors.Is(err, ErrLaneCountInvalid) {
		t.Fatalf("expected ErrLaneCountInvalid, got %v", err)
	}
	if len(resumer.calls) != 0 {
		t.Fatalf("zero lane count must create nothing, resume calls = %d", len(resumer.calls))
	}
}

func TestResumeRejectsFanOutForPauseInPlace(t *testing.T) {
	repo := newFakeCheckpointRepo()
	putResumableCheckpoint(repo, SaveModePauseInPlace, EnvCheckpointSaveComplete)
	resumer := &fakeCheckpointResumer{}
	svc := newResumeService(repo, resumer)

	_, err := svc.ResumeFromCheckpoint(context.Background(), ResumeFromCheckpointInput{
		WorkspaceID: "ws", CheckpointID: "cp-1", ActorUserID: "u",
		LaneCount: 3, LaneKeyAnchor: "anchor",
	})
	if !errors.Is(err, ErrLaneCountInvalid) {
		t.Fatalf("expected ErrLaneCountInvalid for pause_in_place fan-out, got %v", err)
	}
	if len(resumer.calls) != 0 {
		t.Fatalf("rejected fan-out must not resume anything, got %d", len(resumer.calls))
	}
}

func TestResumeRejectsTimedOutCheckpointWithTypedError(t *testing.T) {
	repo := newFakeCheckpointRepo()
	putResumableCheckpoint(repo, SaveModeSnapshot, EnvCheckpointSaveTimedOut)
	svc := newResumeService(repo, &fakeCheckpointResumer{})

	_, err := svc.ResumeFromCheckpoint(context.Background(), ResumeFromCheckpointInput{
		WorkspaceID: "ws", CheckpointID: "cp-1", ActorUserID: "u",
		LaneCount: 1, LaneKeyAnchor: "a",
	})
	if !errors.Is(err, ErrCheckpointNotResumable) {
		t.Fatalf("expected ErrCheckpointNotResumable, got %v", err)
	}
	// A non-resumable checkpoint is a permanent state, so it must be
	// distinguishable from a transient failure.
	if errors.Is(err, ErrLaneCountInvalid) {
		t.Fatal("not-resumable must not be conflated with invalid lane count")
	}
}

func TestPauseInPlaceLaneCountOneResumesSameInstances(t *testing.T) {
	repo := newFakeCheckpointRepo()
	putResumableCheckpoint(repo, SaveModePauseInPlace, EnvCheckpointSaveComplete,
		SandboxInstanceRef{InstanceID: "inst-1", WorkspaceID: "ws"},
		SandboxInstanceRef{InstanceID: "inst-2", WorkspaceID: "ws"})
	resumer := &fakeCheckpointResumer{}
	svc := newResumeService(repo, resumer)

	res, err := svc.ResumeFromCheckpoint(context.Background(), ResumeFromCheckpointInput{
		WorkspaceID: "ws", CheckpointID: "cp-1", ActorUserID: "u",
		LaneCount: 1, LaneKeyAnchor: "a",
	})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if len(resumer.calls) != 2 {
		t.Fatalf("pause_in_place must resume both instances, got %d", len(resumer.calls))
	}
	if len(res.Lanes) != 0 {
		t.Fatalf("pause_in_place resume must report no lanes, got %d", len(res.Lanes))
	}
}

func TestLegacyCheckpointWithoutSaveModeResumesInPlace(t *testing.T) {
	repo := newFakeCheckpointRepo()
	// An empty save mode is a pre-change row: it must resume in place, and it
	// must not be treated as a snapshot capable of fanning out.
	putResumableCheckpoint(repo, "", EnvCheckpointSaveComplete)
	resumer := &fakeCheckpointResumer{}
	svc := newResumeService(repo, resumer)

	if _, err := resumeOneLane(svc, "cp-1"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if len(resumer.calls) != 1 {
		t.Fatalf("legacy checkpoint must resume its instance, got %d", len(resumer.calls))
	}

	_, err := svc.ResumeFromCheckpoint(context.Background(), ResumeFromCheckpointInput{
		WorkspaceID: "ws", CheckpointID: "cp-1", ActorUserID: "u",
		LaneCount: 2, LaneKeyAnchor: "a",
	})
	if !errors.Is(err, ErrLaneCountInvalid) {
		t.Fatalf("a legacy checkpoint must refuse fan-out, got %v", err)
	}
}
