package service

import (
	"context"
	"encoding/json"
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

type fakeResumeAgentRunner struct {
	calledWith *ResumeTrigger
	err        error
}

func (f *fakeResumeAgentRunner) ResumeAgentRun(_ context.Context, t ResumeTrigger) error {
	f.calledWith = &t
	return f.err
}

// --- tests ---

func TestEnvCheckpointCreateWaitsForSynchronousSaveComplete(t *testing.T) {
	repo := newFakeCheckpointRepo()
	saver := &fakeCheckpointSaver{}
	snapshot := &fakeProjectSnapshotReader{snapshot: json.RawMessage(`{"issues":[]}`)}
	svc := NewEnvCheckpointService(repo, saver, &fakeCheckpointResumer{}, snapshot, &fakeInFlightResolver{}, nil)

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
	svc := NewEnvCheckpointService(repo, saver, &fakeCheckpointResumer{}, snapshot, &fakeInFlightResolver{}, nil)

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
	svc := NewEnvCheckpointService(repo, saver, &fakeCheckpointResumer{}, snapshot, &fakeInFlightResolver{}, nil)

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

	svc := NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, &fakeCheckpointResumer{}, &fakeProjectSnapshotReader{}, &fakeInFlightResolver{}, nil)

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
	svc := NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, &fakeCheckpointResumer{}, &fakeProjectSnapshotReader{}, &fakeInFlightResolver{}, nil)

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
	svc := NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, &fakeCheckpointResumer{}, snapshot, &fakeInFlightResolver{}, nil)

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
	svc := NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, resumer, &fakeProjectSnapshotReader{}, &fakeInFlightResolver{}, nil)

	res, err := svc.ResumeFromCheckpoint(context.Background(), "ws", "cp-1", "u")
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
	svc := NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, resumer, &fakeProjectSnapshotReader{}, &fakeInFlightResolver{}, nil)

	_, err := svc.ResumeFromCheckpoint(context.Background(), "ws", "cp-1", "u")
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
	svc := NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, resumer, &fakeProjectSnapshotReader{}, &fakeInFlightResolver{}, nil)

	_, err := svc.ResumeFromCheckpoint(context.Background(), "ws", "cp-1", "u")
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
	svc := NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, resumer, &fakeProjectSnapshotReader{}, &fakeInFlightResolver{}, nil)

	_, err := svc.ResumeFromCheckpoint(context.Background(), "ws", "missing", "u")
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
	svc := NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, resumer, &fakeProjectSnapshotReader{}, &fakeInFlightResolver{}, nil)

	res, err := svc.ResumeFromCheckpoint(context.Background(), "ws", "cp-1", "u")
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
	svc := NewEnvCheckpointService(repo, saver, resumer, snapshot, &fakeInFlightResolver{triggers: []ResumeTrigger{trigger}}, nil)

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
	svc := NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, &fakeCheckpointResumer{}, &fakeProjectSnapshotReader{snapshot: json.RawMessage(`{}`)}, &fakeInFlightResolver{triggers: nil}, nil)
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
	runner := &fakeResumeAgentRunner{}
	svc := NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, &fakeCheckpointResumer{}, &fakeProjectSnapshotReader{}, &fakeInFlightResolver{}, runner)

	res, err := svc.ResumeFromCheckpoint(context.Background(), "ws", "cp-1", "u")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if runner.calledWith == nil || runner.calledWith.TaskID != "t-1" {
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
	runner := &fakeResumeAgentRunner{}
	svc := NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, &fakeCheckpointResumer{}, &fakeProjectSnapshotReader{}, &fakeInFlightResolver{}, runner)

	res, err := svc.ResumeFromCheckpoint(context.Background(), "ws", "cp-1", "u")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if runner.calledWith != nil {
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
	runner := &fakeResumeAgentRunner{err: ErrTriggerTaskNotResumable}
	svc := NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, &fakeCheckpointResumer{}, &fakeProjectSnapshotReader{}, &fakeInFlightResolver{}, runner)

	res, err := svc.ResumeFromCheckpoint(context.Background(), "ws", "cp-1", "u")
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
	svc := NewEnvCheckpointService(repo, &fakeCheckpointSaver{}, &fakeCheckpointResumer{}, &fakeProjectSnapshotReader{}, &fakeInFlightResolver{}, nil)

	if _, err := svc.ResumeFromCheckpoint(context.Background(), "ws", "cp-1", "u"); err == nil {
		t.Fatal("expected error for non-empty trigger with nil runner")
	}
}
